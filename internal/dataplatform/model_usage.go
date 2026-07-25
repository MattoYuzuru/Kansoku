package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionModelUsage1 is the registered formula version for the model
// usage + cost time-series query.
const FormulaVersionModelUsage1 = "model_usage/1"

// ModelUsage executes the "model_usage_range" budgeted query: one row per
// calendar day inside the half-open [from, to) range with model request
// count, provider-reported token sum and estimated cost (all backed
// directly by model_operations/token_usage/cost_estimates), plus a latency
// percentile and error ratio derived from an optional match to the events
// table. Serves the /models "model-usage" and "model-cost" panels: the
// time-series companion to ModelBreakdown's per-model leaderboard.
//
// model_operations has no duration_ms/outcome column of its own (see
// ModelBreakdown's doc comment in entity_breakdown.go). Unlike
// ModelBreakdown, this query does attempt a LEFT JOIN to events via the
// nullable model_operations.event_id/observed_at pair, because
// model.request_latency_seconds/model.error_ratio are "must" metrics the
// dashboard needs and events genuinely carries duration_ms/outcome for any
// operation an adapter chose to correlate. When event_id is null or does
// not match any events row (MatchedEventCount == 0 for that day),
// Percentiles/ErrorRatio are left nil -- an honest "not observable" rather
// than a fabricated zero latency or error rate.
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
			SELECT mo.model_operation_id, mo.observed_at, mo.event_id,
				date_trunc('day', mo.observed_at) AS day
			FROM model_operations mo
			WHERE mo.observed_at >= $1 AND mo.observed_at < $2
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
				sum(ce.cost_micros) AS total_cost_micros
			FROM ops o
			JOIN token_usage tu ON tu.model_operation_id = o.model_operation_id AND tu.observed_at = o.observed_at
			JOIN cost_estimates ce ON ce.token_usage_id = tu.token_usage_id
			GROUP BY o.model_operation_id
		),
		matched AS (
			SELECT o.model_operation_id, o.day, e.event_id, e.duration_ms, e.outcome
			FROM ops o
			LEFT JOIN events e ON e.event_id = o.event_id AND e.observed_at = o.observed_at
		)
		SELECT o.day,
			count(DISTINCT o.model_operation_id) AS request_count,
			coalesce(sum(tt.total_tokens), 0) AS total_tokens,
			coalesce(sum(ct.total_cost_micros), 0) AS total_cost_micros,
			count(*) FILTER (WHERE m.event_id IS NOT NULL) AS matched_event_count,
			count(*) FILTER (WHERE m.outcome = 'succeeded') AS success_count,
			count(*) FILTER (WHERE m.outcome IN ('failed', 'timed_out', 'abandoned')) AS failure_count,
			percentile_cont(0.50) WITHIN GROUP (ORDER BY m.duration_ms) FILTER (WHERE m.duration_ms IS NOT NULL) AS p50,
			percentile_cont(0.90) WITHIN GROUP (ORDER BY m.duration_ms) FILTER (WHERE m.duration_ms IS NOT NULL) AS p90,
			percentile_cont(0.95) WITHIN GROUP (ORDER BY m.duration_ms) FILTER (WHERE m.duration_ms IS NOT NULL) AS p95,
			percentile_cont(0.99) WITHIN GROUP (ORDER BY m.duration_ms) FILTER (WHERE m.duration_ms IS NOT NULL) AS p99
		FROM ops o
		LEFT JOIN token_totals tt ON tt.model_operation_id = o.model_operation_id
		LEFT JOIN cost_totals ct ON ct.model_operation_id = o.model_operation_id
		LEFT JOIN matched m ON m.model_operation_id = o.model_operation_id
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
