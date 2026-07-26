package observability

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var (
	ErrBackpressure  = errors.New("backpressure_retryable")
	ErrCrashInjected = errors.New("crash_injected")
)

type CrashStage string

const (
	CrashNone         CrashStage = ""
	CrashBeforeSync   CrashStage = "before_temp_sync"
	CrashBeforeRename CrashStage = "before_rename"
	CrashAfterRename  CrashStage = "after_rename"
)

type CommitRequest struct {
	Event       *Event
	Evidence    *Evidence
	Checkpoint  *Checkpoint
	Watermark   *Watermark
	Quarantine  *Quarantine
	Incident    *Incident
	Correlation *Correlation
}

type CommitResult struct {
	FactInserted     bool
	EvidenceInserted bool
	DuplicateReplay  bool
	Revision         uint64
}

// FileStore is the pre-Session-04 durable writer. Each accepted mutation is a
// single fsync+rename transaction over a bounded, typed state snapshot. It is
// intentionally not described as a database and will be replaced by the
// PostgreSQL transaction model in Session 04.
type FileStore struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	state      DurableState
	crashStage CrashStage
}

func OpenFileStore(path string, maxBytes int64) (*FileStore, error) {
	if !filepath.IsAbs(path) || maxBytes < 4096 {
		return nil, errors.New("invalid_store_configuration")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errors.New("store_directory_failure")
	}
	_ = os.Remove(path + ".tmp")
	store := &FileStore{path: path, maxBytes: maxBytes, state: emptyState()}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil || int64(len(data)) > maxBytes {
		return nil, errors.New("store_read_failure")
	}
	if err := strictUnmarshal(data, &store.state); err != nil {
		return nil, errors.New("store_schema_failure")
	}
	if err := ValidateState(store.state); err != nil {
		return nil, errors.New("store_invariant_failure")
	}
	return store, nil
}

func (s *FileStore) SetCrashStageForTest(stage CrashStage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crashStage = stage
}

func cloneState(state DurableState) (DurableState, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return DurableState{}, err
	}
	var cloned DurableState
	if err := strictUnmarshal(data, &cloned); err != nil {
		return DurableState{}, err
	}
	return cloned, nil
}

func (s *FileStore) Commit(request CommitRequest) (CommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := cloneState(s.state)
	if err != nil {
		return CommitResult{}, errors.New("store_clone_failure")
	}
	result := CommitResult{}
	if request.Event != nil || request.Evidence != nil {
		if request.Event == nil || request.Evidence == nil || request.Evidence.EventID != request.Event.EventID {
			return result, errors.New("event_evidence_transaction_required")
		}
		fact, factExists := next.Facts[request.Event.FactKey]
		if !factExists {
			fact = Fact{Event: *request.Event, Completeness: Unknown}
			result.FactInserted = true
		}
		if existing, evidenceExists := next.Evidence[request.Evidence.EvidenceID]; evidenceExists {
			existing.ReplayCount++
			existing.LastSeenAt = request.Evidence.LastSeenAt
			next.Evidence[existing.EvidenceID] = existing
			result.DuplicateReplay = true
		} else {
			next.Evidence[request.Evidence.EvidenceID] = *request.Evidence
			fact.EvidenceIDs = append(fact.EvidenceIDs, request.Evidence.EvidenceID)
			sort.Strings(fact.EvidenceIDs)
			result.EvidenceInserted = true
		}
		fact.Completeness = completenessForEvidence(fact.EvidenceIDs, next.Evidence, next.Watermarks)
		next.Facts[request.Event.FactKey] = fact
	}
	if request.Checkpoint != nil {
		next.Checkpoints[request.Checkpoint.ImporterID] = *request.Checkpoint
	}
	if request.Watermark != nil {
		next.Watermarks[request.Watermark.SourceID] = *request.Watermark
	}
	newQuarantineOccurrence := request.Quarantine == nil
	if request.Quarantine != nil {
		newQuarantineOccurrence = true
		for index := range next.Quarantine {
			existing := next.Quarantine[index]
			if existing.QuarantineID != request.Quarantine.QuarantineID {
				continue
			}
			if existing.ObservedAt.Equal(request.Quarantine.ObservedAt) &&
				existing.ByteCount == request.Quarantine.ByteCount &&
				existing.RecordCount == request.Quarantine.RecordCount &&
				existing.Category == request.Quarantine.Category {
				newQuarantineOccurrence = false
			} else {
				next.Quarantine[index] = *request.Quarantine
			}
			break
		}
		if newQuarantineOccurrence {
			found := false
			for _, existing := range next.Quarantine {
				if existing.QuarantineID == request.Quarantine.QuarantineID {
					found = true
					break
				}
			}
			if !found {
				next.Quarantine = append(next.Quarantine, *request.Quarantine)
			}
		}
	}
	if request.Incident != nil {
		incident := *request.Incident
		if existing, ok := next.Incidents[incident.IncidentID]; ok {
			if newQuarantineOccurrence {
				incident.OccurrenceCount = existing.OccurrenceCount + 1
			} else {
				incident.OccurrenceCount = existing.OccurrenceCount
				incident.LastObserved = existing.LastObserved
			}
		} else if incident.OccurrenceCount == 0 {
			incident.OccurrenceCount = 1
		}
		next.Incidents[incident.IncidentID] = incident
	}
	if request.Correlation != nil {
		next.Correlations[request.Correlation.CorrelationID] = *request.Correlation
	}
	for key, fact := range next.Facts {
		fact.Completeness = completenessForEvidence(fact.EvidenceIDs, next.Evidence, next.Watermarks)
		next.Facts[key] = fact
	}
	return s.persist(next, result)
}

