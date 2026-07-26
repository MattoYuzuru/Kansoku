// Package fakebridge is a deliberately non-agent-shaped EvidenceBridge
// conformance fixture. Its pipe-delimited "constellation pulse" protocol
// proves a third adapter can participate without edits to core routing.
package fakebridge

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/privacy"
)

const (
	AdapterID     = "loomwright"
	BridgeID      = "star-pulse"
	Protocol      = "constellation-pulse"
	SchemaVersion = "7"
)

type Bridge struct {
	key        []byte
	now        func() time.Time
	mu         sync.Mutex
	health     adaptersdk.BridgeHealth
	checkpoint adaptersdk.BridgeCheckpoint
}

func New(key []byte, now func() time.Time) (*Bridge, error) {
	if len(key) < 32 {
		return nil, errors.New("invalid_fake_bridge_key")
	}
	if now == nil {
		now = time.Now
	}
	return &Bridge{
		key: append([]byte(nil), key...), now: now,
		health: adaptersdk.BridgeHealth{
			Lifecycle: adaptersdk.BridgeDiscovered, Compatible: true,
		},
	}, nil
}

var _ adaptersdk.EvidenceBridge = (*Bridge)(nil)

func (b *Bridge) Manifest() adaptersdk.BridgeManifest {
	return adaptersdk.BridgeManifest{
		APIVersion: adaptersdk.EvidenceBridgeAPIVersion,
		AdapterID:  AdapterID, BridgeID: BridgeID, BridgeVersion: "9.4.1",
		SupportedAgentVersions: []string{"9.4.1"},
		ProtocolVersions:       []string{Protocol}, SchemaVersions: []string{SchemaVersion},
		Capabilities: []adaptersdk.CapabilityID{
			adaptersdk.CapabilityActivitySessions,
			adaptersdk.CapabilityIngestionEvidenceBridge,
		},
		SafeFields:         []string{"event_type", "observed_at", "session_pseudonym"},
		ProhibitedSurfaces: []string{"dream_text", "raw_orbit_path"},
		Permissions:        adaptersdk.Permissions{Network: adaptersdk.NetworkLoopbackOnly},
		TargetScope:        "explicit_local", MaxFrameBytes: 4096, MaxFrames: 100,
		ConnectTimeout: 5 * time.Second, MaxReconnects: 1,
		IdempotencyStrategy: "pulse_native_id",
		CheckpointStrategy:  "monotonic_frame_sequence",
		FixtureID:           "constellation-seven", CanaryID: "star-pulse-noop",
	}
}

func (b *Bridge) Probe(_ context.Context, _ *adaptersdk.HostView, installation adaptersdk.Installation) (adaptersdk.BridgeHealth, error) {
	if installation.AdapterID != AdapterID {
		return b.Health(context.Background()), adaptersdk.ErrIncompatibleBridgeTarget
	}
	b.mu.Lock()
	b.health.Lifecycle = adaptersdk.BridgeConfigured
	b.mu.Unlock()
	return b.Health(context.Background()), nil
}

