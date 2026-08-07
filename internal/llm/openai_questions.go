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

const openAIQuestionsSchemaName = "customer_questions"

// questionsInstructions is the developer-role prompt for the on-demand
// question-generation call (design 03, "Prompt generation rules"). Unlike
// ProposeProfile this call does no research — the profile is already reviewed
// — so there is no web_search tool and no evidence-gathering guidance here.
const questionsInstructions = `Generate exactly prompt_count varied, natural questions a prospective customer might ask when finding or choosing this type of provider in the given city. Ground service-specific questions in the supplied category and services. You may include ordinary decision factors such as suitability, reputation, cost, availability, and comparisons, but do not invent facts about a particular business.`

// questionsJSONSchema is the strict-mode structured-output schema. Strict mode
// does not support array length keywords, so the exact question count is
// enforced only by ValidateQuestions (same posture as propose-profile's
// prompts previously were).
const questionsJSONSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["prompts"],
  "properties": {
    "prompts": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["text"],
        "properties": {
          "text": {"type": "string"}
        }
      }
    }
  }
}`

var questionsSchema = mustParseJSONSchema(questionsJSONSchema)

// OpenAIQuestionsRunner runs the question-generation call through the OpenAI
// Responses API. No web_search tool is attached — the model drafts purely from
// the supplied category/services/city, so this is a cheap non-reasoning model
// (OPENAI_QUESTIONS_MODEL), unlike the reasoning model ProposeProfile uses.
type OpenAIQuestionsRunner struct {
	openAIResponsesClient
}

// NewOpenAIQuestionsRunner returns an OpenAI-backed QuestionsRunner.
func NewOpenAIQuestionsRunner(cfg OpenAIConfig) (*OpenAIQuestionsRunner, error) {
	client, err := newOpenAIResponsesClient(cfg, "openai questions model is required")
	if err != nil {
		return nil, err
	}
	return &OpenAIQuestionsRunner{openAIResponsesClient: client}, nil
}

// RunQuestions sends one structured-output question-generation call and
// returns the model's JSON text (decoded by DecodeQuestionsOutput) plus the
// reported model id. Provider failures reuse the same RunnerError
// classification as the other runners.
func (r *OpenAIQuestionsRunner) RunQuestions(ctx context.Context, in QuestionsInput) (QuestionsRunResult, error) {
	if r == nil {
		return QuestionsRunResult{}, errors.New("openai questions runner is nil")
	}

	params, err := r.requestParams(in)
	if err != nil {
		return QuestionsRunResult{}, err
	}
	parsed, body, err := r.call(ctx, params)
	if err != nil {
		return QuestionsRunResult{}, err
	}
	if err := validateOpenAIResponse(parsed, body, "openai questions response missing reported model", "openai questions response missing structured output"); err != nil {
		return QuestionsRunResult{}, err
	}

	return QuestionsRunResult{
		RawJSON: json.RawMessage(parsed.Text),
		Model:   strings.TrimSpace(parsed.Model),
	}, nil
}

func (r *OpenAIQuestionsRunner) requestParams(in QuestionsInput) (responses.ResponseNewParams, error) {
	userContent, err := marshalQuestionsUserContent(in)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}

	input := responses.ResponseInputParam{
		responses.ResponseInputItemParamOfMessage(questionsInstructions, responses.EasyInputMessageRoleDeveloper),
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
			Format: strictJSONSchemaFormat(openAIQuestionsSchemaName, questionsSchema),
		},
	}, nil
}

// marshalQuestionsUserContent serialises the (already-reviewed) profile
// fields the model drafts from. prompt_count is in.PromptLimit
// (billing.Plan.PromptLimit — never hardcoded).
func marshalQuestionsUserContent(in QuestionsInput) (string, error) {
	services := in.Services
	if services == nil {
		services = []string{}
	}
	payload := struct {
		Category    string   `json:"category"`
		Services    []string `json:"services"`
		City        string   `json:"city"`
		PromptCount int      `json:"prompt_count"`
	}{
		Category:    in.Category,
		Services:    services,
		City:        in.City,
		PromptCount: in.PromptLimit,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal questions user content: %w", err)
	}
	return string(raw), nil
}
