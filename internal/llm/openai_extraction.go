package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// ExtractionPromptVersion is the version of the extraction prompt + schema,
// recorded per row as result_analyses.extraction_version so a later pass can
// target "re-analyze everything below version N" (design 05). Bump it in the
// same commit as any change to extractionInstructions or extractionJSONSchema.
const ExtractionPromptVersion = 6

const openAIExtractionSchemaName = "result_extraction"

// extractionInstructions is the developer-role prompt. It encodes the design 05
// extraction rules; the literal phrasing may be iterated by the quality gate,
// but the set of rules encoded is fixed. Any edit here bumps
// ExtractionPromptVersion.
const extractionInstructions = `Extract structured facts from a single AI assistant response that answered a consumer's question about local businesses. Never browse, fetch, or infer beyond the response text.

ENTITIES (organizations only)
- List every ORGANISATION the response recommends or discusses — clinics, practices, hospitals, companies. Never list an individual practitioner or employee (e.g. "Dr Tan Wei Ming"), and never list directories, review sites, aggregators, or government bodies; those are citation sources, not entities.
- A recommendation that names only a person, with no organisation, yields NO entity for that recommendation. Practitioner-only mentions are not business mentions.
- List entities in order of their first appearance in the response text.
- verbatim_name and excerpt must each be an exact, character-for-character quote copied from the response text (only incidental whitespace may differ). The excerpt is the sentence where the entity first appears. Do not paraphrase, translate, correct, or fabricate.
- When writing verbatim_name and excerpt strings, output ordinary punctuation (including &) as literal characters — never as a \uXXXX escape sequence.
- is_target: true only if the entity is the target business described below (judge using its name and aliases). This is a hint; when unsure, set false.

TARGET
- Set target to null if the target business is not mentioned at all.
- Otherwise, sentiment/keywords describe HOW THE RESPONSE CHARACTERISES THE TARGET BUSINESS specifically — not the response's overall tone. keywords are descriptors or themes applied to the target.
- Every keyword and the sentiment must be supportable by one of the excerpts. Each excerpt must be an exact quote from the response text (only incidental whitespace may differ).

CITATIONS
- Return exactly one item for every supplied citation occurrence, including repeated occurrences of the same URL, in cite_order. Copy its cite_order and URL exactly.
- links contains one entry for every organisation directly supported by that citation, and [] for general guidance or whenever the relationship is unclear. entity_index identifies the organisation in entities. reference is an exact name, acronym, or shorthand used for that organisation anywhere in the response; never use a pronoun or generic description as the reference. passage is copied exactly from that citation occurrence's supplied evidence_span. The passage need not contain reference when the response uses a pronoun there, but the relationship must still be clear from the response. Never paraphrase. Return multiple links when one occurrence supports multiple organisations.
- Judge subject FROM THE RESPONSE'S OWN SURROUNDING TEXT ONLY: "business" if it supports the target business, "competitor" if it supports another organisation, "other" if neither, "unknown" when unclear. "unknown" is the honest default.

RETRY
- If prior output and validation failures are provided, they list exactly what was wrong. Fix all of them and re-emit the FULL corrected object, not a diff.`

