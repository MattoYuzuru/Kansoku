package observability

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const CompactStoreSpecVersion = "kansoku.compact-state/1"

// CompactState deliberately excludes facts, evidence, correlations,
// quarantine payloads and incidents. PostgreSQL owns those durable records.
// Its cardinality is bounded by the closed importer/source vocabularies.
type CompactState struct {
	SpecVersion string                `json:"spec_version"`
	Revision    uint64                `json:"revision"`
	Watermarks  map[string]Watermark  `json:"watermarks"`
	Checkpoints map[string]Checkpoint `json:"checkpoints"`
}

// CompactStore persists only replay/checkpoint coordination state. A failed
// compact-state write must never roll back or reject a fact already owned by
// PostgreSQL or the emergency spool.
type CompactStore struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	state    CompactState
}

var _ StateStore = (*CompactStore)(nil)

func OpenCompactStore(path string, maxBytes int64) (*CompactStore, error) {
	if !filepath.IsAbs(path) || maxBytes < 4096 {
		return nil, errors.New("invalid_compact_store_configuration")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errors.New("compact_store_directory_failure")
	}
	_ = os.Remove(path + ".tmp")
	store := &CompactStore{
		path: path, maxBytes: maxBytes,
		state: CompactState{
			SpecVersion: CompactStoreSpecVersion,
			Watermarks:  map[string]Watermark{},
			Checkpoints: map[string]Checkpoint{},
		},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil || int64(len(data)) > maxBytes {
		return nil, errors.New("compact_store_read_failure")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store.state); err != nil || ensureCompactEOF(decoder) != nil {
		return nil, errors.New("compact_store_schema_failure")
	}
	if err := validateCompactState(store.state); err != nil {
		return nil, errors.New("compact_store_invariant_failure")
	}
	return store, nil
}

func ensureCompactEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing_json")
	}
	return nil
}

func validateCompactState(state CompactState) error {
	if state.SpecVersion != CompactStoreSpecVersion ||
		state.Watermarks == nil || state.Checkpoints == nil ||
		len(state.Watermarks) > 32 || len(state.Checkpoints) > 2048 {
		return errors.New("invalid_compact_state")
	}
	for key, watermark := range state.Watermarks {
		if key == "" || watermark.SourceID != key {
			return errors.New("invalid_compact_watermark")
		}
	}
	for key, checkpoint := range state.Checkpoints {
		if key == "" || checkpoint.ImporterID != key {
			return errors.New("invalid_compact_checkpoint")
		}
	}
	return nil
}

func (s *CompactStore) Commit(request CommitRequest) (CommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := CompactState{
		SpecVersion: CompactStoreSpecVersion,
		Revision:    s.state.Revision,
		Watermarks:  make(map[string]Watermark, len(s.state.Watermarks)),
		Checkpoints: make(map[string]Checkpoint, len(s.state.Checkpoints)),
	}
	for key, value := range s.state.Watermarks {
		next.Watermarks[key] = value
	}
	for key, value := range s.state.Checkpoints {
		next.Checkpoints[key] = value
	}
	if request.Watermark != nil {
		next.Watermarks[request.Watermark.SourceID] = *request.Watermark
	}
	if request.Checkpoint != nil {
		next.Checkpoints[request.Checkpoint.ImporterID] = *request.Checkpoint
	}
	result := CommitResult{
		FactInserted:     request.Event != nil,
		EvidenceInserted: request.Evidence != nil,
	}
	// Facts and quarantine metadata are already durable before this method is
	// called. If there is no coordination-state mutation, avoid an fsync.
	if request.Watermark == nil && request.Checkpoint == nil {
		result.Revision = s.state.Revision
		return result, nil
	}
	next.Revision++
	if err := validateCompactState(next); err != nil {
		return result, err
	}
	encoded, err := json.Marshal(next)
	if err != nil || int64(len(encoded)) > s.maxBytes {
		return result, ErrBackpressure
	}
	temporary := s.path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return result, errors.New("compact_store_temp_failure")
	}
	if _, err = file.Write(encoded); err != nil {
		_ = file.Close()
		return result, errors.New("compact_store_write_failure")
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return result, errors.New("compact_store_sync_failure")
	}
	if err = file.Close(); err != nil {
		return result, errors.New("compact_store_close_failure")
	}
	if err = os.Rename(temporary, s.path); err != nil {
		return result, errors.New("compact_store_rename_failure")
	}
	directory, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return result, errors.New("compact_store_directory_open_failure")
	}
	syncErr := directory.Sync()
	_ = directory.Close()
	if syncErr != nil {
		return result, errors.New("compact_store_directory_sync_failure")
	}
	s.state = next
	result.Revision = next.Revision
	return result, nil
}

func (s *CompactStore) Snapshot() DurableState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := emptyState()
	state.SpecVersion = StoreSpecVersion
	state.Revision = s.state.Revision
	for key, value := range s.state.Watermarks {
		state.Watermarks[key] = value
	}
	for key, value := range s.state.Checkpoints {
		state.Checkpoints[key] = value
	}
	return state
}

func (s *CompactStore) Usage() (current, budget int64, err error) {
	info, statErr := os.Stat(s.path)
	if errors.Is(statErr, os.ErrNotExist) {
		return 0, s.maxBytes, nil
	}
	if statErr != nil {
		return 0, s.maxBytes, statErr
	}
	return info.Size(), s.maxBytes, nil
}
