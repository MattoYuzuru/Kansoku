package observability

import (
	"bytes"
	"encoding/json"
	"testing"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"

	"kansoku.local/kansoku/internal/claudeadapter"
	"kansoku.local/kansoku/internal/codexadapter"
)

// realResource builds an OTLP resource carrying only the real, documented
// upstream resource identity attributes (service.name) a locally-installed
// Codex CLI or Claude Code process actually stamps -- never the Session 03
// fixture-agent's kansoku.adapter.version/kansoku.source.schema synthetic
// attributes, and never any kansoku.*-namespaced attribute, since a real
// agent never sends one.
func realResource(serviceName string) *resourcev1.Resource {
	return &resourcev1.Resource{Attributes: []*commonv1.KeyValue{stringKV("service.name", serviceName)}}
}

// realLogRequest builds one ExportLogsServiceRequest shaped exactly like a
// real (non-fixture) adapter's OTel exporter: the resource carries only the
// real service.name identity, the record's instrumentation scope name is the
// documented native OTel event name, and every attribute is a real, native
// attribute name (never a kansoku.*-namespaced one) -- proving the dispatch
// path this test exercises never depends on the adapter pre-translating its
// own wire format into Kansoku's internal shape.
func realLogRequest(serviceName, scopeName string, attributes []*commonv1.KeyValue) *collectorlogsv1.ExportLogsServiceRequest {
	attributes = append([]*commonv1.KeyValue{stringKV("event.name", scopeName)}, attributes...)
	record := &logsv1.LogRecord{ObservedTimeUnixNano: uint64(fixedTime.UnixNano()), Attributes: attributes}
	scope := &logsv1.ScopeLogs{Scope: &commonv1.InstrumentationScope{Name: scopeName}, LogRecords: []*logsv1.LogRecord{record}}
	return &collectorlogsv1.ExportLogsServiceRequest{ResourceLogs: []*logsv1.ResourceLogs{{Resource: realResource(serviceName), ScopeLogs: []*logsv1.ScopeLogs{scope}}}}
}

