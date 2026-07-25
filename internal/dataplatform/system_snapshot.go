package dataplatform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionSystemSnapshot1 is the registered formula version for the
// system size/age snapshot query.
const FormulaVersionSystemSnapshot1 = "system_snapshot/1"

// SystemSnapshot executes the "system_snapshot" budgeted query: a single
// non-time-series snapshot of durable system-size and backup/restore-test
// age facts. Serves the /privacy "privacy-retention" panel, /system
// "system-recovery" panel and /settings "settings-impact-preview" panel
// (system.database_size_bytes, system.backup_age_seconds,
// system.restore_test_age_seconds).
//
// DatabaseSizeBytes is read via pg_database_size(current_database()), a
// live, exact measurement (contracts/metrics.yaml exactness: "measured"),
// never a fabricated history series.
//
// Backup/restore-test ages are read from integrity_backup_status (the
// single upserted row from internal/integrity/backupcycle.go's
// persistBackupStatus), not from the schema's own backup_runs/restore_tests
// tables: a repo-wide search confirms zero INSERT statements ever populate
// backup_runs/restore_tests (internal/runtime/backup.go only lists them as
// table NAMES to back up/count, never writes rows into them), whereas
// integrity_backup_status is the real, durably-written system of record for
// "did a backup/restore-test actually run and pass" (see its migration's
// doc comment: "never fabricates a restore-test result that did not
// actually run"). When no row exists yet (fresh install, id=1 never
// upserted), every age/outcome field is left nil -- an honest "unknown",
// never a fabricated zero age or false pass.
//
// This function deliberately does not read or call anything in
// internal/runtime/diagnostics.go's collectResourceMetrics() or the
// admin-gated Diagnostics() mutation endpoint: per internal/webui/webui.go's
// doc comment, the mutation bearer (and therefore anything reachable only
// through the mutation-guarded /api/v1/admin/diagnostics route) is never
// embedded in the read-only dashboard. collector_cpu_ratio, collector_rss_bytes,
// database_growth_bytes_per_day and common_query_latency_seconds have no
// durable backing table at all (they are live-process-only samples) and are
// intentionally left unimplemented here with no route.
func SystemSnapshot(ctx context.Context, pool *pgxpool.Pool) (SystemSnapshotResponse, error) {
	budget := Budgets["system_snapshot"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return SystemSnapshotResponse{}, err
	}
	defer release()

	started := time.Now()
	var response SystemSnapshotResponse
	if err := conn.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&response.DatabaseSizeBytes); err != nil {
		return SystemSnapshotResponse{}, budgetOrErr(budget, started, err)
	}

	var lastBackupAt, lastRestoreTestAt *time.Time
	var backupChecksumOK, restoreTestRan, restoreTestPassed bool
	err = conn.QueryRow(ctx, `
		SELECT last_backup_at, last_backup_checksum_ok, last_restore_test_at, last_restore_test_ran, last_restore_test_passed
		FROM integrity_backup_status WHERE id = 1
	`).Scan(&lastBackupAt, &backupChecksumOK, &lastRestoreTestAt, &restoreTestRan, &restoreTestPassed)
	hasBackupStatusRow := true
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			hasBackupStatusRow = false
		} else {
			return SystemSnapshotResponse{}, budgetOrErr(budget, started, err)
		}
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return SystemSnapshotResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}

	now := time.Now()
	var numerator, denominator int64
	denominator += 2 // backup age + restore-test age are the two eligible facts.
	if hasBackupStatusRow && lastBackupAt != nil {
		age := now.Sub(*lastBackupAt).Seconds()
		response.BackupAgeSeconds = &age
		ok := backupChecksumOK
		response.BackupChecksumOK = &ok
		numerator++
	}
	if hasBackupStatusRow && lastRestoreTestAt != nil && restoreTestRan {
		age := now.Sub(*lastRestoreTestAt).Seconds()
		response.RestoreTestAgeSeconds = &age
		passed := restoreTestPassed
		response.RestoreTestPassed = &passed
		numerator++
	}

	response.FormulaVersion = FormulaVersionSystemSnapshot1
	response.Population = Population{Numerator: numerator, Denominator: denominator}
	response.Completeness = completenessFor(numerator, denominator)
	return response, nil
}
