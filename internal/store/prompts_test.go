package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"opensight/internal/domain"

	"github.com/google/uuid"
)

func TestCreateActivePromptInTxInsertsWhenBelowPlanLimit(t *testing.T) {
	createdAt := time.Date(2026, 7, 18, 15, 30, 0, 0, time.UTC)
	tx := &fakePromptTx{
		rows: []rowScanner{
			fakeRow{values: []any{2}},
			fakeRow{values: []any{1}},
			fakeRow{values: []any{createdAt}},
		},
	}

	promptID := mustUUIDV7(t, "01950000-0000-7000-8000-000000001001")
	businessID := mustUUIDV7(t, "01950000-0000-7000-8000-000000001002")
	tenantID := mustUUIDV7(t, "01950000-0000-7000-8000-000000001003")
	prompt, err := createActivePromptInTx(context.Background(), tx, CreateActivePromptParams{
		ID:         promptID,
		TenantID:   tenantID,
		BusinessID: businessID,
		Text:       "best clinic near me",
	})
	if err != nil {
		t.Fatalf("create active prompt: %v", err)
	}

	if prompt.ID != promptID {
		t.Fatalf("prompt ID = %s, want %s", prompt.ID, promptID)
	}
	if prompt.BusinessID != businessID {
		t.Fatalf("business ID = %s, want %s", prompt.BusinessID, businessID)
	}
	if prompt.Status != PromptStatusActive {
		t.Fatalf("prompt status = %q, want %q", prompt.Status, PromptStatusActive)
	}
	if !prompt.CreatedAt.Equal(createdAt) {
		t.Fatalf("created at = %s, want %s", prompt.CreatedAt, createdAt)
	}
	if len(tx.queries) != 3 {
		t.Fatalf("query count = %d, want 3", len(tx.queries))
	}
	if tx.queries[0] != lockBusinessPromptLimitSQL {
		t.Fatalf("first query = %q, want prompt limit lock query", tx.queries[0])
	}
	if tx.queries[1] != countActivePromptsSQL {
		t.Fatalf("second query = %q, want active prompt count query", tx.queries[1])
	}
	if tx.queries[2] != insertActivePromptSQL {
		t.Fatalf("third query = %q, want prompt insert query", tx.queries[2])
	}
}

func TestCreateActivePromptInTxRejectsPlanLimitExceeded(t *testing.T) {
	tx := &fakePromptTx{
		rows: []rowScanner{
			fakeRow{values: []any{1}},
			fakeRow{values: []any{1}},
		},
	}

	_, err := createActivePromptInTx(context.Background(), tx, CreateActivePromptParams{
		ID:         mustUUIDV7(t, "01950000-0000-7000-8000-000000001101"),
		TenantID:   mustUUIDV7(t, "01950000-0000-7000-8000-000000001103"),
		BusinessID: mustUUIDV7(t, "01950000-0000-7000-8000-000000001102"),
		Text:       "best clinic near me",
	})
	if !errors.Is(err, ErrPromptLimitExceeded) {
		t.Fatalf("error = %v, want ErrPromptLimitExceeded", err)
	}
	if len(tx.queries) != 2 {
		t.Fatalf("query count = %d, want 2", len(tx.queries))
	}
}

