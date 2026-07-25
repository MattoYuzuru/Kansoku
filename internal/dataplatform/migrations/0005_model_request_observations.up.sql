-- Preserve the request/response phase of native model telemetry. Codex emits
-- request latency on codex.api_request and token usage on a separate
-- response.completed SSE record, so one undifferentiated row cannot represent
-- both signals without either losing latency or double-counting requests.
ALTER TABLE model_operations
    ADD COLUMN operation_kind TEXT NOT NULL DEFAULT 'response'
        CHECK (operation_kind IN ('request', 'response')),
    ADD COLUMN duration_ms BIGINT
        CHECK (duration_ms IS NULL OR duration_ms >= 0),
    ADD COLUMN outcome TEXT
        CHECK (outcome IS NULL OR outcome IN (
            'succeeded', 'failed', 'cancelled', 'interrupted',
            'timed_out', 'abandoned', 'unknown'
        ));

CREATE INDEX IF NOT EXISTS model_operations_kind_observed_idx
    ON model_operations USING btree (operation_kind, observed_at);
