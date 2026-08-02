package observability

import (
	"bytes"
	"encoding/json"
	"testing"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"

	"kansoku.local/kansoku/internal/claudeadapter"
)

// The 2026-08-01 wire capture
// (reports/artifacts/2026-08-01-component-audit/evidence/plugins/
// 03-claude-otlp-capture-strings.jsonl) showed Claude Code 2.1.220 sending
// roughly thirty non-content attributes of which this ingress read eleven.
// The rest were not rejected or classified -- they simply fell off the edge of
// the mapping table, so a permission decision, a hook registration and both
// cache-token measurements were indistinguishable from an agent that had never
// reported them. These tests pin the attributes that now survive, and pin the
// ones that still must not.

func TestClaudeToolDecisionCarriesDecisionSourceAndCorrelationHandle(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	request := realLogRequest(
		claudeadapter.OTLPResourceServiceName,
		string(claudeadapter.OTelToolDecision),
		[]*commonv1.KeyValue{
			stringKV(string(claudeadapter.NativeAttributeSessionID), "decision-session"),
			stringKV(string(claudeadapter.NativeAttributeToolName), "Bash"),
			stringKV(string(claudeadapter.NativeAttributeToolDecision), "accept"),
			stringKV(string(claudeadapter.NativeAttributeToolSource), "config"),
			stringKV(string(claudeadapter.NativeAttributeToolUseID), "toolu_01abcdef"),
			intKV(string(claudeadapter.NativeAttributeToolInputBytes), 412),
		},
	)
	if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
		t.Fatalf("tool_decision rejected: %v", err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 || len(state.Quarantine) != 0 {
		t.Fatalf("facts=%d quarantine=%d", len(state.Facts), len(state.Quarantine))
	}
	for _, fact := range state.Facts {
		// A decision is its own canonical event. Mapping it onto tool.called
		// would have doubled every executed call; leaving it on
		// source.observed -- where it lived until now -- discarded the answer
		// itself, which is the only thing a decision record contains.
		if fact.Event.EventType != "tool.decided" {
			t.Fatalf("event_type=%q want tool.decided", fact.Event.EventType)
		}
		activity := fact.Event.Activity
		if activity.ToolDecision != "accept" || activity.ToolSource != "config" {
			t.Fatalf("decision=%q source=%q", activity.ToolDecision, activity.ToolSource)
		}
		if activity.ToolUsePseudonym == "" {
			t.Fatal("tool use id produced no correlation handle")
		}
		if fact.Event.Measurements.ToolInputBytes == nil || *fact.Event.Measurements.ToolInputBytes != 412 {
			t.Fatalf("tool input bytes=%v", fact.Event.Measurements.ToolInputBytes)
		}
	}
	// The raw upstream tool-use id is a correlation handle, not a durable
	// value: it must reach the record only as a keyed pseudonym.
	encoded, _ := json.Marshal(state)
	if bytes.Contains(encoded, []byte("toolu_01abcdef")) {
		t.Fatal("raw tool_use_id reached a durable record")
	}
}

func TestClaudeAPIRequestCarriesBothCacheMeasurementsSeparately(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	request := realLogRequest(
		claudeadapter.OTLPResourceServiceName,
		string(claudeadapter.OTelAPIRequest),
		[]*commonv1.KeyValue{
			stringKV(string(claudeadapter.NativeAttributeSessionID), "cache-session"),
			stringKV(string(claudeadapter.NativeAttributeModel), "claude-opus-5"),
			intKV(string(claudeadapter.NativeAttributeInputTokens), 120),
			intKV(string(claudeadapter.NativeAttributeOutputTokens), 340),
			intKV(string(claudeadapter.NativeAttributeCacheCreation), 900),
			intKV(string(claudeadapter.NativeAttributeCacheRead), 1500),
			intKV(string(claudeadapter.NativeAttributeResponseLength), 2048),
		},
	)
	if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
		t.Fatalf("api_request rejected: %v", err)
	}
	for _, fact := range store.Snapshot().Facts {
		measurements := fact.Event.Measurements
		// Two separately reported numbers stay two numbers. Summing them into
		// the pre-existing cached_input_tokens column would have destroyed the
		// only signal that distinguishes writing a cache from reading one.
		if measurements.CacheCreationTokens == nil || *measurements.CacheCreationTokens != 900 {
			t.Fatalf("cache creation=%v", measurements.CacheCreationTokens)
		}
		if measurements.CacheReadTokens == nil || *measurements.CacheReadTokens != 1500 {
			t.Fatalf("cache read=%v", measurements.CacheReadTokens)
		}
		if measurements.ResponseCharacterCount == nil || *measurements.ResponseCharacterCount != 2048 {
			t.Fatalf("response length=%v", measurements.ResponseCharacterCount)
		}
	}
}