// persist is the shared validate+fsync+rename transaction tail every mutating
// entry point (Commit, PurgeFacts) uses: it never partially writes next, and
// s.state only advances to next after the rename (and its containing
// directory fsync) has actually completed, matching the exact
// crash-injection semantics Commit already exercised before this was
// extracted. Callers must hold s.mu and must have already produced a fully
// self-consistent next (Revision NOT yet incremented) before calling this.
func (s *FileStore) persist(next DurableState, result CommitResult) (CommitResult, error) {
	next.Revision = s.state.Revision + 1
	if err := ValidateState(next); err != nil {
		return CommitResult{}, fmt.Errorf("store_invariant_failure:%w", err)
	}
	encoded, err := json.Marshal(next)
	if err != nil || int64(len(encoded)) > s.maxBytes {
		return result, ErrBackpressure
	}
	temporary := s.path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return result, errors.New("store_temp_failure")
	}
	cleanup := func() { _ = file.Close() }
	if _, err = file.Write(encoded); err != nil {
		cleanup()
		return result, errors.New("store_write_failure")
	}
	if s.crashStage == CrashBeforeSync {
		cleanup()
		return result, ErrCrashInjected
	}
	if err = file.Sync(); err != nil {
		cleanup()
		return result, errors.New("store_sync_failure")
	}
	if err = file.Close(); err != nil {
		return result, errors.New("store_close_failure")
	}
	if s.crashStage == CrashBeforeRename {
		return result, ErrCrashInjected
	}
	if err = os.Rename(temporary, s.path); err != nil {
		return result, errors.New("store_rename_failure")
	}
	directory, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return result, errors.New("store_directory_open_failure")
	}
	if err = directory.Sync(); err != nil {
		_ = directory.Close()
		return result, errors.New("store_directory_sync_failure")
	}
	_ = directory.Close()
	if s.crashStage == CrashAfterRename {
		return result, ErrCrashInjected
	}
	s.state = next
	result.Revision = next.Revision
	return result, nil
}

func completenessForEvidence(ids []string, evidence map[string]Evidence, watermarks map[string]Watermark) Completeness {
	kinds := map[SourceKind]bool{}
	for _, id := range ids {
		kind := evidence[id].Source.Kind
		if watermark, exists := watermarks[string(kind)]; exists && (watermark.Lifecycle == SourceDisabled || watermark.Lifecycle == SourceDegraded || watermark.Lifecycle == SourceError) {
			continue
		}
		kinds[kind] = true
	}
	hook := kinds[SourceHook]
	otlp := kinds[SourceOTLPLog] || kinds[SourceOTLPSpan] || kinds[SourceOTLPMetric]
	transcript := kinds[SourceTranscript]
	if hook && otlp && transcript {
		return Complete
	}
	if hook || otlp || transcript {
		return Partial
	}
	return Unknown
}

