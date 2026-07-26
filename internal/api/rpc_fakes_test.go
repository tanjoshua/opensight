package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"opensight/internal/domain"
	"opensight/internal/metrics"
	"opensight/internal/store"

	enumspb "go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// This file holds the fake store/metrics doubles and fixture IDs shared across
// the *_rpc_test.go files. They used to live alongside their REST-handler
// counterparts (deleted in RPC-8); RPC-8 relocated them here since the RPC
// test suite depends on them directly.

const (
	businessIDForTest = "01950000-0000-7000-8000-0000000000c3"
	runIDForTest      = "01950000-0000-7000-8000-0000000000d4"
	promptIDForTest   = "01950000-0000-7000-8000-0000000000e5"
	resultIDForTest   = "01950000-0000-7000-8000-0000000000f6"
)

// -- BusinessService fakes (business_rpc_test.go) --

type fakePlanStore struct {
	plan store.Plan
	err  error
}

func (f *fakePlanStore) GetTenantPlan(context.Context, domain.ID) (store.Plan, error) {
	return f.plan, f.err
}

type fakeProposalStore struct {
	proposal   store.ProfileProposal
	getErr     error
	discardErr error
	discards   int
}

func (f *fakeProposalStore) GetPending(context.Context, domain.ID, domain.ID) (store.ProfileProposal, error) {
	return f.proposal, f.getErr
}

func (f *fakeProposalStore) DiscardPending(context.Context, domain.ID, domain.ID) error {
	f.discards++
	return f.discardErr
}

type fakeTemporalClient struct {
	execErr        error
	started        []client.StartWorkflowOptions
	describeStatus enumspb.WorkflowExecutionStatus
	describeErr    error
	queryStage     string
	queryErr       error
	schedule       *fakeScheduleClient
}

func (f *fakeTemporalClient) ExecuteWorkflow(_ context.Context, opts client.StartWorkflowOptions, _ interface{}, _ ...interface{}) (client.WorkflowRun, error) {
	f.started = append(f.started, opts)
	return nil, f.execErr
}

func (f *fakeTemporalClient) DescribeWorkflowExecution(context.Context, string, string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	return &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: f.describeStatus},
	}, nil
}

func (f *fakeTemporalClient) QueryWorkflow(context.Context, string, string, string, ...interface{}) (converter.EncodedValue, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return stageValue(f.queryStage), nil
}

// stageValue adapts a plain string into the converter.EncodedValue that
// client.QueryWorkflow returns, so tests can inject a stage without the real
// Temporal payload machinery.
type stageValue string

func (v stageValue) HasValue() bool { return v != "" }

func (v stageValue) Get(valuePtr interface{}) error {
	if p, ok := valuePtr.(*string); ok {
		*p = string(v)
	}
	return nil
}

func (f *fakeTemporalClient) ScheduleClient() client.ScheduleClient {
	if f.schedule == nil {
		f.schedule = &fakeScheduleClient{}
	}
	return f.schedule
}

// fakeScheduleClient records Create calls and can inject an error, and hands
// out a fakeScheduleHandle for GetHandle (RUNS-2's next_run_at lookup). The
// embedded nil client.ScheduleClient satisfies the rest of the interface
// (never called in these tests).
type fakeScheduleClient struct {
	client.ScheduleClient
	creates      int
	err          error
	describeErr  error
	nextActionAt []time.Time
}

func (f *fakeScheduleClient) Create(context.Context, client.ScheduleOptions) (client.ScheduleHandle, error) {
	f.creates++
	return nil, f.err
}

func (f *fakeScheduleClient) GetHandle(context.Context, string) client.ScheduleHandle {
	return &fakeScheduleHandle{describeErr: f.describeErr, nextActionAt: f.nextActionAt}
}

// fakeScheduleHandle implements just Describe; the embedded nil
// client.ScheduleHandle satisfies the rest of the interface.
type fakeScheduleHandle struct {
	client.ScheduleHandle
	describeErr  error
	nextActionAt []time.Time
}

func (f *fakeScheduleHandle) Describe(context.Context) (*client.ScheduleDescription, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	return &client.ScheduleDescription{
		Schedule: client.Schedule{},
		Info:     client.ScheduleInfo{NextActionTimes: f.nextActionAt},
	}, nil
}

type fakeApplyStore struct {
	result store.ApplyProposalResult
	err    error
	calls  []store.ApplyProposalParams
}

func (f *fakeApplyStore) Apply(_ context.Context, params store.ApplyProposalParams) (store.ApplyProposalResult, error) {
	f.calls = append(f.calls, params)
	if f.err != nil {
		return store.ApplyProposalResult{}, f.err
	}
	return f.result, nil
}

