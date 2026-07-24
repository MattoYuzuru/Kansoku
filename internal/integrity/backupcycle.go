package integrity

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kansoku.local/kansoku/internal/dataplatform"
)

// RunBackupCycle performs one REAL backup+verify(+optional restore-test)
// cycle against dataPool (internal/dataplatform's own pool) and durably
// records the outcome into statusPool's integrity_backup_status row, which
// BackupStatusLookup (see NewBackupStatusLookup) reads back. This is the
// only place Session 08 calls internal/dataplatform.CreateBackup/
// VerifyBackupChecksum/CountRows/RestoreBackup: stage_9's
// RetentionDiskBackupCheck itself never opens a second backup mechanism, it
// only reads the BackupStatus this function last recorded.
//
// dataPool and statusPool are typically the same *pgxpool.Pool (this
// package's own migrations live in the same shared PostgreSQL instance as
// internal/dataplatform's tables, per ADR 0011); they are accepted
// separately only so a restore-test dry run can target an isolated
// ephemeral database distinct from the pool CreateBackup exports from,
// without this function assuming they are always identical.
//
// restorePool, when non-nil, must already have internal/dataplatform's
// migrations applied against an EMPTY, isolated database (never the
// production/live pool -- RestoreBackup performs real INSERTs). When
// restorePool is nil, RunBackupCycle records LastRestoreTestRan=false
// rather than fabricating a restore-test result, matching the TDD's "do not
// fabricate a restore-test result that didn't actually run" instruction:
// this function only ever reports a restore test as having run when
// RestoreBackup+row-count-comparison genuinely executed in this call.
func RunBackupCycle(ctx context.Context, dataPool *pgxpool.Pool, statusPool *pgxpool.Pool, restorePool *pgxpool.Pool, appVersion, formulaRegistryVersion, privacyPolicySHA256 string, adapterVersions []string, now time.Time) (BackupStatus, error) {
	backup, err := dataplatform.CreateBackup(ctx, dataPool, appVersion, formulaRegistryVersion, privacyPolicySHA256, adapterVersions, now)
	if err != nil {
		return BackupStatus{}, fmt.Errorf("create backup: %w", err)
	}
	checksumOK := dataplatform.VerifyBackupChecksum(backup) == nil

	status := BackupStatus{
		LastBackupAt:         now.UTC(),
		LastBackupChecksumOK: checksumOK,
	}

	if restorePool != nil && checksumOK {
		sourceCounts := dataplatform.CountRows(backup)
		restoreErr := dataplatform.RestoreBackup(ctx, restorePool, backup)
		status.LastRestoreTestAt = now.UTC()
		status.LastRestoreTestRan = true
		if restoreErr == nil {
			restoredCounts, countErr := countRestoredRows(ctx, restorePool, sourceCounts)
			status.LastRestoreTestPassed = countErr == nil && restoredCountsMatch(sourceCounts, restoredCounts)
		} else {
			status.LastRestoreTestPassed = false
		}
	}

	if err := persistBackupStatus(ctx, statusPool, status); err != nil {
		return BackupStatus{}, fmt.Errorf("persist backup status: %w", err)
	}
	return status, nil
}

// countRestoredRows re-derives row counts for each table CreateBackup
// exported, from restorePool after RestoreBackup ran, by exporting a fresh
// LogicalBackup of restorePool itself and reusing dataplatform.CountRows --
// never a second, independently written row-counting query.
func countRestoredRows(ctx context.Context, restorePool *pgxpool.Pool, sourceCounts dataplatform.RestoreCounts) (dataplatform.RestoreCounts, error) {
	restored, err := dataplatform.CreateBackup(ctx, restorePool, "restore-test-probe", "restore-test-probe", "restore-test-probe", nil, time.Now())
	if err != nil {
		return nil, err
	}
	return dataplatform.CountRows(restored), nil
}

// restoredCountsMatch reports whether every table CreateBackup exported has
// an identical row count in the restored database, matching
// contracts/data-platform/retention.yaml `backup.restore_test.verifies`.
func restoredCountsMatch(source, restored dataplatform.RestoreCounts) bool {
	if len(source) != len(restored) {
		return false
	}
	for table, count := range source {
		if restored[table] != count {
			return false
		}
	}
	return true
}

