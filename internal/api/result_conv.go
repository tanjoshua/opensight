package api

// Shared with RPC-6: RPC-6 extends this with ResultAnalysis/PromptRef
// population for ListResults/GetResult.

import (
	"time"

	opensightv1 "opensight/internal/gen/opensight/v1"
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

// runToProto mirrors runToResponse (responses.go) field-for-field.
// Visibility is deliberately left nil here — only RPC-6's ListRuns populates
// it (matches latestRunToResponse's REST behavior, which never sets it).
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
	}
}

// promptResultToProto mirrors resultToResponse(result, includeRaw=false)
// (responses.go): raw_response_json always stays "" here — RPC-6's GetResult
// adds that. Unanalyzed and Prompt/Run/Analysis are left at zero value; the
// caller sets Unanalyzed and any of those it owns.
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
