//go:build postgres_integration

// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema work.
package dataplatform

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// insertPrivacyCanaryRun/insertPrivacyCanaryCheck are minimal fixture helpers
// for PrivacyCanaryHistory's two source tables (integrity_audit_runs,
// integrity_audit_checks), scoped to the exact check_id/source_id literals
// -- stage_9_retention_disk_and_backup / privacy-canary -- the query filters
// on. integrity_audit_checks's primary key is (audit_run_id, check_id,
// capability_id, installation_id, source_id), not a single surrogate id
// column (see internal/integrity/migrations/0001_audit_run_schema.up.sql and
// 0005_source_fingerprint_report_schema.up.sql, which adds source_id and
// widens the primary key).
func insertPrivacyCanaryRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string, startedAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO integrity_audit_runs (audit_run_id, run_mode, trigger, state, scheduled_at, started_at, finished_at, advisory_lock_key, requested_stages, inputs_version_ref)
		VALUES ($1, 'full', 'scheduled_daily', 'passed', $2, $2, $2, 1, '[]'::jsonb, '{}'::jsonb)
	`, id, startedAt); err != nil {
		t.Fatalf("insert integrity_audit_run: %v", err)
	}
}

func insertPrivacyCanaryCheck(t *testing.T, ctx context.Context, pool *pgxpool.Pool, auditRunID, installationID, status string, observedAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO integrity_audit_checks (audit_run_id, check_id, capability_id, installation_id, source_id, stage_id, status, observed_at)
		VALUES ($1, $2, 'disk-forecast', $3, $4, 'stage_9', $5, $6)
	`, auditRunID, privacyCanaryCheckID, installationID, privacyCanarySourceID, status, observedAt); err != nil {
		t.Fatalf("insert integrity_audit_check: %v", err)
	}
}

// TestPrivacyCanaryHistoryAggregatesPassFailWithinRangeAndBudget proves
// PrivacyCanaryHistory groups integrity_audit_checks by calendar day
// restricted to the exact (check_id, source_id) pair the privacy canary
// sub-check writes, ignores checks under a different check_id/capability_id,
// respects the half-open [from, to) boundary, and returns within its
// registered budget.
func TestPrivacyCanaryHistoryAggregatesPassFailWithinRangeAndBudget(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	createIntegrityTablesForTest(t, ctx, pool)
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	insertPrivacyCanaryRun(t, ctx, pool, "iar_canary_1", base.Add(time.Minute))
	insertPrivacyCanaryCheck(t, ctx, pool, "iar_canary_1", "ain_canary_a", "pass", base.Add(time.Minute))
	insertPrivacyCanaryCheck(t, ctx, pool, "iar_canary_1", "ain_canary_b", "pass", base.Add(2*time.Minute))
	insertPrivacyCanaryCheck(t, ctx, pool, "iar_canary_1", "ain_canary_c", "fail", base.Add(3*time.Minute))

	// A different source_id (not the privacy-canary sub-check) under the
	// same run must never be counted.
	if _, err := pool.Exec(ctx, `
		INSERT INTO integrity_audit_checks (audit_run_id, check_id, capability_id, installation_id, source_id, stage_id, status, observed_at)
		VALUES ('iar_canary_1', $1, 'disk-forecast', 'ain_canary_a', 'disk-forecast', 'stage_9', 'pass', $2)
	`, privacyCanaryCheckID, base.Add(time.Minute)); err != nil {
		t.Fatalf("insert unrelated sub-check: %v", err)
	}

	// Outside range: must never leak in.
	insertPrivacyCanaryRun(t, ctx, pool, "iar_canary_out", base.AddDate(0, 0, 2))
	insertPrivacyCanaryCheck(t, ctx, pool, "iar_canary_out", "ain_canary_out", "pass", base.AddDate(0, 0, 2))

	to := base.AddDate(0, 0, 1)
	started := time.Now()
	response, err := PrivacyCanaryHistory(ctx, pool, base, to)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("PrivacyCanaryHistory: %v", err)
	}
	if elapsed > time.Duration(Budgets["privacy_canary_history_range"].MaxMS)*time.Millisecond {
		t.Fatalf("privacy_canary_history_range exceeded its budget: %v", elapsed)
	}
	if response.FormulaVersion != FormulaVersionPrivacyCanaryHistory1 {
		t.Fatalf("formula_version = %q, want %q", response.FormulaVersion, FormulaVersionPrivacyCanaryHistory1)
	}
	if len(response.Data) != 1 {
		t.Fatalf("expected exactly 1 day row (out-of-range row must not leak in), got %d: %+v", len(response.Data), response.Data)
	}
	day := response.Data[0]
	if day.PassCount != 2 {
		t.Fatalf("pass_count = %d, want 2", day.PassCount)
	}
	if day.FailCount != 1 {
		t.Fatalf("fail_count = %d, want 1", day.FailCount)
	}
	if response.Population.Numerator != 2 || response.Population.Denominator != 3 {
		t.Fatalf("population = %+v, want numerator=2 denominator=3", response.Population)
	}
	if response.Completeness.Status == "unknown" {
		t.Fatalf("completeness should not be unknown when data is present: %+v", response.Completeness)
	}
}

// TestPrivacyCanaryHistoryEmptyRangeReportsUnknownNotZero proves the "no
// silent zero" convention: an empty range must report completeness
// "unknown", not a fabricated "complete" with empty data.
func TestPrivacyCanaryHistoryEmptyRangeReportsUnknownNotZero(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	createIntegrityTablesForTest(t, ctx, pool)
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	response, err := PrivacyCanaryHistory(ctx, pool, base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("PrivacyCanaryHistory: %v", err)
	}
	if len(response.Data) != 0 {
		t.Fatalf("expected zero rows, got %d: %+v", len(response.Data), response.Data)
	}
	if response.Completeness.Status != "unknown" {
		t.Fatalf("completeness = %+v, want unknown for empty range", response.Completeness)
	}
}
