DELETE FROM cost_estimates
WHERE method IN ('public_api_token_rates', 'public_api_uncached_upper_bound');

DELETE FROM price_catalog_versions
WHERE price_catalog_version_id IN (
    'pcv_openai_gpt_5_6_sol_20260725',
    'pcv_openai_gpt_5_6_terra_20260725',
    'pcv_openai_gpt_5_6_luna_20260725'
);

ALTER TABLE cost_estimates DROP COLUMN method;
ALTER TABLE token_usage DROP COLUMN cached_input_tokens;

ALTER TABLE price_catalog_versions
    DROP COLUMN retrieved_at,
    DROP COLUMN source_url,
    DROP COLUMN pricing_basis,
    DROP COLUMN long_context_output_multiplier_millis,
    DROP COLUMN long_context_input_multiplier_millis,
    DROP COLUMN long_context_threshold_tokens,
    DROP COLUMN output_price_nanos_per_token,
    DROP COLUMN cached_input_price_nanos_per_token,
    DROP COLUMN input_price_nanos_per_token;

UPDATE price_catalog_versions SET input_price_micros = 0
WHERE input_price_micros IS NULL;
UPDATE price_catalog_versions SET output_price_micros = 0
WHERE output_price_micros IS NULL;
ALTER TABLE price_catalog_versions
    ALTER COLUMN input_price_micros SET NOT NULL,
    ALTER COLUMN output_price_micros SET NOT NULL;
