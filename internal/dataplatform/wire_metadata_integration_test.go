//go:build postgres_integration

package dataplatform

import (
	"context"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/observability"
)

// Migration 0018 opened five storage surfaces for wire metadata that used to
// be discarded, and none of them had a single data-platform test. These tests
// assert the two properties that matter for each surface: the value the agent
// reported actually lands in its column, and a value the agent did not report
// lands as NULL rather than as a fabricated zero or empty string.

func TestToolDecisionLandsInItsOwnLaneWithBothProvenances(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	handoff, err := NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("NewObservabilityHandoff: %v", err)
	}
	observedAt := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)
	sessionID, turnID := "ses_wire_01", "trn_wire_01"

	decision := nativeProjectionEvent("evt_wire_decision_01", "tool.decided", observedAt, sessionID, turnID)
	decision.Subject = observability.Subject{Kind: "tool", ComponentID: "exec_command"}
	decision.Activity.ToolDecision = "accept"
	decision.Activity.ToolDecisionSource = "config"
	decision.Activity.ToolSource = "builtin"
	decision.Activity.ToolUsePseudonym = "hmac-sha256:wire01"

	call := nativeProjectionEvent("evt_wire_call_01", "tool.called", observedAt.Add(time.Second), sessionID, turnID)
	call.Subject = observability.Subject{Kind: "tool", ComponentID: "exec_command"}
	call.Activity.ToolUsePseudonym = "hmac-sha256:wire01"
	call.Activity.ToolSource = "builtin"
	call.Outcome = "succeeded"

	for _, event := range []observability.Event{decision, call} {
		evidence := nativeProjectionEvidence(event)
		if err := handoff.PersistNormalizedFact(event, evidence); err != nil {
			t.Fatalf("PersistNormalizedFact(%s): %v", event.EventType, err)
		}
		if err := handoff.PersistNormalizedFact(event, evidence); err != nil {
			t.Fatalf("replay PersistNormalizedFact(%s): %v", event.EventType, err)
		}
	}

	ctx := context.Background()
	var (
		decisionValue, decisionState, decisionSource, toolSource string
		storedTurn, componentID                                  string
	)
	if err := pool.QueryRow(ctx, `
		SELECT decision, decision_state, decision_source, tool_source,
		       coalesce(turn_id, ''), coalesce(component_id, '')
		FROM tool_decisions
	`).Scan(&decisionValue, &decisionState, &decisionSource, &toolSource,
		&storedTurn, &componentID); err != nil {
		t.Fatalf("read tool_decisions: %v", err)
	}
	if decisionValue != "accept" || decisionState != "observed" {
		t.Errorf("decision=%q state=%q, want accept/observed", decisionValue, decisionState)
	}
	// The regression this pins: decision_source used to be fed from
	// tool_source, so every decision read "builtin" and no decision could
	// ever be attributed to configuration.
	if decisionSource != "config" {
		t.Errorf("decision_source=%q, want config (where the answer came from)", decisionSource)
	}
	if toolSource != "builtin" {
		t.Errorf("tool_source=%q, want builtin (where the tool came from)", toolSource)
	}
	if storedTurn != turnID {
		t.Errorf("turn_id=%q, want %q: a decision without a turn cannot be placed in a conversation", storedTurn, turnID)
	}
	if componentID != "exec_command" {
		t.Errorf("component_id=%q, want exec_command: otherwise which tool was decided on is unanswerable", componentID)
	}

	// A decision is not an execution. tool_calls must still hold exactly one
	// row -- the tool_result -- joinable back to the decision.
	var callCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tool_calls`).Scan(&callCount); err != nil {
		t.Fatalf("count tool_calls: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("tool_calls=%d, want exactly 1: a decision must never inflate the execution count", callCount)
	}
	var joined int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM tool_calls c
		JOIN tool_decisions d ON d.tool_use_pseudonym = c.tool_use_pseudonym
	`).Scan(&joined); err != nil {
		t.Fatalf("join decisions to calls: %v", err)
	}
	if joined != 1 {
		t.Errorf("decision-to-call join produced %d rows, want 1", joined)
	}
}

func TestUnreportedDecisionSourceStaysNullRatherThanEmpty(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	handoff, err := NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("NewObservabilityHandoff: %v", err)
	}
	observedAt := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
	event := nativeProjectionEvent("evt_wire_decision_02", "tool.decided", observedAt, "ses_wire_02", "trn_wire_02")
	event.Subject = observability.Subject{Kind: "tool", ComponentID: "exec_command"}
	event.Activity.ToolDecision = "accept"
	if err := handoff.PersistNormalizedFact(event, nativeProjectionEvidence(event)); err != nil {
		t.Fatalf("PersistNormalizedFact: %v", err)
	}
	var sourceIsNull, toolSourceIsNull bool
	if err := pool.QueryRow(context.Background(), `
		SELECT decision_source IS NULL, tool_source IS NULL FROM tool_decisions
	`).Scan(&sourceIsNull, &toolSourceIsNull); err != nil {
		t.Fatalf("read tool_decisions: %v", err)
	}
	if !sourceIsNull || !toolSourceIsNull {
		t.Errorf("decision_source null=%v tool_source null=%v; an unreported provenance must be NULL, not ''",
			sourceIsNull, toolSourceIsNull)
	}
}

