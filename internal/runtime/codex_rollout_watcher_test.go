package runtime

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRolloutProjectionSeparatesRequestedFromCorroboratedInvocation(t *testing.T) {
	watcher := &CodexRolloutWatcher{
		key: bytes.Repeat([]byte("r"), 32),
	}
	memory := &rolloutFileMemory{
		pendingSkill: map[string]string{},
		pendingCall:  map[string]string{},
	}
	requestedLine := []byte(`{
		"type":"event_msg","timestamp":"2026-07-28T12:00:00Z",
		"payload":{"type":"user_message","message":"Use $search-workflow. Raw prompt canary KANSOKU_RAW_PROMPT"}
	}`)
	records, fingerprint := watcher.projectRolloutLine(
		requestedLine, "session-raw", "0.145.0", 1, memory,
	)
	if fingerprint != "" || len(records) != 1 ||
		records[0].EventType != "component.requested" ||
		records[0].ComponentEvidence.QualifiedIdentity != "search-workflow" ||
		records[0].ComponentEvidence.InvocationMode != "requested" ||
		records[0].Confidence >= 1 {
		t.Fatalf("requested projection=%#v fingerprint=%q", records, fingerprint)
	}
	callLine := []byte(`{
		"type":"response_item","timestamp":"2026-07-28T12:00:01Z",
		"payload":{"type":"function_call","call_id":"call-1","name":"exec_command",
		"arguments":"{\"cmd\":\"sed -n 1,20p /catalog/search-workflow/SKILL.md KANSOKU_TOOL_CANARY\"}"}
	}`)
	records, fingerprint = watcher.projectRolloutLine(
		callLine, "session-raw", "0.145.0", 2, memory,
	)
	if fingerprint != "" || len(records) != 0 {
		t.Fatalf("tool start unexpectedly emitted: %#v %q", records, fingerprint)
	}
	outputLine := []byte(`{
		"type":"response_item","timestamp":"2026-07-28T12:00:02Z",
		"payload":{"type":"function_call_output","call_id":"call-1",
		"output":"KANSOKU_RAW_SKILL_CONTENT"}
	}`)
	records, fingerprint = watcher.projectRolloutLine(
		outputLine, "session-raw", "0.145.0", 3, memory,
	)
	if fingerprint != "" || len(records) != 2 ||
		records[0].EventType != "component.loaded" ||
		records[1].EventType != "component.invoked" ||
		records[1].ComponentEvidence.InvocationMode != "explicit" {
		t.Fatalf("corroborated projection=%#v fingerprint=%q", records, fingerprint)
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{
		"KANSOKU_RAW_PROMPT", "KANSOKU_TOOL_CANARY",
		"KANSOKU_RAW_SKILL_CONTENT", "/catalog/", "session-raw",
	} {
		if bytes.Contains(encoded, []byte(prohibited)) {
			t.Fatalf("raw rollout content persisted: %q", prohibited)
		}
	}
}

func TestRolloutProjectionPersistsRequestedWithoutInventoryAndDoesNotPromoteIt(t *testing.T) {
	watcher := &CodexRolloutWatcher{
		key: bytes.Repeat([]byte("s"), 32),
	}
	memory := &rolloutFileMemory{
		pendingSkill: map[string]string{},
		pendingCall:  map[string]string{},
	}
	line := []byte(`{
		"type":"event_msg","timestamp":"2026-07-28T12:00:00Z",
		"payload":{"type":"user_message","message":"$skill-a $skill-b"}
	}`)
	records, _ := watcher.projectRolloutLine(
		line, "session", "0.145.0", 1, memory,
	)
	if len(records) != 2 {
		t.Fatalf("requested records=%d", len(records))
	}
	for _, record := range records {
		if record.EventType != "component.requested" ||
			record.ComponentEvidence.InvocationMode != "requested" {
			t.Fatalf("marker promoted to invocation: %#v", record)
		}
	}
	if len(memory.pendingSkill) != 2 || len(memory.pendingCall) != 0 {
		t.Fatalf("pending state=%#v", memory)
	}
	if rolloutReadSkill(
		"exec_command",
		json.RawMessage(`{"cmd":"echo unrelated"}`), nil,
		memory.pendingSkill,
	) != "" {
		t.Fatal("uncorroborated command promoted marker")
	}
	if strings.Contains(records[0].Lineage.SourceRecordPseudonym, "skill-a") {
		t.Fatal("identity was not pseudonymized in lineage")
	}
	if records[0].ReceivedAt.Before(time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("received timestamp was not populated")
	}
}
