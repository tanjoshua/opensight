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

type fakePromptTx struct {
	rows    []rowScanner
	queries []string
	args    [][]any
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
	return nil, fmt.Errorf("unexpected execContext %q", query)
}

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
