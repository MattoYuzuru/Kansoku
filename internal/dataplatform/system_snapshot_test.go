//go:build postgres_integration

// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema work.
package dataplatform

import (
	"context"
	"testing"
	"time"
)

// TestSystemSnapshotReadsRealSizeAndBackupAgeWithinBudget proves
// SystemSnapshot reads a real pg_database_size measurement and, once a
// integrity_backup_status row exists, computes a real (non-fabricated)
// backup/restore-test age from it, and returns within its registered
// budget. Unlike every other Wave 1b aggregation, this is a live snapshot
// with no [from, to) range argument at all.
func TestSystemSnapshotReadsRealSizeAndBackupAgeWithinBudget(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	createIntegrityTablesForTest(t, ctx, pool)

	lastBackupAt := time.Now().Add(-2 * time.Hour)
	lastRestoreTestAt := time.Now().Add(-3 * time.Hour)
	if _, err := pool.Exec(ctx, `
		INSERT INTO integrity_backup_status (id, last_backup_at, last_backup_checksum_ok, last_restore_test_at, last_restore_test_ran, last_restore_test_passed)
		VALUES (1, $1, true, $2, true, true)
	`, lastBackupAt, lastRestoreTestAt); err != nil {
		t.Fatalf("insert integrity_backup_status: %v", err)
	}

	started := time.Now()
	response, err := SystemSnapshot(ctx, pool)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("SystemSnapshot: %v", err)
	}
	if elapsed > time.Duration(Budgets["system_snapshot"].MaxMS)*time.Millisecond {
		t.Fatalf("system_snapshot exceeded its budget: %v", elapsed)
	}
	if response.FormulaVersion != FormulaVersionSystemSnapshot1 {
		t.Fatalf("formula_version = %q, want %q", response.FormulaVersion, FormulaVersionSystemSnapshot1)
	}
	if response.DatabaseSizeBytes <= 0 {
		t.Fatalf("database_size_bytes = %d, want a real positive measurement", response.DatabaseSizeBytes)
	}
	if response.BackupAgeSeconds == nil {
		t.Fatalf("expected a computed backup_age_seconds once a backup status row exists")
	}
	if *response.BackupAgeSeconds < 3600 || *response.BackupAgeSeconds > 3*3600 {
		t.Fatalf("backup_age_seconds = %v, want ~2h (7200s)", *response.BackupAgeSeconds)
	}
	if response.BackupChecksumOK == nil || !*response.BackupChecksumOK {
		t.Fatalf("backup_checksum_ok = %v, want true", response.BackupChecksumOK)
	}
	if response.RestoreTestAgeSeconds == nil {
		t.Fatalf("expected a computed restore_test_age_seconds once a backup status row exists")
	}
	if response.RestoreTestPassed == nil || !*response.RestoreTestPassed {
		t.Fatalf("restore_test_passed = %v, want true", response.RestoreTestPassed)
	}
	if response.Population.Numerator != 2 || response.Population.Denominator != 2 {
		t.Fatalf("population = %+v, want numerator=denominator=2 (both facts observed)", response.Population)
	}
	if response.Completeness.Status != "complete" {
		t.Fatalf("completeness = %+v, want complete when both facts are observed", response.Completeness)
	}
}

// TestSystemSnapshotNoBackupStatusRowReportsUnknownNotFabricated proves the
// "no silent zero" convention for a fresh install where
// integrity_backup_status has never been upserted: every age/outcome field
// must be nil (honest unknown), never a fabricated zero age or false pass.
// The denominator here is always 2 by construction (backup age + restore
// age are the two eligible facts), so a zero numerator against that fixed
// denominator reports completeness "degraded" (per completenessFor's
// ratio<0.5 rule), not "unknown" and never "complete" -- this test locks in
// that exact honest-degraded behavior rather than asserting the wrong
// "unknown" status a truly denominator-less metric would report.
func TestSystemSnapshotNoBackupStatusRowReportsUnknownNotFabricated(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	createIntegrityTablesForTest(t, ctx, pool)

	response, err := SystemSnapshot(ctx, pool)
	if err != nil {
		t.Fatalf("SystemSnapshot: %v", err)
	}
	if response.DatabaseSizeBytes <= 0 {
		t.Fatalf("database_size_bytes = %d, want a real positive measurement even with no backup row", response.DatabaseSizeBytes)
	}
	if response.BackupAgeSeconds != nil {
		t.Fatalf("expected nil backup_age_seconds with no backup status row, got %v", *response.BackupAgeSeconds)
	}
	if response.RestoreTestAgeSeconds != nil {
		t.Fatalf("expected nil restore_test_age_seconds with no backup status row, got %v", *response.RestoreTestAgeSeconds)
	}
	if response.BackupChecksumOK != nil {
		t.Fatalf("expected nil backup_checksum_ok with no backup status row, got %v", *response.BackupChecksumOK)
	}
	if response.RestoreTestPassed != nil {
		t.Fatalf("expected nil restore_test_passed with no backup status row, got %v", *response.RestoreTestPassed)
	}
	if response.Population.Numerator != 0 {
		t.Fatalf("population numerator = %d, want 0 (neither fact observed)", response.Population.Numerator)
	}
	if response.Completeness.Status != "degraded" {
		t.Fatalf("completeness = %+v, want degraded (0 of 2 eligible facts observed), never complete/unknown", response.Completeness)
	}
}
