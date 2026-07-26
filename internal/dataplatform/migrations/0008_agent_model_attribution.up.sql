-- Session 13 agent/model attribution. Only fresh exact observations populate
-- these relations; historical rows remain not_observed unless a later
-- deterministic reconciliation proves them. No migration guesses one
-- installation for ambiguous history.

CREATE TABLE IF NOT EXISTS agent_installation_profiles (
    agent_installation_id TEXT PRIMARY KEY
        REFERENCES agent_installations(agent_installation_id) ON DELETE CASCADE,
    adapter_id            TEXT NOT NULL,
    provider_id           TEXT NOT NULL,
    display_name          TEXT NOT NULL,
    display_alias         TEXT,
    surface_kind          TEXT NOT NULL,
    observed_agent_version TEXT,
    adapter_version       TEXT,
    completeness          TEXT NOT NULL
        CHECK (completeness IN ('complete', 'partial', 'degraded', 'unknown')),
    source_provenance     TEXT NOT NULL,
    first_seen_at         TIMESTAMPTZ NOT NULL,
    last_seen_at          TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS source_installation_attributions (
    source_instance_id    TEXT PRIMARY KEY REFERENCES source_instances(source_instance_id),
    agent_installation_id TEXT NOT NULL
        REFERENCES agent_installations(agent_installation_id),
    attribution_state     TEXT NOT NULL
        CHECK (attribution_state IN ('exact', 'candidate', 'ambiguous', 'unmatched')),
    observed_at           TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS session_installation_attributions (
    session_id            TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
    agent_installation_id TEXT NOT NULL
        REFERENCES agent_installations(agent_installation_id),
    attribution_state     TEXT NOT NULL
        CHECK (attribution_state IN ('exact', 'candidate', 'ambiguous', 'unmatched')),
    first_observed_at     TIMESTAMPTZ NOT NULL,
    last_observed_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (session_id, agent_installation_id)
);

ALTER TABLE model_operations
    ADD COLUMN IF NOT EXISTS agent_installation_id TEXT
        REFERENCES agent_installations(agent_installation_id),
    ADD COLUMN IF NOT EXISTS installation_attribution_state TEXT NOT NULL DEFAULT 'not_observed'
        CHECK (installation_attribution_state IN (
            'exact', 'candidate', 'ambiguous', 'unmatched', 'not_observed'
        ));

ALTER TABLE tool_calls
    ADD COLUMN IF NOT EXISTS agent_installation_id TEXT
        REFERENCES agent_installations(agent_installation_id),
    ADD COLUMN IF NOT EXISTS installation_attribution_state TEXT NOT NULL DEFAULT 'not_observed'
        CHECK (installation_attribution_state IN (
            'exact', 'candidate', 'ambiguous', 'unmatched', 'not_observed'
        ));

CREATE INDEX IF NOT EXISTS model_operations_installation_observed_idx
    ON model_operations (agent_installation_id, observed_at, model_id)
    WHERE installation_attribution_state = 'exact';
CREATE INDEX IF NOT EXISTS tool_calls_installation_observed_idx
    ON tool_calls (agent_installation_id, observed_at)
    WHERE installation_attribution_state = 'exact';
CREATE INDEX IF NOT EXISTS session_installation_agent_observed_idx
    ON session_installation_attributions
       (agent_installation_id, last_observed_at DESC, session_id);
CREATE INDEX IF NOT EXISTS source_installation_agent_idx
    ON source_installation_attributions (agent_installation_id, source_instance_id);
