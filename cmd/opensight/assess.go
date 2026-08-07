package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"opensight/internal/config"
	"opensight/internal/domain"
	"opensight/internal/store"
	"opensight/internal/visibility"

	"github.com/google/uuid"
)

const (
	assessUsage       = "usage: opensight assess <replay>"
	assessReplayUsage = "usage: opensight assess replay [--business <business-id>] [--since <date>] [--until <date>] [--limit <n>]"
)

// replayOptions selects which stored generations to re-derive verdicts for.
// Every filter is optional; the limit only bounds how much history one
// invocation reads.
type replayOptions struct {
	BusinessID   *domain.ID
	Since, Until *time.Time
	Limit        int
}

func runAssessCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("assess subcommand required; %s", assessUsage)
	}
	switch args[0] {
	case "replay":
		opts, err := parseAssessReplayArgs(args[1:])
		if err != nil {
			return err
		}
		return replayAssessmentsCLI(ctx, cfg, opts, os.Stdout)
	default:
		return fmt.Errorf("unknown assess subcommand %q; %s", args[0], assessUsage)
	}
}

func parseAssessReplayArgs(args []string) (replayOptions, error) {
	flags := flag.NewFlagSet("assess replay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	businessRaw := flags.String("business", "", "restrict to one business id")
	sinceRaw := flags.String("since", "", "only generations started on or after this date (YYYY-MM-DD or RFC3339)")
	untilRaw := flags.String("until", "", "only generations started before this date (YYYY-MM-DD or RFC3339)")
	limit := flags.Int("limit", 100, "maximum generations to replay")
	if err := flags.Parse(args); err != nil {
		return replayOptions{}, fmt.Errorf("%s", assessReplayUsage)
	}
	if flags.NArg() != 0 {
		return replayOptions{}, fmt.Errorf("unexpected argument %q; %s", flags.Arg(0), assessReplayUsage)
	}
	if *limit < 1 {
		return replayOptions{}, fmt.Errorf("--limit must be positive; %s", assessReplayUsage)
	}

	opts := replayOptions{Limit: *limit}
	if raw := strings.TrimSpace(*businessRaw); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return replayOptions{}, fmt.Errorf("--business must be a UUID: %w", err)
		}
		opts.BusinessID = &id
	}
	var err error
	if opts.Since, err = parseReplayTime("--since", *sinceRaw); err != nil {
		return replayOptions{}, err
	}
	if opts.Until, err = parseReplayTime("--until", *untilRaw); err != nil {
		return replayOptions{}, err
	}
	if opts.Since != nil && opts.Until != nil && !opts.Until.After(*opts.Since) {
		return replayOptions{}, errors.New("--until must be after --since")
	}
	return opts, nil
}

func parseReplayTime(flagName, raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			parsed = parsed.UTC()
			return &parsed, nil
		}
	}
	return nil, fmt.Errorf("%s must be YYYY-MM-DD or RFC3339", flagName)
}

// replayResearcher refuses every inspection. Replay re-derives verdicts from
// evidence that is already stored; a live fetch would make the comparison
// depend on today's network rather than on the assessor change being tested. An
// assessor that needs research therefore sees the same failure it would see
// from an unreachable source, and the report says how often that happened.
type replayResearcher struct{ refused int }

func (r *replayResearcher) Inspect(context.Context, visibility.ResearchRequest) (visibility.ResearchResult, error) {
	r.refused++
	return visibility.ResearchResult{}, errors.New("live research is unavailable during replay")
}

type verdictChange struct {
	Kind                    string // SAME, CHANGED, ADDED, REMOVED
	PracticeKey, SubjectKey string
	Stored, Fresh           visibility.AssessmentStatus
}

// generationReplay is one generation's outcome: the verdicts today's assessors
// derived from its stored evidence, plus the assessors that could not run.
type generationReplay struct {
	Generation      store.ReplayGeneration
	Changes         []verdictChange
	Skipped, Failed []string
}

func (g generationReplay) counts() (same, changed, added, removed int) {
	for _, c := range g.Changes {
		switch c.Kind {
		case "SAME":
			same++
		case "CHANGED":
			changed++
		case "ADDED":
			added++
		case "REMOVED":
			removed++
		}
	}
	return
}

