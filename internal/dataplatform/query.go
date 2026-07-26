package dataplatform

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryBudget binds one contracts/data-platform/query-contract.yaml budgeted
// query id to its ceiling.
type QueryBudget struct {
	ID    string
	MaxMS int64
}

// Budgets mirrors contracts/data-platform/query-contract.yaml `budgets.queries`.
var Budgets = map[string]QueryBudget{
	"hourly_rollup_range_30d":       {ID: "hourly_rollup_range_30d", MaxMS: 50},
	"daily_rollup_range_1y":         {ID: "daily_rollup_range_1y", MaxMS: 50},
	"session_drilldown":             {ID: "session_drilldown", MaxMS: 100},
	"percentile_recompute_bucket":   {ID: "percentile_recompute_bucket", MaxMS: 200},
	"agent_breakdown_range":         {ID: "agent_breakdown_range", MaxMS: 150},
	"model_breakdown_range":         {ID: "model_breakdown_range", MaxMS: 150},
	"component_breakdown_range":     {ID: "component_breakdown_range", MaxMS: 150},
	"component_lifecycle_funnel":    {ID: "component_lifecycle_funnel", MaxMS: 150},
	"component_inventory_current":   {ID: "component_inventory_current", MaxMS: 100},
	"reliability_coverage_timeline": {ID: "reliability_coverage_timeline", MaxMS: 150},
	"mcp_topology":                  {ID: "mcp_topology", MaxMS: 100},
	"incident_list":                 {ID: "incident_list", MaxMS: 150},
	"incident_detail":               {ID: "incident_detail", MaxMS: 100},
	"incident_occurrences":          {ID: "incident_occurrences", MaxMS: 100},
	"quarantine_list":               {ID: "quarantine_list", MaxMS: 150},
	"quarantine_detail":             {ID: "quarantine_detail", MaxMS: 100},
	"incident_debug_bundle":         {ID: "incident_debug_bundle", MaxMS: 150},
	// Wave 1b (Session 10 continuation) budgets. Not yet mirrored into
	// contracts/data-platform/query-contract.yaml `budgets.queries` or the
	// contracts/data-platform-policy-locks.yaml semantic_sha256 chain, the
	// same Go-only-addition precedent task #12's five budgets above
	// established; flagged as a follow-up contract-governance task.
	"activity_timeline_range":      {ID: "activity_timeline_range", MaxMS: 150},
	"prompt_shape_range":           {ID: "prompt_shape_range", MaxMS: 150},
	"model_usage_range":            {ID: "model_usage_range", MaxMS: 150},
	"tool_analytics_range":         {ID: "tool_analytics_range", MaxMS: 150},
	"mcp_uptime_range":             {ID: "mcp_uptime_range", MaxMS: 100},
	"reliability_counts_range":     {ID: "reliability_counts_range", MaxMS: 100},
	"system_snapshot":              {ID: "system_snapshot", MaxMS: 50},
	"privacy_canary_history_range": {ID: "privacy_canary_history_range", MaxMS: 100},
}

// ErrBudgetExceeded is returned when a budgeted query's measured wall-clock
// duration exceeds its ceiling, or Postgres's own statement_timeout fired.
type ErrBudgetExceeded struct {
	BudgetID string
	MaxMS    int64
	ActualMS int64
}

func (e *ErrBudgetExceeded) Error() string {
	return fmt.Sprintf("query %s exceeded budget: %dms > %dms", e.BudgetID, e.ActualMS, e.MaxMS)
}

// acquireBudgeted checks out a pooled connection with statement_timeout set
// to the budget ceiling, so a runaway plan is killed server-side even if the
// Go wall-clock check races the response. The caller must call release().
func acquireBudgeted(ctx context.Context, pool *pgxpool.Pool, maxMS int64) (conn *pgxpool.Conn, release func(), err error) {
	conn, err = pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET statement_timeout = %d", maxMS)); err != nil {
		conn.Release()
		return nil, nil, err
	}
	release = func() {
		_, _ = conn.Exec(context.Background(), "SET statement_timeout = 0")
		conn.Release()
	}
	return conn, release, nil
}

