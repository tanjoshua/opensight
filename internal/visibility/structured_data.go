package visibility

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SiteProfile is the confirmed business profile, as the user reviewed it during
// onboarding. It is the only input to the structured-data snippet: everything
// in the block a user is told to paste is their own approved data, so the
// advice cannot invent a fact about them.
type SiteProfile struct {
	Name    string
	Website string
	Address string
	City    string
	Country string
}

// snippetType is the schema.org type the generated block declares. Resolving a
// specific type per industry would put industry knowledge into the deterministic
// catalog, which is the half that generalizes precisely because it holds none.
// LocalBusiness is valid for any of them, and narrowing it is a step the user is
// told to take.
const snippetType = "LocalBusiness"

type snippetAddress struct {
	Type          string `json:"@type"`
	StreetAddress string `json:"streetAddress,omitempty"`
	Locality      string `json:"addressLocality,omitempty"`
	Country       string `json:"addressCountry,omitempty"`
}

type snippetBody struct {
	Context string          `json:"@context"`
	Type    string          `json:"@type"`
	Name    string          `json:"name"`
	URL     string          `json:"url,omitempty"`
	Address *snippetAddress `json:"address,omitempty"`
}

// structuredDataFields names the schema.org field each dependent check asks
// for, so a folded check can say whether the generated block already answers it.
var structuredDataFields = map[string]string{
	CheckStructuredType:      "@type",
	CheckStructuredAddress:   "address",
	CheckStructuredOpenHours: "openingHours",
}

// structuredDataSteps turns the confirmed profile into a block the user can
// paste, plus the sentence explaining what the one edit settles. It returns no
// steps when there is no profile to build from, leaving the catalog's generic
// fix in place — generic advice beats a snippet with invented values.
func structuredDataSteps(profile SiteProfile, folded []Check) ([]string, string) {
	snippet := StructuredDataSnippet(profile)
	if snippet == "" {
		return nil, ""
	}
	steps := []string{
		"Add this to your homepage inside a <script type=\"application/ld+json\"> tag. Every value is from the profile you confirmed:\n\n" + snippet,
		"Replace \"" + snippetType + "\" with the most specific schema.org type that fits your business.",
	}
	missing := []string{}
	for _, check := range folded {
		field, ok := structuredDataFields[check.Key]
		if ok && !strings.Contains(snippet, `"`+field+`"`) {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		steps = append(steps, "Add "+nameList(missing)+" from your own records — we do not hold "+plural(len(missing), "that field", "those fields")+" in your profile.")
	}
	steps = append(steps, "Most site builders have a plugin or built-in setting for this.")

	detail := ""
	if len(folded) > 0 {
		detail = fmt.Sprintf(" This one block also settles the %d dependent %s on the checklist.", len(folded), plural(len(folded), "check", "checks"))
	}
	return steps, detail
}

// StructuredDataSnippet renders the JSON-LD block for a profile, or "" when
// there is not even a name to put in it. Fields the profile does not hold are
// omitted rather than emitted blank: a placeholder pasted unedited would publish
// an empty claim, which is worse than an absent one.
func StructuredDataSnippet(profile SiteProfile) string {
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		return ""
	}
	body := snippetBody{
		Context: "https://schema.org",
		Type:    snippetType,
		Name:    name,
		URL:     strings.TrimSpace(profile.Website),
	}
	street, city, country := strings.TrimSpace(profile.Address), strings.TrimSpace(profile.City), strings.ToUpper(strings.TrimSpace(profile.Country))
	if street != "" || city != "" || country != "" {
		body.Address = &snippetAddress{Type: "PostalAddress", StreetAddress: street, Locality: city, Country: country}
	}
	out, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return ""
	}
	return string(out)
}
