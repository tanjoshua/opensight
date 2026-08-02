package llm

import (
	"strings"
	"unicode"

	"opensight/internal/domain"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// legalSuffixes are the trailing legal-form tokens dropped during name
// normalization (design 05, Phase 2 step 1). Only genuine legal suffixes are
// listed — meaningful words like "clinic" stay, so "Atlas Clinic" and "Atlas
// Orthopaedics" remain distinct. Ordered longest-first so a longer suffix is
// tried before a shorter one it contains. Each entry is a space-joined token
// sequence matched against the tail of the normalized token list.
var legalSuffixes = [][]string{
	{"private", "limited"},
	{"pte", "ltd"},
	{"llp"},
}

// foldTransformer strips diacritics: NFD decomposes accented runes into base +
// combining marks, the marks (category Mn) are removed, and NFC recomposes.
// "Café" folds to "cafe". Built once — transform.Transformer is reusable but not
// safe for concurrent Reset, so callers go through NormalizeEntityName which
// clones via transform.String.
var foldTransformer = transform.Chain(norm.NFKD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// NormalizeEntityName reduces a verbatim organization name to its exact-match
// key (design 05, Phase 2 step 1): Unicode-fold (strip diacritics, fold
// compatibility forms via NFKD), lowercase, strip punctuation to spaces,
// collapse whitespace, and drop trailing legal suffixes. It never strips
// meaningful words, so distinct businesses that share a token stay distinct.
// An input that is empty or all-punctuation normalizes to "".
func NormalizeEntityName(name string) string {
	folded, _, err := transform.String(foldTransformer, name)
	if err != nil {
		// transform.String only errors on a broken transformer, not on input;
		// fall back to the raw name so normalization degrades rather than panics.
		folded = name
	}
	folded = strings.ToLower(folded)

	// Replace every non-alphanumeric rune with a space, so punctuation both
	// disappears and can't glue tokens together ("A&B" -> "a b", not "ab").
	var b strings.Builder
	b.Grow(len(folded))
	for _, r := range folded {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}

	tokens := strings.Fields(b.String())
	tokens = dropLegalSuffix(tokens)
	return strings.Join(tokens, " ")
}

// dropLegalSuffix removes one trailing legal-suffix token sequence from tokens.
// Only one suffix is dropped (a name has at most one legal form), and never the
// entire name — a bare "Pte Ltd" keeps its tokens rather than normalizing to "".
func dropLegalSuffix(tokens []string) []string {
	for _, suffix := range legalSuffixes {
		if len(suffix) >= len(tokens) {
			continue // would strip the whole name (or more) — leave it intact
		}
		if slicesHaveSuffix(tokens, suffix) {
			return tokens[:len(tokens)-len(suffix)]
		}
	}
	return tokens
}

func slicesHaveSuffix(tokens, suffix []string) bool {
	tail := tokens[len(tokens)-len(suffix):]
	for i := range suffix {
		if tail[i] != suffix[i] {
			return false
		}
	}
	return true
}

// ReconcileTarget carries the target business's exact-match keys. Both the
// business name and its approved aliases are matching keys: a business whose
// name is not also listed as an alias must still self-match, or reconcile would
// mint a competitor row for the business itself.
type ReconcileTarget struct {
	Name    string
	Aliases []string
}

// ReconcileCompetitor is one existing competitor row the exact pass matches
// against. Competitors are supplied regardless of status — dismissed
// competitors still accrue mentions (design 02: dismissal is a display filter),
// so status is deliberately absent here.
type ReconcileCompetitor struct {
	ID      domain.ID
	Name    string
	Aliases []string
}

// MatchSubject is the resolved subject of an extracted entity after the exact
// pass.
type MatchSubject string

const (
	// SubjectSelf is the target business.
	SubjectSelf MatchSubject = "self"
	// SubjectCompetitor is an existing competitor (see ExactMatch.CompetitorID).
	SubjectCompetitor MatchSubject = "competitor"
	// SubjectUnmatched found no exact match; the LLM match pass judges these.
	SubjectUnmatched MatchSubject = "unmatched"
)

// MatchedByExact is the mentions.matched_by value the exact pass records.
const MatchedByExact = "exact"

// ExactMatch is the exact-pass outcome for one extracted entity, kept in the
// input's first-appearance order. MatchedBy is MatchedByExact for a self or
// competitor match and "" when unmatched; CompetitorID is set only for a
// competitor match.
type ExactMatch struct {
	Entity       ExtractedEntity
	Subject      MatchSubject
	CompetitorID domain.ID
	MatchedBy    string
}

// ExactMatchEntities runs Phase 2's deterministic exact pass (design 05, step
// 2) over extracted entities. Each name is normalized, then matched — target
// keys first, then competitors' names and aliases — with the first match
// winning. The model's IsTarget flag is only a hint: an entity self-matches
// solely by matching a target key, so an IsTarget entity whose name fails
// target matching is demoted to whatever competitor/unmatched result the name
// yields, never trusted into a spurious self mention. Entities that normalize
// to "" or match nothing are returned SubjectUnmatched for the LLM pass.
func ExactMatchEntities(entities []ExtractedEntity, target ReconcileTarget, competitors []ReconcileCompetitor) []ExactMatch {
	targetKeys := make(map[string]struct{})
	addKey(targetKeys, target.Name)
	for _, a := range target.Aliases {
		addKey(targetKeys, a)
	}

	// First competitor wins a shared key: build the index in the supplied order
	// and skip keys already claimed (by the target or an earlier competitor).
	competitorKeys := make(map[string]domain.ID)
	for _, c := range competitors {
		for _, key := range append([]string{c.Name}, c.Aliases...) {
			norm := NormalizeEntityName(key)
			if norm == "" {
				continue
			}
			if _, taken := targetKeys[norm]; taken {
				continue
			}
			if _, taken := competitorKeys[norm]; taken {
				continue
			}
			competitorKeys[norm] = c.ID
		}
	}

	matches := make([]ExactMatch, len(entities))
	for i, e := range entities {
		norm := NormalizeEntityName(e.VerbatimName)
		switch {
		case norm == "":
			matches[i] = ExactMatch{Entity: e, Subject: SubjectUnmatched}
		case keyPresent(targetKeys, norm):
			matches[i] = ExactMatch{Entity: e, Subject: SubjectSelf, MatchedBy: MatchedByExact}
		default:
			if id, ok := competitorKeys[norm]; ok {
				matches[i] = ExactMatch{Entity: e, Subject: SubjectCompetitor, CompetitorID: id, MatchedBy: MatchedByExact}
			} else {
				matches[i] = ExactMatch{Entity: e, Subject: SubjectUnmatched}
			}
		}
	}
	return matches
}

func addKey(set map[string]struct{}, raw string) {
	if norm := NormalizeEntityName(raw); norm != "" {
		set[norm] = struct{}{}
	}
}

func keyPresent(set map[string]struct{}, norm string) bool {
	_, ok := set[norm]
	return ok
}
