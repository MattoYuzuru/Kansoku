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
	"sync"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/privacy"
)

const (
	AppServerBridgeID        = "codex-app-server"
	AppServerBridgeVersion   = "0.1.0"
	AppServerProtocolVersion = "codex-app-server-jsonl"
	AppServerSchemaVersion   = "0.145.0"
)

// AppServerBridge translates the exact locally generated Codex App Server
// 0.145.0 JSONL surface into privacy.SafeRecord. Raw frames live only for the
// duration of projectFrame; content-bearing fields have no destination in
// either the bridge state or its sink.
type AppServerBridge struct {
	key                 []byte
	now                 func() time.Time
	mu                  sync.Mutex
	health              adaptersdk.BridgeHealth
	checkpoint          adaptersdk.BridgeCheckpoint
	pendingSkillsListID string
}

var bridgeSkillNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

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
			if err := sink.Accept(ctx, record); err != nil {
				b.setLifecycle(adaptersdk.BridgeDegraded, "sink_unavailable")
				return err
			}
		}
		b.mu.Lock()
		b.health.Lifecycle = adaptersdk.BridgeProducing
		b.health.AcceptedFrames += uint64(len(records))
		b.health.LastObservedAt = records[len(records)-1].ObservedAt
		b.checkpoint = adaptersdk.BridgeCheckpoint{
			Sequence: sequence, LastObservedAt: records[len(records)-1].ObservedAt, ReplaySupported: false,
		}
		b.mu.Unlock()
	}
	if err := scanner.Err(); err != nil {
		b.reject(ctx, sink, "frame_too_large_or_stream_error", 0)
		return errors.New("bridge_stream_error")
	}
	b.setLifecycle(adaptersdk.BridgeReconciled, "")
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
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func (b *AppServerBridge) projectFrame(raw []byte, sequence uint64) ([]privacy.SafeRecord, string) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope appServerEnvelope
	if err := decoder.Decode(&envelope); err != nil ||
		(envelope.Method == "" && len(envelope.Result) == 0) {
		return nil, "unknown_or_invalid_frame_schema"
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, "trailing_frame_json"
	}
	if envelope.Method == "" {
		return b.projectSkillsListResponse(envelope, sequence)
	}
	switch envelope.Method {
	case "skills/list":
		if len(envelope.ID) == 0 || len(envelope.Params) == 0 {
			return nil, "invalid_skills_list_request"
		}
		b.mu.Lock()
		b.pendingSkillsListID = string(envelope.ID)
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
		if params.Item.Type != "userMessage" {
			return nil, ""
		}
		observedAt := time.UnixMilli(params.StartedAtM)
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
		outcome := bridgeOutcome(params.Item.Status)
		record := b.safeRecord(
			params.Item.ID, params.ThreadID, params.TurnID, "tool.called",
			outcome, "mcp:"+params.Item.Server+"/"+params.Item.Tool, "mcp",
			time.UnixMilli(params.CompletedAtMS), sequence,
			privacy.RedactionCounts{ToolIOFields: 3, ExceptionFields: 1, SensitiveIdentifierFields: 1},
		)
		record.Telemetry.DurationMS = params.Item.DurationMS
		return []privacy.SafeRecord{record}, ""
	case "turn/completed", "skills/changed":
		return nil, ""
	default:
		return nil, "unsupported_bridge_method"
	}
}

func (b *AppServerBridge) projectSkillsListResponse(envelope appServerEnvelope, sequence uint64) ([]privacy.SafeRecord, string) {
	b.mu.Lock()
	expectedID := b.pendingSkillsListID
	if expectedID != "" && expectedID == string(envelope.ID) {
		b.pendingSkillsListID = ""
	}
	b.mu.Unlock()
	if expectedID == "" || expectedID != string(envelope.ID) {
		return nil, "unmatched_bridge_response"
	}
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
	now := b.now().UTC()
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
				"unknown", skill.Name, "skill", now, sequence,
				privacy.RedactionCounts{PathFields: 1, SourceFields: 1},
			))
		}
	}
	return records, ""
}

func bridgeOutcome(status string) string {
	switch status {
	case "completed":
		return "succeeded"
	case "failed", "declined":
		return "failed"
	case "cancelled":
		return "cancelled"
	default:
		return "unknown"
	}
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
	schemaFingerprint := b.pseudonym("app-server-schema/1", AppServerSchemaVersion)
	return privacy.SafeRecord{
		RecordID: recordID, IdempotencyKey: idempotency,
		AdapterID: AdapterID, AdapterVersion: AdapterVersion,
		SourceSchemaID:    AdapterID + ".bridge/" + AppServerSchemaVersion,
		SchemaFingerprint: schemaFingerprint,
		ObservedAt:        observedAt.UTC(), ReceivedAt: b.now().UTC(), Confidence: 1,
		EventType: eventType, Outcome: outcome, ValueState: privacy.ValueObserved,
		Model: privacy.CatalogObservation{State: privacy.ObservationNotObserved},
		Tool:  tool, ComponentKind: componentKind, RedactionCounts: redactions,
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
