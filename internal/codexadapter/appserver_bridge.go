package codexadapter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/privacy"
)

const (
	AppServerBridgeID        = "codex-app-server"
	AppServerBridgeVersion   = "0.3.0"
	AppServerProtocolVersion = "codex-app-server-jsonl"
	AppServerSchemaVersion   = "0.145.0"
)

// AppServerBridge translates the exact locally generated Codex App Server
// 0.145.0 JSONL surface into privacy.SafeRecord. Raw frames live only for the
// duration of projectFrame; content-bearing fields have no destination in
// either the bridge state or its sink.
type AppServerBridge struct {
	key             []byte
	now             func() time.Time
	mu              sync.Mutex
	health          adaptersdk.BridgeHealth
	checkpoint      adaptersdk.BridgeCheckpoint
	pendingRequests map[string]string
}

var (
	bridgeSkillNamePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	bridgeIdentityPartPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	bridgeComponentNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/-]{0,127}$`)
)

func NewAppServerBridge(key []byte, now func() time.Time) (*AppServerBridge, error) {
	if len(key) < 32 {
		return nil, errors.New("invalid_bridge_pseudonym_key")
	}
	if now == nil {
		now = time.Now
	}
	return &AppServerBridge{
		key: append([]byte(nil), key...), now: now,
		health: adaptersdk.BridgeHealth{
			Lifecycle: adaptersdk.BridgeDiscovered, Compatible: true,
		},
		pendingRequests: map[string]string{},
	}, nil
}

var _ adaptersdk.EvidenceBridge = (*AppServerBridge)(nil)

func (b *AppServerBridge) Manifest() adaptersdk.BridgeManifest {
	return adaptersdk.BridgeManifest{
		APIVersion: adaptersdk.EvidenceBridgeAPIVersion,
		AdapterID:  AdapterID, BridgeID: AppServerBridgeID, BridgeVersion: AppServerBridgeVersion,
		SupportedAgentVersions: []string{"0.145.0"},
		ProtocolVersions:       []string{AppServerProtocolVersion},
		SchemaVersions:         []string{AppServerSchemaVersion},
		Capabilities: []adaptersdk.CapabilityID{
			adaptersdk.CapabilityActivitySessions,
			adaptersdk.CapabilityComponentsSkillInvocation,
			adaptersdk.CapabilityComponentsPluginAndCustomCmd,
			adaptersdk.CapabilityComponentsMCPLifecycle,
			adaptersdk.CapabilityIngestionEvidenceBridge,
		},
		SafeFields: []string{
			"adapter_version", "bridge_version", "component_kind", "event_type",
			"idempotency_key", "installation_id", "observed_at", "outcome",
			"schema_version", "sequence", "session_pseudonym", "source_record_pseudonym",
			"tool_identity", "turn_pseudonym", "value_state",
		},
		ProhibitedSurfaces: []string{
			"arguments", "commands", "config_values", "environment", "errors",
			"file_contents", "messages", "paths", "prompts", "reasoning", "resources",
			"results", "uris",
		},
		Permissions: adaptersdk.Permissions{Network: adaptersdk.NetworkLoopbackOnly},
		TargetScope: "explicit_local", MaxFrameBytes: 64 << 10, MaxFrames: 10_000,
		ConnectTimeout: 30 * time.Second, MaxReconnects: 3,
		IdempotencyStrategy: "adapter_session_native_id_event_type",
		CheckpointStrategy:  "sequence_only_no_source_replay",
		FixtureID:           "codex-app-server-0-145-0-sanitized",
		CanaryID:            "codex-app-server-bounded-local",
	}
}

func (b *AppServerBridge) Probe(_ context.Context, _ *adaptersdk.HostView, installation adaptersdk.Installation) (adaptersdk.BridgeHealth, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if installation.AdapterID != AdapterID {
		return b.health, adaptersdk.ErrIncompatibleBridgeTarget
	}
	b.health.Lifecycle = adaptersdk.BridgeConfigured
	return b.health, nil
}

func (b *AppServerBridge) Connect(ctx context.Context, target adaptersdk.BridgeTarget, sink adaptersdk.SafeAssertionSink) error {
	manifest := b.Manifest()
	if err := adaptersdk.ValidateBridgeManifest(manifest); err != nil {
		return err
	}
	if target.Installation.AdapterID != AdapterID || target.Protocol != AppServerProtocolVersion ||
		target.SchemaVersion != AppServerSchemaVersion || target.Frames == nil || sink == nil {
		return adaptersdk.ErrIncompatibleBridgeTarget
	}
	b.setLifecycle(adaptersdk.BridgeConnected, "")
	scanner := bufio.NewScanner(target.Frames)
	scanner.Buffer(make([]byte, 4096), manifest.MaxFrameBytes)
	var sequence uint64
	terminalOrder := make([]string, 0)
	terminalCalls := map[string]*bridgeTerminalCall{}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			b.setLifecycle(adaptersdk.BridgeDegraded, "context_cancelled")
			return err
		}
		sequence++
		if sequence > uint64(manifest.MaxFrames) {
			b.reject(ctx, sink, "frame_limit_exceeded", int64(len(scanner.Bytes())))
			return errors.New("bridge_frame_limit_exceeded")
		}
		records, category := b.projectFrame(scanner.Bytes(), sequence)
		if category != "" {
			if err := b.reject(ctx, sink, category, int64(len(scanner.Bytes()))); err != nil {
				return err
			}
			continue
		}
		if len(records) == 0 {
			continue
		}
		for _, record := range records {
			if record.EventType == "tool.called" && record.ComponentKind == "mcp" {
				key := record.Lineage.SourceRecordPseudonym
				call := terminalCalls[key]
				if call == nil {
					call = &bridgeTerminalCall{terminals: map[string]privacy.SafeRecord{}}
					terminalCalls[key] = call
					terminalOrder = append(terminalOrder, key)
				}
				call.observe(record)
				continue
			}
			if err := b.accept(ctx, sink, record, sequence); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		b.reject(ctx, sink, "frame_too_large_or_stream_error", 0)
		return errors.New("bridge_stream_error")
	}
	for _, key := range terminalOrder {
		record, category := terminalCalls[key].reconcile(b, key)
		if err := b.accept(ctx, sink, record, sequence); err != nil {
			return err
		}
		if category != "" {
			if err := b.reject(ctx, sink, category, 0); err != nil {
				return err
			}
		}
	}
	b.mu.Lock()
	if b.health.RejectedFrames == 0 {
		b.health.Lifecycle = adaptersdk.BridgeReconciled
		b.health.Category = ""
	}
	b.mu.Unlock()
	return nil
}

type bridgeTerminalCall struct {
	started   *privacy.SafeRecord
	terminals map[string]privacy.SafeRecord
}

func (c *bridgeTerminalCall) observe(record privacy.SafeRecord) {
	if record.ComponentEvidence.IdentitySource == "native_bridge_started" {
		copyRecord := record
		if c.started == nil || record.ObservedAt.Before(c.started.ObservedAt) {
			c.started = &copyRecord
		}
		return
	}
	existing, exists := c.terminals[record.Outcome]
	if !exists || record.ObservedAt.Before(existing.ObservedAt) {
		c.terminals[record.Outcome] = record
	}
}

func (c *bridgeTerminalCall) reconcile(
	bridge *AppServerBridge,
	key string,
) (privacy.SafeRecord, string) {
	if len(c.terminals) == 0 {
		record := *c.started
		record.Outcome = "unknown"
		record.Telemetry.DurationMS = nil
		record.IdempotencyKey = bridge.pseudonym(
			"terminal-reconciliation/1", key+"\x00missing_terminal",
		)
		return record, "missing_terminal"
	}
	if len(c.terminals) == 1 {
		for outcome, record := range c.terminals {
			record.IdempotencyKey = bridge.pseudonym(
				"terminal-reconciliation/1", key+"\x00"+outcome,
			)
			return record, ""
		}
	}
	var record privacy.SafeRecord
	for _, candidate := range c.terminals {
		if record.RecordID == "" || candidate.ObservedAt.After(record.ObservedAt) {
			record = candidate
		}
	}
	record.Outcome = "unknown"
	record.Telemetry.DurationMS = nil
	record.IdempotencyKey = bridge.pseudonym(
		"terminal-reconciliation/1", key+"\x00contradictory_terminal",
	)
	return record, "contradictory_terminal"
}

func (b *AppServerBridge) accept(
	ctx context.Context,
	sink adaptersdk.SafeAssertionSink,
	record privacy.SafeRecord,
	sequence uint64,
) error {
	if err := sink.Accept(ctx, record); err != nil {
		b.setLifecycle(adaptersdk.BridgeDegraded, "sink_unavailable")
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.health.Lifecycle = adaptersdk.BridgeProducing
	b.health.AcceptedFrames++
	if record.ObservedAt.After(b.health.LastObservedAt) {
		b.health.LastObservedAt = record.ObservedAt
	}
	b.checkpoint = adaptersdk.BridgeCheckpoint{
		Sequence: sequence, LastObservedAt: b.health.LastObservedAt, ReplaySupported: false,
	}
	return nil
}

func (b *AppServerBridge) Checkpoint(context.Context) adaptersdk.BridgeCheckpoint {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.checkpoint
}

func (b *AppServerBridge) Health(context.Context) adaptersdk.BridgeHealth {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.health
}

func (b *AppServerBridge) setLifecycle(lifecycle adaptersdk.BridgeLifecycle, category string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.health.Lifecycle = lifecycle
	b.health.Category = category
}

func (b *AppServerBridge) reject(ctx context.Context, sink adaptersdk.SafeAssertionSink, category string, byteCount int64) error {
	now := b.now().UTC()
	rejection := adaptersdk.BridgeRejection{
		BridgeID: AppServerBridgeID, SchemaVersion: AppServerSchemaVersion,
		SchemaFingerprint: b.pseudonym("bridge-schema/1", AppServerSchemaVersion),
		Category:          category, ByteCount: byteCount, ObservedAt: now,
	}
	if err := sink.Reject(ctx, rejection); err != nil {
		return err
	}
	b.mu.Lock()
	b.health.RejectedFrames++
	b.health.Lifecycle = adaptersdk.BridgeDegraded
	b.health.Category = category
	b.mu.Unlock()
	return nil
}

type appServerEnvelope struct {
	JSONRPC     string          `json:"jsonrpc,omitempty"`
	ID          json.RawMessage `json:"id,omitempty"`
	Method      string          `json:"method"`
	Params      json.RawMessage `json:"params,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       json.RawMessage `json:"error,omitempty"`
	EmittedAtMS *int64          `json:"emittedAtMs,omitempty"`
}

