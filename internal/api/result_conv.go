package api

// Converters shared by PromptService and ResultService: Run and PromptResult
// shaping, plus the ResultAnalysis/citation-span reconstruction
// ListResults/GetResult need.

import (
	"encoding/json"
	"sort"
	"time"

	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/llm"
	"opensight/internal/store"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func runStatusToProto(s store.RunStatus) opensightv1.RunStatus {
	switch s {
	case store.RunStatusRunning:
		return opensightv1.RunStatus_RUN_STATUS_RUNNING
	case store.RunStatusCompleted:
		return opensightv1.RunStatus_RUN_STATUS_COMPLETED
	case store.RunStatusPartial:
		return opensightv1.RunStatus_RUN_STATUS_PARTIAL
	case store.RunStatusFailed:
		return opensightv1.RunStatus_RUN_STATUS_FAILED
	default:
		return opensightv1.RunStatus_RUN_STATUS_UNSPECIFIED
	}
}

func runTriggerToProto(t store.RunTrigger) opensightv1.RunTrigger {
	switch t {
	case store.RunTriggerInitial:
		return opensightv1.RunTrigger_RUN_TRIGGER_INITIAL
	case store.RunTriggerScheduled:
		return opensightv1.RunTrigger_RUN_TRIGGER_SCHEDULED
	case store.RunTriggerManual:
		return opensightv1.RunTrigger_RUN_TRIGGER_MANUAL
	default:
		return opensightv1.RunTrigger_RUN_TRIGGER_UNSPECIFIED
	}
}

func resultStatusToProto(s store.ResultStatus) opensightv1.ResultStatus {
	switch s {
	case store.ResultStatusSucceeded:
		return opensightv1.ResultStatus_RESULT_STATUS_SUCCEEDED
	case store.ResultStatusFailed:
		return opensightv1.ResultStatus_RESULT_STATUS_FAILED
	default:
		return opensightv1.ResultStatus_RESULT_STATUS_UNSPECIFIED
	}
}

// timestampOrNil converts an optional *time.Time to a *timestamppb.Timestamp,
// preserving nil (e.g. a run's completed_at before it finishes).
func timestampOrNil(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

// int32OrNil converts an optional *int (e.g. Run.ExpectedResults) to a
// *int32, preserving nil.
func int32OrNil(n *int) *int32 {
	if n == nil {
		return nil
	}
	v := int32(*n)
	return &v
}

// runToProto shapes a store.Run. Visibility and the three result counts are
// deliberately left unset here — only ListRuns/GetOverview populate them via
// runListItemToProto.
func runToProto(run store.Run) *opensightv1.Run {
	return &opensightv1.Run{
		Id:                  run.ID.String(),
		BusinessId:          run.BusinessID.String(),
		Platform:            run.Platform,
		Trigger:             runTriggerToProto(run.Trigger),
		ScheduledFor:        run.ScheduledFor.Format(time.DateOnly),
		Status:              runStatusToProto(run.Status),
		WorkflowId:          run.WorkflowID,
		StartedAt:           timestamppb.New(run.StartedAt),
		CompletedAt:         timestampOrNil(run.CompletedAt),
		AnalysisCompletedAt: timestampOrNil(run.AnalysisCompletedAt),
		ExpectedResults:     int32OrNil(run.ExpectedResults),
	}
}

// runListItemToProto extends runToProto with the per-run result counts that
// only ListRuns and GetOverview.latest_run populate.
func runListItemToProto(item store.RunListItem) *opensightv1.Run {
	row := runToProto(item.Run)
	row.SucceededResults = int32(item.SucceededResults)
	row.FailedResults = int32(item.FailedResults)
	row.AnalyzedResults = int32(item.AnalyzedResults)
	return row
}

// promptResultToProto shapes a store.PromptResult: raw_response_json always
// stays "" here. Unanalyzed and
// Prompt/Run/Analysis are left at zero value; each caller (GetPrompt,
// ListResults, GetResult) sets the extra fields it owns on the returned
// struct after the call — GetResult additionally sets RawResponseJson when
// include_raw is requested.
func promptResultToProto(r store.PromptResult) *opensightv1.PromptResult {
	return &opensightv1.PromptResult{
		Id:           r.ID.String(),
		RunId:        r.RunID.String(),
		PromptId:     r.PromptID.String(),
		Status:       resultStatusToProto(r.Status),
		Model:        r.Model,
		RequestJson:  string(r.Request),
		ResponseText: r.ResponseText,
		Error:        r.Error,
		RequestedAt:  timestamppb.New(r.RequestedAt),
		CompletedAt:  timestamppb.New(r.CompletedAt),
	}
}

func mentionSubjectToProto(s string) opensightv1.MentionSubject {
	switch s {
	case "self":
		return opensightv1.MentionSubject_MENTION_SUBJECT_SELF
	case "competitor":
		return opensightv1.MentionSubject_MENTION_SUBJECT_COMPETITOR
	default:
		return opensightv1.MentionSubject_MENTION_SUBJECT_UNSPECIFIED
	}
}

func matchMethodToProto(s string) opensightv1.MatchMethod {
	switch s {
	case "exact":
		return opensightv1.MatchMethod_MATCH_METHOD_EXACT
	case "llm":
		return opensightv1.MatchMethod_MATCH_METHOD_LLM
	default:
		return opensightv1.MatchMethod_MATCH_METHOD_UNSPECIFIED
	}
}

// resultCitationSubjectToProto maps a store.ResultCitation's subject string
// to the generated enum. Named distinctly from citation_conv.go's
// citationSubjectToProto (which converts a different type,
// metrics.CitationSubjectStat) to avoid a duplicate function name.
// "unknown" is a real, persisted value, not an error case.
func resultCitationSubjectToProto(s string) opensightv1.CitationSubject {
	switch s {
	case "business":
		return opensightv1.CitationSubject_CITATION_SUBJECT_BUSINESS
	case "competitor":
		return opensightv1.CitationSubject_CITATION_SUBJECT_COMPETITOR
	case "other":
		return opensightv1.CitationSubject_CITATION_SUBJECT_OTHER
	case "unknown":
		return opensightv1.CitationSubject_CITATION_SUBJECT_UNKNOWN
	default:
		return opensightv1.CitationSubject_CITATION_SUBJECT_UNSPECIFIED
	}
}

// citationSpansToProto returns the response's url_citation annotation spans
// in first-appearance (StartIndex-ascending) order — the same order
// cite_order was assigned in (workflows.buildCitationWrites). A malformed or
// empty raw_response yields no spans.
func citationSpansToProto(rawResponse json.RawMessage) []*opensightv1.CitationSpan {
	annotations, err := llm.ParseCitationAnnotations(rawResponse)
	if err != nil || len(annotations) == 0 {
		return nil
	}
	sort.SliceStable(annotations, func(i, j int) bool {
		return annotations[i].StartIndex < annotations[j].StartIndex
	})
	spans := make([]*opensightv1.CitationSpan, len(annotations))
	for i, a := range annotations {
		spans[i] = &opensightv1.CitationSpan{Start: int32(a.StartIndex), End: int32(a.EndIndex)}
	}
	return spans
}

// resultAnalysisToProto shapes a store.ResultAnalysis, including the
// span-index reconstruction: citations were written one-per-annotation
// in StartIndex order with cite_order equal to that index, so cite_order
// indexes directly into the sorted span slice from citationSpansToProto — no
// URL matching needed.
func resultAnalysisToProto(a store.ResultAnalysis, rawResponse json.RawMessage) *opensightv1.ResultAnalysis {
	resp := &opensightv1.ResultAnalysis{
		Sentiment: sentimentToProto(a.Sentiment),
		Keywords:  a.Keywords,
		Excerpts:  a.Excerpts,
		Mentions:  make([]*opensightv1.ResultMention, 0, len(a.Mentions)),
		Citations: make([]*opensightv1.ResultCitation, 0, len(a.Citations)),
	}
	for _, m := range a.Mentions {
		resp.Mentions = append(resp.Mentions, &opensightv1.ResultMention{
			Subject:      mentionSubjectToProto(m.Subject),
			VerbatimName: m.VerbatimName,
			Order:        int32(m.MentionOrder),
			MatchedBy:    matchMethodToProto(m.MatchedBy),
			Excerpt:      m.Excerpt,
		})
	}

	spans := citationSpansToProto(rawResponse)
	for _, c := range a.Citations {
		row := &opensightv1.ResultCitation{
			Url:       c.URL,
			Domain:    c.Domain,
			Title:     c.Title,
			CiteOrder: int32(c.CiteOrder),
			Subject:   resultCitationSubjectToProto(c.Subject),
		}
		if c.CiteOrder >= 0 && c.CiteOrder < len(spans) {
			row.Span = spans[c.CiteOrder]
		}
		resp.Citations = append(resp.Citations, row)
	}
	return resp
}
