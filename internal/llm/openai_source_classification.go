package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const openAISourceClassificationSchemaName = "citation_source_classification"

const sourceClassificationInstructions = `Analyze all inspected citation sources and the customer's readable website in one batch.

CLASSIFICATIONS
- competitor_owned: the domain/page is the official site or first-party content of exactly one linked competitor. A same-domain page title or visible brand name that clearly matches that competitor is sufficient ownership evidence, including when the page is a JavaScript shell with little other visible text.
- third_party: the source independently lists, reviews, reports on, or otherwise covers businesses.
- unknown: ownership cannot be established confidently from the supplied evidence.

RULES
- Emit exactly one source for every candidate_index, without adding, dropping, or duplicating indexes.
- For competitor_owned, owner must exactly copy one linked claim owner and claim_indices must select only claims belonging to that owner. Select claims that contain concrete facts used to recommend that owner.
- For third_party and unknown, return owner "" and claim_indices [].
- Prefer the competitor whose name matches the page title/visible brand when several linked names occur in claims. Do not assign the page to the other businesses merely because the same cited passage names them too.
- Do not infer ownership from a domain name alone when neither the page identity nor inspected content confirms it.
- After classifying every source, compare all selected competitor-owned claims with the complete readable customer-site content and emit content_gaps.
- If site_content is empty, emit no content gaps because absence could not be verified; source classifications and third-party listing decisions still proceed.
- A content gap is an actionable topic that materially helped competitors appear in answers and is absent or meaningfully underdeveloped on the customer's site. Do not emit a gap merely because a competitor said something; suppress it when the customer already publishes equivalent concrete information anywhere in the supplied site content.
- Decide the content groupings from the evidence itself. Merge claims across every competitor and domain when they support the same coherent publishing job, but keep genuinely different jobs separate even if they would live on the same page.
- For each gap, invent a durable topic_key of 3-64 lowercase ASCII letters, digits, and single hyphens. It must name the enduring subject of the publishing job, not a competitor, a volatile number, or generic buckets such as "other" or "business-details". Examples of the required specificity, drawn from unrelated industries, are "data-security-certifications", "same-day-turnaround", and "warranty-and-returns". They illustrate specificity only; they are not a taxonomy and the customer's own industry decides the subject.
- prior_content_gaps contains model-defined publishing jobs from earlier assessments. Reuse an exact prior topic_key when current evidence supports the same underlying job, even if the wording or supporting competitors changed. Do not retain a prior gap that the current evidence and site comparison no longer support, and create a new key whenever the current job is genuinely different.
- title is a short imperative naming the customer's task. reason concisely explains what readers and answer engines cannot currently establish from the customer's site. recommendation is one specific instruction naming the page and the concrete facts to add. Generate all three for this exact evidence cluster; never name a competitor in these user-facing fields.
- Use coverage "absent" with site_evidence [] when the topic is not stated. Use "partial" only when some relevant information exists but important concrete detail is missing, and copy one to three exact supporting passages from site_content into site_evidence.
- Every evidence reference must point to a claim selected by a competitor_owned classification. Use all and only the claims that support that grouped topic.
- Phrase unknown customer facts conditionally (for example, "If offered, state whether..."). Never invent credentials, equipment, experience, prices, outcomes, or services. Never recommend copying wording or publishing an outcome claim that cannot be substantiated.
- If prior output and validation failures are supplied, fix every failure and emit the full corrected object.`

