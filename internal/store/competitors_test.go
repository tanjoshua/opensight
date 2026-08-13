package store

import (
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeCreateManualCompetitorParams(t *testing.T) {
	website := "  https://example.com  "
	params, err := normalizeCreateManualCompetitorParams(CreateManualCompetitorParams{
		AccountID:  mustUUIDV7(t, "01950000-0000-7000-8000-000000002001"),
		BusinessID: mustUUIDV7(t, "01950000-0000-7000-8000-000000002002"),
		Name:       "  Example Clinic  ",
		Aliases:    []string{" Example ", "", "example", "Example Health "},
		Website:    &website,
	})
	if err != nil {
		t.Fatalf("normalize manual competitor: %v", err)
	}
	if params.ID == uuid.Nil || params.ID.Version() != uuid.Version(7) {
		t.Fatalf("generated id = %s, want UUIDv7", params.ID)
	}
	if params.Name != "Example Clinic" {
		t.Fatalf("name = %q", params.Name)
	}
	if params.Website == nil || *params.Website != "https://example.com" {
		t.Fatalf("website = %v", params.Website)
	}
	if len(params.Aliases) != 2 || params.Aliases[0] != "Example" || params.Aliases[1] != "Example Health" {
		t.Fatalf("aliases = %#v", params.Aliases)
	}
}

func TestNormalizeCreateManualCompetitorParamsRejectsBlankName(t *testing.T) {
	_, err := normalizeCreateManualCompetitorParams(CreateManualCompetitorParams{
		AccountID:  mustUUIDV7(t, "01950000-0000-7000-8000-000000002101"),
		BusinessID: mustUUIDV7(t, "01950000-0000-7000-8000-000000002102"),
		Name:       " ",
	})
	if err == nil {
		t.Fatal("expected blank name error")
	}
}

func TestNormalizeSuggestedAliasParams(t *testing.T) {
	params, err := normalizeSuggestedAliasParams(SuggestedAliasParams{
		AccountID:    mustUUIDV7(t, "01950000-0000-7000-8000-000000002201"),
		CompetitorID: mustUUIDV7(t, "01950000-0000-7000-8000-000000002202"),
		Alias:        "  Rival Medical  ",
	})
	if err != nil {
		t.Fatalf("normalize suggested alias: %v", err)
	}
	if params.Alias != "Rival Medical" {
		t.Fatalf("alias = %q", params.Alias)
	}

	params.Alias = " "
	if _, err := normalizeSuggestedAliasParams(params); err == nil {
		t.Fatal("expected blank alias error")
	}
}

func TestNormalizeAliases(t *testing.T) {
	got := normalizeAliases([]string{" Rival ", "rival", "", "Rival Health"})
	if len(got) != 2 || got[0] != "Rival" || got[1] != "Rival Health" {
		t.Fatalf("aliases = %#v", got)
	}
}
