package adaptersdk

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync"
	"time"

	"kansoku.local/kansoku/internal/privacy"
)

const EvidenceBridgeAPIVersion = "kansoku.evidence-bridge/v1"

// BridgeManifest is the closed, data-only contract core uses to supervise an
// adapter-owned rich-evidence lane. Protocol translation remains inside the
// adapter; none of these fields can carry a payload, database handle or
// unrestricted host capability.
type BridgeManifest struct {
	APIVersion             string
	AdapterID              string
	BridgeID               string
	BridgeVersion          string
	SupportedAgentVersions []string
	ProtocolVersions       []string
	SchemaVersions         []string
	Capabilities           []CapabilityID
	SafeFields             []string
	ProhibitedSurfaces     []string
	Permissions            Permissions
	TargetScope            string
	MaxFrameBytes          int
	MaxFrames              int
	ConnectTimeout         time.Duration
	MaxReconnects          int
	IdempotencyStrategy    string
	CheckpointStrategy     string
	FixtureID              string
	CanaryID               string
}

// BridgeTarget is an explicitly selected local stream and installation. A
// bridge cannot discover arbitrary processes or receive database credentials.
type BridgeTarget struct {
	Installation  Installation
	Protocol      string
	SchemaVersion string
	Frames        io.Reader
}

type BridgeCheckpoint struct {
	Sequence        uint64
	LastObservedAt  time.Time
	ReplaySupported bool
}

type BridgeLifecycle string

const (
	BridgeDiscovered BridgeLifecycle = "discovered"
	BridgeConfigured BridgeLifecycle = "configured"
	BridgeConnected  BridgeLifecycle = "connected"
	BridgeProducing  BridgeLifecycle = "producing"
	BridgeReconciled BridgeLifecycle = "reconciled"
	BridgeDegraded   BridgeLifecycle = "degraded"
	BridgeDisabled   BridgeLifecycle = "disabled"
)

type BridgeHealth struct {
	Lifecycle      BridgeLifecycle
	Compatible     bool
	LastObservedAt time.Time
	AcceptedFrames uint64
	RejectedFrames uint64
	GapCount       uint64
	Category       string
}

// BridgeRejection is metadata-only by construction. It deliberately has no
// raw frame, decoder error, message, path, URI, arguments or result field.
type BridgeRejection struct {
	BridgeID          string
	SchemaVersion     string
	SchemaFingerprint string
	Category          string
	ByteCount         int64
	ObservedAt        time.Time
}

// SafeAssertionSink is the only write surface exposed to a bridge. SafeRecord
// is the repository-wide persistence allowlist and Reject accepts only
// structural metadata.
type SafeAssertionSink interface {
	Accept(context.Context, privacy.SafeRecord) error
	Reject(context.Context, BridgeRejection) error
}

type EvidenceBridge interface {
	Manifest() BridgeManifest
	Probe(context.Context, *HostView, Installation) (BridgeHealth, error)
	Connect(context.Context, BridgeTarget, SafeAssertionSink) error
	Checkpoint(context.Context) BridgeCheckpoint
	Health(context.Context) BridgeHealth
}

var (
	ErrInvalidBridgeManifest    = errors.New("invalid_bridge_manifest")
	ErrIncompatibleBridgeTarget = errors.New("incompatible_bridge_target")
)

func ValidateBridgeManifest(m BridgeManifest) error {
	if m.APIVersion != EvidenceBridgeAPIVersion || !validAdapterID(m.AdapterID) ||
		!validAdapterID(m.BridgeID) || m.BridgeVersion == "" || m.TargetScope != "explicit_local" ||
		m.MaxFrameBytes < 1024 || m.MaxFrameBytes > 1<<20 || m.MaxFrames < 1 ||
		m.MaxFrames > 100_000 || m.ConnectTimeout <= 0 || m.ConnectTimeout > time.Minute ||
		m.MaxReconnects < 0 || m.MaxReconnects > 10 || m.Permissions.Network != NetworkLoopbackOnly ||
		len(m.Permissions.FilesystemRead) != 0 || len(m.SafeFields) == 0 ||
		len(m.ProhibitedSurfaces) == 0 || m.IdempotencyStrategy == "" ||
		m.CheckpointStrategy == "" || m.FixtureID == "" || m.CanaryID == "" {
		return ErrInvalidBridgeManifest
	}
	for _, capability := range m.Capabilities {
		if !validCapabilityID(capability) {
			return ErrInvalidBridgeManifest
		}
	}
	for _, values := range [][]string{
		m.SupportedAgentVersions, m.ProtocolVersions, m.SchemaVersions,
		m.SafeFields, m.ProhibitedSurfaces,
	} {
		if len(values) == 0 || !sortedUniqueBounded(values) {
			return ErrInvalidBridgeManifest
		}
	}
	return nil
}

func sortedUniqueBounded(values []string) bool {
	if len(values) > MaxManifestConfigEntries {
		return false
	}
	copyValues := append([]string(nil), values...)
	sort.Strings(copyValues)
	for index, value := range copyValues {
		if value == "" || len(value) > MaxManifestConfigString ||
			index > 0 && copyValues[index-1] == value {
			return false
		}
	}
	return true
}

// MemoryAssertionSink is a deterministic conformance sink. It stores only the
// same typed safe values a production sink accepts and is useful to adapter
// authors without exposing any core database API.
type MemoryAssertionSink struct {
	mu         sync.Mutex
	records    []privacy.SafeRecord
	rejections []BridgeRejection
}

func (s *MemoryAssertionSink) Accept(_ context.Context, record privacy.SafeRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	return nil
}

func (s *MemoryAssertionSink) Reject(_ context.Context, rejection BridgeRejection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejections = append(s.rejections, rejection)
	return nil
}

func (s *MemoryAssertionSink) Records() []privacy.SafeRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]privacy.SafeRecord(nil), s.records...)
}

func (s *MemoryAssertionSink) Rejections() []BridgeRejection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]BridgeRejection(nil), s.rejections...)
}