const sourceClassificationJSONSchema = `{
  "type":"object","additionalProperties":false,"required":["sources","content_gaps"],
  "properties":{
    "sources":{"type":"array","items":{
    "type":"object","additionalProperties":false,
    "required":["candidate_index","classification","owner","claim_indices"],
    "properties":{
      "candidate_index":{"type":"integer","minimum":0},
      "classification":{"type":"string","enum":["competitor_owned","third_party","unknown"]},
      "owner":{"type":"string"},
      "claim_indices":{"type":"array","items":{"type":"integer","minimum":0}}
    }
  }},
    "content_gaps":{"type":"array","maxItems":8,"items":{
      "type":"object","additionalProperties":false,
      "required":["topic_key","title","reason","recommendation","coverage","site_evidence","evidence"],
      "properties":{
        "topic_key":{"type":"string","minLength":3,"maxLength":64,"pattern":"^[a-z0-9]+(?:-[a-z0-9]+)*$"},
        "title":{"type":"string"},
        "reason":{"type":"string"},
        "recommendation":{"type":"string"},
        "coverage":{"type":"string","enum":["absent","partial"]},
        "site_evidence":{"type":"array","maxItems":3,"items":{"type":"string"}},
        "evidence":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["candidate_index","claim_index"],"properties":{"candidate_index":{"type":"integer","minimum":0},"claim_index":{"type":"integer","minimum":0}}}}
      }
    }}
  }
}`

var sourceClassificationSchema = mustParseJSONSchema(sourceClassificationJSONSchema)

type OpenAISourceClassifier struct{ openAIResponsesClient }

func NewOpenAISourceClassifier(cfg OpenAIConfig) (*OpenAISourceClassifier, error) {
	client, err := newOpenAIResponsesClient(cfg, "openai analysis model is required")
	if err != nil {
		return nil, err
	}
	return &OpenAISourceClassifier{openAIResponsesClient: client}, nil
}

func (r *OpenAISourceClassifier) ClassifySources(ctx context.Context, in SourceClassificationInput) (SourceAnalysis, error) {
	if r == nil {
		return SourceAnalysis{}, errors.New("openai source classifier is nil")
	}
	if len(in.Candidates) == 0 {
		return SourceAnalysis{Sources: []SourceClassification{}, Gaps: []ContentGap{}}, nil
	}
	return classifySourcesWithRetry(ctx, r, in)
}

func (r *OpenAISourceClassifier) runSourceClassification(ctx context.Context, in SourceClassificationInput) (SourceClassificationRunResult, error) {
	content, err := json.Marshal(struct {
		BusinessName     string            `json:"business_name"`
		SiteContent      string            `json:"site_content"`
		PriorContentGaps []PriorContentGap `json:"prior_content_gaps"`
		Candidates       []SourceCandidate `json:"candidates"`
	}{BusinessName: in.BusinessName, SiteContent: in.SiteContent, PriorContentGaps: in.PriorContentGaps, Candidates: in.Candidates})
	if err != nil {
		return SourceClassificationRunResult{}, fmt.Errorf("marshal source classification input: %w", err)
	}
	input := responses.ResponseInputParam{
		responses.ResponseInputItemParamOfMessage(sourceClassificationInstructions, responses.EasyInputMessageRoleDeveloper),
		responses.ResponseInputItemParamOfMessage(string(content), responses.EasyInputMessageRoleUser),
	}
	if len(in.PriorOutputJSON) > 0 || len(in.RetryValidationErrors) > 0 {
		input = append(input,
			responses.ResponseInputItemParamOfMessage(string(in.PriorOutputJSON), responses.EasyInputMessageRoleAssistant),
			responses.ResponseInputItemParamOfMessage(retryContent(in.RetryValidationErrors), responses.EasyInputMessageRoleUser),
		)
	}
	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(r.model),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Store: openai.Bool(false),
		Text:  responses.ResponseTextConfigParam{Format: strictJSONSchemaFormat(openAISourceClassificationSchemaName, sourceClassificationSchema)},
	}
	parsed, body, err := r.call(ctx, params)
	if err != nil {
		return SourceClassificationRunResult{}, err
	}
	if err := validateOpenAIResponse(parsed, body, "openai source classification response missing reported model", "openai source classification response missing structured output"); err != nil {
		return SourceClassificationRunResult{}, err
	}
	return SourceClassificationRunResult{RawJSON: json.RawMessage(parsed.Text), Model: strings.TrimSpace(parsed.Model)}, nil
}