func TestUnknownDecisionVocabularyIsRecordedNotCoerced(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	handoff, err := NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("NewObservabilityHandoff: %v", err)
	}
	observedAt := time.Date(2026, 8, 2, 9, 45, 0, 0, time.UTC)
	event := nativeProjectionEvent("evt_wire_decision_03", "tool.decided", observedAt, "ses_wire_03", "trn_wire_03")
	event.Subject = observability.Subject{Kind: "tool", ComponentID: "exec_command"}
	event.Activity.ToolDecision = "deferred_to_operator"
	if err := handoff.PersistNormalizedFact(event, nativeProjectionEvidence(event)); err != nil {
		t.Fatalf("PersistNormalizedFact: %v", err)
	}
	var decision, state string
	if err := pool.QueryRow(context.Background(),
		`SELECT decision, decision_state FROM tool_decisions`).Scan(&decision, &state); err != nil {
		t.Fatalf("read tool_decisions: %v", err)
	}
	if decision != "deferred_to_operator" || state != "unknown" {
		t.Errorf("decision=%q state=%q, want the raw value kept with state unknown", decision, state)
	}
}

func TestHookRegistrationsRecordTheThreeAnnouncedTokens(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	handoff, err := NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("NewObservabilityHandoff: %v", err)
	}
	observedAt := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	event := nativeProjectionEvent("evt_wire_hook_01", "source.observed", observedAt, "ses_wire_04", "")
	event.Activity.HookEvent = "PreToolUse"
	event.Activity.HookType = "command"
	event.Activity.HookSource = "plugin"
	if err := handoff.PersistNormalizedFact(event, nativeProjectionEvidence(event)); err != nil {
		t.Fatalf("PersistNormalizedFact: %v", err)
	}
	var hookEvent, hookType, hookSource string
	if err := pool.QueryRow(context.Background(), `
		SELECT hook_event, hook_type, hook_source FROM hook_registrations
	`).Scan(&hookEvent, &hookType, &hookSource); err != nil {
		t.Fatalf("read hook_registrations: %v", err)
	}
	if hookEvent != "PreToolUse" || hookType != "command" || hookSource != "plugin" {
		t.Errorf("hook row = %q/%q/%q, want PreToolUse/command/plugin", hookEvent, hookType, hookSource)
	}
}

func TestBothCacheMeasurementsAreStoredSeparately(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	handoff, err := NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("NewObservabilityHandoff: %v", err)
	}
	observedAt := time.Date(2026, 8, 2, 11, 0, 0, 0, time.UTC)
	inputTokens, outputTokens := int64(100), int64(20)
	cacheCreation, cacheRead := int64(7), int64(913)

	event := nativeProjectionEvent("evt_wire_model_01", "model.responded", observedAt, "ses_wire_05", "trn_wire_05")
	event.Subject.ModelID = "gpt-5.6-terra"
	event.Measurements.InputTokens = &inputTokens
	event.Measurements.OutputTokens = &outputTokens
	event.Measurements.CacheCreationTokens = &cacheCreation
	event.Measurements.CacheReadTokens = &cacheRead
	event.Outcome = "succeeded"
	if err := handoff.PersistNormalizedFact(event, nativeProjectionEvidence(event)); err != nil {
		t.Fatalf("PersistNormalizedFact: %v", err)
	}

	var storedCreation, storedRead int64
	var cachedInputIsNull bool
	if err := pool.QueryRow(context.Background(), `
		SELECT cache_creation_tokens, cache_read_tokens, cached_input_tokens IS NULL
		FROM token_usage
	`).Scan(&storedCreation, &storedRead, &cachedInputIsNull); err != nil {
		t.Fatalf("read token_usage: %v", err)
	}
	if storedCreation != cacheCreation || storedRead != cacheRead {
		t.Errorf("cache tokens = %d/%d, want %d/%d", storedCreation, storedRead, cacheCreation, cacheRead)
	}
	// The two new measurements must not be summed into the pre-existing
	// column: cached_input_tokens keeps meaning whatever the source called
	// "cached input", and this source reported none.
	if !cachedInputIsNull {
		t.Error("cached_input_tokens was written from the new cache measurements; the distinction is destroyed")
	}
}