func TestCreateActivePromptInTxReturnsNotFoundForMissingOrCrossTenantBusiness(t *testing.T) {
	tx := &fakePromptTx{
		rows: []rowScanner{
			fakeRow{err: sql.ErrNoRows},
		},
	}

	_, err := createActivePromptInTx(context.Background(), tx, CreateActivePromptParams{
		ID:         mustUUIDV7(t, "01950000-0000-7000-8000-000000001201"),
		TenantID:   mustUUIDV7(t, "01950000-0000-7000-8000-000000001203"),
		BusinessID: mustUUIDV7(t, "01950000-0000-7000-8000-000000001202"),
		Text:       "best clinic near me",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if len(tx.queries) != 1 {
		t.Fatalf("query count = %d, want 1", len(tx.queries))
	}
}

func TestNormalizeCreateActivePromptParamsGeneratesUUIDv7(t *testing.T) {
	params, err := normalizeCreateActivePromptParams(CreateActivePromptParams{
		TenantID:   mustUUIDV7(t, "01950000-0000-7000-8000-000000001303"),
		BusinessID: mustUUIDV7(t, "01950000-0000-7000-8000-000000001302"),
		Text:       "best clinic near me",
	})
	if err != nil {
		t.Fatalf("normalize params: %v", err)
	}
	if params.ID == uuid.Nil {
		t.Fatal("prompt ID was not generated")
	}
	if params.ID.Version() != uuid.Version(7) {
		t.Fatalf("prompt ID version = %s, want VERSION_7", params.ID.Version())
	}
}

func TestNormalizeCreateActivePromptParamsRejectsSelfReplacement(t *testing.T) {
	promptID := mustUUIDV7(t, "01950000-0000-7000-8000-000000001401")

	_, err := normalizeCreateActivePromptParams(CreateActivePromptParams{
		ID:               promptID,
		TenantID:         mustUUIDV7(t, "01950000-0000-7000-8000-000000001403"),
		BusinessID:       mustUUIDV7(t, "01950000-0000-7000-8000-000000001402"),
		Text:             "best clinic near me",
		ReplacesPromptID: &promptID,
	})
	if err == nil {
		t.Fatal("expected self replacement error")
	}
}

// TestReplacePromptInTxRetiresThenInserts pins the replace sequence: lock the old
// active prompt, retire it, then insert the new active prompt recording it as
// predecessor — all in the order the transaction depends on.
func TestReplacePromptInTxRetiresThenInserts(t *testing.T) {
	oldID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002001")
	businessID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002002")
	tenantID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002003")
	createdAt := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)

	tx := &fakePromptTx{
		rows: []rowScanner{
			// lock old prompt: id, business_id, text, status, replaces_prompt_id, created_at
			fakeRow{values: []any{oldID, businessID, "old text", PromptStatusActive, (*domain.ID)(nil), createdAt}},
			// createActivePromptInTx: prompt limit lock, active count, insert created_at
			fakeRow{values: []any{5}},
			fakeRow{values: []any{0}},
			fakeRow{values: []any{createdAt}},
		},
	}

	prompt, err := replacePromptInTx(context.Background(), tx, ReplacePromptParams{
		TenantID:    tenantID,
		OldPromptID: oldID,
		Text:        "new text",
	})
	if err != nil {
		t.Fatalf("replace prompt: %v", err)
	}
	if prompt.ReplacesPromptID == nil || *prompt.ReplacesPromptID != oldID {
		t.Fatalf("replaces prompt id = %v, want %s", prompt.ReplacesPromptID, oldID)
	}
	if prompt.Text != "new text" || prompt.Status != PromptStatusActive {
		t.Fatalf("new prompt = %+v", prompt)
	}
	if len(tx.queries) != 4 {
		t.Fatalf("query count = %d, want 4", len(tx.queries))
	}
	if tx.queries[0] != lockPromptForReplaceSQL {
		t.Fatalf("first query = %q, want lock-for-replace", tx.queries[0])
	}
	if tx.execs != 1 || tx.execQueries[0] != retirePromptSQL {
		t.Fatalf("exec queries = %v, want one retire before insert", tx.execQueries)
	}
	if tx.queries[3] != insertActivePromptSQL {
		t.Fatalf("last query = %q, want insert", tx.queries[3])
	}
}

// TestReplacePromptInTxRejectsRetiredPrompt confirms a replace targeting an
// already-retired prompt is rejected before any write — this is also what a
// second racing replace sees after the first commits.
func TestReplacePromptInTxRejectsRetiredPrompt(t *testing.T) {
	oldID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002101")
	businessID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002102")
	createdAt := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)

	tx := &fakePromptTx{
		rows: []rowScanner{
			fakeRow{values: []any{oldID, businessID, "old text", PromptStatusRetired, (*domain.ID)(nil), createdAt}},
		},
	}

	_, err := replacePromptInTx(context.Background(), tx, ReplacePromptParams{
		TenantID:    mustUUIDV7(t, "01950000-0000-7000-8000-000000002103"),
		OldPromptID: oldID,
		Text:        "new text",
	})
	if !errors.Is(err, ErrPromptNotActive) {
		t.Fatalf("error = %v, want ErrPromptNotActive", err)
	}
	if tx.execs != 0 {
		t.Fatalf("exec count = %d, want 0 (no retire on rejected replace)", tx.execs)
	}
}

