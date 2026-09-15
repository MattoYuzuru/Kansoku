-- A canonical event/evidence transaction owns one bounded projection receipt.
-- The receipt is removed only after all derived projections succeed. It
-- contains identifiers and error classes only, never a raw payload.
CREATE TABLE IF NOT EXISTS observability_projection_receipts (
    event_id TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    evidence_id TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'retryable', 'permanent_error')),
    attempt_count BIGINT NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error_class TEXT,
    first_enqueued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_attempted_at TIMESTAMPTZ,
    PRIMARY KEY (event_id, observed_at),
    CHECK (
        last_error_class IS NULL OR
        last_error_class ~ '^[a-z0-9_]{1,64}$'
    )
);

CREATE INDEX IF NOT EXISTS observability_projection_receipts_pending_idx
    ON observability_projection_receipts (state, first_enqueued_at);
