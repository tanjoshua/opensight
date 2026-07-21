package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRepairDoubleEscapedUnicode(t *testing.T) {
	// Each `raw` is the actual JSON text the model emitted. In Go source a
	// backtick string is literal, so `\\u0026` here is two backslashes + u0026 —
	// exactly the double-escaped artifact seen in the failing quality-gate run.
	t.Run("double-escaped ampersand decodes to a real ampersand", func(t *testing.T) {
		raw := json.RawMessage(`{"verbatim_name": "Q \\u0026 M Dental Surgery"}`)
		var out struct {
			VerbatimName string `json:"verbatim_name"`
		}
		if err := json.Unmarshal(RepairDoubleEscapedUnicode(raw), &out); err != nil {
			t.Fatalf("unmarshal after repair: %v", err)
		}
		if out.VerbatimName != "Q & M Dental Surgery" {
			t.Fatalf("got %q, want %q", out.VerbatimName, "Q & M Dental Surgery")
		}
	})

	t.Run("single escape is untouched and still decodes correctly", func(t *testing.T) {
		// `&` here is one backslash + u0026 — a single, well-formed JSON
		// escape; repair must not touch it and json.Unmarshal must still
		// resolve it to a real ampersand.
		raw := json.RawMessage(`{"verbatim_name": "Q \u0026 M Dental Surgery"}`)
		if got := string(RepairDoubleEscapedUnicode(raw)); got != string(raw) {
			t.Fatalf("repair altered a single escape: %q", got)
		}
		var out struct {
			VerbatimName string `json:"verbatim_name"`
		}
		if err := json.Unmarshal(RepairDoubleEscapedUnicode(raw), &out); err != nil {
			t.Fatalf("unmarshal after repair: %v", err)
		}
		if out.VerbatimName != "Q & M Dental Surgery" {
			t.Fatalf("got %q, want %q", out.VerbatimName, "Q & M Dental Surgery")
		}
	})

	t.Run("literal ampersand is untouched", func(t *testing.T) {
		raw := json.RawMessage(`{"verbatim_name": "foo & bar"}`)
		if got := string(RepairDoubleEscapedUnicode(raw)); got != string(raw) {
			t.Fatalf("repair altered a literal ampersand: %q", got)
		}
	})

	t.Run("unrelated escapes pass through untouched", func(t *testing.T) {
		// \\n, \\", \\\\ are not \\u + 4 hex, so the regex must leave them alone.
		raw := json.RawMessage(`{"excerpt": "line one\\nline \\"two\\" \\\\ end"}`)
		if got := string(RepairDoubleEscapedUnicode(raw)); got != string(raw) {
			t.Fatalf("repair altered unrelated escapes: %q", got)
		}
	})
}

func TestNormalizeForVerbatimCheck(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"  hello   world  ", "hello world"},
		{"line one\nline two", "line one line two"},
		{"tabs\tand nbsp", "tabs and nbsp"},
		{"already clean", "already clean"},
	} {
		if got := normalizeForVerbatimCheck(tc.in); got != tc.want {
			t.Errorf("normalizeForVerbatimCheck(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsVerbatimSubstring(t *testing.T) {
	hay := normalizeForVerbatimCheck("Visit National Dental Centre Singapore for specialist care.")
	if !isVerbatimSubstring("National Dental   Centre\nSingapore", hay) {
		t.Error("whitespace-normalized quote should match")
	}
	if isVerbatimSubstring("national dental centre", hay) {
		t.Error("case-different quote must NOT match (verbatim is case-sensitive)")
	}
	if isVerbatimSubstring("", hay) {
		t.Error("empty needle must never be verbatim")
	}
}

func TestValidateExtraction(t *testing.T) {
	const responseText = "For braces, Atlas Dental is a solid choice. NDCS handles complex cases."
	annotation := CitationAnnotation{URL: "https://atlas.example.com/?utm_source=openai", StartIndex: 10, EndIndex: 21}

	valid := ExtractionOutput{
		Entities: []ExtractedEntity{
			{VerbatimName: "Atlas Dental", IsTarget: true, Excerpt: "For braces, Atlas Dental is a solid choice."},
			{VerbatimName: "NDCS", IsTarget: false, Excerpt: "NDCS handles complex cases."},
		},
		Target: &ExtractionTarget{
			Sentiment: "positive",
			Keywords:  []string{"braces"},
			Excerpts:  []string{"Atlas Dental is a solid choice"},
		},
		Citations: []ExtractedCitation{
			{URL: "https://atlas.example.com/?utm_source=openai", Subject: "business"},
		},
	}

	t.Run("valid passes", func(t *testing.T) {
		if errs := ValidateExtraction(valid, responseText, []CitationAnnotation{annotation}); len(errs) != 0 {
			t.Fatalf("expected no errors, got %v", errs)
		}
	})

	t.Run("fabricated verbatim_name", func(t *testing.T) {
		out := valid
		out.Entities = []ExtractedEntity{{VerbatimName: "Ghost Clinic", IsTarget: false, Excerpt: "NDCS handles complex cases."}}
		errs := ValidateExtraction(out, responseText, []CitationAnnotation{annotation})
		if !containsSubstr(errs, "verbatim_name") {
			t.Fatalf("expected a verbatim_name violation, got %v", errs)
		}
	})

	t.Run("fabricated excerpt", func(t *testing.T) {
		out := valid
		out.Entities = []ExtractedEntity{{VerbatimName: "NDCS", IsTarget: false, Excerpt: "NDCS is the cheapest by far."}}
		errs := ValidateExtraction(out, responseText, []CitationAnnotation{annotation})
		if !containsSubstr(errs, "excerpt") {
			t.Fatalf("expected an excerpt violation, got %v", errs)
		}
	})

	t.Run("fabricated citation url", func(t *testing.T) {
		out := valid
		out.Citations = []ExtractedCitation{
			{URL: "https://atlas.example.com/?utm_source=openai", Subject: "business"},
			{URL: "https://made-up.example.com", Subject: "other"},
		}
		errs := ValidateExtraction(out, responseText, []CitationAnnotation{annotation})
		if !containsSubstr(errs, "fabricated") {
			t.Fatalf("expected a fabricated citation violation, got %v", errs)
		}
	})

	t.Run("omitted citation url", func(t *testing.T) {
		out := valid
		out.Citations = nil
		errs := ValidateExtraction(out, responseText, []CitationAnnotation{annotation})
		if !containsSubstr(errs, "missing from the output") {
			t.Fatalf("expected an omitted citation violation, got %v", errs)
		}
	})
}

func containsSubstr(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}
