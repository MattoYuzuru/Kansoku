//go:build postgres_integration

// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema work.
package dataplatform

import (
	"context"
	"testing"
	"time"
)

// TestMCPUptimeComputesConnectedRatioOverObservableWindowWithinBudget proves
// MCPUptime restricts to kind='mcp' components, computes ConnectedSeconds
// as the real span spent in the 'connected' state between consecutive
// observations, reports a nil UptimeRatio for a component with fewer than
// two observations (never a fabricated 0% or 100%), respects the half-open
// [from, to) boundary, and returns within its registered budget.
func TestMCPUptimeComputesConnectedRatioOverObservableWindowWithinBudget(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartition(ctx, pool, "mcp_connections", base); err != nil {
		t.Fatalf("ensure partition: %v", err)
	}
	insertComponent(t, ctx, pool, "comp_uptime_mcp", "mcp")
	insertComponent(t, ctx, pool, "comp_uptime_lonely", "mcp")
	insertComponent(t, ctx, pool, "comp_uptime_skill", "skill")

	insertConnection := func(id, componentID, state string, observedAt time.Time) {
		if _, err := pool.Exec(ctx, `INSERT INTO mcp_connections (mcp_connection_id, observed_at, component_id, state) VALUES ($1, $2, $3, $4)`,
			id, observedAt, componentID, state); err != nil {
			t.Fatalf("insert mcp_connection: %v", err)
		}
	}
	// comp_uptime_mcp: connected for 10 minutes then disconnected for 5 minutes.
	insertConnection("mc_up_1", "comp_uptime_mcp", "connected", base.Add(1*time.Minute))
	insertConnection("mc_up_2", "comp_uptime_mcp", "disconnected", base.Add(11*time.Minute))
	insertConnection("mc_up_3", "comp_uptime_mcp", "connected", base.Add(16*time.Minute))
	// comp_uptime_lonely: exactly one observation -- observable window unknown.
	insertConnection("mc_up_lonely", "comp_uptime_lonely", "connected", base.Add(2*time.Minute))
	// A non-mcp component's connection rows must never appear (kind filter).
	insertConnection("mc_up_skill", "comp_uptime_skill", "connected", base.Add(1*time.Minute))
	// Outside range: must never leak in.
	insertConnection("mc_up_out", "comp_uptime_mcp", "connected", base.AddDate(0, 0, 2))

	to := base.AddDate(0, 0, 1)
	started := time.Now()
	response, err := MCPUptime(ctx, pool, base, to)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("MCPUptime: %v", err)
	}
	if elapsed > time.Duration(Budgets["mcp_uptime_range"].MaxMS)*time.Millisecond {
		t.Fatalf("mcp_uptime_range exceeded its budget: %v", elapsed)
	}
	if response.FormulaVersion != FormulaVersionMCPUptime1 {
		t.Fatalf("formula_version = %q, want %q", response.FormulaVersion, FormulaVersionMCPUptime1)
	}
	byID := make(map[string]MCPUptimeRow, len(response.Data))
	for _, row := range response.Data {
		byID[row.ComponentID] = row
	}
	if _, ok := byID["comp_uptime_skill"]; ok {
		t.Fatalf("non-mcp component leaked into uptime response: %+v", response.Data)
	}
	mcp, ok := byID["comp_uptime_mcp"]
	if !ok {
		t.Fatalf("missing comp_uptime_mcp row: %+v", response.Data)
	}
	if mcp.UptimeRatio == nil {
		t.Fatalf("expected a computed uptime_ratio for comp_uptime_mcp, got nil")
	}
	wantObservable := 15 * time.Minute.Seconds()
	if diff := mcp.ObservableSeconds - wantObservable; diff > 0.5 || diff < -0.5 {
		t.Fatalf("observable_seconds = %v, want ~%v", mcp.ObservableSeconds, wantObservable)
	}
	wantConnected := 10 * time.Minute.Seconds()
	if diff := mcp.ConnectedSeconds - wantConnected; diff > 0.5 || diff < -0.5 {
		t.Fatalf("connected_seconds = %v, want ~%v", mcp.ConnectedSeconds, wantConnected)
	}
	lonely, ok := byID["comp_uptime_lonely"]
	if !ok {
		t.Fatalf("missing comp_uptime_lonely row: %+v", response.Data)
	}
	if lonely.UptimeRatio != nil {
		t.Fatalf("expected nil uptime_ratio for a component with a single observation, got %v", *lonely.UptimeRatio)
	}
	if response.Population.Numerator != 1 || response.Population.Denominator != 2 {
		t.Fatalf("population = %+v, want numerator=1 (has ratio) denominator=2 (total mcp components)", response.Population)
	}
}

// TestMCPUptimeEmptyRangeReportsUnknownNotZero proves the "no silent zero"
// convention: no mcp components/observations at all must report
// completeness "unknown", not a fabricated "complete" with empty data.
func TestMCPUptimeEmptyRangeReportsUnknownNotZero(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartition(ctx, pool, "mcp_connections", base); err != nil {
		t.Fatalf("ensure partition: %v", err)
	}
	response, err := MCPUptime(ctx, pool, base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("MCPUptime: %v", err)
	}
	if len(response.Data) != 0 {
		t.Fatalf("expected zero rows, got %d: %+v", len(response.Data), response.Data)
	}
	if response.Completeness.Status != "unknown" {
		t.Fatalf("completeness = %+v, want unknown for empty range", response.Completeness)
	}
}