const testBusinessID = "01950000-0000-7000-8000-0000000000c3"

func setupBusinessFixture(t *testing.T, status store.BusinessStatus) store.Business {
	t.Helper()
	category := "clinic"
	website := "https://old.example"
	return store.Business{
		ID: mustHashV7(t, testBusinessID), TenantID: mustHashV7(t, tenantID),
		Status: status, Name: "Old Clinic", Website: &website,
		Aliases: []string{"Old"}, Category: &category,
		Services: json.RawMessage(`["checkups"]`),
		Location: json.RawMessage(`{"address":"1 Road","area":"Central","city":"Singapore","country":"SG"}`),
	}
}

// validApplyPayload is a final review payload that passes ValidateProposal at
// plan.prompt_limit == 4: required profile fields, a two-letter country, and
// four varied prompts, none naming the business.
const validApplyPayload = `{
  "low_confidence": false,
  "profile": {
    "name": "Acme Clinic",
    "aliases": [],
    "category": "orthopaedic clinic",
    "services": ["consultation"],
    "location": {"address": "", "area": "Novena", "city": "Singapore", "country": "SG"}
  },
  "prompts": [
    {"text": "best orthopaedic clinic in Singapore"},
    {"text": "where can I get ACL reconstruction in Singapore"},
    {"text": "knee pain that won't go away, who should I see in Singapore"},
    {"text": "orthopaedic specialist near Novena"}
  ]
}`

func applyResult(t *testing.T) store.ApplyProposalResult {
	return store.ApplyProposalResult{Business: store.Business{
		ID:     mustHashV7(t, testBusinessID),
		Name:   "Acme Clinic",
		Status: store.BusinessStatusActive,
	}}
}

type errorString string

func (e errorString) Error() string { return string(e) }

var errAny = errorString("boom")

// -- CitationService fakes (citation_rpc_test.go) --

type fakeCitationMetrics struct {
	sources []metrics.CitationSource
	err     error
}

func (f *fakeCitationMetrics) CitationSources(context.Context, domain.ID, domain.ID) ([]metrics.CitationSource, error) {
	return f.sources, f.err
}

func seedCitationSources(t *testing.T) []metrics.CitationSource {
	t.Helper()
	resultA := mustHashV7(t, resultIDForTest)
	resultB := mustHashV7(t, "01950000-0000-7000-8000-0000000002a2")
	promptID := mustHashV7(t, promptIDForTest)
	return []metrics.CitationSource{
		{
			Domain:    "healthline.com",
			Frequency: 2,
			ResultIDs: []domain.ID{resultA, resultB},
			Subjects: metrics.CitationSubjectBreakdown{
				Business:   metrics.CitationSubjectStat{Frequency: 1, ResultIDs: []domain.ID{resultA}},
				Competitor: metrics.CitationSubjectStat{Frequency: 1, ResultIDs: []domain.ID{resultB}},
			},
			Pages: []metrics.CitationPage{
				{
					URL:       "https://healthline.com/root-canal",
					Title:     ptrString("Root canal guide"),
					Frequency: 1,
					ResultIDs: []domain.ID{resultA},
					Subjects:  metrics.CitationSubjectBreakdown{Business: metrics.CitationSubjectStat{Frequency: 1, ResultIDs: []domain.ID{resultA}}},
				},
			},
			Prompts: []metrics.CitationPrompt{
				{PromptID: promptID, Text: "best root canal clinic", Frequency: 2, ResultIDs: []domain.ID{resultA, resultB}},
			},
		},
		{
			Domain:    "other.com",
			Frequency: 1,
			ResultIDs: []domain.ID{resultB},
			Subjects:  metrics.CitationSubjectBreakdown{Other: metrics.CitationSubjectStat{Frequency: 1, ResultIDs: []domain.ID{resultB}}},
		},
	}
}

// -- CompetitorService fakes (competitor_rpc_test.go) --

type fakeCompetitorsMetrics struct {
	stats metrics.CompetitorStats
	err   error
}

func (f *fakeCompetitorsMetrics) CompetitorStats(context.Context, domain.ID, domain.ID) (metrics.CompetitorStats, error) {
	return f.stats, f.err
}

type fakeCompetitorStore struct {
	created             store.CompetitorRecord
	updated             store.CompetitorRecord
	createParams        store.CreateManualCompetitorParams
	statusParams        store.SetCompetitorStatusParams
	aliasParams         store.SuggestedAliasParams
	aliasRecord         store.CompetitorRecord
	createErr           error
	statusErr           error
	aliasErr            error
	aliasAction         string
	updateAliasesParams store.UpdateCompetitorAliasesParams
}