// TestRealCodexOTelPayloadLandsAsRealEventEndToEnd proves TDD 11 section A
// step 7's first requirement: a real (non-fixture) Codex OTel payload, built
// from the documented native shape (service.name="codex_cli_rs",
// conversation.id as the real upstream attribute name -- never
// kansoku.session.id), lands as a real events row through
// OTLPReceiver.ingestLogs end to end, using
// codexadapter.OTLPResourceServiceName/NativeOTLPAttributeSafeSlot, never a
// second, parallel identity literal.
func TestRealCodexOTelPayloadLandsAsRealEventEndToEnd(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	attributes := []*commonv1.KeyValue{
		stringKV(string(codexadapter.NativeAttributeConversationID), "real-codex-conversation-001"),
	}
	request := realLogRequest(codexadapter.OTLPResourceServiceName, string(codexadapter.OTelConversationStarts), attributes)
	if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
		t.Fatalf("real codex payload rejected: %v", err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 || len(state.Quarantine) != 0 || len(state.Incidents) != 0 {
		t.Fatalf("state=%+v", state)
	}
	for _, fact := range state.Facts {
		if fact.Event.EventType != "session.started" {
			t.Fatalf("event_type=%s", fact.Event.EventType)
		}
	}
}

// TestRealClaudeOTelPayloadLandsAsRealEventEndToEnd is the Claude Code
// sibling of TestRealCodexOTelPayloadLandsAsRealEventEndToEnd: a real
// (non-fixture) OTel payload shaped exactly like Claude Code's documented
// wire format (service.name="claude-code", session.id/tool_name/tool_status
// as the real upstream attribute names -- never kansoku.*-namespaced ones)
// lands as a real events row end to end, using
// claudeadapter.OTLPResourceServiceName/NativeOTLPAttributeSafeSlot.
func TestRealClaudeOTelPayloadLandsAsRealEventEndToEnd(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	attributes := []*commonv1.KeyValue{
		stringKV(string(claudeadapter.NativeAttributeSessionID), "real-claude-session-001"),
		stringKV(string(claudeadapter.NativeAttributeToolName), "Bash"),
		stringKV(string(claudeadapter.NativeAttributeToolState), "true"),
		intKV(string(claudeadapter.NativeAttributeDuration), 42),
	}
	request := realLogRequest(claudeadapter.OTLPResourceServiceName, string(claudeadapter.OTelToolResult), attributes)
	if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
		t.Fatalf("real claude payload rejected: %v", err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 || len(state.Quarantine) != 0 || len(state.Incidents) != 0 {
		t.Fatalf("state=%+v", state)
	}
	for _, fact := range state.Facts {
		if fact.Event.EventType != "tool.called" {
			t.Fatalf("event_type=%s", fact.Event.EventType)
		}
		if fact.Event.Outcome != "succeeded" {
			t.Fatalf("outcome=%s", fact.Event.Outcome)
		}
	}
	encoded, _ := json.Marshal(state)
	for _, prohibited := range []string{"conversation.id", "skill.name", "plugin.name", "agent.name"} {
		if bytes.Contains(encoded, []byte(prohibited)) {
			t.Fatalf("native attribute name %q leaked into durable state", prohibited)
		}
	}
}

// TestRealClaudeComponentAttributesTranslateOntoToolIDSlot proves the
// skill.name/plugin.name/agent.name identity-and-component attributes
// contracts/claude/hooks-and-otel.yaml's documented_attributes block
// declares are actually translated onto the existing kansoku.tool.id slot by
// claudeadapter.ComponentAttributeSafeSlot (previously dead code never
// called from any production path), rather than being rejected as an unsafe
// attribute or silently dropped.
func TestRealClaudeComponentAttributesTranslateOntoToolIDSlot(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	attributes := []*commonv1.KeyValue{
		stringKV(string(claudeadapter.NativeAttributeSessionID), "real-claude-session-002"),
		stringKV(string(claudeadapter.AttributeSkillName), "commit-helper"),
	}
	request := realLogRequest(claudeadapter.OTLPResourceServiceName, string(claudeadapter.OTelSkillActivated), attributes)
	if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
		t.Fatalf("real claude skill.name payload rejected: %v", err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 {
		t.Fatalf("facts=%d", len(state.Facts))
	}
	found := false
	for _, fact := range state.Facts {
		if fact.Event.Subject.ComponentID == "commit-helper" && fact.Event.Subject.Kind == "skill" {
			found = true
		}
	}
	if !found {
		t.Fatal("skill.name did not translate onto the component/tool.id slot")
	}
}

func TestDocumentedNonTerminalCodexSSEIsMetadataNotSchemaDrift(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	request := realLogRequest(
		codexadapter.OTLPResourceServiceName,
		string(codexadapter.OTelSSEEvent),
		[]*commonv1.KeyValue{
			stringKV(string(codexadapter.NativeAttributeConversationID), "real-codex-conversation-002"),
		},
	)
	if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
		t.Fatalf("documented non-terminal SSE rejected: %v", err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 || len(state.Quarantine) != 0 || len(state.Incidents) != 0 {
		t.Fatalf("state=%+v", state)
	}
	for _, fact := range state.Facts {
		if fact.Event.EventType != "source.observed" {
			t.Fatalf("event_type=%s, want source.observed", fact.Event.EventType)
		}
	}
}

// TestUnrecognizedResourceStillQuarantinesAfterAdapterDispatch is the Gap A
// regression test TDD section A step 7 requires: a resource matching neither
// the fixture-agent identity nor any registered adapter's MatchesOTLPResource
// must still fall through to the existing unknown()/IngestUnknown quarantine
// path exactly as before adapter-aware dispatch was added -- the gap closes
// by recognizing more real traffic, never by weakening what happens to
// genuinely unrecognized traffic.
func TestUnrecognizedResourceStillQuarantinesAfterAdapterDispatch(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	request := realLogRequest("some-other-cli-tool", "some.unrecognized.event", []*commonv1.KeyValue{stringKV("random.attribute", "value")})
	err := receiver.ingestLogs(request, SourceOTLPLog)
	if err != nil {
		t.Fatalf("durable non-retryable quarantine rejected: %v", err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 0 || len(state.Quarantine) != 1 || len(state.Incidents) != 1 {
		t.Fatalf("state=%+v", state)
	}
	if state.Watermarks[string(SourceOTLPLog)].Lifecycle != SourceDegraded {
		t.Fatal("source not degraded")
	}
}

func TestMixedOTLPBatchQuarantinesUnknownAndDurablyProcessesFollowingClaudeRecords(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	unknown := realLogRequest(
		"unreviewed-agent", "future.event",
		[]*commonv1.KeyValue{stringKV("unsafe.body", "discarded")},
	)
	skill := realLogRequest(
		claudeadapter.OTLPResourceServiceName, string(claudeadapter.OTelSkillActivated),
		[]*commonv1.KeyValue{
			stringKV(string(claudeadapter.NativeAttributeSessionID), "mixed-session"),
			stringKV(string(claudeadapter.AttributeSkillName), "plugin-skill"),
			stringKV(string(claudeadapter.AttributePluginName), "owner-plugin"),
			stringKV(string(claudeadapter.AttributeInvocationTrigger), "user-slash"),
			stringKV(string(claudeadapter.AttributeSkillSource), "plugin"),
		},
	)
	plugin := realLogRequest(
		claudeadapter.OTLPResourceServiceName, string(claudeadapter.OTelPluginLoaded),
		[]*commonv1.KeyValue{
			stringKV(string(claudeadapter.NativeAttributeSessionID), "mixed-session"),
			stringKV(string(claudeadapter.AttributePluginName), "owner-plugin"),
			stringKV(string(claudeadapter.AttributePluginScope), "user"),
			stringKV(string(claudeadapter.AttributePluginIDHash), "opaque-upstream-id"),
		},
	)
	request := &collectorlogsv1.ExportLogsServiceRequest{}
	request.ResourceLogs = append(request.ResourceLogs, unknown.ResourceLogs...)
	request.ResourceLogs = append(request.ResourceLogs, skill.ResourceLogs...)
	request.ResourceLogs = append(request.ResourceLogs, plugin.ResourceLogs...)
	if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
		t.Fatalf("mixed batch rejected: %v", err)
	}
	state := store.Snapshot()
	if len(state.Quarantine) != 1 || len(state.Incidents) != 1 || len(state.Facts) != 2 {
		t.Fatalf("quarantine=%d incidents=%d facts=%d", len(state.Quarantine), len(state.Incidents), len(state.Facts))
	}
	seen := map[string]Event{}
	for _, fact := range state.Facts {
		seen[fact.Event.EventType] = fact.Event
	}
	skillEvent := seen["component.invoked"]
	if skillEvent.Subject.Kind != "skill" ||
		skillEvent.ComponentEvidence.QualifiedIdentity != "owner-plugin:plugin-skill" ||
		skillEvent.ComponentEvidence.InvocationMode != "explicit" {
		t.Fatalf("skill metadata=%+v subject=%+v", skillEvent.ComponentEvidence, skillEvent.Subject)
	}
	pluginEvent := seen["component.loaded"]
	if pluginEvent.Subject.Kind != "plugin" ||
		pluginEvent.ComponentEvidence.QualifiedIdentity != "owner-plugin" ||
		pluginEvent.ComponentEvidence.UpstreamIdentityHash == "" {
		t.Fatalf("plugin metadata=%+v subject=%+v", pluginEvent.ComponentEvidence, pluginEvent.Subject)
	}
	encoded, _ := json.Marshal(state)
	for _, raw := range []string{"discarded", "opaque-upstream-id", "mixed-session"} {
		if bytes.Contains(encoded, []byte(raw)) {
			t.Fatalf("raw OTLP value persisted: %q", raw)
		}
	}
}

func TestClaudeSkillInvocationModesAndPluginOwnership(t *testing.T) {
	for _, test := range []struct {
		trigger string
		want    string
	}{
		{"user-slash", "explicit"},
		{"claude-proactive", "proactive"},
		{"nested-skill", "nested"},
	} {
		t.Run(test.want, func(t *testing.T) {
			store, ingestor, _ := testIngestor(t, 4<<20)
			receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
			request := realLogRequest(
				claudeadapter.OTLPResourceServiceName,
				string(claudeadapter.OTelSkillActivated),
				[]*commonv1.KeyValue{
					stringKV(string(claudeadapter.NativeAttributeSessionID), "mode-session"),
					stringKV(string(claudeadapter.AttributeSkillName), "shared-name"),
					stringKV(string(claudeadapter.AttributePluginName), "plugin-owner"),
					stringKV(string(claudeadapter.AttributeInvocationTrigger), test.trigger),
					stringKV(string(claudeadapter.AttributeSkillSource), "plugin"),
				},
			)
			if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
				t.Fatal(err)
			}
			for _, fact := range store.Snapshot().Facts {
				if fact.Event.ComponentEvidence.InvocationMode != test.want ||
					fact.Event.ComponentEvidence.OwnerPluginIdentity != "plugin-owner" ||
					fact.Event.ComponentEvidence.QualifiedIdentity != "plugin-owner:shared-name" {
					t.Fatalf("component metadata=%+v", fact.Event.ComponentEvidence)
				}
			}
		})
	}
}

// TestClaude2_1_220SkillActivatedWireShapeQualifiesOwnerOnce replays the exact
// skill_activated record captured from Claude Code 2.1.220 on 2026-08-01
// (reports/artifacts/2026-08-01-component-audit). Unlike the bare-name cases
// above -- which are kept unchanged as the backward-compatibility proof --
// this shape already carries its owner on skill.name, and prepending
// plugin.name a second time produced
// "sre-agent:sre-agent:verification-strategy", which no inventory row can ever
// equal.
//
// skill.source is asserted to survive verbatim: "plugin" is not a member of
// adaptersdk.SourceScope, and the ingress must not repair, coerce or drop it
// here. Classifying it is the data platform's job, and it deliberately widens
// rather than narrows resolution there.
func TestClaude2_1_220SkillActivatedWireShapeQualifiesOwnerOnce(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	request := realLogRequest(
		claudeadapter.OTLPResourceServiceName,
		string(claudeadapter.OTelSkillActivated),
		[]*commonv1.KeyValue{
			stringKV(string(claudeadapter.NativeAttributeSessionID), "wire-capture-session"),
			stringKV(string(claudeadapter.AttributeSkillName), "sre-agent:verification-strategy"),
			stringKV(string(claudeadapter.AttributePluginName), "sre-agent"),
			stringKV(string(claudeadapter.AttributeInvocationTrigger), "claude-proactive"),
			stringKV(string(claudeadapter.AttributeSkillSource), "plugin"),
			// Emitted upstream, mapped onto no safe slot, and therefore
			// dropped -- asserted below never to reach a durable record.
			stringKV("marketplace.name", "yuzuru-engineering"),
		},
	)
	if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
		t.Fatalf("2.1.220 skill_activated rejected: %v", err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 || len(state.Quarantine) != 0 {
		t.Fatalf("facts=%d quarantine=%d", len(state.Facts), len(state.Quarantine))
	}
	for _, fact := range state.Facts {
		evidence := fact.Event.ComponentEvidence
		if fact.Event.EventType != "component.invoked" || fact.Event.Subject.Kind != "skill" {
			t.Fatalf("event_type=%s kind=%s", fact.Event.EventType, fact.Event.Subject.Kind)
		}
		if evidence.QualifiedIdentity != "sre-agent:verification-strategy" {
			t.Fatalf("qualified identity=%q want single owner prefix", evidence.QualifiedIdentity)
		}
		if evidence.OwnerPluginIdentity != "sre-agent" {
			t.Fatalf("owner=%q", evidence.OwnerPluginIdentity)
		}
		if evidence.SourceScope != "plugin" {
			t.Fatalf("source scope=%q want the raw upstream value", evidence.SourceScope)
		}
		if evidence.InvocationMode != "proactive" {
			t.Fatalf("invocation mode=%q", evidence.InvocationMode)
		}
	}
	encoded, _ := json.Marshal(state)
	if bytes.Contains(encoded, []byte("yuzuru-engineering")) {
		t.Fatal("dropped marketplace.name reached a durable record")
	}
}

// TestClaudeMetadataOnlyEventsAreDeclaredAndNeverQuarantine covers the two
// events Claude Code 2.1.220 emits that this recipe had never declared:
// hook_registered (every session start) and assistant_response (every
// assistant turn). Undeclared, each one quarantined as unsupported adapter
// drift, so both produced standing incident noise about a shape that had
// simply never been written down.
//
// Both map to source.observed. assistant_response deliberately does not map to
// model.responded: api_request already counts that same operation, and
// counting both would double every model response.
func TestClaudeMetadataOnlyEventsAreDeclaredAndNeverQuarantine(t *testing.T) {
	for _, name := range []claudeadapter.OTelEventName{
		claudeadapter.OTelHookRegistered, claudeadapter.OTelAssistantResponse,
	} {
		t.Run(string(name), func(t *testing.T) {
			store, ingestor, _ := testIngestor(t, 4<<20)
			receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
			request := realLogRequest(
				claudeadapter.OTLPResourceServiceName, string(name),
				[]*commonv1.KeyValue{
					stringKV(string(claudeadapter.NativeAttributeSessionID), "declared-session"),
				},
			)
			if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
				t.Fatalf("%s rejected: %v", name, err)
			}
			state := store.Snapshot()
			if len(state.Quarantine) != 0 || len(state.Incidents) != 0 {
				t.Fatalf("%s quarantined: quarantine=%d incidents=%d",
					name, len(state.Quarantine), len(state.Incidents))
			}
			if len(state.Facts) != 1 {
				t.Fatalf("%s facts=%d want 1", name, len(state.Facts))
			}
			for _, fact := range state.Facts {
				if fact.Event.EventType != "source.observed" {
					t.Fatalf("%s event_type=%s want source.observed", name, fact.Event.EventType)
				}
			}
		})
	}
}

// TestClaudeUnrecognisedInvocationTriggerIsRecordedNotDropped proves an
// invocation_trigger outside the known vocabulary is preserved as the distinct
// state "unknown". It used to be dropped by a bare `continue`, which made a
// future Claude Code trigger addition indistinguishable from a skill
// invocation that reported no trigger at all -- AGENTS.md keeps "unknown" and
// "not_observed" as separate states precisely so drift stays visible.
func TestClaudeUnrecognisedInvocationTriggerIsRecordedNotDropped(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	request := realLogRequest(
		claudeadapter.OTLPResourceServiceName,
		string(claudeadapter.OTelSkillActivated),
		[]*commonv1.KeyValue{
			stringKV(string(claudeadapter.NativeAttributeSessionID), "drift-session"),
			stringKV(string(claudeadapter.AttributeSkillName), "owner:drifting-skill"),
			stringKV(string(claudeadapter.AttributePluginName), "owner"),
			stringKV(string(claudeadapter.AttributeInvocationTrigger), "future-trigger-shape"),
		},
	)
	if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
		t.Fatalf("unrecognised trigger rejected the whole record: %v", err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 {
		t.Fatalf("facts=%d want 1", len(state.Facts))
	}
	for _, fact := range state.Facts {
		if mode := fact.Event.ComponentEvidence.InvocationMode; mode != "unknown" {
			t.Fatalf("invocation mode=%q want unknown", mode)
		}
	}
	encoded, _ := json.Marshal(state)
	if bytes.Contains(encoded, []byte("future-trigger-shape")) {
		t.Fatal("raw trigger value persisted verbatim")
	}
}
