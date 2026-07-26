-- Identity-resolution incidents use the Session 12 unified incident model.
-- Runtime migration 0002 owns these columns in an upgraded installation;
-- these idempotent declarations make a fresh data-platform schema complete
-- enough to enforce the Session 14 handoff contract independently.
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ;
UPDATE incidents SET last_seen_at = opened_at WHERE last_seen_at IS NULL;
ALTER TABLE incidents ALTER COLUMN last_seen_at SET NOT NULL;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS occurrence_count BIGINT NOT NULL DEFAULT 1
    CHECK (occurrence_count > 0);
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS occurrence_retention_excluded_count BIGINT NOT NULL DEFAULT 0
    CHECK (occurrence_retention_excluded_count >= 0);
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS schema_fingerprint TEXT;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS detector_state TEXT NOT NULL DEFAULT 'open'
    CHECK (detector_state IN ('open', 'recovering', 'resolved'));
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS triage_state TEXT NOT NULL DEFAULT 'new'
    CHECK (triage_state IN ('new', 'acknowledged', 'investigating', 'action_ready'));
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS triage_note_category TEXT
    CHECK (triage_note_category IS NULL OR triage_note_category IN (
        'fixture_needed', 'parser_fix_prepared', 'source_owner_contacted', 'recovery_pending'
    ));
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS capability_id TEXT NOT NULL DEFAULT 'core_ingestion';
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS installation_id TEXT;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS installation_value_state TEXT NOT NULL DEFAULT 'not_observed'
    CHECK (installation_value_state IN (
        'observed', 'unsupported', 'not_observed', 'redacted', 'unknown'
    ));
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS source_id TEXT;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS source_value_state TEXT NOT NULL DEFAULT 'not_observed'
    CHECK (source_value_state IN (
        'observed', 'unsupported', 'not_observed', 'redacted', 'unknown'
    ));
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS severity TEXT NOT NULL DEFAULT 'warning'
    CHECK (severity IN ('info', 'warning', 'error', 'critical'));
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS adapter_version TEXT;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS source_schema_version TEXT;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS parser_version TEXT;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS recovery_criteria TEXT NOT NULL
    DEFAULT 'fresh supported evidence followed by a passing targeted audit';
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS recovery_observed_at TIMESTAMPTZ;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS recovery_audit_run_id TEXT;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS recovery_evidence_ref TEXT;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE TABLE IF NOT EXISTS incident_occurrences (
    incident_occurrence_id TEXT PRIMARY KEY,
    incident_id            TEXT NOT NULL,
    observed_at            TIMESTAMPTZ NOT NULL,
    evidence_ref           TEXT NOT NULL,
    schema_fingerprint     TEXT,
    safe_error_class       TEXT NOT NULL,
    record_count           INTEGER NOT NULL CHECK (record_count >= 0),
    byte_count             BIGINT NOT NULL CHECK (byte_count >= 0),
    idempotency_key        TEXT NOT NULL UNIQUE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_incident_occurrences_page
    ON incident_occurrences (incident_id, observed_at DESC, incident_occurrence_id DESC);

CREATE TABLE IF NOT EXISTS component_terminal_contracts (
    terminal_contract_id TEXT PRIMARY KEY,
    component_kind       TEXT NOT NULL,
    version_scope        TEXT NOT NULL,
    start_assertion      TEXT NOT NULL,
    terminal_assertion   TEXT NOT NULL,
    correlation_key      TEXT NOT NULL,
    terminal_deadline_ms BIGINT NOT NULL CHECK (terminal_deadline_ms > 0),
    evidence_tiers       TEXT[] NOT NULL,
    formula_version      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS component_assertions (
    assertion_id             TEXT PRIMARY KEY,
    component_installation_id TEXT REFERENCES component_installations(component_installation_id),
    agent_installation_id    TEXT NOT NULL REFERENCES agent_installations(agent_installation_id),
    session_id               TEXT REFERENCES sessions(session_id),
    turn_id                  TEXT REFERENCES turns(turn_id),
    event_id                 TEXT,
    evidence_id              TEXT,
    assertion_kind           TEXT NOT NULL CHECK (assertion_kind IN (
        'installed','enabled','exposed','invoked','loaded','child_activity','outcome'
    )),
    mode                     TEXT NOT NULL CHECK (mode IN (
        'explicit','proactive','nested','not_observed'
    )),
    outcome                  TEXT CHECK (outcome IN (
        'succeeded','failed','cancelled','timed_out','denied','unknown'
    )),
    terminal_contract_id     TEXT REFERENCES component_terminal_contracts(terminal_contract_id),
    evidence_tier            TEXT NOT NULL CHECK (evidence_tier IN (
        'corroborated','native','reconstructed','inferred'
    )),
    confidence               DOUBLE PRECISION NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    source_instance_id       TEXT NOT NULL REFERENCES source_instances(source_instance_id),
    adapter_version          TEXT NOT NULL,
    schema_version           TEXT NOT NULL,
    observed_at              TIMESTAMPTZ NOT NULL,
    idempotency_key          TEXT NOT NULL,
    identity_resolution      TEXT NOT NULL CHECK (identity_resolution IN (
        'exact','unresolved','ambiguous'
    )),
    declared_identity_pseudonym TEXT NOT NULL,
    candidate_count          INTEGER NOT NULL DEFAULT 0 CHECK (candidate_count >= 0),
    CHECK (
        (assertion_kind = 'outcome' AND outcome IS NOT NULL AND terminal_contract_id IS NOT NULL)
        OR (assertion_kind <> 'outcome' AND outcome IS NULL AND terminal_contract_id IS NULL)
    ),
    CHECK (
        (identity_resolution = 'exact' AND component_installation_id IS NOT NULL AND candidate_count = 1)
        OR (identity_resolution = 'unresolved' AND component_installation_id IS NULL AND candidate_count = 0)
        OR (identity_resolution = 'ambiguous' AND component_installation_id IS NULL AND candidate_count > 1)
    ),
    UNIQUE (source_instance_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS component_observation_windows (
    observation_window_id    TEXT PRIMARY KEY,
    component_installation_id TEXT NOT NULL REFERENCES component_installations(component_installation_id),
    source_instance_id       TEXT NOT NULL REFERENCES source_instances(source_instance_id),
    plane                    TEXT NOT NULL CHECK (plane IN ('availability','runtime')),
    window_start             TIMESTAMPTZ NOT NULL,
    window_end               TIMESTAMPTZ NOT NULL,
    completeness             TEXT NOT NULL CHECK (completeness IN (
        'complete','partial','degraded','unknown'
    )),
    idempotency_key          TEXT NOT NULL,
    CHECK (window_end > window_start),
    UNIQUE (source_instance_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS component_file_tree_metadata (
    component_installation_id TEXT NOT NULL REFERENCES component_installations(component_installation_id),
    inventory_snapshot_id     TEXT NOT NULL REFERENCES inventory_snapshots(snapshot_id),
    node_pseudonym            TEXT NOT NULL,
    parent_pseudonym          TEXT,
    entry_kind                TEXT NOT NULL CHECK (entry_kind IN ('file','directory')),
    depth                     INTEGER NOT NULL CHECK (depth >= 0 AND depth <= 32),
    byte_count                BIGINT CHECK (byte_count IS NULL OR byte_count >= 0),
    PRIMARY KEY (component_installation_id, inventory_snapshot_id, node_pseudonym)
);

CREATE INDEX IF NOT EXISTS component_assertions_profile_idx
    ON component_assertions (component_installation_id, observed_at DESC, assertion_kind);
CREATE INDEX IF NOT EXISTS component_assertions_installation_idx
    ON component_assertions (agent_installation_id, observed_at DESC, assertion_kind);
CREATE INDEX IF NOT EXISTS component_assertions_unresolved_idx
    ON component_assertions (identity_resolution, observed_at DESC)
    WHERE identity_resolution <> 'exact';
CREATE INDEX IF NOT EXISTS component_observation_windows_range_idx
    ON component_observation_windows (component_installation_id, plane, window_start, window_end);
