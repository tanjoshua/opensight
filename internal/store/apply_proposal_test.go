package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// fakeApplyTx is a querier fake for applyProposalInTx. Unlike fakePromptTx it
// must feed scanBusiness a full row, so its rows carry arbitrary column values
// assigned by reflection (and sql.Scanner columns like aliases/location).
type fakeApplyTx struct {
	rows     []fakeApplyRow
	queries  []string
	execs    []string
	rowIndex int
}

func (tx *fakeApplyTx) queryRowContext(_ context.Context, query string, _ ...any) rowScanner {
	tx.queries = append(tx.queries, query)
	if tx.rowIndex >= len(tx.rows) {
		return fakeApplyRow{err: fmt.Errorf("unexpected query %q", query)}
	}
	row := tx.rows[tx.rowIndex]
	tx.rowIndex++
	return row
}

func (tx *fakeApplyTx) queryContext(_ context.Context, query string, _ ...any) (*sql.Rows, error) {
	return nil, fmt.Errorf("unexpected queryContext %q", query)
}

func (tx *fakeApplyTx) execContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	tx.execs = append(tx.execs, query)
	return execResult{}, nil
}

type execResult struct{}

func (execResult) LastInsertId() (int64, error) { return 0, nil }
func (execResult) RowsAffected() (int64, error) { return 0, nil }

type fakeApplyRow struct {
	values []any
	err    error
}

func (r fakeApplyRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan destinations = %d, values = %d", len(dest), len(r.values))
	}
	for i, v := range r.values {
		if scanner, ok := dest[i].(sql.Scanner); ok {
			if err := scanner.Scan(v); err != nil {
				return err
			}
			continue
		}
		rv := reflect.ValueOf(dest[i])
		if rv.Kind() != reflect.Pointer {
			return fmt.Errorf("scan destination %d is not a pointer: %T", i, dest[i])
		}
		if v == nil {
			rv.Elem().Set(reflect.Zero(rv.Elem().Type()))
			continue
		}
		val := reflect.ValueOf(v)
		if !val.Type().AssignableTo(rv.Elem().Type()) {
			return fmt.Errorf("scan value %d has type %T, not assignable to %s", i, v, rv.Elem().Type())
		}
		rv.Elem().Set(val)
	}
	return nil
}

// activeBusinessRow is the column set scanBusiness expects, filled for an
// activated business (mirrors businessColumns order).
func activeBusinessRow(businessID, tenantID interface{ String() string }) fakeApplyRow {
	return fakeApplyRow{values: []any{
		businessID.String(),        // id (uuid.UUID.Scan takes a string)
		tenantID.String(),          // tenant_id
		BusinessStatusActive,       // status
		"Acme Clinic",              // name
		(*string)(nil),             // website
		[]byte(`[]`),               // aliases (to_jsonb)
		(*string)(nil),             // category
		json.RawMessage(`[]`),      // services
		[]byte(`{"country":"SG"}`), // location
		time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC), // created_at
		(*time.Time)(nil), // activated_at
	}}
}

func TestApplyProposalInTxHappyPath(t *testing.T) {
	businessID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002001")
	tenantID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002002")
	createdAt := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)

	tx := &fakeApplyTx{rows: []fakeApplyRow{
		{values: []any{BusinessStatusDraft}}, // lock draft business
		activeBusinessRow(businessID, tenantID),
		{values: []any{4}}, {values: []any{0}}, {values: []any{createdAt}}, // prompt 1
		{values: []any{4}}, {values: []any{1}}, {values: []any{createdAt}}, // prompt 2
	}}

	result, err := applyProposalInTx(context.Background(), tx, ApplyProposalParams{
		TenantID:    tenantID,
		BusinessID:  businessID,
		Name:        "Acme Clinic",
		Category:    "orthopaedic clinic",
		PromptTexts: []string{"best clinic near me", "who fixes knees in Novena"},
		ActivatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("apply proposal: %v", err)
	}
	if len(result.Prompts) != 2 {
		t.Fatalf("prompts = %d, want 2", len(result.Prompts))
	}
	if result.Business.Status != BusinessStatusActive {
		t.Fatalf("business status = %q, want active", result.Business.Status)
	}

	wantQueries := []string{
		lockDraftBusinessSQL,
		activateBusinessSQL,
		lockBusinessPromptLimitSQL, countActivePromptsSQL, insertActivePromptSQL,
		lockBusinessPromptLimitSQL, countActivePromptsSQL, insertActivePromptSQL,
	}
	if !reflect.DeepEqual(tx.queries, wantQueries) {
		t.Fatalf("queries = %v, want %v", tx.queries, wantQueries)
	}
	if len(tx.execs) != 1 || tx.execs[0] != markProposalAppliedSQL {
		t.Fatalf("execs = %v, want one markProposalAppliedSQL", tx.execs)
	}
}

func TestApplyProposalInTxRejectsNonDraftBusiness(t *testing.T) {
	businessID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002101")
	tenantID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002102")

	tx := &fakeApplyTx{rows: []fakeApplyRow{
		{values: []any{BusinessStatusActive}},
	}}

	_, err := applyProposalInTx(context.Background(), tx, ApplyProposalParams{
		TenantID:    tenantID,
		BusinessID:  businessID,
		Name:        "Acme Clinic",
		Category:    "clinic",
		PromptTexts: []string{"best clinic near me"},
		ActivatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrBusinessNotDraft) {
		t.Fatalf("error = %v, want ErrBusinessNotDraft", err)
	}
	if len(tx.queries) != 1 {
		t.Fatalf("queries = %d, want 1 (locked and stopped)", len(tx.queries))
	}
}

func TestApplyProposalInTxReturnsNotFoundForMissingBusiness(t *testing.T) {
	businessID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002201")
	tenantID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002202")

	tx := &fakeApplyTx{rows: []fakeApplyRow{
		{err: sql.ErrNoRows},
	}}

	_, err := applyProposalInTx(context.Background(), tx, ApplyProposalParams{
		TenantID:    tenantID,
		BusinessID:  businessID,
		Name:        "Acme Clinic",
		Category:    "clinic",
		PromptTexts: []string{"best clinic near me"},
		ActivatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestApplyProposalInTxPropagatesPromptLimit(t *testing.T) {
	businessID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002301")
	tenantID := mustUUIDV7(t, "01950000-0000-7000-8000-000000002302")

	tx := &fakeApplyTx{rows: []fakeApplyRow{
		{values: []any{BusinessStatusDraft}},
		activeBusinessRow(businessID, tenantID),
		{values: []any{1}}, {values: []any{1}}, // limit 1, already 1 active → exceeded
	}}

	_, err := applyProposalInTx(context.Background(), tx, ApplyProposalParams{
		TenantID:    tenantID,
		BusinessID:  businessID,
		Name:        "Acme Clinic",
		Category:    "clinic",
		PromptTexts: []string{"best clinic near me"},
		ActivatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrPromptLimitExceeded) {
		t.Fatalf("error = %v, want ErrPromptLimitExceeded", err)
	}
}
