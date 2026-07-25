package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionMCPTopology1 is the registered formula version for the MCP
// server/tool topology tree query.
const FormulaVersionMCPTopology1 = "mcp_topology/1"

// MCPTopology executes the "mcp_topology" budgeted query: every component
// of kind "mcp" (a server) together with its declared child components via
// component_relations (relation_kind = 'bundles', the schema's only
// parent-owns-child relation kind), plus that server's most recent
// mcp_connections.state observed inside the half-open [from, to) range.
//
// This is the one dedicated new route named by ADR 0013 decision #12: no
// existing metric_family/dimension_scope pair in contracts/metrics.yaml can
// represent a parent/child relationship tree, since dimension_scope is a
// flat 4-tuple naming exactly one already-known entity, never a graph.
// Only opaque component ids and the connection state enum are returned --
// never a command, path or credential.
func MCPTopology(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (ComponentTopologyResponse, error) {
	budget := Budgets["mcp_topology"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return ComponentTopologyResponse{}, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		SELECT c.component_id, c.kind,
			coalesce(array_agg(DISTINCT cr.child_id) FILTER (WHERE cr.child_id IS NOT NULL), '{}') AS child_component_ids
		FROM components c
		LEFT JOIN component_relations cr ON cr.parent_id = c.component_id AND cr.relation_kind = 'bundles'
		WHERE c.kind = 'mcp'
		GROUP BY c.component_id, c.kind
		ORDER BY c.component_id
	`)
	if err != nil {
		return ComponentTopologyResponse{}, budgetOrErr(budget, started, err)
	}
	var nodes []ComponentTreeNode
	for rows.Next() {
		var node ComponentTreeNode
		if err := rows.Scan(&node.ComponentID, &node.Kind, &node.ChildComponentIDs); err != nil {
			rows.Close()
			return ComponentTopologyResponse{}, err
		}
		nodes = append(nodes, node)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ComponentTopologyResponse{}, err
	}

	stateRows, err := conn.Query(ctx, `
		SELECT DISTINCT ON (component_id) component_id, state, observed_at
		FROM mcp_connections
		WHERE observed_at >= $1 AND observed_at < $2 AND component_id IS NOT NULL
		ORDER BY component_id, observed_at DESC
	`, from, to)
	if err != nil {
		return ComponentTopologyResponse{}, budgetOrErr(budget, started, err)
	}
	latestState := make(map[string]struct {
		state      string
		observedAt time.Time
	}, len(nodes))
	for stateRows.Next() {
		var componentID, state string
		var observedAt time.Time
		if err := stateRows.Scan(&componentID, &state, &observedAt); err != nil {
			stateRows.Close()
			return ComponentTopologyResponse{}, err
		}
		latestState[componentID] = struct {
			state      string
			observedAt time.Time
		}{state: state, observedAt: observedAt}
	}
	stateRows.Close()
	if err := stateRows.Err(); err != nil {
		return ComponentTopologyResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return ComponentTopologyResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}

	var response ComponentTopologyResponse
	var withState int64
	for _, node := range nodes {
		if entry, ok := latestState[node.ComponentID]; ok {
			node.LatestConnectionState = entry.state
			observedAt := entry.observedAt
			node.ConnectionObservedAt = &observedAt
			withState++
		}
		response.Data = append(response.Data, node)
	}
	response.FormulaVersion = FormulaVersionMCPTopology1
	response.Population = Population{Numerator: withState, Denominator: int64(len(nodes))}
	response.Completeness = completenessFor(withState, int64(len(nodes)))
	return response, nil
}
