package llm

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// CitationAnnotation is one url_citation annotation lifted from a Responses API
// raw payload. StartIndex/EndIndex are byte offsets into the response text, so
// the objective first-appearance order of citations is StartIndex ascending.
type CitationAnnotation struct {
	URL        string
	Title      string
	StartIndex int
	EndIndex   int
}

// ParseCitationAnnotations walks raw_response's output[].content[].annotations[]
// collecting the url_citation annotations, mirroring parseOpenAIResponse's own
// message/content iteration (openai.go) so the annotation indices stay
// consistent with the response_text those same content blocks produce. Non-url
// annotations are ignored. It never fetches any URL.
func ParseCitationAnnotations(rawResponse json.RawMessage) ([]CitationAnnotation, error) {
	if len(rawResponse) == 0 {
		return nil, nil
	}

	var payload struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type        string `json:"type"`
				Annotations []struct {
					Type       string `json:"type"`
					URL        string `json:"url"`
					Title      string `json:"title"`
					StartIndex int    `json:"start_index"`
					EndIndex   int    `json:"end_index"`
				} `json:"annotations"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(rawResponse, &payload); err != nil {
		return nil, fmt.Errorf("parse citation annotations: %w", err)
	}

	var annotations []CitationAnnotation
	for _, output := range payload.Output {
		if output.Type != "message" {
			continue
		}
		for _, content := range output.Content {
			for _, a := range content.Annotations {
				if a.Type != "url_citation" {
					continue
				}
				if strings.TrimSpace(a.URL) == "" {
					continue
				}
				annotations = append(annotations, CitationAnnotation{
					URL:        a.URL,
					Title:      a.Title,
					StartIndex: a.StartIndex,
					EndIndex:   a.EndIndex,
				})
			}
		}
	}
	return annotations, nil
}

// NormalizeCitationURL strips utm_* query parameters (OpenAI appends
// ?utm_source=openai) and lowercases the host, returning the cleaned URL to
// store and the bare domain to group by. Only utm_* params are dropped — some
// publisher URLs need their other query params to resolve. It never fetches the
// URL.
func NormalizeCitationURL(raw string) (cleanURL, domain string, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", fmt.Errorf("citation url is empty")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", "", fmt.Errorf("parse citation url %q: %w", raw, err)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("citation url %q has no host", raw)
	}

	u.Host = strings.ToLower(u.Host)

	if u.RawQuery != "" {
		q := u.Query()
		for key := range q {
			if strings.HasPrefix(strings.ToLower(key), "utm_") {
				q.Del(key)
			}
		}
		u.RawQuery = q.Encode()
	}

	domain = u.Hostname()
	return u.String(), domain, nil
}
