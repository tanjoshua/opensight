package llm

import (
	"testing"

	"opensight/internal/domain"
)

func TestNormalizeEntityName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Legal suffixes are dropped only as trailing tokens.
		{"pte ltd suffix", "Acme Pte. Ltd.", "acme"},
		{"private limited suffix", "Acme Private Limited", "acme"},
		{"llp suffix", "Baker & McKenzie LLP", "baker mckenzie"},
		// "clinic" is a meaningful word, never stripped: two Atlas businesses stay
		// distinct after normalization (the core false-merge guard).
		{"clinic kept", "Atlas Clinic", "atlas clinic"},
		{"orthopaedics kept", "Atlas Orthopaedics", "atlas orthopaedics"},
		// Unicode-fold: diacritics stripped, full-width folded.
		{"diacritics", "Café Society", "cafe society"},
		{"fullwidth", "ＡＣＭＥ", "acme"},
		// Punctuation to spaces (no token gluing) + whitespace collapse.
		{"punctuation and spacing", "  A&B   Co.,  Ltd  ", "a b co ltd"},
		// Degenerate inputs.
		{"all punctuation", "!!! --- ", ""},
		{"bare legal form not emptied", "Pte Ltd", "pte ltd"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeEntityName(c.in); got != c.want {
				t.Fatalf("NormalizeEntityName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	// "Atlas Clinic" and "Atlas Orthopaedics" must not collide after
	// normalization — pinned explicitly since it's the acceptance criterion.
	if NormalizeEntityName("Atlas Clinic") == NormalizeEntityName("Atlas Orthopaedics") {
		t.Fatal("Atlas Clinic and Atlas Orthopaedics normalized to the same key")
	}
}

func TestExactMatchEntities(t *testing.T) {
	rivalID := mustUUIDv7(t)
	dismissedID := mustUUIDv7(t)

	target := ReconcileTarget{Name: "Atlas Clinic", Aliases: []string{"Atlas"}}
	competitors := []ReconcileCompetitor{
		{ID: rivalID, Name: "Rival Health", Aliases: []string{"Rival"}},
		// A dismissed competitor is supplied like any other — the caller does not
		// pre-filter by status — so it must still match.
		{ID: dismissedID, Name: "Gone Clinic Pte Ltd"},
	}

	entities := []ExtractedEntity{
		{VerbatimName: "Atlas Clinic", IsTarget: true}, // confirmed self
		{VerbatimName: "Atlas", IsTarget: false},       // self via alias, flag off
		{VerbatimName: "Rival Health", IsTarget: false},
		{VerbatimName: "gone clinic", IsTarget: false}, // matches dismissed competitor (suffix folded)
		{VerbatimName: "Somebody Else", IsTarget: false},
		// is_target hint is unverified: no target key matches, so it must NOT be
		// self — it demotes to unmatched (a normal entity), not a self mention.
		{VerbatimName: "Totally Different Co", IsTarget: true},
	}

	got := ExactMatchEntities(entities, target, competitors)
	if len(got) != len(entities) {
		t.Fatalf("got %d matches, want %d", len(got), len(entities))
	}

	assert := func(i int, subj MatchSubject, comp domain.ID, matchedBy string) {
		t.Helper()
		m := got[i]
		if m.Subject != subj {
			t.Fatalf("entity %d (%q): subject = %q, want %q", i, m.Entity.VerbatimName, m.Subject, subj)
		}
		if m.CompetitorID != comp {
			t.Fatalf("entity %d (%q): competitor id = %v, want %v", i, m.Entity.VerbatimName, m.CompetitorID, comp)
		}
		if m.MatchedBy != matchedBy {
			t.Fatalf("entity %d (%q): matched_by = %q, want %q", i, m.Entity.VerbatimName, m.MatchedBy, matchedBy)
		}
	}

	assert(0, SubjectSelf, domain.ID{}, MatchedByExact)
	assert(1, SubjectSelf, domain.ID{}, MatchedByExact)
	assert(2, SubjectCompetitor, rivalID, MatchedByExact)
	assert(3, SubjectCompetitor, dismissedID, MatchedByExact)
	assert(4, SubjectUnmatched, domain.ID{}, "")
	// The demotion guarantee: is_target=true but no target key → not self.
	assert(5, SubjectUnmatched, domain.ID{}, "")
}

func mustUUIDv7(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	return id
}
