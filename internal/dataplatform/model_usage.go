package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionModelUsage1 is the registered formula version for the model
// usage + cost time-series query.
const FormulaVersionModelUsage1 = "model_usage/2"

// ModelUsage executes the "model_usage_range" budgeted query: one row per
// calendar day inside the half-open [from, to) range with model request
// count, provider-reported token sum and estimated cost (all backed
// directly by model_operations/token_usage and provider-reported cost, with
// cost_estimates as a fallback), plus latency percentiles and error ratio
// derived from native request/response observations. Serves the /models
// "model-usage" and "model-cost" panels: the
// time-series companion to ModelBreakdown's per-model leaderboard.
//
// Response rows are the request-volume/token denominator. Separate request
// rows contribute latency without double-counting that volume; Claude's
// combined api_request response row can carry both. When no operation has a
// duration or terminal outcome, Percentiles/ErrorRatio remain nil -- an
// honest "not observed" rather than a fabricated zero.
func ModelUsage(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (ModelUsageResponse, error) {
	budget := Budgets["model_usage_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return ModelUsageResponse{}, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		WITH ops AS (
			SELECT mo.model_operation_id, mo.observed_at, mo.provider_cost_micros,
				date_trunc('day', mo.observed_at) AS day
			FROM model_operations mo
			WHERE mo.observed_at >= $1 AND mo.observed_at < $2
			  AND mo.operation_kind = 'response'
		),
		token_totals AS (
			SELECT o.model_operation_id,
				sum(tu.input_tokens + tu.output_tokens) AS total_tokens
			FROM ops o
			JOIN token_usage tu ON tu.model_operation_id = o.model_operation_id AND tu.observed_at = o.observed_at
			GROUP BY o.model_operation_id
		),
		cost_totals AS (
			SELECT o.model_operation_id,
				coalesce(o.provider_cost_micros, max(ce.cost_micros), 0) AS total_cost_micros,
				(o.provider_cost_micros IS NOT NULL OR count(ce.cost_estimate_id) > 0) AS is_costed,
				(o.provider_cost_micros IS NOT NULL) AS is_provider_cost,
				bool_or(ce.method = 'public_api_uncached_upper_bound') AS is_upper_bound
			FROM ops o
			LEFT JOIN token_usage tu ON tu.model_operation_id = o.model_operation_id AND tu.observed_at = o.observed_at
			LEFT JOIN LATERAL (
				SELECT ce.cost_estimate_id, ce.cost_micros, ce.method
				FROM cost_estimates ce
				JOIN price_catalog_versions pcv
				  ON pcv.price_catalog_version_id = ce.price_catalog_version_id
				WHERE ce.token_usage_id = tu.token_usage_id
				ORDER BY pcv.effective_at DESC
				LIMIT 1
			) ce ON TRUE
			GROUP BY o.model_operation_id, o.provider_cost_micros
		),
		observations AS (
			SELECT date_trunc('day', mo.observed_at) AS day,
				mo.duration_ms, mo.outcome
			FROM model_operations mo
			WHERE mo.observed_at >= $1 AND mo.observed_at < $2
		)
		SELECT o.day,
			count(DISTINCT o.model_operation_id) AS request_count,
			coalesce(sum(tt.total_tokens), 0) AS total_tokens,
			coalesce(sum(ct.total_cost_micros), 0) AS total_cost_micros,
			count(*) FILTER (WHERE ct.is_costed) AS costed_request_count,
			count(*) FILTER (WHERE ct.is_provider_cost) AS provider_cost_count,
			count(*) FILTER (WHERE ct.is_upper_bound) AS upper_bound_cost_count,
			coalesce(max(obs.observation_count), 0) AS matched_event_count,
			coalesce(max(obs.success_count), 0) AS success_count,
			coalesce(max(obs.failure_count), 0) AS failure_count,
			max(obs.p50) AS p50,
			max(obs.p90) AS p90,
			max(obs.p95) AS p95,
			max(obs.p99) AS p99
		FROM ops o
		LEFT JOIN token_totals tt ON tt.model_operation_id = o.model_operation_id
		LEFT JOIN cost_totals ct ON ct.model_operation_id = o.model_operation_id
		LEFT JOIN (
			SELECT day,
				count(*) FILTER (WHERE duration_ms IS NOT NULL) AS observation_count,
				count(*) FILTER (WHERE outcome = 'succeeded') AS success_count,
				count(*) FILTER (WHERE outcome IN ('failed', 'timed_out', 'abandoned')) AS failure_count,
				percentile_cont(0.50) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL) AS p50,
				percentile_cont(0.90) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL) AS p90,
				percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL) AS p95,
				percentile_cont(0.99) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL) AS p99
			FROM observations
			GROUP BY day
		) obs ON obs.day = o.day
		GROUP BY o.day
		ORDER BY o.day
	`, from, to)
	if err != nil {
		return ModelUsageResponse{}, budgetOrErr(budget, started, err)
	}
	var response ModelUsageResponse
	var totalRequests int64
	for rows.Next() {
		var row ModelUsageDayRow
		var successCount, failureCount int64
		var p Percentiles
		if err := rows.Scan(&row.Day, &row.RequestCount, &row.TotalTokens, &row.EstimatedCostMicros,
			&row.CostedRequestCount, &row.ProviderCostCount, &row.UpperBoundCostCount,
			&row.MatchedEventCount, &successCount, &failureCount, &p.P50, &p.P90, &p.P95, &p.P99); err != nil {
			rows.Close()
			return ModelUsageResponse{}, err
		}
		if p.P50 != nil || p.P90 != nil || p.P95 != nil || p.P99 != nil {
			row.Percentiles = &p
		}
		if terminal := successCount + failureCount; terminal > 0 {
			ratio := float64(successCount) / float64(terminal)
			// Error ratio is failed/terminal, not succeeded/terminal.
			ratio = 1 - ratio
			row.ErrorRatio = &ratio
		}
		response.Data = append(response.Data, row)
		totalRequests += row.RequestCount
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ModelUsageResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return ModelUsageResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}

	response.FormulaVersion = FormulaVersionModelUsage1
	// Population reports request-volume presence (mirrors ModelBreakdown's
	// precedent): numerator/denominator both equal total requests, since
	// there is no independent "expected" request baseline. The
	// matched-event fraction (how much of that volume also has a real
	// latency/error signal) is separately visible per-row via
	// MatchedEventCount/RequestCount, never blended into this top-level
	// completeness.
	response.Population = Population{Numerator: totalRequests, Denominator: totalRequests}
	response.Completeness = completenessFor(totalRequests, totalRequests)

	watermark, pending, err := aggregateSourceWatermarkFreshness(ctx, pool)
	if err != nil {
		return ModelUsageResponse{}, err
	}
	response.Freshness = Freshness{RollupWatermark: watermark, LateEventsPending: pending}
	return response, nil
}
