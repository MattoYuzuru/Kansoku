//go:build postgres_integration

// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema work.
package dataplatform

import (
	"context"
	"testing"
	"time"
)

// TestPromptShapeComputesExactByteLengthPercentilesWithinRangeAndBudget
// proves PromptShape groups prompt_features by calendar day, computes an
// exact (not averaged) percentile_cont over prompt_size_bytes excluding null
// sizes from the percentile but not from the count, respects the half-open
// [from, to) boundary, and returns within its registered budget.
func TestPromptShapeComputesExactByteLengthPercentilesWithinRangeAndBudget(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	if err := EnsureDimensions(ctx, pool, testDimensionRefs("src_prompt_shape")); err != nil {
		t.Fatalf("ensure dimensions: %v", err)
	}

	sizes := []int64{100, 200, 300, 400, 500}
	for i, size := range sizes {
		id := "pf_shape_" + string(rune('a'+i))
		if _, err := pool.Exec(ctx, `
			INSERT INTO prompt_features (prompt_feature_id, turn_id, observed_at, prompt_size_bytes, value_state)
			VALUES ($1, 'turn_fixture', $2, $3, 'observed')
		`, id, base.Add(time.Duration(i)*time.Minute), size); err != nil {
			t.Fatalf("insert prompt_feature: %v", err)
		}
	}
	// A prompt with a null size still counts toward prompt_count but is
	// excluded from the percentile computation.
	if _, err := pool.Exec(ctx, `
		INSERT INTO prompt_features (prompt_feature_id, turn_id, observed_at, prompt_size_bytes, value_state)
		VALUES ('pf_shape_null', 'turn_fixture', $1, NULL, 'redacted')
	`, base.Add(10*time.Minute)); err != nil {
		t.Fatalf("insert null-size prompt_feature: %v", err)
	}
	// Outside range: must never leak in.
	if _, err := pool.Exec(ctx, `
		INSERT INTO prompt_features (prompt_feature_id, turn_id, observed_at, prompt_size_bytes, value_state)
		VALUES ('pf_shape_out', 'turn_fixture', $1, 999999, 'observed')
	`, base.AddDate(0, 0, 2)); err != nil {
		t.Fatalf("insert out-of-range prompt_feature: %v", err)
	}

	to := base.AddDate(0, 0, 1)
	started := time.Now()
	response, err := PromptShape(ctx, pool, base, to)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("PromptShape: %v", err)
	}
	if elapsed > time.Duration(Budgets["prompt_shape_range"].MaxMS)*time.Millisecond {
		t.Fatalf("prompt_shape_range exceeded its budget: %v", elapsed)
	}
	if response.FormulaVersion != FormulaVersionPromptShape1 {
		t.Fatalf("formula_version = %q, want %q", response.FormulaVersion, FormulaVersionPromptShape1)
	}
	if len(response.Data) != 1 {
		t.Fatalf("expected exactly 1 day row (out-of-range row must not leak in), got %d: %+v", len(response.Data), response.Data)
	}
	day := response.Data[0]
	if day.PromptCount != 6 {
		t.Fatalf("prompt_count = %d, want 6 (5 sized + 1 null-size)", day.PromptCount)
	}
	if day.Percentiles == nil || day.Percentiles.P50 == nil {
		t.Fatalf("expected computed percentiles, got %+v", day.Percentiles)
	}
	wantP50 := exactPercentile(sizes, 0.50)
	if diff := *day.Percentiles.P50 - wantP50; diff > 0.001 || diff < -0.001 {
		t.Fatalf("p50 = %v, want exact %v (null-size row must be excluded)", *day.Percentiles.P50, wantP50)
	}
	if response.Population.Denominator != 6 {
		t.Fatalf("population denominator = %d, want 6", response.Population.Denominator)
	}
	if response.Completeness.Status == "unknown" {
		t.Fatalf("completeness should not be unknown when data is present: %+v", response.Completeness)
	}
}

// TestPromptShapeEmptyRangeReportsUnknownNotZero proves the "no silent zero"
// convention: an empty range must report completeness "unknown", not a
// fabricated "complete" with empty data.
func TestPromptShapeEmptyRangeReportsUnknownNotZero(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	response, err := PromptShape(ctx, pool, base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("PromptShape: %v", err)
	}
	if len(response.Data) != 0 {
		t.Fatalf("expected zero rows, got %d: %+v", len(response.Data), response.Data)
	}
	if response.Completeness.Status != "unknown" {
		t.Fatalf("completeness = %+v, want unknown for empty range", response.Completeness)
	}
}
