//go:build postgres_integration

// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema work.
package dataplatform

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// insertModel/insertProvider are minimal fixture helpers for the
// model_operations/token_usage dimension chain, which
// postgres_integration_test.go's existing testDimensionRefs/EnsureDimensions
// path does not cover.
func insertProviderAndModel(t *testing.T, ctx context.Context, pool *pgxpool.Pool, providerID, modelID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO providers (provider_id) VALUES ($1) ON CONFLICT DO NOTHING`, providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO models (model_id, provider_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, modelID, providerID); err != nil {
		t.Fatalf("insert model: %v", err)
	}
}

func insertComponent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, componentID, kind string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO components (component_id, kind) VALUES ($1, $2) ON CONFLICT DO NOTHING`, componentID, kind); err != nil {
		t.Fatalf("insert component: %v", err)
	}
}

// TestAgentBreakdownGroupsAcrossAgentsWithinRangeAndBudget is the exit-gate
// proof that AgentBreakdown produces a real per-agent leaderboard (which
// RollupRange's single dimension_scope cannot), respects the half-open
// [from, to) range boundary, and returns within its registered budget.
func TestAgentBreakdownGroupsAcrossAgentsWithinRangeAndBudget(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()

	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartition(ctx, pool, "events", base); err != nil {
		t.Fatalf("ensure partition: %v", err)
	}
	if err := EnsurePartition(ctx, pool, "events", base.AddDate(0, 0, -1)); err != nil {
		t.Fatalf("ensure previous-month partition: %v", err)
	}

	sourceInstanceID := "src_agent_breakdown"
	refsA := testDimensionRefs(sourceInstanceID)
	refsA.AgentInstallationID = "ain_alpha"
	refsA.ComponentID = ""
	if err := EnsureDimensions(ctx, pool, refsA); err != nil {
		t.Fatalf("ensure dimensions alpha: %v", err)
	}
	refsB := refsA
	refsB.AgentInstallationID = "ain_bravo"
	if err := EnsureDimensions(ctx, pool, refsB); err != nil {
		t.Fatalf("ensure dimensions bravo: %v", err)
	}

	insertEvent := func(index int, agentInstallationID, outcome string, observedAt time.Time) {
		eventID := "evt_agentbd_" + agentInstallationID + "_" + outcome + "_" + observedAt.Format("150405.000000000")
		_, err := pool.Exec(ctx, `
			INSERT INTO events (event_id, fact_key, event_type, observed_at, source_instance_id, source_native_event_id, sequence, agent_installation_id, value_state, outcome, correlation_status)
			VALUES ($1, $1, 'component.executed', $2, $3, $1, $4, $5, 'observed', $6, 'exact')
		`, eventID, observedAt, sourceInstanceID, index, agentInstallationID, outcome)
		if err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}
	// alpha: 2 succeeded, 1 failed inside range.
	insertEvent(1, "ain_alpha", "succeeded", base.Add(time.Minute))
	insertEvent(2, "ain_alpha", "succeeded", base.Add(2*time.Minute))
	insertEvent(3, "ain_alpha", "failed", base.Add(3*time.Minute))
	// bravo: 1 succeeded inside range.
	insertEvent(4, "ain_bravo", "succeeded", base.Add(4*time.Minute))
	// outside range: must never be counted (half-open boundary proof).
	insertEvent(5, "ain_alpha", "succeeded", base.Add(-time.Hour))
	insertEvent(6, "ain_alpha", "succeeded", base.AddDate(0, 0, 2))

	to := base.AddDate(0, 0, 1)
	started := time.Now()
	response, err := AgentBreakdown(ctx, pool, base, to)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("AgentBreakdown: %v", err)
	}
	if elapsed > time.Duration(Budgets["agent_breakdown_range"].MaxMS)*time.Millisecond {
		t.Fatalf("agent_breakdown_range exceeded its budget: %v", elapsed)
	}
	if response.FormulaVersion != FormulaVersionEntityBreakdown1 {
		t.Fatalf("formula_version = %q, want %q", response.FormulaVersion, FormulaVersionEntityBreakdown1)
	}
	if len(response.Data) != 2 {
		t.Fatalf("expected 2 agent rows (out-of-range events must not leak in), got %d: %+v", len(response.Data), response.Data)
	}
	byID := make(map[string]EntityRow, len(response.Data))
	for _, row := range response.Data {
		byID[row.EntityID] = row
	}
	alpha, ok := byID["ain_alpha"]
	if !ok {
		t.Fatalf("missing ain_alpha row: %+v", response.Data)
	}
	if alpha.EventCount != 3 || alpha.SuccessCount != 2 || alpha.FailureCount != 1 {
		t.Fatalf("ain_alpha row = %+v, want event=3 success=2 failure=1", alpha)
	}
	if alpha.AgentID != refsA.AgentID {
		t.Fatalf("ain_alpha agent_id = %q, want %q", alpha.AgentID, refsA.AgentID)
	}
	bravo, ok := byID["ain_bravo"]
	if !ok {
		t.Fatalf("missing ain_bravo row: %+v", response.Data)
	}
	if bravo.EventCount != 1 || bravo.SuccessCount != 1 || bravo.FailureCount != 0 {
		t.Fatalf("ain_bravo row = %+v, want event=1 success=1 failure=0", bravo)
	}
	// numerator=succeeded(3), denominator=succeeded+failed(4) across both agents.
	if response.Population.Numerator != 3 || response.Population.Denominator != 4 {
		t.Fatalf("population = %+v, want numerator=3 denominator=4", response.Population)
	}
	if response.Completeness.Status == "unknown" {
		t.Fatalf("completeness should not be unknown when data is present: %+v", response.Completeness)
	}
}

