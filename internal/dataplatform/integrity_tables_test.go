//go:build postgres_integration

// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema work.
package dataplatform

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// createIntegrityTablesForTest creates the minimal subset of
// internal/integrity's durable schema that PrivacyCanaryHistory and
// SystemSnapshot read directly (integrity_audit_runs, integrity_audit_checks,
// integrity_backup_status), mirroring the cumulative effect of
// internal/integrity/migrations/0001_audit_run_schema.up.sql,
// 0004_backup_status_schema.up.sql and 0005_source_fingerprint_report_schema.up.sql
// (the source_id column + 5-column primary key on integrity_audit_checks).
//
// package dataplatform cannot import internal/integrity to call its own
// Migrate directly: internal/integrity already imports internal/dataplatform
// (backupcycle.go, wiring.go, syntheticpipeline.go), so the reverse import
// would be a cycle. This helper duplicates just the DDL these two read-only
// query functions depend on, the same way partitions_test.go-adjacent
// helpers duplicate EnsurePartition's DDL locally rather than importing
// across a package boundary.
func createIntegrityTablesForTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS integrity_audit_runs (
			audit_run_id        TEXT PRIMARY KEY,
			run_mode            TEXT NOT NULL CHECK (run_mode IN ('full', 'reduced')),
			trigger             TEXT NOT NULL CHECK (trigger IN ('scheduled_daily', 'startup', 'version_change_detected', 'manual_operator_request')),
			state               TEXT NOT NULL CHECK (state IN ('scheduled', 'running', 'passed', 'degraded', 'failed', 'cancelled')),
			failure_reason      TEXT,
			scheduled_at        TIMESTAMPTZ NOT NULL,
			started_at          TIMESTAMPTZ,
			finished_at         TIMESTAMPTZ,
			advisory_lock_key   BIGINT NOT NULL,
			requested_stages    JSONB NOT NULL,
			inputs_version_ref  JSONB NOT NULL,
			created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS integrity_audit_checks (
			audit_run_id    TEXT NOT NULL REFERENCES integrity_audit_runs (audit_run_id),
			check_id        TEXT NOT NULL,
			capability_id   TEXT NOT NULL,
			installation_id TEXT NOT NULL,
			source_id       TEXT NOT NULL DEFAULT '',
			stage_id        TEXT NOT NULL,
			status          TEXT NOT NULL CHECK (status IN ('pending', 'pass', 'fail', 'skipped_unsupported')),
			category        TEXT,
			detail_ref      TEXT,
			observed_at     TIMESTAMPTZ,
			started_at      TIMESTAMPTZ,
			finished_at     TIMESTAMPTZ,
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (audit_run_id, check_id, capability_id, installation_id, source_id)
		)`,
		`CREATE TABLE IF NOT EXISTS integrity_backup_status (
			id                       INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
			last_backup_at           TIMESTAMPTZ,
			last_backup_checksum_ok  BOOLEAN NOT NULL DEFAULT false,
			last_restore_test_at     TIMESTAMPTZ,
			last_restore_test_ran    BOOLEAN NOT NULL DEFAULT false,
			last_restore_test_passed BOOLEAN NOT NULL DEFAULT false,
			updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
	}
	for _, stmt := range statements {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("create integrity stand-in table: %v", err)
		}
	}
}
