package workflows

import (
	"context"
	"errors"

	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"
)

// ResultEntities carries one analyzed result's ordered entity list into phase 2,
// each entity carrying phase 1's validated model-supplied citation links.
type ResultEntities struct {
	ResultID domain.ID
	Entities []llm.EntityWithCitations
}

// ReconcileEntitiesInput is the whole-run phase-2 payload: only Analyzed==true
// results are included (design 05, "Commit"), so unanalyzed results are absent
// from both matching and the mention rewrite.
type ReconcileEntitiesInput struct {
	AccountID  domain.ID `json:"TenantID"`
	BusinessID domain.ID
	RunID      domain.ID
	Results    []ResultEntities
}

// ReconcileEntitiesOutput reports what the reconcile wrote, for logging/tests.
type ReconcileEntitiesOutput struct {
	MentionsWritten   int
	DiscoveredCreated int
	LLMMatched        int
}

// ReconcileEntities is phase 2's single serial operation (design 05, "single
// operation, serial"): it exact-matches every entity, runs one LLM match call for
// the run's still-unmatched names, mints discovered competitors for the rest,
// and commits all mentions plus analysis_completed_at in one transaction.
//
// Idempotency requirement: this operation ALWAYS reloads the target business and
// the competitor list fresh from the DB at the top of every attempt, and never
// trusts anything the job passed across a retry. That is what makes a
// River at-least-once retry of the whole operation safe — a retry's exact pass
// re-matches names against the competitors a previous (success-not-acked)
// attempt already minted, so the delete-and-rewrite converges instead of
// re-creating them (CommitReconcile is itself one transaction).
func (a *Operations) ReconcileEntities(ctx context.Context, in ReconcileEntitiesInput) (ReconcileEntitiesOutput, error) {
	business, err := a.Store.GetBusiness(ctx, in.AccountID, in.BusinessID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ReconcileEntitiesOutput{}, NewPermanentError(
				"load business", "BadRun", err)
		}
		return ReconcileEntitiesOutput{}, err
	}
	target := llm.ReconcileTarget{Name: business.Name, Aliases: business.Aliases}

	competitors, err := a.Store.ListCompetitors(ctx, in.AccountID, in.BusinessID)
	if err != nil {
		return ReconcileEntitiesOutput{}, err
	}
	reconcileCompetitors := make([]llm.ReconcileCompetitor, 0, len(competitors))
	matchCandidates := make([]llm.MatchCandidate, 0, len(competitors))
	for _, c := range competitors {
		reconcileCompetitors = append(reconcileCompetitors, llm.ReconcileCompetitor{
			ID: c.ID, Name: c.Name, Aliases: c.Aliases,
		})
		website := ""
		if c.Website != nil {
			website = *c.Website
		}
		matchCandidates = append(matchCandidates, llm.MatchCandidate{
			ID: c.ID, Name: c.Name, Aliases: c.Aliases, Website: website,
		})
	}

	// Exact pass per result, keeping the per-result match slice so we can build
	// mention rows with the right mention_order (index within the result), and
	// the citation-linked entities alongside it. ExactMatchEntities returns one match
	// per entity in the order given, so index i is the same entity in both.
	type resultMatches struct {
		resultID domain.ID
		linked   []llm.EntityWithCitations
		matches  []llm.ExactMatch
	}
	perResult := make([]resultMatches, 0, len(in.Results))

	// Group still-unmatched entities by normalized name across the whole run, so
	// one LLM verdict (and at most one discovered competitor) covers every
	// occurrence of a given name.
	unmatchedOrder := []string{} // normalized keys, first-appearance order
	unmatchedVerbatim := map[string]string{}
	seenUnmatched := map[string]bool{}

	for _, re := range in.Results {
		entities := make([]llm.ExtractedEntity, len(re.Entities))
		for i, a := range re.Entities {
			entities[i] = a.Entity
		}
		matches := llm.ExactMatchEntities(entities, target, reconcileCompetitors)
		perResult = append(perResult, resultMatches{resultID: re.ResultID, linked: re.Entities, matches: matches})
		for _, m := range matches {
			if m.Subject != llm.SubjectUnmatched {
				continue
			}
			key := llm.NormalizeEntityName(m.Entity.VerbatimName)
			if !seenUnmatched[key] {
				seenUnmatched[key] = true
				unmatchedOrder = append(unmatchedOrder, key)
				unmatchedVerbatim[key] = m.Entity.VerbatimName
			}
		}
	}

	// LLM pass: one call for the deduped unmatched names. resolvedByKey maps a
	// normalized unmatched key to the id the LLM matched it to — an existing
	// competitor, or in.BusinessID for the target business itself (uuid.Nil /
	// absent == still unmatched). The target is always a candidate, so the call
	// is worth making even with no competitors yet.
	resolvedByKey := map[string]domain.ID{}
	if len(unmatchedOrder) > 0 {
		if err := a.Limiter.Acquire(ctx); err != nil {
			return ReconcileEntitiesOutput{}, err
		}
		names := make([]string, len(unmatchedOrder))
		for i, key := range unmatchedOrder {
			names[i] = unmatchedVerbatim[key]
		}
		businessWebsite := ""
		if business.Website != nil {
			businessWebsite = *business.Website
		}
		matchIn := llm.MatchInput{
			Names: names,
			Target: llm.MatchCandidate{
				ID: in.BusinessID, Name: business.Name,
				Aliases: business.Aliases, Website: businessWebsite,
			},
			Competitors: matchCandidates,
		}
		res, err := a.Matcher.RunMatch(ctx, matchIn)
		a.Limiter.Release()
		if err != nil {
			return ReconcileEntitiesOutput{}, err
		}
		ids, warnings, err := llm.DecodeMatchOutput(res.RawJSON, matchIn)
		if err != nil {
			return ReconcileEntitiesOutput{}, err
		}
		for _, w := range warnings {
			actLogger(ctx).Warn("llm match verdict warning", "run_id", in.RunID.String(), "warning", w)
		}
		for i, key := range unmatchedOrder {
			if ids[i] != (domain.ID{}) {
				resolvedByKey[key] = ids[i]
			}
		}
	}

	// Remaining unmatched keys (not LLM-resolved) each mint one discovered
	// competitor, keyed by the normalized name.
	discovered := []store.DiscoveredCompetitor{}
	suggested := []store.SuggestedAliasWrite{}
	for _, key := range unmatchedOrder {
		if id, ok := resolvedByKey[key]; ok {
			if id == in.BusinessID {
				// Resolved to the target business — a self mention, not a
				// competitor. The variant is re-judged each run until the user
				// makes it an approved business alias.
				continue
			}
			// LLM-resolved: record the verbatim variant as a suggested alias.
			suggested = append(suggested, store.SuggestedAliasWrite{
				CompetitorID: id, Variant: unmatchedVerbatim[key],
			})
			continue
		}
		discovered = append(discovered, store.DiscoveredCompetitor{
			Key: key, VerbatimName: unmatchedVerbatim[key],
		})
	}

	// Build mention rows from every entity across every result.
	mentions := []store.MentionWrite{}
	for _, pr := range perResult {
		for order, m := range pr.matches {
			mw := store.MentionWrite{
				PromptResultID: pr.resultID,
				MentionOrder:   order,
				VerbatimName:   m.Entity.VerbatimName,
				Excerpt:        m.Entity.Excerpt,
				CiteOrders:     pr.linked[order].CiteOrders,
			}
			switch m.Subject {
			case llm.SubjectSelf:
				mw.Subject = "self"
				mw.MatchedBy = llm.MatchedByExact
			case llm.SubjectCompetitor:
				mw.Subject = "competitor"
				mw.CompetitorID = m.CompetitorID
				mw.MatchedBy = llm.MatchedByExact
			case llm.SubjectUnmatched:
				key := llm.NormalizeEntityName(m.Entity.VerbatimName)
				mw.Subject = "competitor"
				if id, ok := resolvedByKey[key]; ok && id == in.BusinessID {
					mw.Subject = "self"
					mw.MatchedBy = "llm"
				} else if ok {
					mw.CompetitorID = id
					mw.MatchedBy = "llm"
				} else {
					// Resolves to a competitor this same commit mints; its own
					// triggering mention exact-matches the just-minted alias.
					mw.DiscoveredKey = key
					mw.MatchedBy = llm.MatchedByExact
				}
			}
			mentions = append(mentions, mw)
		}
	}

	if err := a.Store.CommitReconcile(ctx, in.AccountID, in.BusinessID, store.ReconcileCommitParams{
		RunID:            in.RunID,
		Discovered:       discovered,
		SuggestedAliases: suggested,
		Mentions:         mentions,
	}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ReconcileEntitiesOutput{}, NewPermanentError(
				"commit reconcile", "BadRun", err)
		}
		return ReconcileEntitiesOutput{}, err
	}

	return ReconcileEntitiesOutput{
		MentionsWritten:   len(mentions),
		DiscoveredCreated: len(discovered),
		LLMMatched:        len(resolvedByKey),
	}, nil
}
