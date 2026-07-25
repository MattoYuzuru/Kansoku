package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionPrivacyCanaryHistory1 is the registered formula version for
// the privacy canary check history query.
const FormulaVersionPrivacyCanaryHistory1 = "privacy_canary_history/1"

// privacyCanaryCheckID/privacyCanarySourceID are the exact literal values
// internal/integrity/storageops.go's RetentionDiskBackupCheck writes for
// its privacy-canary sub-check: RetentionDiskBackupCheckID is the shared
// check_id every stage_9 sub-check uses, and "privacy-canary" is the
// specific CheckTarget.SourceID for the canary sub-check within that set
// (Targets() returns "retention-preview", "disk-forecast", "backup-status",
// "restore-status", "durable-spool", "privacy-canary").
const (
	privacyCanaryCheckID  = "stage_9_retention_disk_and_backup"
	privacyCanarySourceID = "privacy-canary"
)

// PrivacyCanaryHistory executes the "privacy_canary_history_range" budgeted
// query: one row per calendar day inside the half-open [from, to) range
// with the pass/fail count of the integrity privacy-canary check observed
// that day, from integrity_audit_checks (check_id =
// "stage_9_retention_disk_and_backup", source_id = "privacy-canary") joined
// to integrity_audit_runs. Serves the /privacy "privacy-canary" panel.
//
// This deliberately does NOT serve privacy.raw_content_persisted_count as a
// literal exact count: no such count exists anywhere in the schema (the
// canary is a boolean pass/fail check, not a scanned-match counter). The
// response type is named around "canary check history" with pass/fail
// counts, never implying it is that literal metric.
//
// integrity_audit_checks.observed_at/started_at/finished_at are all
// nullable; this mirrors internal/integrity/health.go's checkTime()
// precedence (observed_at, then finished_at) for picking the timestamp to
// bucket by, falling back to the parent integrity_audit_runs.started_at
// when both check-level timestamps are null.
func PrivacyCanaryHistory(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (PrivacyCanaryHistoryResponse, error) {
	budget := Budgets["privacy_canary_history_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return PrivacyCanaryHistoryResponse{}, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		SELECT date_trunc('day', coalesce(iac.observed_at, iac.finished_at, iar.started_at)) AS day,
			count(*) FILTER (WHERE iac.status = 'pass') AS pass_count,
			count(*) FILTER (WHERE iac.status = 'fail') AS fail_count
		FROM integrity_audit_checks iac
		JOIN integrity_audit_runs iar ON iar.audit_run_id = iac.audit_run_id
		WHERE iac.check_id = $3 AND iac.source_id = $4
			AND coalesce(iac.observed_at, iac.finished_at, iar.started_at) >= $1
			AND coalesce(iac.observed_at, iac.finished_at, iar.started_at) < $2
		GROUP BY day
		ORDER BY day
	`, from, to, privacyCanaryCheckID, privacyCanarySourceID)
	if err != nil {
		return PrivacyCanaryHistoryResponse{}, budgetOrErr(budget, started, err)
	}
	var response PrivacyCanaryHistoryResponse
	var numerator, denominator int64
	for rows.Next() {
		var row PrivacyCanaryDayRow
		if err := rows.Scan(&row.Day, &row.PassCount, &row.FailCount); err != nil {
			rows.Close()
			return PrivacyCanaryHistoryResponse{}, err
		}
		response.Data = append(response.Data, row)
		numerator += row.PassCount
		denominator += row.PassCount + row.FailCount
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PrivacyCanaryHistoryResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return PrivacyCanaryHistoryResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}

	response.FormulaVersion = FormulaVersionPrivacyCanaryHistory1
	response.Population = Population{Numerator: numerator, Denominator: denominator}
	response.Completeness = completenessFor(numerator, denominator)
	return response, nil
}
