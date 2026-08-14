package llm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSourceClassificationMacroQuality is an opt-in provider smoke test for the
// judgment the deterministic suite cannot score: distinguishing an observed
// competitor pattern from a proven cause. It uses the shape of the Macro
// finding that exposed the boundary.
func TestSourceClassificationMacroQuality(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	model := strings.TrimSpace(os.Getenv("OPENAI_IMPROVE_MODEL"))
	if apiKey == "" || model == "" {
		t.Skip("set OPENAI_API_KEY and OPENAI_IMPROVE_MODEL to run the Improve quality gate")
	}
	runner, err := NewOpenAISourceClassifier(OpenAIConfig{APIKey: apiKey, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	analysis, err := runner.ClassifySources(ctx, SourceClassificationInput{
		BusinessName: "Macro Academy",
		SiteContent:  "Branches Upper Thomson · Bukit Timah · Kovan · Siglap. Schedule Multiple weekly slots — confirmed at booking. Class schedule Bukit Timah Plaza. Schedule subject to confirmation. Book a consultation for current availability.",
		Candidates: []SourceCandidate{
			{
				Domain: "indigo.com.sg", Pages: []SourcePage{{URL: "https://indigo.com.sg/programmes-jc/general-paper/", Content: "Indigo Education Group General Paper. Multiple physical class slots are available."}},
				Claims: []SourceClaim{{Owner: "Indigo Education Group", Question: "Which centres offer comprehensive JC tuition for Economics, Mathematics and General Paper?", Passage: "Indigo is attractive if timetable flexibility and a larger operation matter. It has dedicated programmes for all three subjects and several available class slots at some branches.", ResultID: "result-1", PromptID: "prompt-1"}},
			},
			{
				Domain: "www.thelearninglab.com.sg", Pages: []SourcePage{{URL: "https://www.thelearninglab.com.sg/sec-complimentary-lesson-experience", Content: "The Learning Lab complimentary secondary lesson, with physical and online options."}},
				Claims: []SourceClaim{{Owner: "The Learning Lab", Question: "What are the best options in Singapore for upper secondary Additional Mathematics tuition?", Passage: "TLL is useful if you want the flexibility of a larger network and currently has physical and online options for its complimentary secondary lesson.", ResultID: "result-2", PromptID: "prompt-2"}},
			},
			{
				Domain: "highernucleus.com.sg", Pages: []SourcePage{{URL: "https://highernucleus.com.sg/", Content: "Higher Nucleus Learning Studio. Published JC1 and JC2 class timetable by subject."}},
				Claims: []SourceClaim{{Owner: "Higher Nucleus Learning Studio", Question: "Which centres offer comprehensive JC tuition for Economics, Mathematics and General Paper?", Passage: "Its published timetable currently shows dedicated JC1/JC2 classes in Math, Economics and GP.", ResultID: "result-3", PromptID: "prompt-1"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var schedule *ContentOpportunity
	for i := range analysis.Opportunities {
		candidate := &analysis.Opportunities[i]
		searchable := strings.ToLower(candidate.Key + " " + candidate.Title + " " + candidate.Observation)
		if strings.Contains(searchable, "schedule") || strings.Contains(searchable, "timetable") || strings.Contains(searchable, "class time") {
			schedule = candidate
			break
		}
	}
	if schedule == nil {
		t.Fatalf("no schedule opportunity in output: %+v", analysis.Opportunities)
	}
	if schedule.Coverage != "partial" {
		t.Errorf("coverage = %q, want partial", schedule.Coverage)
	}
	combined := strings.ToLower(schedule.Observation + " " + schedule.SiteState + " " + schedule.SuggestedAction)
	for _, unsupported := range []string{"remaining availability", "will improve visibility", "improve rankings", "caused macro"} {
		if strings.Contains(combined, unsupported) {
			t.Errorf("opportunity contains unsupported causal or operational claim %q: %+v", unsupported, schedule)
		}
	}
}
