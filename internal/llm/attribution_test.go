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

// TestAttributeCitationsResolvesUnusableIndices proves the URL check is load
// bearing: a response whose indices do not line up with rune offsets is still
// displayed by finding the marker, rather than silently showing the wrong text.
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
