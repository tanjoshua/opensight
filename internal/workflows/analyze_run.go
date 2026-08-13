package workflows

import (
	"context"
	"errors"

	"opensight/internal/domain"
	"opensight/internal/store"
)

const (
	MaxAnalyzeResultAttempts = 3
	MaxReconcileAttempts     = 3
)

type AnalyzeInput struct {
	AccountID domain.ID `json:"account_id"`
	RunID     domain.ID `json:"run_id"`
}

func (a *Operations) LoadAnalysisJobSpec(ctx context.Context, in AnalyzeInput) (store.AnalysisJobSpec, error) {
	spec, err := a.Store.LoadAnalysisJobSpec(ctx, in.AccountID, in.RunID)
	if errors.Is(err, store.ErrNotFound) {
		return store.AnalysisJobSpec{}, NewPermanentError("load analyze run spec", "BadRun", err)
	}
	return spec, err
}
