package store

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeApplyProposalParams(t *testing.T) {
	got, err := normalizeApplyProposalParams(ApplyProposalParams{
		AccountID:  mustUUIDV7(t, "01950000-0000-7000-8000-000000000101"),
		BusinessID: mustUUIDV7(t, "01950000-0000-7000-8000-000000000102"),
		Name:       "Atlas Clinic", Category: "Dental", ActivatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.Aliases == nil {
		t.Fatal("aliases must be a non-nil empty slice")
	}
	if string(got.Services) != "[]" {
		t.Fatalf("services = %s, want []", got.Services)
	}
}

func TestNormalizeApplyProposalParamsRejectsIncompleteProfile(t *testing.T) {
	base := ApplyProposalParams{
		AccountID:  mustUUIDV7(t, "01950000-0000-7000-8000-000000000111"),
		BusinessID: mustUUIDV7(t, "01950000-0000-7000-8000-000000000112"),
		Name:       "Atlas Clinic", Category: "Dental", Services: json.RawMessage("[]"),
		ActivatedAt: time.Now(),
	}
	tests := []struct {
		name string
		edit func(*ApplyProposalParams)
	}{
		{"name", func(p *ApplyProposalParams) { p.Name = " " }},
		{"category", func(p *ApplyProposalParams) { p.Category = "" }},
		{"activation", func(p *ApplyProposalParams) { p.ActivatedAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := base
			tt.edit(&params)
			if _, err := normalizeApplyProposalParams(params); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