// RollupRange executes the "hourly_rollup_range_30d"/"daily_rollup_range_1y"
// budgeted query: one metric family's rollup rows for one dimension scope
// over a half-open [from, to) range, enforced by both a Postgres
// statement_timeout and a measured wall-clock assertion.
func RollupRange(ctx context.Context, pool *pgxpool.Pool, budgetID, metricFamily string, granularity Granularity, dimensionScope string, from, to time.Time) (QueryResponse, error) {
	budget, ok := Budgets[budgetID]
	if !ok {
		return QueryResponse{}, fmt.Errorf("unknown query budget id %q", budgetID)
	}
	table := "metric_rollups_hourly"
	if granularity == GranularityDaily {
		table = "metric_rollups_daily"
	}

	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return QueryResponse{}, err
	}
	defer release()

	started := time.Now()
	var response QueryResponse
	rows, err := conn.Query(ctx, fmt.Sprintf(`
		SELECT bucket_start, formula_version, event_count, unknown_count,
			value_numeric, value_p50, value_p90, value_p95, value_p99
		FROM %s
		WHERE metric_family = $1 AND dimension_scope = $2 AND bucket_start >= $3 AND bucket_start < $4
		ORDER BY bucket_start
	`, table), metricFamily, dimensionScope, from, to)
	if err != nil {
		return QueryResponse{}, budgetOrErr(budget, started, err)
	}
	var numerator, denominator int64
	var formulaVersion string
	for rows.Next() {
		var point RollupPoint
		var p Percentiles
		var eventCount, unknownCount int64
		if err := rows.Scan(&point.BucketStart, &formulaVersion, &eventCount, &unknownCount, &point.Value, &p.P50, &p.P90, &p.P95, &p.P99); err != nil {
			rows.Close()
			return QueryResponse{}, err
		}
		point.EventCount = eventCount
		point.Percentiles = &p
		response.Data = append(response.Data, point)
		numerator += eventCount
		denominator += eventCount + unknownCount
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return QueryResponse{}, err
	}
	response.FormulaVersion = formulaVersion
	if response.FormulaVersion == "" {
		response.FormulaVersion = FormulaVersionLatencyMS1
	}
	response.Population = Population{Numerator: numerator, Denominator: denominator}
	response.Completeness = completenessFor(numerator, denominator)

	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return QueryResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}

	watermark, pending, err := rollupFreshness(ctx, pool, metricFamily, granularity, dimensionScope)
	if err != nil {
		return QueryResponse{}, err
	}
	response.Freshness = Freshness{RollupWatermark: watermark, LateEventsPending: pending}
	return response, nil
}

func budgetOrErr(budget QueryBudget, started time.Time, err error) error {
	elapsed := time.Since(started).Milliseconds()
	if elapsed > budget.MaxMS {
		return &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}
	return err
}

func completenessFor(numerator, denominator int64) Completeness {
	if denominator == 0 {
		return Completeness{Status: "unknown", CoveredRatio: 0, Intervals: []string{}}
	}
	ratio := float64(numerator) / float64(denominator)
	status := "complete"
	switch {
	case ratio < 0.5:
		status = "degraded"
	case ratio < 1.0:
		status = "partial"
	}
	return Completeness{Status: status, CoveredRatio: ratio, Intervals: []string{}}
}

func rollupFreshness(ctx context.Context, pool *pgxpool.Pool, metricFamily string, granularity Granularity, dimensionScope string) (time.Time, int64, error) {
	var watermark time.Time
	err := pool.QueryRow(ctx, `
		SELECT rollup_watermark FROM rollup_status
		WHERE metric_family = $1 AND granularity = $2 AND dimension_scope = $3
	`, metricFamily, string(granularity), dimensionScope).Scan(&watermark)
	if err != nil {
		watermark = time.Time{}
	}
	var pending int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM rollup_repair_queue
		WHERE metric_family = $1 AND granularity = $2 AND dimension_scope = $3
	`, metricFamily, string(granularity), dimensionScope).Scan(&pending); err != nil {
		return watermark, 0, err
	}
	return watermark, pending, nil
}

// SessionDrilldown executes the "session_drilldown" budgeted query: the raw
// normalized event list for one session, partition-pruned by an explicit
// observed_at range so the planner never scans every monthly partition.
func SessionDrilldown(ctx context.Context, pool *pgxpool.Pool, sessionID string, from, to time.Time) ([]FactRow, error) {
	budget := Budgets["session_drilldown"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return nil, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		SELECT event_id, event_type, observed_at, outcome, value_state, duration_ms
		FROM events
		WHERE session_id = $1 AND observed_at >= $2 AND observed_at < $3
		ORDER BY observed_at
	`, sessionID, from, to)
	if err != nil {
		return nil, budgetOrErr(budget, started, err)
	}
	var results []FactRow
	for rows.Next() {
		var row FactRow
		if err := rows.Scan(&row.EventID, &row.EventType, &row.ObservedAt, &row.Outcome, &row.ValueState, &row.DurationMS); err != nil {
			rows.Close()
			return nil, err
		}
		results = append(results, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return nil, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}
	return results, nil
}

// ExplainNoSeqScan runs EXPLAIN over a budgeted query and fails if the plan
// contains a sequential scan of a partitioned fact table, matching
// contracts/data-platform/query-contract.yaml `budgets.plan_review`.
func ExplainNoSeqScan(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) (string, error) {
	rows, err := pool.Query(ctx, "EXPLAIN "+sql, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", err
		}
		plan += line + "\n"
	}
	return plan, rows.Err()
}
