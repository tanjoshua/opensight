package llm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestExtractionQualityGate is the ANA-2 quality-gate driver. It is skipped by
// default and makes real OpenAI calls: it runs the live extraction against every
// captured response in testdata/spk1 and prints the extraction output alongside
// deterministic validation, so a human can spot-check both entity extraction
// and citation-to-entity sets for zero false or missed clear links.
//
// Run it (from the repo root, with the OpenAI key exported and a real analysis
// model chosen):
//
//	set -a; source .env; set +a
//	OPENAI_ANALYSIS_MODEL=gpt-5-mini \
//	  go test ./internal/llm -run TestExtractionQualityGate -v -count=1
//
// Optionally set QUALITY_GATE_BUSINESS_NAME (and _ALIASES comma-separated,
// _CATEGORY, _LOCATION) to treat a specific organisation as the target business
// and check its self-mention/sentiment extraction; unset, target extraction is
// exercised with a neutral placeholder that should not appear in the responses.
func TestExtractionQualityGate(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		t.Skip("set OPENAI_API_KEY to run the extraction quality-gate driver")
	}
	model := strings.TrimSpace(os.Getenv("OPENAI_ANALYSIS_MODEL"))
	if model == "" || model == "gpt-5.6-luna" {
		t.Skip("set OPENAI_ANALYSIS_MODEL to a real mini-class model to run the quality gate")
	}

	runner, err := NewOpenAIExtractionRunner(OpenAIConfig{APIKey: apiKey, Model: model})
	if err != nil {
		t.Fatalf("build extraction runner: %v", err)
	}

	businessName := getenvDefault("QUALITY_GATE_BUSINESS_NAME", "Acme Health Clinic")
	var aliases []string
	if raw := strings.TrimSpace(os.Getenv("QUALITY_GATE_BUSINESS_ALIASES")); raw != "" {
		for _, a := range strings.Split(raw, ",") {
			if a = strings.TrimSpace(a); a != "" {
				aliases = append(aliases, a)
			}
		}
	}
	category := getenvDefault("QUALITY_GATE_BUSINESS_CATEGORY", "clinic")
	location := getenvDefault("QUALITY_GATE_BUSINESS_LOCATION", "Singapore, SG")

	captures, err := filepath.Glob("../../testdata/spk1/*.json")
	if err != nil {
		t.Fatalf("glob captures: %v", err)
	}
	sort.Strings(captures)

	t.Logf("quality gate: model=%s target=%q captures=%d", model, businessName, len(captures)-1)

	for _, path := range captures {
		if filepath.Base(path) == "index.json" {
			continue
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read capture: %v", err)
			}
			parsed, err := parseOpenAIResponse(json.RawMessage(raw))
			if err != nil {
				t.Fatalf("parse capture: %v", err)
			}
			annotations, err := ParseCitationAnnotations(json.RawMessage(raw))
			if err != nil {
				t.Fatalf("parse annotations: %v", err)
			}

			// Run the exact shared decode/validate/retry path the AnalyzeResult
			// activity uses, so the gate exercises the real repair + one-retry
			// self-correction rather than a divergent single-shot copy.
			result, err := ExtractWithRetry(context.Background(), runner, ExtractionInput{
				ResponseText:     parsed.Text,
				BusinessName:     businessName,
				BusinessAliases:  aliases,
				BusinessCategory: category,
				BusinessLocation: location,
				Citations:        annotations,
			}, parsed.Text, annotations)
			if err != nil {
				t.Fatalf("ExtractWithRetry: %v", err)
			}
			out := result.Output

			t.Logf("\n=== %s ===\nRESPONSE:\n%s\n\nENTITIES (%d):", filepath.Base(path), parsed.Text, len(out.Entities))
			for _, e := range out.Entities {
				t.Logf("  - %q (is_target=%v)\n      excerpt: %q", e.VerbatimName, e.IsTarget, e.Excerpt)
			}
			if out.Target != nil {
				t.Logf("TARGET: sentiment=%s keywords=%v", out.Target.Sentiment, out.Target.Keywords)
				for _, ex := range out.Target.Excerpts {
					t.Logf("      excerpt: %q", ex)
				}
			} else {
				t.Logf("TARGET: null (business not mentioned)")
			}
			t.Logf("CITATIONS (%d):", len(out.Citations))
			for _, c := range out.Citations {
				t.Logf("  - #%d [%s] %s -> links %v", c.CiteOrder, c.Subject, c.URL, c.Links)
			}

			// PASS = analyzed (verbatim-clean, zero validation errors) after at
			// most one retry; FLAGGED = never became clean even after the retry.
			// A FLAGGED fixture is the gate's hard failure — a human reads the
			// final validation errors to see what stayed fabricated.
			if result.Analyzed {
				t.Logf("QUALITY GATE: PASS (analyzed, zero validation errors)")
			} else {
				t.Errorf("QUALITY GATE: FLAGGED after retry; final validation errors:")
				for _, e := range result.ValidationErrs {
					t.Errorf("  - %s", e)
				}
			}
		})
	}
}

func getenvDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
