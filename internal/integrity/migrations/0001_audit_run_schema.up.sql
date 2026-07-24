-- Session 08 audit-run/check durable schema, matching
-- contracts/integrity/audit-run-and-schedule.yaml's state machine, stage
-- registry and idempotency rule. This ledger is intentionally named
-- integrity_schema_migrations (not schema_migrations) because it is
-- tracked independently from internal/dataplatform's own migration ledger,
-- which already owns the schema_migrations table name in the same
-- PostgreSQL instance; reusing that exact name here would collide on the
-- numeric version primary key the two packages' migrations both start from
-- (0001).

CREATE TABLE IF NOT EXISTS integrity_audit_runs (
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
);

CREATE INDEX IF NOT EXISTS idx_integrity_audit_runs_state ON integrity_audit_runs (state);
CREATE INDEX IF NOT EXISTS idx_integrity_audit_runs_scheduled_at ON integrity_audit_runs (scheduled_at);

CREATE TABLE IF NOT EXISTS integrity_audit_checks (
    audit_run_id    TEXT NOT NULL REFERENCES integrity_audit_runs (audit_run_id),
    check_id        TEXT NOT NULL,
    capability_id   TEXT NOT NULL,
    installation_id TEXT NOT NULL,
    stage_id        TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('pending', 'pass', 'fail', 'skipped_unsupported')),
    category        TEXT,
    detail_ref      TEXT,
    observed_at     TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (audit_run_id, check_id, capability_id, installation_id)
);

CREATE INDEX IF NOT EXISTS idx_integrity_audit_checks_status ON integrity_audit_checks (status);
CREATE INDEX IF NOT EXISTS idx_integrity_audit_checks_key ON integrity_audit_checks (check_id, capability_id, installation_id);
