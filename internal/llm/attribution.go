package llm

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// NoCitation is the CiteOrder of an entity no citation backs. It is a real and
// common outcome — an intro paragraph, a bullet the model wrote without a
// source, anything after the last citation — and never means "unknown".
const NoCitation = -1

// CitationSpan is one citation annotation paired with the text it backs.
//
// A url_citation annotation does not span the claim it supports: its
// StartIndex/EndIndex cover the inline markdown link the model wrote
// ("([example.com](https://example.com))"), which is a position rather than a
// claim. The text a citation backs is therefore what precedes its marker, which
// is how these answers read — one business per bullet, its source at the end of
// the bullet.
//
// Start and End are rune offsets into the response text, [Start, End).
type CitationSpan struct {
	Annotation CitationAnnotation
	// CiteOrder is the citation's first-appearance rank, which is exactly what
	// citations.cite_order stores, so an attribution can be written as a link to
	// the citation row without carrying its id around.
	CiteOrder  int
	Start, End int
}

// AttributeCitations pairs every annotation with the text it backs. A marker
// backs the text from the previous marker's end, or the start of its own line,
// whichever is later. The line bound is what stops the first bullet's citation
// from claiming the paragraph above it; the previous-marker bound is what splits
// a paragraph that carries two citations.
func AttributeCitations(responseText string, annotations []CitationAnnotation) []CitationSpan {
	runes := []rune(responseText)
	sorted := make([]CitationAnnotation, len(annotations))
	copy(sorted, annotations)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartIndex < sorted[j].StartIndex })

	spans := make([]CitationSpan, 0, len(sorted))
	prevEnd := 0
	for i, a := range sorted {
		start, end := markerBounds(runes, a)
		spanStart := lineStart(runes, start)
		if prevEnd > spanStart {
			spanStart = prevEnd
		}
		if spanStart > start {
			spanStart = start
		}
		spans = append(spans, CitationSpan{Annotation: a, CiteOrder: i, Start: spanStart, End: start})
		if end > prevEnd {
			prevEnd = end
		}
	}
	return spans
}

// markerBounds resolves where an annotation's marker sits in the response text.
//
// The reported indices are character positions, which agree with rune offsets
// for every response we have captured. Rather than assume that, the resolution
// is proven by requiring the marker to contain the annotation's own URL, and
// falls back to locating that URL in the text — which also covers a response
// that cites without writing an inline link.
func markerBounds(runes []rune, a CitationAnnotation) (start, end int) {
	if a.StartIndex >= 0 && a.EndIndex <= len(runes) && a.StartIndex < a.EndIndex &&
		strings.Contains(string(runes[a.StartIndex:a.EndIndex]), a.URL) {
		return a.StartIndex, a.EndIndex
	}
	if i := locateText(string(runes), a.URL); i >= 0 {
		return expandMarker(runes, i, i+utf8.RuneCountInString(a.URL))
	}
	return clamp(a.StartIndex, len(runes)), clamp(a.EndIndex, len(runes))
}

// expandMarker grows a bare URL match to the inline markdown link wrapping it,
// "([label](url))", so the backed text stops before the whole marker rather than
// partway into it. A URL written as plain prose is left as it was found.
func expandMarker(runes []rune, start, end int) (int, int) {
	if start < 2 || runes[start-1] != '(' || runes[start-2] != ']' {
		return start, end
	}
	label := -1
	for j := start - 2; j >= 0; j-- {
		if runes[j] == '[' {
			label = j
			break
		}
	}
	if label < 0 {
		return start, end
	}
	start = label
	if end < len(runes) && runes[end] == ')' {
		end++
	}
	if start > 0 && runes[start-1] == '(' {
		start--
		if end < len(runes) && runes[end] == ')' {
			end++
		}
	}
	return start, end
}

func clamp(i, max int) int {
	switch {
	case i < 0:
		return 0
	case i > max:
		return max
	default:
		return i
	}
}

func lineStart(runes []rune, i int) int {
	for j := clamp(i, len(runes)) - 1; j >= 0; j-- {
		if runes[j] == '\n' {
			return j + 1
		}
	}
	return 0
}

// AttributedEntity is one extracted entity paired with the citation backing the
// text that names it. It is what phase 1 hands phase 2, so the mention row phase
// 2 writes can record which source the answer cited for that business.
type AttributedEntity struct {
	Entity    ExtractedEntity
	CiteOrder int
}

// AttributeEntities attributes every entity to the citation whose span contains
// the text naming it, in the order given. An entity that no span covers, or that
// cannot be located, keeps NoCitation: an attribution we cannot prove is one we
// do not make.
func AttributeEntities(responseText string, entities []ExtractedEntity, spans []CitationSpan) []AttributedEntity {
	out := make([]AttributedEntity, len(entities))
	for i, e := range entities {
		out[i] = AttributedEntity{Entity: e, CiteOrder: NoCitation}
		offset, ok := LocateEntity(responseText, e)
		if !ok {
			continue
		}
		for _, span := range spans {
			if offset >= span.Start && offset < span.End {
				out[i].CiteOrder = span.CiteOrder
				break
			}
		}
	}
	return out
}

// LocateEntity reports where the response names an entity, as a rune offset. It
// prefers the excerpt, which is longer and so less likely to match a different
// sentence than the one the model read, and falls back to the verbatim name.
// ValidateExtraction has already proven both appear verbatim in the response, so
// a miss here means the whitespace-tolerant match disagreed with that check
// rather than that the model invented the name.
func LocateEntity(responseText string, e ExtractedEntity) (int, bool) {
	for _, needle := range []string{e.Excerpt, e.VerbatimName} {
		if i := locateText(responseText, needle); i >= 0 {
			return i, true
		}
	}
	return 0, false
}

// locateText finds needle's first occurrence as a rune offset, forgiving the
// same incidental whitespace reformatting normalizeForVerbatimCheck forgives, so
// a name that passed validation is locatable here.
func locateText(text, needle string) int {
	fields := strings.Fields(needle)
	if len(fields) == 0 {
		return -1
	}
	quoted := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = regexp.QuoteMeta(f)
	}
	pattern, err := regexp.Compile(strings.Join(quoted, `\s+`))
	if err != nil {
		return -1
	}
	loc := pattern.FindStringIndex(text)
	if loc == nil {
		return -1
	}
	return utf8.RuneCountInString(text[:loc[0]])
}
