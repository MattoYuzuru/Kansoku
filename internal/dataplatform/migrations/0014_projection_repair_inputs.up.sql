-- Preserve the closed normalized Event/Evidence metadata only while a
-- derived projection remains pending.  The JSON shape has no raw-content
-- fields and is deleted with the receipt after a successful projection.
--
-- Existing receipts are deliberately not rewritten: NULL input identifies
-- legacy receipts whose original metadata cannot be reconstructed safely.
ALTER TABLE observability_projection_receipts
    ADD COLUMN IF NOT EXISTS projection_input_schema TEXT,
    ADD COLUMN IF NOT EXISTS projection_input JSONB;

ALTER TABLE observability_projection_receipts
    DROP CONSTRAINT IF EXISTS observability_projection_receipts_projection_input_check;

ALTER TABLE observability_projection_receipts
    ADD CONSTRAINT observability_projection_receipts_projection_input_check
    CHECK (
        (
            projection_input_schema IS NULL
            AND projection_input IS NULL
        )
        OR
        (
            projection_input_schema = 'kansoku.projection-input/1'
            AND jsonb_typeof(projection_input) = 'object'
            AND octet_length(projection_input::text) <= 32768
        )
    );

-- One source assertion owns one retry receipt.  Multiple independent
-- evidence rows may point at the same canonical event and must not overwrite
-- each other's pending projection input.
ALTER TABLE observability_projection_receipts
    DROP CONSTRAINT IF EXISTS observability_projection_receipts_pkey;

ALTER TABLE observability_projection_receipts
    ADD CONSTRAINT observability_projection_receipts_pkey
    PRIMARY KEY (evidence_id, observed_at);

CREATE INDEX IF NOT EXISTS observability_projection_receipts_event_idx
    ON observability_projection_receipts (event_id, observed_at);
