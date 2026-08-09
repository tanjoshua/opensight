package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type extractionSequence struct {
	outputs []json.RawMessage
	calls   int
	inputs  []ExtractionInput
}

func (s *extractionSequence) RunExtraction(_ context.Context, in ExtractionInput) (ExtractionRunResult, error) {
	s.inputs = append(s.inputs, in)
	out := s.outputs[s.calls]
	s.calls++
	return ExtractionRunResult{RawJSON: out, Model: "fake-mini"}, nil
}

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

func TestNDCSCitationShiftToTwinCityFailsAndRetries(t *testing.T) {
	const url = "https://www.twincityendo.com.sg/services"
	const marker = "([Twin City](https://www.twincityendo.com.sg/services))"
	const response = "National Dental Centre Singapore (NDCS) provides public specialist care.\nTwin City Endodontics focuses on root canal treatment. " + marker
	start := strings.Index(response, marker)
	annotations := []CitationAnnotation{{URL: url, StartIndex: start, EndIndex: start + len(marker)}}
	entities := `[{"verbatim_name":"National Dental Centre Singapore (NDCS)","is_target":false,"excerpt":"National Dental Centre Singapore (NDCS) provides public specialist care."},{"verbatim_name":"Twin City Endodontics","is_target":false,"excerpt":"Twin City Endodontics focuses on root canal treatment."}]`
	shifted := json.RawMessage(`{"entities":` + entities + `,"target":null,"citations":[{"cite_order":0,"url":"` + url + `","subject":"competitor","links":[{"entity_index":0,"reference":"Twin City Endodontics","passage":"Twin City Endodontics focuses on root canal treatment."}]}]}`)
	corrected := json.RawMessage(`{"entities":` + entities + `,"target":null,"citations":[{"cite_order":0,"url":"` + url + `","subject":"competitor","links":[{"entity_index":1,"reference":"Twin City Endodontics","passage":"Twin City Endodontics focuses on root canal treatment."}]}]}`)
	runner := &extractionSequence{outputs: []json.RawMessage{shifted, corrected}}

	result, err := ExtractWithRetry(context.Background(), runner, ExtractionInput{ResponseText: response, Citations: annotations}, response, annotations)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Analyzed || runner.calls != 2 {
		t.Fatalf("analyzed=%v calls=%d, want corrected second attempt", result.Analyzed, runner.calls)
	}
	if len(runner.inputs[1].RetryValidationErrors) == 0 || !containsSubstr(runner.inputs[1].RetryValidationErrors, "does not match entity[0]") {
		t.Fatalf("shifted mapping did not drive retry: %+v", runner.inputs[1].RetryValidationErrors)
	}
	linked := LinkEntities(result.Output)
	if len(linked[0].CiteOrders) != 0 || len(linked[1].CiteOrders) != 1 {
		t.Fatalf("corrected links = %+v", linked)
	}
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
	const citationURL = "https://atlas.example.com/?utm_source=openai"
	const marker = "([source](https://atlas.example.com/?utm_source=openai))"
	const responseText = "For braces, Atlas Dental is a solid choice. " + marker + "\nNDCS handles complex cases."
	annotation := CitationAnnotation{CiteOrder: 0, URL: citationURL, StartIndex: strings.Index(responseText, marker), EndIndex: strings.Index(responseText, marker) + len(marker)}

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
			{CiteOrder: 0, URL: citationURL, Subject: "business", Links: []CitationEntityLink{{EntityIndex: 0, Reference: "Atlas Dental", Passage: "For braces, Atlas Dental is a solid choice."}}},
		},
	}

	t.Run("valid passes", func(t *testing.T) {
		if errs := ValidateExtraction(valid, responseText, []CitationAnnotation{annotation}); len(errs) != 0 {
			t.Fatalf("expected no errors, got %v", errs)
		}
	})

	t.Run("fabricated verbatim_name", func(t *testing.T) {
		out := cloneExtraction(t, valid)
		out.Entities = []ExtractedEntity{{VerbatimName: "Ghost Clinic", IsTarget: false, Excerpt: "NDCS handles complex cases."}}
		errs := ValidateExtraction(out, responseText, []CitationAnnotation{annotation})
		if !containsSubstr(errs, "verbatim_name") {
			t.Fatalf("expected a verbatim_name violation, got %v", errs)
		}
	})

	t.Run("fabricated excerpt", func(t *testing.T) {
		out := cloneExtraction(t, valid)
		out.Entities = []ExtractedEntity{{VerbatimName: "NDCS", IsTarget: false, Excerpt: "NDCS is the cheapest by far."}}
		errs := ValidateExtraction(out, responseText, []CitationAnnotation{annotation})
		if !containsSubstr(errs, "excerpt") {
			t.Fatalf("expected an excerpt violation, got %v", errs)
		}
	})

	t.Run("url disagrees with occurrence", func(t *testing.T) {
		out := cloneExtraction(t, valid)
		out.Citations = []ExtractedCitation{
			{CiteOrder: 0, URL: "https://made-up.example.com", Subject: "other", Links: []CitationEntityLink{}},
		}
		errs := ValidateExtraction(out, responseText, []CitationAnnotation{annotation})
		if !containsSubstr(errs, "does not match annotation occurrence") {
			t.Fatalf("expected an occurrence URL violation, got %v", errs)
		}
	})

	t.Run("omitted citation url", func(t *testing.T) {
		out := cloneExtraction(t, valid)
		out.Citations = nil
		errs := ValidateExtraction(out, responseText, []CitationAnnotation{annotation})
		if !containsSubstr(errs, "occurrences") {
			t.Fatalf("expected an omitted citation violation, got %v", errs)
		}
	})

	t.Run("repeated URL occurrences are required separately and in order", func(t *testing.T) {
		const repeated = "Atlas Dental is recommended. " + marker + " Atlas Dental has late hours. " + marker
		first := strings.Index(repeated, marker)
		second := strings.LastIndex(repeated, marker)
		annotations := []CitationAnnotation{{URL: annotation.URL, StartIndex: second, EndIndex: second + len(marker)}, {URL: annotation.URL, StartIndex: first, EndIndex: first + len(marker)}}
		out := cloneExtraction(t, valid)
		out.Entities = []ExtractedEntity{{VerbatimName: "Atlas Dental", Excerpt: "Atlas Dental is recommended."}}
		out.Citations = []ExtractedCitation{
			{CiteOrder: 0, URL: annotation.URL, Subject: "business", Links: []CitationEntityLink{{EntityIndex: 0, Reference: "Atlas Dental", Passage: "Atlas Dental is recommended."}}},
			{CiteOrder: 1, URL: annotation.URL, Subject: "competitor", Links: []CitationEntityLink{{EntityIndex: 0, Reference: "Atlas Dental", Passage: "Atlas Dental has late hours."}}},
		}
		out.Target = nil
		if errs := ValidateExtraction(out, repeated, annotations); len(errs) != 0 {
			t.Fatalf("repeated occurrences rejected: %v", errs)
		}
		out.Citations[1].CiteOrder = 0
		if errs := ValidateExtraction(out, repeated, annotations); !containsSubstr(errs, "cite_order") {
			t.Fatalf("duplicate order accepted: %v", errs)
		}
	})

	t.Run("entity indexes are unique and in range", func(t *testing.T) {
		out := cloneExtraction(t, valid)
		out.Citations[0].Links = []CitationEntityLink{
			{EntityIndex: 0, Reference: "Atlas Dental", Passage: "For braces, Atlas Dental is a solid choice."},
			{EntityIndex: 0, Reference: "Atlas Dental", Passage: "For braces, Atlas Dental is a solid choice."},
			{EntityIndex: 2, Reference: "Ghost", Passage: "For braces, Atlas Dental is a solid choice."},
			{EntityIndex: -1, Reference: "Ghost", Passage: "For braces, Atlas Dental is a solid choice."},
		}
		errs := ValidateExtraction(out, responseText, []CitationAnnotation{annotation})
		if !containsSubstr(errs, "duplicate index") || !containsSubstr(errs, "must reference") {
			t.Fatalf("expected duplicate and range violations, got %v", errs)
		}
	})

	t.Run("empty, one-to-many, and many-to-one links", func(t *testing.T) {
		const oneMarker = "([one](https://one.example))"
		const twoMarker = "([two](https://two.example))"
		const linkedResponse = "General advice. " + oneMarker + "\nAtlas Dental and NDCS offer care. " + twoMarker
		oneStart := strings.Index(linkedResponse, oneMarker)
		twoStart := strings.Index(linkedResponse, twoMarker)
		annotations := []CitationAnnotation{{URL: "https://one.example", StartIndex: oneStart, EndIndex: oneStart + len(oneMarker)}, {URL: "https://two.example", StartIndex: twoStart, EndIndex: twoStart + len(twoMarker)}}
		out := cloneExtraction(t, valid)
		out.Entities = []ExtractedEntity{{VerbatimName: "Atlas Dental", Excerpt: "Atlas Dental and NDCS offer care."}, {VerbatimName: "NDCS", Excerpt: "Atlas Dental and NDCS offer care."}}
		out.Target = nil
		out.Citations = []ExtractedCitation{
			{CiteOrder: 0, URL: "https://one.example", Subject: "other", Links: []CitationEntityLink{}},
			{CiteOrder: 1, URL: "https://two.example", Subject: "competitor", Links: []CitationEntityLink{{EntityIndex: 0, Reference: "Atlas Dental", Passage: "Atlas Dental and NDCS offer care."}, {EntityIndex: 1, Reference: "NDCS", Passage: "Atlas Dental and NDCS offer care."}}},
		}
		if errs := ValidateExtraction(out, linkedResponse, annotations); len(errs) != 0 {
			t.Fatalf("empty/one-to-many links rejected: %v", errs)
		}
		out.Citations[0].Links = []CitationEntityLink{{EntityIndex: 0, Reference: "Atlas Dental", Passage: "General advice."}}
		linked := LinkEntities(out)
		if len(linked[0].CiteOrders) != 2 || len(linked[1].CiteOrders) != 1 {
			t.Fatalf("many-to-one inversion = %+v", linked)
		}
	})

	t.Run("shifted entity mapping is rejected", func(t *testing.T) {
		out := cloneExtraction(t, valid)
		out.Citations[0].Links[0] = CitationEntityLink{EntityIndex: 1, Reference: "Atlas Dental", Passage: "For braces, Atlas Dental is a solid choice."}
		if errs := ValidateExtraction(out, responseText, []CitationAnnotation{annotation}); !containsSubstr(errs, "does not match entity[1]") {
			t.Fatalf("shifted mapping accepted: %v", errs)
		}
	})

	t.Run("passage outside citation span is rejected", func(t *testing.T) {
		out := cloneExtraction(t, valid)
		out.Citations[0].Links[0].Passage = "NDCS handles complex cases."
		if errs := ValidateExtraction(out, responseText, []CitationAnnotation{annotation}); !containsSubstr(errs, "outside citation") {
			t.Fatalf("outside passage accepted: %v", errs)
		}
	})

	t.Run("acronym reference matches entity", func(t *testing.T) {
		out := cloneExtraction(t, valid)
		out.Entities[0].VerbatimName = "National Dental Centre Singapore (NDCS)"
		out.Entities[0].Excerpt = "National Dental Centre Singapore (NDCS) is established."
		out.Entities = out.Entities[:1]
		const acronymText = "National Dental Centre Singapore (NDCS) is established. " + marker
		out.Citations[0].Links[0] = CitationEntityLink{EntityIndex: 0, Reference: "NDCS", Passage: "National Dental Centre Singapore (NDCS) is established."}
		a := annotation
		a.StartIndex = strings.Index(acronymText, marker)
		a.EndIndex = a.StartIndex + len(marker)
		out.Target = nil
		if errs := ValidateExtraction(out, acronymText, []CitationAnnotation{a}); len(errs) != 0 {
			t.Fatalf("acronym rejected: %v", errs)
		}
	})
}

func cloneExtraction(t *testing.T, in ExtractionOutput) ExtractionOutput {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out ExtractionOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func containsSubstr(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}
