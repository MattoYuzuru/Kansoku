CREATE TABLE runtime_job_runs (
    job_run_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL CHECK (job_id IN (
        'daily_integrity', 'rollup_repair', 'retention', 'backup',
        'restore_verify', 'export', 'import'
    )),
    state TEXT NOT NULL CHECK (state IN (
        'scheduled', 'running', 'passed', 'failed', 'cancelled',
        'interrupted', 'already_running'
    )),
    attempt INTEGER NOT NULL CHECK (attempt BETWEEN 1 AND 3),
    scheduled_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    lease_owner_id TEXT,
    lease_expires_at TIMESTAMPTZ,
    error_class TEXT,
    detail_ref TEXT,
    result_counts JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((state = 'running') = (lease_owner_id IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK (error_class IS NULL OR error_class ~ '^[a-z0-9_]{1,64}$'),
    CHECK (detail_ref IS NULL OR detail_ref ~ '^[a-z0-9_.:-]{1,160}$')
);

CREATE INDEX runtime_job_runs_job_state_idx
    ON runtime_job_runs (job_id, state, scheduled_at DESC);

CREATE UNIQUE INDEX runtime_job_runs_one_active_lease_idx
    ON runtime_job_runs (job_id)
    WHERE state = 'running';

CREATE TABLE runtime_operation_approvals (
    request_id TEXT PRIMARY KEY,
    operation TEXT NOT NULL CHECK (operation IN (
        'plan_apply', 'retention_apply', 'import', 'backup', 'restore_verify'
    )),
    parameters_sha256 TEXT NOT NULL CHECK (parameters_sha256 ~ '^[0-9a-f]{64}$'),
    approval_nonce_sha256 TEXT NOT NULL UNIQUE CHECK (approval_nonce_sha256 ~ '^[0-9a-f]{64}$'),
    approved_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    result TEXT CHECK (result IN ('approved', 'applied', 'rejected', 'failed'))
);

CREATE TABLE runtime_import_receipts (
    import_id TEXT PRIMARY KEY,
    idempotency_key_sha256 TEXT NOT NULL UNIQUE CHECK (idempotency_key_sha256 ~ '^[0-9a-f]{64}$'),
    manifest_sha256 TEXT NOT NULL CHECK (manifest_sha256 ~ '^[0-9a-f]{64}$'),
    imported_count BIGINT NOT NULL CHECK (imported_count >= 0),
    duplicate_count BIGINT NOT NULL CHECK (duplicate_count >= 0),
    completed_at TIMESTAMPTZ NOT NULL
);