// TestAgentBreakdownEmptyRangeReportsUnknownNotZero proves the "no silent
// zero" convention: an empty range must report completeness "unknown" via a
// zero denominator, not a fabricated "complete" with empty data.
func TestAgentBreakdownEmptyRangeReportsUnknownNotZero(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartition(ctx, pool, "events", base); err != nil {
		t.Fatalf("ensure partition: %v", err)
	}
	response, err := AgentBreakdown(ctx, pool, base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("AgentBreakdown: %v", err)
	}
	if len(response.Data) != 0 {
		t.Fatalf("expected zero rows, got %d", len(response.Data))
	}
	if response.Completeness.Status != "unknown" {
		t.Fatalf("completeness = %+v, want unknown for empty range", response.Completeness)
	}
}

// TestModelBreakdownSumsTokensAcrossModels proves ModelBreakdown groups
// model_operations/token_usage across every model observed in range and
// reports a real "data present" completeness rather than a hardcoded
// "complete".
func TestModelBreakdownSumsTokensAcrossModels(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for _, table := range []string{"model_operations", "token_usage"} {
		if err := EnsurePartition(ctx, pool, table, base); err != nil {
			t.Fatalf("ensure partition %s: %v", table, err)
		}
	}
	insertProviderAndModel(t, ctx, pool, "prov_anthropic", "model_sonnet")
	insertProviderAndModel(t, ctx, pool, "prov_anthropic", "model_haiku")

	insertOperation := func(id, modelID string, observedAt time.Time, inputTokens, outputTokens int64) {
		if _, err := pool.Exec(ctx, `INSERT INTO model_operations (model_operation_id, observed_at, model_id) VALUES ($1, $2, $3)`, id, observedAt, modelID); err != nil {
			t.Fatalf("insert model_operation: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO token_usage (token_usage_id, observed_at, model_operation_id, input_tokens, output_tokens) VALUES ($1, $2, $3, $4, $5)`,
			"tu_"+id, observedAt, id, inputTokens, outputTokens); err != nil {
			t.Fatalf("insert token_usage: %v", err)
		}
	}
	insertOperation("mop_1", "model_sonnet", base.Add(time.Minute), 100, 50)
	insertOperation("mop_2", "model_sonnet", base.Add(2*time.Minute), 200, 75)
	insertOperation("mop_3", "model_haiku", base.Add(3*time.Minute), 10, 5)
	// Outside range: must not leak in.
	insertOperation("mop_4", "model_sonnet", base.AddDate(0, 0, 5), 999, 999)

	response, err := ModelBreakdown(ctx, pool, base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("ModelBreakdown: %v", err)
	}
	byID := make(map[string]EntityRow, len(response.Data))
	for _, row := range response.Data {
		byID[row.EntityID] = row
	}
	sonnet, ok := byID["model_sonnet"]
	if !ok {
		t.Fatalf("missing model_sonnet row: %+v", response.Data)
	}
	if sonnet.EventCount != 2 {
		t.Fatalf("model_sonnet event_count = %d, want 2", sonnet.EventCount)
	}
	if sonnet.Value == nil || *sonnet.Value != float64(100+50+200+75) {
		t.Fatalf("model_sonnet total tokens = %v, want %d", sonnet.Value, 100+50+200+75)
	}
	haiku, ok := byID["model_haiku"]
	if !ok {
		t.Fatalf("missing model_haiku row: %+v", response.Data)
	}
	if haiku.Value == nil || *haiku.Value != float64(15) {
		t.Fatalf("model_haiku total tokens = %v, want 15", haiku.Value)
	}
	if response.Population.Denominator == 0 {
		t.Fatalf("expected a nonzero denominator when operations are present: %+v", response.Population)
	}
	if response.Completeness.Status == "unknown" {
		t.Fatalf("completeness should not be unknown when data is present: %+v", response.Completeness)
	}
}

// TestComponentBreakdownFiltersByKindAndComputesExactP95 proves
// ComponentBreakdown restricts to the requested component kind, computes an
// exact (not averaged) p95 per component, and that an empty-string kind
// selects every kind.
func TestComponentBreakdownFiltersByKindAndComputesExactP95(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartition(ctx, pool, "tool_calls", base); err != nil {
		t.Fatalf("ensure partition: %v", err)
	}
	insertComponent(t, ctx, pool, "comp_mcp_server", "mcp")
	insertComponent(t, ctx, pool, "comp_skill_one", "skill")

	insertCall := func(id, componentID, outcome string, observedAt time.Time, durationMS int64) {
		if _, err := pool.Exec(ctx, `INSERT INTO tool_calls (tool_call_id, observed_at, component_id, duration_ms, outcome) VALUES ($1, $2, $3, $4, $5)`,
			id, observedAt, componentID, durationMS, outcome); err != nil {
			t.Fatalf("insert tool_call: %v", err)
		}
	}
	durations := []int64{100, 150, 200, 250, 300}
	for i, d := range durations {
		insertCall("tc_mcp_"+string(rune('a'+i)), "comp_mcp_server", "succeeded", base.Add(time.Duration(i)*time.Minute), d)
	}
	insertCall("tc_mcp_fail", "comp_mcp_server", "failed", base.Add(10*time.Minute), 50)
	insertCall("tc_skill_a", "comp_skill_one", "succeeded", base.Add(time.Minute), 999)

	response, err := ComponentBreakdown(ctx, pool, "mcp", base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("ComponentBreakdown: %v", err)
	}
	if len(response.Data) != 1 {
		t.Fatalf("expected exactly one mcp component row, got %d: %+v", len(response.Data), response.Data)
	}
	row := response.Data[0]
	if row.EntityID != "comp_mcp_server" {
		t.Fatalf("entity_id = %q, want comp_mcp_server", row.EntityID)
	}
	if row.EventCount != 6 || row.SuccessCount != 5 || row.FailureCount != 1 {
		t.Fatalf("row = %+v, want event=6 success=5 failure=1", row)
	}
	if row.Percentiles == nil || row.Percentiles.P95 == nil {
		t.Fatalf("expected a computed p95 percentile, got %+v", row.Percentiles)
	}
	wantP95 := exactPercentile(append(append([]int64{}, durations...), 50), 0.95)
	if diff := *row.Percentiles.P95 - wantP95; diff > 0.001 || diff < -0.001 {
		t.Fatalf("p95 = %v, want exact %v", *row.Percentiles.P95, wantP95)
	}

	// Empty-string kind selects every component kind.
	all, err := ComponentBreakdown(ctx, pool, "", base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("ComponentBreakdown (all kinds): %v", err)
	}
	if len(all.Data) != 2 {
		t.Fatalf("expected 2 rows across all kinds, got %d: %+v", len(all.Data), all.Data)
	}
}

// TestComponentLifecycleFunnelZeroFillsUnobservedCanonicalStages proves the
// funnel always reports every canonical stage (a real counted zero, never
// an omitted stage), filters by component kind, and surfaces a non-canonical
// stage value rather than silently dropping it.
func TestComponentLifecycleFunnelZeroFillsUnobservedCanonicalStages(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	insertComponent(t, ctx, pool, "comp_skill_funnel", "skill")
	if _, err := pool.Exec(ctx, `INSERT INTO component_versions (component_version_id, component_id, version) VALUES ($1, $2, '1.0.0')`, "cv_funnel", "comp_skill_funnel"); err != nil {
		t.Fatalf("insert component_version: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO devices (device_id) VALUES ('dev_funnel') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("insert device: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO agent_installations (agent_installation_id, device_id, agent_id) VALUES ('ain_funnel', 'dev_funnel', 'fixture-agent') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("insert agent_installation: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO component_installations (component_installation_id, component_version_id, agent_installation_id) VALUES ($1, $2, $3)`,
		"ci_funnel", "cv_funnel", "ain_funnel"); err != nil {
		t.Fatalf("insert component_installation: %v", err)
	}

	insertLifecycleEvent := func(id, stage string, observedAt time.Time) {
		if _, err := pool.Exec(ctx, `INSERT INTO component_lifecycle_events (component_lifecycle_event_id, component_installation_id, observed_at, lifecycle_stage) VALUES ($1, $2, $3, $4)`,
			id, "ci_funnel", observedAt, stage); err != nil {
			t.Fatalf("insert lifecycle event: %v", err)
		}
	}
	insertLifecycleEvent("cle_1", "installed", base.Add(time.Minute))
	insertLifecycleEvent("cle_2", "enabled", base.Add(2*time.Minute))
	insertLifecycleEvent("cle_3", "succeeded", base.Add(3*time.Minute))
	insertLifecycleEvent("cle_4", "vendor_custom_stage", base.Add(4*time.Minute))

	response, err := ComponentLifecycleFunnel(ctx, pool, "skill", base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("ComponentLifecycleFunnel: %v", err)
	}
	if response.FormulaVersion != FormulaVersionComponentFunnel1 {
		t.Fatalf("formula_version = %q, want %q", response.FormulaVersion, FormulaVersionComponentFunnel1)
	}
	byStage := make(map[string]FunnelStageRow, len(response.Data))
	for _, row := range response.Data {
		byStage[row.Stage] = row
	}
	for _, stage := range canonicalLifecycleStages {
		row, ok := byStage[stage]
		if !ok {
			t.Fatalf("canonical stage %q missing from response entirely, want a zero-filled row", stage)
		}
		switch stage {
		case "installed", "enabled", "succeeded":
			if row.ComponentCount == 0 {
				t.Fatalf("stage %q expected a nonzero component_count, got %+v", stage, row)
			}
		default:
			if row.ComponentCount != 0 || row.EventCount != 0 {
				t.Fatalf("unobserved canonical stage %q must be a real zero, got %+v", stage, row)
			}
		}
	}
	custom, ok := byStage["vendor_custom_stage"]
	if !ok {
		t.Fatalf("non-canonical observed stage must still be surfaced, not silently dropped: %+v", response.Data)
	}
	if custom.ComponentCount != 1 || custom.EventCount != 1 {
		t.Fatalf("vendor_custom_stage row = %+v, want component=1 event=1", custom)
	}
	if response.Population.Denominator == 0 {
		t.Fatalf("expected nonzero denominator (installed count): %+v", response.Population)
	}

	// A component kind with nothing installed must report unknown, not zero.
	empty, err := ComponentLifecycleFunnel(ctx, pool, "mcp", base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("ComponentLifecycleFunnel (mcp, no data): %v", err)
	}
	if empty.Completeness.Status != "unknown" {
		t.Fatalf("completeness = %+v, want unknown when nothing of that kind was installed", empty.Completeness)
	}
}

// TestReliabilityCoverageTimelineGroupsOverlappingIntervalsBySourceDayStatus
// proves the timeline groups completeness_intervals correctly by overlap
// (not exact containment), and that watermark freshness is reported as
// genuinely unknown (zero time) when no source_watermarks rows exist yet.
func TestReliabilityCoverageTimelineGroupsOverlappingIntervalsBySourceDayStatus(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	insertInterval := func(id, sourceInstanceID, status string, start, end time.Time) {
		scope := `{"source_instance_id": "` + sourceInstanceID + `"}`
		if _, err := pool.Exec(ctx, `INSERT INTO completeness_intervals (completeness_interval_id, dimension_scope, interval_start, interval_end, status) VALUES ($1, $2::jsonb, $3, $4, $5)`,
			id, scope, start, end, status); err != nil {
			t.Fatalf("insert completeness_interval: %v", err)
		}
	}
	// Fully inside range.
	insertInterval("ci_1", "src_alpha", "complete", base.Add(time.Hour), base.Add(2*time.Hour))
	// Overlaps the range boundary (starts before `from`, ends inside range) --
	// must still be counted since the query filters by overlap, not containment.
	insertInterval("ci_2", "src_alpha", "partial", base.Add(-time.Hour), base.Add(time.Hour))
	// Fully outside range (ends before `from`).
	insertInterval("ci_3", "src_alpha", "complete", base.AddDate(0, 0, -2), base.AddDate(0, 0, -1))
	// Different source, degraded status.
	insertInterval("ci_4", "src_bravo", "degraded", base.Add(3*time.Hour), base.Add(4*time.Hour))

	response, err := ReliabilityCoverageTimeline(ctx, pool, base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("ReliabilityCoverageTimeline: %v", err)
	}
	if len(response.Data) != 3 {
		t.Fatalf("expected 3 grouped rows (ci_3 excluded), got %d: %+v", len(response.Data), response.Data)
	}
	var sawComplete, sawPartial, sawDegraded bool
	for _, row := range response.Data {
		if row.SourceInstanceID == "src_alpha" && row.Status == "complete" {
			sawComplete = true
		}
		if row.SourceInstanceID == "src_alpha" && row.Status == "partial" {
			sawPartial = true
		}
		if row.SourceInstanceID == "src_bravo" && row.Status == "degraded" {
			sawDegraded = true
		}
	}
	if !sawComplete || !sawPartial || !sawDegraded {
		t.Fatalf("expected complete/partial/degraded rows to all be present, got %+v", response.Data)
	}
	if response.Population.Denominator != 3 {
		t.Fatalf("denominator = %d, want 3 (total overlapping intervals)", response.Population.Denominator)
	}
	if response.Population.Numerator != 1 {
		t.Fatalf("numerator = %d, want 1 (only the fully-inside complete interval)", response.Population.Numerator)
	}
	// No source_watermarks rows exist yet: freshness must be honestly
	// reported as unknown (zero time), never fabricated as "now" or "complete".
	if !response.Freshness.RollupWatermark.IsZero() {
		t.Fatalf("expected zero-value watermark when no source_watermarks rows exist, got %v", response.Freshness.RollupWatermark)
	}
}

// TestMCPTopologyReturnsParentChildTreeWithLatestConnectionState proves the
// one genuinely new dedicated route (ADR 0013 decision #12): MCP server
// components with their bundled children and each server's most recent
// observed connection state within range, restricted to kind='mcp' only,
// and that only opaque IDs/enum states are ever returned.
func TestMCPTopologyReturnsParentChildTreeWithLatestConnectionState(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartition(ctx, pool, "mcp_connections", base); err != nil {
		t.Fatalf("ensure partition: %v", err)
	}

	insertComponent(t, ctx, pool, "comp_mcp_parent", "mcp")
	insertComponent(t, ctx, pool, "comp_mcp_child_tool", "skill")
	insertComponent(t, ctx, pool, "comp_mcp_lonely", "mcp") // mcp with no children, no connection observed.
	if _, err := pool.Exec(ctx, `INSERT INTO component_relations (relation_id, parent_id, child_id, relation_kind) VALUES ($1, $2, $3, 'bundles')`,
		"rel_1", "comp_mcp_parent", "comp_mcp_child_tool"); err != nil {
		t.Fatalf("insert component_relation: %v", err)
	}

	insertConnection := func(id, componentID, state string, observedAt time.Time) {
		if _, err := pool.Exec(ctx, `INSERT INTO mcp_connections (mcp_connection_id, observed_at, component_id, state) VALUES ($1, $2, $3, $4)`,
			id, observedAt, componentID, state); err != nil {
			t.Fatalf("insert mcp_connection: %v", err)
		}
	}
	insertConnection("conn_old", "comp_mcp_parent", "connected", base.Add(time.Minute))
	insertConnection("conn_new", "comp_mcp_parent", "disconnected", base.Add(10*time.Minute))
	// Outside range: must not become the "latest" state.
	insertConnection("conn_future", "comp_mcp_parent", "connected", base.AddDate(0, 0, 5))

	response, err := MCPTopology(ctx, pool, base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("MCPTopology: %v", err)
	}
	if response.FormulaVersion != FormulaVersionMCPTopology1 {
		t.Fatalf("formula_version = %q, want %q", response.FormulaVersion, FormulaVersionMCPTopology1)
	}
	byID := make(map[string]ComponentTreeNode, len(response.Data))
	for _, node := range response.Data {
		byID[node.ComponentID] = node
		if node.Kind != "mcp" {
			t.Fatalf("non-mcp component leaked into topology: %+v", node)
		}
	}
	if len(byID) != 2 {
		t.Fatalf("expected exactly 2 mcp nodes, got %d: %+v", len(byID), response.Data)
	}
	parent, ok := byID["comp_mcp_parent"]
	if !ok {
		t.Fatalf("missing comp_mcp_parent node: %+v", response.Data)
	}
	if len(parent.ChildComponentIDs) != 1 || parent.ChildComponentIDs[0] != "comp_mcp_child_tool" {
		t.Fatalf("parent children = %+v, want [comp_mcp_child_tool]", parent.ChildComponentIDs)
	}
	if parent.LatestConnectionState != "disconnected" {
		t.Fatalf("latest_connection_state = %q, want disconnected (most recent in-range observation)", parent.LatestConnectionState)
	}
	if parent.ConnectionObservedAt == nil {
		t.Fatalf("expected a non-nil connection_observed_at for a component with observed connections")
	}
	lonely, ok := byID["comp_mcp_lonely"]
	if !ok {
		t.Fatalf("missing comp_mcp_lonely node: %+v", response.Data)
	}
	if len(lonely.ChildComponentIDs) != 0 {
		t.Fatalf("comp_mcp_lonely should have zero children, got %+v", lonely.ChildComponentIDs)
	}
	if lonely.LatestConnectionState != "" || lonely.ConnectionObservedAt != nil {
		t.Fatalf("comp_mcp_lonely has no observed connection in range and must not fabricate one: %+v", lonely)
	}
	if response.Population.Numerator != 1 || response.Population.Denominator != 2 {
		t.Fatalf("population = %+v, want numerator=1 (has state) denominator=2 (total mcp nodes)", response.Population)
	}
}
