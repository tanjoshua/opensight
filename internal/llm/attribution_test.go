package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAttributeCitationsAgainstCaptures holds the rule to the shape real answers
// actually have. Every captured response cites by writing an inline markdown
// link, so every resolved marker must contain its own URL, and the text a
// citation backs must end where its marker begins and never reach back past the
// line it sits on.
func TestAttributeCitationsAgainstCaptures(t *testing.T) {
	captures, err := filepath.Glob("../../testdata/spk1/0*.json")
	if err != nil || len(captures) == 0 {
		t.Fatalf("glob captures: %v (%d found)", err, len(captures))
	}

	total := 0
	for _, path := range captures {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := captureText(t, raw)
		annotations, err := ParseCitationAnnotations(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		runes := []rune(text)
		for _, span := range AttributeCitations(text, annotations) {
			total++
			name := filepath.Base(path)
			if span.Start < 0 || span.End > len(runes) || span.Start > span.End {
				t.Fatalf("%s: span [%d,%d) out of range for %d runes", name, span.Start, span.End, len(runes))
			}
			// The marker begins where the backed text ends; proving the marker is
			// the model's own link is what makes that boundary meaningful.
			marker := string(runes[span.End:min(span.End+len(span.Annotation.URL)+64, len(runes))])
			if !strings.Contains(marker, span.Annotation.URL) {
				t.Errorf("%s: text at span end %d does not start the marker for %s", name, span.End, span.Annotation.URL)
			}
			if strings.Contains(string(runes[span.Start:span.End]), "\n") {
				t.Errorf("%s: span [%d,%d) reaches past its own line", name, span.Start, span.End)
			}
		}
	}
	if total == 0 {
		t.Fatal("captures produced no citation spans")
	}
}

// TestAttributeEntitiesFromCapture labels one real answer by hand: the source
// each business is cited from is the whole point of the attribution, so it is
// worth asserting against a response nobody wrote for this test.
func TestAttributeEntitiesFromCapture(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/spk1/01-category-dental.json")
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	text := captureText(t, raw)
	annotations, err := ParseCitationAnnotations(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("parse annotations: %v", err)
	}
	spans := AttributeCitations(text, annotations)

	for _, tc := range []struct{ name, wantDomain string }{
		{"National Dental Centre Singapore", "ndcs.com.sg"},
		{"National University Centre for Oral Health, Singapore", "nucohs.com.sg"},
		{"Specialist Dental Group", "specialistdentalgroup.com"},
	} {
		attributed := AttributeEntities(text, []ExtractedEntity{{VerbatimName: tc.name, Excerpt: tc.name}}, spans)
		if attributed[0].CiteOrder == NoCitation {
			t.Errorf("%s: no citation attributed", tc.name)
			continue
		}
		if got := spans[attributed[0].CiteOrder].Annotation.URL; !strings.Contains(got, tc.wantDomain) {
			t.Errorf("%s attributed to %s, want %s", tc.name, got, tc.wantDomain)
		}
	}
}

// TestAttributeEntitiesBoundaries covers the shapes the captures do not contain,
// where a wrong boundary would silently credit a source for a business it never
// backed.
func TestAttributeEntitiesBoundaries(t *testing.T) {
	const text = "Several clinics are worth a look, including Prologue Dental.\n" +
		"- Alpha Dental is the established choice. ([alpha.example](https://alpha.example/a))\n" +
		"- Beta Dental is newer. ([dir.example](https://dir.example/list)) Gamma Dental shares the entry. ([dir.example](https://dir.example/list))\n" +
		"- Delta Dental has no source at all."

	annotations := []CitationAnnotation{
		{URL: "https://alpha.example/a", StartIndex: idx(text, "([alpha.example]"), EndIndex: idx(text, "([alpha.example]") + len("([alpha.example](https://alpha.example/a))")},
		{URL: "https://dir.example/list", StartIndex: idx(text, "([dir.example]"), EndIndex: idx(text, "([dir.example]") + len("([dir.example](https://dir.example/list))")},
		{URL: "https://dir.example/list", StartIndex: lastIdx(text, "([dir.example]"), EndIndex: lastIdx(text, "([dir.example]") + len("([dir.example](https://dir.example/list))")},
	}
	spans := AttributeCitations(text, annotations)

	for _, tc := range []struct {
		name          string
		wantCiteOrder int
		why           string
	}{
		{"Prologue Dental", NoCitation, "named in the intro, before any citation"},
		{"Alpha Dental", 0, "its own bullet's source"},
		{"Beta Dental", 1, "the first of the two markers on its line"},
		{"Gamma Dental", 2, "the second marker, not the first — the previous marker bounds the span"},
		{"Delta Dental", NoCitation, "its bullet cites nothing"},
	} {
		got := AttributeEntities(text, []ExtractedEntity{{VerbatimName: tc.name, Excerpt: tc.name}}, spans)
		if got[0].CiteOrder != tc.wantCiteOrder {
			t.Errorf("%s cite order = %d, want %d (%s)", tc.name, got[0].CiteOrder, tc.wantCiteOrder, tc.why)
		}
	}
}

// TestAttributeCitationsResolvesUnusableIndices proves the URL check is load
// bearing: a response whose indices do not line up with rune offsets is still
// attributed by finding the marker, rather than silently backing the wrong text.
func TestAttributeCitationsResolvesUnusableIndices(t *testing.T) {
	const text = "Alpha Dental leads the list. ([alpha.example](https://alpha.example/a))"
	spans := AttributeCitations(text, []CitationAnnotation{
		{URL: "https://alpha.example/a", StartIndex: 900, EndIndex: 999},
	})
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if got := text[spans[0].Start:spans[0].End]; got != "Alpha Dental leads the list. " {
		t.Errorf("span text = %q, want the prose before the marker", got)
	}
}

func TestLocateEntityToleratesWhitespaceReformatting(t *testing.T) {
	const text = "Best options:\n- Alpha\n  Dental Practice is nearby."
	// The excerpt a model returns is whitespace-normalized; the response wrapped
	// the same name across a line, which must still locate.
	offset, ok := LocateEntity(text, ExtractedEntity{Excerpt: "Alpha Dental Practice is nearby."})
	if !ok {
		t.Fatal("excerpt did not locate")
	}
	if got := []rune(text)[offset]; got != 'A' {
		t.Errorf("offset %d points at %q, want the start of the excerpt", offset, string(got))
	}
}

func captureText(t *testing.T, raw []byte) string {
	t.Helper()
	var payload struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode capture: %v", err)
	}
	var b strings.Builder
	for _, output := range payload.Output {
		if output.Type != "message" {
			continue
		}
		for _, content := range output.Content {
			b.WriteString(content.Text)
		}
	}
	return b.String()
}

func idx(text, needle string) int { return len([]rune(text[:strings.Index(text, needle)])) }
func lastIdx(text, needle string) int {
	return len([]rune(text[:strings.LastIndex(text, needle)]))
}
