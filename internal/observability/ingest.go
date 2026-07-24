package observability

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"kansoku.local/kansoku/internal/privacy"
)

type Ingestor struct {
	store       *FileStore
	sanitizer   *privacy.IngressSanitizer
	capacity    chan struct{}
	identityKey []byte
	now         func() time.Time
	sinkMu      sync.RWMutex
	durableSink DurableFactSink
}

// DurableFactSink is the production handoff from the normalized ingress
// pipeline into the system of record. The sink receives only the closed,
// privacy-safe Event/Evidence pair; it never receives the raw request.
type DurableFactSink interface {
	PersistNormalizedFact(Event, Evidence) error
}

var ErrDurableFactSink = errors.New("durable_fact_sink_failed")

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

// SyntheticProbeIdentity is a minimal, read-only export of keyedIdentity so a
// caller in the same process (internal/integrity's stage_5 synthetic
// pipeline probe) can deterministically recompute the SAME pseudonymized
// identity this Ingestor's sanitizer/normalizer would derive for a literal
// tag value it controls (e.g. a reserved test-namespace session_id/event_id
// string), without internal/integrity forking a second copy of the HMAC
// derivation, the sanitizer's private key, or privacy.SafeRecord's
// pseudonymization logic. It exposes no ingestion behavior, only the
// identity function already used to key watermarks/idempotency internally.
func (i *Ingestor) SyntheticProbeIdentity(namespace, value string) string {
	// privacy.IngressSanitizer prefixes persisted pseudonyms with the
	// algorithm identifier. Preserve that exact durable representation here
	// so callers can select only records created from their reserved literal
	// probe IDs.
	return "hmac-sha256:" + i.keyedIdentity(namespace, value)
}

func (i *Ingestor) SetClockForTest(now func() time.Time) {
	i.now = now
	i.sanitizer.SetClockForTest(now)
}

// ConfigureDurableFactSink wires the production system-of-record handoff
// exactly once. Refusing replacement prevents a test probe or late caller
// from silently swapping out the sink used by public ingress.
func (i *Ingestor) ConfigureDurableFactSink(sink DurableFactSink) error {
	if sink == nil {
		return errors.New("durable_fact_sink_required")
	}
	i.sinkMu.Lock()
	defer i.sinkMu.Unlock()
	if i.durableSink != nil {
		return errors.New("durable_fact_sink_already_configured")
	}
	i.durableSink = sink
	return nil
}

func (i *Ingestor) HasDurableFactSink() bool {
	i.sinkMu.RLock()
	defer i.sinkMu.RUnlock()
	return i.durableSink != nil
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
	result, err := i.store.Commit(request)
	if err != nil {
		return result, err
	}
	i.sinkMu.RLock()
	sink := i.durableSink
	i.sinkMu.RUnlock()
	if sink != nil {
		if err := sink.PersistNormalizedFact(event, evidence); err != nil {
			return result, ErrDurableFactSink
		}
	}
	return result, nil
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
