//go:build postgres_integration

// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema work.
package dataplatform

import (
	"context"
	"testing"
	"time"
)

// TestToolAnalyticsAggregatesCallsAndFiltersByComponentWithinRangeAndBudget
// proves ToolAnalytics groups tool_calls by calendar day, computes an exact
// (not averaged) p95 latency, restricts to one component_id when given (and
// selects every component when componentID is ""), respects the half-open
// [from, to) boundary, and returns within its registered budget.
func TestToolAnalyticsAggregatesCallsAndFiltersByComponentWithinRangeAndBudget(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartition(ctx, pool, "tool_calls", base); err != nil {
		t.Fatalf("ensure partition: %v", err)
	}
	insertComponent(t, ctx, pool, "comp_tool_a", "mcp")
	insertComponent(t, ctx, pool, "comp_tool_b", "skill")

	insertCall := func(id, componentID, outcome string, observedAt time.Time, durationMS int64) {
		if _, err := pool.Exec(ctx, `INSERT INTO tool_calls (tool_call_id, observed_at, component_id, duration_ms, outcome) VALUES ($1, $2, $3, $4, $5)`,
			id, observedAt, componentID, durationMS, outcome); err != nil {
			t.Fatalf("insert tool_call: %v", err)
		}
	}
	durations := []int64{100, 150, 200, 250, 300}
	for i, d := range durations {
		insertCall("tc_ta_"+string(rune('a'+i)), "comp_tool_a", "succeeded", base.Add(time.Duration(i)*time.Minute), d)
	}
	insertCall("tc_ta_fail", "comp_tool_a", "failed", base.Add(10*time.Minute), 50)
	insertCall("tc_ta_other", "comp_tool_b", "succeeded", base.Add(time.Minute), 999)
	// Outside range: must never leak in.
	insertCall("tc_ta_out", "comp_tool_a", "succeeded", base.AddDate(0, 0, 2), 111)

	to := base.AddDate(0, 0, 1)
	started := time.Now()
	response, err := ToolAnalytics(ctx, pool, "comp_tool_a", base, to)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("ToolAnalytics: %v", err)
	}
	if elapsed > time.Duration(Budgets["tool_analytics_range"].MaxMS)*time.Millisecond {
		t.Fatalf("tool_analytics_range exceeded its budget: %v", elapsed)
	}
	if response.FormulaVersion != FormulaVersionToolAnalytics1 {
		t.Fatalf("formula_version = %q, want %q", response.FormulaVersion, FormulaVersionToolAnalytics1)
	}
	if len(response.Data) != 1 {
		t.Fatalf("expected exactly 1 day row (out-of-range row must not leak in), got %d: %+v", len(response.Data), response.Data)
	}
	day := response.Data[0]
	if day.CallCount != 6 || day.SuccessCount != 5 || day.FailureCount != 1 {
		t.Fatalf("day row = %+v, want call=6 success=5 failure=1 (comp_tool_b must be excluded)", day)
	}
	if day.Percentiles == nil || day.Percentiles.P95 == nil {
		t.Fatalf("expected a computed p95 percentile, got %+v", day.Percentiles)
	}
	wantP95 := exactPercentile(append(append([]int64{}, durations...), 50), 0.95)
	if diff := *day.Percentiles.P95 - wantP95; diff > 0.001 || diff < -0.001 {
		t.Fatalf("p95 = %v, want exact %v", *day.Percentiles.P95, wantP95)
	}
	if response.Population.Numerator != 5 || response.Population.Denominator != 6 {
		t.Fatalf("population = %+v, want numerator=5 denominator=6", response.Population)
	}
	if response.Completeness.Status == "unknown" {
		t.Fatalf("completeness should not be unknown when data is present: %+v", response.Completeness)
	}

	// Empty-string componentID selects every component.
	all, err := ToolAnalytics(ctx, pool, "", base, to)
	if err != nil {
		t.Fatalf("ToolAnalytics (all components): %v", err)
	}
	if len(all.Data) != 1 {
		t.Fatalf("expected 1 day row across all components, got %d: %+v", len(all.Data), all.Data)
	}
	if all.Data[0].CallCount != 7 {
		t.Fatalf("all-components call_count = %d, want 7 (6 comp_tool_a + 1 comp_tool_b)", all.Data[0].CallCount)
	}
}

// TestToolAnalyticsEmptyRangeReportsUnknownNotZero proves the "no silent
// zero" convention: an empty range must report completeness "unknown", not a
// fabricated "complete" with empty data.
func TestToolAnalyticsEmptyRangeReportsUnknownNotZero(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartition(ctx, pool, "tool_calls", base); err != nil {
		t.Fatalf("ensure partition: %v", err)
	}
	response, err := ToolAnalytics(ctx, pool, "", base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("ToolAnalytics: %v", err)
	}
	if len(response.Data) != 0 {
		t.Fatalf("expected zero rows, got %d: %+v", len(response.Data), response.Data)
	}
	if response.Completeness.Status != "unknown" {
		t.Fatalf("completeness = %+v, want unknown for empty range", response.Completeness)
	}
}
