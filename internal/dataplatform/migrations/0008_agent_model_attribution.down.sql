DROP INDEX IF EXISTS source_installation_agent_idx;
DROP INDEX IF EXISTS session_installation_agent_observed_idx;
DROP INDEX IF EXISTS tool_calls_installation_observed_idx;
DROP INDEX IF EXISTS model_operations_installation_observed_idx;

ALTER TABLE tool_calls
    DROP COLUMN IF EXISTS installation_attribution_state,
    DROP COLUMN IF EXISTS agent_installation_id;
ALTER TABLE model_operations
    DROP COLUMN IF EXISTS installation_attribution_state,
    DROP COLUMN IF EXISTS agent_installation_id;

DROP TABLE IF EXISTS session_installation_attributions;
DROP TABLE IF EXISTS source_installation_attributions;
DROP TABLE IF EXISTS agent_installation_profiles;
