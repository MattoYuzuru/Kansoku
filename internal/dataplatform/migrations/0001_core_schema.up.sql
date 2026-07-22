-- Session 04 core schema: dimensions, activity facts, quality/operations tables.
-- Range partitioning by observed_at (monthly) for high-volume fact tables.

CREATE TABLE IF NOT EXISTS devices (
    device_id           TEXT PRIMARY KEY,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agent_installations (
    agent_installation_id TEXT PRIMARY KEY,
    device_id             TEXT NOT NULL REFERENCES devices(device_id),
    agent_id              TEXT NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agent_surfaces (
    surface_id             TEXT PRIMARY KEY,
    agent_installation_id  TEXT NOT NULL REFERENCES agent_installations(agent_installation_id),
    surface_kind           TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_versions (
    agent_version_id TEXT PRIMARY KEY,
    agent_id         TEXT NOT NULL,
    version          TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS projects (
    project_id  TEXT PRIMARY KEY,
    alias       TEXT
);

CREATE TABLE IF NOT EXISTS providers (
    provider_id TEXT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS models (
    model_id    TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(provider_id)
);

CREATE TABLE IF NOT EXISTS price_catalog_versions (
    price_catalog_version_id TEXT PRIMARY KEY,
    model_id                 TEXT NOT NULL REFERENCES models(model_id),
    effective_at             TIMESTAMPTZ NOT NULL,
    input_price_micros       BIGINT NOT NULL CHECK (input_price_micros >= 0),
    output_price_micros      BIGINT NOT NULL CHECK (output_price_micros >= 0)
);

CREATE TABLE IF NOT EXISTS components (
    component_id TEXT PRIMARY KEY,
    kind         TEXT NOT NULL CHECK (kind IN ('skill', 'plugin', 'mcp', 'hook', 'command'))
);

CREATE TABLE IF NOT EXISTS component_versions (
    component_version_id TEXT PRIMARY KEY,
    component_id          TEXT NOT NULL REFERENCES components(component_id),
    version               TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS component_installations (
    component_installation_id TEXT PRIMARY KEY,
    component_version_id      TEXT NOT NULL REFERENCES component_versions(component_version_id),
    agent_installation_id     TEXT NOT NULL REFERENCES agent_installations(agent_installation_id)
);

CREATE TABLE IF NOT EXISTS component_relations (
    relation_id     TEXT PRIMARY KEY,
    parent_id       TEXT NOT NULL REFERENCES components(component_id),
    child_id        TEXT NOT NULL REFERENCES components(component_id),
    relation_kind   TEXT NOT NULL CHECK (relation_kind IN ('bundles', 'collides_with', 'shadows'))
);

CREATE TABLE IF NOT EXISTS adapter_versions (
    adapter_version_id TEXT PRIMARY KEY,
    adapter_id         TEXT NOT NULL,
    version            TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS source_instances (
    source_instance_id TEXT PRIMARY KEY,
    adapter_version_id TEXT NOT NULL REFERENCES adapter_versions(adapter_version_id),
    source_kind        TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS source_schema_fingerprints (
    schema_fingerprint TEXT PRIMARY KEY,
    source_schema_id   TEXT NOT NULL,
    first_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    session_id  TEXT PRIMARY KEY,
    project_id  TEXT REFERENCES projects(project_id),
    started_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS turns (
    turn_id     TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(session_id),
    started_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS prompt_features (
    prompt_feature_id TEXT PRIMARY KEY,
    turn_id            TEXT NOT NULL REFERENCES turns(turn_id),
    observed_at         TIMESTAMPTZ NOT NULL,
    prompt_size_bytes    BIGINT CHECK (prompt_size_bytes IS NULL OR prompt_size_bytes >= 0),
    value_state           TEXT NOT NULL CHECK (value_state IN ('unsupported', 'not_observed', 'redacted', 'unknown', 'numeric_zero', 'observed'))
);

-- events: partitioned monthly by observed_at. Idempotency is unique per
-- source instance + native event id, scoped inside each partition (the
-- partition key observed_at also participates so that the unique index can
-- be a genuine per-partition local index).
CREATE TABLE IF NOT EXISTS events (
    event_id            TEXT NOT NULL,
    fact_key             TEXT NOT NULL,
    event_type           TEXT NOT NULL,
    observed_at           TIMESTAMPTZ NOT NULL,
    ingested_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    timestamp_quality     TEXT NOT NULL DEFAULT 'source_rfc3339',
    source_instance_id    TEXT NOT NULL REFERENCES source_instances(source_instance_id),
    source_native_event_id TEXT NOT NULL,
    sequence              BIGINT NOT NULL CHECK (sequence >= 0),
    agent_installation_id TEXT REFERENCES agent_installations(agent_installation_id),
    surface_id            TEXT REFERENCES agent_surfaces(surface_id),
    project_id            TEXT REFERENCES projects(project_id),
    session_id            TEXT REFERENCES sessions(session_id),
    turn_id               TEXT REFERENCES turns(turn_id),
    component_id          TEXT REFERENCES components(component_id),
    duration_ms            BIGINT CHECK (duration_ms IS NULL OR duration_ms >= 0),
    success                BOOLEAN,
    count                  BIGINT CHECK (count IS NULL OR count >= 0),
    value_state            TEXT NOT NULL CHECK (value_state IN ('unsupported', 'not_observed', 'redacted', 'unknown', 'numeric_zero', 'observed')),
    outcome                 TEXT NOT NULL CHECK (outcome IN ('succeeded', 'failed', 'cancelled', 'interrupted', 'timed_out', 'abandoned', 'unknown')),
    correlation_status      TEXT NOT NULL CHECK (correlation_status IN ('exact', 'candidate', 'ambiguous', 'unmatched')),
    source_extension        JSONB,
    PRIMARY KEY (event_id, observed_at)
) PARTITION BY RANGE (observed_at);

CREATE UNIQUE INDEX IF NOT EXISTS events_idempotency
    ON events (source_instance_id, source_native_event_id, observed_at);

CREATE TABLE IF NOT EXISTS event_evidence (
    evidence_id            TEXT NOT NULL,
    event_id                TEXT NOT NULL,
    observed_at              TIMESTAMPTZ NOT NULL,
    source_instance_id       TEXT NOT NULL REFERENCES source_instances(source_instance_id),
    tier                      TEXT NOT NULL CHECK (tier IN ('corroborated', 'native', 'reconstructed', 'inferred')),
    confidence                DOUBLE PRECISION NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    completeness              TEXT NOT NULL CHECK (completeness IN ('complete', 'partial', 'degraded', 'unknown', 'unsupported')),
    replay_count              BIGINT NOT NULL DEFAULT 0 CHECK (replay_count >= 0),
    first_seen_at             TIMESTAMPTZ NOT NULL,
    last_seen_at              TIMESTAMPTZ NOT NULL,
    sanitizer_version          TEXT NOT NULL,
    privacy_contract_sha256    TEXT NOT NULL,
    assertion_event_type       TEXT NOT NULL,
    assertion_outcome          TEXT NOT NULL,
    assertion_value_state      TEXT NOT NULL,
    PRIMARY KEY (evidence_id, observed_at)
) PARTITION BY RANGE (observed_at);

CREATE TABLE IF NOT EXISTS model_operations (
    model_operation_id TEXT NOT NULL,
    observed_at         TIMESTAMPTZ NOT NULL,
    event_id             TEXT,
    model_id             TEXT NOT NULL REFERENCES models(model_id),
    session_id           TEXT REFERENCES sessions(session_id),
    PRIMARY KEY (model_operation_id, observed_at)
) PARTITION BY RANGE (observed_at);

CREATE TABLE IF NOT EXISTS token_usage (
    token_usage_id      TEXT NOT NULL,
    observed_at          TIMESTAMPTZ NOT NULL,
    model_operation_id   TEXT NOT NULL,
    input_tokens          BIGINT NOT NULL CHECK (input_tokens >= 0),
    output_tokens         BIGINT NOT NULL CHECK (output_tokens >= 0),
    PRIMARY KEY (token_usage_id, observed_at)
) PARTITION BY RANGE (observed_at);

CREATE TABLE IF NOT EXISTS cost_estimates (
    cost_estimate_id       TEXT PRIMARY KEY,
    token_usage_id          TEXT NOT NULL,
    price_catalog_version_id TEXT NOT NULL REFERENCES price_catalog_versions(price_catalog_version_id),
    cost_micros              BIGINT NOT NULL CHECK (cost_micros >= 0)
);

CREATE TABLE IF NOT EXISTS component_lifecycle_events (
    component_lifecycle_event_id TEXT PRIMARY KEY,
    component_installation_id     TEXT NOT NULL REFERENCES component_installations(component_installation_id),
    observed_at                    TIMESTAMPTZ NOT NULL,
    lifecycle_stage                TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tool_calls (
    tool_call_id  TEXT NOT NULL,
    observed_at    TIMESTAMPTZ NOT NULL,
    event_id        TEXT,
    component_id    TEXT REFERENCES components(component_id),
    session_id      TEXT REFERENCES sessions(session_id),
    duration_ms      BIGINT CHECK (duration_ms IS NULL OR duration_ms >= 0),
    outcome          TEXT NOT NULL CHECK (outcome IN ('succeeded', 'failed', 'cancelled', 'interrupted', 'timed_out', 'abandoned', 'unknown')),
    source_extension JSONB,
    PRIMARY KEY (tool_call_id, observed_at)
) PARTITION BY RANGE (observed_at);

CREATE TABLE IF NOT EXISTS mcp_connections (
    mcp_connection_id TEXT NOT NULL,
    observed_at        TIMESTAMPTZ NOT NULL,
    component_id        TEXT REFERENCES components(component_id),
    session_id           TEXT REFERENCES sessions(session_id),
    state                 TEXT NOT NULL,
    PRIMARY KEY (mcp_connection_id, observed_at)
) PARTITION BY RANGE (observed_at);

CREATE TABLE IF NOT EXISTS change_outcomes (
    change_outcome_id TEXT PRIMARY KEY,
    event_id           TEXT,
    kind               TEXT NOT NULL CHECK (kind IN ('edit', 'test', 'commit')),
    outcome             TEXT NOT NULL CHECK (outcome IN ('succeeded', 'failed', 'cancelled', 'interrupted', 'timed_out', 'abandoned', 'unknown'))
);

CREATE TABLE IF NOT EXISTS correlations (
    correlation_id TEXT PRIMARY KEY,
    event_id        TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('exact', 'candidate', 'ambiguous', 'unmatched'))
);

CREATE TABLE IF NOT EXISTS source_watermarks (
    source_instance_id     TEXT PRIMARY KEY REFERENCES source_instances(source_instance_id),
    last_read_sequence      BIGINT NOT NULL DEFAULT 0,
    last_emitted_sequence   BIGINT NOT NULL DEFAULT 0,
    last_observed_at        TIMESTAMPTZ,
    last_committed_at        TIMESTAMPTZ,
    gap_count                BIGINT NOT NULL DEFAULT 0 CHECK (gap_count >= 0),
    inactivity                BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS completeness_intervals (
    completeness_interval_id TEXT PRIMARY KEY,
    dimension_scope           JSONB NOT NULL,
    interval_start             TIMESTAMPTZ NOT NULL,
    interval_end               TIMESTAMPTZ NOT NULL,
    status                     TEXT NOT NULL CHECK (status IN ('complete', 'partial', 'degraded', 'unknown'))
);

CREATE TABLE IF NOT EXISTS ingest_failures (
    ingest_failure_id TEXT PRIMARY KEY,
    category           TEXT NOT NULL,
    observed_at         TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS schema_quarantine_metadata (
    quarantine_id      TEXT PRIMARY KEY,
    source_kind         TEXT NOT NULL,
    schema_fingerprint  TEXT NOT NULL,
    category             TEXT NOT NULL,
    byte_count           BIGINT NOT NULL CHECK (byte_count >= 0),
    record_count         INTEGER NOT NULL CHECK (record_count >= 0),
    observed_at           TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS reconciliation_runs (
    reconciliation_run_id TEXT PRIMARY KEY,
    started_at             TIMESTAMPTZ NOT NULL,
    finished_at             TIMESTAMPTZ,
    status                  TEXT NOT NULL CHECK (status IN ('running', 'passed', 'failed'))
);

CREATE TABLE IF NOT EXISTS reconciliation_mismatches (
    reconciliation_mismatch_id TEXT PRIMARY KEY,
    reconciliation_run_id       TEXT NOT NULL REFERENCES reconciliation_runs(reconciliation_run_id),
    fact_key                     TEXT NOT NULL,
    category                     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_runs (
    audit_run_id TEXT PRIMARY KEY,
    started_at    TIMESTAMPTZ NOT NULL,
    finished_at    TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS audit_checks (
    audit_check_id TEXT PRIMARY KEY,
    audit_run_id     TEXT NOT NULL REFERENCES audit_runs(audit_run_id),
    check_name        TEXT NOT NULL,
    status             TEXT NOT NULL CHECK (status IN ('pass', 'fail'))
);

CREATE TABLE IF NOT EXISTS incidents (
    incident_id      TEXT PRIMARY KEY,
    category          TEXT NOT NULL,
    opened_at          TIMESTAMPTZ NOT NULL,
    resolved_at        TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS retention_policies (
    retention_policy_id TEXT PRIMARY KEY,
    table_name           TEXT NOT NULL,
    horizon_days          INTEGER NOT NULL CHECK (horizon_days > 0),
    aggregate_only_after   BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS backup_runs (
    backup_run_id TEXT PRIMARY KEY,
    started_at     TIMESTAMPTZ NOT NULL,
    finished_at     TIMESTAMPTZ,
    checksum_sha256  TEXT,
    manifest         JSONB NOT NULL
);

CREATE TABLE IF NOT EXISTS restore_tests (
    restore_test_id TEXT PRIMARY KEY,
    backup_run_id     TEXT NOT NULL REFERENCES backup_runs(backup_run_id),
    started_at         TIMESTAMPTZ NOT NULL,
    finished_at         TIMESTAMPTZ,
    status               TEXT NOT NULL CHECK (status IN ('running', 'passed', 'failed'))
);

CREATE TABLE IF NOT EXISTS formula_versions (
    formula_id     TEXT NOT NULL,
    version         INTEGER NOT NULL CHECK (version > 0),
    sql_template     TEXT NOT NULL,
    unit             TEXT NOT NULL,
    dimensions       JSONB NOT NULL,
    numerator        TEXT,
    denominator      TEXT,
    population       TEXT,
    minimum_sample   INTEGER NOT NULL DEFAULT 0 CHECK (minimum_sample >= 0),
    allowed_completeness JSONB NOT NULL,
    formatting        TEXT,
    PRIMARY KEY (formula_id, version)
);

CREATE TABLE IF NOT EXISTS rollup_status (
    metric_family     TEXT NOT NULL,
    granularity        TEXT NOT NULL CHECK (granularity IN ('hourly', 'daily')),
    dimension_scope     TEXT NOT NULL,
    rollup_watermark     TIMESTAMPTZ NOT NULL,
    late_events_pending   BIGINT NOT NULL DEFAULT 0 CHECK (late_events_pending >= 0),
    PRIMARY KEY (metric_family, granularity, dimension_scope)
);

-- rollups: NOT partitioned (bounded cardinality per bucket; retained separately from raw facts).
CREATE TABLE IF NOT EXISTS metric_rollups_hourly (
    metric_family        TEXT NOT NULL,
    bucket_start           TIMESTAMPTZ NOT NULL,
    dimension_scope         TEXT NOT NULL,
    formula_version          TEXT NOT NULL,
    event_count               BIGINT NOT NULL CHECK (event_count >= 0),
    unknown_count             BIGINT NOT NULL DEFAULT 0 CHECK (unknown_count >= 0),
    completeness_duration_ms   BIGINT NOT NULL DEFAULT 0 CHECK (completeness_duration_ms >= 0),
    value_numeric              DOUBLE PRECISION,
    value_p50                  DOUBLE PRECISION,
    value_p90                  DOUBLE PRECISION,
    value_p95                  DOUBLE PRECISION,
    value_p99                  DOUBLE PRECISION,
    computed_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (metric_family, bucket_start, dimension_scope)
);

CREATE TABLE IF NOT EXISTS metric_rollups_daily (
    metric_family        TEXT NOT NULL,
    bucket_start           TIMESTAMPTZ NOT NULL,
    dimension_scope         TEXT NOT NULL,
    formula_version          TEXT NOT NULL,
    event_count               BIGINT NOT NULL CHECK (event_count >= 0),
    unknown_count             BIGINT NOT NULL DEFAULT 0 CHECK (unknown_count >= 0),
    completeness_duration_ms   BIGINT NOT NULL DEFAULT 0 CHECK (completeness_duration_ms >= 0),
    value_numeric              DOUBLE PRECISION,
    value_p50                  DOUBLE PRECISION,
    value_p90                  DOUBLE PRECISION,
    value_p95                  DOUBLE PRECISION,
    value_p99                  DOUBLE PRECISION,
    computed_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (metric_family, bucket_start, dimension_scope)
);

CREATE TABLE IF NOT EXISTS rollup_repair_queue (
    repair_id       BIGSERIAL PRIMARY KEY,
    metric_family    TEXT NOT NULL,
    granularity       TEXT NOT NULL CHECK (granularity IN ('hourly', 'daily')),
    bucket_start       TIMESTAMPTZ NOT NULL,
    dimension_scope     TEXT NOT NULL,
    enqueued_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at            TIMESTAMPTZ,
    UNIQUE (metric_family, granularity, bucket_start, dimension_scope)
);
