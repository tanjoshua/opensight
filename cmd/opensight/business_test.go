package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opensight/internal/config"
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

// TestRunBusinessCreateDispatches confirms the spec file is parsed into
// store-ready fields and handed to createBusiness.
func TestRunBusinessCreateDispatches(t *testing.T) {
	var got businessCreateOptions
	called := 0
	deps := commandDeps{
		createBusiness: func(_ context.Context, _ config.Config, opts businessCreateOptions, w io.Writer) error {
			called++
			got = opts
			return nil
		},
	}

	err := runWithDeps(context.Background(), []string{
		"business", "create",
		"--tenant", tenantIDForTest,
		"--file", writeSpec(t, validSpecYAML),
	}, deps)
	if err != nil {
		t.Fatalf("business create returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("createBusiness called %d times, want 1", called)
	}
	if got.TenantID.String() != tenantIDForTest {
		t.Fatalf("tenant id = %s, want %s", got.TenantID, tenantIDForTest)
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

func TestBusinessCreateRequiresTenantAndFile(t *testing.T) {
	deps := commandDeps{
		createBusiness: func(context.Context, config.Config, businessCreateOptions, io.Writer) error {
			t.Fatal("createBusiness should not be called")
			return nil
		},
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing tenant", []string{"business", "create", "--file", "x.yaml"}, "--tenant is required"},
		{"missing file", []string{"business", "create", "--tenant", tenantIDForTest}, "--file is required"},
		{"bad tenant", []string{"business", "create", "--tenant", "nope", "--file", "x.yaml"}, "--tenant must be a UUID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runWithDeps(context.Background(), tc.args, deps)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}
