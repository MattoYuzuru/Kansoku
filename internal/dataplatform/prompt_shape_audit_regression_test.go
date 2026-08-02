//go:build postgres_integration

// Regression tests written by the 2026-08-01 component audit, lane 05
// (prompt metadata). Each test documents a defect that the existing
// prompt_shape_test.go suite does not catch. See
// reports/artifacts/2026-08-01-component-audit/05-prompt-metadata.md.
//
// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema work.
package dataplatform

import (
	"context"
	"testing"
	"time"
)

// TestAuditL05PromptShapeDistinguishesEmptyBucketFromUnobservedBucket
// documents F-05-2. PromptShape's own doc comment promises "one row per
// requested calendar bucket inside the half-open [from, to) range", and the
// /prompts panel must let an operator tell an hour in which zero prompts
// were submitted apart from an hour that was never observed at all
// (AGENTS.md: "unsupported, not_observed, redacted, unknown, and numeric
// zero are separate states").
//
// The query GROUP BYs the truncated timestamp, so a bucket with no rows is
// simply absent from the result. This test asserts the contract, and
// therefore FAILS against the current implementation.
func TestAuditL05PromptShapeDistinguishesEmptyBucketFromUnobservedBucket(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	if err := EnsureDimensions(ctx, pool, testDimensionRefs("src_l05_gap")); err != nil {
		t.Fatalf("ensure dimensions: %v", err)
	}
	// One prompt in hour 00 and one in hour 03. Hours 01 and 02 were
	// observed (the appliance was up and ingesting) but contained no
	// prompts: they are numeric zero, not "unknown".
	for i, offset := range []time.Duration{0, 3 * time.Hour} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO prompt_features (prompt_feature_id, turn_id, observed_at, prompt_size_bytes, prompt_character_count, value_state)
			VALUES ($1, 'turn_fixture', $2, 100, 100, 'observed')
		`, "pf_l05_gap_"+string(rune('a'+i)), base.Add(offset)); err != nil {
			t.Fatalf("insert prompt_feature: %v", err)
		}
	}

	spec, err := NewTimeBucketSpec("hourly", "UTC")
	if err != nil {
		t.Fatalf("bucket spec: %v", err)
	}
	response, err := PromptShape(ctx, pool, base, base.Add(4*time.Hour), spec)
	if err != nil {
		t.Fatalf("PromptShape: %v", err)
	}
	if len(response.Data) != 4 {
		t.Fatalf("expected 4 hourly rows for a 4-hour range (empty buckets must be "+
			"emitted with prompt_count = 0 so they are distinguishable from unobserved "+
			"buckets), got %d rows: %+v", len(response.Data), response.Data)
	}
	for index, row := range response.Data {
		want := base.Add(time.Duration(index) * time.Hour)
		if !row.Day.UTC().Equal(want) {
			t.Fatalf("row %d bucket = %s, want %s", index, row.Day.UTC(), want)
		}
	}
	if response.Data[1].PromptCount != 0 || response.Data[2].PromptCount != 0 {
		t.Fatalf("hours 01 and 02 must report prompt_count = 0, got %d and %d",
			response.Data[1].PromptCount, response.Data[2].PromptCount)
	}
}

// TestAuditL05PromptShapeExposesPercentileExclusions documents F-05-3.
// contracts/metrics.yaml's prompt.utf8_bytes evaluator declares
// "null_policy": "explicit_exclusion_only" and AGENTS.md requires that
// "Dashboard percentages always expose numerator, denominator, exclusions,
// and completeness". PromptShape counts a null-sized prompt toward
// prompt_count but silently drops it from the percentile FILTER, and then
// reports Population{Numerator: total, Denominator: total} — a tautological
// 100% with no exclusion count anywhere in the envelope. An operator cannot
// tell that the p95 was computed from 1 of 6 prompts.
//
// This test asserts that the excluded population is visible, and therefore
// FAILS against the current implementation.
func TestAuditL05PromptShapeExposesPercentileExclusions(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	if err := EnsureDimensions(ctx, pool, testDimensionRefs("src_l05_excl")); err != nil {
		t.Fatalf("ensure dimensions: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO prompt_features (prompt_feature_id, turn_id, observed_at, prompt_size_bytes, prompt_character_count, value_state)
		VALUES ('pf_l05_sized', 'turn_fixture', $1, 100, 100, 'observed')
	`, base); err != nil {
		t.Fatalf("insert sized prompt_feature: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO prompt_features (prompt_feature_id, turn_id, observed_at, prompt_size_bytes, prompt_character_count, value_state)
			VALUES ($1, 'turn_fixture', $2, NULL, NULL, 'not_observed')
		`, "pf_l05_null_"+string(rune('a'+i)), base.Add(time.Duration(i+1)*time.Minute)); err != nil {
			t.Fatalf("insert null prompt_feature: %v", err)
		}
	}

	response, err := PromptShape(ctx, pool, base, base.AddDate(0, 0, 1), DefaultTimeBucketSpec())
	if err != nil {
		t.Fatalf("PromptShape: %v", err)
	}
	if len(response.Data) != 1 {
		t.Fatalf("expected 1 day row, got %d", len(response.Data))
	}
	if response.Data[0].PromptCount != 6 {
		t.Fatalf("prompt_count = %d, want 6", response.Data[0].PromptCount)
	}
	// 1 of 6 prompts actually contributed to the percentile band.
	if response.Population.Numerator == response.Population.Denominator {
		t.Fatalf("population numerator (%d) must be the measured subpopulation and "+
			"denominator (%d) the eligible population; a tautological n==d hides that "+
			"5 of 6 prompts were excluded from the percentile band",
			response.Population.Numerator, response.Population.Denominator)
	}
	if response.Completeness.Status == "complete" {
		t.Fatalf("completeness = %q, want partial/degraded when 5 of 6 prompts carry "+
			"no length measurement", response.Completeness.Status)
	}
}

// TestAuditL05PromptShapeByteBandIsReachableFromTheLivePipeline documents
// F-05-1 at the query level. contracts/dashboard.yaml binds panel
// "prompt-shape" to metric prompt.utf8_bytes (priority "must"), but the only
// production writer of prompt_features
// (internal/dataplatform/observability_handoff.go, case "prompt.submitted")
// hard-codes prompt_size_bytes to NULL, so PromptShape's byte percentiles
// are structurally unreachable in production. This test proves the query
// itself is fine — it is the writer that is broken — by inserting the row
// shape the writer would have to produce. It PASSES today and exists to pin
// the query behaviour once the writer is fixed.
func TestAuditL05PromptShapeByteBandIsReachableFromTheLivePipeline(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	if err := EnsureDimensions(ctx, pool, testDimensionRefs("src_l05_bytes")); err != nil {
		t.Fatalf("ensure dimensions: %v", err)
	}
	// A two-byte-per-rune prompt: 10 runes, 20 UTF-8 bytes. Character and
	// byte percentiles must be different numbers, which is the whole point
	// of keeping both columns.
	if _, err := pool.Exec(ctx, `
		INSERT INTO prompt_features (prompt_feature_id, turn_id, observed_at, prompt_size_bytes, prompt_character_count, value_state)
		VALUES ('pf_l05_utf8', 'turn_fixture', $1, 20, 10, 'observed')
	`, base); err != nil {
		t.Fatalf("insert prompt_feature: %v", err)
	}
	response, err := PromptShape(ctx, pool, base, base.AddDate(0, 0, 1), DefaultTimeBucketSpec())
	if err != nil {
		t.Fatalf("PromptShape: %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].Percentiles == nil ||
		response.Data[0].Percentiles.P95 == nil {
		t.Fatalf("byte percentile band must be present when prompt_size_bytes is "+
			"populated: %+v", response.Data)
	}
	if *response.Data[0].Percentiles.P95 != 20 {
		t.Fatalf("byte p95 = %v, want 20", *response.Data[0].Percentiles.P95)
	}
}
