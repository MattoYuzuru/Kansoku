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

func TestAppServerBridgeMapsMCPStartAndIsErrorTerminalWithoutPayload(t *testing.T) {
	bridge := appServerTestBridge(t)
	sink := &adaptersdk.MemoryAssertionSink{}
	frames := strings.Join([]string{
		`{"method":"item/started","params":{"threadId":"thr-1","turnId":"turn-1","startedAtMs":1785060001000,"item":{"type":"mcpToolCall","id":"call-1","server":"kansoku-noop-mcp","tool":"nothing.error","arguments":{"token":"sk-raw"}}}}`,
		`{"method":"item/completed","params":{"threadId":"thr-1","turnId":"turn-1","completedAtMs":1785060001050,"item":{"type":"mcpToolCall","id":"call-1","server":"kansoku-noop-mcp","tool":"nothing.error","status":"completed","durationMs":50,"result":{"isError":true,"content":[{"type":"text","text":"raw error"}]}}}}`,
	}, "\n")
	if err := bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
		Installation: adaptersdk.Installation{InstallationID: "ain-safe", AdapterID: AdapterID},
		Protocol:     AppServerProtocolVersion, SchemaVersion: AppServerSchemaVersion,
		Frames: strings.NewReader(frames),
	}, sink); err != nil {
		t.Fatal(err)
	}
	records := sink.Records()
	if len(records) != 1 || records[0].Outcome != "failed" ||
		records[0].Tool.ID == nil || *records[0].Tool.ID != "mcp:kansoku-noop-mcp/nothing.error" {
		t.Fatalf("unexpected MCP lifecycle: %#v", records)
	}
	serialized, _ := json.Marshal(records)
	for _, raw := range []string{"sk-raw", "raw error", "arguments", "content"} {
		if bytes.Contains(serialized, []byte(raw)) {
			t.Fatalf("raw MCP payload survived: %q", raw)
		}
	}
	sinks, err := privacy.SerializeAllSinks(records, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sinks) != 10 {
		t.Fatalf("sink count=%d want 10", len(sinks))
	}
	if matches := privacy.ScanCanaries(sinks, map[string]string{"argument": "sk-raw", "result": "raw error"}); len(matches) != 0 {
		t.Fatalf("raw MCP content reached sinks: %#v", matches)
	}
	if matches := privacy.ScanSecretFormats(sinks); len(matches) != 0 {
		t.Fatalf("secret-shaped MCP argument reached sinks: %#v", matches)
	}
}

func TestAppServerBridgeTerminalOutcomeMatrix(t *testing.T) {
	start := `{"method":"item/started","params":{"threadId":"thr","turnId":"turn","startedAtMs":1785060001000,"item":{"type":"mcpToolCall","id":"call","server":"safe-server","tool":"safe-tool"}}}`
	completed := func(status string) string {
		return `{"method":"item/completed","params":{"threadId":"thr","turnId":"turn","completedAtMs":1785060001050,"item":{"type":"mcpToolCall","id":"call","server":"safe-server","tool":"safe-tool","status":"` + status + `","durationMs":50}}}`
	}
	tests := []struct {
		name      string
		frames    []string
		outcome   string
		rejection string
	}{
		{"success", []string{start, completed("completed")}, "succeeded", ""},
		{"failure", []string{start, completed("failed")}, "failed", ""},
		{"cancel", []string{start, completed("cancelled")}, "cancelled", ""},
		{"deny", []string{start, completed("declined")}, "failed", ""},
		{"timeout", []string{start, completed("timed_out")}, "timed_out", ""},
		{"missing", []string{start}, "unknown", "missing_terminal"},
		{"duplicate", []string{start, completed("completed"), completed("completed")}, "succeeded", ""},
		{"contradictory", []string{start, completed("completed"), completed("failed")}, "unknown", "contradictory_terminal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bridge := appServerTestBridge(t)
			sink := &adaptersdk.MemoryAssertionSink{}
			if err := bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
				Installation: adaptersdk.Installation{
					InstallationID: "ain-safe", AdapterID: AdapterID,
				},
				Protocol: AppServerProtocolVersion, SchemaVersion: AppServerSchemaVersion,
				Frames: strings.NewReader(strings.Join(test.frames, "\n")),
			}, sink); err != nil {
				t.Fatal(err)
			}
			records := sink.Records()
			if len(records) != 1 || records[0].Outcome != test.outcome {
				t.Fatalf("records=%#v", records)
			}
			rejections := sink.Rejections()
			if test.rejection == "" {
				if len(rejections) != 0 {
					t.Fatalf("rejections=%#v", rejections)
				}
			} else if len(rejections) != 1 || rejections[0].Category != test.rejection {
				t.Fatalf("rejections=%#v want %s", rejections, test.rejection)
			}
		})
	}
}