// TestReplacePromptInTxReturnsNotFoundForMissingPrompt confirms a missing or
// cross-tenant prompt (no locked row) is ErrNotFound.
func TestReplacePromptInTxReturnsNotFoundForMissingPrompt(t *testing.T) {
	tx := &fakePromptTx{rows: []rowScanner{fakeRow{err: sql.ErrNoRows}}}

	_, err := replacePromptInTx(context.Background(), tx, ReplacePromptParams{
		TenantID:    mustUUIDV7(t, "01950000-0000-7000-8000-000000002203"),
		OldPromptID: mustUUIDV7(t, "01950000-0000-7000-8000-000000002201"),
		Text:        "new text",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

type fakePromptTx struct {
	rows        []rowScanner
	queries     []string
	args        [][]any
	execs       int
	execQueries []string
}

func (tx *fakePromptTx) queryRowContext(_ context.Context, query string, args ...any) rowScanner {
	tx.queries = append(tx.queries, query)
	tx.args = append(tx.args, args)
	if len(tx.rows) == 0 {
		return fakeRow{err: fmt.Errorf("unexpected query %q", query)}
	}
	row := tx.rows[0]
	tx.rows = tx.rows[1:]
	return row
}

func (tx *fakePromptTx) queryContext(_ context.Context, query string, _ ...any) (*sql.Rows, error) {
	return nil, fmt.Errorf("unexpected queryContext %q", query)
}

func (tx *fakePromptTx) execContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	tx.execs++
	tx.execQueries = append(tx.execQueries, query)
	return fakeResult{}, nil
}

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 1, nil }

type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan destinations = %d, values = %d", len(dest), len(r.values))
	}
	for i, value := range r.values {
		switch target := dest[i].(type) {
		case *int:
			intValue, ok := value.(int)
			if !ok {
				return fmt.Errorf("scan value %d has type %T, want int", i, value)
			}
			*target = intValue
		case *time.Time:
			timeValue, ok := value.(time.Time)
			if !ok {
				return fmt.Errorf("scan value %d has type %T, want time.Time", i, value)
			}
			*target = timeValue
		case *string:
			stringValue, ok := value.(string)
			if !ok {
				return fmt.Errorf("scan value %d has type %T, want string", i, value)
			}
			*target = stringValue
		case *domain.ID:
			idValue, ok := value.(domain.ID)
			if !ok {
				return fmt.Errorf("scan value %d has type %T, want domain.ID", i, value)
			}
			*target = idValue
		case *PromptStatus:
			statusValue, ok := value.(PromptStatus)
			if !ok {
				return fmt.Errorf("scan value %d has type %T, want PromptStatus", i, value)
			}
			*target = statusValue
		case **domain.ID:
			idPtr, ok := value.(*domain.ID)
			if !ok {
				return fmt.Errorf("scan value %d has type %T, want *domain.ID", i, value)
			}
			*target = idPtr
		default:
			return fmt.Errorf("unsupported scan destination %T", target)
		}
	}
	return nil
}

func mustUUIDV7(t *testing.T, raw string) domain.ID {
	t.Helper()

	id, err := uuid.Parse(raw)
	if err != nil {
		t.Fatalf("parse UUID %q: %v", raw, err)
	}
	if id.Version() != uuid.Version(7) {
		t.Fatalf("UUID %q version = %s, want VERSION_7", raw, id.Version())
	}
	return id
}
