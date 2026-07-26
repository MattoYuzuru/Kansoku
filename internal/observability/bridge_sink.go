package observability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync/atomic"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/privacy"
)

// BridgeAssertionSink adapts the generic adapter SDK safe sink to the one
// canonical Ingestor transaction path. It owns no protocol or brand logic.
type BridgeAssertionSink struct {
	ingestor *Ingestor
	sequence atomic.Uint64
}

func NewBridgeAssertionSink(ingestor *Ingestor) (*BridgeAssertionSink, error) {
	if ingestor == nil {
		return nil, errors.New("bridge_ingestor_required")
	}
	return &BridgeAssertionSink{ingestor: ingestor}, nil
}

var _ adaptersdk.SafeAssertionSink = (*BridgeAssertionSink)(nil)

func (s *BridgeAssertionSink) Accept(_ context.Context, record privacy.SafeRecord) error {
	_, err := s.ingestor.IngestSanitizedBridgeRecord(record, s.sequence.Add(1))
	return err
}

func (s *BridgeAssertionSink) Reject(_ context.Context, rejection adaptersdk.BridgeRejection) error {
	if rejection.BridgeID == "" || rejection.SchemaVersion == "" ||
		rejection.SchemaFingerprint == "" || rejection.Category == "" ||
		rejection.ByteCount < 0 || rejection.ObservedAt.IsZero() {
		return errors.New("invalid_bridge_rejection")
	}
	hash := sha256.Sum256([]byte(
		rejection.BridgeID + "\x00" + rejection.SchemaVersion + "\x00" +
			rejection.SchemaFingerprint + "\x00" + rejection.Category,
	))
	kind := SourceEvidenceBridge
	quarantine := Quarantine{
		QuarantineID: "qua_" + hex.EncodeToString(hash[:])[:32],
		SourceKind:   kind, SchemaFingerprint: rejection.SchemaFingerprint,
		Category: rejection.Category, ByteCount: rejection.ByteCount,
		RecordCount: 1, ObservedAt: rejection.ObservedAt.UTC(),
	}
	incident := NewIncident(rejection.Category, kind, rejection.ObservedAt.UTC())
	_, err := s.ingestor.store.Commit(CommitRequest{
		Quarantine: &quarantine, Incident: &incident,
	})
	if err != nil {
		return err
	}
	s.ingestor.sinkMu.RLock()
	durable := s.ingestor.durableSink
	s.ingestor.sinkMu.RUnlock()
	if metadata, ok := durable.(DurableMetadataSink); ok {
		return metadata.PersistQuarantineMetadata(quarantine, incident)
	}
	return nil
}