func TestAppServerBridgeUnownedServiceMethodIsFilteredWithoutQuarantine(t *testing.T) {
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
	if len(rejections) != 0 {
		t.Fatalf("unowned service traffic quarantined: %#v", rejections)
	}
	health := bridge.Health(context.Background())
	if health.AcceptedFrames != 0 || health.RejectedFrames != 0 {
		t.Fatalf("health=%#v", health)
	}
	manifestCapabilities := bridge.Manifest().Capabilities
	if !reflect.DeepEqual(manifestCapabilities, []adaptersdk.CapabilityID{
		adaptersdk.CapabilityActivitySessions,
		adaptersdk.CapabilityComponentsSkillInvocation,
		adaptersdk.CapabilityComponentsPluginAndCustomCmd,
		adaptersdk.CapabilityComponentsMCPLifecycle,
		adaptersdk.CapabilityIngestionEvidenceBridge,
	}) {
		t.Fatalf("unexpected bridge capability ownership: %#v", manifestCapabilities)
	}
}

func TestAppServerBridgeDemultiplexesConcurrentResponsesAndServiceTraffic(t *testing.T) {
	bridge := appServerTestBridge(t)
	sink := &adaptersdk.MemoryAssertionSink{}
	frames := strings.Join([]string{
		`{"id":1,"method":"initialize","params":{"clientInfo":{"name":"test"}}}`,
		`{"id":7,"method":"skills/list","params":{"cwds":[],"forceReload":false}}`,
		`{"id":8,"method":"skills/list","params":{"cwds":[],"forceReload":false}}`,
		`{"id":1,"result":{"userAgent":"codex_cli_rs/0.145.0"}}`,
		`{"method":"thread/status/changed","params":{"threadId":"thr","status":{"type":"idle"}}}`,
		`{"id":8,"result":{"data":[{"skills":[{"name":"second-skill","path":"/redacted/two","enabled":true}]}]}}`,
		`{"id":999,"result":{"unrelated":"service-response"}}`,
		`{"id":7,"result":{"data":[{"skills":[{"name":"first-skill","path":"/redacted/one","enabled":true}]}]}}`,
	}, "\n")
	if err := bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
		Installation: adaptersdk.Installation{InstallationID: "ain-safe", AdapterID: AdapterID},
		Protocol:     AppServerProtocolVersion, SchemaVersion: AppServerSchemaVersion,
		Frames: strings.NewReader(frames),
	}, sink); err != nil {
		t.Fatal(err)
	}
	records := sink.Records()
	if len(records) != 2 || records[0].Tool.ID == nil || records[1].Tool.ID == nil ||
		*records[0].Tool.ID != "second-skill" || *records[1].Tool.ID != "first-skill" {
		t.Fatalf("concurrent response demux failed: %#v", records)
	}
	if len(sink.Rejections()) != 0 {
		t.Fatalf("service traffic quarantined: %#v", sink.Rejections())
	}
}

