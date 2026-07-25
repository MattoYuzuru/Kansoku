package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionMCPUptime1 is the registered formula version for the MCP
// connection uptime ratio query.
const FormulaVersionMCPUptime1 = "mcp_uptime/1"

// MCPUptime executes the "mcp_uptime_range" budgeted query: per MCP-server
// component, the fraction of its own observable window (the span between
// its first and last observed mcp_connections state change within the
// half-open [from, to) range) actually spent in the "connected" state.
// Serves /components/mcp "mcp-health" (mcp.connection_uptime_ratio).
//
// mcp_connections.state has no DB CHECK constraint enumerating valid values
// (free text; see mcp_topology.go's doc comment), so this query only ever
// compares against the literal 'connected' string rather than assuming a
// closed enum. ObservableSeconds is deliberately the span between the first
// and last observation for that component, not the full requested range:
// contracts/metrics.yaml's mcp.connection_uptime_ratio population is
// "configured MCP server intervals where connection state is observable",
// and a server with only one observation in range (or none) has an
// observable window that cannot honestly be assumed to cover the whole
// range. A component with fewer than two observations gets a nil
// UptimeRatio (unknown), never a fabricated 0% or 100%.
func MCPUptime(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (MCPUptimeResponse, error) {
	budget := Budgets["mcp_uptime_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return MCPUptimeResponse{}, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		WITH mcp_components AS (
			SELECT component_id FROM components WHERE kind = 'mcp'
		),
		observations AS (
			SELECT mc.component_id, mc.state, mc.observed_at,
				lead(mc.observed_at) OVER (PARTITION BY mc.component_id ORDER BY mc.observed_at) AS next_observed_at
			FROM mcp_connections mc
			JOIN mcp_components c ON c.component_id = mc.component_id
			WHERE mc.observed_at >= $1 AND mc.observed_at < $2 AND mc.component_id IS NOT NULL
		)
		SELECT component_id,
			count(*) AS observation_count,
			min(observed_at) AS first_observed_at,
			max(observed_at) AS last_observed_at,
			coalesce(sum(extract(epoch FROM (next_observed_at - observed_at))) FILTER (WHERE state = 'connected' AND next_observed_at IS NOT NULL), 0) AS connected_seconds
		FROM observations
		GROUP BY component_id
	`, from, to)
	if err != nil {
		return MCPUptimeResponse{}, budgetOrErr(budget, started, err)
	}
	var response MCPUptimeResponse
	var numerator, denominator int64
	for rows.Next() {
		var componentID string
		var observationCount int64
		var firstObservedAt, lastObservedAt time.Time
		var connectedSeconds float64
		if err := rows.Scan(&componentID, &observationCount, &firstObservedAt, &lastObservedAt, &connectedSeconds); err != nil {
			rows.Close()
			return MCPUptimeResponse{}, err
		}
		row := MCPUptimeRow{ComponentID: componentID, ConnectedSeconds: connectedSeconds}
		denominator++
		if observationCount >= 2 {
			observable := lastObservedAt.Sub(firstObservedAt).Seconds()
			row.ObservableSeconds = observable
			if observable > 0 {
				ratio := connectedSeconds / observable
				row.UptimeRatio = &ratio
				numerator++
			}
		}
		response.Data = append(response.Data, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return MCPUptimeResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return MCPUptimeResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}

	response.FormulaVersion = FormulaVersionMCPUptime1
	response.Population = Population{Numerator: numerator, Denominator: denominator}
	response.Completeness = completenessFor(numerator, denominator)
	return response, nil
}
