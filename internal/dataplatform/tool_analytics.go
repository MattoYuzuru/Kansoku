package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionToolAnalytics1 is the registered formula version for the
// tool analytics query.
const FormulaVersionToolAnalytics1 = "tool_analytics/1"

// ToolAnalytics executes the "tool_analytics_range" budgeted query: one row
// per calendar day inside the half-open [from, to) range with tool_calls
// volume, success/failure split and exact percentile_cont latency,
// optionally restricted to one component_id (an MCP server or any other
// component). An empty componentID selects every component, matching
// ComponentBreakdown's "" == all-kinds convention. Serves the /tools
// "tool-analytics" panel and the /components/mcp "mcp-health" panel's
// calls/errors/latency series (tool.calls, tool.success_ratio,
// tool.latency_seconds).
func ToolAnalytics(ctx context.Context, pool *pgxpool.Pool, componentID string, from, to time.Time) (ToolAnalyticsResponse, error) {
	budget := Budgets["tool_analytics_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return ToolAnalyticsResponse{}, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		SELECT date_trunc('day', tc.observed_at) AS day,
			count(*) AS call_count,
			count(*) FILTER (WHERE tc.outcome = 'succeeded') AS success_count,
			count(*) FILTER (WHERE tc.outcome IN ('failed', 'timed_out', 'abandoned')) AS failure_count,
			percentile_cont(0.50) WITHIN GROUP (ORDER BY tc.duration_ms) FILTER (WHERE tc.duration_ms IS NOT NULL) AS p50,
			percentile_cont(0.90) WITHIN GROUP (ORDER BY tc.duration_ms) FILTER (WHERE tc.duration_ms IS NOT NULL) AS p90,
			percentile_cont(0.95) WITHIN GROUP (ORDER BY tc.duration_ms) FILTER (WHERE tc.duration_ms IS NOT NULL) AS p95,
			percentile_cont(0.99) WITHIN GROUP (ORDER BY tc.duration_ms) FILTER (WHERE tc.duration_ms IS NOT NULL) AS p99
		FROM tool_calls tc
		WHERE tc.observed_at >= $1 AND tc.observed_at < $2
			AND ($3 = '' OR tc.component_id = $3)
		GROUP BY day
		ORDER BY day
	`, from, to, componentID)
	if err != nil {
		return ToolAnalyticsResponse{}, budgetOrErr(budget, started, err)
	}
	var response ToolAnalyticsResponse
	var numerator, denominator int64
	for rows.Next() {
		var row ToolAnalyticsDayRow
		var p Percentiles
		if err := rows.Scan(&row.Day, &row.CallCount, &row.SuccessCount, &row.FailureCount, &p.P50, &p.P90, &p.P95, &p.P99); err != nil {
			rows.Close()
			return ToolAnalyticsResponse{}, err
		}
		if p.P50 != nil || p.P90 != nil || p.P95 != nil || p.P99 != nil {
			row.Percentiles = &p
		}
		response.Data = append(response.Data, row)
		numerator += row.SuccessCount
		denominator += row.SuccessCount + row.FailureCount
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ToolAnalyticsResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return ToolAnalyticsResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}

	response.FormulaVersion = FormulaVersionToolAnalytics1
	response.Population = Population{Numerator: numerator, Denominator: denominator}
	response.Completeness = completenessFor(numerator, denominator)

	watermark, pending, err := aggregateSourceWatermarkFreshness(ctx, pool)
	if err != nil {
		return ToolAnalyticsResponse{}, err
	}
	response.Freshness = Freshness{RollupWatermark: watermark, LateEventsPending: pending}
	return response, nil
}
