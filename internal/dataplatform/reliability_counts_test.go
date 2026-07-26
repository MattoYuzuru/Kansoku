//go:build postgres_integration

// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema work.
package dataplatform

import (
	"context"
	"testing"
	"time"
)

// TestReliabilityCountsAggregatesUnknownSchemaAndMismatchesWithinRangeAndBudget
// proves ReliabilityCounts sums schema_quarantine_metadata rows (bucketed by
// their own observed_at) and reconciliation_mismatches rows (bucketed by
// their parent reconciliation_runs.started_at, since the mismatch table has
// no timestamp of its own) by calendar day, respects the half-open
// [from, to) boundary, and returns within its registered budget.
func TestReliabilityCountsAggregatesUnknownSchemaAndMismatchesWithinRangeAndBudget(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	insertQuarantine := func(id string, observedAt time.Time) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO schema_quarantine_metadata (quarantine_id, source_kind, schema_fingerprint, category, byte_count, record_count, observed_at)
			VALUES ($1, 'hook_http', 'fp_rc', 'unknown_field', 64, 1, $2)
		`, id, observedAt); err != nil {
			t.Fatalf("insert schema_quarantine_metadata: %v", err)
		}
	}
	insertQuarantine("sqm_rc_1", base.Add(time.Minute))
	insertQuarantine("sqm_rc_2", base.Add(2*time.Minute))
	// Outside range: must never leak in.
	insertQuarantine("sqm_rc_out", base.AddDate(0, 0, 2))

	insertRun := func(id string, startedAt time.Time) {
		if _, err := pool.Exec(ctx, `INSERT INTO reconciliation_runs (reconciliation_run_id, started_at, finished_at, status) VALUES ($1, $2, $2, 'failed')`, id, startedAt); err != nil {
			t.Fatalf("insert reconciliation_run: %v", err)
		}
	}
	insertMismatch := func(id, runID string) {
		if _, err := pool.Exec(ctx, `INSERT INTO reconciliation_mismatches (reconciliation_mismatch_id, reconciliation_run_id, fact_key, category) VALUES ($1, $2, 'fact_rc', 'missing_bucket')`, id, runID); err != nil {
			t.Fatalf("insert reconciliation_mismatch: %v", err)
		}
	}
	insertRun("rr_rc_1", base.Add(3*time.Minute))
	insertMismatch("rm_rc_1", "rr_rc_1")
	insertMismatch("rm_rc_2", "rr_rc_1")
	// Outside range: must never leak in.
	insertRun("rr_rc_out", base.AddDate(0, 0, 2))
	insertMismatch("rm_rc_out", "rr_rc_out")

	to := base.AddDate(0, 0, 1)
	started := time.Now()
	response, err := ReliabilityCounts(ctx, pool, base, to, DefaultTimeBucketSpec())
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("ReliabilityCounts: %v", err)
	}
	if elapsed > time.Duration(Budgets["reliability_counts_range"].MaxMS)*time.Millisecond {
		t.Fatalf("reliability_counts_range exceeded its budget: %v", elapsed)
	}
	if response.FormulaVersion != FormulaVersionReliabilityCounts1 {
		t.Fatalf("formula_version = %q, want %q", response.FormulaVersion, FormulaVersionReliabilityCounts1)
	}
	if len(response.Data) != 1 {
		t.Fatalf("expected exactly 1 day row (out-of-range rows must not leak in), got %d: %+v", len(response.Data), response.Data)
	}
	day := response.Data[0]
	if day.UnknownSchemaCount != 2 {
		t.Fatalf("unknown_schema_count = %d, want 2", day.UnknownSchemaCount)
	}
	if day.ReconciliationMismatchCount != 2 {
		t.Fatalf("reconciliation_mismatch_count = %d, want 2", day.ReconciliationMismatchCount)
	}
	if response.Population.Numerator != 4 || response.Population.Denominator != 4 {
		t.Fatalf("population = %+v, want numerator=denominator=4", response.Population)
	}
	if response.Completeness.Status == "unknown" {
		t.Fatalf("completeness should not be unknown when data is present: %+v", response.Completeness)
	}
}

// TestReliabilityCountsEmptyRangeReportsUnknownNotZero proves the "no silent
// zero" convention: an empty range must report completeness "unknown", not a
// fabricated "complete" with empty data.
func TestReliabilityCountsEmptyRangeReportsUnknownNotZero(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	response, err := ReliabilityCounts(ctx, pool, base, base.AddDate(0, 0, 1), DefaultTimeBucketSpec())
	if err != nil {
		t.Fatalf("ReliabilityCounts: %v", err)
	}
	if len(response.Data) != 0 {
		t.Fatalf("expected zero rows, got %d: %+v", len(response.Data), response.Data)
	}
	if response.Completeness.Status != "unknown" {
		t.Fatalf("completeness = %+v, want unknown for empty range", response.Completeness)
	}
}
