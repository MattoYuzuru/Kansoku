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