func (b *AppServerBridge) projectFrame(raw []byte, sequence uint64) ([]privacy.SafeRecord, string) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope appServerEnvelope
	if err := decoder.Decode(&envelope); err != nil ||
		(envelope.Method == "" && len(envelope.Result) == 0 && len(envelope.Error) == 0) {
		return nil, "unknown_or_invalid_frame_schema"
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, "trailing_frame_json"
	}
	if envelope.Method == "" {
		if len(envelope.ID) == 0 {
			return nil, ""
		}
		b.mu.Lock()
		pendingMethod := b.pendingRequests[string(envelope.ID)]
		delete(b.pendingRequests, string(envelope.ID))
		b.mu.Unlock()
		if pendingMethod == "" {
			// A passive demultiplexer sees responses for every App Server
			// client request. Unowned responses are service traffic, not
			// bridge schema failures.
			return nil, ""
		}
		switch pendingMethod {
		case "skills/list":
			if len(envelope.Error) != 0 {
				return nil, "skills_list_response_error"
			}
			return b.projectSkillsListResponse(envelope, sequence)
		case "plugin/read":
			if len(envelope.Error) != 0 {
				return nil, "plugin_read_response_error"
			}
			return b.projectPluginReadResponse(envelope, sequence)
		default:
			return nil, ""
		}
	}
	switch envelope.Method {
	case "skills/list":
		if len(envelope.ID) == 0 || len(envelope.Params) == 0 {
			return nil, "invalid_skills_list_request"
		}
		b.mu.Lock()
		if len(b.pendingRequests) >= 128 {
			b.mu.Unlock()
			return nil, "pending_request_limit_exceeded"
		}
		b.pendingRequests[string(envelope.ID)] = envelope.Method
		b.mu.Unlock()
		return nil, ""
	case "plugin/read":
		if len(envelope.ID) == 0 || len(envelope.Params) == 0 {
			return nil, "invalid_plugin_read_request"
		}
		var params struct {
			PluginName            string  `json:"pluginName"`
			MarketplacePath       *string `json:"marketplacePath"`
			RemoteMarketplaceName *string `json:"remoteMarketplaceName"`
		}
		if json.Unmarshal(envelope.Params, &params) != nil ||
			!bridgeIdentityPartPattern.MatchString(params.PluginName) {
			return nil, "invalid_plugin_read_request"
		}
		b.mu.Lock()
		if len(b.pendingRequests) >= 128 {
			b.mu.Unlock()
			return nil, "pending_request_limit_exceeded"
		}
		b.pendingRequests[string(envelope.ID)] = envelope.Method
		b.mu.Unlock()
		return nil, ""
	case "thread/started":
		var params struct {
			Thread struct {
				ID         string `json:"id"`
				SessionID  string `json:"sessionId"`
				CLIVersion string `json:"cliVersion"`
				CreatedAt  int64  `json:"createdAt"`
			} `json:"thread"`
		}
		if json.Unmarshal(envelope.Params, &params) != nil || params.Thread.ID == "" ||
			params.Thread.SessionID == "" || params.Thread.CLIVersion != AppServerSchemaVersion ||
			params.Thread.CreatedAt <= 0 {
			return nil, "invalid_thread_started"
		}
		return []privacy.SafeRecord{b.safeRecord(
			params.Thread.ID, params.Thread.SessionID, "", "session.started",
			"unknown", "", "", time.Unix(params.Thread.CreatedAt, 0), sequence,
			privacy.RedactionCounts{PromptFields: 1, PathFields: 2, SourceFields: 1},
		)}, ""
	case "turn/started":
		var params struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID        string `json:"id"`
				StartedAt *int64 `json:"startedAt"`
			} `json:"turn"`
		}
		if json.Unmarshal(envelope.Params, &params) != nil || params.ThreadID == "" ||
			params.Turn.ID == "" || params.Turn.StartedAt == nil {
			return nil, "invalid_turn_started"
		}
		observedAt := time.Unix(*params.Turn.StartedAt, 0)
		return []privacy.SafeRecord{b.safeRecord(
			params.Turn.ID, params.ThreadID, params.Turn.ID, "prompt.submitted",
			"unknown", "", "", observedAt, sequence,
			privacy.RedactionCounts{PromptFields: 1},
		)}, ""
	case "item/started":
		var params struct {
			ThreadID   string `json:"threadId"`
			TurnID     string `json:"turnId"`
			StartedAtM int64  `json:"startedAtMs"`
			Item       struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				Server  string `json:"server"`
				Tool    string `json:"tool"`
				Content []struct {
					Type string `json:"type"`
					Name string `json:"name"`
					Path string `json:"path"`
				} `json:"content"`
			} `json:"item"`
		}
		if json.Unmarshal(envelope.Params, &params) != nil ||
			params.ThreadID == "" || params.TurnID == "" || params.Item.ID == "" ||
			params.StartedAtM <= 0 {
			return nil, "invalid_item_started"
		}
		observedAt := time.UnixMilli(params.StartedAtM)
		if params.Item.Type == "mcpToolCall" {
			if params.Item.Server == "" || params.Item.Tool == "" ||
				len(params.Item.Server)+len(params.Item.Tool) > 120 {
				return nil, "invalid_mcp_item_started"
			}
			record := b.safeRecord(
				params.Item.ID, params.ThreadID, params.TurnID, "tool.called",
				"unknown", "mcp:"+params.Item.Server+"/"+params.Item.Tool, "mcp",
				observedAt, sequence,
				privacy.RedactionCounts{ToolIOFields: 1, SensitiveIdentifierFields: 1},
			)
			record.ComponentEvidence.IdentitySource = "native_bridge_started"
			return []privacy.SafeRecord{record}, ""
		}
		if params.Item.Type != "userMessage" {
			return nil, ""
		}
		var records []privacy.SafeRecord
		skillCount := 0
		for _, input := range params.Item.Content {
			if input.Type != "skill" {
				continue
			}
			if !bridgeSkillNamePattern.MatchString(input.Name) || input.Path == "" {
				return nil, "invalid_skill_input"
			}
			skillCount++
			if skillCount > 16 {
				return nil, "skill_input_limit_exceeded"
			}
			nativeID := params.Item.ID + ":skill:" + input.Name
			records = append(records,
				b.safeRecord(nativeID+":invoked", params.ThreadID, params.TurnID,
					"component.invoked", "unknown", input.Name, "skill", observedAt,
					sequence, privacy.RedactionCounts{PathFields: 1, PromptFields: 1}),
				b.safeRecord(nativeID+":loaded", params.ThreadID, params.TurnID,
					"component.loaded", "unknown", input.Name, "skill", observedAt,
					sequence, privacy.RedactionCounts{PathFields: 1, PromptFields: 1}),
			)
		}
		return records, ""
	case "item/completed":
		var params struct {
			ThreadID      string `json:"threadId"`
			TurnID        string `json:"turnId"`
			CompletedAtMS int64  `json:"completedAtMs"`
			Item          struct {
				ID         string `json:"id"`
				Type       string `json:"type"`
				Server     string `json:"server"`
				Tool       string `json:"tool"`
				Status     string `json:"status"`
				DurationMS *int64 `json:"durationMs"`
				Result     *struct {
					IsError bool `json:"isError"`
				} `json:"result"`
			} `json:"item"`
		}
		if json.Unmarshal(envelope.Params, &params) != nil || params.Item.Type != "mcpToolCall" {
			// Every content-only item type is deliberately discarded before
			// the sink; it is a known frame, not an unknown schema incident.
			return nil, ""
		}
		if params.ThreadID == "" || params.TurnID == "" || params.Item.ID == "" ||
			params.Item.Server == "" || params.Item.Tool == "" || params.CompletedAtMS <= 0 ||
			len(params.Item.Server)+len(params.Item.Tool) > 120 {
			return nil, "invalid_mcp_item_completed"
		}
		outcome := bridgeOutcome(
			params.Item.Status,
			params.Item.Result != nil && params.Item.Result.IsError,
		)
		record := b.safeRecord(
			params.Item.ID, params.ThreadID, params.TurnID, "tool.called",
			outcome, "mcp:"+params.Item.Server+"/"+params.Item.Tool, "mcp",
			time.UnixMilli(params.CompletedAtMS), sequence,
			privacy.RedactionCounts{ToolIOFields: 3, ExceptionFields: 1, SensitiveIdentifierFields: 1},
		)
		record.ComponentEvidence.IdentitySource = "native_bridge_terminal"
		record.Telemetry.DurationMS = params.Item.DurationMS
		return []privacy.SafeRecord{record}, ""
	case "turn/completed", "skills/changed":
		return nil, ""
	default:
		// initialize/thread/model/account/config/status and future service
		// methods share the multiplexed JSON-RPC stream but are not owned by
		// this evidence bridge. They are filtered without quarantine.
		return nil, ""
	}
}

