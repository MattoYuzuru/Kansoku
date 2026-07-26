//go:build postgres_integration

// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema work.
package dataplatform

import (
	"context"
	"testing"
	"time"
)

// TestModelUsageAggregatesTokensCostAndMatchedLatencyWithinRangeAndBudget
// proves ModelUsage sums response/token/cost volume per calendar day across
// multiple model_operations rows, that a separate request phase contributes
// to Percentiles/ErrorRatio without double-counting request volume, respects
// the half-open [from, to) boundary, and returns within its registered budget.
func TestModelUsageAggregatesTokensCostAndMatchedLatencyWithinRangeAndBudget(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for _, table := range []string{"model_operations", "token_usage"} {
		if err := EnsurePartition(ctx, pool, table, base); err != nil {
			t.Fatalf("ensure partition %s: %v", table, err)
		}
	}
	insertProviderAndModel(t, ctx, pool, "prov_model_usage", "model_usage_a")

	insertOp := func(id string, observedAt time.Time, inputTokens, outputTokens, costMicros int64) {
		if _, err := pool.Exec(ctx, `INSERT INTO model_operations (model_operation_id, observed_at, model_id) VALUES ($1, $2, 'model_usage_a')`, id, observedAt); err != nil {
			t.Fatalf("insert model_operation: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO token_usage (token_usage_id, observed_at, model_operation_id, input_tokens, output_tokens) VALUES ($1, $2, $3, $4, $5)`,
			"tu_"+id, observedAt, id, inputTokens, outputTokens); err != nil {
			t.Fatalf("insert token_usage: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO price_catalog_versions (price_catalog_version_id, model_id, effective_at, input_price_micros, output_price_micros) VALUES ($1, 'model_usage_a', $2, 1, 1) ON CONFLICT DO NOTHING`,
			"pcv_"+id, base); err != nil {
			t.Fatalf("insert price_catalog_version: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO cost_estimates (cost_estimate_id, token_usage_id, price_catalog_version_id, cost_micros) VALUES ($1, $2, $3, $4)`,
			"ce_"+id, "tu_"+id, "pcv_"+id, costMicros); err != nil {
			t.Fatalf("insert cost_estimate: %v", err)
		}
	}

	// Two response phases are the volume/token denominator.
	insertOp("mop_usage_1", base.Add(time.Minute), 100, 50, 1000)
	if _, err := pool.Exec(ctx, `
		INSERT INTO model_operations (
			model_operation_id, observed_at, model_id, operation_kind, duration_ms, outcome
		) VALUES ('mop_usage_request_1', $1, 'model_usage_a', 'request', 250, 'succeeded')
	`, base.Add(30*time.Second)); err != nil {
		t.Fatalf("insert request observation: %v", err)
	}

	// Op 2 has no corresponding duration observation.
	insertOp("mop_usage_2", base.Add(2*time.Minute), 10, 5, 100)

	// Outside range: must never leak in.
	insertOp("mop_usage_out", base.AddDate(0, 0, 2), 999, 999, 999)

	to := base.AddDate(0, 0, 1)
	started := time.Now()
	response, err := ModelUsage(ctx, pool, base, to, DefaultTimeBucketSpec())
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("ModelUsage: %v", err)
	}
	if elapsed > time.Duration(Budgets["model_usage_range"].MaxMS)*time.Millisecond {
		t.Fatalf("model_usage_range exceeded its budget: %v", elapsed)
	}
	if response.FormulaVersion != FormulaVersionModelUsage1 {
		t.Fatalf("formula_version = %q, want %q", response.FormulaVersion, FormulaVersionModelUsage1)
	}
	if len(response.Data) != 1 {
		t.Fatalf("expected exactly 1 day row (out-of-range row must not leak in), got %d: %+v", len(response.Data), response.Data)
	}
	day := response.Data[0]
	if day.RequestCount != 2 {
		t.Fatalf("request_count = %d, want 2", day.RequestCount)
	}
	if day.TotalTokens != (100+50)+(10+5) {
		t.Fatalf("total_tokens = %d, want %d", day.TotalTokens, (100+50)+(10+5))
	}
	if day.EstimatedCostMicros != 1000+100 {
		t.Fatalf("estimated_cost_micros = %d, want %d", day.EstimatedCostMicros, 1000+100)
	}
	if day.CostedRequestCount != 2 {
		t.Fatalf("costed_request_count = %d, want 2", day.CostedRequestCount)
	}
	if day.MatchedEventCount != 1 {
		t.Fatalf("matched_event_count = %d, want 1", day.MatchedEventCount)
	}
	if day.Percentiles == nil || day.Percentiles.P50 == nil {
		t.Fatalf("expected computed percentiles from the request observation, got %+v", day.Percentiles)
	}
	if *day.Percentiles.P50 != 250 {
		t.Fatalf("p50 = %v, want 250 (the single request observation's duration)", *day.Percentiles.P50)
	}
	if response.Population.Denominator == 0 {
		t.Fatalf("expected a nonzero denominator when requests are present: %+v", response.Population)
	}
	if response.Completeness.Status == "unknown" {
		t.Fatalf("completeness should not be unknown when data is present: %+v", response.Completeness)
	}
}

// TestModelUsageEmptyRangeReportsUnknownNotZero proves the "no silent zero"
// convention: an empty range must report completeness "unknown", not a
// fabricated "complete" with empty data.
func TestModelUsageEmptyRangeReportsUnknownNotZero(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartition(ctx, pool, "model_operations", base); err != nil {
		t.Fatalf("ensure partition: %v", err)
	}
	response, err := ModelUsage(ctx, pool, base, base.AddDate(0, 0, 1), DefaultTimeBucketSpec())
	if err != nil {
		t.Fatalf("ModelUsage: %v", err)
	}
	if len(response.Data) != 0 {
		t.Fatalf("expected zero rows, got %d: %+v", len(response.Data), response.Data)
	}
	if response.Completeness.Status != "unknown" {
		t.Fatalf("completeness = %+v, want unknown for empty range", response.Completeness)
	}
}
