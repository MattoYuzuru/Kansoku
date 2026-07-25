package dataplatform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	publicAPIPriceBasis = "api_equivalent_public_list"
	priceRetrievedAt    = "2026-07-25T00:00:00Z"
)

type publicAPIPrice struct {
	VersionID                   string
	ModelID                     string
	SourceURL                   string
	InputNanosPerToken          int64
	CachedInputNanosPerToken    int64
	OutputNanosPerToken         int64
	LongContextThreshold        int64
	LongContextInputMultiplier  int64
	LongContextOutputMultiplier int64
}

var publicAPIPrices = map[string]publicAPIPrice{
	"gpt-5.6-sol": {
		VersionID: "pcv_openai_gpt_5_6_sol_20260725", ModelID: "gpt-5.6-sol",
		SourceURL:          "https://developers.openai.com/api/docs/models/gpt-5.6-sol",
		InputNanosPerToken: 5000, CachedInputNanosPerToken: 500, OutputNanosPerToken: 30000,
		LongContextThreshold: 272000, LongContextInputMultiplier: 2000, LongContextOutputMultiplier: 1500,
	},
	"gpt-5.6-terra": {
		VersionID: "pcv_openai_gpt_5_6_terra_20260725", ModelID: "gpt-5.6-terra",
		SourceURL:          "https://developers.openai.com/api/docs/models/gpt-5.6-terra",
		InputNanosPerToken: 2500, CachedInputNanosPerToken: 250, OutputNanosPerToken: 15000,
		LongContextThreshold: 272000, LongContextInputMultiplier: 2000, LongContextOutputMultiplier: 1500,
	},
	"gpt-5.6-luna": {
		VersionID: "pcv_openai_gpt_5_6_luna_20260725", ModelID: "gpt-5.6-luna",
		SourceURL:          "https://developers.openai.com/api/docs/models/gpt-5.6-luna",
		InputNanosPerToken: 1000, CachedInputNanosPerToken: 100, OutputNanosPerToken: 6000,
		LongContextThreshold: 272000, LongContextInputMultiplier: 2000, LongContextOutputMultiplier: 1500,
	},
}

// ensurePublicAPIPrice inserts the versioned public API price that applies to
// a known model. Unknown models remain unpriced rather than being guessed.
func ensurePublicAPIPrice(ctx context.Context, pool *pgxpool.Pool, modelID string) (*publicAPIPrice, error) {
	price, ok := publicAPIPrices[modelID]
	if !ok {
		return nil, nil
	}
	retrievedAt, err := time.Parse(time.RFC3339, priceRetrievedAt)
	if err != nil {
		return nil, errors.New("invalid_embedded_price_retrieval_time")
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO price_catalog_versions (
			price_catalog_version_id, model_id, effective_at,
			input_price_micros, output_price_micros,
			input_price_nanos_per_token, cached_input_price_nanos_per_token,
			output_price_nanos_per_token, long_context_threshold_tokens,
			long_context_input_multiplier_millis,
			long_context_output_multiplier_millis, pricing_basis, source_url,
			retrieved_at
		) VALUES ($1,$2,$3,NULL,NULL,$4,$5,$6,$7,$8,$9,$10,$11,$3)
		ON CONFLICT (price_catalog_version_id) DO NOTHING
	`, price.VersionID, price.ModelID, retrievedAt,
		price.InputNanosPerToken, price.CachedInputNanosPerToken,
		price.OutputNanosPerToken, price.LongContextThreshold,
		price.LongContextInputMultiplier, price.LongContextOutputMultiplier,
		publicAPIPriceBasis, price.SourceURL)
	if err != nil {
		return nil, err
	}
	return &price, nil
}

// estimatePublicAPICost returns micro-dollars rounded to the nearest micro.
// When cachedInputTokens is absent, every input token is priced as uncached
// and the method explicitly identifies the result as an upper bound.
func estimatePublicAPICost(price publicAPIPrice, inputTokens, outputTokens int64, cachedInputTokens *int64) (int64, string) {
	cached := int64(0)
	method := "public_api_uncached_upper_bound"
	if cachedInputTokens != nil {
		cached = *cachedInputTokens
		if cached > inputTokens {
			cached = inputTokens
		}
		method = "public_api_token_rates"
	}
	uncached := inputTokens - cached
	inputMultiplier := int64(1000)
	outputMultiplier := int64(1000)
	if inputTokens > price.LongContextThreshold {
		inputMultiplier = price.LongContextInputMultiplier
		outputMultiplier = price.LongContextOutputMultiplier
	}
	inputNanos := (uncached*price.InputNanosPerToken + cached*price.CachedInputNanosPerToken) * inputMultiplier / 1000
	outputNanos := outputTokens * price.OutputNanosPerToken * outputMultiplier / 1000
	return (inputNanos + outputNanos + 500) / 1000, method
}

func persistPublicAPICostEstimate(
	ctx context.Context,
	pool *pgxpool.Pool,
	tokenUsageID string,
	price publicAPIPrice,
	inputTokens, outputTokens int64,
	cachedInputTokens *int64,
) error {
	costMicros, method := estimatePublicAPICost(price, inputTokens, outputTokens, cachedInputTokens)
	_, err := pool.Exec(ctx, `
		INSERT INTO cost_estimates (
			cost_estimate_id, token_usage_id, price_catalog_version_id,
			cost_micros, method
		) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (cost_estimate_id) DO NOTHING
	`, "ce_catalog_20260725_"+tokenUsageID, tokenUsageID, price.VersionID, costMicros, method)
	return err
}
