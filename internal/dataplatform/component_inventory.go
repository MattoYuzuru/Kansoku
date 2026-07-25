package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const FormulaVersionComponentInventory1 = "component_inventory_current/1"

// ComponentInventory returns the current sanitized component projection
// across every completed inventory target. Completeness is target coverage,
// not the component row count, so a complete scan that finds zero MCP servers
// remains a measured empty inventory instead of becoming "unknown".
func ComponentInventory(
	ctx context.Context,
	pool *pgxpool.Pool,
	componentKind string,
) (InventoryComponentResponse, error) {
	budget := Budgets["component_inventory_current"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return InventoryComponentResponse{}, err
	}
	defer release()
	started := time.Now()
	rows, err := conn.Query(ctx, `
		SELECT c.component_id, coalesce(c.declared_name, c.component_id), c.kind,
			coalesce(c.source_scope, 'unknown'), cv.version, cv.version_state,
			cis.enabled, ai.agent_id, ai.agent_installation_id,
			cis.first_seen_at, cis.last_seen_at
		FROM component_inventory_state cis
		JOIN component_installations ci
		  ON ci.component_installation_id = cis.component_installation_id
		JOIN component_versions cv ON cv.component_version_id = ci.component_version_id
		JOIN components c ON c.component_id = cv.component_id
		JOIN agent_installations ai
		  ON ai.agent_installation_id = ci.agent_installation_id
		WHERE ($1 = '' OR c.kind = $1)
		ORDER BY c.kind, coalesce(c.declared_name, c.component_id), c.component_id
	`, componentKind)
	if err != nil {
		return InventoryComponentResponse{}, budgetOrErr(budget, started, err)
	}
	defer rows.Close()
	var response InventoryComponentResponse
	for rows.Next() {
		var row InventoryComponentRow
		if err := rows.Scan(
			&row.ComponentID, &row.DeclaredName, &row.Kind, &row.SourceScope,
			&row.Version, &row.VersionState, &row.Enabled, &row.AgentID,
			&row.AgentInstallationID, &row.FirstSeenAt, &row.LastSeenAt,
		); err != nil {
			return InventoryComponentResponse{}, err
		}
		response.Data = append(response.Data, row)
	}
	if err := rows.Err(); err != nil {
		return InventoryComponentResponse{}, err
	}
	var completeTargets, totalTargets int64
	var watermark *time.Time
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE state = 'complete'), count(*),
			max(last_succeeded_at)
		FROM inventory_collection_status
	`).Scan(&completeTargets, &totalTargets, &watermark); err != nil {
		return InventoryComponentResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return InventoryComponentResponse{}, &ErrBudgetExceeded{
			BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed,
		}
	}
	response.FormulaVersion = FormulaVersionComponentInventory1
	response.Population = Population{Numerator: completeTargets, Denominator: totalTargets}
	response.Completeness = completenessFor(completeTargets, totalTargets)
	if watermark != nil {
		response.Freshness.RollupWatermark = watermark.UTC()
	}
	return response, nil
}
