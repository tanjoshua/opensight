package api

import (
	"time"

	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/metrics"
)

// visibilitySummaryToProto mirrors visibilityToResponse (overview.go). Trend
// is oldest-first; Current is the latest point's percent (n > 0), Delta the
// difference from the previous point (n > 1). Always returns non-nil, even
// for an empty trend.
func visibilitySummaryToProto(trend []metrics.VisibilityPoint) *opensightv1.VisibilitySummary {
	v := &opensightv1.VisibilitySummary{Trend: make([]*opensightv1.VisibilityPoint, 0, len(trend))}
	for _, p := range trend {
		v.Trend = append(v.Trend, &opensightv1.VisibilityPoint{
			RunId:        p.RunID.String(),
			ScheduledFor: p.ScheduledFor.Format(time.DateOnly),
			Analyzed:     int32(p.Analyzed),
			Mentioned:    int32(p.Mentioned),
			Percent:      p.Percent,
			ResultIds:    idStrings(p.ResultIDs),
		})
	}
	if n := len(trend); n > 0 {
		current := trend[n-1].Percent
		v.Current = &current
		if n > 1 {
			delta := current - trend[n-2].Percent
			v.Delta = &delta
		}
	}
	return v
}

func keywordStatsToProto(stats []metrics.KeywordStat) []*opensightv1.KeywordStat {
	out := make([]*opensightv1.KeywordStat, 0, min(len(stats), overviewPanelLimit))
	for _, s := range stats[:min(len(stats), overviewPanelLimit)] {
		out = append(out, &opensightv1.KeywordStat{Keyword: s.Keyword, ResultIds: idStrings(s.ResultIDs)})
	}
	return out
}

func domainStatsToProto(stats []metrics.DomainStat) []*opensightv1.DomainStat {
	out := make([]*opensightv1.DomainStat, 0, min(len(stats), overviewPanelLimit))
	for _, s := range stats[:min(len(stats), overviewPanelLimit)] {
		out = append(out, &opensightv1.DomainStat{Domain: s.Domain, ResultIds: idStrings(s.ResultIDs)})
	}
	return out
}

func promptChangesToProto(changes []metrics.PromptChange) []*opensightv1.PromptChange {
	out := make([]*opensightv1.PromptChange, 0, len(changes))
	for _, c := range changes {
		out = append(out, &opensightv1.PromptChange{
			Date:     c.Date.UTC().Format(time.DateOnly),
			Added:    int32(c.Added),
			Retired:  int32(c.Retired),
			Replaced: int32(c.Replaced),
		})
	}
	return out
}

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

// topCompetitorsToProto mirrors topCompetitorsToResponse (overview.go)
// including its exact switch shape — RPC-6 should relocate these two
// competitor converters to a future competitor_conv.go once it owns
// CompetitorService.
func topCompetitorsToProto(stats metrics.CompetitorStats) ([]*opensightv1.CompetitorSummary, int) {
	out := []*opensightv1.CompetitorSummary{}
	shown, total := 0, 0
	for _, c := range stats.Competitors {
		switch c.Status {
		case "tracked":
		case "discovered":
			total++
			if shown >= overviewDiscoveredLimit {
				continue
			}
			shown++
		default: // dismissed
			continue
		}
		out = append(out, &opensightv1.CompetitorSummary{
			Id:             c.CompetitorID.String(),
			Name:           c.Name,
			Status:         competitorStatusToProto(c.Status),
			Mentioned:      int32(c.Mentioned),
			TotalMentions:  int32(c.TotalMentions),
			MentionPercent: c.MentionPercent,
			AvgOrder:       c.AvgOrder,
			VsSelf:         c.VsSelf,
			ResultIds:      idStrings(c.ResultIDs),
			Trend:          competitorTrendPointsToProto(c.Trend),
		})
	}
	return out, total
}
