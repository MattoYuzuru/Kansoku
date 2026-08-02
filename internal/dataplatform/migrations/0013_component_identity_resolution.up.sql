-- Additive identity semantics for native/reconstructed component evidence.
-- Existing assertions remain byte-for-byte unchanged; nullable columns are
-- populated only for evidence observed after this migration.
ALTER TABLE component_assertions
    ADD COLUMN IF NOT EXISTS component_kind TEXT,
    ADD COLUMN IF NOT EXISTS qualified_identity TEXT,
    ADD COLUMN IF NOT EXISTS identity_source TEXT,
    ADD COLUMN IF NOT EXISTS owner_plugin_identity TEXT,
    ADD COLUMN IF NOT EXISTS invocation_mode TEXT,
    ADD COLUMN IF NOT EXISTS upstream_identity_hash TEXT,
    ADD COLUMN IF NOT EXISTS resolution_version BIGINT;

ALTER TABLE component_assertions
    DROP CONSTRAINT IF EXISTS component_assertions_assertion_kind_check;
ALTER TABLE component_assertions
    ADD CONSTRAINT component_assertions_assertion_kind_check
    CHECK (assertion_kind IN (
        'installed','enabled','exposed','requested','invoked','loaded',
        'child_activity','outcome'
    ));

DO $$
DECLARE constraint_row RECORD;
BEGIN
    FOR constraint_row IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'component_assertions'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%identity_resolution%'
    LOOP
        EXECUTE format(
            'ALTER TABLE component_assertions DROP CONSTRAINT %I',
            constraint_row.conname
        );
    END LOOP;
END $$;

ALTER TABLE component_assertions
    ADD CONSTRAINT component_assertions_identity_resolution_check
    CHECK (identity_resolution IN (
        'exact','unresolved','ambiguous','redacted'
    )),
    ADD CONSTRAINT component_assertions_resolution_consistency_check
    CHECK (
        (identity_resolution = 'exact' AND
         component_installation_id IS NOT NULL AND candidate_count = 1)
        OR
        (identity_resolution IN ('unresolved','redacted') AND
         component_installation_id IS NULL AND candidate_count = 0)
        OR
        (identity_resolution = 'ambiguous' AND
         component_installation_id IS NULL AND candidate_count > 1)
    );

ALTER TABLE component_assertions
    ADD CONSTRAINT component_assertions_component_kind_check
    CHECK (
        component_kind IS NULL OR component_kind IN (
            'skill','plugin','mcp','hook','command','app'
        )
    ),
    ADD CONSTRAINT component_assertions_invocation_mode_check
    CHECK (
        invocation_mode IS NULL OR invocation_mode IN (
            'explicit','proactive','nested','requested','not_observed'
        )
    ),
    ADD CONSTRAINT component_assertions_resolution_version_check
    CHECK (resolution_version IS NULL OR resolution_version > 0);

CREATE TABLE IF NOT EXISTS component_assertion_resolution_history (
    resolution_history_id TEXT PRIMARY KEY,
    assertion_id TEXT NOT NULL REFERENCES component_assertions(assertion_id),
    resolution_version BIGINT NOT NULL CHECK (resolution_version > 0),
    identity_resolution TEXT NOT NULL CHECK (
        identity_resolution IN ('exact','unresolved','ambiguous','redacted')
    ),
    component_installation_id TEXT
        REFERENCES component_installations(component_installation_id),
    candidate_count INTEGER NOT NULL CHECK (candidate_count >= 0),
    resolver_version TEXT NOT NULL,
    resolution_trigger TEXT NOT NULL CHECK (
        resolution_trigger IN (
            'initial_ingest','inventory_snapshot','manual_repair'
        )
    ),
    resolved_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (identity_resolution = 'exact' AND
         component_installation_id IS NOT NULL AND candidate_count = 1)
        OR
        (identity_resolution IN ('unresolved','redacted') AND
         component_installation_id IS NULL AND candidate_count = 0)
        OR
        (identity_resolution = 'ambiguous' AND
         component_installation_id IS NULL AND candidate_count > 1)
    ),
    UNIQUE (assertion_id, resolution_version)
);

CREATE INDEX IF NOT EXISTS component_assertions_kind_observed_idx
    ON component_assertions (component_kind, observed_at DESC);
CREATE INDEX IF NOT EXISTS component_assertions_qualified_identity_idx
    ON component_assertions (
        agent_installation_id, component_kind, qualified_identity
    )
    WHERE qualified_identity IS NOT NULL;
CREATE INDEX IF NOT EXISTS component_resolution_history_current_idx
    ON component_assertion_resolution_history (
        assertion_id, resolution_version DESC
    );

CREATE OR REPLACE VIEW component_assertion_current_resolution AS
SELECT
    ca.assertion_id,
    COALESCE(latest.identity_resolution, ca.identity_resolution)
        AS identity_resolution,
    COALESCE(
        latest.component_installation_id,
        ca.component_installation_id
    ) AS component_installation_id,
    COALESCE(latest.candidate_count, ca.candidate_count) AS candidate_count,
    latest.resolution_version,
    latest.resolver_version,
    latest.resolution_trigger,
    latest.resolved_at
FROM component_assertions ca
LEFT JOIN LATERAL (
    SELECT h.identity_resolution, h.component_installation_id,
           h.candidate_count, h.resolution_version, h.resolver_version,
           h.resolution_trigger, h.resolved_at
    FROM component_assertion_resolution_history h
    WHERE h.assertion_id = ca.assertion_id
    ORDER BY h.resolution_version DESC
    LIMIT 1
) latest ON TRUE;