func (s *FileStore) Snapshot() DurableState {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned, err := cloneState(s.state)
	if err != nil {
		return emptyState()
	}
	return cloned
}

// PurgeFacts removes exactly the Facts named by factKeys, together with
// every Evidence/Correlation row that exists ONLY to support one of those
// Facts, and returns how many Facts were actually removed (a factKey absent
// from the current state is simply not counted, never an error, so a
// caller replaying an already-purged key is safe). This is the durable
// "explicit test-namespace retention path" internal/integrity's stage_5
// synthetic pipeline probe uses to expire its own uniquely-tagged records
// after verifying their end-to-end appearance: it never deletes anything
// the caller did not name explicitly by FactKey, so it cannot be used as a
// generic bulk-delete of real usage data, and it goes through the exact
// same validate+fsync+rename transaction (persist) every other durable
// mutation in this package uses -- a purge is not a second, weaker
// durability tier.
//
// Watermarks are deliberately left untouched: SourceKind-scoped watermark
// counters (last_read_sequence, last_observed_at, ...) are shared
// infrastructure-level liveness evidence for the whole hook_http/otlp_*
// lane, not a namespaced fact a retention sweep should roll back; a
// synthetic probe intentionally proves the lane is live, and that
// liveness evidence is legitimate even after the probe's own facts expire.
func (s *FileStore) PurgeFacts(factKeys []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := cloneState(s.state)
	if err != nil {
		return 0, errors.New("store_clone_failure")
	}
	removed := 0
	for _, factKey := range factKeys {
		fact, ok := next.Facts[factKey]
		if !ok {
			continue
		}
		for _, evidenceID := range fact.EvidenceIDs {
			delete(next.Evidence, evidenceID)
		}
		for correlationID, correlation := range next.Correlations {
			if correlation.EventID == fact.Event.EventID {
				delete(next.Correlations, correlationID)
			}
		}
		delete(next.Facts, factKey)
		removed++
	}
	if removed == 0 {
		return 0, nil
	}
	for key, remaining := range next.Facts {
		remaining.Completeness = completenessForEvidence(remaining.EvidenceIDs, next.Evidence, next.Watermarks)
		next.Facts[key] = remaining
	}
	result, err := s.persist(next, CommitResult{})
	if err != nil {
		return 0, err
	}
	_ = result
	return removed, nil
}

type DurableSpool struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
}

type SpoolStats struct {
	Depth    int
	OldestAt time.Time
}

func NewDurableSpool(path string, maxBytes int64) (*DurableSpool, error) {
	if !filepath.IsAbs(path) || maxBytes < 1024 {
		return nil, errors.New("invalid_spool_configuration")
	}
	if err := validateSpoolPath(path); err != nil {
		return nil, errors.New("spool_path_unsafe")
	}
	return &DurableSpool{path: path, maxBytes: maxBytes}, nil
}

func validateSpoolRequest(request CommitRequest) error {
	if request.Event == nil || request.Evidence == nil || request.Event.EventID != request.Evidence.EventID ||
		request.Checkpoint != nil || request.Watermark != nil || request.Quarantine != nil || request.Incident != nil || request.Correlation != nil {
		return errors.New("unsafe_spool_request")
	}
	if err := validateEvent(*request.Event, request.Event.FactKey); err != nil {
		return errors.New("unsafe_spool_request")
	}
	if err := validateEvidence(*request.Evidence, request.Evidence.EvidenceID); err != nil {
		return errors.New("unsafe_spool_request")
	}
	return nil
}

// Append accepts typed sanitized requests only and fsyncs before returning.
func (s *DurableSpool) Append(request CommitRequest) error {
	if err := validateSpoolRequest(request); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	encoded, err := json.Marshal(request)
	if err != nil {
		return errors.New("spool_encode_failure")
	}
	return appendSecureSpool(s.path, append(encoded, '\n'), s.maxBytes)
}