// extractionJSONSchema is the strict-mode structured-output schema (design 05).
// Nullability is expressed via "type":["object","null"] because strict mode
// requires every property to be present and every object to forbid extras.
const extractionJSONSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["entities", "target", "citations"],
  "properties": {
    "entities": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["verbatim_name", "is_target", "excerpt"],
        "properties": {
          "verbatim_name": {"type": "string"},
          "is_target": {"type": "boolean"},
          "excerpt": {"type": "string"}
        }
      }
    },
    "target": {
      "type": ["object", "null"],
      "additionalProperties": false,
      "required": ["sentiment", "keywords", "excerpts"],
      "properties": {
        "sentiment": {"type": "string", "enum": ["positive", "neutral", "negative", "mixed"]},
        "keywords": {"type": "array", "items": {"type": "string"}},
        "excerpts": {"type": "array", "items": {"type": "string"}}
      }
    },
    "citations": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["cite_order", "url", "subject", "links"],
        "properties": {
          "cite_order": {"type": "integer", "minimum": 0},
          "url": {"type": "string"},
          "subject": {"type": "string", "enum": ["business", "competitor", "other", "unknown"]},
          "links": {
            "type": "array",
            "items": {
              "type": "object",
              "additionalProperties": false,
              "required": ["entity_index", "reference", "passage"],
              "properties": {
                "entity_index": {"type": "integer", "minimum": 0},
                "reference": {"type": "string"},
                "passage": {"type": "string"}
              }
            }
          }
        }
      }
    }
  }
}`

var extractionSchema = mustParseJSONSchema(extractionJSONSchema)

func mustParseJSONSchema(s string) json.RawMessage {
	if !json.Valid([]byte(s)) {
		panic("extraction json schema is not valid JSON")
	}
	return json.RawMessage(s)
}

// OpenAIExtractionRunner runs the extraction call through the OpenAI Responses
// API. Same field shape as OpenAIPromptRunner.
type OpenAIExtractionRunner struct {
	openAIResponsesClient
}

// NewOpenAIExtractionRunner returns an OpenAI-backed ExtractionRunner.
func NewOpenAIExtractionRunner(cfg OpenAIConfig) (*OpenAIExtractionRunner, error) {
	client, err := newOpenAIResponsesClient(cfg, "openai analysis model is required")
	if err != nil {
		return nil, err
	}
	return &OpenAIExtractionRunner{openAIResponsesClient: client}, nil
}

// RunExtraction sends one structured-output extraction call and returns the
// model's JSON text (unmarshalled by the caller) plus the reported model id.
// Provider failures reuse the same transient/non-retryable RunnerError
// classification as the execution runner.
func (r *OpenAIExtractionRunner) RunExtraction(ctx context.Context, in ExtractionInput) (ExtractionRunResult, error) {
	if r == nil {
		return ExtractionRunResult{}, errors.New("openai extraction runner is nil")
	}
	if strings.TrimSpace(in.ResponseText) == "" {
		return ExtractionRunResult{}, errors.New("response text is required")
	}

	params, err := r.requestParams(in)
	if err != nil {
		return ExtractionRunResult{}, err
	}
	parsed, body, err := r.call(ctx, params)
	if err != nil {
		return ExtractionRunResult{}, err
	}
	if err := validateOpenAIResponse(parsed, body, "openai extraction response missing reported model", "openai extraction response missing structured output"); err != nil {
		return ExtractionRunResult{}, err
	}

	return ExtractionRunResult{
		RawJSON: json.RawMessage(parsed.Text),
		Model:   strings.TrimSpace(parsed.Model),
	}, nil
}

func (r *OpenAIExtractionRunner) requestParams(in ExtractionInput) (responses.ResponseNewParams, error) {
	userContent, err := marshalExtractionUserContent(in)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}

	input := responses.ResponseInputParam{
		responses.ResponseInputItemParamOfMessage(extractionInstructions, responses.EasyInputMessageRoleDeveloper),
		responses.ResponseInputItemParamOfMessage(userContent, responses.EasyInputMessageRoleUser),
	}
	if len(in.PriorOutputJSON) > 0 || len(in.RetryValidationErrors) > 0 {
		input = append(input,
			responses.ResponseInputItemParamOfMessage(string(in.PriorOutputJSON), responses.EasyInputMessageRoleAssistant),
			responses.ResponseInputItemParamOfMessage(retryContent(in.RetryValidationErrors), responses.EasyInputMessageRoleUser),
		)
	}

	return responses.ResponseNewParams{
		Model: shared.ResponsesModel(r.model),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Store: openai.Bool(false),
		Text: responses.ResponseTextConfigParam{
			Format: strictJSONSchemaFormat(openAIExtractionSchemaName, extractionSchema),
		},
	}, nil
}

// marshalExtractionUserContent serialises the analysis payload the model reads.
func marshalExtractionUserContent(in ExtractionInput) (string, error) {
	type citationView struct {
		CiteOrder    int    `json:"cite_order"`
		URL          string `json:"url"`
		Title        string `json:"title"`
		StartIndex   int    `json:"start_index"`
		EndIndex     int    `json:"end_index"`
		EvidenceSpan string `json:"evidence_span"`
	}
	spans := AttributeCitations(in.ResponseText, in.Citations)
	runes := []rune(in.ResponseText)
	citations := make([]citationView, 0, len(in.Citations))
	for i, c := range OrderCitationAnnotations(in.Citations) {
		evidence := ""
		if i < len(spans) && spans[i].Start >= 0 && spans[i].Start <= spans[i].End && spans[i].End <= len(runes) {
			evidence = string(runes[spans[i].Start:spans[i].End])
		}
		citations = append(citations, citationView{CiteOrder: i, URL: c.URL, Title: c.Title, StartIndex: c.StartIndex, EndIndex: c.EndIndex, EvidenceSpan: evidence})
	}
	aliases := in.BusinessAliases
	if aliases == nil {
		aliases = []string{}
	}

	payload := struct {
		Prompt         string `json:"prompt"`
		ResponseText   string `json:"response_text"`
		TargetBusiness struct {
			Name     string   `json:"name"`
			Aliases  []string `json:"aliases"`
			Category string   `json:"category"`
			Location string   `json:"location"`
		} `json:"target_business"`
		Citations []citationView `json:"citations"`
	}{
		Prompt:       in.Prompt,
		ResponseText: in.ResponseText,
		Citations:    citations,
	}
	payload.TargetBusiness.Name = in.BusinessName
	payload.TargetBusiness.Aliases = aliases
	payload.TargetBusiness.Category = in.BusinessCategory
	payload.TargetBusiness.Location = in.BusinessLocation

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal extraction user content: %w", err)
	}
	return string(raw), nil
}

func retryContent(validationErrors []string) string {
	var b strings.Builder
	b.WriteString("Your previous output failed these validation checks:\n")
	for _, e := range validationErrors {
		b.WriteString("- ")
		b.WriteString(e)
		b.WriteString("\n")
	}
	b.WriteString("Fix all issues and re-emit the full corrected object.")
	return b.String()
}
