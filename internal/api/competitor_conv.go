package api

import (
	"time"

	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/metrics"
	"opensight/internal/store"
)

// competitorStatusToProto maps metrics.CompetitorStat.Status (a plain Go
// string: discovered|tracked|dismissed) to the generated enum.
func competitorStatusToProto(status string) opensightv1.CompetitorStatus {
	switch status {
	case "discovered":
		return opensightv1.CompetitorStatus_COMPETITOR_STATUS_DISCOVERED
	case "tracked":
		return opensightv1.CompetitorStatus_COMPETITOR_STATUS_TRACKED
	case "dismissed":
		return opensightv1.CompetitorStatus_COMPETITOR_STATUS_DISMISSED
	default:
		return opensightv1.CompetitorStatus_COMPETITOR_STATUS_UNSPECIFIED
	}
}

func competitorTrendPointsToProto(points []metrics.CompetitorTrendPoint) []*opensightv1.CompetitorTrendPoint {
	out := make([]*opensightv1.CompetitorTrendPoint, 0, len(points))
	for _, p := range points {
		out = append(out, &opensightv1.CompetitorTrendPoint{
			RunId:        p.RunID.String(),
			ScheduledFor: p.ScheduledFor.Format(time.DateOnly),
			Analyzed:     int32(p.Analyzed),
			Mentioned:    int32(p.Mentioned),
			Percent:      p.Percent,
			ResultIds:    idStrings(p.ResultIDs),
		})
	}
	return out
}

// competitorSourceToProto maps store.CompetitorRecord.Source (a plain Go
// string: discovered|manual) to the generated enum.
func competitorSourceToProto(source string) opensightv1.CompetitorSource {
	switch source {
	case "discovered":
		return opensightv1.CompetitorSource_COMPETITOR_SOURCE_DISCOVERED
	case "manual":
		return opensightv1.CompetitorSource_COMPETITOR_SOURCE_MANUAL
	default:
		return opensightv1.CompetitorSource_COMPETITOR_SOURCE_UNSPECIFIED
	}
}

// competitorStatusFromProto maps the generated enum to a store.CompetitorStatus.
// ok is false for COMPETITOR_STATUS_UNSPECIFIED and any unrecognized enum
// number — callers that treat UNSPECIFIED as "no filter" (ListCompetitors)
// check for that case themselves before calling this.
func competitorStatusFromProto(s opensightv1.CompetitorStatus) (store.CompetitorStatus, bool) {
	switch s {
	case opensightv1.CompetitorStatus_COMPETITOR_STATUS_DISCOVERED:
		return store.CompetitorStatusDiscovered, true
	case opensightv1.CompetitorStatus_COMPETITOR_STATUS_TRACKED:
		return store.CompetitorStatusTracked, true
	case opensightv1.CompetitorStatus_COMPETITOR_STATUS_DISMISSED:
		return store.CompetitorStatusDismissed, true
	default:
		return "", false
	}
}

// competitorRecordToProto builds the write-path shape returned by
// AddCompetitor, SetCompetitorStatus, ReviewSuggestedAlias, and
// UpdateCompetitorAliases.
func competitorRecordToProto(record store.CompetitorRecord) *opensightv1.CompetitorRecord {
	return &opensightv1.CompetitorRecord{
		Id:               record.ID.String(),
		Name:             record.Name,
		Website:          record.Website,
		Aliases:          record.Aliases,
		SuggestedAliases: record.SuggestedAliases,
		Source:           competitorSourceToProto(record.Source),
		Status:           competitorStatusToProto(string(record.Status)),
	}
}

// competitorSelfToProto reports the business's own coverage over the shared
// analyzed base. Always non-nil.
func competitorSelfToProto(stats metrics.CompetitorStats) *opensightv1.CompetitorSelf {
	return &opensightv1.CompetitorSelf{
		TotalAnalyzed: int32(stats.TotalAnalyzed),
		Mentioned:     int32(stats.SelfMentioned),
		Percent:       stats.SelfPercent,
		ResultIds:     idStrings(stats.ResultIDs),
	}
}

// competitorStatToProto builds one competitor's full comparison stats
// (design 06/PRD §6).
func competitorStatToProto(c metrics.CompetitorStat) *opensightv1.Competitor {
	resp := &opensightv1.Competitor{
		Id:               c.CompetitorID.String(),
		Name:             c.Name,
		Status:           competitorStatusToProto(c.Status),
		Aliases:          c.Aliases,
		SuggestedAliases: c.SuggestedAliases,
		Mentioned:        int32(c.Mentioned),
		TotalMentions:    int32(c.TotalMentions),
		MentionPercent:   c.MentionPercent,
		AvgOrder:         c.AvgOrder,
		VsSelf:           c.VsSelf,
		ResultIds:        idStrings(c.ResultIDs),
		PerPrompt:        make([]*opensightv1.CompetitorPromptAppearance, 0, len(c.PerPrompt)),
		Trend:            competitorTrendPointsToProto(c.Trend),
	}
	for _, p := range c.PerPrompt {
		resp.PerPrompt = append(resp.PerPrompt, &opensightv1.CompetitorPromptAppearance{
			PromptId:   p.PromptID.String(),
			PromptText: p.Text,
			ResultIds:  idStrings(p.ResultIDs),
		})
	}
	return resp
}
