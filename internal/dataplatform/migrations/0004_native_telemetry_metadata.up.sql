ALTER TABLE prompt_features
    ADD COLUMN prompt_character_count BIGINT
    CHECK (prompt_character_count IS NULL OR prompt_character_count >= 0);

ALTER TABLE model_operations
    ADD COLUMN provider_cost_micros BIGINT
    CHECK (provider_cost_micros IS NULL OR provider_cost_micros >= 0);
