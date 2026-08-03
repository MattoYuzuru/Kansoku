-- The metric lane.
--
-- Claude Code has always exported a metric stream next to its log stream, and
-- Kansoku has always thrown it away: the receiver read only a data point's
-- attributes, and because a metric point carries no event.name, the point was
-- routed to the event lane, failed event canonicalization and landed in
-- quarantine. Four documented measurements were lost that way -- accumulated
-- cost, token counts split by kind, sessions started, and accumulated active
-- time, which is the only "time actually spent" signal any agent reports.
--
-- These samples are stored in their own table on purpose. They are NOT merged
-- into token_usage or model_operations: api_request remains the single counted
-- operation surface, and the metric stream is an independent second
-- observation of the same spend. Keeping them apart is what makes the two
-- comparable -- a disagreement is a number rather than a silence.
CREATE TABLE IF NOT EXISTS metric_samples (
    metric_sample_id      TEXT NOT NULL,
    observed_at           TIMESTAMPTZ NOT NULL,
    ingested_at           TIMESTAMPTZ NOT NULL,

    metric_name           TEXT NOT NULL,
    unit                  TEXT,
    -- An integer counter and a floating-point gauge are different
    -- measurements. Storing both in one numeric column would either lose
    -- exactness on large token counts or fabricate precision on a cost.
    value_int             BIGINT,
    value_double          DOUBLE PRECISION,

    adapter_id            TEXT NOT NULL,
    adapter_version       TEXT NOT NULL,
    source_kind           TEXT NOT NULL,
    schema_fingerprint    TEXT NOT NULL,
    agent_installation_id TEXT,

    -- Allowlisted dimensions, carrying the same prefixed pseudonyms the event
    -- lane derives so a sample joins to sessions without a second identity
    -- scheme. There is deliberately no foreign key: a metric export can reach
    -- the appliance before any log record created the session row, and a
    -- constraint would turn "arrived early" into "dropped".
    session_id            TEXT,
    user_pseudonym        TEXT,
    model_id              TEXT,
    terminal_type         TEXT,
    query_source          TEXT,
    start_type            TEXT,
    -- dimension splits a metric into the kinds the agent reported; for
    -- claude_code.token.usage the observed values are input, output,
    -- cacheCreation and cacheRead.
    dimension             TEXT,

    idempotency_key       TEXT NOT NULL,
    PRIMARY KEY (metric_sample_id, observed_at),
    CONSTRAINT metric_samples_value_present CHECK (
        value_int IS NOT NULL OR value_double IS NOT NULL
    )
) PARTITION BY RANGE (observed_at);

-- Re-exporting the same point must not add a second row.
CREATE UNIQUE INDEX IF NOT EXISTS metric_samples_idempotency
    ON metric_samples (idempotency_key, observed_at);
CREATE INDEX IF NOT EXISTS metric_samples_name_idx
    ON metric_samples (metric_name, observed_at DESC);
CREATE INDEX IF NOT EXISTS metric_samples_session_idx
    ON metric_samples (session_id, observed_at DESC)
    WHERE session_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS metric_samples_model_idx
    ON metric_samples (model_id, observed_at DESC)
    WHERE model_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS metric_samples_observed_at_brin
    ON metric_samples USING brin (observed_at);
