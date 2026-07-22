package observability

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"kansoku.local/kansoku/internal/privacy"
)

type Ingestor struct {
	store       *FileStore
	sanitizer   *privacy.IngressSanitizer
	capacity    chan struct{}
	identityKey []byte
	now         func() time.Time
}

func NewIngestor(store *FileStore, identityKey []byte, limits privacy.Limits, concurrent int) (*Ingestor, error) {
	if store == nil || concurrent <= 0 || concurrent > 128 {
		return nil, errors.New("invalid_ingestor_configuration")
	}
	sanitizer, err := privacy.NewIngressSanitizer(identityKey, limits)
	if err != nil {
		return nil, err
	}
	return &Ingestor{store: store, sanitizer: sanitizer, capacity: make(chan struct{}, concurrent), identityKey: append([]byte(nil), identityKey...), now: time.Now}, nil
}

func (i *Ingestor) keyedIdentity(namespace, value string) string {
	digest := hmac.New(sha256.New, i.identityKey)
	digest.Write([]byte(namespace))
	digest.Write([]byte{0})
	digest.Write([]byte(value))
	return hex.EncodeToString(digest.Sum(nil))
}

func (i *Ingestor) SetClockForTest(now func() time.Time) {
	i.now = now
	i.sanitizer.SetClockForTest(now)
}

func (i *Ingestor) acquire() bool {
	select {
	case i.capacity <- struct{}{}:
		return true
	default:
		return false
	}
}

func (i *Ingestor) release() { <-i.capacity }

func (i *Ingestor) IngestHook(raw []byte, sequence uint64) (CommitResult, error) {
	return i.ingestJSON(raw, SourceHook, sequence, nil)
}

func (i *Ingestor) ingestJSON(raw []byte, kind SourceKind, sequence uint64, checkpoint *Checkpoint) (CommitResult, error) {
	if !i.acquire() {
		return CommitResult{}, ErrBackpressure
	}
	defer i.release()
	records, safeErr := i.sanitizer.DecodeAndExtract(bytes.NewReader(raw), privacy.FixtureSourceSchema())
	if safeErr != nil {
		quarantine := Quarantine{QuarantineID: safeErr.IncidentID, SourceKind: kind, SchemaFingerprint: safeErr.SchemaFingerprint, Category: safeErr.Category, ByteCount: safeErr.TotalBytes, RecordCount: safeErr.RecordCount, ObservedAt: i.now().UTC()}
		incident := NewIncident(safeErr.Category, kind, i.now())
		_, commitErr := i.store.Commit(CommitRequest{Quarantine: &quarantine, Incident: &incident, Checkpoint: checkpoint})
		if commitErr != nil {
			return CommitResult{}, commitErr
		}
		return CommitResult{}, safeErr
	}
	if len(records) != 1 {
		return CommitResult{}, errors.New("single_record_required")
	}
	return i.ingestSafe(records[0], kind, sequence, checkpoint)
}

func (i *Ingestor) ingestSafe(record privacy.SafeRecord, kind SourceKind, sequence uint64, checkpoint *Checkpoint) (CommitResult, error) {
	now := i.now().UTC()
	event, evidence, err := NormalizedFromSafe(record, kind, sequence, now)
	if err != nil {
		return CommitResult{}, err
	}
	correlation := Correlate(event, nil)
	event.CorrelationStatus = correlation.Status
	event.Lifecycle = append(event.Lifecycle, StageDeduped, StageCorrelated, StageReconciled)
	snapshot := i.store.Snapshot()
	watermark := snapshot.Watermarks[string(kind)]
	watermark.SourceID = string(kind)
	watermark.Lifecycle = SourceProducing
	if watermark.LastDiscovered.IsZero() {
		watermark.LastDiscovered = now
	}
	if watermark.LastReadSequence != 0 && sequence > watermark.LastReadSequence+1 {
		watermark.GapCount += sequence - watermark.LastReadSequence - 1
	}
	if sequence > watermark.LastReadSequence {
		watermark.LastReadSequence = sequence
	}
	if sequence > watermark.LastEmittedSequence {
		watermark.LastEmittedSequence = sequence
	}
	if event.ObservedAt.After(watermark.LastObserved) {
		watermark.LastObserved = event.ObservedAt
	}
	watermark.LastCommitted = now
	watermark.LastEligibleActivity = now
	watermark.ExpectedCadenceMS = 30_000
	request := CommitRequest{Event: &event, Evidence: &evidence, Correlation: &correlation, Checkpoint: checkpoint, Watermark: &watermark}
	if existing, ok := snapshot.Facts[event.FactKey]; ok && (existing.Event.Outcome != event.Outcome || existing.Event.ValueState != event.ValueState || existing.Event.EventType != event.EventType) {
		incident := NewIncident("evidence_contradiction", kind, now)
		request.Incident = &incident
	}
	return i.store.Commit(request)
}