// replayGeneration runs every registered assessor over one generation's stored
// evidence and diffs the result against what that generation recorded.
//
// An assessor whose required collectors are missing from the stored evidence is
// skipped: it was never in a position to produce a verdict, so its practices are
// left out of the diff rather than reported as disappeared. An assessor that has
// its evidence but fails on it — most importantly by refusing an evidence
// payload_version it no longer understands — is a hard failure for the
// generation, never a silent omission.
func replayGeneration(ctx context.Context, gen store.ReplayGeneration, artifacts []visibility.EvidenceArtifact, stored []store.StoredAssessment, research visibility.BoundedResearcher) generationReplay {
	out := generationReplay{Generation: gen}
	view := visibility.NewEvidenceView(artifacts)
	available := map[string]bool{}
	for _, a := range artifacts {
		available[a.CollectorKey] = true
	}

	fresh := []visibility.AssessmentDraft{}
	unavailable := map[string]bool{}
	for _, assessor := range visibility.Assessors() {
		m := assessor.Manifest()
		missing := []string{}
		for _, key := range m.RequiredCollectors {
			if !available[key] {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			out.Skipped = append(out.Skipped, fmt.Sprintf("assessor=%s reason=%q", m.Key, "missing stored evidence: "+strings.Join(missing, ",")))
			for _, key := range m.PracticeKeys {
				unavailable[key] = true
			}
			continue
		}
		drafts, err := assessor.Assess(ctx, view, research)
		if err != nil {
			out.Failed = append(out.Failed, fmt.Sprintf("assessor=%s error=%q", m.Key, err.Error()))
			for _, key := range m.PracticeKeys {
				unavailable[key] = true
			}
			continue
		}
		fresh = append(fresh, drafts...)
	}
	out.Changes = diffVerdicts(stored, fresh, unavailable)
	return out
}

// diffVerdicts pairs stored and freshly derived verdicts by practice and
// subject. Practices listed in unavailable are omitted entirely: their assessor
// could not run, so silence about them is honest and "REMOVED" would not be.
func diffVerdicts(stored []store.StoredAssessment, fresh []visibility.AssessmentDraft, unavailable map[string]bool) []verdictChange {
	type pair struct {
		practice, subject string
		stored, fresh     visibility.AssessmentStatus
	}
	index := map[string]*pair{}
	order := []string{}
	get := func(practice, subject string) *pair {
		key := practice + "\x00" + subject
		p, ok := index[key]
		if !ok {
			p = &pair{practice: practice, subject: subject}
			index[key] = p
			order = append(order, key)
		}
		return p
	}
	for _, s := range stored {
		if unavailable[s.PracticeKey] {
			continue
		}
		get(s.PracticeKey, s.SubjectKey).stored = s.Status
	}
	for _, d := range fresh {
		get(d.PracticeKey, d.SubjectKey).fresh = d.Status
	}
	sort.Strings(order)
	out := make([]verdictChange, 0, len(order))
	for _, key := range order {
		p := index[key]
		change := verdictChange{PracticeKey: p.practice, SubjectKey: p.subject, Stored: p.stored, Fresh: p.fresh}
		switch {
		case p.stored == "":
			change.Kind = "ADDED"
		case p.fresh == "":
			change.Kind = "REMOVED"
		case p.stored == p.fresh:
			change.Kind = "SAME"
		default:
			change.Kind = "CHANGED"
		}
		out = append(out, change)
	}
	return out
}

// replayAssessmentsCLI is a dry run in the strict sense: it opens the database
// read-only in effect, writing nothing. `assessment_generations` is unique per
// monitoring run, so a replay cannot record itself as a second generation for
// the same run, and writing into the existing one would destroy the historical
// verdict that a criteria change is supposed to preserve. Reporting the diff is
// the whole deliverable; re-recording it is not offered.
func replayAssessmentsCLI(ctx context.Context, cfg config.Config, opts replayOptions, out io.Writer) error {
	if ctx.Err() != nil {
		return nil
	}
	db, err := store.Open(ctx, cfg.DatabaseURL, int32(cfg.DBMaxOpenConns))
	if err != nil {
		return err
	}
	defer db.Close()
	dataStore := store.New(db)

	generations, err := dataStore.ListReplayGenerations(ctx, store.ReplayFilter{BusinessID: opts.BusinessID, Since: opts.Since, Until: opts.Until, MaxGenerations: opts.Limit})
	if err != nil {
		return err
	}

	research := &replayResearcher{}
	results := make([]generationReplay, 0, len(generations))
	for _, gen := range generations {
		artifacts, err := dataStore.LoadGenerationEvidence(ctx, gen.ID)
		if err != nil {
			return err
		}
		stored, err := dataStore.ListGenerationAssessments(ctx, gen.ID)
		if err != nil {
			return err
		}
		results = append(results, replayGeneration(ctx, gen, artifacts, stored, research))
	}
	return writeReplayReport(out, results, research.refused)
}

func writeReplayReport(out io.Writer, results []generationReplay, refusedResearch int) error {
	totals := map[string]int{}
	failed := 0
	for _, r := range results {
		same, changed, added, removed := r.counts()
		totals["same"] += same
		totals["changed"] += changed
		totals["added"] += added
		totals["removed"] += removed
		totals["skipped"] += len(r.Skipped)
		totals["failed"] += len(r.Failed)
		failed += len(r.Failed)

		if _, err := fmt.Fprintf(out, "generation=%s business=%s run=%s started=%s status=%s verdicts=%d changed=%d\n",
			r.Generation.ID, r.Generation.BusinessID, r.Generation.MonitoringRunID,
			r.Generation.StartedAt.UTC().Format(time.RFC3339), r.Generation.Status,
			len(r.Changes), changed+added+removed); err != nil {
			return err
		}
		for _, c := range r.Changes {
			if c.Kind == "SAME" {
				continue
			}
			if _, err := fmt.Fprintf(out, "  %-7s %s %s stored=%s replayed=%s\n", c.Kind, c.PracticeKey, c.SubjectKey, orDash(c.Stored), orDash(c.Fresh)); err != nil {
				return err
			}
		}
		for _, s := range r.Skipped {
			if _, err := fmt.Fprintf(out, "  SKIPPED %s\n", s); err != nil {
				return err
			}
		}
		for _, f := range r.Failed {
			if _, err := fmt.Fprintf(out, "  FAILED  %s\n", f); err != nil {
				return err
			}
		}
	}

	if _, err := fmt.Fprintf(out, "generations=%d unchanged=%d changed=%d added=%d removed=%d skipped=%d failed=%d written=0\n",
		len(results), totals["same"], totals["changed"], totals["added"], totals["removed"], totals["skipped"], totals["failed"]); err != nil {
		return err
	}
	if refusedResearch > 0 {
		if _, err := fmt.Fprintf(out, "note: refused %d live research inspections; verdicts from research-dependent assessors are not comparable\n", refusedResearch); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d assessor failures during replay", failed)
	}
	return nil
}

func orDash(s visibility.AssessmentStatus) string {
	if s == "" {
		return "-"
	}
	return string(s)
}