func persistBackupStatus(ctx context.Context, pool *pgxpool.Pool, status BackupStatus) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO integrity_backup_status (id, last_backup_at, last_backup_checksum_ok, last_restore_test_at, last_restore_test_ran, last_restore_test_passed, updated_at)
		VALUES (1, $1, $2, NULLIF($3, '0001-01-01 00:00:00+00'::timestamptz), $4, $5, now())
		ON CONFLICT (id) DO UPDATE SET
			last_backup_at = EXCLUDED.last_backup_at,
			last_backup_checksum_ok = EXCLUDED.last_backup_checksum_ok,
			last_restore_test_at = COALESCE(EXCLUDED.last_restore_test_at, integrity_backup_status.last_restore_test_at),
			last_restore_test_ran = integrity_backup_status.last_restore_test_ran OR EXCLUDED.last_restore_test_ran,
			last_restore_test_passed = CASE WHEN EXCLUDED.last_restore_test_ran THEN EXCLUDED.last_restore_test_passed ELSE integrity_backup_status.last_restore_test_passed END,
			updated_at = now()
	`, status.LastBackupAt, status.LastBackupChecksumOK, status.LastRestoreTestAt, status.LastRestoreTestRan, status.LastRestoreTestPassed)
	return err
}

// NewBackupStatusLookup returns a BackupStatusLookup reading the row
// RunBackupCycle last persisted into pool's integrity_backup_status table.
// If no cycle has ever run, it honestly reports the zero BackupStatus
// (LastBackupAt/LastRestoreTestAt zero, LastRestoreTestRan=false) rather
// than a fabricated pass.
func NewBackupStatusLookup(pool *pgxpool.Pool) BackupStatusLookup {
	return func(ctx context.Context) (BackupStatus, error) {
		row := pool.QueryRow(ctx, `
			SELECT last_backup_at, last_backup_checksum_ok, last_restore_test_at, last_restore_test_ran, last_restore_test_passed
			FROM integrity_backup_status WHERE id = 1
		`)
		var status BackupStatus
		var lastBackupAt, lastRestoreTestAt *time.Time
		if err := row.Scan(&lastBackupAt, &status.LastBackupChecksumOK, &lastRestoreTestAt, &status.LastRestoreTestRan, &status.LastRestoreTestPassed); err != nil {
			if err.Error() == "no rows in result set" {
				return BackupStatus{}, nil
			}
			return BackupStatus{}, err
		}
		if lastBackupAt != nil {
			status.LastBackupAt = *lastBackupAt
		}
		if lastRestoreTestAt != nil {
			status.LastRestoreTestAt = *lastRestoreTestAt
		}
		return status, nil
	}
}

// NewRetentionDryRunLookup returns a RetentionDryRunLookup that PREVIEWS
// (never drops) retention-eligible partitions for every
// dataplatform.PartitionedTables entry, by reusing the exact same
// pg_inherits/pg_get_expr partition-bound enumeration
// dataplatform.DropPartitionsOlderThan itself uses internally, stopping
// short of any DROP TABLE statement. This keeps stage_9 a genuinely
// read-only audit check (matching "verify configured endpoints/hooks
// WITHOUT mutating them" extended to retention) while still exercising the
// real partition-enumeration path, not a second hand-rolled one.
func NewRetentionDryRunLookup(pool *pgxpool.Pool) RetentionDryRunLookup {
	return func(ctx context.Context, now time.Time, horizonDays int) (RetentionDryRunResult, error) {
		horizon := now.AddDate(0, 0, -horizonDays)
		eligible := map[string][]string{}
		for _, table := range dataplatform.PartitionedTables {
			names, err := dataplatform.PreviewPartitionsOlderThan(ctx, pool, table, horizon)
			if err != nil {
				return RetentionDryRunResult{}, fmt.Errorf("preview retention for %s: %w", table, err)
			}
			if len(names) > 0 {
				eligible[table] = names
			}
		}
		return RetentionDryRunResult{EligibleForDrop: eligible}, nil
	}
}