func (b *AppServerBridge) projectSkillsListResponse(envelope appServerEnvelope, _ uint64) ([]privacy.SafeRecord, string) {
	var result struct {
		Data []struct {
			Skills []struct {
				Name    string `json:"name"`
				Path    string `json:"path"`
				Enabled bool   `json:"enabled"`
			} `json:"skills"`
		} `json:"data"`
	}
	if json.Unmarshal(envelope.Result, &result) != nil {
		return nil, "invalid_skills_list_response"
	}
	snapshotDay := b.now().UTC().Truncate(24 * time.Hour)
	var records []privacy.SafeRecord
	for _, entry := range result.Data {
		for _, skill := range entry.Skills {
			if !skill.Enabled {
				continue
			}
			if !bridgeSkillNamePattern.MatchString(skill.Name) || skill.Path == "" {
				return nil, "invalid_skills_list_response"
			}
			if len(records) >= 4096 {
				return nil, "skills_list_limit_exceeded"
			}
			records = append(records, b.safeRecord(
				"skills-list:"+skill.Name, "skills-list", "", "component.exposed",
				"unknown", skill.Name, "skill", snapshotDay, 0,
				privacy.RedactionCounts{PathFields: 1, SourceFields: 1},
			))
		}
	}
	return records, ""
}

