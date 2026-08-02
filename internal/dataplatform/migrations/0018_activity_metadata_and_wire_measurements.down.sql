ALTER TABLE events
    DROP COLUMN IF EXISTS message_id;

ALTER TABLE component_assertions
    DROP COLUMN IF EXISTS component_version,
    DROP COLUMN IF EXISTS component_scope,
    DROP COLUMN IF EXISTS marketplace;

DROP INDEX IF EXISTS hook_registrations_session_idx;
DROP TABLE IF EXISTS hook_registrations;

ALTER TABLE sessions
    DROP COLUMN IF EXISTS user_pseudonym,
    DROP COLUMN IF EXISTS safe_mode,
    DROP COLUMN IF EXISTS terminal_type,
    DROP COLUMN IF EXISTS query_source,
    DROP COLUMN IF EXISTS start_type;

ALTER TABLE model_operations
    DROP CONSTRAINT IF EXISTS model_operations_response_character_count_check;
ALTER TABLE model_operations
    DROP COLUMN IF EXISTS response_character_count;

ALTER TABLE token_usage
    DROP CONSTRAINT IF EXISTS token_usage_cache_tokens_check;
ALTER TABLE token_usage
    DROP COLUMN IF EXISTS cache_read_tokens,
    DROP COLUMN IF EXISTS cache_creation_tokens;

ALTER TABLE tool_calls
    DROP CONSTRAINT IF EXISTS tool_calls_byte_counts_check;
ALTER TABLE tool_calls
    DROP COLUMN IF EXISTS result_bytes,
    DROP COLUMN IF EXISTS input_bytes,
    DROP COLUMN IF EXISTS tool_source,
    DROP COLUMN IF EXISTS tool_use_pseudonym;

DROP INDEX IF EXISTS tool_decisions_use_idx;
DROP INDEX IF EXISTS tool_decisions_session_idx;
DROP TABLE IF EXISTS tool_decisions;
