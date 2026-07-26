package observability

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
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

// DurableFactReservation lets a bounded production sink reserve capacity
// before the local mirror is committed. Commit may acknowledge only after the
// system of record or its fsynced sanitized spool owns the fact.
type DurableFactReservation interface {
	Commit() error
	Cancel()
}

type ReservingDurableFactSink interface {
	DurableFactSink
	ReserveNormalizedFact(Event, Evidence) (DurableFactReservation, error)
}

type DurableMetadataSink interface {
	PersistQuarantineMetadata(Quarantine, Incident) error
}

var (
	ErrDurableFactSink       = errors.New("durable_fact_sink_failed")
	ErrDurabilityUnavailable = errors.New("durability_unavailable_retryable")
)

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
		// safeErr.IncidentID is privacy.IngressSanitizer's own
		// "hmac-sha256:"-prefixed pseudonym (see (*IngressSanitizer).safeError),
		// distinct from this store's closed Quarantine.QuarantineID identifier
		// shape ("qua_" + 32 lowercase hex characters, enforced by
		// state_validation.go's hex32IDPattern). IngestUnknown already derives
		// its own store-shaped "qua_"-prefixed id rather than reusing a
		// caller-supplied identity string verbatim; do the same here instead
		// of passing the sanitizer's differently-shaped pseudonym straight
		// through as the store's primary key -- this stayed unnoticed because
		// no prior caller of ingestJSON's quarantine branch ever fed
		// genuinely-rejected (non-fixture-schema) JSON through it until real
		// adapter OTLP records started producing dotted canonical event types
		// FixtureSourceSchema()'s enum never declared.
		quarantineID := "qua_" + stableID("quarantine/1", safeErr.SourceSchemaID, safeErr.SchemaFingerprint, safeErr.Category, safeErr.FieldPath)[:32]
		quarantine := Quarantine{QuarantineID: quarantineID, SourceKind: kind, SchemaFingerprint: safeErr.SchemaFingerprint, Category: safeErr.Category, ByteCount: safeErr.TotalBytes, RecordCount: safeErr.RecordCount, ObservedAt: i.now().UTC()}
		incident := NewSchemaIncident(safeErr.Category, kind, safeErr.SchemaFingerprint, i.now())
		_, commitErr := i.store.Commit(CommitRequest{Quarantine: &quarantine, Incident: &incident, Checkpoint: checkpoint})
		if commitErr != nil {
			return CommitResult{}, commitErr
		}
		i.sinkMu.RLock()
		sink := i.durableSink
		i.sinkMu.RUnlock()
		if metadata, ok := sink.(DurableMetadataSink); ok {
			if err := metadata.PersistQuarantineMetadata(quarantine, incident); err != nil {
				return CommitResult{}, ErrDurabilityUnavailable
			}
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
	i.sinkMu.RLock()
	sink := i.durableSink
	i.sinkMu.RUnlock()
	var reservation DurableFactReservation
	if reserving, ok := sink.(ReservingDurableFactSink); ok {
		reservation, err = reserving.ReserveNormalizedFact(event, evidence)
		if err != nil {
			if errors.Is(err, ErrBackpressure) || errors.Is(err, ErrDurabilityUnavailable) {
				return CommitResult{}, err
			}
			return CommitResult{}, ErrDurableFactSink
		}
		defer reservation.Cancel()
	}
	result, err := i.store.Commit(request)
	if err != nil {
		return result, err
	}
	if reservation != nil {
		if err := reservation.Commit(); err != nil {
			if errors.Is(err, ErrBackpressure) || errors.Is(err, ErrDurabilityUnavailable) {
				return result, err
			}
			return result, ErrDurableFactSink
		}
	} else if sink != nil {
		if err := sink.PersistNormalizedFact(event, evidence); err != nil {
			if errors.Is(err, ErrBackpressure) || errors.Is(err, ErrDurabilityUnavailable) {
				return result, err
			}
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
	incident := NewSchemaIncident("unknown_schema", kind, schemaFingerprint, now)
	watermark := Watermark{SourceID: string(kind), Lifecycle: SourceDegraded, LastDiscovered: now, LastObserved: now, LastCommitted: now, ExpectedCadenceMS: 30_000, GapCount: 1}
	if _, err := i.store.Commit(CommitRequest{Quarantine: &quarantine, Incident: &incident, Watermark: &watermark}); err != nil {
		return err
	}
	i.sinkMu.RLock()
	sink := i.durableSink
	i.sinkMu.RUnlock()
	if metadata, ok := sink.(DurableMetadataSink); ok {
		if err := metadata.PersistQuarantineMetadata(quarantine, incident); err != nil {
			return ErrDurabilityUnavailable
		}
	}
	return nil
}

// IngestSafeFields is the real-adapter OTLP dispatch boundary (Gap A):
// otlp.go's ingestOneRecord has already translated a matched adapter's
// native attributes onto this closed field allowlist and already resolved
// the final canonical event_type via that adapter's own
// CanonicalEventForOTel. Earlier this method round-tripped fields through
// json.Marshal and privacy.IngressSanitizer.DecodeAndExtract(...,
// FixtureSourceSchema()) -- the same decode path IngestHook uses for the
// Session 03 fixture-agent's literal underscore-shaped event_type wire
// vocabulary (session_started/user_prompt/tool_finished/session_finished).
// A real adapter's event_type is already the final dotted canonical form
// (session.started/prompt.submitted/tool.called), which is never a member
// of that fixture-only enum, so every real event was rejected as
// unknown_enum before ever reaching the store -- and the quarantine branch
// taken on that rejection separately reused the sanitizer's
// "hmac-sha256:"-prefixed SafeError.IncidentID as this store's
// "qua_"+32-hex-chars-shaped QuarantineID, tripping the durable state
// invariant check instead of surfacing the real unknown_enum rejection
// (both were pre-existing bugs in code that no caller had ever exercised
// with a genuine schema mismatch until this real-adapter path existed).
// This method now builds its own privacy.SafeRecord directly -- reusing
// NormalizedFromSafe/ingestSafe's existing lineage, dedup, watermark and
// store-commit machinery exactly as the fixture and adapter-batch lanes do,
// never a second commit mechanism -- instead of forcing an already-safe,
// already-canonical field set through a decode path built for a different
// wire schema.
//
// adapterID identifies the logical agent/fixture that produced fields (for
// example FixtureAdapterID, codexadapter.AdapterID, claudeadapter.AdapterID),
// never the transport-specific SourceKind. This matters for cross-lane
// correlation: privacy.IngressSanitizer.DecodeAndExtract (the path
// IngestHook/ImportTranscript use for the exact same fixture-agent logical
// event) derives sourceRecordPseudonym/sessionPseudonym/recordID from
// schema.AdapterID, which is constant across hook/OTLP/transcript lanes for
// one agent. Keying this method's pseudonyms off kind (otlp_log/hook_http/
// transcript_jsonl differ per lane) instead of adapterID would give the same
// logical event a different RecordID depending only on which transport
// carried it, so NormalizedFromSafe's factKey (derived from RecordID) would
// never merge across lanes -- exactly the regression
// TestSharedLogicalFixtureAcrossHookOTLPAndTranscript guards against.
func (i *Ingestor) IngestSafeFields(fields map[string]any, adapterID string, kind SourceKind, sequence uint64) (CommitResult, error) {
	sourceSchemaID := adapterID + ".otel/1"
	if adapterID == FixtureAdapterID {
		sourceSchemaID = fixtureOTLPSchema
	}
	return i.ingestCanonicalSafeFields(fields, adapterID, kind, sourceSchemaID, sequence)
}

// IngestSafeHookFields is the equivalent already-sanitized boundary for a
// real adapter hook. It deliberately shares the canonical-field validator,
// pseudonymization and commit path with OTLP while retaining hook_http
// lineage and the adapter's hook schema identity.
func (i *Ingestor) IngestSafeHookFields(fields map[string]any, adapterID string, sequence uint64) (CommitResult, error) {
	if adapterID == "" || adapterID == FixtureAdapterID {
		return CommitResult{}, errors.New("invalid_hook_adapter_identity")
	}
	return i.ingestCanonicalSafeFields(
		fields, adapterID, SourceHook, adapterID+".hook/1", sequence,
	)
}

func (i *Ingestor) ingestCanonicalSafeFields(
	fields map[string]any,
	adapterID string,
	kind SourceKind,
	sourceSchemaID string,
	sequence uint64,
) (CommitResult, error) {
	allowed := map[string]bool{
		"event_id": true, "session_id": true, "observed_at": true, "event_type": true,
		"outcome": true, "value_state": true, "model": true, "tool_name": true,
		"component_kind": true, "duration_ms": true, "prompt_character_count": true,
		"input_tokens": true, "cached_input_tokens": true, "output_tokens": true, "provider_cost_micros": true,
		"turn_id": true,
	}
	for key := range fields {
		if !allowed[key] {
			return CommitResult{}, errors.New("unsafe_otlp_field")
		}
	}
	eventID, _ := fields["event_id"].(string)
	sessionID, _ := fields["session_id"].(string)
	observedText, _ := fields["observed_at"].(string)
	eventType, _ := fields["event_type"].(string)
	if eventID == "" || sessionID == "" || observedText == "" || eventType == "" {
		return CommitResult{}, errors.New("missing_safe_attribute")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observedText)
	if err != nil {
		return CommitResult{}, errors.New("invalid_timestamp")
	}
	outcome, _ := fields["outcome"].(string)
	if outcome == "" {
		outcome = "unknown"
	}
	if !validOutcome(outcome) {
		return CommitResult{}, errors.New("unsafe_otlp_field")
	}
	valueState, _ := fields["value_state"].(string)
	if valueState == "" {
		valueState = string(privacy.ValueObserved)
	}
	if !validValueState(valueState) {
		return CommitResult{}, errors.New("unsafe_otlp_field")
	}
	if !i.acquire() {
		return CommitResult{}, ErrBackpressure
	}
	defer i.release()
	if adapterID == "" {
		return CommitResult{}, errors.New("invalid_otlp_adapter_identity")
	}
	now := i.now().UTC()
	sourceRecordPseudonym := "hmac-sha256:" + i.keyedIdentity("source-record/1", adapterID+"\x00"+eventID)
	sessionPseudonym := "hmac-sha256:" + i.keyedIdentity("session/1", adapterID+"\x00"+sessionID)
	turnPseudonym := ""
	if turnID, ok := fields["turn_id"].(string); ok && turnID != "" {
		turnPseudonym = "hmac-sha256:" + i.keyedIdentity("turn/1", adapterID+"\x00"+sessionID+"\x00"+turnID)
	}
	idempotency := "hmac-sha256:" + i.keyedIdentity("idempotency/1", adapterID+"\x00"+string(kind)+"\x00"+eventID+"\x00"+observedText)
	// index is always "0": IngestSafeFields ingests exactly one OTLP
	// record/data point per call (see ingestOneRecord), unlike
	// DecodeAndExtract's batch-object loop, which the trailing
	// strconv.Itoa(index) in recordID's sibling derivation below
	// distinguishes when the same eventID repeats within one batch.
	recordID := "hmac-sha256:" + i.keyedIdentity("record/1", adapterID+"\x00"+sessionID+"\x00"+eventID+"\x000")
	var tool privacy.CatalogObservation
	if toolName, ok := fields["tool_name"].(string); ok && toolName != "" {
		tool = privacy.CatalogObservation{State: privacy.ObservationObserved, ID: &toolName}
	} else {
		tool = privacy.CatalogObservation{State: privacy.ObservationNotObserved}
	}
	var model privacy.CatalogObservation
	if modelName, ok := fields["model"].(string); ok && modelName != "" {
		model = privacy.CatalogObservation{State: privacy.ObservationObserved, ID: &modelName}
	} else {
		model = privacy.CatalogObservation{State: privacy.ObservationNotObserved}
	}
	componentKind, _ := fields["component_kind"].(string)
	measurement := privacy.TelemetryMeasurements{
		DurationMS:           safeInt64Pointer(fields["duration_ms"]),
		PromptCharacterCount: safeInt64Pointer(fields["prompt_character_count"]),
		InputTokens:          safeInt64Pointer(fields["input_tokens"]),
		CachedInputTokens:    safeInt64Pointer(fields["cached_input_tokens"]),
		OutputTokens:         safeInt64Pointer(fields["output_tokens"]),
		ProviderCostMicros:   safeInt64Pointer(fields["provider_cost_micros"]),
	}
	for _, value := range []*int64{
		measurement.DurationMS, measurement.PromptCharacterCount,
		measurement.InputTokens, measurement.CachedInputTokens,
		measurement.OutputTokens, measurement.ProviderCostMicros,
	} {
		if value != nil && *value < 0 {
			return CommitResult{}, errors.New("unsafe_otlp_field")
		}
	}
	record := privacy.SafeRecord{
		RecordID: recordID, IdempotencyKey: idempotency,
		AdapterID: adapterID, AdapterVersion: "1.0.0",
		SourceSchemaID: sourceSchemaID, SchemaFingerprint: sourceRecordPseudonym,
		ObservedAt: observedAt.UTC(), ReceivedAt: now,
		Confidence: 1, EventType: eventType, Outcome: outcome, ValueState: privacy.ValueState(valueState),
		Model: model, Tool: tool, ComponentKind: componentKind, Telemetry: measurement,
		Lineage: privacy.Lineage{
			SourceRecordPseudonym: sourceRecordPseudonym, SessionPseudonym: sessionPseudonym,
			TurnPseudonym: turnPseudonym,
			AdapterID:     adapterID, AdapterVersion: "1.0.0", SourceSchemaID: sourceSchemaID,
			SchemaFingerprint: sourceRecordPseudonym, SanitizerVersion: "kansoku.ingress-sanitizer/1",
			ContractSHA256: privacy.PrivacyContractSemanticSHA256,
		},
	}
	return i.ingestSafe(record, kind, sequence, nil)
}

func safeInt64Pointer(value any) *int64 {
	switch typed := value.(type) {
	case int64:
		copy := typed
		return &copy
	case int:
		copy := int64(typed)
		return &copy
	case uint64:
		if typed <= uint64(^uint64(0)>>1) {
			copy := int64(typed)
			return &copy
		}
	}
	return nil
}

// IngestSanitizedAdapterRecord is the external-adapter batch boundary. The
// input is the closed privacy.SafeRecord allowlist, not an arbitrary agent
// payload or generic attribute map.
func (i *Ingestor) IngestSanitizedAdapterRecord(record privacy.SafeRecord, sequence uint64) (CommitResult, error) {
	if err := validateSanitizedAdapterRecord(record); err != nil {
		return CommitResult{}, err
	}
	if !i.acquire() {
		return CommitResult{}, ErrBackpressure
	}
	defer i.release()
	return i.ingestSafe(record, SourceAdapterBatch, sequence, nil)
}

func validateSanitizedAdapterRecord(record privacy.SafeRecord) error {
	if record.AdapterID == "" || record.AdapterVersion == "" ||
		record.SourceSchemaID == "" || record.SchemaFingerprint == "" ||
		record.RecordID == "" || record.IdempotencyKey == "" ||
		record.ObservedAt.IsZero() || record.ReceivedAt.IsZero() ||
		record.Lineage.AdapterID != record.AdapterID ||
		record.Lineage.AdapterVersion != record.AdapterVersion ||
		record.Lineage.SourceSchemaID != record.SourceSchemaID ||
		record.Lineage.SchemaFingerprint != record.SchemaFingerprint ||
		record.Lineage.SanitizerVersion != "kansoku.ingress-sanitizer/1" ||
		record.Lineage.ContractSHA256 != privacy.PrivacyContractSemanticSHA256 ||
		!strings.HasPrefix(record.Lineage.SourceRecordPseudonym, "hmac-sha256:") ||
		!strings.HasPrefix(record.Lineage.SessionPseudonym, "hmac-sha256:") ||
		len(record.ComponentMentions) > 128 {
		return errors.New("invalid_sanitized_adapter_record")
	}
	for _, value := range record.ComponentMentions {
		if value == "" || len(value) > 128 {
			return errors.New("invalid_sanitized_adapter_record")
		}
	}
	redactions := record.RedactionCounts
	if redactions.PromptFields < 0 || redactions.AttachmentFields < 0 ||
		redactions.ResponseFields < 0 || redactions.SourceFields < 0 ||
		redactions.ToolIOFields < 0 || redactions.CommandFields < 0 ||
		redactions.PathFields < 0 || redactions.EnvironmentFields < 0 ||
		redactions.CredentialFields < 0 || redactions.ExceptionFields < 0 ||
		redactions.SensitiveIdentifierFields < 0 {
		return errors.New("invalid_sanitized_adapter_record")
	}
	if _, _, err := NormalizedFromSafe(record, SourceAdapterBatch, 0, record.ReceivedAt); err != nil {
		return errors.New("invalid_sanitized_adapter_record")
	}
	return nil
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
