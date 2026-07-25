package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionComponentFunnel1 is the registered formula version for the
// component lifecycle funnel query.
const FormulaVersionComponentFunnel1 = "component_lifecycle_funnel/2"

// canonicalLifecycleStages mirrors contracts/capabilities.yaml
// `lifecycle.canonical_progression` plus the parallel `opportunity_detected`
// state. lifecycle_stage is a free-form TEXT column (no DB CHECK
// constraint), so the funnel query group-bys whatever values are actually
// present rather than assuming this exact set is populated; this list is
// only used to report every canonical stage even when a stage has zero
// observed rows (an honest "not_observed" datum rather than an omitted one).
var canonicalLifecycleStages = []string{
	"opportunity_detected",
	"installed",
	"enabled",
	"exposed",
	"invoked",
	"loaded",
	"executed",
	"succeeded",
}

// ComponentLifecycleFunnel executes the "component_lifecycle_funnel"
// budgeted query: for one component kind (skill/plugin/mcp/hook/command),
// the distinct-component count and raw event count at each canonical
// lifecycle stage observed inside the half-open [from, to) range. Serves
// the overview component funnel and the /components/skills,
// /components/plugins per-kind lifecycle panels.
//
// Every canonical stage from contracts/capabilities.yaml is always present
// in the response, with component_count/event_count of 0 when that stage
// was never observed for the requested kind/range -- a real, counted zero,
// not a stage silently missing from the array.
func ComponentLifecycleFunnel(ctx context.Context, pool *pgxpool.Pool, componentKind string, from, to time.Time) (FunnelResponse, error) {
	budget := Budgets["component_lifecycle_funnel"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return FunnelResponse{}, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		WITH observations AS (
			SELECT cle.lifecycle_stage, c.component_id,
				'cle:' || cle.component_lifecycle_event_id AS observation_id
			FROM component_lifecycle_events cle
			JOIN component_installations ci ON ci.component_installation_id = cle.component_installation_id
			JOIN component_versions cv ON cv.component_version_id = ci.component_version_id
			JOIN components c ON c.component_id = cv.component_id
			WHERE cle.observed_at >= $1 AND cle.observed_at < $2
				AND ($3 = '' OR c.kind = $3)
			UNION ALL
			SELECT CASE e.event_type
					WHEN 'component.installed' THEN 'installed'
					WHEN 'component.loaded' THEN 'loaded'
					WHEN 'component.invoked' THEN 'invoked'
					WHEN 'component.executed' THEN 'executed'
				END AS lifecycle_stage,
				c.component_id, 'evt:' || e.event_id AS observation_id
			FROM events e
			JOIN components c ON c.component_id = e.component_id
			WHERE e.observed_at >= $1 AND e.observed_at < $2
				AND e.event_type IN ('component.installed','component.loaded','component.invoked','component.executed')
				AND ($3 = '' OR c.kind = $3)
		)
		SELECT lifecycle_stage,
			count(DISTINCT component_id) AS component_count,
			count(*) AS event_count
		FROM observations
		GROUP BY lifecycle_stage
	`, from, to, componentKind)
	if err != nil {
		return FunnelResponse{}, budgetOrErr(budget, started, err)
	}
	observed := make(map[string]FunnelStageRow, len(canonicalLifecycleStages))
	for rows.Next() {
		var stage string
		var componentCount, eventCount int64
		if err := rows.Scan(&stage, &componentCount, &eventCount); err != nil {
			rows.Close()
			return FunnelResponse{}, err
		}
		observed[stage] = FunnelStageRow{Stage: stage, ComponentCount: componentCount, EventCount: eventCount}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return FunnelResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return FunnelResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}

	var response FunnelResponse
	var numerator, denominator int64
	installedCount := int64(0)
	if row, ok := observed["installed"]; ok {
		installedCount = row.ComponentCount
	}
	for _, stage := range canonicalLifecycleStages {
		row, ok := observed[stage]
		if !ok {
			row = FunnelStageRow{Stage: stage, ComponentCount: 0, EventCount: 0}
		}
		response.Data = append(response.Data, row)
	}
	// Also surface any non-canonical stage values actually present (schema
	// allows arbitrary TEXT), appended after the canonical set, so the
	// response never silently drops real data because it didn't match the
	// expected vocabulary.
	for stage, row := range observed {
		if !isCanonicalLifecycleStage(stage) {
			response.Data = append(response.Data, row)
		}
	}
	// Population/completeness measures reporting coverage against the
	// "installed" stage as the eligible population baseline (every
	// component that reached "installed" is eligible to have progressed
	// further); succeeded is the numerator. When nothing was installed in
	// range, completenessFor's zero-denominator path reports unknown.
	if succeeded, ok := observed["succeeded"]; ok {
		numerator = succeeded.ComponentCount
	}
	denominator = installedCount
	response.FormulaVersion = FormulaVersionComponentFunnel1
	response.Population = Population{Numerator: numerator, Denominator: denominator}
	response.Completeness = completenessFor(numerator, denominator)
	return response, nil
}

func isCanonicalLifecycleStage(stage string) bool {
	for _, candidate := range canonicalLifecycleStages {
		if candidate == stage {
			return true
		}
	}
	return false
}
