//go:build postgres_integration

package dataplatform

import (
	"context"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/observability"
)

func metricSample(id, name, dimension string, value int64, observedAt time.Time) observability.MetricSample {
	stored := value
	return observability.MetricSample{
		SampleID: id, MetricName: name, Unit: "tokens", ValueInt: &stored,
		ObservedAt: observedAt, IngestedAt: observedAt.Add(time.Second),
		AdapterID: "claude", AdapterVersion: "1.0.0",
		SourceKind: observability.SourceOTLPMetric, SchemaFingerprint: "metric_schema_01",
		SessionID: "ses_metric_01", ModelID: "gpt-5.6-terra", Dimension: dimension,
		IdempotencyKey: "idem_" + id,
	}
}

func TestMetricSamplesPersistIdempotentlyAndKeepBothValueKinds(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	handoff, err := NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("NewObservabilityHandoff: %v", err)
	}
	observedAt := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)

	counter := metricSample("mts_tokens_01", "claude_code.token.usage", "input", 4_294_967_296, observedAt)
	cost := observability.MetricSample{
		SampleID: "mts_cost_01", MetricName: "claude_code.cost.usage", Unit: "USD",
		ObservedAt: observedAt, IngestedAt: observedAt.Add(time.Second),
		AdapterID: "claude", AdapterVersion: "1.0.0",
		SourceKind: observability.SourceOTLPMetric, SchemaFingerprint: "metric_schema_01",
		SessionID: "ses_metric_01", IdempotencyKey: "idem_mts_cost_01",
	}
	costValue := 1.25
	cost.ValueDouble = &costValue

	for _, sample := range []observability.MetricSample{counter, cost} {
		if err := handoff.PersistMetricSample(sample); err != nil {
			t.Fatalf("PersistMetricSample(%s): %v", sample.MetricName, err)
		}
		// The OTLP exporter resends after any 5xx. A replay must not double a
		// cost or a token total.
		if err := handoff.PersistMetricSample(sample); err != nil {
			t.Fatalf("replay PersistMetricSample(%s): %v", sample.MetricName, err)
		}
	}

	ctx := context.Background()
	var rowCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM metric_samples`).Scan(&rowCount); err != nil {
		t.Fatalf("count metric_samples: %v", err)
	}
	if rowCount != 2 {
		t.Fatalf("metric_samples=%d, want 2 after a replay of each", rowCount)
	}

	var storedInt int64
	var doubleIsNull bool
	if err := pool.QueryRow(ctx, `
		SELECT value_int, value_double IS NULL FROM metric_samples WHERE metric_name = $1
	`, "claude_code.token.usage").Scan(&storedInt, &doubleIsNull); err != nil {
		t.Fatalf("read token metric: %v", err)
	}
	if storedInt != 4_294_967_296 || !doubleIsNull {
		t.Errorf("token sample = %d (double null=%v); a counter above 2^32 must survive exactly and not become a float",
			storedInt, doubleIsNull)
	}

	var storedDouble float64
	var intIsNull bool
	if err := pool.QueryRow(ctx, `
		SELECT value_double, value_int IS NULL FROM metric_samples WHERE metric_name = $1
	`, "claude_code.cost.usage").Scan(&storedDouble, &intIsNull); err != nil {
		t.Fatalf("read cost metric: %v", err)
	}
	if storedDouble != 1.25 || !intIsNull {
		t.Errorf("cost sample = %v (int null=%v)", storedDouble, intIsNull)
	}
}

func TestMetricReconciliationReportsTheGapAsANumberNotASilence(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	handoff, err := NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("NewObservabilityHandoff: %v", err)
	}
	ctx := context.Background()
	observedAt := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)

	// Fact lane: one api_request-derived model response with token counts.
	inputTokens, outputTokens := int64(1000), int64(250)
	event := nativeProjectionEvent("evt_metric_recon_01", "model.responded", observedAt, "ses_metric_recon", "trn_metric_recon")
	event.Subject.ModelID = "gpt-5.6-terra"
	event.Measurements.InputTokens = &inputTokens
	event.Measurements.OutputTokens = &outputTokens
	event.Outcome = "succeeded"
	if err := handoff.PersistNormalizedFact(event, nativeProjectionEvidence(event)); err != nil {
		t.Fatalf("PersistNormalizedFact: %v", err)
	}

	// Metric lane: the agent's own counter, deliberately ahead by 40 input
	// tokens. That gap is exactly what this query exists to surface.
	if err := handoff.PersistMetricSample(
		metricSample("mts_recon_in", "claude_code.token.usage", "input", 1040, observedAt)); err != nil {
		t.Fatalf("PersistMetricSample(input): %v", err)
	}
	if err := handoff.PersistMetricSample(
		metricSample("mts_recon_out", "claude_code.token.usage", "output", 250, observedAt)); err != nil {
		t.Fatalf("PersistMetricSample(output): %v", err)
	}

	response, err := MetricReconciliation(ctx, pool, observedAt.Add(-time.Hour), observedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("MetricReconciliation: %v", err)
	}
	byMeasurement := make(map[string]MetricReconciliationRow, len(response.Data))
	for _, row := range response.Data {
		byMeasurement[row.Measurement] = row
	}

	input := byMeasurement["input_tokens"]
	if input.State != "observed" {
		t.Fatalf("input_tokens state=%q, want observed when both lanes reported", input.State)
	}
	if input.MetricValue != 1040 || input.FactValue != 1000 || input.Difference != 40 {
		t.Errorf("input_tokens metric=%d fact=%d difference=%d, want 1040/1000/40",
			input.MetricValue, input.FactValue, input.Difference)
	}

	output := byMeasurement["output_tokens"]
	if output.Difference != 0 || output.State != "observed" {
		t.Errorf("output_tokens difference=%d state=%q, want an exact agreement", output.Difference, output.State)
	}

	// Cost was never reported by either lane here. Agreement and absence are
	// different facts, and a zero difference would claim the first.
	cost := byMeasurement["provider_cost_micros"]
	if cost.State != "not_observed" {
		t.Errorf("provider_cost_micros state=%q, want not_observed when neither lane reported", cost.State)
	}

	if response.Population.Denominator != int64(len(response.Data)) {
		t.Errorf("denominator=%d, want %d", response.Population.Denominator, len(response.Data))
	}
	if response.Exclusions["measurement_reported_by_one_lane_only"] == 0 {
		t.Error("a measurement only one lane reported must appear in exclusions")
	}
	if response.Completeness.Status == "complete" {
		t.Errorf("completeness=%q, want partial while some measurements are single-lane",
			response.Completeness.Status)
	}
}