func (b *Bridge) Connect(ctx context.Context, target adaptersdk.BridgeTarget, sink adaptersdk.SafeAssertionSink) error {
	if err := adaptersdk.ValidateBridgeManifest(b.Manifest()); err != nil {
		return err
	}
	if target.Installation.AdapterID != AdapterID || target.Protocol != Protocol ||
		target.SchemaVersion != SchemaVersion || target.Frames == nil || sink == nil {
		return adaptersdk.ErrIncompatibleBridgeTarget
	}
	b.mu.Lock()
	b.health.Lifecycle = adaptersdk.BridgeConnected
	b.mu.Unlock()
	scanner := bufio.NewScanner(target.Frames)
	scanner.Buffer(make([]byte, 256), b.Manifest().MaxFrameBytes)
	var sequence uint64
	for scanner.Scan() {
		sequence++
		fields := strings.Split(scanner.Text(), "|")
		if len(fields) != 5 || fields[0] != "pulse" {
			if err := b.reject(ctx, sink, "unknown_constellation_schema", int64(len(scanner.Bytes()))); err != nil {
				return err
			}
			continue
		}
		observedUnix, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil || fields[1] == "" || fields[2] == "" || fields[3] != "awake" {
			if err := b.reject(ctx, sink, "invalid_constellation_pulse", int64(len(scanner.Bytes()))); err != nil {
				return err
			}
			continue
		}
		record := b.record(fields[1], fields[2], time.Unix(observedUnix, 0))
		if err := sink.Accept(ctx, record); err != nil {
			return err
		}
		b.mu.Lock()
		b.health.Lifecycle = adaptersdk.BridgeProducing
		b.health.AcceptedFrames++
		b.health.LastObservedAt = record.ObservedAt
		b.checkpoint = adaptersdk.BridgeCheckpoint{
			Sequence: sequence, LastObservedAt: record.ObservedAt,
		}
		b.mu.Unlock()
	}
	if err := scanner.Err(); err != nil {
		return errors.New("fake_bridge_stream_error")
	}
	b.mu.Lock()
	b.health.Lifecycle = adaptersdk.BridgeReconciled
	b.mu.Unlock()
	return nil
}

func (b *Bridge) Checkpoint(context.Context) adaptersdk.BridgeCheckpoint {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.checkpoint
}

func (b *Bridge) Health(context.Context) adaptersdk.BridgeHealth {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.health
}

func (b *Bridge) reject(ctx context.Context, sink adaptersdk.SafeAssertionSink, category string, bytes int64) error {
	if err := sink.Reject(ctx, adaptersdk.BridgeRejection{
		BridgeID: BridgeID, SchemaVersion: SchemaVersion,
		SchemaFingerprint: b.pseudonym("schema", SchemaVersion),
		Category:          category, ByteCount: bytes, ObservedAt: b.now().UTC(),
	}); err != nil {
		return err
	}
	b.mu.Lock()
	b.health.Lifecycle = adaptersdk.BridgeDegraded
	b.health.RejectedFrames++
	b.health.Category = category
	b.mu.Unlock()
	return nil
}

func (b *Bridge) record(sessionID, eventID string, observedAt time.Time) privacy.SafeRecord {
	sourceID := b.pseudonym("source-record/1", AdapterID+"\x00"+eventID)
	session := b.pseudonym("session/1", AdapterID+"\x00"+sessionID)
	schema := b.pseudonym("schema", SchemaVersion)
	return privacy.SafeRecord{
		RecordID:       b.pseudonym("record/1", AdapterID+"\x00"+sessionID+"\x00"+eventID+"\x000"),
		IdempotencyKey: b.pseudonym("idempotency/1", eventID+"\x00"+observedAt.UTC().Format(time.RFC3339Nano)),
		AdapterID:      AdapterID, AdapterVersion: "9.4.1",
		SourceSchemaID:    AdapterID + ".bridge/" + SchemaVersion,
		SchemaFingerprint: schema, ObservedAt: observedAt.UTC(),
		ReceivedAt: b.now().UTC(), Confidence: 1,
		EventType: "source.observed", Outcome: "unknown",
		ValueState: privacy.ValueObserved,
		Model:      privacy.CatalogObservation{State: privacy.ObservationNotObserved},
		Tool:       privacy.CatalogObservation{State: privacy.ObservationNotObserved},
		Lineage: privacy.Lineage{
			SourceRecordPseudonym: sourceID, SessionPseudonym: session,
			AdapterID: AdapterID, AdapterVersion: "9.4.1",
			SourceSchemaID:    AdapterID + ".bridge/" + SchemaVersion,
			SchemaFingerprint: schema,
			SanitizerVersion:  "kansoku.ingress-sanitizer/1",
			ContractSHA256:    privacy.PrivacyContractSemanticSHA256,
		},
	}
}

func (b *Bridge) pseudonym(namespace, value string) string {
	hash := hmac.New(sha256.New, b.key)
	hash.Write([]byte(namespace))
	hash.Write([]byte{0})
	hash.Write([]byte(value))
	return "hmac-sha256:" + hex.EncodeToString(hash.Sum(nil))
}