func TestAppServerBridgeProjectsPluginReadLifecycleAndOwnershipWithoutContent(t *testing.T) {
	bridge := appServerTestBridge(t)
	sink := &adaptersdk.MemoryAssertionSink{}
	frames := strings.Join([]string{
		`{"jsonrpc":"2.0","id":9,"method":"plugin/read","params":{"pluginName":"sre-agent","marketplacePath":"/private/KANSOKU_PLUGIN_REQUEST_PATH","remoteMarketplaceName":"yuzuru-engineering"}}`,
		`{"jsonrpc":"2.0","id":9,"result":{"plugin":{"marketplaceName":"yuzuru-engineering","marketplacePath":"/private/KANSOKU_PLUGIN_MARKETPLACE_PATH","description":"KANSOKU_PLUGIN_DESCRIPTION_MUST_NOT_PERSIST","shareUrl":"https://example.invalid/KANSOKU_PLUGIN_SHARE_URL","summary":{"id":"KANSOKU_RAW_PLUGIN_ID_MUST_NOT_PERSIST","name":"sre-agent","installed":true,"enabled":true,"source":{"type":"local","path":"/private/KANSOKU_PLUGIN_SOURCE_PATH"}},"skills":[{"name":"sre-agent","enabled":true,"path":"/private/KANSOKU_PLUGIN_SKILL_PATH/SKILL.md","description":"KANSOKU_PLUGIN_SKILL_DESCRIPTION_MUST_NOT_PERSIST"}],"hooks":[{"key":"pre-tool","eventName":"preToolUse"}],"mcpServers":["sre-mcp"],"apps":[{"id":"KANSOKU_RAW_APP_ID_MUST_NOT_PERSIST","name":"sre-app","description":"KANSOKU_APP_DESCRIPTION_MUST_NOT_PERSIST","installUrl":"https://example.invalid/KANSOKU_APP_URL"}],"appTemplates":[{"templateId":"raw-template","name":"raw template","materializedAppIds":[]}],"scheduledTasks":[{"key":"raw-task","name":"raw task","prompt":"KANSOKU_SCHEDULED_PROMPT_MUST_NOT_PERSIST","schedule":{"type":"daily"}}]}}}`,
	}, "\n")
	if err := bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
		Installation: adaptersdk.Installation{InstallationID: "ain-safe", AdapterID: AdapterID},
		Protocol:     AppServerProtocolVersion, SchemaVersion: AppServerSchemaVersion,
		Frames: strings.NewReader(frames),
	}, sink); err != nil {
		t.Fatal(err)
	}
	records := sink.Records()
	if len(records) != 11 {
		t.Fatalf("plugin/read records=%d, want 11: %#v", len(records), records)
	}
	var pluginEnabled, skillEnabled, mcpEnabled, hookEnabled, appEnabled bool
	for _, record := range records {
		if record.Tool.ID == nil {
			continue
		}
		switch {
		case record.EventType == "component.enabled" &&
			record.ComponentKind == "plugin" &&
			*record.Tool.ID == "sre-agent@yuzuru-engineering":
			pluginEnabled = record.ComponentEvidence.IdentitySource == "native_bridge_plugin_read" &&
				strings.HasPrefix(record.ComponentEvidence.UpstreamIdentityHash, "hmac-sha256:")
		case record.EventType == "component.enabled" &&
			record.ComponentKind == "skill" &&
			*record.Tool.ID == "sre-agent@yuzuru-engineering:sre-agent":
			skillEnabled = record.ComponentEvidence.OwnerPluginIdentity ==
				"sre-agent@yuzuru-engineering"
		case record.EventType == "component.enabled" &&
			record.ComponentKind == "mcp":
			mcpEnabled = record.ComponentEvidence.OwnerPluginIdentity ==
				"sre-agent@yuzuru-engineering"
		case record.EventType == "component.enabled" &&
			record.ComponentKind == "hook":
			hookEnabled = record.ComponentEvidence.OwnerPluginIdentity ==
				"sre-agent@yuzuru-engineering"
		case record.EventType == "component.enabled" &&
			record.ComponentKind == "app":
			appEnabled = record.ComponentEvidence.OwnerPluginIdentity ==
				"sre-agent@yuzuru-engineering"
		}
	}
	if !pluginEnabled || !skillEnabled || !mcpEnabled || !hookEnabled || !appEnabled {
		t.Fatalf(
			"plugin/read lifecycle plugin=%t skill=%t mcp=%t hook=%t app=%t",
			pluginEnabled, skillEnabled, mcpEnabled, hookEnabled, appEnabled,
		)
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{
		"KANSOKU_PLUGIN_REQUEST_PATH",
		"KANSOKU_PLUGIN_MARKETPLACE_PATH",
		"KANSOKU_PLUGIN_DESCRIPTION_MUST_NOT_PERSIST",
		"KANSOKU_PLUGIN_SHARE_URL",
		"KANSOKU_RAW_PLUGIN_ID_MUST_NOT_PERSIST",
		"KANSOKU_PLUGIN_SOURCE_PATH",
		"KANSOKU_PLUGIN_SKILL_PATH",
		"KANSOKU_PLUGIN_SKILL_DESCRIPTION_MUST_NOT_PERSIST",
		"KANSOKU_RAW_APP_ID_MUST_NOT_PERSIST",
		"KANSOKU_APP_DESCRIPTION_MUST_NOT_PERSIST",
		"KANSOKU_APP_URL",
		"KANSOKU_SCHEDULED_PROMPT_MUST_NOT_PERSIST",
	} {
		if bytes.Contains(encoded, []byte(prohibited)) {
			t.Fatalf("plugin/read durable projection leaked %q: %s", prohibited, encoded)
		}
	}
}

