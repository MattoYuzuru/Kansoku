package codexadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/privacy"
)

func appServerTestBridge(t *testing.T) *AppServerBridge {
	t.Helper()
	bridge, err := NewAppServerBridge(
		[]byte("codex-app-server-bridge-test-key-0123456789abcdef"),
		func() time.Time { return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC) },
	)
	if err != nil {
		t.Fatal(err)
	}
	return bridge
}

func TestAppServerBridgeProjectsOnlySafeTypedFields(t *testing.T) {
	bridge := appServerTestBridge(t)
	sink := &adaptersdk.MemoryAssertionSink{}
	canary := "KANSOKU_CONTENT_CANARY_DO_NOT_PERSIST"
	secret := "sk-proj-THIS-MUST-NOT-SURVIVE"
	frames := strings.Join([]string{
		`{"method":"thread/started","params":{"thread":{"id":"thr-1","sessionId":"ses-1","cliVersion":"0.145.0","createdAt":1785060000,"cwd":"/private/` + canary + `","preview":"` + secret + `","path":"/private/rollout","modelProvider":"openai","ephemeral":true,"source":"exec","status":{"type":"idle"},"turns":[],"updatedAt":1785060000}}}`,
		`{"method":"turn/started","params":{"threadId":"thr-1","turn":{"id":"turn-1","startedAt":1785060001,"status":"inProgress","items":[{"type":"userMessage","id":"msg-1","content":[{"type":"text","text":"` + canary + `"}]}]}}}`,
		`{"method":"item/completed","params":{"threadId":"thr-1","turnId":"turn-1","completedAtMs":1785060002500,"item":{"type":"mcpToolCall","id":"call-1","server":"safe-server","tool":"safe-tool","status":"completed","durationMs":41,"arguments":{"token":"` + secret + `"},"result":{"content":"` + canary + `"},"error":null}}}`,
		`{"method":"item/completed","params":{"threadId":"thr-1","turnId":"turn-1","completedAtMs":1785060003000,"item":{"type":"agentMessage","id":"msg-2","text":"` + canary + ` ` + secret + `"}}}`,
	}, "\n")
	err := bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
		Installation: adaptersdk.Installation{InstallationID: "ain-safe", AdapterID: AdapterID},
		Protocol:     AppServerProtocolVersion, SchemaVersion: AppServerSchemaVersion,
		Frames: strings.NewReader(frames),
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	records := sink.Records()
	if len(records) != 3 {
		t.Fatalf("records=%d want 3", len(records))
	}
	if records[0].EventType != "session.started" || records[1].EventType != "prompt.submitted" ||
		records[2].EventType != "tool.called" || records[2].Tool.ID == nil ||
		*records[2].Tool.ID != "mcp:safe-server/safe-tool" {
		t.Fatalf("unexpected projections: %#v", records)
	}
	serialized, err := json.Marshal(struct {
		Records []any
		Health  adaptersdk.BridgeHealth
	}{
		Records: []any{records}, Health: bridge.Health(context.Background()),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{
		canary, secret, "/private/", "arguments", "result", "preview", "cwd", "content",
	} {
		if bytes.Contains(serialized, []byte(prohibited)) {
			t.Fatalf("prohibited content reached typed sink/health: %q", prohibited)
		}
	}
	health := bridge.Health(context.Background())
	if health.Lifecycle != adaptersdk.BridgeReconciled || health.AcceptedFrames != 3 ||
		health.RejectedFrames != 0 {
		t.Fatalf("health=%#v", health)
	}
	sinks, err := privacy.SerializeAllSinks(records, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sinks) != 10 {
		t.Fatalf("accepted bridge sink count=%d want 10", len(sinks))
	}
	if matches := privacy.ScanCanaries(sinks, map[string]string{
		"content": canary, "secret": secret, "path": "/private/",
	}); len(matches) != 0 {
		t.Fatalf("bridge content reached accepted sink set: %#v", matches)
	}
	if matches := privacy.ScanSecretFormats(sinks); len(matches) != 0 {
		t.Fatalf("secret format reached accepted bridge sink set: %#v", matches)
	}
}

func TestAppServerBridgeUnknownSchemaIsMetadataOnlyAndDegradesOnlyBridge(t *testing.T) {
	bridge := appServerTestBridge(t)
	sink := &adaptersdk.MemoryAssertionSink{}
	raw := `{"method":"future/content","params":{"prompt":"do not retain","secret":"sk-live"}}`
	if err := bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
		Installation: adaptersdk.Installation{InstallationID: "ain-safe", AdapterID: AdapterID},
		Protocol:     AppServerProtocolVersion, SchemaVersion: AppServerSchemaVersion,
		Frames: strings.NewReader(raw),
	}, sink); err != nil {
		t.Fatal(err)
	}
	rejections := sink.Rejections()
	if len(rejections) != 1 || rejections[0].Category != "unsupported_bridge_method" {
		t.Fatalf("rejections=%#v", rejections)
	}
	serialized, _ := json.Marshal(rejections)
	if bytes.Contains(serialized, []byte("do not retain")) || bytes.Contains(serialized, []byte("sk-live")) {
		t.Fatal("raw unknown frame reached rejection metadata")
	}
	safeErr := &privacy.SafeError{
		IncidentID: "inc_bridge_schema", SourceSchemaID: AdapterID + ".bridge/" + AppServerSchemaVersion,
		SchemaFingerprint: rejections[0].SchemaFingerprint, FieldPath: "$",
		Category: rejections[0].Category, TotalBytes: rejections[0].ByteCount,
		RecordCount: 1, ObservedAt: rejections[0].ObservedAt, ReceivedAt: rejections[0].ObservedAt,
	}
	sinks, err := privacy.SerializeAllSinks(nil, safeErr)
	if err != nil {
		t.Fatal(err)
	}
	if len(sinks) != 10 {
		t.Fatalf("rejected bridge sink count=%d want 10", len(sinks))
	}
	if matches := privacy.ScanCanaries(sinks, map[string]string{
		"content": "do not retain", "secret": "sk-live",
	}); len(matches) != 0 {
		t.Fatalf("bridge content reached rejection sink set: %#v", matches)
	}
	if matches := privacy.ScanSecretFormats(sinks); len(matches) != 0 {
		t.Fatalf("secret format reached rejected bridge sink set: %#v", matches)
	}
	health := bridge.Health(context.Background())
	if health.AcceptedFrames != 0 || health.RejectedFrames != 1 {
		t.Fatalf("health=%#v", health)
	}
	manifestCapabilities := bridge.Manifest().Capabilities
	if !reflect.DeepEqual(manifestCapabilities, []adaptersdk.CapabilityID{
		adaptersdk.CapabilityActivitySessions,
		adaptersdk.CapabilityComponentsMCPLifecycle,
		adaptersdk.CapabilityIngestionEvidenceBridge,
	}) {
		t.Fatalf("unexpected bridge capability ownership: %#v", manifestCapabilities)
	}
}

func TestAppServerBridgeRejectsWrongVersionAndOversizeFrames(t *testing.T) {
	bridge := appServerTestBridge(t)
	sink := &adaptersdk.MemoryAssertionSink{}
	err := bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
		Installation: adaptersdk.Installation{AdapterID: AdapterID},
		Protocol:     AppServerProtocolVersion, SchemaVersion: "0.146.0",
		Frames: strings.NewReader("{}"),
	}, sink)
	if err == nil {
		t.Fatal("unreviewed schema version accepted")
	}

	bridge = appServerTestBridge(t)
	err = bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
		Installation: adaptersdk.Installation{AdapterID: AdapterID},
		Protocol:     AppServerProtocolVersion, SchemaVersion: AppServerSchemaVersion,
		Frames: strings.NewReader(strings.Repeat("x", bridge.Manifest().MaxFrameBytes+1)),
	}, sink)
	if err == nil {
		t.Fatal("oversize frame accepted")
	}
}
