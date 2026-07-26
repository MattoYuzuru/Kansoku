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
	key        []byte
	now        func() time.Time
	mu         sync.Mutex
	health     adaptersdk.BridgeHealth
	checkpoint adaptersdk.BridgeCheckpoint
}

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
		record, emit, category := b.projectFrame(scanner.Bytes(), sequence)
		if category != "" {
			if err := b.reject(ctx, sink, category, int64(len(scanner.Bytes()))); err != nil {
				return err
			}
			continue
		}
		if !emit {
			continue
		}
		if err := sink.Accept(ctx, record); err != nil {
			b.setLifecycle(adaptersdk.BridgeDegraded, "sink_unavailable")
			return err
		}
		b.mu.Lock()
		b.health.Lifecycle = adaptersdk.BridgeProducing
		b.health.AcceptedFrames++
		b.health.LastObservedAt = record.ObservedAt
		b.checkpoint = adaptersdk.BridgeCheckpoint{
			Sequence: sequence, LastObservedAt: record.ObservedAt, ReplaySupported: false,
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
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (b *AppServerBridge) projectFrame(raw []byte, sequence uint64) (privacy.SafeRecord, bool, string) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope appServerEnvelope
	if err := decoder.Decode(&envelope); err != nil || envelope.Method == "" || len(envelope.Params) == 0 {
		return privacy.SafeRecord{}, false, "unknown_or_invalid_frame_schema"
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return privacy.SafeRecord{}, false, "trailing_frame_json"
	}
	switch envelope.Method {
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
			return privacy.SafeRecord{}, false, "invalid_thread_started"
		}
		return b.safeRecord(
			params.Thread.ID, params.Thread.SessionID, "", "session.started",
			"unknown", "", "", time.Unix(params.Thread.CreatedAt, 0), sequence,
			privacy.RedactionCounts{PromptFields: 1, PathFields: 2, SourceFields: 1},
		), true, ""
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
			return privacy.SafeRecord{}, false, "invalid_turn_started"
		}
		return b.safeRecord(
			params.Turn.ID, params.ThreadID, params.Turn.ID, "prompt.submitted",
			"unknown", "", "", time.Unix(*params.Turn.StartedAt, 0), sequence,
			privacy.RedactionCounts{PromptFields: 1},
		), true, ""
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
			return privacy.SafeRecord{}, false, ""
		}
		if params.ThreadID == "" || params.TurnID == "" || params.Item.ID == "" ||
			params.Item.Server == "" || params.Item.Tool == "" || params.CompletedAtMS <= 0 ||
			len(params.Item.Server)+len(params.Item.Tool) > 120 {
			return privacy.SafeRecord{}, false, "invalid_mcp_item_completed"
		}
		outcome := bridgeOutcome(params.Item.Status)
		record := b.safeRecord(
			params.Item.ID, params.ThreadID, params.TurnID, "tool.called",
			outcome, "mcp:"+params.Item.Server+"/"+params.Item.Tool, "mcp",
			time.UnixMilli(params.CompletedAtMS), sequence,
			privacy.RedactionCounts{ToolIOFields: 3, ExceptionFields: 1, SensitiveIdentifierFields: 1},
		)
		record.Telemetry.DurationMS = params.Item.DurationMS
		return record, true, ""
	case "item/started", "turn/completed", "skills/changed":
		return privacy.SafeRecord{}, false, ""
	default:
		return privacy.SafeRecord{}, false, "unsupported_bridge_method"
	}
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