func TestAppServerBridgeRedactsUnrepresentablePluginChildIdentityWithoutDroppingLifecycle(t *testing.T) {
	bridge := appServerTestBridge(t)
	sink := &adaptersdk.MemoryAssertionSink{}
	frames := strings.Join([]string{
		`{"jsonrpc":"2.0","id":10,"method":"plugin/read","params":{"pluginName":"safe-plugin"}}`,
		`{"jsonrpc":"2.0","id":10,"result":{"plugin":{"marketplaceName":"safe-marketplace","marketplacePath":null,"description":null,"shareUrl":null,"summary":{"id":"raw-plugin-id","name":"safe-plugin","installed":true,"enabled":false,"source":{"type":"remote"}},"skills":[{"name":"unsafe child identity","enabled":true,"path":null,"description":"must not persist"}],"hooks":[],"mcpServers":[],"apps":[],"appTemplates":[],"scheduledTasks":[]}}}`,
	}, "\n")
	if err := bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
		Installation: adaptersdk.Installation{InstallationID: "ain-safe", AdapterID: AdapterID},
		Protocol:     AppServerProtocolVersion, SchemaVersion: AppServerSchemaVersion,
		Frames: strings.NewReader(frames),
	}, sink); err != nil {
		t.Fatal(err)
	}
	records := sink.Records()
	if len(records) != 3 {
		t.Fatalf("records=%d, want plugin requested+installed and redacted child installed", len(records))
	}
	child := records[2]
	if child.EventType != "component.installed" || child.ComponentKind != "skill" ||
		child.Tool.ID != nil || child.ComponentEvidence.IdentitySource != "redacted" ||
		child.ComponentEvidence.OwnerPluginIdentity != "safe-plugin@safe-marketplace" ||
		!strings.HasPrefix(child.ComponentEvidence.UpstreamIdentityHash, "hmac-sha256:") {
		t.Fatalf("redacted child lifecycle=%#v", child)
	}
	encoded, err := json.Marshal(child)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("unsafe child identity")) {
		t.Fatalf("unsafe child identity leaked: %s", encoded)
	}
}

