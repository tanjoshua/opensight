package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"

	"go.temporal.io/sdk/temporal"
)

// AnalyzeResultInput identifies one succeeded result to analyze. The account is
// resolved by the parent workflow (AnalyzeRun).
type AnalyzeResultInput struct {
	AccountID domain.ID `json:"TenantID"`
	ResultID  domain.ID
}

// AnalyzeResultOutput is the extraction outcome. Analyzed is false when the
// result failed validation after MaxExtractionAttempts: no result_analyses row
// is written and the result is excluded from metrics (design 02/05). Entities
// is the ordered entity list for phase 2, each already attributed to the
// citation backing the text that names it (empty when !Analyzed).
type AnalyzeResultOutput struct {
	ResultID domain.ID
	Analyzed bool
	Entities []llm.AttributedEntity
}

// AnalyzeResult runs one structured-output extraction call per succeeded result
// with deterministic validation, then writes result_analyses + citations
// (design 05, "Phase 1 — AnalyzeResult"). It writes no mention facts. It always
// re-extracts (never short-circuits on an existing row) so a re-analysis pass
// can rewrite onto a bumped extraction_version.
func (a *Activities) AnalyzeResult(ctx context.Context, in AnalyzeResultInput) (AnalyzeResultOutput, error) {
	detail, err := a.Store.GetResultDetail(ctx, in.AccountID, in.ResultID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return AnalyzeResultOutput{}, temporal.NewNonRetryableApplicationError(
				"load result detail", "BadResult", err)
		}
		return AnalyzeResultOutput{}, err
	}
	if detail.Result.Status != store.ResultStatusSucceeded {
		// A non-succeeded result never becomes succeeded on retry.
		return AnalyzeResultOutput{}, temporal.NewNonRetryableApplicationError(
			"result is not succeeded", "BadResult",
			fmt.Errorf("result %s has status %q", in.ResultID, detail.Result.Status))
	}
	if detail.Result.ResponseText == nil || strings.TrimSpace(*detail.Result.ResponseText) == "" {
		return AnalyzeResultOutput{}, temporal.NewNonRetryableApplicationError(
			"succeeded result has no response text", "BadResult",
			fmt.Errorf("result %s has no response_text", in.ResultID))
	}
	responseText := *detail.Result.ResponseText

	business, err := a.Store.GetBusiness(ctx, in.AccountID, detail.BusinessID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return AnalyzeResultOutput{}, temporal.NewNonRetryableApplicationError(
				"load business", "BadResult", err)
		}
		return AnalyzeResultOutput{}, err
	}

	annotations, err := llm.ParseCitationAnnotations(detail.Result.RawResponse)
	if err != nil {
		// A malformed raw_response is a data bug, not a transient failure.
		return AnalyzeResultOutput{}, temporal.NewNonRetryableApplicationError(
			"parse citation annotations", "BadResult", err)
	}

	category := ""
	if business.Category != nil {
		category = *business.Category
	}

	extIn := llm.ExtractionInput{
		ResponseText:     responseText,
		Prompt:           detail.Prompt.Text,
		BusinessName:     business.Name,
		BusinessAliases:  business.Aliases,
		BusinessCategory: category,
		BusinessLocation: locationSummary(business.Location),
		Citations:        annotations,
	}

	result, err := llm.ExtractWithRetry(ctx, a.Extractor, extIn, responseText, annotations)
	if err != nil {
		return AnalyzeResultOutput{}, err
	}
	if !result.Analyzed {
		actLogger(ctx).Warn("extraction flagged after retries; no analysis written",
			"result_id", in.ResultID.String(),
			"validation_errors", result.ValidationErrs,
		)
		// Clear any stale result_analyses row from a prior successful analysis
		// (e.g. a ReanalyzeRun whose new extraction attempt failed): a failed
		// analysis must never leave old sentiment/keywords counting toward
		// metrics under a result that today has no valid analysis (design 05).
		if err := a.Store.DeleteResultAnalysis(ctx, in.AccountID, in.ResultID); err != nil {
			return AnalyzeResultOutput{}, err
		}
		return AnalyzeResultOutput{ResultID: in.ResultID, Analyzed: false}, nil
	}
	output := result.Output
	model := result.Model

	// One attribution pass serves both writes: the spans order the citation rows
	// and place each entity against the source the answer cited for it.
	spans := llm.AttributeCitations(responseText, annotations)
	citations, err := buildCitationWrites(output.Citations, spans)
	if err != nil {
		return AnalyzeResultOutput{}, temporal.NewNonRetryableApplicationError(
			"build citations", "BadResult", err)
	}

	var sentiment *string
	keywords := []string{}
	excerpts := []string{}
	if output.Target != nil {
		s := output.Target.Sentiment
		sentiment = &s
		if output.Target.Keywords != nil {
			keywords = output.Target.Keywords
		}
		if output.Target.Excerpts != nil {
			excerpts = output.Target.Excerpts
		}
	}

	if err := a.Store.SaveResultAnalysis(ctx, in.AccountID, store.SaveResultAnalysisParams{
		PromptResultID:    in.ResultID,
		Sentiment:         sentiment,
		Keywords:          keywords,
		Excerpts:          excerpts,
		AnalysisModel:     model,
		ExtractionVersion: llm.ExtractionPromptVersion,
		Citations:         citations,
	}); err != nil {
		return AnalyzeResultOutput{}, err
	}

	return AnalyzeResultOutput{
		ResultID: in.ResultID,
		Analyzed: true,
		Entities: llm.AttributeEntities(responseText, output.Entities, spans),
	}, nil
}

// locationSummary renders a "City, Country" hint for the extraction prompt from
// businesses.location. It reuses LocationFromBusinessJSON (the same parser
// LoadRunSpec uses); a location that fails to parse yields an empty summary
// rather than failing analysis, since it is only model context.
func locationSummary(raw json.RawMessage) string {
	loc, err := llm.LocationFromBusinessJSON(raw)
	if err != nil {
		return ""
	}
	if loc.City != "" {
		return loc.City + ", " + loc.Country
	}
	return loc.Country
}

// buildCitationWrites turns the response's attributed citations into normalized
// citation rows. First-appearance order comes from the spans, which are ordered
// by the annotations' StartIndex (objective), never from the model's output
// array order. Each annotation's subject is looked up from the model output by
// URL — the URL sets are guaranteed to match by ValidateExtraction before this
// runs.
func buildCitationWrites(outCitations []llm.ExtractedCitation, spans []llm.CitationSpan) ([]store.CitationWrite, error) {
	subjectByURL := make(map[string]string, len(outCitations))
	for _, c := range outCitations {
		subjectByURL[c.URL] = c.Subject
	}

	writes := make([]store.CitationWrite, 0, len(spans))
	for _, span := range spans {
		a := span.Annotation
		cleanURL, domain, err := llm.NormalizeCitationURL(a.URL)
		if err != nil {
			return nil, err
		}
		var title *string
		if t := strings.TrimSpace(a.Title); t != "" {
			title = &t
		}
		writes = append(writes, store.CitationWrite{
			URL:       cleanURL,
			Domain:    domain,
			Title:     title,
			CiteOrder: span.CiteOrder,
			Subject:   subjectByURL[a.URL],
			TextStart: span.Start,
			TextEnd:   span.End,
		})
	}
	return writes, nil
}
