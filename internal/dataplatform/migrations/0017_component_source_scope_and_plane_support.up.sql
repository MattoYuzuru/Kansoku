-- Component source-scope provenance, adapter-declared evidence plane support
-- and inventory coverage-gap accounting (ADR 0023).
--
-- All three are additive. Existing rows keep their current meaning: a NULL
-- source_scope_state is an assertion observed before this migration, an
-- installation with no agent_component_plane_support row is treated as
-- supported exactly as before, and coverage_gap_count defaults to zero, which
-- is the honest value for a snapshot taken by a scanner that could not yet
-- count gaps.

-- 1. Source scope carried on the assertion itself.
--
-- The raw value an agent reported (Claude Code's skill.source, Codex's
-- inventory scope) is recorded alongside its classification against the closed
-- adaptersdk.SourceScope vocabulary. A value outside that vocabulary no longer
-- narrows identity resolution; it is stored here with state 'unknown' and
-- raised as an idempotent info incident instead of silently resolving to zero
-- candidates.
ALTER TABLE component_assertions
    ADD COLUMN IF NOT EXISTS source_scope TEXT,
    ADD COLUMN IF NOT EXISTS source_scope_state TEXT;

ALTER TABLE component_assertions
    DROP CONSTRAINT IF EXISTS component_assertions_source_scope_state_check;
ALTER TABLE component_assertions
    ADD CONSTRAINT component_assertions_source_scope_state_check
    CHECK (
        source_scope_state IS NULL OR source_scope_state IN (
            'observed', 'unknown', 'not_observed'
        )
    );

CREATE INDEX IF NOT EXISTS component_assertions_source_scope_state_idx
    ON component_assertions (source_scope_state, observed_at DESC)
    WHERE source_scope_state = 'unknown';

-- 1b. An observed-but-unrecognized invocation trigger is 'unknown', which is
-- not 'not_observed'. The ingress previously dropped such a trigger silently,
-- so a future Claude Code trigger vocabulary addition would have been recorded
-- as "the agent reported no mode" -- a different claim entirely.
ALTER TABLE component_assertions
    DROP CONSTRAINT IF EXISTS component_assertions_mode_check;
ALTER TABLE component_assertions
    DROP CONSTRAINT IF EXISTS component_assertions_invocation_mode_check;
ALTER TABLE component_assertions
    ADD CONSTRAINT component_assertions_mode_check
    CHECK (mode IN ('explicit', 'proactive', 'nested', 'not_observed', 'unknown')),
    ADD CONSTRAINT component_assertions_invocation_mode_check
    CHECK (
        invocation_mode IS NULL OR invocation_mode IN (
            'explicit', 'proactive', 'nested', 'requested', 'not_observed', 'unknown'
        )
    );

-- 2. Adapter-declared evidence plane support, per installation.
--
-- Whether an agent has a surface that reports the model-visible ("exposed")
-- component set is a property of the agent, not of one observation window.
-- Codex reports it natively through the App Server skills/list response;
-- Claude Code documents no equivalent event or snapshot at all. Recording the
-- adapter's own manifest declaration here lets the data platform read the
-- distinction as data -- "there is no surface to look at" versus "we looked
-- and saw nothing" -- without any core query branching on an agent name.
CREATE TABLE IF NOT EXISTS agent_component_plane_support (
    agent_installation_id TEXT NOT NULL
        REFERENCES agent_installations(agent_installation_id) ON DELETE CASCADE,
    component_kind        TEXT NOT NULL CHECK (component_kind IN (
        'skill', 'plugin', 'mcp', 'hook', 'command', 'app'
    )),
    plane                 TEXT NOT NULL CHECK (plane IN ('exposed')),
    state                 TEXT NOT NULL CHECK (state IN (
        'native', 'reconstructed', 'unsupported'
    )),
    reason                TEXT NOT NULL,
    adapter_id            TEXT NOT NULL,
    adapter_version       TEXT NOT NULL,
    observed_at           TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (agent_installation_id, component_kind, plane)
);

CREATE INDEX IF NOT EXISTS agent_component_plane_support_lookup_idx
    ON agent_component_plane_support (component_kind, plane, agent_installation_id)
    INCLUDE (state);

-- 3. Inventory coverage gaps.
--
-- A scanner that skipped an entry it could not read (a dangling symlink, an
-- unreadable, truncated or unparseable manifest) previously recorded nothing,
-- so a mis-mounted host produced a confident, silently truncated inventory
-- marked 'complete'. The tally is carried on the snapshot and downgrades its
-- completeness, which is what keeps the cold-eligibility fallback honest: an
-- installation whose exposed plane is unsupported falls back on inventory
-- completeness, and must not be handed a confident number derived from a
-- truncated scan.
ALTER TABLE inventory_snapshots
    ADD COLUMN IF NOT EXISTS coverage_gap_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS coverage_gap_classes JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE inventory_snapshots
    DROP CONSTRAINT IF EXISTS inventory_snapshots_coverage_gap_count_check;
ALTER TABLE inventory_snapshots
    ADD CONSTRAINT inventory_snapshots_coverage_gap_count_check
    CHECK (coverage_gap_count >= 0);
