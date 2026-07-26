CREATE TABLE IF NOT EXISTS mcp_server_observations (
    server_observation_id TEXT PRIMARY KEY,
    server_component_id TEXT NOT NULL REFERENCES components(component_id),
    agent_installation_id TEXT NOT NULL REFERENCES agent_installations(agent_installation_id),
    source_instance_id TEXT NOT NULL REFERENCES source_instances(source_instance_id),
    scope TEXT NOT NULL CHECK (scope IN ('user','system','repository','managed','unknown')),
    configured BOOLEAN NOT NULL,
    enabled BOOLEAN NOT NULL,
    transport TEXT NOT NULL CHECK (transport IN ('stdio','streamable_http','sse','websocket','other','unknown')),
    locality TEXT NOT NULL CHECK (locality IN ('local','remote','unknown')),
    configuration_fingerprint TEXT NOT NULL,
    protocol_version_state TEXT NOT NULL CHECK (protocol_version_state IN ('observed','not_observed','redacted','unknown','unsupported')),
    protocol_version TEXT,
    server_version_state TEXT NOT NULL CHECK (server_version_state IN ('observed','not_observed','redacted','unknown','unsupported')),
    server_version TEXT,
    capability_fingerprint TEXT,
    enumeration_completeness TEXT NOT NULL CHECK (enumeration_completeness IN ('complete','partial','degraded','unknown')),
    observed_at TIMESTAMPTZ NOT NULL,
    adapter_version TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    UNIQUE (source_instance_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS mcp_primitive_observations (
    primitive_observation_id TEXT PRIMARY KEY,
    server_component_id TEXT NOT NULL REFERENCES components(component_id),
    primitive_component_id TEXT NOT NULL REFERENCES components(component_id),
    source_instance_id TEXT NOT NULL REFERENCES source_instances(source_instance_id),
    primitive_kind TEXT NOT NULL CHECK (primitive_kind IN ('tool','resource','resource_template','prompt')),
    approved_display_alias TEXT NOT NULL,
    structural_schema_fingerprint TEXT,
    input_schema_present BOOLEAN NOT NULL DEFAULT FALSE,
    output_schema_present BOOLEAN NOT NULL DEFAULT FALSE,
    description_byte_count BIGINT CHECK (description_byte_count IS NULL OR description_byte_count >= 0),
    schema_byte_count BIGINT CHECK (schema_byte_count IS NULL OR schema_byte_count >= 0),
    annotation_claim_flags TEXT[] NOT NULL DEFAULT '{}',
    page_number INTEGER NOT NULL CHECK (page_number > 0),
    revision TEXT NOT NULL,
    enumeration_completeness TEXT NOT NULL CHECK (enumeration_completeness IN ('complete','partial','degraded','unknown')),
    first_advertised_at TIMESTAMPTZ NOT NULL,
    last_advertised_at TIMESTAMPTZ NOT NULL,
    adapter_version TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    UNIQUE (source_instance_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS mcp_connection_assertions (
    connection_assertion_id TEXT PRIMARY KEY,
    server_component_id TEXT NOT NULL REFERENCES components(component_id),
    agent_installation_id TEXT NOT NULL REFERENCES agent_installations(agent_installation_id),
    session_id TEXT REFERENCES sessions(session_id),
    source_instance_id TEXT NOT NULL REFERENCES source_instances(source_instance_id),
    attempt_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('configured','connecting','connected','failed','disconnected','timed_out','unknown')),
    observed_at TIMESTAMPTZ NOT NULL,
    duration_ms BIGINT CHECK (duration_ms IS NULL OR duration_ms >= 0),
    transport TEXT NOT NULL CHECK (transport IN ('stdio','streamable_http','sse','websocket','other','unknown')),
    negotiated_protocol TEXT,
    capability_fingerprint TEXT,
    failure_class TEXT NOT NULL CHECK (failure_class IN ('none','version_mismatch','capability_negotiation','auth','transport','process_exit','timeout','unknown')),
    evidence_tier TEXT NOT NULL CHECK (evidence_tier IN ('corroborated','native','reconstructed','inferred')),
    confidence DOUBLE PRECISION NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    adapter_version TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    UNIQUE (source_instance_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS mcp_call_assertions (
    call_assertion_id TEXT PRIMARY KEY,
    logical_call_id TEXT NOT NULL,
    server_component_id TEXT NOT NULL REFERENCES components(component_id),
    tool_component_id TEXT NOT NULL REFERENCES components(component_id),
    agent_installation_id TEXT NOT NULL REFERENCES agent_installations(agent_installation_id),
    session_id TEXT REFERENCES sessions(session_id),
    source_instance_id TEXT NOT NULL REFERENCES source_instances(source_instance_id),
    state TEXT NOT NULL CHECK (state IN ('decided','denied','started','progressing','completed','execution_error','protocol_error','cancelled','timed_out','transport_lost','incomplete')),
    observed_at TIMESTAMPTZ NOT NULL,
    duration_ms BIGINT CHECK (duration_ms IS NULL OR duration_ms >= 0),
    safe_error_class TEXT NOT NULL CHECK (safe_error_class IN ('none','json_rpc','execution','timeout','cancelled','policy_denial','transport_loss','missing_terminal','contradictory_terminal','unknown')),
    approval_decision TEXT NOT NULL CHECK (approval_decision IN ('not_observed','allowed','denied')),
    approval_source TEXT NOT NULL CHECK (approval_source IN ('not_observed','user','policy','agent','system')),
    request_byte_count BIGINT CHECK (request_byte_count IS NULL OR request_byte_count >= 0),
    result_byte_count BIGINT CHECK (result_byte_count IS NULL OR result_byte_count >= 0),
    result_item_count BIGINT CHECK (result_item_count IS NULL OR result_item_count >= 0),
    result_type_categories TEXT[] NOT NULL DEFAULT '{}',
    progress_count BIGINT NOT NULL DEFAULT 0 CHECK (progress_count >= 0),
    last_progress_at TIMESTAMPTZ,
    retry_of_logical_call_id TEXT,
    evidence_tier TEXT NOT NULL CHECK (evidence_tier IN ('corroborated','native','reconstructed','inferred')),
    confidence DOUBLE PRECISION NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    adapter_version TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    UNIQUE (source_instance_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS mcp_server_observations_component_time
    ON mcp_server_observations (server_component_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS mcp_primitive_observations_server_time
    ON mcp_primitive_observations (server_component_id, last_advertised_at DESC);
CREATE INDEX IF NOT EXISTS mcp_connection_assertions_server_time
    ON mcp_connection_assertions (server_component_id, observed_at, state);
CREATE INDEX IF NOT EXISTS mcp_call_assertions_server_tool_time
    ON mcp_call_assertions (server_component_id, tool_component_id, observed_at, logical_call_id);
