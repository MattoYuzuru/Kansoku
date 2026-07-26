-- Session 12 incident workbench. Existing ingress and integrity projections
-- remain intact; this migration adds their shared occurrence/read metadata
-- without rewriting historical telemetry.

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
    CHECK (installation_value_state IN ('observed', 'unsupported', 'not_observed', 'redacted', 'unknown'));
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS source_id TEXT;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS source_value_state TEXT NOT NULL DEFAULT 'not_observed'
    CHECK (source_value_state IN ('observed', 'unsupported', 'not_observed', 'redacted', 'unknown'));
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
UPDATE incidents
SET detector_state = CASE WHEN resolved_at IS NULL THEN 'open' ELSE 'resolved' END;
CREATE INDEX IF NOT EXISTS idx_incidents_workbench_page
    ON incidents (last_seen_at DESC, incident_id DESC);

CREATE TABLE IF NOT EXISTS incident_occurrences (
    incident_occurrence_id TEXT PRIMARY KEY,
    incident_id TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    evidence_ref TEXT NOT NULL,
    schema_fingerprint TEXT,
    safe_error_class TEXT NOT NULL,
    record_count INTEGER NOT NULL CHECK (record_count >= 0),
    byte_count BIGINT NOT NULL CHECK (byte_count >= 0),
    idempotency_key TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_incident_occurrences_page
    ON incident_occurrences (incident_id, observed_at DESC, incident_occurrence_id DESC);

CREATE TABLE IF NOT EXISTS quarantine_structural_manifests (
    quarantine_id TEXT PRIMARY KEY REFERENCES schema_quarantine_metadata(quarantine_id),
    incident_id TEXT NOT NULL,
    source_kind TEXT NOT NULL,
    source_instance_pseudonym TEXT,
    source_instance_value_state TEXT NOT NULL
        CHECK (source_instance_value_state IN ('observed', 'unsupported', 'not_observed', 'redacted', 'unknown')),
    signal_kind TEXT NOT NULL,
    safe_event_type TEXT,
    event_type_value_state TEXT NOT NULL
        CHECK (event_type_value_state IN ('observed', 'unsupported', 'not_observed', 'redacted', 'unknown')),
    structural_field_paths JSONB NOT NULL DEFAULT '[]'::jsonb,
    primitive_types JSONB NOT NULL DEFAULT '["object"]'::jsonb,
    shape_value_state TEXT NOT NULL
        CHECK (shape_value_state IN ('observed', 'unsupported', 'not_observed', 'redacted', 'unknown')),
    schema_fingerprint TEXT NOT NULL,
    adapter_version TEXT,
    source_schema_version TEXT,
    parser_version TEXT,
    classification TEXT NOT NULL,
    rejection_reason TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    occurrence_count BIGINT NOT NULL DEFAULT 1 CHECK (occurrence_count > 0),
    total_record_count BIGINT NOT NULL DEFAULT 0 CHECK (total_record_count >= 0),
    total_byte_count BIGINT NOT NULL DEFAULT 0 CHECK (total_byte_count >= 0),
    disposition TEXT NOT NULL DEFAULT 'unresolved'
        CHECK (disposition IN ('unresolved', 'fixture_added', 'supported', 'unsupported')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(structural_field_paths) = 'array'),
    CHECK (jsonb_typeof(primitive_types) = 'array')
);
CREATE INDEX IF NOT EXISTS idx_quarantine_manifests_page
    ON quarantine_structural_manifests (last_seen_at DESC, quarantine_id DESC);

INSERT INTO incident_occurrences (
    incident_occurrence_id, incident_id, observed_at, evidence_ref,
    schema_fingerprint, safe_error_class, record_count, byte_count, idempotency_key
)
SELECT
    'ioc_legacy_' || substr(md5(i.incident_id), 1, 20),
    i.incident_id, i.opened_at, 'legacy:incidents:' || i.incident_id,
    NULL, i.category, 0, 0, 'legacy:incidents:' || i.incident_id
FROM incidents i
ON CONFLICT (idempotency_key) DO NOTHING;

INSERT INTO incident_occurrences (
    incident_occurrence_id, incident_id, observed_at, evidence_ref,
    schema_fingerprint, safe_error_class, record_count, byte_count, idempotency_key
)
SELECT
    'ioc_integrity_' || substr(md5(i.incident_id), 1, 17),
    i.incident_id, i.opened_at, 'legacy:integrity:' || i.incident_id,
    NULL, i.category, 0, 0, 'legacy:integrity:' || i.incident_id
FROM integrity_incidents i
ON CONFLICT (idempotency_key) DO NOTHING;

INSERT INTO quarantine_structural_manifests (
    quarantine_id, incident_id, source_kind, source_instance_pseudonym,
    source_instance_value_state, signal_kind, safe_event_type,
    event_type_value_state, structural_field_paths, primitive_types,
    shape_value_state, schema_fingerprint, classification, rejection_reason,
    first_seen_at, last_seen_at, occurrence_count, total_record_count,
    total_byte_count
)
SELECT
    q.quarantine_id,
    -- The legacy quarantine table has no incident reference. Assigning it to
    -- an arbitrary same-category incident would fabricate lineage, so retain
    -- an explicit deterministic unlinked identity for the migration record.
    'inc_unlinked_' || substr(md5(q.quarantine_id), 1, 19),
    q.source_kind, NULL, 'not_observed', q.source_kind, NULL, 'unknown',
    '[]'::jsonb, '["object"]'::jsonb, 'not_observed',
    q.schema_fingerprint, 'metadata_only_unknown_schema', q.category,
    q.observed_at, q.observed_at, 1, q.record_count, q.byte_count
FROM schema_quarantine_metadata q
ON CONFLICT (quarantine_id) DO NOTHING;
