//go:build postgres_integration

package dataplatform

import (
	"context"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/observability"
)

func TestObservabilityHandoffPersistsNativeTelemetryProjectionsIdempotently(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	handoff, err := NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("NewObservabilityHandoff: %v", err)
	}

	observedAt := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	turnID := "trn_native_prompt_01"
	sessionID := "ses_native_01"
	duration := int64(420)
	promptCharacters := int64(37)
	inputTokens := int64(120)
	outputTokens := int64(35)
	costMicros := int64(875)
	success := true

	events := []observability.Event{
		nativeProjectionEvent("evt_native_prompt_01", "prompt.submitted", observedAt, sessionID, turnID),
		nativeProjectionEvent("evt_native_tool_01", "tool.called", observedAt.Add(time.Second), sessionID, turnID),
		nativeProjectionEvent("evt_native_model_request_01", "model.requested", observedAt.Add(2*time.Second), sessionID, turnID),
		nativeProjectionEvent("evt_native_model_01", "model.responded", observedAt.Add(3*time.Second), sessionID, turnID),
	}
	events[0].Measurements.PromptCharacterCount = &promptCharacters
	events[1].Subject = observability.Subject{Kind: "tool", ComponentID: "exec_command"}
	events[1].Measurements.DurationMS = &duration
	events[1].Measurements.Success = &success
	events[1].Outcome = "succeeded"
	events[2].Subject.ModelID = "gpt-5.6-terra"
	events[2].Measurements.DurationMS = &duration
	events[2].Outcome = "succeeded"
	events[3].Subject.ModelID = "gpt-5.6-terra"
	events[3].Measurements.InputTokens = &inputTokens
	events[3].Measurements.OutputTokens = &outputTokens
	events[3].Measurements.ProviderCostMicros = &costMicros
	events[3].Measurements.Success = &success
	events[3].Outcome = "succeeded"

	for _, event := range events {
		evidence := nativeProjectionEvidence(event)
		if err := handoff.PersistNormalizedFact(event, evidence); err != nil {
			t.Fatalf("PersistNormalizedFact(%s): %v", event.EventType, err)
		}
		if err := handoff.PersistNormalizedFact(event, evidence); err != nil {
			t.Fatalf("replay PersistNormalizedFact(%s): %v", event.EventType, err)
		}
	}

	ctx := context.Background()
	assertCount := func(table string, want int64) {
		t.Helper()
		var got int64
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s count = %d, want %d", table, got, want)
		}
	}
	assertCount("events", 4)
	assertCount("turns", 1)
	assertCount("prompt_features", 1)
	assertCount("tool_calls", 1)
	assertCount("model_operations", 2)
	assertCount("token_usage", 1)
	assertCount("source_watermarks", 1)

	var gotCharacters int64
	if err := pool.QueryRow(ctx, `SELECT prompt_character_count FROM prompt_features`).Scan(&gotCharacters); err != nil {
		t.Fatalf("read prompt characters: %v", err)
	}
	if gotCharacters != promptCharacters {
		t.Fatalf("prompt characters = %d, want %d", gotCharacters, promptCharacters)
	}

	var gotDuration int64
	var gotOutcome, gotKind string
	if err := pool.QueryRow(ctx, `
		SELECT tc.duration_ms, tc.outcome, c.kind
		FROM tool_calls tc JOIN components c ON c.component_id = tc.component_id
	`).Scan(&gotDuration, &gotOutcome, &gotKind); err != nil {
		t.Fatalf("read tool projection: %v", err)
	}
	if gotDuration != duration || gotOutcome != "succeeded" || gotKind != "command" {
		t.Fatalf("tool projection = (%d,%q,%q), want (%d,%q,%q)", gotDuration, gotOutcome, gotKind, duration, "succeeded", "command")
	}

	var gotInput, gotOutput, gotCost int64
	var gotProvider string
	if err := pool.QueryRow(ctx, `
		SELECT tu.input_tokens, tu.output_tokens, mo.provider_cost_micros, m.provider_id
		FROM token_usage tu
		JOIN model_operations mo
		  ON mo.model_operation_id = tu.model_operation_id AND mo.observed_at = tu.observed_at
		JOIN models m ON m.model_id = mo.model_id
	`).Scan(&gotInput, &gotOutput, &gotCost, &gotProvider); err != nil {
		t.Fatalf("read model projection: %v", err)
	}
	if gotInput != inputTokens || gotOutput != outputTokens || gotCost != costMicros || gotProvider != "openai" {
		t.Fatalf("model projection = (%d,%d,%d,%q), want (%d,%d,%d,%q)",
			gotInput, gotOutput, gotCost, gotProvider, inputTokens, outputTokens, costMicros, "openai")
	}

	var gotModelDuration int64
	var gotOperationKind string
	if err := pool.QueryRow(ctx, `
		SELECT duration_ms, operation_kind
		FROM model_operations
		WHERE operation_kind = 'request'
	`).Scan(&gotModelDuration, &gotOperationKind); err != nil {
		t.Fatalf("read model request projection: %v", err)
	}
	if gotModelDuration != duration || gotOperationKind != "request" {
		t.Fatalf("model request projection = (%d,%q), want (%d,%q)",
			gotModelDuration, gotOperationKind, duration, "request")
	}

	var lastSequence int64
	var lastCommitted *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT last_committed_at, last_emitted_sequence FROM source_watermarks
	`).Scan(&lastCommitted, &lastSequence); err != nil {
		t.Fatalf("read watermark: %v", err)
	}
	if lastCommitted == nil || lastSequence != 4 {
		t.Fatalf("watermark = (%v,%d), want committed timestamp and sequence 4", lastCommitted, lastSequence)
	}
}

func nativeProjectionEvent(eventID, eventType string, observedAt time.Time, sessionID, turnID string) observability.Event {
	sequence := uint64(1)
	switch eventType {
	case "tool.called":
		sequence = 2
	case "model.responded":
		sequence = 4
	case "model.requested":
		sequence = 3
	}
	return observability.Event{
		SpecVersion: observability.EventSpecVersion,
		EventID:     eventID, FactKey: "fact_" + eventID, EventType: eventType,
		EmittedAt: observedAt, ObservedAt: observedAt, IngestedAt: observedAt.Add(time.Second),
		TimestampQuality: "source_rfc3339",
		Source: observability.SourceRef{
			AdapterID: "codex", AdapterVersion: "0.145.0", Kind: observability.SourceOTLPLog,
			SchemaID: "codex.otel/1", SchemaFingerprint: "schema_native_01",
			InstallationID: "ain_native_01", NativeEventID: "native_" + eventID, Sequence: sequence,
		},
		Scope: observability.Scope{
			DeviceID: "dev_native_01", AgentInstallationID: "ain_native_01",
			SurfaceID: "surface_native_01", ProjectID: "project_native_01",
			SessionID: sessionID, TurnID: turnID,
		},
		ValueState: "observed", Outcome: "unknown",
		CorrelationStatus: observability.CorrelationExact,
	}
}

func nativeProjectionEvidence(event observability.Event) observability.Evidence {
	return observability.Evidence{
		EvidenceID: "evd_" + event.EventID, EventID: event.EventID, Source: event.Source,
		Tier: observability.TierNative, Confidence: 1, Completeness: observability.Complete,
		FirstSeenAt: event.IngestedAt, LastSeenAt: event.IngestedAt,
		Sanitizer: "kansoku.ingress-sanitizer/1", PrivacySHA256: "privacy-test",
		Assertion: observability.EvidenceAssertion{
			EventType: event.EventType, Outcome: event.Outcome, ValueState: event.ValueState,
		},
	}
}