func (s *DurableSpool) Replay(commit func(CommitRequest) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := readSecureSpool(s.path, s.maxBytes)
	if err != nil {
		return errors.New("spool_open_failure")
	}
	if len(raw) == 0 {
		return nil
	}
	if raw[len(raw)-1] != '\n' {
		return errors.New("spool_decode_failure")
	}
	for _, encoded := range bytes.Split(raw[:len(raw)-1], []byte{'\n'}) {
		var request CommitRequest
		if len(encoded) == 0 || strictUnmarshal(encoded, &request) != nil || validateSpoolRequest(request) != nil {
			return errors.New("spool_decode_failure")
		}
		if err := commit(request); err != nil {
			return err
		}
	}
	if err := drainSecureSpool(s.path); err != nil {
		return errors.New("spool_drain_failure")
	}
	return nil
}

// Stats returns framing and age metadata without exposing any persisted fact.
func (s *DurableSpool) Stats() (SpoolStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := readSecureSpool(s.path, s.maxBytes)
	if err != nil {
		return SpoolStats{}, errors.New("spool_open_failure")
	}
	if len(raw) == 0 {
		return SpoolStats{}, nil
	}
	if raw[len(raw)-1] != '\n' {
		return SpoolStats{}, errors.New("spool_decode_failure")
	}
	stats := SpoolStats{}
	for _, encoded := range bytes.Split(raw[:len(raw)-1], []byte{'\n'}) {
		var request CommitRequest
		if len(encoded) == 0 || strictUnmarshal(encoded, &request) != nil || validateSpoolRequest(request) != nil {
			return SpoolStats{}, errors.New("spool_decode_failure")
		}
		stats.Depth++
		if stats.OldestAt.IsZero() || request.Event.IngestedAt.Before(stats.OldestAt) {
			stats.OldestAt = request.Event.IngestedAt
		}
	}
	return stats, nil
}

// CheckDurableSpool validates path security, framing, strict JSON shape and
// every typed request without committing or draining anything. It is the
// read-only stage-9 integrity probe for a corrupt spool.
func CheckDurableSpool(path string, maxBytes int64) error {
	if !filepath.IsAbs(path) || maxBytes < 1024 {
		return errors.New("invalid_spool_configuration")
	}
	if err := validateSpoolPath(path); err != nil {
		return errors.New("spool_path_unsafe")
	}
	raw, err := readSecureSpool(path, maxBytes)
	if err != nil {
		return errors.New("spool_open_failure")
	}
	if len(raw) == 0 {
		return nil
	}
	if raw[len(raw)-1] != '\n' {
		return errors.New("spool_decode_failure")
	}
	for _, encoded := range bytes.Split(raw[:len(raw)-1], []byte{'\n'}) {
		var request CommitRequest
		if len(encoded) == 0 || strictUnmarshal(encoded, &request) != nil || validateSpoolRequest(request) != nil {
			return errors.New("spool_decode_failure")
		}
	}
	return nil
}

func NewIncident(category string, source SourceKind, at time.Time) Incident {
	id := "inc_" + stableID("incident/1", category, string(source))[:32]
	return Incident{IncidentID: id, Capability: "core_ingestion", Category: category, Completeness: Degraded, OpenedAt: at.UTC(), LastObserved: at.UTC()}
}

// NewSchemaIncident groups unknown-schema failures by their structural
// fingerprint as well as source and category. A second unknown shape on the
// same transport must remain an independently actionable incident; a replay
// of the same shape retains the same identity.
func NewSchemaIncident(category string, source SourceKind, schemaFingerprint string, at time.Time) Incident {
	id := "inc_" + stableID("incident-schema/1", category, string(source), schemaFingerprint)[:32]
	return Incident{
		IncidentID: id, Capability: "core_ingestion", Category: category,
		Completeness: Degraded, OpenedAt: at.UTC(), LastObserved: at.UTC(),
	}
}