func TestAppServerBridgePluginReadSnapshotReplayIsStableWithinUTCDate(t *testing.T) {
	frames := strings.Join([]string{
		`{"jsonrpc":"2.0","id":11,"method":"plugin/read","params":{"pluginName":"safe-plugin"}}`,
		`{"jsonrpc":"2.0","id":11,"result":{"plugin":{"marketplaceName":"safe-marketplace","marketplacePath":null,"description":null,"shareUrl":null,"summary":{"id":"stable-upstream-id","name":"safe-plugin","installed":false,"enabled":false,"source":{"type":"remote"}},"skills":[],"hooks":[],"mcpServers":[],"apps":[],"appTemplates":[],"scheduledTasks":[]}}}`,
	}, "\n")
	projectAt := func(now time.Time) privacy.SafeRecord {
		t.Helper()
		bridge, err := NewAppServerBridge(
			[]byte("codex-app-server-bridge-test-key-0123456789abcdef"),
			func() time.Time { return now },
		)
		if err != nil {
			t.Fatal(err)
		}
		sink := &adaptersdk.MemoryAssertionSink{}
		if err := bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
			Installation: adaptersdk.Installation{InstallationID: "ain-safe", AdapterID: AdapterID},
			Protocol:     AppServerProtocolVersion, SchemaVersion: AppServerSchemaVersion,
			Frames: strings.NewReader(frames),
		}, sink); err != nil {
			t.Fatal(err)
		}
		records := sink.Records()
		if len(records) != 1 {
			t.Fatalf("records=%d, want one requested assertion", len(records))
		}
		return records[0]
	}
	first := projectAt(time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC))
	retry := projectAt(time.Date(2026, 7, 29, 23, 59, 59, 0, time.UTC))
	nextDay := projectAt(time.Date(2026, 7, 30, 0, 0, 1, 0, time.UTC))
	if first.RecordID != retry.RecordID ||
		first.IdempotencyKey != retry.IdempotencyKey ||
		!first.ObservedAt.Equal(retry.ObservedAt) {
		t.Fatalf("same-day retry identities differ: first=%#v retry=%#v", first, retry)
	}
	if nextDay.RecordID != first.RecordID ||
		nextDay.IdempotencyKey == first.IdempotencyKey ||
		nextDay.ObservedAt.Equal(first.ObservedAt) {
		t.Fatalf("next-day snapshot aggregation did not advance: first=%#v next=%#v", first, nextDay)
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

func TestAppServerBridgeProjectsSkillExposureInvocationAndLoadWithoutPath(t *testing.T) {
	bridge := appServerTestBridge(t)
	sink := &adaptersdk.MemoryAssertionSink{}
	canaryPath := "/private/KANSOKU_SKILL_PATH_MUST_NOT_PERSIST/SKILL.md"
	frames := strings.Join([]string{
		`{"jsonrpc":"2.0","id":7,"method":"skills/list","params":{"cwds":["/private/work"],"forceReload":true}}`,
		`{"jsonrpc":"2.0","id":7,"result":{"data":[{"cwd":"/private/work","errors":[],"skills":[{"name":"kansoku-noop-skill","path":"` + canaryPath + `","enabled":true,"scope":"user","description":"content is discarded"}]}]}}`,
		`{"emittedAtMs":1785060001000,"method":"turn/started","params":{"threadId":"thr-skill","turn":{"id":"turn-skill","startedAt":1785060001,"status":"inProgress","items":[]}}}`,
		`{"emittedAtMs":1785060001001,"method":"item/started","params":{"threadId":"thr-skill","turnId":"turn-skill","startedAtMs":1785060001000,"item":{"type":"userMessage","id":"msg-skill","content":[{"type":"skill","name":"sre-agent:sre-agent","path":"` + canaryPath + `"},{"type":"text","text":"raw prompt is discarded"}]}}}`,
	}, "\n")
	if err := bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
		Installation: adaptersdk.Installation{InstallationID: "ain-safe", AdapterID: AdapterID},
		Protocol:     AppServerProtocolVersion, SchemaVersion: AppServerSchemaVersion,
		Frames: strings.NewReader(frames),
	}, sink); err != nil {
		t.Fatal(err)
	}
	records := sink.Records()
	var events []string
	for _, record := range records {
		events = append(events, record.EventType)
		if record.Tool.ID != nil && *record.Tool.ID == "kansoku-noop-skill" &&
			record.ComponentKind != "skill" {
			t.Fatalf("skill identity lost its kind: %#v", record)
		}
		if record.EventType == "component.invoked" &&
			(record.ComponentEvidence.QualifiedIdentity != "sre-agent:sre-agent" ||
				record.ComponentEvidence.OwnerPluginIdentity != "sre-agent") {
			t.Fatalf("plugin-owned child attribution=%#v", record.ComponentEvidence)
		}
	}
	want := []string{"component.exposed", "prompt.submitted", "component.invoked", "component.loaded"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v want %v", events, want)
	}
	sinks, err := privacy.SerializeAllSinks(records, nil)
	if err != nil {
		t.Fatal(err)
	}
	if matches := privacy.ScanCanaries(sinks, map[string]string{
		"path": canaryPath, "prompt": "raw prompt is discarded", "description": "content is discarded",
	}); len(matches) != 0 {
		t.Fatalf("skill content reached sinks: %#v", matches)
	}
}