func (b *AppServerBridge) projectPluginReadResponse(
	envelope appServerEnvelope,
	_ uint64,
) ([]privacy.SafeRecord, string) {
	var result struct {
		Plugin struct {
			MarketplaceName string `json:"marketplaceName"`
			MarketplacePath any    `json:"marketplacePath"`
			Description     any    `json:"description"`
			ShareURL        any    `json:"shareUrl"`
			Summary         struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Installed bool   `json:"installed"`
				Enabled   bool   `json:"enabled"`
				Source    any    `json:"source"`
			} `json:"summary"`
			Skills []struct {
				Name        string `json:"name"`
				Enabled     bool   `json:"enabled"`
				Path        any    `json:"path"`
				Description any    `json:"description"`
			} `json:"skills"`
			Hooks []struct {
				Key       string `json:"key"`
				EventName string `json:"eventName"`
			} `json:"hooks"`
			MCPServers []string `json:"mcpServers"`
			Apps       []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				Description any    `json:"description"`
				InstallURL  any    `json:"installUrl"`
			} `json:"apps"`
			AppTemplates   []any `json:"appTemplates"`
			ScheduledTasks []any `json:"scheduledTasks"`
		} `json:"plugin"`
	}
	if json.Unmarshal(envelope.Result, &result) != nil ||
		result.Plugin.Summary.ID == "" || result.Plugin.Summary.Name == "" ||
		result.Plugin.MarketplaceName == "" {
		return nil, "invalid_plugin_read_response"
	}

	pluginIdentity, identityObserved := bridgePluginIdentity(
		result.Plugin.Summary.Name,
		result.Plugin.MarketplaceName,
	)
	upstreamHash := b.pseudonym("upstream-plugin-identity/1", result.Plugin.Summary.ID)
	snapshotDay := b.now().UTC().Truncate(24 * time.Hour)
	redactions := privacy.RedactionCounts{
		SourceFields:              3 + len(result.Plugin.AppTemplates) + len(result.Plugin.ScheduledTasks),
		PathFields:                1,
		SensitiveIdentifierFields: 1,
	}

	var records []privacy.SafeRecord
	appendPluginRecord := func(
		nativeSuffix, eventType, componentKind, qualifiedIdentity, ownerIdentity string,
		componentRedactions privacy.RedactionCounts,
	) bool {
		if len(records) >= 4096 {
			return false
		}
		record := b.safeRecord(
			"plugin-read:"+result.Plugin.Summary.ID+":"+nativeSuffix,
			"plugin-read", "", eventType, "unknown", qualifiedIdentity,
			componentKind, snapshotDay, 0, componentRedactions,
		)
		record.ComponentEvidence.IdentitySource = "native_bridge_plugin_read"
		record.ComponentEvidence.UpstreamIdentityHash = upstreamHash
		record.ComponentEvidence.OwnerPluginIdentity = ownerIdentity
		record.ComponentEvidence.SourceScope = "marketplace"
		if !identityObserved && componentKind == "plugin" {
			record.ComponentEvidence.IdentitySource = "redacted"
			record.ComponentEvidence.QualifiedIdentity = ""
		}
		records = append(records, record)
		return true
	}

	if !appendPluginRecord("requested", "component.requested", "plugin", pluginIdentity, "", redactions) {
		return nil, "plugin_read_response_limit_exceeded"
	}
	if result.Plugin.Summary.Installed &&
		!appendPluginRecord("installed", "component.installed", "plugin", pluginIdentity, "", redactions) {
		return nil, "plugin_read_response_limit_exceeded"
	}
	if result.Plugin.Summary.Enabled &&
		!appendPluginRecord("enabled", "component.enabled", "plugin", pluginIdentity, "", redactions) {
		return nil, "plugin_read_response_limit_exceeded"
	}

	appendChildLifecycle := func(
		childKind, childName, childNativeID string,
		childRedactions privacy.RedactionCounts,
		childEnabled bool,
	) bool {
		qualified := ""
		ownerIdentity := ""
		childIdentityObserved := identityObserved &&
			bridgeComponentNamePattern.MatchString(childName)
		if identityObserved {
			ownerIdentity = pluginIdentity
		}
		if childIdentityObserved {
			qualified = pluginIdentity + ":" + childName
			childIdentityObserved = len(qualified) <= 256
		}
		appendChildRecord := func(nativeSuffix, eventType string) bool {
			if !appendPluginRecord(
				childNativeID+":"+nativeSuffix, eventType, childKind,
				qualified, ownerIdentity, childRedactions,
			) {
				return false
			}
			if !childIdentityObserved {
				record := &records[len(records)-1]
				record.ComponentEvidence.IdentitySource = "redacted"
				record.ComponentEvidence.UpstreamIdentityHash = b.pseudonym(
					"upstream-plugin-child-identity/1",
					result.Plugin.Summary.ID+"\x00"+childKind+"\x00"+childName,
				)
			}
			return true
		}
		if result.Plugin.Summary.Installed &&
			!appendChildRecord("installed", "component.installed") {
			return false
		}
		if result.Plugin.Summary.Enabled && childEnabled &&
			!appendChildRecord("enabled", "component.enabled") {
			return false
		}
		return true
	}

	for index, skill := range result.Plugin.Skills {
		if !appendChildLifecycle(
			"skill", skill.Name, "skill:"+strconv.Itoa(index),
			privacy.RedactionCounts{PathFields: 1, SourceFields: 1},
			skill.Enabled,
		) {
			return nil, "plugin_read_response_limit_exceeded"
		}
	}
	for index, server := range result.Plugin.MCPServers {
		if !appendChildLifecycle(
			"mcp", server, "mcp:"+strconv.Itoa(index),
			privacy.RedactionCounts{SensitiveIdentifierFields: 1},
			true,
		) {
			return nil, "plugin_read_response_limit_exceeded"
		}
	}
	for index, hook := range result.Plugin.Hooks {
		if !appendChildLifecycle(
			"hook", hook.Key, "hook:"+strconv.Itoa(index),
			privacy.RedactionCounts{SourceFields: 1},
			true,
		) {
			return nil, "plugin_read_response_limit_exceeded"
		}
	}
	for index, app := range result.Plugin.Apps {
		if !appendChildLifecycle(
			"app", app.Name, "app:"+strconv.Itoa(index),
			privacy.RedactionCounts{SourceFields: 2, SensitiveIdentifierFields: 1},
			true,
		) {
			return nil, "plugin_read_response_limit_exceeded"
		}
	}
	return records, ""
}

