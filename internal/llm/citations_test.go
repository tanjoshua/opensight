package llm

import (
	"encoding/json"
	"os"
	"testing"
)

func TestParseCitationAnnotationsFromCapture(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/spk1/01-category-dental.json")
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}

	annotations, err := ParseCitationAnnotations(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("ParseCitationAnnotations: %v", err)
	}
	if len(annotations) == 0 {
		t.Fatal("no annotations parsed from a capture that has url_citation annotations")
	}

	// Every parsed annotation must be a usable url_citation: non-empty URL and a
	// non-negative index range.
	for i, a := range annotations {
		if a.CiteOrder != i {
			t.Errorf("annotation[%d].cite_order = %d", i, a.CiteOrder)
		}
		if a.URL == "" {
			t.Errorf("annotation[%d] has empty URL", i)
		}
		if a.StartIndex < 0 || a.EndIndex < a.StartIndex {
			t.Errorf("annotation[%d] index range = [%d,%d]", i, a.StartIndex, a.EndIndex)
		}
	}
}

func TestNormalizeCitationURL(t *testing.T) {
	for _, tc := range []struct {
		name       string
		raw        string
		wantURL    string
		wantDomain string
	}{
		{
			name:       "strips utm and lowercases host",
			raw:        "https://WWW.NDCS.com.sg/about-ndcs/about-us?utm_source=openai",
			wantURL:    "https://www.ndcs.com.sg/about-ndcs/about-us",
			wantDomain: "www.ndcs.com.sg",
		},
		{
			name:       "preserves non-utm query params",
			raw:        "https://example.com/search?q=dentist&utm_medium=email",
			wantURL:    "https://example.com/search?q=dentist",
			wantDomain: "example.com",
		},
		{
			name:       "no query untouched",
			raw:        "https://clinic.example.com/team",
			wantURL:    "https://clinic.example.com/team",
			wantDomain: "clinic.example.com",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotDomain, err := NormalizeCitationURL(tc.raw)
			if err != nil {
				t.Fatalf("NormalizeCitationURL: %v", err)
			}
			if gotURL != tc.wantURL {
				t.Errorf("url = %q, want %q", gotURL, tc.wantURL)
			}
			if gotDomain != tc.wantDomain {
				t.Errorf("domain = %q, want %q", gotDomain, tc.wantDomain)
			}
		})
	}
}
