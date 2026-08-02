package api

import (
	"time"

	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/metrics"
	"opensight/internal/store"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func promptStatusToProto(s store.PromptStatus) opensightv1.PromptStatus {
	switch s {
	case store.PromptStatusActive:
		return opensightv1.PromptStatus_PROMPT_STATUS_ACTIVE
	case store.PromptStatusRetired:
		return opensightv1.PromptStatus_PROMPT_STATUS_RETIRED
	default:
		return opensightv1.PromptStatus_PROMPT_STATUS_UNSPECIFIED
	}
}

// sentimentToProto maps the nullable stored sentiment to the generated enum.
// nil means "not mentioned" — per the frozen schema, SENTIMENT_UNSPECIFIED
// legitimately covers both "unspecified" and "not mentioned" here.
func sentimentToProto(s *string) opensightv1.Sentiment {
	if s == nil {
		return opensightv1.Sentiment_SENTIMENT_UNSPECIFIED
	}
	switch *s {
	case "positive":
		return opensightv1.Sentiment_SENTIMENT_POSITIVE
	case "neutral":
		return opensightv1.Sentiment_SENTIMENT_NEUTRAL
	case "negative":
		return opensightv1.Sentiment_SENTIMENT_NEGATIVE
	case "mixed":
		return opensightv1.Sentiment_SENTIMENT_MIXED
	default:
		return opensightv1.Sentiment_SENTIMENT_UNSPECIFIED
	}
}

// int32Ptr is a nil-safe *int -> *int32 conversion, needed because
// metrics.PromptLatest.MentionOrder is *int while the proto field is *int32.
func int32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	out := int32(*v)
	return &out
}

// promptToProto shapes a store.Prompt. CreatedAt is a Timestamp here (unlike the date-only scheduled_for elsewhere).
func promptToProto(p store.Prompt) *opensightv1.Prompt {
	node := &opensightv1.Prompt{
		Id:        p.ID.String(),
		Text:      p.Text,
		Status:    promptStatusToProto(p.Status),
		CreatedAt: timestamppb.New(p.CreatedAt),
	}
	if p.ReplacesPromptID != nil {
		id := p.ReplacesPromptID.String()
		node.ReplacesPromptId = &id
	}
	return node
}

func promptTrendPointsToProto(points []metrics.PromptTrendPoint) []*opensightv1.PromptTrendPoint {
	out := make([]*opensightv1.PromptTrendPoint, 0, len(points))
	for _, p := range points {
		out = append(out, &opensightv1.PromptTrendPoint{
			RunId:        p.RunID.String(),
			ScheduledFor: p.ScheduledFor.Format(time.DateOnly),
			Mentioned:    p.Mentioned,
			ResultId:     p.ResultID.String(),
		})
	}
	return out
}
