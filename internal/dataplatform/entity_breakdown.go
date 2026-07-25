package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionEntityBreakdown1 is the registered formula version for the
// per-entity breakdown/leaderboard family of queries added in Session 10:
// a "group and count/rank across entities within a time range" shape that
// contracts/data-platform/rollups.yaml's fixed dimension_scope tuple cannot
// express, since dimension_scope names exactly one already-known entity.
const FormulaVersionEntityBreakdown1 = "entity_breakdown/1"

// AgentBreakdown executes the "agent_breakdown_range" budgeted query: one
// row per agent_installation_id observed inside the partition-pruned
// half-open [from, to) range, with total event count and outcome split.
// Serves the /agents and /agents/:id "installation table; surface
// activity" panels, which need a per-agent leaderboard the single-scope
// RollupRange cannot produce.
func AgentBreakdown(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (EntityBreakdownResponse, error) {
	budget := Budgets["agent_breakdown_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return EntityBreakdownResponse{}, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		SELECT agent_installation_id,
			count(*) AS event_count,
			count(*) FILTER (WHERE outcome = 'succeeded') AS success_count,
			count(*) FILTER (WHERE outcome IN ('failed', 'timed_out', 'abandoned')) AS failure_count
		FROM events
		WHERE observed_at >= $1 AND observed_at < $2 AND agent_installation_id IS NOT NULL
		GROUP BY agent_installation_id
		ORDER BY event_count DESC, agent_installation_id
	`, from, to)
	if err != nil {
		return EntityBreakdownResponse{}, budgetOrErr(budget, started, err)
	}
	response, err := scanEntityRows(rows)
	if err != nil {
		return EntityBreakdownResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return EntityBreakdownResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}
	response.FormulaVersion = FormulaVersionEntityBreakdown1
	return response, nil
}

// ModelBreakdown executes the "model_breakdown_range" budgeted query: one
// row per model_id observed in model_operations inside the requested
// range, with request count and provider-reported token sum as Value.
// Serves the /models "request/token share" panel.
func ModelBreakdown(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (EntityBreakdownResponse, error) {
	budget := Budgets["model_breakdown_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return EntityBreakdownResponse{}, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		SELECT mo.model_id,
			count(DISTINCT mo.model_operation_id) AS event_count,
			coalesce(sum(tu.input_tokens + tu.output_tokens), 0) AS total_tokens
		FROM model_operations mo
		LEFT JOIN token_usage tu ON tu.model_operation_id = mo.model_operation_id AND tu.observed_at = mo.observed_at
		WHERE mo.observed_at >= $1 AND mo.observed_at < $2
		GROUP BY mo.model_id
		ORDER BY event_count DESC, mo.model_id
	`, from, to)
	if err != nil {
		return EntityBreakdownResponse{}, budgetOrErr(budget, started, err)
	}
	defer rows.Close()
	var response EntityBreakdownResponse
	var totalEvents int64
	for rows.Next() {
		var row EntityRow
		var totalTokens int64
		if err := rows.Scan(&row.EntityID, &row.EventCount, &totalTokens); err != nil {
			return EntityBreakdownResponse{}, err
		}
		value := float64(totalTokens)
		row.Value = &value
		response.Data = append(response.Data, row)
		totalEvents += row.EventCount
	}
	if err := rows.Err(); err != nil {
		return EntityBreakdownResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return EntityBreakdownResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}
	response.FormulaVersion = FormulaVersionEntityBreakdown1
	// model_operations has no independent "expected" population signal of
	// its own (unlike collection.coverage_ratio's reconciliation source), so
	// completeness here can only honestly report "some data present" vs
	// "no data present" rather than a covered_ratio against an expected
	// count. denominator == numerator when any row exists; zero denominator
	// (no rows) surfaces unknown via completenessFor, never a silent zero.
	response.Population = Population{Numerator: totalEvents, Denominator: totalEvents}
	response.Completeness = completenessFor(totalEvents, totalEvents)
	return response, nil
}

// ComponentBreakdown executes the "component_breakdown_range" budgeted
// query: one row per component_id observed in tool_calls inside the
// requested range, restricted to components of the given kind (e.g.
// "mcp" for MCP-server call volume, "" for every kind on /tools). Latency
// is an exact percentile_cont over that component's terminal calls only,
// matching contracts/data-platform/rollups.yaml `percentile_policy`
// (never averaged across components). Serves /tools "call timeline;
// success/errors/latency; tool table" and /components/mcp "calls/errors/
// latency" panels.
func ComponentBreakdown(ctx context.Context, pool *pgxpool.Pool, componentKind string, from, to time.Time) (EntityBreakdownResponse, error) {
	budget := Budgets["component_breakdown_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return EntityBreakdownResponse{}, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		SELECT tc.component_id,
			count(*) AS event_count,
			count(*) FILTER (WHERE tc.outcome = 'succeeded') AS success_count,
			count(*) FILTER (WHERE tc.outcome IN ('failed', 'timed_out', 'abandoned')) AS failure_count,
			percentile_cont(0.95) WITHIN GROUP (ORDER BY tc.duration_ms) FILTER (WHERE tc.duration_ms IS NOT NULL) AS p95_ms
		FROM tool_calls tc
		JOIN components c ON c.component_id = tc.component_id
		WHERE tc.observed_at >= $1 AND tc.observed_at < $2 AND tc.component_id IS NOT NULL
			AND ($3 = '' OR c.kind = $3)
		GROUP BY tc.component_id
		ORDER BY event_count DESC, tc.component_id
	`, from, to, componentKind)
	if err != nil {
		return EntityBreakdownResponse{}, budgetOrErr(budget, started, err)
	}
	defer rows.Close()
	var response EntityBreakdownResponse
	var numerator, denominator int64
	for rows.Next() {
		var row EntityRow
		var p95 *float64
		if err := rows.Scan(&row.EntityID, &row.EventCount, &row.SuccessCount, &row.FailureCount, &p95); err != nil {
			return EntityBreakdownResponse{}, err
		}
		if p95 != nil {
			row.Percentiles = &Percentiles{P95: p95}
		}
		response.Data = append(response.Data, row)
		numerator += row.SuccessCount
		denominator += row.SuccessCount + row.FailureCount
	}
	if err := rows.Err(); err != nil {
		return EntityBreakdownResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return EntityBreakdownResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}
	response.FormulaVersion = FormulaVersionEntityBreakdown1
	response.Population = Population{Numerator: numerator, Denominator: denominator}
	response.Completeness = completenessFor(numerator, denominator)
	return response, nil
}

func scanEntityRows(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}) (EntityBreakdownResponse, error) {
	defer rows.Close()
	var response EntityBreakdownResponse
	var numerator, denominator int64
	for rows.Next() {
		var row EntityRow
		if err := rows.Scan(&row.EntityID, &row.EventCount, &row.SuccessCount, &row.FailureCount); err != nil {
			return EntityBreakdownResponse{}, err
		}
		response.Data = append(response.Data, row)
		numerator += row.SuccessCount
		denominator += row.SuccessCount + row.FailureCount
	}
	if err := rows.Err(); err != nil {
		return EntityBreakdownResponse{}, err
	}
	response.Population = Population{Numerator: numerator, Denominator: denominator}
	response.Completeness = completenessFor(numerator, denominator)
	return response, nil
}
