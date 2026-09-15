package observability

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/privacy"
)

type recordingDurableSink struct {
	events      []Event
	evidence    []Evidence
	quarantines []Quarantine
	incidents   []Incident
	err         error
}

func (s *recordingDurableSink) PersistNormalizedFact(event Event, evidence Evidence) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, event)
	s.evidence = append(s.evidence, evidence)
	return nil
}

func (s *recordingDurableSink) PersistQuarantineMetadata(
	quarantine Quarantine,
	incident Incident,
) error {
	if s.err != nil {
		return s.err
	}
	s.quarantines = append(s.quarantines, quarantine)
	s.incidents = append(s.incidents, incident)
	return nil
}

func compactTestFields(eventID string) map[string]any {
	return map[string]any{
		"event_id": eventID, "session_id": "session-1",
		"observed_at": time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		"event_type":  "source.observed", "value_state": "observed",
	}
}

func TestCompactStoreDoesNotGrowWithFactCardinality(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints", "state.json")
	store, err := OpenCompactStore(path, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	for sequence := uint64(1); sequence <= 50_000; sequence++ {
		event := Event{EventID: "ignored"}
		evidence := Evidence{EvidenceID: "ignored"}
		if _, err := store.Commit(CommitRequest{
			Event: &event, Evidence: &evidence,
		}); err != nil {
			t.Fatalf("commit %d: %v", sequence, err)
		}
	}
	watermark := Watermark{
		SourceID: "otlp_log", LastReadSequence: 50_000,
		LastEmittedSequence: 50_000,
	}
	if _, err := store.Commit(CommitRequest{Watermark: &watermark}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 4096 {
		t.Fatalf("compact state grew with facts: %d bytes", info.Size())
	}
	state := store.Snapshot()
	if len(state.Facts) != 0 || len(state.Evidence) != 0 ||
		len(state.Quarantine) != 0 || len(state.Incidents) != 0 {
		t.Fatalf("fact/evidence metadata crossed compact boundary: %+v", state)
	}
	if state.Watermarks["otlp_log"].LastReadSequence != 50_000 {
		t.Fatal("latest bounded watermark was not retained")
	}
}

func TestCompactProductionPersistsBeforeAdvancingCheckpoint(t *testing.T) {
	store, err := OpenCompactStore(filepath.Join(t.TempDir(), "state.json"), 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, err := NewIngestor(
		store, bytes.Repeat([]byte("d"), 32), privacy.DefaultLimits(), 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordingDurableSink{err: ErrDurabilityUnavailable}
	if err := ingestor.ConfigureDurableFactSink(sink); err != nil {
		t.Fatal(err)
	}
	_, err = ingestor.IngestSafeFields(
		compactTestFields("durability-rejected"), "adapter", SourceOTLPLog, 1,
	)
	if !errors.Is(err, ErrDurabilityUnavailable) {
		t.Fatalf("error=%v want durability unavailable", err)
	}
	if len(store.Snapshot().Watermarks) != 0 {
		t.Fatal("checkpoint/watermark advanced before durable ownership")
	}
}

func TestCompactStateFailureDoesNotRejectAlreadyDurableFact(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "checkpoints")
	store, err := OpenCompactStore(filepath.Join(stateDir, "state.json"), 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, err := NewIngestor(
		store, bytes.Repeat([]byte("e"), 32), privacy.DefaultLimits(), 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordingDurableSink{}
	if err := ingestor.ConfigureDurableFactSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateDir, []byte("blocks-directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestor.IngestSafeFields(
		compactTestFields("durable-before-checkpoint"), "adapter", SourceOTLPLog, 1,
	); err != nil {
		t.Fatalf("already-durable fact was rejected: %v", err)
	}
	if len(sink.events) != 1 || len(sink.evidence) != 1 {
		t.Fatalf("durable sink calls=%d/%d", len(sink.events), len(sink.evidence))
	}
	health := ingestor.LocalStateHealth()
	if health.CommitFailureTotal != 1 || health.LastFailedCommitAt.IsZero() {
		t.Fatalf("checkpoint failure not exposed: %+v", health)
	}
}

func TestUnknownSchemaStormUsesOneHourlyOccurrenceKeyAndBoundedCompactState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints", "state.json")
	store, err := OpenCompactStore(path, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, err := NewIngestor(
		store, bytes.Repeat([]byte("u"), 32), privacy.DefaultLimits(), 8,
	)
	if err != nil {
		t.Fatal(err)
	}
	ingestor.SetClockForTest(func() time.Time {
		return time.Date(2026, 7, 29, 3, 15, 0, 0, time.UTC)
	})
	sink := &recordingDurableSink{}
	if err := ingestor.ConfigureDurableFactSink(sink); err != nil {
		t.Fatal(err)
	}
	receiver, err := NewOTLPReceiver(ingestor, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	const repeats = 2048
	for attempt := 0; attempt < repeats; attempt++ {
		if err := receiver.ingestLogs(
			logsRequest("unknown-storm", "unreviewed/99", 1),
			SourceOTLPLog,
		); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	if len(sink.quarantines) != repeats || len(sink.incidents) != repeats {
		t.Fatalf(
			"metadata sink calls=%d/%d want %d",
			len(sink.quarantines), len(sink.incidents), repeats,
		)
	}
	occurrenceKeys := map[string]struct{}{}
	for _, quarantine := range sink.quarantines {
		if quarantine.OccurrenceKey == "" {
			t.Fatal("storm occurrence was not hourly bucketed")
		}
		occurrenceKeys[quarantine.OccurrenceKey] = struct{}{}
	}
	if len(occurrenceKeys) != 1 {
		t.Fatalf("hourly storm occurrence keys=%d want 1", len(occurrenceKeys))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 4096 {
		t.Fatalf("compact state grew with unknown storm: %d bytes", info.Size())
	}
	state := store.Snapshot()
	if len(state.Facts) != 0 || len(state.Evidence) != 0 ||
		len(state.Quarantine) != 0 || len(state.Incidents) != 0 ||
		len(state.Watermarks) != 1 {
		t.Fatalf("unknown storm crossed compact boundary: %+v", state)
	}
}