func TestClaudeSessionContextAndHookRegistrationSurvive(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	request := realLogRequest(
		claudeadapter.OTLPResourceServiceName,
		string(claudeadapter.OTelHookRegistered),
		[]*commonv1.KeyValue{
			stringKV(string(claudeadapter.NativeAttributeSessionID), "hook-session"),
			stringKV(string(claudeadapter.NativeAttributeHookEvent), "PreToolUse"),
			stringKV(string(claudeadapter.NativeAttributeHookType), "command"),
			stringKV(string(claudeadapter.NativeAttributeHookSource), "settings"),
			stringKV(string(claudeadapter.NativeAttributeStartType), "startup"),
			stringKV(string(claudeadapter.NativeAttributeQuerySource), "cli"),
			stringKV(string(claudeadapter.NativeAttributeTerminalType), "iTerm.app"),
			stringKV(string(claudeadapter.NativeAttributeUserID), "operator-42"),
		},
	)
	if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
		t.Fatalf("hook_registered rejected: %v", err)
	}
	state := store.Snapshot()
	if len(state.Quarantine) != 0 {
		t.Fatalf("quarantine=%d", len(state.Quarantine))
	}
	for _, fact := range state.Facts {
		activity := fact.Event.Activity
		if activity.HookEvent != "PreToolUse" || activity.HookType != "command" ||
			activity.HookSource != "settings" {
			t.Fatalf("hook metadata lost: %+v", activity)
		}
		if activity.SessionStartType != "startup" || activity.QuerySource != "cli" ||
			activity.TerminalType != "iTerm.app" {
			t.Fatalf("session context lost: %+v", activity)
		}
		if fact.Event.Scope.UserID == "" {
			t.Fatal("operator id produced no pseudonym")
		}
	}
	encoded, _ := json.Marshal(state)
	if bytes.Contains(encoded, []byte("operator-42")) {
		t.Fatal("raw user.id reached a durable record")
	}
}

// TestUnshapedActivityValueIsRejectedNotStored proves the widened surface did
// not become a general-purpose string channel: an activity slot still only
// accepts a vocabulary-shaped token, so a path or a sentence cannot ride in
// through one.
func TestUnshapedActivityValueIsRejectedNotStored(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	request := realLogRequest(
		claudeadapter.OTLPResourceServiceName,
		string(claudeadapter.OTelToolDecision),
		[]*commonv1.KeyValue{
			stringKV(string(claudeadapter.NativeAttributeSessionID), "unshaped-session"),
			stringKV(string(claudeadapter.NativeAttributeToolDecision), "/Users/someone/private notes.txt"),
		},
	)
	err := receiver.ingestLogs(request, SourceOTLPLog)
	if err == nil && len(store.Snapshot().Facts) > 0 {
		t.Fatal("an unshaped activity value produced a durable fact")
	}
	encoded, _ := json.Marshal(store.Snapshot())
	if bytes.Contains(encoded, []byte("private notes")) {
		t.Fatal("an unshaped activity value reached a durable record")
	}
}
