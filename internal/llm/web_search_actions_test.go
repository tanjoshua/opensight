package llm

import (
	"encoding/json"
	"testing"
)

// rawResponseWithActions mirrors the shape the spike observed: web_search_call
// items carrying an action{type,url}, then the final message. The utm param and
// duplicate open_page exercise normalization and dedupe.
const rawResponseWithActions = `{
  "status": "completed",
  "model": "gpt-5.6-luna",
  "output": [
    {"type": "web_search_call", "action": {"type": "search", "query": "roots endodontics"}},
    {"type": "web_search_call", "action": {"type": "open_page", "url": "https://rootsendo.sg?utm_source=openai"}},
    {"type": "web_search_call", "action": {"type": "open_page", "url": "https://www.healthhub.sg/directory/clinic"}},
    {"type": "web_search_call", "action": {"type": "open_page", "url": "https://rootsendo.sg"}},
    {"type": "message", "content": [{"type": "output_text", "text": "{}"}]}
  ]
}`

func TestParseWebSearchActions(t *testing.T) {
	actions := ParseWebSearchActions(json.RawMessage(rawResponseWithActions))
	if len(actions) != 4 {
		t.Fatalf("actions = %d, want 4", len(actions))
	}
	if actions[0].Type != "search" || actions[0].URL != "" {
		t.Errorf("action[0] = %+v, want a URL-less search", actions[0])
	}
	if actions[1].Type != "open_page" || actions[1].URL == "" {
		t.Errorf("action[1] = %+v, want an open_page with a URL", actions[1])
	}
	if ParseWebSearchActions(nil) != nil {
		t.Error("empty response should yield no actions")
	}
}

func TestSourcesFromWebSearch(t *testing.T) {
	sources := SourcesFromWebSearch(json.RawMessage(rawResponseWithActions))
	// The URL-less search is dropped; the two rootsendo URLs collapse to one after
	// utm stripping; the directory page is the other. First-appearance order.
	if len(sources) != 2 {
		t.Fatalf("sources = %d (%+v), want 2", len(sources), sources)
	}
	if sources[0].URL != "https://rootsendo.sg" || sources[0].Domain != "rootsendo.sg" {
		t.Errorf("sources[0] = %+v, want cleaned rootsendo.sg", sources[0])
	}
	if sources[1].Domain != "www.healthhub.sg" {
		t.Errorf("sources[1].Domain = %q, want www.healthhub.sg", sources[1].Domain)
	}
}

func TestOpenedSiteDomain(t *testing.T) {
	actions := ParseWebSearchActions(json.RawMessage(rawResponseWithActions))
	// The model opened rootsendo.sg — www-insensitive and via the full website URL.
	if !OpenedSiteDomain(actions, "https://www.rootsendo.sg/") {
		t.Error("want OpenedSiteDomain true for the site the model opened")
	}
	if OpenedSiteDomain(actions, "https://someother.example") {
		t.Error("want false for a site the model never opened")
	}
	// A search-only run (no open_page) never counts as reading the site.
	searchOnly := []WebSearchAction{{Type: "search"}}
	if OpenedSiteDomain(searchOnly, "https://rootsendo.sg") {
		t.Error("search-only actions must not count as opening the site")
	}
}
