package adaptersdk_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/privacy"
)

type bridgeFixture struct {
	manifest adaptersdk.BridgeManifest
	health   adaptersdk.BridgeHealth
}

func (b *bridgeFixture) Manifest() adaptersdk.BridgeManifest { return b.manifest }
func (b *bridgeFixture) Probe(context.Context, *adaptersdk.HostView, adaptersdk.Installation) (adaptersdk.BridgeHealth, error) {
	return b.health, nil
}
func (b *bridgeFixture) Connect(context.Context, adaptersdk.BridgeTarget, adaptersdk.SafeAssertionSink) error {
	return nil
}
func (b *bridgeFixture) Checkpoint(context.Context) adaptersdk.BridgeCheckpoint {
	return adaptersdk.BridgeCheckpoint{}
}
func (b *bridgeFixture) Health(context.Context) adaptersdk.BridgeHealth { return b.health }

func validBridgeManifest() adaptersdk.BridgeManifest {
	return adaptersdk.BridgeManifest{
		APIVersion: adaptersdk.EvidenceBridgeAPIVersion,
		AdapterID:  "loomwright", BridgeID: "star-pulse", BridgeVersion: "9.4.1",
		SupportedAgentVersions: []string{"9.4.0", "9.4.1"},
		ProtocolVersions:       []string{"pulse-3"}, SchemaVersions: []string{"constellation-7"},
		Capabilities: []adaptersdk.CapabilityID{
			adaptersdk.CapabilityIngestionEvidenceBridge,
		},
		SafeFields:         []string{"event_type", "observed_at", "session_pseudonym"},
		ProhibitedSurfaces: []string{"dream_text", "raw_orbit_path"},
		Permissions:        adaptersdk.Permissions{Network: adaptersdk.NetworkLoopbackOnly},
		TargetScope:        "explicit_local", MaxFrameBytes: 4096, MaxFrames: 100,
		ConnectTimeout: 5 * time.Second, MaxReconnects: 2,
		IdempotencyStrategy: "native_pulse_id",
		CheckpointStrategy:  "monotonic_pulse_sequence",
		FixtureID:           "constellation-seven", CanaryID: "star-pulse-noop",
	}
}

func TestEvidenceBridgeManifestIsGenericBoundedAndBrandAgnostic(t *testing.T) {
	manifest := validBridgeManifest()
	if err := adaptersdk.ValidateBridgeManifest(manifest); err != nil {
		t.Fatalf("valid generic bridge manifest: %v", err)
	}
	for _, mutate := range []func(*adaptersdk.BridgeManifest){
		func(m *adaptersdk.BridgeManifest) { m.APIVersion = "future" },
		func(m *adaptersdk.BridgeManifest) { m.Permissions.Network = adaptersdk.NetworkNone },
		func(m *adaptersdk.BridgeManifest) { m.Permissions.FilesystemRead = []string{"/"} },
		func(m *adaptersdk.BridgeManifest) { m.MaxFrameBytes = 2 << 20 },
		func(m *adaptersdk.BridgeManifest) { m.MaxReconnects = 99 },
		func(m *adaptersdk.BridgeManifest) { m.SafeFields = nil },
	} {
		invalid := manifest
		mutate(&invalid)
		if err := adaptersdk.ValidateBridgeManifest(invalid); err == nil {
			t.Fatalf("invalid bridge manifest accepted: %#v", invalid)
		}
	}
}

func TestMemoryAssertionSinkHasOnlyTypedSafeRecordsAndMetadataRejections(t *testing.T) {
	sink := &adaptersdk.MemoryAssertionSink{}
	record := privacy.SafeRecord{
		RecordID: "hmac-sha256:safe", EventType: "source.observed",
	}
	rejection := adaptersdk.BridgeRejection{
		BridgeID: "star-pulse", SchemaVersion: "constellation-7",
		SchemaFingerprint: "hmac-sha256:fingerprint", Category: "unknown_schema",
		ByteCount: 42, ObservedAt: time.Unix(1, 0),
	}
	if err := sink.Accept(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if err := sink.Reject(context.Background(), rejection); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sink.Records(), []privacy.SafeRecord{record}) ||
		!reflect.DeepEqual(sink.Rejections(), []adaptersdk.BridgeRejection{rejection}) {
		t.Fatal("memory sink changed typed bridge values")
	}
}
