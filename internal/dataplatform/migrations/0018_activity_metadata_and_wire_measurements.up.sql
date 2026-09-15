-- Storage for the non-content wire metadata Claude Code and Codex already
-- emit and Kansoku previously dropped on the floor.
--
-- Every column here is additive and nullable, so an event recorded before this
-- migration keeps its exact meaning: NULL is "the agent did not report it or
-- we were not yet reading it", never a fabricated zero. Content surfaces
-- (prompt, response, tool input/parameters, raw API bodies) remain
-- unconditionally dropped at the sanitizer and have no representation here.

-- 1. Tool decisions get their own lane.
--
-- A permission decision is not an execution: tool_calls stays the single
-- counted execution surface (fed only by tool_result), and decisions live in
-- their own table. That separation is what lets a denied call -- which never
-- produces an execution at all -- be counted for the first time without
-- inflating any existing tool-call number by a single row.
CREATE TABLE IF NOT EXISTS tool_decisions (
    tool_decision_id      TEXT NOT NULL,
    observed_at           TIMESTAMPTZ NOT NULL,
    event_id              TEXT,
    session_id            TEXT,
    turn_id               TEXT,
    component_id          TEXT,
    decision              TEXT NOT NULL,
    decision_state        TEXT NOT NULL CHECK (decision_state IN (
        'observed', 'unknown', 'not_observed'
    )),
    -- Two independent provenances the agent reports side by side.
    -- decision_source is where the permission answer came from ("config");
    -- tool_source is where the tool came from ("builtin"). Folding them into
    -- one column would make every decision look configuration-free.
    decision_source       TEXT,
    tool_source           TEXT,
    tool_use_pseudonym    TEXT,
    agent_installation_id TEXT,
    installation_attribution_state TEXT NOT NULL DEFAULT 'not_observed',
    PRIMARY KEY (tool_decision_id, observed_at)
) PARTITION BY RANGE (observed_at);

CREATE INDEX IF NOT EXISTS tool_decisions_session_idx
    ON tool_decisions (session_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS tool_decisions_turn_idx
    ON tool_decisions (turn_id, observed_at DESC)
    WHERE turn_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS tool_decisions_use_idx
    ON tool_decisions (tool_use_pseudonym)
    WHERE tool_use_pseudonym IS NOT NULL;

-- 2. Execution-side detail the agent already measured.
--
-- tool_use_pseudonym is the join back to the decision that authorized this
-- execution; the sizes are numbers the agent computed about payloads Kansoku
-- still never reads.
ALTER TABLE tool_calls
    ADD COLUMN IF NOT EXISTS tool_use_pseudonym TEXT,
    ADD COLUMN IF NOT EXISTS tool_source TEXT,
    ADD COLUMN IF NOT EXISTS input_bytes BIGINT,
    ADD COLUMN IF NOT EXISTS result_bytes BIGINT;

ALTER TABLE tool_calls
    DROP CONSTRAINT IF EXISTS tool_calls_byte_counts_check;
ALTER TABLE tool_calls
    ADD CONSTRAINT tool_calls_byte_counts_check
    CHECK (
        (input_bytes IS NULL OR input_bytes >= 0) AND
        (result_bytes IS NULL OR result_bytes >= 0)
    );

-- 3. The two cache measurements are two numbers, not one.
--
-- cached_input_tokens keeps its existing meaning. Cache creation and cache
-- read are reported separately by the agent and are stored separately here;
-- summing them into the existing column would have destroyed the distinction
-- that makes cache behaviour analysable at all.
ALTER TABLE token_usage
    ADD COLUMN IF NOT EXISTS cache_creation_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS cache_read_tokens BIGINT;

ALTER TABLE token_usage
    DROP CONSTRAINT IF EXISTS token_usage_cache_tokens_check;
ALTER TABLE token_usage
    ADD CONSTRAINT token_usage_cache_tokens_check
    CHECK (
        (cache_creation_tokens IS NULL OR cache_creation_tokens >= 0) AND
        (cache_read_tokens IS NULL OR cache_read_tokens >= 0)
    );

-- 4. Response length is a length, never the response -- and it belongs to
--    the turn, not to an API call.
--
-- The agent reports response_length on assistant_response, which carries
-- prompt.id, model and query_source but is not an api_request: one assistant
-- turn can be served by several API calls. Hanging the measurement off
-- model_operations would have required either inventing a model operation for
-- a non-request event (inflating every request count) or leaving the column
-- permanently NULL. The turn is the thing that was actually measured.
ALTER TABLE turns
    ADD COLUMN IF NOT EXISTS response_character_count BIGINT,
    ADD COLUMN IF NOT EXISTS model_id TEXT,
    ADD COLUMN IF NOT EXISTS query_source TEXT;

ALTER TABLE turns
    DROP CONSTRAINT IF EXISTS turns_response_character_count_check;
ALTER TABLE turns
    ADD CONSTRAINT turns_response_character_count_check
    CHECK (response_character_count IS NULL OR response_character_count >= 0);

-- 5. Session context.
--
-- How a session was started, what asked for it, and which terminal it ran in
-- are session properties the agent reports on its events; until now the
-- sessions table held only an id, a project and a timestamp.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS start_type TEXT,
    ADD COLUMN IF NOT EXISTS query_source TEXT,
    ADD COLUMN IF NOT EXISTS terminal_type TEXT,
    ADD COLUMN IF NOT EXISTS safe_mode TEXT,
    ADD COLUMN IF NOT EXISTS user_pseudonym TEXT;

-- 6. Hook registrations.
--
-- Claude Code announces every hook it registered at session start. The
-- announcement carried hook_event/hook_type/hook_source, all of which were
-- discarded. hook_matcher is deliberately absent: it is user-authored and can
-- embed a path or project name, so it is excluded at the sanitizer rather than
-- pseudonymized here.
CREATE TABLE IF NOT EXISTS hook_registrations (
    hook_registration_id  TEXT NOT NULL,
    observed_at           TIMESTAMPTZ NOT NULL,
    event_id              TEXT,
    session_id            TEXT,
    hook_event            TEXT,
    hook_type             TEXT,
    hook_source           TEXT,
    agent_installation_id TEXT,
    installation_attribution_state TEXT NOT NULL DEFAULT 'not_observed',
    PRIMARY KEY (hook_registration_id, observed_at)
) PARTITION BY RANGE (observed_at);

CREATE INDEX IF NOT EXISTS hook_registrations_session_idx
    ON hook_registrations (session_id, observed_at DESC);

-- 7. Component provenance the resolver used to approximate.
--
-- marketplace arrived on every plugin and skill event and was dropped, while
-- the resolver approximated it by splitting an owner's declared name on '@'.
-- plugin.scope is deliberately absent: it already lands on the existing
-- source_scope column added by 0017. The wire carries the value once, so a
-- second column would only duplicate it under a different name.
ALTER TABLE component_assertions
    ADD COLUMN IF NOT EXISTS marketplace TEXT,
    ADD COLUMN IF NOT EXISTS component_version TEXT;

-- 8. Message-level correlation.
--
-- A pseudonym, derived with the same device-scoped HMAC as session and turn
-- handles. The agent's raw message uuid never becomes durable.
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS message_id TEXT;
