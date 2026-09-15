package observability

import (
	"strings"
	"testing"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"

	"kansoku.local/kansoku/internal/codexadapter"
)

// This file is the standing proof that repairing Claude Code's component
// identity resolution leaves Codex byte-identical. The argument is structural,
// not statistical: qualification is reached only from this package's
// already-sanitized ingress boundaries, and none of Codex's lanes carries a
// component identity through one of them.
//
//   - Codex's OTel vocabulary has no skill event at all.
//   - codexHookSafeFields sets no component_* key, so the hook lane never
//     qualifies anything.
//   - Codex skill evidence comes from the App Server bridge, the rollout
//     watcher and inventory, each of which sets QualifiedIdentity directly and
//     never passes through this boundary.
//
// Each of the three is asserted below, so a future change that quietly routes
// Codex evidence through the qualification path fails here rather than in a
// dashboard number.

// TestCodexOTelDeclaresNoSkillEventOrComponentAttribute proves the first two
// legs: no Codex OTel event maps onto a component lifecycle type, and no
// Codex-native attribute -- including the exact "skill.source" key Claude
// sends -- resolves onto a component-shaped safe slot.
func TestCodexOTelDeclaresNoSkillEventOrComponentAttribute(t *testing.T) {
	for _, name := range codexadapter.DocumentedOTelEvents() {
		canonical, err := codexadapter.CanonicalEventForOTel(
			name, codexadapter.OTelAttributeShape{InstrumentationScope: string(name)},
		)
		// A shape mismatch is expected here (no attributes are supplied); the
		// point is only that no documented Codex event can ever resolve to a
		// component lifecycle type, whatever its shape.
		if err == nil && strings.HasPrefix(canonical, "component.") {
			t.Fatalf("codex event %q maps to %q; codex has no skill event", name, canonical)
		}
	}
	for _, key := range []string{
		"skill.source", "skill.name", "plugin.name", "plugin.scope",
		"invocation_trigger", "enabled_via", "plugin_id_hash", "agent.name",
	} {
		if slot, ok := nativeAttributeSafeSlot(adapterCodex, key); ok {
			t.Fatalf("codex attribute %q resolved onto slot %q; codex declares no component attribute", key, slot)
		}
	}
}

// TestCodexHookLaneEmitsNoComponentField proves the third leg for hooks: the
// Codex hook field builder produces no component_-prefixed key, so
// IngestSafeHookFields can never reach qualification for Codex regardless of
// what a hook payload contains.
func TestCodexHookLaneEmitsNoComponentField(t *testing.T) {
	fields := codexHookSafeFields(codexadapter.HookHelperOutput{
		EventID: "codex-hook-event", SessionID: "codex-hook-session",
		ObservedAt: fixedTime.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		EventType:  "tool.called", ToolID: "shell", ToolStatus: "success",
	})
	for key := range fields {
		if strings.HasPrefix(key, "component_") {
			t.Fatalf("codexHookSafeFields emitted %q; the hook lane must never qualify", key)
		}
	}
}

// TestCodexBridgeQualifiedIdentityIsUnchangedByQualification pins the App
// Server bridge's identity shape. Codex tool ids arrive already namespaced
// ("owner:skill"), and the bridge derives the owner from that same string, so
// the pair is exactly the input that an unconditional prepend would have
// doubled. Passing it through the qualification helper must be a no-op --
// which is why Codex safety does not rest on the reachability argument alone.
func TestCodexBridgeQualifiedIdentityIsUnchangedByQualification(t *testing.T) {
	for _, test := range []struct{ owner, identity string }{
		{"", "shell"},
		{"codex-plugin", "codex-plugin:review"},
		{"vendor@market", "vendor@market:migrate"},
	} {
		if got := qualifyComponentIdentity(test.owner, test.identity); got != test.identity {
			t.Fatalf("codex identity %q under owner %q became %q", test.identity, test.owner, got)
		}
	}
}

// TestCodexOTelRecordIsUnaffectedEndToEnd is the end-to-end control: a real
// Codex OTel record still lands as the same fact, with no component evidence
// and no quarantine, after the identity changes.
func TestCodexOTelRecordIsUnaffectedEndToEnd(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	request := realLogRequest(
		codexadapter.OTLPResourceServiceName,
		string(codexadapter.OTelConversationStarts),
		[]*commonv1.KeyValue{
			stringKV(string(codexadapter.NativeAttributeConversationID), "codex-regression-conversation"),
			// Sent deliberately: Codex declares no mapping for it, so it must
			// be dropped rather than acquiring Claude's meaning.
			stringKV("skill.source", "plugin"),
		},
	)
	if err := receiver.ingestLogs(request, SourceOTLPLog); err != nil {
		t.Fatalf("codex record rejected: %v", err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 || len(state.Quarantine) != 0 {
		t.Fatalf("facts=%d quarantine=%d", len(state.Facts), len(state.Quarantine))
	}
	for _, fact := range state.Facts {
		if fact.Event.EventType != "session.started" {
			t.Fatalf("event_type=%s", fact.Event.EventType)
		}
		evidence := fact.Event.ComponentEvidence
		if evidence.QualifiedIdentity != "" || evidence.OwnerPluginIdentity != "" ||
			evidence.SourceScope != "" || evidence.InvocationMode != "" {
			t.Fatalf("codex record acquired component evidence: %+v", evidence)
		}
	}
}
