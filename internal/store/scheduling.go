package store

import (
	"context"
	"fmt"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
)

type MonitoringCandidate struct {
	BusinessID  domain.ID
	AccountID   domain.ID
	ActivatedAt time.Time
	PlanCode    string
	AccessState billing.State
}

func (s *Store) ListMonitoringCandidates(ctx context.Context) ([]MonitoringCandidate, error) {
	rows, err := s.q(ctx).ListMonitoringCandidates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list monitoring candidates: %w", err)
	}
	out := make([]MonitoringCandidate, 0, len(rows))
	for _, row := range rows {
		state := billing.State{Comped: row.Comped}
		if row.StripeSubscriptionID != nil {
			state.StripeSubscriptionID = *row.StripeSubscriptionID
		}
		if row.StripeStatus != nil {
			state.StripeStatus = *row.StripeStatus
		}
		if row.PastDueSince != nil {
			state.PastDueSince = *row.PastDueSince
		}
		out = append(out, MonitoringCandidate{BusinessID: row.ID, AccountID: row.AccountID,
			ActivatedAt: *row.ActivatedAt, PlanCode: row.PlanCode, AccessState: state})
	}
	return out, nil
}
