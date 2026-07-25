-- The original *_price_micros columns cannot represent fractional
-- micro-dollars per token (for example $2.50 / 1M tokens). Keep them
-- nullable for backward compatibility and use integer nano-dollars per token
-- for exact catalog arithmetic.
ALTER TABLE price_catalog_versions
    ALTER COLUMN input_price_micros DROP NOT NULL,
    ALTER COLUMN output_price_micros DROP NOT NULL,
    ADD COLUMN input_price_nanos_per_token BIGINT
        CHECK (input_price_nanos_per_token IS NULL OR input_price_nanos_per_token >= 0),
    ADD COLUMN cached_input_price_nanos_per_token BIGINT
        CHECK (cached_input_price_nanos_per_token IS NULL OR cached_input_price_nanos_per_token >= 0),
    ADD COLUMN output_price_nanos_per_token BIGINT
        CHECK (output_price_nanos_per_token IS NULL OR output_price_nanos_per_token >= 0),
    ADD COLUMN long_context_threshold_tokens BIGINT
        CHECK (long_context_threshold_tokens IS NULL OR long_context_threshold_tokens >= 0),
    ADD COLUMN long_context_input_multiplier_millis BIGINT
        CHECK (long_context_input_multiplier_millis IS NULL OR long_context_input_multiplier_millis >= 0),
    ADD COLUMN long_context_output_multiplier_millis BIGINT
        CHECK (long_context_output_multiplier_millis IS NULL OR long_context_output_multiplier_millis >= 0),
    ADD COLUMN pricing_basis TEXT,
    ADD COLUMN source_url TEXT,
    ADD COLUMN retrieved_at TIMESTAMPTZ;

ALTER TABLE token_usage
    ADD COLUMN cached_input_tokens BIGINT
        CHECK (cached_input_tokens IS NULL OR cached_input_tokens >= 0);

ALTER TABLE cost_estimates
    ADD COLUMN method TEXT NOT NULL DEFAULT 'legacy_catalog'
        CHECK (method IN (
            'legacy_catalog',
            'public_api_token_rates',
            'public_api_uncached_upper_bound'
        ));

-- Seed only dimensions already observed by this appliance. The same
-- idempotent catalog rows are inserted for newly observed models by the
-- projection handoff.
INSERT INTO price_catalog_versions (
    price_catalog_version_id, model_id, effective_at,
    input_price_micros, output_price_micros,
    input_price_nanos_per_token, cached_input_price_nanos_per_token,
    output_price_nanos_per_token, long_context_threshold_tokens,
    long_context_input_multiplier_millis,
    long_context_output_multiplier_millis, pricing_basis, source_url,
    retrieved_at
)
SELECT 'pcv_openai_gpt_5_6_sol_20260725', model_id, '2026-07-25 00:00:00+00',
       NULL, NULL, 5000, 500, 30000, 272000, 2000, 1500,
       'api_equivalent_public_list',
       'https://developers.openai.com/api/docs/models/gpt-5.6-sol',
       '2026-07-25 00:00:00+00'
FROM models WHERE model_id = 'gpt-5.6-sol'
ON CONFLICT (price_catalog_version_id) DO NOTHING;

INSERT INTO price_catalog_versions (
    price_catalog_version_id, model_id, effective_at,
    input_price_micros, output_price_micros,
    input_price_nanos_per_token, cached_input_price_nanos_per_token,
    output_price_nanos_per_token, long_context_threshold_tokens,
    long_context_input_multiplier_millis,
    long_context_output_multiplier_millis, pricing_basis, source_url,
    retrieved_at
)
SELECT 'pcv_openai_gpt_5_6_terra_20260725', model_id, '2026-07-25 00:00:00+00',
       NULL, NULL, 2500, 250, 15000, 272000, 2000, 1500,
       'api_equivalent_public_list',
       'https://developers.openai.com/api/docs/models/gpt-5.6-terra',
       '2026-07-25 00:00:00+00'
FROM models WHERE model_id = 'gpt-5.6-terra'
ON CONFLICT (price_catalog_version_id) DO NOTHING;

INSERT INTO price_catalog_versions (
    price_catalog_version_id, model_id, effective_at,
    input_price_micros, output_price_micros,
    input_price_nanos_per_token, cached_input_price_nanos_per_token,
    output_price_nanos_per_token, long_context_threshold_tokens,
    long_context_input_multiplier_millis,
    long_context_output_multiplier_millis, pricing_basis, source_url,
    retrieved_at
)
SELECT 'pcv_openai_gpt_5_6_luna_20260725', model_id, '2026-07-25 00:00:00+00',
       NULL, NULL, 1000, 100, 6000, 272000, 2000, 1500,
       'api_equivalent_public_list',
       'https://developers.openai.com/api/docs/models/gpt-5.6-luna',
       '2026-07-25 00:00:00+00'
FROM models WHERE model_id = 'gpt-5.6-luna'
ON CONFLICT (price_catalog_version_id) DO NOTHING;

-- Historical token facts are not rewritten. This creates an idempotent,
-- explicitly upper-bound derived estimate because older facts did not retain
-- the cached-token subset.
INSERT INTO cost_estimates (
    cost_estimate_id, token_usage_id, price_catalog_version_id,
    cost_micros, method
)
SELECT
    'ce_catalog_20260725_' || tu.token_usage_id,
    tu.token_usage_id,
    pcv.price_catalog_version_id,
    round((
        tu.input_tokens * pcv.input_price_nanos_per_token *
            CASE WHEN tu.input_tokens > pcv.long_context_threshold_tokens
                 THEN pcv.long_context_input_multiplier_millis ELSE 1000 END / 1000.0
        + tu.output_tokens * pcv.output_price_nanos_per_token *
            CASE WHEN tu.input_tokens > pcv.long_context_threshold_tokens
                 THEN pcv.long_context_output_multiplier_millis ELSE 1000 END / 1000.0
    ) / 1000.0)::BIGINT,
    'public_api_uncached_upper_bound'
FROM token_usage tu
JOIN model_operations mo
  ON mo.model_operation_id = tu.model_operation_id
 AND mo.observed_at = tu.observed_at
JOIN price_catalog_versions pcv
  ON pcv.model_id = mo.model_id
 AND pcv.effective_at <= mo.observed_at
WHERE pcv.pricing_basis = 'api_equivalent_public_list'
  AND NOT EXISTS (
      SELECT 1 FROM cost_estimates ce
      WHERE ce.token_usage_id = tu.token_usage_id
  );
