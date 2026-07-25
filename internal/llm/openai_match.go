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

const openAIMatchSchemaName = "competitor_match"

// matchInstructions is the developer-role prompt for the run's single match call
// (design 05 step 3). The bar is deliberately conservative: a wrong merge
// silently pollutes a competitor's trend, while a wrong split is visible and
// user-fixable, so the model defaults to "no match" unless the evidence is
// strong.
const matchInstructions = `Decide whether each unmatched business name refers to the SAME real-world business as one supplied known competitor. Judge only from the supplied names, aliases, and websites; never browse or infer beyond them.

RULES
- For each name index, decide if it is the SAME real-world business as exactly one competitor. Set competitor_id to that competitor's id ONLY when the evidence is strong (e.g. an obvious abbreviation, a spelling/spacing variant, or a website that plainly belongs to the same business). Otherwise set competitor_id to null.
- When in doubt, return null. A wrong merge is worse than a missed one: it silently corrupts a competitor's history, while a missed match simply creates a new entry the user can merge later.
- competitor_id must be copied exactly from the supplied competitor list. Never invent an id and never guess.
- Emit exactly one entry per name index. Do not add, drop, or reorder indices.`

const matchJSONSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["matches"],
  "properties": {
    "matches": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["index", "competitor_id"],
        "properties": {
          "index": {"type": "integer"},
          "competitor_id": {"type": ["string", "null"]}
        }
      }
    }
  }
}`

var matchSchema = mustParseJSONSchema(matchJSONSchema)

// OpenAIMatchRunner runs the match call through the OpenAI Responses API. Same
// field shape as OpenAIExtractionRunner.
type OpenAIMatchRunner struct {
	openAIResponsesClient
}

// NewOpenAIMatchRunner returns an OpenAI-backed MatchRunner.
func NewOpenAIMatchRunner(cfg OpenAIConfig) (*OpenAIMatchRunner, error) {
	client, err := newOpenAIResponsesClient(cfg, "openai analysis model is required")
	if err != nil {
		return nil, err
	}
	return &OpenAIMatchRunner{openAIResponsesClient: client}, nil
}

// RunMatch sends one structured-output match call and returns the model's JSON
// text (decoded by DecodeMatchOutput) plus the reported model id. Provider
// failures reuse the same RunnerError classification as the other runners.
func (r *OpenAIMatchRunner) RunMatch(ctx context.Context, in MatchInput) (MatchRunResult, error) {
	if r == nil {
		return MatchRunResult{}, errors.New("openai match runner is nil")
	}
	if len(in.Names) == 0 {
		return MatchRunResult{}, errors.New("match input has no names")
	}

	params, err := r.requestParams(in)
	if err != nil {
		return MatchRunResult{}, err
	}
	parsed, body, err := r.call(ctx, params)
	if err != nil {
		return MatchRunResult{}, err
	}
	if err := validateOpenAIResponse(parsed, body, "openai match response missing reported model", "openai match response missing structured output"); err != nil {
		return MatchRunResult{}, err
	}

	return MatchRunResult{
		RawJSON: json.RawMessage(parsed.Text),
		Model:   strings.TrimSpace(parsed.Model),
	}, nil
}

func (r *OpenAIMatchRunner) requestParams(in MatchInput) (responses.ResponseNewParams, error) {
	userContent, err := marshalMatchUserContent(in)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}

	return responses.ResponseNewParams{
		Model: shared.ResponsesModel(r.model),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: responses.ResponseInputParam{
			responses.ResponseInputItemParamOfMessage(matchInstructions, responses.EasyInputMessageRoleDeveloper),
			responses.ResponseInputItemParamOfMessage(userContent, responses.EasyInputMessageRoleUser),
		}},
		Store: openai.Bool(false),
		Text: responses.ResponseTextConfigParam{
			Format: strictJSONSchemaFormat(openAIMatchSchemaName, matchSchema),
		},
	}, nil
}

// marshalMatchUserContent serialises the names (index-referenced) and the
// candidate competitor list the model reads.
func marshalMatchUserContent(in MatchInput) (string, error) {
	type nameView struct {
		Index int    `json:"index"`
		Name  string `json:"name"`
	}
	type competitorView struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Aliases []string `json:"aliases"`
		Website string   `json:"website,omitempty"`
	}

	names := make([]nameView, 0, len(in.Names))
	for i, n := range in.Names {
		names = append(names, nameView{Index: i, Name: n})
	}
	competitors := make([]competitorView, 0, len(in.Competitors))
	for _, c := range in.Competitors {
		aliases := c.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		competitors = append(competitors, competitorView{
			ID:      c.ID.String(),
			Name:    c.Name,
			Aliases: aliases,
			Website: c.Website,
		})
	}

	raw, err := json.Marshal(struct {
		Names       []nameView       `json:"names"`
		Competitors []competitorView `json:"competitors"`
	}{Names: names, Competitors: competitors})
	if err != nil {
		return "", fmt.Errorf("marshal match user content: %w", err)
	}
	return string(raw), nil
}