func bridgePluginIdentity(name, marketplace string) (string, bool) {
	if !bridgeIdentityPartPattern.MatchString(name) ||
		!bridgeIdentityPartPattern.MatchString(marketplace) {
		return "", false
	}
	identity := name + "@" + marketplace
	if len(identity) > 256 {
		return "", false
	}
	return identity, true
}

func bridgeOutcome(status string, resultError bool) string {
	return adaptersdk.ClassifyTerminalStatus(status, resultError).Outcome
}

func (b *AppServerBridge) safeRecord(nativeID, sessionID, turnID, eventType, outcome, toolID, componentKind string, observedAt time.Time, sequence uint64, redactions privacy.RedactionCounts) privacy.SafeRecord {
	sourcePseudonym := b.pseudonym("source-record/1", AdapterID+"\x00"+nativeID)
	sessionPseudonym := b.pseudonym("session/1", AdapterID+"\x00"+sessionID)
	turnPseudonym := ""
	if turnID != "" {
		turnPseudonym = b.pseudonym("turn/1", AdapterID+"\x00"+sessionID+"\x00"+turnID)
	}
	recordID := b.pseudonym("record/1", AdapterID+"\x00"+sessionID+"\x00"+nativeID+"\x000")
	idempotency := b.pseudonym(
		"idempotency/1", AdapterID+"\x00evidence_bridge\x00"+nativeID+"\x00"+
			observedAt.UTC().Format(time.RFC3339Nano)+"\x00"+strconv.FormatUint(sequence, 10),
	)
	tool := privacy.CatalogObservation{State: privacy.ObservationNotObserved}
	if toolID != "" {
		tool = privacy.CatalogObservation{State: privacy.ObservationObserved, ID: &toolID}
	}
	ownerPluginIdentity := ""
	if componentKind == "skill" {
		if index := strings.LastIndex(toolID, ":"); index > 0 {
			ownerPluginIdentity = toolID[:index]
		}
	}
	schemaFingerprint := b.pseudonym("app-server-schema/1", AppServerSchemaVersion)
	return privacy.SafeRecord{
		RecordID: recordID, IdempotencyKey: idempotency,
		AdapterID: AdapterID, AdapterVersion: AdapterVersion,
		SourceSchemaID:    AdapterID + ".bridge/" + AppServerSchemaVersion,
		SchemaFingerprint: schemaFingerprint,
		ObservedAt:        observedAt.UTC(), ReceivedAt: b.now().UTC(), Confidence: 1,
		EventType: eventType, Outcome: outcome, ValueState: privacy.ValueObserved,
		Model: privacy.CatalogObservation{State: privacy.ObservationNotObserved},
		Tool:  tool, ComponentKind: componentKind,
		ComponentEvidence: privacy.ComponentEvidenceMetadata{
			QualifiedIdentity: toolID, IdentitySource: "native_bridge",
			OwnerPluginIdentity: ownerPluginIdentity,
			InvocationMode: func() string {
				if eventType == "component.invoked" {
					return "explicit"
				}
				return "not_observed"
			}(),
		},
		RedactionCounts: redactions,
		Lineage: privacy.Lineage{
			SourceRecordPseudonym: sourcePseudonym,
			SessionPseudonym:      sessionPseudonym, TurnPseudonym: turnPseudonym,
			AdapterID: AdapterID, AdapterVersion: AdapterVersion,
			SourceSchemaID:    AdapterID + ".bridge/" + AppServerSchemaVersion,
			SchemaFingerprint: schemaFingerprint,
			SanitizerVersion:  "kansoku.ingress-sanitizer/1",
			ContractSHA256:    privacy.PrivacyContractSemanticSHA256,
		},
	}
}

func (b *AppServerBridge) pseudonym(namespace, value string) string {
	hash := hmac.New(sha256.New, b.key)
	hash.Write([]byte(namespace))
	hash.Write([]byte{0})
	hash.Write([]byte(value))
	return "hmac-sha256:" + hex.EncodeToString(hash.Sum(nil))
}
