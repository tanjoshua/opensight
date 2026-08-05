package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validSpecYAML = `name: Roots! Advanced Endodontics
website: https://example.com/roots
category: Dental
aliases:
  - Roots Endo
location:
  country: SG
  city: Singapore
  region: Central
prompts:
  - What are the best dental clinics in Singapore?
  - Is there a good dental clinic near Tampines?
`

func writeSpec(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

// TestParseBusinessCreateArgs confirms the spec file is parsed into store-ready
// fields.
func TestParseBusinessCreateArgs(t *testing.T) {
	got, err := parseBusinessCreateArgs([]string{
		"--account", accountIDForTest,
		"--file", writeSpec(t, validSpecYAML),
	})
	if err != nil {
		t.Fatalf("parseBusinessCreateArgs: %v", err)
	}
	if got.AccountID.String() != accountIDForTest {
		t.Fatalf("account id = %s, want %s", got.AccountID, accountIDForTest)
	}
	if got.Spec.Name != "Roots! Advanced Endodontics" {
		t.Fatalf("name = %q", got.Spec.Name)
	}
	if len(got.Spec.Prompts) != 2 {
		t.Fatalf("prompts = %d, want 2", len(got.Spec.Prompts))
	}
	if got.Spec.Website == nil || *got.Spec.Website != "https://example.com/roots" {
		t.Fatalf("website = %v", got.Spec.Website)
	}
	if !strings.Contains(string(got.Spec.Location), `"country":"SG"`) {
		t.Fatalf("location JSON = %s", got.Spec.Location)
	}
}

func TestBusinessCreateRejectsBadSpecs(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec string
		want string
	}{
		{"missing name", "prompts:\n  - a\nlocation:\n  country: SG\n", "name is required"},
		{"no prompts", "name: X\nlocation:\n  country: SG\n", "at least one prompt is required"},
		{"missing location", "name: X\nprompts:\n  - a\n", "location"},
		{"bad country", "name: X\nprompts:\n  - a\nlocation:\n  country: Singapore\n", "two-letter"},
		{"unknown field", "name: X\nbogus: 1\nprompts:\n  - a\nlocation:\n  country: SG\n", "parse spec"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadBusinessSpec(writeSpec(t, tc.spec))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestBusinessCreateRequiresAccountAndFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing account", []string{"--file", "x.yaml"}, "--account is required"},
		{"missing file", []string{"--account", accountIDForTest}, "--file is required"},
		{"bad account", []string{"--account", "nope", "--file", "x.yaml"}, "--account must be a UUID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseBusinessCreateArgs(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}
