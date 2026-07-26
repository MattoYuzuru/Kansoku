package codexadapter_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/codexadapter"
	"kansoku.local/kansoku/internal/observability"
	"kansoku.local/kansoku/internal/privacy"
)

func TestAppServerAndOTelAreTwoEvidenceLanesForOneLogicalFact(t *testing.T) {
	key := bytes.Repeat([]byte("k"), 32)
	observedAt := time.Date(2026, 7, 26, 14, 0, 0, 0, time.UTC)
	bridge, err := codexadapter.NewAppServerBridge(key, func() time.Time {
		return observedAt.Add(time.Second)
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := &adaptersdk.MemoryAssertionSink{}
	frame := `{"method":"turn/started","params":{"threadId":"shared-session","turn":{"id":"shared-event","startedAt":` +
		"1785074400" + `,"status":"inProgress","items":[]}}}`
	if err := bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
		Installation:  adaptersdk.Installation{AdapterID: codexadapter.AdapterID},
		Protocol:      codexadapter.AppServerProtocolVersion,
		SchemaVersion: codexadapter.AppServerSchemaVersion,
		Frames:        strings.NewReader(frame),
	}, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Records()) != 1 {
		t.Fatalf("bridge records=%d", len(sink.Records()))
	}

	store, err := observability.OpenFileStore(filepath.Join(t.TempDir(), "state.json"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, err := observability.NewIngestor(store, key, privacy.DefaultLimits(), 2)
	if err != nil {
		t.Fatal(err)
	}
	ingestor.SetClockForTest(func() time.Time { return observedAt.Add(time.Second) })
	if _, err := ingestor.IngestSanitizedBridgeRecord(sink.Records()[0], 1); err != nil {
		t.Fatalf("bridge ingest: %v", err)
	}
	if _, err := ingestor.IngestSafeFields(map[string]any{
		"event_id": "shared-event", "session_id": "shared-session",
		"observed_at": observedAt.Format(time.RFC3339Nano),
		"event_type":  "prompt.submitted", "outcome": "unknown",
		"value_state": "observed",
	}, codexadapter.AdapterID, observability.SourceOTLPLog, 2); err != nil {
		t.Fatalf("OTel ingest: %v", err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 || len(state.Evidence) != 2 {
		t.Fatalf("facts=%d evidence=%d", len(state.Facts), len(state.Evidence))
	}
	var lanes = map[observability.SourceKind]bool{}
	for _, evidence := range state.Evidence {
		lanes[evidence.Source.Kind] = true
	}
	if !lanes[observability.SourceEvidenceBridge] || !lanes[observability.SourceOTLPLog] {
		t.Fatalf("evidence lanes=%#v", lanes)
	}
}
