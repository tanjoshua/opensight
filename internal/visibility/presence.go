package visibility

import (
	"strings"

	"golang.org/x/net/html"

	"opensight/internal/llm"
)

// businessDescriptors are generic words a directory adds to or drops from a
// business name without making it a different business. "St. Anne's Dental" and
// "St Anne's Dental Practice" are one listing; "Summit Dental" and "Summit
// Dental Arts of Beaverton" are two businesses, because "arts of beaverton" is
// not a descriptor. Keeping this list to genuinely generic words is what stops
// the match from merging real rivals.
var businessDescriptors = map[string]bool{
	"the": true, "and": true,
	"practice": true, "clinic": true, "clinics": true, "studio": true,
	"group": true, "centre": true, "center": true, "surgery": true,
	"associates": true, "partners": true, "specialists": true,
	"dentistry": true, "dental": true, "orthodontics": true, "endodontics": true,
	"medical": true, "health": true, "healthcare": true, "care": true,
}

// listedOnPage reports whether a third-party page presents the business as one
// of its own entries. The test is deliberately positional rather than a
// substring search over the whole document: a name in a heading or a list item
// is the page's subject, while a name in prose is just as likely to be a
// "no results for …" message, a rival's comparison advert, or an unrelated
// business whose name happens to contain ours.
func listedOnPage(pageHTML, businessName string) bool {
	want := llm.NormalizeEntityName(businessName)
	if want == "" {
		return false
	}
	for _, candidate := range subjectTexts(pageHTML) {
		if sameBusiness(llm.NormalizeEntityName(candidate), want) {
			return true
		}
	}
	return false
}

// subjectElements are the elements whose text names what a page is about or
// what it lists. Prose and meta content are deliberately excluded.
var subjectElements = map[string]bool{
	"title": true, "h1": true, "h2": true, "h3": true, "li": true, "dt": true,
}

func subjectTexts(pageHTML string) []string {
	doc, err := html.Parse(strings.NewReader(pageHTML))
	if err != nil {
		return nil
	}
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && subjectElements[n.Data] {
			if text := strings.TrimSpace(elementText(n)); text != "" {
				out = append(out, text)
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return out
}

func elementText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// sameBusiness compares two normalized names. They match when they are equal,
// or when one extends the other at a token boundary using only generic
// descriptor words. Comparing tokens rather than characters is what keeps
// "Ace Dental" out of "Palace Dental Group".
func sameBusiness(candidate, want string) bool {
	if candidate == "" || want == "" {
		return false
	}
	if candidate == want {
		return true
	}
	shorter, longer := strings.Fields(candidate), strings.Fields(want)
	if len(shorter) > len(longer) {
		shorter, longer = longer, shorter
	}
	if len(shorter) == 0 || len(longer) == len(shorter) {
		return false
	}
	for i, token := range shorter {
		if longer[i] != token {
			return false
		}
	}
	for _, extra := range longer[len(shorter):] {
		if !businessDescriptors[extra] {
			return false
		}
	}
	return true
}

// normalizeHost folds the www. spelling of a host, so one directory cited under
// two spellings is one source rather than two half-evidenced ones.
func normalizeHost(host string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
}
