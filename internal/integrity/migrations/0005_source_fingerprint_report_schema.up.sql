-- Session 08 final integrity schema: explicit source identity, structural
-- drift snapshots, live-canary cooldown state, and signed/versioned reports.

ALTER TABLE integrity_audit_checks ADD COLUMN IF NOT EXISTS source_id TEXT NOT NULL DEFAULT '';
ALTER TABLE integrity_audit_checks DROP CONSTRAINT IF EXISTS integrity_audit_checks_pkey;
ALTER TABLE integrity_audit_checks
    ADD PRIMARY KEY (audit_run_id, check_id, capability_id, installation_id, source_id);
CREATE INDEX IF NOT EXISTS idx_integrity_audit_checks_source_key
    ON integrity_audit_checks (installation_id, source_id, capability_id, stage_id);

CREATE TABLE IF NOT EXISTS integrity_audit_attempts (
    attempt_id TEXT PRIMARY KEY,
    run_mode TEXT NOT NULL CHECK (run_mode IN ('full', 'reduced')),
    trigger TEXT NOT NULL CHECK (trigger IN ('scheduled_daily', 'startup', 'version_change_detected', 'manual_operator_request')),
    attempted_at TIMESTAMPTZ NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('already_running')),
    advisory_lock_key BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS integrity_fingerprints (
    fingerprint_kind TEXT NOT NULL CHECK (fingerprint_kind IN (
        'executable_version', 'config_recipe_fingerprint', 'adapter_version',
        'fixture_version', 'formula_registry_version', 'event_schema_fingerprint'
    )),
    subject_id TEXT NOT NULL,
    source_id TEXT NOT NULL DEFAULT '',
    capability_id TEXT NOT NULL DEFAULT '',
    value_ref TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (fingerprint_kind, subject_id, source_id, capability_id)
);

CREATE TABLE IF NOT EXISTS integrity_schema_compatibility (
    adapter_id TEXT NOT NULL,
    adapter_version TEXT NOT NULL,
    event_schema_fingerprint TEXT NOT NULL,
    review_reference TEXT NOT NULL,
    approved_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (adapter_id, adapter_version, event_schema_fingerprint)
);

CREATE TABLE IF NOT EXISTS integrity_live_canary_state (
    recipe_id TEXT PRIMARY KEY,
    adapter_id TEXT,
    credentials_confirmed_at TIMESTAMPTZ,
    consent_recorded_at TIMESTAMPTZ,
    last_started_at TIMESTAMPTZ,
    last_finished_at TIMESTAMPTZ,
    last_status TEXT CHECK (last_status IN ('pass', 'fail', 'skipped_unsupported')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS integrity_audit_reports (
    audit_run_id TEXT PRIMARY KEY REFERENCES integrity_audit_runs (audit_run_id),
    report_schema_version TEXT NOT NULL,
    generated_at TIMESTAMPTZ NOT NULL,
    canonical_report JSONB NOT NULL,
    report_sha256 TEXT NOT NULL,
    signature_algorithm TEXT NOT NULL CHECK (signature_algorithm = 'hmac-sha256'),
    signature_key_id TEXT NOT NULL,
    signature TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
