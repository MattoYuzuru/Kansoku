package runtime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestBoundedRolloutReaderSkipsOversizedLineAndContinues(t *testing.T) {
	rawMarker := "KANSOKU_RAW_OVERSIZED_MUST_NOT_PERSIST"
	input := strings.Repeat(rawMarker, maxRolloutWatchLineBytes/len(rawMarker)+2) +
		"\n{\"type\":\"valid\"}\n"
	reader := bufio.NewReaderSize(strings.NewReader(input), 64<<10)

	line, size, digest, oversized, err := readBoundedRolloutLine(reader)
	if err != nil || !oversized || len(line) != 0 ||
		size <= maxRolloutWatchLineBytes || len(digest) != 64 {
		t.Fatalf(
			"oversized line=%d size=%d digest=%q oversized=%t err=%v",
			len(line), size, digest, oversized, err,
		)
	}
	if strings.Contains(digest, rawMarker) {
		t.Fatal("raw oversized content escaped through digest")
	}

	line, size, digest, oversized, err = readBoundedRolloutLine(reader)
	if err != nil || oversized || string(line) != `{"type":"valid"}` ||
		size != int64(len(line)+1) || digest != "" {
		t.Fatalf(
			"following line=%q size=%d digest=%q oversized=%t err=%v",
			line, size, digest, oversized, err,
		)
	}
	if _, _, _, _, err = readBoundedRolloutLine(reader); err != io.EOF {
		t.Fatalf("final err=%v, want EOF", err)
	}
}

func TestRolloutProjectionRequiresCorroborationBeforeAnySkillAssertion(t *testing.T) {
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
	if fingerprint != "" || len(records) != 0 {
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
	if fingerprint != "" || len(records) != 3 ||
		records[0].EventType != "component.requested" ||
		records[0].ComponentEvidence.InvocationMode != "requested" ||
		records[1].EventType != "component.loaded" ||
		records[2].EventType != "component.invoked" ||
		records[2].ComponentEvidence.InvocationMode != "explicit" {
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

func TestRolloutProjectionDollarVariablesCreateZeroSkillAssertions(t *testing.T) {
	watcher := &CodexRolloutWatcher{
		key: bytes.Repeat([]byte("s"), 32),
	}
	memory := &rolloutFileMemory{
		pendingSkill: map[string]string{},
		pendingCall:  map[string]string{},
	}
	line := []byte(`{
		"type":"event_msg","timestamp":"2026-07-28T12:00:00Z",
		"payload":{"type":"user_message","message":"const $identifier = $PATH + $HOME; price is $5"}
	}`)
	records, _ := watcher.projectRolloutLine(
		line, "session", "0.145.0", 1, memory,
	)
	if len(records) != 0 {
		t.Fatalf("dollar variables created assertions: %#v", records)
	}
	if len(memory.pendingCall) != 0 {
		t.Fatalf("pending state=%#v", memory)
	}
	if rolloutReadSkill(
		"exec_command",
		json.RawMessage(`{"cmd":"echo unrelated"}`), nil,
		memory.pendingSkill,
	) != "" {
		t.Fatal("ordinary dollar variable was promoted")
	}
}