func (f *fakeCompetitorStore) CreateManual(_ context.Context, params store.CreateManualCompetitorParams) (store.CompetitorRecord, error) {
	f.createParams = params
	return f.created, f.createErr
}

func (f *fakeCompetitorStore) SetStatus(_ context.Context, params store.SetCompetitorStatusParams) (store.CompetitorRecord, error) {
	f.statusParams = params
	return f.updated, f.statusErr
}

func (f *fakeCompetitorStore) ApproveSuggestedAlias(_ context.Context, params store.SuggestedAliasParams) (store.CompetitorRecord, error) {
	f.aliasParams = params
	f.aliasAction = "approve"
	return f.aliasRecord, f.aliasErr
}

func (f *fakeCompetitorStore) RejectSuggestedAlias(_ context.Context, params store.SuggestedAliasParams) (store.CompetitorRecord, error) {
	f.aliasParams = params
	f.aliasAction = "reject"
	return f.aliasRecord, f.aliasErr
}

func (f *fakeCompetitorStore) UpdateAliases(_ context.Context, params store.UpdateCompetitorAliasesParams) (store.CompetitorRecord, error) {
	f.updateAliasesParams = params
	return f.aliasRecord, f.aliasErr
}

func seedCompetitorStats(t *testing.T) metrics.CompetitorStats {
	t.Helper()
	resultID := mustHashV7(t, resultIDForTest)
	runID := mustHashV7(t, runIDForTest)
	promptID := mustHashV7(t, promptIDForTest)
	return metrics.CompetitorStats{
		TotalAnalyzed: 20,
		SelfMentioned: 11,
		SelfPercent:   55,
		ResultIDs:     []domain.ID{resultID},
		Competitors: []metrics.CompetitorStat{
			{
				CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000201"), Name: "Tracked Co", Status: "tracked",
				Aliases: []string{"Tracked"}, SuggestedAliases: []string{"Tracked Health"},
				Mentioned: 9, TotalMentions: 12, MentionPercent: 45, AvgOrder: 2.1, VsSelf: -10,
				ResultIDs: []domain.ID{resultID},
				PerPrompt: []metrics.PromptAppearance{{PromptID: promptID, Text: "best clinic near me", ResultIDs: []domain.ID{resultID}}},
				Trend:     []metrics.CompetitorTrendPoint{{RunID: runID, ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), Analyzed: 20, Mentioned: 9, Percent: 45, ResultIDs: []domain.ID{resultID}}},
			},
			{
				CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000202"), Name: "Disc Co", Status: "discovered",
				Mentioned: 7, TotalMentions: 8, MentionPercent: 35, AvgOrder: 3.4, VsSelf: -20,
				ResultIDs: []domain.ID{resultID},
				PerPrompt: []metrics.PromptAppearance{},
				Trend:     []metrics.CompetitorTrendPoint{},
			},
			{
				CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000203"), Name: "Dismissed Co", Status: "dismissed",
				Mentioned: 5, TotalMentions: 6, MentionPercent: 25, AvgOrder: 4.0, VsSelf: -30,
				ResultIDs: []domain.ID{resultID},
				PerPrompt: []metrics.PromptAppearance{{PromptID: promptID, Text: "best clinic near me", ResultIDs: []domain.ID{resultID}}},
				Trend:     []metrics.CompetitorTrendPoint{{RunID: runID, ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), Analyzed: 20, Mentioned: 5, Percent: 25, ResultIDs: []domain.ID{resultID}}},
			},
		},
	}
}

// -- OverviewService fakes (overview_rpc_test.go) --

type fakeOverviewMetrics struct {
	trend       []metrics.VisibilityPoint
	keywords    []metrics.KeywordStat
	domains     []metrics.DomainStat
	competitors metrics.CompetitorStats
	changes     []metrics.PromptChange
	err         error
}

func (f *fakeOverviewMetrics) VisibilityTrend(context.Context, domain.ID, domain.ID) ([]metrics.VisibilityPoint, error) {
	return f.trend, f.err
}
func (f *fakeOverviewMetrics) KeywordStats(context.Context, domain.ID, domain.ID) ([]metrics.KeywordStat, error) {
	return f.keywords, f.err
}
func (f *fakeOverviewMetrics) CitationDomainStats(context.Context, domain.ID, domain.ID) ([]metrics.DomainStat, error) {
	return f.domains, f.err
}
func (f *fakeOverviewMetrics) CompetitorStats(context.Context, domain.ID, domain.ID) (metrics.CompetitorStats, error) {
	return f.competitors, f.err
}
func (f *fakeOverviewMetrics) PromptChanges(context.Context, domain.ID, domain.ID) ([]metrics.PromptChange, error) {
	return f.changes, f.err
}

