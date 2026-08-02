-- Additive P0 operational state. These rows contain counters and capacity
-- metadata only; no prompt, response, tool payload, environment value or
-- filesystem path has a column in this schema.

CREATE TABLE IF NOT EXISTS runtime_ingestion_health (
    source_kind TEXT PRIMARY KEY,
    last_successful_ingest_at TIMESTAMPTZ,
    last_rejected_ingest_at TIMESTAMPTZ,
    backpressure_rejected_total BIGINT NOT NULL DEFAULT 0
        CHECK (backpressure_rejected_total >= 0),
    durability_unavailable_total BIGINT NOT NULL DEFAULT 0
        CHECK (durability_unavailable_total >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS runtime_capacity_samples (
    sampled_at TIMESTAMPTZ PRIMARY KEY,
    database_bytes BIGINT NOT NULL CHECK (database_bytes >= 0),
    index_bytes BIGINT NOT NULL CHECK (index_bytes >= 0),
    backup_bytes BIGINT CHECK (backup_bytes IS NULL OR backup_bytes >= 0),
    emergency_spool_bytes BIGINT NOT NULL CHECK (emergency_spool_bytes >= 0),
    checkpoint_bytes BIGINT NOT NULL CHECK (checkpoint_bytes >= 0),
    filesystem_available_bytes BIGINT CHECK (
        filesystem_available_bytes IS NULL OR filesystem_available_bytes >= 0
    )
);

CREATE TABLE IF NOT EXISTS runtime_source_health (
    source_id TEXT PRIMARY KEY,
    state TEXT NOT NULL CHECK (
        state IN (
            'configured','producing','degraded','not_configured','unsupported'
        )
    ),
    value_state TEXT NOT NULL CHECK (
        value_state IN (
            'observed','unsupported','not_observed','redacted','unknown'
        )
    ),
    last_attempted_at TIMESTAMPTZ,
    last_successful_at TIMESTAMPTZ,
    last_error_class TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        last_error_class IS NULL OR
        last_error_class ~ '^[a-z0-9_]{1,64}$'
    )
);

CREATE INDEX IF NOT EXISTS runtime_capacity_samples_recent_idx
    ON runtime_capacity_samples (sampled_at DESC);

CREATE TABLE IF NOT EXISTS runtime_mirror_reconciliations (
    reconciliation_id TEXT PRIMARY KEY,
    mirror_sha256 TEXT NOT NULL CHECK (mirror_sha256 ~ '^[0-9a-f]{64}$'),
    mirror_bytes BIGINT NOT NULL CHECK (mirror_bytes >= 0),
    mirror_revision BIGINT NOT NULL CHECK (mirror_revision >= 0),
    mirror_fact_count BIGINT NOT NULL CHECK (mirror_fact_count >= 0),
    database_fact_count BIGINT NOT NULL CHECK (database_fact_count >= 0),
    mirror_only_fact_count BIGINT NOT NULL CHECK (mirror_only_fact_count >= 0),
    database_only_fact_count BIGINT NOT NULL CHECK (database_only_fact_count >= 0),
    mirror_evidence_count BIGINT NOT NULL CHECK (mirror_evidence_count >= 0),
    database_evidence_count BIGINT NOT NULL CHECK (database_evidence_count >= 0),
    mirror_only_evidence_count BIGINT NOT NULL CHECK (mirror_only_evidence_count >= 0),
    database_only_evidence_count BIGINT NOT NULL CHECK (database_only_evidence_count >= 0),
    lineage_mismatch_count BIGINT NOT NULL CHECK (lineage_mismatch_count >= 0),
    checkpoint_count BIGINT NOT NULL CHECK (checkpoint_count >= 0),
    watermark_count BIGINT NOT NULL CHECK (watermark_count >= 0),
    quarantine_fingerprint_count BIGINT NOT NULL
        CHECK (quarantine_fingerprint_count >= 0),
    status TEXT NOT NULL CHECK (status IN ('reconciled', 'blocked')),
    backup_artifact_id TEXT NOT NULL,
    archive_artifact_id TEXT,
    exclusions JSONB NOT NULL DEFAULT '{}'::jsonb,
    reconciled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(exclusions) = 'object')
);
