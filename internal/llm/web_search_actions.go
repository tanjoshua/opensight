package llm

import (
	"encoding/json"
	"strings"
)

// WebSearchAction is one action the model took through the web_search tool,
// lifted from a Responses raw payload's output[] items of type
// "web_search_call". Type is "search" | "open_page" | "find_in_page"; URL is the
// page the model opened/searched within (empty for a plain "search", which
// carries only a query). The onboarding proposer forces at least one such action
// via tool_choice: required.
type WebSearchAction struct {
	Type string
	URL  string
}

// ParseWebSearchActions walks raw_response's output[] collecting web_search_call
// actions in order. It never fetches any URL. Used to (1) build Sources from the
// pages the model opened and (2) decide whether the model actually read the
// business's own site (an open_page on its domain) so the workflow can trust the
// model's low_confidence judgement even when FetchSite failed.
func ParseWebSearchActions(rawResponse json.RawMessage) []WebSearchAction {
	if len(rawResponse) == 0 {
		return nil
	}

	var payload struct {
		Output []struct {
			Type   string `json:"type"`
			Action struct {
				Type string `json:"type"`
				URL  string `json:"url"`
			} `json:"action"`
		} `json:"output"`
	}
	if err := json.Unmarshal(rawResponse, &payload); err != nil {
		return nil
	}

	var actions []WebSearchAction
	for _, item := range payload.Output {
		if item.Type != "web_search_call" {
			continue
		}
		actions = append(actions, WebSearchAction{
			Type: item.Action.Type,
			URL:  strings.TrimSpace(item.Action.URL),
		})
	}
	return actions
}

// SourcesFromWebSearch derives the review-screen source list from the pages the
// model opened while researching (open_page/find_in_page actions with a URL).
//
// Under strict JSON output the model emits no prose, so the response carries no
// url_citation annotations (empirically 0 across every spike run) — the opened
// pages are the only signal of what the model actually read, and are what a user
// wants to see as "sources". URLs are normalized (utm_* stripped, host
// lowercased — the same rule as citation URLs) and deduped by cleaned URL,
// first-appearance order preserved. A plain "search" action has no URL and is
// not a source. Title is left empty; the UI labels each link by its domain.
func SourcesFromWebSearch(rawResponse json.RawMessage) []ProposalSource {
	actions := ParseWebSearchActions(rawResponse)
	if len(actions) == 0 {
		return nil
	}

	seen := map[string]bool{}
	var sources []ProposalSource
	for _, a := range actions {
		if a.URL == "" {
			continue
		}
		clean, domain, err := NormalizeCitationURL(a.URL)
		if err != nil || seen[clean] {
			continue
		}
		seen[clean] = true
		sources = append(sources, ProposalSource{URL: clean, Domain: domain})
	}
	return sources
}

// OpenedSiteDomain reports whether any open_page action opened a page on the
// given website's own host (bare host, case- and www-insensitive). It is how the
// workflow tells "the model read the site itself" apart from "the model only
// searched the name" when FetchSite failed. host is the website URL's host; an
// empty or unparseable host yields false.
func OpenedSiteDomain(actions []WebSearchAction, host string) bool {
	target := normalizeHost(host)
	if target == "" {
		return false
	}
	for _, a := range actions {
		if a.Type != "open_page" || a.URL == "" {
			continue
		}
		_, domain, err := NormalizeCitationURL(a.URL)
		if err != nil {
			continue
		}
		if normalizeHost(domain) == target {
			return true
		}
	}
	return false
}

// normalizeHost lowercases a host and drops a leading "www." so "www.foo.sg" and
// "foo.sg" compare equal. It accepts either a bare host or a full URL.
func normalizeHost(raw string) string {
	h := strings.TrimSpace(strings.ToLower(raw))
	if h == "" {
		return ""
	}
	if strings.Contains(h, "://") {
		if _, domain, err := NormalizeCitationURL(raw); err == nil {
			h = strings.ToLower(domain)
		}
	}
	h = strings.TrimPrefix(h, "www.")
	return h
}