func TestResponseLengthLandsOnTheTurnItMeasuredWithoutInventingAModelOperation(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	handoff, err := NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("NewObservabilityHandoff: %v", err)
	}
	observedAt := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	responseCharacters := int64(2048)

	event := nativeProjectionEvent("evt_wire_answer_01", "source.observed", observedAt, "ses_wire_06", "trn_wire_06")
	event.Subject.ModelID = "gpt-5.6-terra"
	event.Measurements.ResponseCharacterCount = &responseCharacters
	event.Activity.QuerySource = "main"
	if err := handoff.PersistNormalizedFact(event, nativeProjectionEvidence(event)); err != nil {
		t.Fatalf("PersistNormalizedFact: %v", err)
	}

	ctx := context.Background()
	var storedCharacters int64
	var storedModel, storedQuerySource string
	if err := pool.QueryRow(ctx, `
		SELECT response_character_count, coalesce(model_id, ''), coalesce(query_source, '')
		FROM turns WHERE turn_id = $1
	`, "trn_wire_06").Scan(&storedCharacters, &storedModel, &storedQuerySource); err != nil {
		t.Fatalf("read turns: %v", err)
	}
	if storedCharacters != responseCharacters {
		t.Errorf("turns.response_character_count=%d, want %d", storedCharacters, responseCharacters)
	}
	if storedModel != "gpt-5.6-terra" || storedQuerySource != "main" {
		t.Errorf("turn context = %q/%q, want gpt-5.6-terra/main", storedModel, storedQuerySource)
	}

	// An assistant answer is not an API request. If it ever became one, every
	// model request count in the product would silently double.
	var operations int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM model_operations`).Scan(&operations); err != nil {
		t.Fatalf("count model_operations: %v", err)
	}
	if operations != 0 {
		t.Errorf("model_operations=%d, want 0: assistant_response must not become a model operation", operations)
	}
}

func TestSessionContextIsFilledOnceAndNeverErased(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	handoff, err := NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("NewObservabilityHandoff: %v", err)
	}
	observedAt := time.Date(2026, 8, 2, 13, 0, 0, 0, time.UTC)
	sessionID := "ses_wire_07"

	first := nativeProjectionEvent("evt_wire_session_01", "source.observed", observedAt, sessionID, "")
	first.Activity.SessionStartType = "fresh"
	first.Activity.TerminalType = "ssh-session"
	first.Scope.UserID = "hmac-sha256:user07"

	// A later event that reports nothing must not blank what was observed.
	second := nativeProjectionEvent("evt_wire_session_02", "source.observed", observedAt.Add(time.Minute), sessionID, "")
	second.Activity.QuerySource = "main"

	for _, event := range []observability.Event{first, second} {
		if err := handoff.PersistNormalizedFact(event, nativeProjectionEvidence(event)); err != nil {
			t.Fatalf("PersistNormalizedFact(%s): %v", event.EventID, err)
		}
	}

	var startType, terminalType, querySource, userPseudonym string
	var safeModeIsNull bool
	if err := pool.QueryRow(context.Background(), `
		SELECT coalesce(start_type, ''), coalesce(terminal_type, ''),
		       coalesce(query_source, ''), coalesce(user_pseudonym, ''),
		       safe_mode IS NULL
		FROM sessions WHERE session_id = $1
	`, sessionID).Scan(&startType, &terminalType, &querySource, &userPseudonym, &safeModeIsNull); err != nil {
		t.Fatalf("read sessions: %v", err)
	}
	if startType != "fresh" || terminalType != "ssh-session" {
		t.Errorf("session context = %q/%q, want fresh/ssh-session", startType, terminalType)
	}
	if querySource != "main" {
		t.Errorf("query_source=%q, want main from the later event", querySource)
	}
	if userPseudonym != "hmac-sha256:user07" {
		t.Errorf("user_pseudonym=%q, want the keyed handle", userPseudonym)
	}
	if !safeModeIsNull {
		t.Error("safe_mode was written although no event reported it")
	}
}

func TestMessageIdentityReachesTheEventRow(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	handoff, err := NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("NewObservabilityHandoff: %v", err)
	}
	observedAt := time.Date(2026, 8, 2, 14, 0, 0, 0, time.UTC)
	event := nativeProjectionEvent("evt_wire_prompt_01", "prompt.submitted", observedAt, "ses_wire_08", "trn_wire_08")
	event.Scope.MessageID = "hmac-sha256:message08"
	if err := handoff.PersistNormalizedFact(event, nativeProjectionEvidence(event)); err != nil {
		t.Fatalf("PersistNormalizedFact: %v", err)
	}
	var messageID string
	if err := pool.QueryRow(context.Background(),
		`SELECT coalesce(message_id, '') FROM events WHERE event_id = $1`,
		"evt_wire_prompt_01").Scan(&messageID); err != nil {
		t.Fatalf("read events: %v", err)
	}
	if messageID != "hmac-sha256:message08" {
		t.Errorf("events.message_id=%q, want the keyed handle", messageID)
	}
}

// The two lanes 0018 introduced are partitioned. If they are missing from
// PartitionedTables the inserts still work -- the handoff creates partitions
// inline -- but retention never drops their old partitions and the backup
// cycle never checks them, so the omission is invisible until storage grows.
func TestNewPartitionedLanesAreRegisteredForRetentionAndBackup(t *testing.T) {
	registered := make(map[string]struct{}, len(PartitionedTables))
	for _, table := range PartitionedTables {
		registered[table] = struct{}{}
	}
	for _, table := range []string{"tool_decisions", "hook_registrations"} {
		if _, ok := registered[table]; !ok {
			t.Errorf("%s is partitioned but absent from PartitionedTables", table)
		}
	}
}