// -- PromptService fakes (prompt_rpc_test.go) --

type fakePromptStore struct {
	active     []store.Prompt
	byID       map[domain.ID]store.Prompt
	listErr    error
	getErr     error
	listCalled int

	createResult  store.Prompt
	createErr     error
	createParams  store.CreateActivePromptParams
	replaceResult store.Prompt
	replaceErr    error
	replaceParams store.ReplacePromptParams
}

func (f *fakePromptStore) ListActivePrompts(_ context.Context, _, _ domain.ID) ([]store.Prompt, error) {
	f.listCalled++
	return f.active, f.listErr
}

func (f *fakePromptStore) GetPrompt(_ context.Context, _, promptID domain.ID) (store.Prompt, error) {
	if f.getErr != nil {
		return store.Prompt{}, f.getErr
	}
	p, ok := f.byID[promptID]
	if !ok {
		return store.Prompt{}, store.ErrNotFound
	}
	return p, nil
}

func (f *fakePromptStore) CreateActivePrompt(_ context.Context, params store.CreateActivePromptParams) (store.Prompt, error) {
	f.createParams = params
	if f.createErr != nil {
		return store.Prompt{}, f.createErr
	}
	return f.createResult, nil
}

func (f *fakePromptStore) ReplacePrompt(_ context.Context, params store.ReplacePromptParams) (store.Prompt, error) {
	f.replaceParams = params
	if f.replaceErr != nil {
		return store.Prompt{}, f.replaceErr
	}
	return f.replaceResult, nil
}

type fakePromptsMetrics struct {
	latest []metrics.PromptLatest
	trends map[domain.ID][]metrics.PromptTrendPoint
	err    error
}

func (f *fakePromptsMetrics) PromptLatestStats(context.Context, domain.ID, domain.ID) ([]metrics.PromptLatest, error) {
	return f.latest, f.err
}
func (f *fakePromptsMetrics) PromptTrends(context.Context, domain.ID, domain.ID) (map[domain.ID][]metrics.PromptTrendPoint, error) {
	return f.trends, f.err
}

// -- ResultService fakes (overview_rpc_test.go, prompt_rpc_test.go, result_rpc_test.go) --

type fakeRunStore struct {
	runs        []store.RunListItem
	err         error
	gotTenant   domain.ID
	gotBusiness domain.ID
	called      int
}

func (f *fakeRunStore) ListRuns(_ context.Context, tenantID, businessID domain.ID) ([]store.RunListItem, error) {
	f.called++
	f.gotTenant = tenantID
	f.gotBusiness = businessID
	return f.runs, f.err
}

type fakeResultStore struct {
	results        []store.ResultListItem
	detail         store.ResultDetail
	analysis       store.ResultAnalysis
	listErr        error
	detailErr      error
	analysisErr    error
	gotTenant      domain.ID
	gotBusiness    domain.ID
	gotResult      domain.ID
	gotFilter      store.ResultFilter
	listCalled     int
	detailCalled   int
	analysisCalled int
}

func (f *fakeResultStore) ListResults(_ context.Context, tenantID, businessID domain.ID, filter store.ResultFilter) ([]store.ResultListItem, error) {
	f.listCalled++
	f.gotTenant = tenantID
	f.gotBusiness = businessID
	f.gotFilter = filter
	return f.results, f.listErr
}

func (f *fakeResultStore) GetResultDetail(_ context.Context, tenantID, resultID domain.ID) (store.ResultDetail, error) {
	f.detailCalled++
	f.gotTenant = tenantID
	f.gotResult = resultID
	return f.detail, f.detailErr
}

func (f *fakeResultStore) GetResultAnalysis(_ context.Context, tenantID, resultID domain.ID) (store.ResultAnalysis, error) {
	f.analysisCalled++
	f.gotTenant = tenantID
	f.gotResult = resultID
	return f.analysis, f.analysisErr
}

type fakeRunsMetrics struct {
	trend []metrics.VisibilityPoint
	err   error
}

func (f *fakeRunsMetrics) VisibilityTrend(context.Context, domain.ID, domain.ID) ([]metrics.VisibilityPoint, error) {
	return f.trend, f.err
}