func (i *Ingestor) IngestUnknown(kind SourceKind, schemaFingerprint string, byteCount int64, recordCount int) error {
	if schemaFingerprint == "" {
		return errors.New("unknown_schema_fingerprint_required")
	}
	now := i.now().UTC()
	quarantine := Quarantine{QuarantineID: "qua_" + stableID("quarantine/1", string(kind), schemaFingerprint)[:32], SourceKind: kind, SchemaFingerprint: schemaFingerprint, Category: "unknown_schema", ByteCount: byteCount, RecordCount: recordCount, ObservedAt: now}
	incident := NewIncident("unknown_schema", kind, now)
	watermark := Watermark{SourceID: string(kind), Lifecycle: SourceDegraded, LastDiscovered: now, LastObserved: now, LastCommitted: now, ExpectedCadenceMS: 30_000, GapCount: 1}
	_, err := i.store.Commit(CommitRequest{Quarantine: &quarantine, Incident: &incident, Watermark: &watermark})
	return err
}

func (i *Ingestor) IngestSafeFields(fields map[string]any, kind SourceKind, sequence uint64) (CommitResult, error) {
	allowed := map[string]bool{"event_id": true, "session_id": true, "observed_at": true, "event_type": true, "outcome": true, "value_state": true, "model": true, "tool_name": true}
	for key := range fields {
		if !allowed[key] {
			return CommitResult{}, errors.New("unsafe_otlp_field")
		}
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return CommitResult{}, errors.New("safe_field_encoding_failure")
	}
	return i.ingestJSON(raw, kind, sequence, nil)
}

func (i *Ingestor) SetSourceLifecycle(kind SourceKind, lifecycle SourceLifecycle, eligible bool) error {
	now := i.now().UTC()
	existing := i.store.Snapshot().Watermarks[string(kind)]
	existing.SourceID = string(kind)
	existing.Lifecycle = lifecycle
	existing.Inactivity = !eligible
	existing.LastCommitted = now
	_, err := i.store.Commit(CommitRequest{Watermark: &existing})
	return err
}

func (i *Ingestor) AuditSource(kind SourceKind, eligible bool) error {
	now := i.now().UTC()
	existing, exists := i.store.Snapshot().Watermarks[string(kind)]
	if !exists {
		existing = Watermark{SourceID: string(kind), Lifecycle: SourceDiscovered, LastDiscovered: now, ExpectedCadenceMS: 30_000}
	}
	if !eligible {
		existing.Inactivity = true
		existing.LastEligibleActivity = time.Time{}
		_, err := i.store.Commit(CommitRequest{Watermark: &existing})
		return err
	}
	existing.Inactivity = false
	existing.LastEligibleActivity = now
	if !existing.LastObserved.IsZero() && now.Sub(existing.LastObserved) > time.Duration(existing.ExpectedCadenceMS)*time.Millisecond {
		existing.Lifecycle = SourceDegraded
		existing.GapCount++
		incident := NewIncident("watermark_stall", kind, now)
		_, err := i.store.Commit(CommitRequest{Watermark: &existing, Incident: &incident})
		return err
	}
	_, err := i.store.Commit(CommitRequest{Watermark: &existing})
	return err
}
