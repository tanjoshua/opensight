package store

import (
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeCreateActivePromptParams(t *testing.T) {
	accountID := mustUUIDV7(t, "01950000-0000-7000-8000-000000000001")
	businessID := mustUUIDV7(t, "01950000-0000-7000-8000-000000000002")

	got, err := normalizeCreateActivePromptParams(CreateActivePromptParams{
		AccountID: accountID, BusinessID: businessID, Text: "  best clinic  ",
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.ID == uuid.Nil || got.ID.Version() != uuid.Version(7) {
		t.Fatalf("generated ID = %v, want UUIDv7", got.ID)
	}
	if got.Text != "  best clinic  " {
		t.Fatalf("text changed to %q", got.Text)
	}
}

func TestNormalizeCreateActivePromptParamsRejectsInvalidInputs(t *testing.T) {
	accountID := mustUUIDV7(t, "01950000-0000-7000-8000-000000000011")
	businessID := mustUUIDV7(t, "01950000-0000-7000-8000-000000000012")
	id := mustUUIDV7(t, "01950000-0000-7000-8000-000000000013")

	tests := []struct {
		name   string
		params CreateActivePromptParams
	}{
		{"missing account", CreateActivePromptParams{ID: id, BusinessID: businessID, Text: "x"}},
		{"missing business", CreateActivePromptParams{ID: id, AccountID: accountID, Text: "x"}},
		{"blank text", CreateActivePromptParams{ID: id, AccountID: accountID, BusinessID: businessID, Text: "  "}},
		{"self replacement", CreateActivePromptParams{ID: id, AccountID: accountID, BusinessID: businessID, Text: "x", ReplacesPromptID: &id}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeCreateActivePromptParams(tt.params); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestNormalizeCreateActivePromptParamsRejectsNonV7(t *testing.T) {
	_, err := normalizeCreateActivePromptParams(CreateActivePromptParams{
		ID: uuid.New(), AccountID: uuid.New(), BusinessID: uuid.New(), Text: "x",
	})
	if err == nil {
		t.Fatal("expected UUIDv7 validation error")
	}
}
