package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"kansoku.local/kansoku/internal/localhttp"
	"kansoku.local/kansoku/internal/privacy"
)

var fixedTime = time.Date(2026, 7, 21, 12, 0, 0, 123000000, time.UTC)

func testRaw(eventID string) []byte {
	value := map[string]any{
		"event_id": eventID, "session_id": "session-safe-001", "observed_at": fixedTime.Format(time.RFC3339Nano),
		"event_type": "tool_finished", "outcome": "succeeded", "value_state": "numeric_zero", "tool_name": "inventory/tool-safe",
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

func scenarioRaw(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "session-03", "shared-scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Synthetic    bool           `json:"synthetic"`
		LogicalEvent map[string]any `json:"logical_event"`
		Expected     struct {
			Facts        int    `json:"facts"`
			Evidence     int    `json:"evidence"`
			Completeness string `json:"completeness"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if !fixture.Synthetic || fixture.Expected.Facts != 1 || fixture.Expected.Evidence != 3 || fixture.Expected.Completeness != "complete" {
		t.Fatal("fixture acceptance contract changed")
	}
	raw, err := json.Marshal(fixture.LogicalEvent)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testIngestor(t *testing.T, maxBytes int64) (*FileStore, *Ingestor, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := OpenFileStore(path, maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, err := NewIngestor(store, bytes.Repeat([]byte("k"), 32), privacy.DefaultLimits(), 4)
	if err != nil {
		t.Fatal(err)
	}
	ingestor.SetClockForTest(func() time.Time { return fixedTime.Add(time.Second) })
	return store, ingestor, path
}

func stringKV(key, value string) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}}}
}
func intKV(key string, value int64) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: value}}}
}
func otlpResource(schema string) *resourcev1.Resource {
	return &resourcev1.Resource{Attributes: []*commonv1.KeyValue{stringKV("service.name", "fixture-agent"), stringKV("kansoku.adapter.version", "1.0.0"), stringKV("kansoku.source.schema", schema)}}
}
func otlpAttrs(eventID string, sequence int64) []*commonv1.KeyValue {
	return []*commonv1.KeyValue{
		stringKV("kansoku.event.id", eventID), stringKV("kansoku.session.id", "session-safe-001"),
		stringKV("kansoku.event.type", "tool_finished"), stringKV("kansoku.outcome", "succeeded"),
		stringKV("kansoku.value_state", "numeric_zero"), stringKV("kansoku.tool.id", "inventory/tool-safe"), intKV("kansoku.sequence", sequence),
	}
}
func logsRequest(eventID, schema string, sequence int64) *collectorlogsv1.ExportLogsServiceRequest {
	record := &logsv1.LogRecord{ObservedTimeUnixNano: uint64(fixedTime.UnixNano()), Attributes: otlpAttrs(eventID, sequence), Body: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "SYNTHETIC_RAW_BODY_MUST_DISAPPEAR"}}}
	return &collectorlogsv1.ExportLogsServiceRequest{ResourceLogs: []*logsv1.ResourceLogs{{Resource: otlpResource(schema), ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{record}}}}}}
}
func traceRequest(eventID string, sequence int64) *collectortracev1.ExportTraceServiceRequest {
	span := &tracev1.Span{StartTimeUnixNano: uint64(fixedTime.UnixNano()), Attributes: otlpAttrs(eventID, sequence), Name: "SYNTHETIC_RAW_SPAN_NAME"}
	return &collectortracev1.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{{Resource: otlpResource(fixtureOTLPSchema), ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{span}}}}}}
}
func metricRequest(eventID string, sequence int64) *collectormetricsv1.ExportMetricsServiceRequest {
	point := &metricsv1.NumberDataPoint{TimeUnixNano: uint64(fixedTime.UnixNano()), Attributes: otlpAttrs(eventID, sequence), Value: &metricsv1.NumberDataPoint_AsInt{AsInt: 1}}
	metric := &metricsv1.Metric{Name: "safe_fixture_counter", Data: &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{DataPoints: []*metricsv1.NumberDataPoint{point}}}}
	return &collectormetricsv1.ExportMetricsServiceRequest{ResourceMetrics: []*metricsv1.ResourceMetrics{{Resource: otlpResource(fixtureOTLPSchema), ScopeMetrics: []*metricsv1.ScopeMetrics{{Metrics: []*metricsv1.Metric{metric}}}}}}
}

func exactProtoSize(t *testing.T, message proto.Message, target int, setPadding func(string)) {
	t.Helper()
	setPadding("")
	padding := target - proto.Size(message)
	if padding < 0 {
		t.Fatalf("base message exceeds target: size=%d target=%d", proto.Size(message), target)
	}
	for attempts := 0; attempts < 8; attempts++ {
		setPadding(strings.Repeat("p", padding))
		delta := target - proto.Size(message)
		if delta == 0 {
			return
		}
		padding += delta
		if padding < 0 {
			break
		}
	}
	t.Fatalf("cannot size protobuf exactly: got=%d target=%d", proto.Size(message), target)
}

func sizedSignalRequests(t *testing.T, target int, suffix string) (*collectorlogsv1.ExportLogsServiceRequest, *collectormetricsv1.ExportMetricsServiceRequest, *collectortracev1.ExportTraceServiceRequest) {
	t.Helper()
	logs := logsRequest("grpc-sized-log-"+suffix, fixtureOTLPSchema, 10)
	exactProtoSize(t, logs, target, func(value string) {
		logs.ResourceLogs[0].ScopeLogs[0].LogRecords[0].Body = &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}}
	})
	metrics := metricRequest("grpc-sized-metric-"+suffix, 11)
	exactProtoSize(t, metrics, target, func(value string) {
		metrics.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Description = value
	})
	traces := traceRequest("grpc-sized-trace-"+suffix, 12)
	exactProtoSize(t, traces, target, func(value string) {
		traces.ResourceSpans[0].ScopeSpans[0].Spans[0].Name = value
	})
	return logs, metrics, traces
}

func TestSharedLogicalFixtureAcrossHookOTLPAndTranscript(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	sharedRaw := scenarioRaw(t)
	if _, err := ingestor.IngestHook(sharedRaw, 10); err != nil {
		t.Fatal(err)
	}
	if err := receiver.ingestLogs(logsRequest("shared-001", fixtureOTLPSchema, 11), SourceOTLPLog); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcript, append(sharedRaw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := ingestor.ImportTranscript(transcript, "fixture-importer"); err != nil || result.Accepted != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 || len(state.Evidence) != 3 {
		t.Fatalf("facts=%d evidence=%d", len(state.Facts), len(state.Evidence))
	}
	for _, fact := range state.Facts {
		if fact.Completeness != Complete || len(fact.EvidenceIDs) != 3 {
			t.Fatalf("fact=%+v", fact)
		}
		if len(fact.Event.Lifecycle) != 7 {
			t.Fatalf("lifecycle=%v", fact.Event.Lifecycle)
		}
	}
	encoded, _ := json.Marshal(state)
	if bytes.Contains(encoded, []byte("SYNTHETIC_RAW_BODY")) {
		t.Fatal("raw OTLP body crossed durable boundary")
	}
}

func TestDuplicateReorderLateReplayDoesNotInflateFact(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	for _, sequence := range []int64{30, 2, 19, 2} {
		if err := receiver.ingestLogs(logsRequest("same-event", fixtureOTLPSchema, sequence), SourceOTLPLog); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ingestor.IngestHook(testRaw("same-event"), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestor.IngestHook(testRaw("same-event"), 1); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 || len(state.Evidence) != 2 {
		t.Fatalf("facts=%d evidence=%d", len(state.Facts), len(state.Evidence))
	}
	replays := uint64(0)
	for _, evidence := range state.Evidence {
		replays += evidence.ReplayCount
	}
	if replays != 4 {
		t.Fatalf("replays=%d", replays)
	}
}

func TestSourceLossLowersCompletenessWithoutDeletingFact(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	_, _ = ingestor.IngestHook(testRaw("loss-001"), 1)
	_ = receiver.ingestLogs(logsRequest("loss-001", fixtureOTLPSchema, 2), SourceOTLPLog)
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	_ = os.WriteFile(transcript, append(testRaw("loss-001"), '\n'), 0o600)
	_, _ = ingestor.ImportTranscript(transcript, "loss-importer")
	if err := ingestor.SetSourceLifecycle(SourceTranscript, SourceDisabled, true); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 {
		t.Fatal("fact deleted")
	}
	for _, fact := range state.Facts {
		if fact.Completeness != Partial {
			t.Fatalf("completeness=%s", fact.Completeness)
		}
	}
}

func TestSessionPseudonymIsStableAcrossEventsAndNeverPersistsNativeID(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	for _, eventID := range []string{"session-event-001", "session-event-002"} {
		if _, err := ingestor.IngestHook(testRaw(eventID), 1); err != nil {
			t.Fatal(err)
		}
	}
	state := store.Snapshot()
	sessionIDs := map[string]bool{}
	encoded, _ := json.Marshal(state)
	for _, fact := range state.Facts {
		sessionIDs[fact.Event.Scope.SessionID] = true
	}
	if len(sessionIDs) != 1 {
		t.Fatalf("session IDs=%v", sessionIDs)
	}
	if bytes.Contains(encoded, []byte("session-safe-001")) {
		t.Fatal("native session identifier crossed durable boundary")
	}
}

func TestUnknownSchemaIsMetadataOnlyQuarantineAndDegradedIncident(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	err := receiver.ingestLogs(logsRequest("unknown-001", "unreviewed/99", 1), SourceOTLPLog)
	if err == nil {
		t.Fatal("unknown schema accepted")
	}
	state := store.Snapshot()
	if len(state.Facts) != 0 || len(state.Quarantine) != 1 || len(state.Incidents) != 1 {
		t.Fatalf("state=%+v", state)
	}
	if state.Watermarks[string(SourceOTLPLog)].Lifecycle != SourceDegraded {
		t.Fatal("source not degraded")
	}
	encoded, _ := json.Marshal(state)
	for _, prohibited := range []string{"unknown-001", "unreviewed/99", "SYNTHETIC_RAW_BODY"} {
		if bytes.Contains(encoded, []byte(prohibited)) {
			t.Fatalf("metadata quarantine leaked %q", prohibited)
		}
	}
}

func TestCrashStagesAndRestartAreTransactional(t *testing.T) {
	for _, stage := range []CrashStage{CrashBeforeSync, CrashBeforeRename, CrashAfterRename} {
		t.Run(string(stage), func(t *testing.T) {
			store, ingestor, path := testIngestor(t, 4<<20)
			store.SetCrashStageForTest(stage)
			if _, err := ingestor.IngestHook(testRaw("crash-001"), 1); !errors.Is(err, ErrCrashInjected) {
				t.Fatalf("err=%v", err)
			}
			reopened, err := OpenFileStore(path, 4<<20)
			if err != nil {
				t.Fatal(err)
			}
			wantFacts := 0
			if stage == CrashAfterRename {
				wantFacts = 1
			}
			if len(reopened.Snapshot().Facts) != wantFacts {
				t.Fatalf("stage=%s facts=%d", stage, len(reopened.Snapshot().Facts))
			}
			if err := ValidateState(reopened.Snapshot()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFileStoreStrictRestartRejectsCorruptUnknownAndOrphanStateWithoutRewrite(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	if _, err := ingestor.IngestHook(testRaw("restart-valid-001"), 1); err != nil {
		t.Fatal(err)
	}
	base := store.Snapshot()
	validBytes, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	type mutation struct {
		name string
		raw  func() []byte
	}
	stateMutation := func(change func(*DurableState)) func() []byte {
		return func() []byte {
			candidate, cloneErr := cloneState(base)
			if cloneErr != nil {
				t.Fatal(cloneErr)
			}
			change(&candidate)
			encoded, marshalErr := json.Marshal(candidate)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			return encoded
		}
	}
	mutations := []mutation{
		{"duplicate_top_level", func() []byte {
			return bytes.Replace(validBytes, []byte(`"revision":1`), []byte(`"revision":1,"revision":1`), 1)
		}},
		{"unknown_top_level", func() []byte {
			return bytes.Replace(validBytes, []byte(`{`), []byte(`{"unknown_state":"rejected",`), 1)
		}},
		{"unknown_nested_event", func() []byte {
			return bytes.Replace(validBytes, []byte(`"event_type":`), []byte(`"unknown_event_field":true,"event_type":`), 1)
		}},
		{"unsupported_schema_version", stateMutation(func(state *DurableState) { state.SpecVersion = "kansoku.durable-state/99" })},
		{"fact_map_key", stateMutation(func(state *DurableState) {
			for key, fact := range state.Facts {
				delete(state.Facts, key)
				state.Facts[strings.Repeat("a", 64)] = fact
				break
			}
		})},
		{"missing_evidence_reference", stateMutation(func(state *DurableState) {
			for key, fact := range state.Facts {
				fact.EvidenceIDs = []string{"evd_" + strings.Repeat("a", 32)}
				state.Facts[key] = fact
				break
			}
		})},
		{"orphan_evidence", stateMutation(func(state *DurableState) {
			for _, evidence := range state.Evidence {
				evidence.EvidenceID = "evd_" + strings.Repeat("a", 32)
				state.Evidence[evidence.EvidenceID] = evidence
				break
			}
		})},
		{"invalid_typed_outcome", stateMutation(func(state *DurableState) {
			for key, fact := range state.Facts {
				fact.Event.Outcome = "success-ish"
				state.Facts[key] = fact
				break
			}
		})},
		{"unrecorded_contradiction", stateMutation(func(state *DurableState) {
			for key, evidence := range state.Evidence {
				evidence.Assertion.Outcome = "failed"
				state.Evidence[key] = evidence
				break
			}
		})},
		{"checkpoint_key", stateMutation(func(state *DurableState) {
			state.Checkpoints["fixture-importer"] = Checkpoint{ImporterID: "different-importer", Offset: 1, Sequence: 1, FileID: strings.Repeat("a", 64)}
		})},
		{"watermark_key", stateMutation(func(state *DurableState) {
			watermark := state.Watermarks[string(SourceHook)]
			state.Watermarks[string(SourceTranscript)] = watermark
		})},
		{"incident_key", stateMutation(func(state *DurableState) {
			incident := NewIncident("watermark_stall", SourceHook, fixedTime)
			state.Incidents["inc_"+strings.Repeat("a", 32)] = incident
		})},
		{"orphan_correlation", stateMutation(func(state *DurableState) {
			eventID := "evt_" + strings.Repeat("a", 32)
			correlation := Correlation{CorrelationID: "cor_" + stableID("correlation/1", eventID)[:32], EventID: eventID, Status: CorrelationExact, Candidates: []Candidate{}}
			state.Correlations[correlation.CorrelationID] = correlation
		})},
	}
	for _, item := range mutations {
		t.Run(item.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			raw := item.raw()
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			if _, err := OpenFileStore(path, 4<<20); err == nil {
				t.Fatal("accepted invalid durable state")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("invalid state was modified: err=%v", err)
			}
		})
	}
}

func TestImporterCheckpointNeverCommitsWithoutEventAndResumesAfterCrash(t *testing.T) {
	store, ingestor, path := testIngestor(t, 4<<20)
	transcript := filepath.Join(t.TempDir(), "checkpoint.jsonl")
	if err := os.WriteFile(transcript, append(testRaw("checkpoint-001"), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	store.SetCrashStageForTest(CrashBeforeRename)
	if _, err := ingestor.ImportTranscript(transcript, "checkpoint-importer"); !errors.Is(err, ErrCrashInjected) {
		t.Fatalf("err=%v", err)
	}
	reopened, err := OpenFileStore(path, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Snapshot().Facts) != 0 || len(reopened.Snapshot().Checkpoints) != 0 {
		t.Fatal("partial event/checkpoint transaction")
	}
	resumed, err := NewIngestor(reopened, bytes.Repeat([]byte("k"), 32), privacy.DefaultLimits(), 2)
	if err != nil {
		t.Fatal(err)
	}
	resumed.SetClockForTest(func() time.Time { return fixedTime.Add(time.Second) })
	result, err := resumed.ImportTranscript(transcript, "checkpoint-importer")
	if err != nil || result.Accepted != 1 || len(reopened.Snapshot().Checkpoints) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestImporterCheckpointUsesKeyedPathIdentityAndSupportsAppend(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	transcript := filepath.Join(t.TempDir(), "append.jsonl")
	if err := os.WriteFile(transcript, append(testRaw("append-001"), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := ingestor.ImportTranscript(transcript, "append-importer")
	if err != nil || first.Accepted != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(append(testRaw("append-002"), '\n')); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := ingestor.ImportTranscript(transcript, "append-importer")
	if err != nil || second.Accepted != 1 || len(store.Snapshot().Facts) != 2 {
		t.Fatalf("second=%+v facts=%d err=%v", second, len(store.Snapshot().Facts), err)
	}
	encoded, _ := json.Marshal(store.Snapshot().Checkpoints)
	if bytes.Contains(encoded, []byte(transcript)) {
		t.Fatal("raw transcript path crossed durable boundary")
	}
}

func TestContradictionCreatesIncidentAndPreservesFirstFact(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	if _, err := ingestor.IngestHook(testRaw("contradiction-001"), 1); err != nil {
		t.Fatal(err)
	}
	changed := map[string]any{}
	if err := json.Unmarshal(testRaw("contradiction-001"), &changed); err != nil {
		t.Fatal(err)
	}
	changed["outcome"] = "failed"
	raw, _ := json.Marshal(changed)
	if _, err := ingestor.ingestJSON(raw, SourceTranscript, 2, nil); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 || len(state.Evidence) != 2 || len(state.Incidents) != 1 {
		t.Fatalf("facts=%d evidence=%d incidents=%d", len(state.Facts), len(state.Evidence), len(state.Incidents))
	}
	for _, fact := range state.Facts {
		if fact.Event.Outcome != "succeeded" {
			t.Fatal("contradiction overwrote first fact")
		}
	}
	conflictingAssertion := false
	for _, evidence := range state.Evidence {
		if evidence.Source.Kind == SourceTranscript && evidence.Assertion == (EvidenceAssertion{EventType: "component.executed", Outcome: "failed", ValueState: "numeric_zero"}) {
			conflictingAssertion = true
		}
	}
	if !conflictingAssertion {
		t.Fatal("conflicting typed evidence assertion was not retained")
	}
}

func TestClockSkewAndWatermarkSequenceAreExplicit(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	ingestor.SetClockForTest(func() time.Time { return fixedTime.Add(time.Hour) })
	for _, sequence := range []uint64{1, 5, 3} {
		if _, err := ingestor.IngestHook(testRaw("skew-"+strconv.FormatUint(sequence, 10)), sequence); err != nil {
			t.Fatal(err)
		}
	}
	state := store.Snapshot()
	if state.Watermarks[string(SourceHook)].LastReadSequence != 5 || state.Watermarks[string(SourceHook)].GapCount != 3 {
		t.Fatalf("watermark=%+v", state.Watermarks[string(SourceHook)])
	}
	for _, fact := range state.Facts {
		if fact.Event.TimestampQuality != "source_clock_skewed" {
			t.Fatalf("quality=%s", fact.Event.TimestampQuality)
		}
	}
}

func TestDeterministicPropertyLoadMaintainsUniqueFactCardinality(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 16<<20)
	for round := 0; round < 3; round++ {
		for index := 79; index >= 0; index-- {
			logical := (index*37 + round*13) % 80
			if _, err := ingestor.IngestHook(testRaw("property-"+strconv.Itoa(logical)), uint64(index+1)); err != nil {
				t.Fatal(err)
			}
		}
	}
	state := store.Snapshot()
	if len(state.Facts) != 80 || len(state.Evidence) != 80 {
		t.Fatalf("facts=%d evidence=%d", len(state.Facts), len(state.Evidence))
	}
	for _, evidence := range state.Evidence {
		if evidence.ReplayCount != 2 {
			t.Fatalf("replays=%d", evidence.ReplayCount)
		}
	}
}

func TestCorrelationStatusesNeverForceAmbiguousCandidate(t *testing.T) {
	event := Event{EventID: "a"}
	cases := []struct {
		native     string
		candidates []Candidate
		want       CorrelationStatus
	}{
		{"native", nil, CorrelationExact}, {"", []Candidate{{EventID: "b", Confidence: .8}}, CorrelationCandidate},
		{"", []Candidate{{EventID: "b", Confidence: .8}, {EventID: "c", Confidence: .8}}, CorrelationAmbiguous}, {"", nil, CorrelationUnmatched},
	}
	for _, item := range cases {
		event.Source.NativeEventID = item.native
		if got := Correlate(event, item.candidates).Status; got != item.want {
			t.Fatalf("got=%s want=%s", got, item.want)
		}
	}
}

func TestWatermarkGapDiffersFromTrueInactivity(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	_, _ = ingestor.IngestHook(testRaw("health-001"), 1)
	clock := fixedTime.Add(2 * time.Minute)
	ingestor.SetClockForTest(func() time.Time { return clock })
	if err := ingestor.AuditSource(SourceHook, false); err != nil {
		t.Fatal(err)
	}
	if len(store.Snapshot().Incidents) != 0 || !store.Snapshot().Watermarks[string(SourceHook)].Inactivity {
		t.Fatal("inactivity became gap")
	}
	if err := ingestor.AuditSource(SourceHook, true); err != nil {
		t.Fatal(err)
	}
	if len(store.Snapshot().Incidents) != 1 || store.Snapshot().Watermarks[string(SourceHook)].GapCount != 1 {
		t.Fatal("eligible stall not detected")
	}
}

func TestDurableSpoolIsBounded0600AndReplaySafe(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("secure_spool_unsupported: no fd-relative openat/inode-binding spool backend outside linux (see spool_unsupported.go's //go:build !linux fallback); this is a pre-existing, intentional OS-gated limitation predating Session 11, not a regression, on GOOS=%s", runtime.GOOS)
	}
	store, ingestor, _ := testIngestor(t, 4<<20)
	records, safeErr := ingestor.sanitizer.DecodeAndExtract(bytes.NewReader(testRaw("spool-001")), privacy.FixtureSourceSchema())
	if safeErr != nil {
		t.Fatal(safeErr)
	}
	event, evidence, _ := NormalizedFromSafe(records[0], SourceHook, 1, fixedTime)
	event.Lifecycle = append(event.Lifecycle, StageDeduped, StageCorrelated, StageReconciled)
	request := CommitRequest{Event: &event, Evidence: &evidence}
	path := filepath.Join(t.TempDir(), "spool.ndjson")
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	spool, err := NewDurableSpool(path, 8<<10)
	if err != nil {
		t.Fatal(err)
	}
	if err := spool.Append(request); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	if err := spool.Replay(func(item CommitRequest) error { _, err := store.Commit(item); return err }); err != nil {
		t.Fatal(err)
	}
	if len(store.Snapshot().Facts) != 1 {
		t.Fatal("spool did not replay")
	}
	for count := 0; ; count++ {
		if err := spool.Append(request); errors.Is(err, ErrBackpressure) {
			break
		} else if err != nil || count > 100 {
			t.Fatalf("err=%v count=%d", err, count)
		}
	}
}

func TestDurableSpoolRejectsUnsafeParentsFilesAndLinksWithoutModification(t *testing.T) {
	sentinel := []byte("preserve-unsafe-spool-bytes")
	unsafeParent := t.TempDir()
	if err := os.Chmod(unsafeParent, 0o755); err != nil {
		t.Fatal(err)
	}
	unsafeParentPath := filepath.Join(unsafeParent, "fresh.ndjson")
	if _, err := NewDurableSpool(unsafeParentPath, 8<<10); err == nil {
		t.Fatal("accepted non-private parent")
	}
	if _, err := os.Lstat(unsafeParentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsafe-parent validation created a file")
	}

	for _, item := range []struct {
		name  string
		setup func(t *testing.T, directory, path string)
	}{
		{"mode_0644", func(t *testing.T, _, path string) {
			if err := os.WriteFile(path, sentinel, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink", func(t *testing.T, directory, path string) {
			target := filepath.Join(directory, "target.ndjson")
			if err := os.WriteFile(target, sentinel, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"hardlink", func(t *testing.T, directory, path string) {
			target := filepath.Join(directory, "target.ndjson")
			if err := os.WriteFile(target, sentinel, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(target, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory", func(t *testing.T, _, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(item.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "spool.ndjson")
			item.setup(t, directory, path)
			before, statErr := os.Lstat(path)
			if statErr != nil {
				t.Fatal(statErr)
			}
			var beforeBytes []byte
			if before.Mode().IsRegular() {
				beforeBytes, _ = os.ReadFile(path)
			}
			if _, err := NewDurableSpool(path, 8<<10); err == nil {
				t.Fatal("accepted unsafe existing spool")
			}
			after, err := os.Lstat(path)
			if err != nil || after.Mode() != before.Mode() || after.Size() != before.Size() {
				t.Fatalf("unsafe entry modified: before=%v after=%v err=%v", before.Mode(), after.Mode(), err)
			}
			if before.Mode().IsRegular() {
				afterBytes, _ := os.ReadFile(path)
				if !bytes.Equal(beforeBytes, afterBytes) {
					t.Fatal("unsafe existing bytes modified")
				}
			}
		})
	}

	// The preceding unsafe-parent/mode/symlink/hardlink/directory rejections
	// above hold on every OS: spool_unsupported.go's !linux fallback fails
	// closed (rejects everything), which still satisfies "never accept an
	// unsafe path". Only this final "a genuinely safe existing spool is
	// accepted" assertion requires the real fd-relative openat/inode-binding
	// backend spool_linux.go implements -- a pre-existing, intentional
	// OS-gated limitation predating Session 11 (see spool_unsupported.go),
	// not a regression, so it is skipped rather than failed on non-linux.
	if runtime.GOOS != "linux" {
		t.Skipf("secure_spool_unsupported: no fd-relative openat/inode-binding spool backend outside linux, GOOS=%s", runtime.GOOS)
	}
	safeDirectory := t.TempDir()
	if err := os.Chmod(safeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	safePath := filepath.Join(safeDirectory, "existing.ndjson")
	if err := os.WriteFile(safePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDurableSpool(safePath, 8<<10); err != nil {
		t.Fatalf("rejected safe existing spool: %v", err)
	}
}

func TestBackpressureAndPoisonAreBounded(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4096)
	ingestor.capacity <- struct{}{}
	for count := 1; count < cap(ingestor.capacity); count++ {
		ingestor.capacity <- struct{}{}
	}
	if _, err := ingestor.IngestHook(testRaw("busy"), 1); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("err=%v", err)
	}
	for len(ingestor.capacity) > 0 {
		<-ingestor.capacity
	}
	for count := 0; count < 20; count++ {
		_, _ = ingestor.IngestHook(testRaw(strings.Repeat("x", count+1)), uint64(count))
	}
	if len(store.Snapshot().Facts) == 20 {
		t.Fatal("bounded store accepted unbounded load")
	}
}

func TestHTTPHookAndOTLPProtobufReuseLocalSecurityBoundary(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	bearer := bytes.Repeat([]byte("b"), 32)
	guard, err := localhttp.NewGuard([]string{"127.0.0.1", "::1", "localhost"}, []string{"http://127.0.0.1:3000", "http://[::1]:3000", "http://localhost:3000"}, bearer, bytes.Repeat([]byte("c"), 32), 1<<20, 120, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := NewIngressHTTPHandler(guard, ingestor, receiver)
	hook := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4318/v1/hooks/fixture-agent/tool_finished", bytes.NewReader(testRaw("http-hook")))
	hook.Host, hook.RemoteAddr = "127.0.0.1:4318", "127.0.0.1:52000"
	hook.Header.Set("Authorization", "Bearer "+string(bearer))
	hookResponse := httptest.NewRecorder()
	handler.ServeHTTP(hookResponse, hook)
	if hookResponse.Code != http.StatusOK {
		t.Fatalf("hook status=%d body=%s", hookResponse.Code, hookResponse.Body.String())
	}
	encoded, _ := proto.Marshal(logsRequest("http-otlp", fixtureOTLPSchema, 2))
	otlpRequest := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4318/v1/logs", bytes.NewReader(encoded))
	otlpRequest.Host, otlpRequest.RemoteAddr = "127.0.0.1:4318", "127.0.0.1:52001"
	otlpRequest.Header.Set("Authorization", "Bearer "+string(bearer))
	otlpRequest.Header.Set("Content-Type", otlpContentType)
	otlpResponse := httptest.NewRecorder()
	handler.ServeHTTP(otlpResponse, otlpRequest)
	if otlpResponse.Code != http.StatusOK || otlpResponse.Header().Get("Content-Type") != otlpContentType {
		t.Fatalf("otlp status=%d body=%s", otlpResponse.Code, otlpResponse.Body.String())
	}
	if len(store.Snapshot().Facts) != 2 {
		t.Fatalf("facts=%d", len(store.Snapshot().Facts))
	}
	unauthorized := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4318/v1/logs", bytes.NewReader(encoded))
	unauthorized.Host, unauthorized.RemoteAddr = "127.0.0.1:4318", "127.0.0.1:52002"
	unauthorized.Header.Set("Content-Type", otlpContentType)
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, unauthorized)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", denied.Code)
	}
}

// TestRealAdapterHookStdinPayloadsReachCodexAndClaudeHookRoutes proves TDD
// 11.B step 6's requirement that a synthetic stdin payload shaped exactly
// like what the installed codex.user_hook/claude.user_hook helper would
// forward (contracts/codex/hooks-and-otel.yaml / contracts/claude/hooks-and-
// otel.yaml's hook_source stdin shape) is a real event through the existing
// codexHookHandler/claudeHookHandler routes -- the same generic
// /v1/hooks/{adapter}/{event} mux fixture-agent already uses, no second
// ingress mechanism. Already-sanitized adapter output enters the shared
// canonical safe-field path with hook_http lineage; it is never re-decoded
// against the fixture-agent-only wire schema.
func TestRealAdapterHookStdinPayloadsReachCodexAndClaudeHookRoutes(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	bearer := bytes.Repeat([]byte("b"), 32)
	guard, err := localhttp.NewGuard([]string{"127.0.0.1", "::1", "localhost"}, []string{"http://127.0.0.1:3000", "http://[::1]:3000", "http://localhost:3000"}, bearer, bytes.Repeat([]byte("c"), 32), 1<<20, 120, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewIngressHTTPHandler(guard, ingestor, receiver)
	if err != nil {
		t.Fatal(err)
	}

	send := func(path string, body []byte, remote string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4318"+path, bytes.NewReader(body))
		request.Host, request.RemoteAddr = "127.0.0.1:4318", remote
		request.Header.Set("Authorization", "Bearer "+string(bearer))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	// A hook payload for an event outside the closed vocabulary is rejected
	// by codexadapter/claudeadapter's own DecodeHookInput before it ever
	// reaches the Ingestor -- this is the real per-adapter handler running,
	// not a generic passthrough.
	unsupportedCodex, _ := json.Marshal(map[string]any{"hook_event_name": "SomeFutureEvent", "session_id": "codex-real-session-1"})
	unsupportedResponse := send("/v1/hooks/codex/SomeFutureEvent", unsupportedCodex, "127.0.0.1:52012")
	if unsupportedResponse.Code != http.StatusBadRequest {
		t.Fatalf("an event outside codexadapter's closed hook vocabulary must be rejected by the real handler, status=%d body=%s", unsupportedResponse.Code, unsupportedResponse.Body.String())
	}

	// A well-formed, documented SessionStart payload for both adapters is
	// decoded, mapped, allowlist-validated and committed.
	codexStdin, _ := json.Marshal(map[string]any{"hook_event_name": "SessionStart", "session_id": "codex-real-session-1"})
	codexResponse := send("/v1/hooks/codex/SessionStart", codexStdin, "127.0.0.1:52010")
	if codexResponse.Code != http.StatusOK {
		t.Fatalf("codex hook status=%d body=%s", codexResponse.Code, codexResponse.Body.String())
	}

	claudeStdin, _ := json.Marshal(map[string]any{"hook_event_name": "SessionStart", "session_id": "claude-real-session-1"})
	claudeResponse := send("/v1/hooks/claude/SessionStart", claudeStdin, "127.0.0.1:52011")
	if claudeResponse.Code != http.StatusOK {
		t.Fatalf("claude hook status=%d body=%s", claudeResponse.Code, claudeResponse.Body.String())
	}

	state := store.Snapshot()
	if len(state.Facts) != 2 || len(state.Quarantine) != 0 {
		t.Fatalf("facts=%d quarantine=%d, want two committed hooks and no quarantine", len(state.Facts), len(state.Quarantine))
	}
	for _, fact := range state.Facts {
		if fact.Event.EventType != "session.started" ||
			fact.Event.Source.Kind != SourceHook ||
			fact.Event.Source.SchemaID != fact.Event.Source.AdapterID+".hook/1" {
			t.Fatalf("unexpected hook lineage: %+v", fact.Event)
		}
	}
}

func TestGRPCProtobufLogsMetricsAndTracesWithLoopbackAuth(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	bearer := bytes.Repeat([]byte("g"), 32)
	server, err := NewIngressGRPCServer(receiver, bearer)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+string(bearer))
	if _, err := collectorlogsv1.NewLogsServiceClient(connection).Export(ctx, logsRequest("grpc-log", fixtureOTLPSchema, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := collectormetricsv1.NewMetricsServiceClient(connection).Export(ctx, metricRequest("grpc-metric", 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := collectortracev1.NewTraceServiceClient(connection).Export(ctx, traceRequest("grpc-trace", 3)); err != nil {
		t.Fatal(err)
	}
	if len(store.Snapshot().Facts) != 3 {
		t.Fatalf("facts=%d", len(store.Snapshot().Facts))
	}
}

func TestProductionGRPCServerEnforcesExactOneMiBBoundaryForEverySignal(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 16<<20)
	receiver, _ := NewOTLPReceiver(ingestor, maxOTLPFrameBytes)
	bearer := bytes.Repeat([]byte("m"), 32)
	server, err := NewIngressGRPCServer(receiver, bearer)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+string(bearer))

	boundaryLogs, boundaryMetrics, boundaryTraces := sizedSignalRequests(t, maxOTLPFrameBytes, "boundary")
	if _, err := collectorlogsv1.NewLogsServiceClient(connection).Export(ctx, boundaryLogs); err != nil {
		t.Fatalf("logs boundary rejected: %v", err)
	}
	if _, err := collectormetricsv1.NewMetricsServiceClient(connection).Export(ctx, boundaryMetrics); err != nil {
		t.Fatalf("metrics boundary rejected: %v", err)
	}
	if _, err := collectortracev1.NewTraceServiceClient(connection).Export(ctx, boundaryTraces); err != nil {
		t.Fatalf("traces boundary rejected: %v", err)
	}
	acceptedRevision := store.Snapshot().Revision
	oversizedLogs, oversizedMetrics, oversizedTraces := sizedSignalRequests(t, maxOTLPFrameBytes+1, "oversized")
	for name, call := range map[string]func() error{
		"logs": func() error {
			_, err := collectorlogsv1.NewLogsServiceClient(connection).Export(ctx, oversizedLogs)
			return err
		},
		"metrics": func() error {
			_, err := collectormetricsv1.NewMetricsServiceClient(connection).Export(ctx, oversizedMetrics)
			return err
		},
		"traces": func() error {
			_, err := collectortracev1.NewTraceServiceClient(connection).Export(ctx, oversizedTraces)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if code := status.Code(call()); code != codes.ResourceExhausted {
				t.Fatalf("oversized status=%s", code)
			}
		})
	}
	if store.Snapshot().Revision != acceptedRevision || len(store.Snapshot().Facts) != 3 {
		t.Fatal("oversized gRPC request crossed durable boundary")
	}
}

func TestOTLPHTTPRejectsJSONCompressionAndOversizeWithoutDurability(t *testing.T) {
	store, ingestor, _ := testIngestor(t, 4<<20)
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	for _, item := range []struct {
		name, method, contentType, encoding string
		body                                []byte
		want                                int
	}{
		{"method", http.MethodGet, otlpContentType, "", []byte{}, http.StatusMethodNotAllowed},
		{"json", http.MethodPost, "application/json", "", []byte("{}"), http.StatusUnsupportedMediaType},
		{"gzip", http.MethodPost, otlpContentType, "gzip", []byte("compressed"), http.StatusUnsupportedMediaType},
		{"malformed", http.MethodPost, otlpContentType, "", []byte("not-protobuf"), http.StatusBadRequest},
		{"oversize", http.MethodPost, otlpContentType, "", bytes.Repeat([]byte("x"), 1<<20+1), http.StatusRequestEntityTooLarge},
	} {
		t.Run(item.name, func(t *testing.T) {
			request := httptest.NewRequest(item.method, "/v1/logs", bytes.NewReader(item.body))
			request.Header.Set("Content-Type", item.contentType)
			if item.encoding != "" {
				request.Header.Set("Content-Encoding", item.encoding)
			}
			response := httptest.NewRecorder()
			receiver.HTTPMux().ServeHTTP(response, request)
			if response.Code != item.want {
				t.Fatalf("got=%d want=%d", response.Code, item.want)
			}
			if response.Header().Get("Content-Type") != otlpContentType {
				t.Fatalf("non-protobuf error content type %q", response.Header().Get("Content-Type"))
			}
			var safeStatus statuspb.Status
			if err := proto.Unmarshal(response.Body.Bytes(), &safeStatus); err != nil || safeStatus.Message == "" {
				t.Fatalf("invalid protobuf status: status=%+v err=%v", &safeStatus, err)
			}
		})
	}
	if store.Snapshot().Revision != 0 {
		t.Fatal("rejected request became durable")
	}
}

func FuzzNormalizationIdentityStable(f *testing.F) {
	f.Add("fuzz-safe-001")
	f.Add("fuzz-safe-002")
	f.Fuzz(func(t *testing.T, eventID string) {
		if eventID == "" || len(eventID) > 128 {
			t.Skip()
		}
		sanitizer, err := privacy.NewIngressSanitizer(bytes.Repeat([]byte("f"), 32), privacy.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		sanitizer.SetClockForTest(func() time.Time { return fixedTime.Add(time.Second) })
		records, safeErr := sanitizer.DecodeAndExtract(bytes.NewReader(testRaw(eventID)), privacy.FixtureSourceSchema())
		if safeErr != nil {
			t.Skip()
		}
		firstEvent, firstEvidence, err := NormalizedFromSafe(records[0], SourceHook, 1, fixedTime.Add(time.Second))
		if err != nil {
			t.Skip()
		}
		secondEvent, secondEvidence, err := NormalizedFromSafe(records[0], SourceHook, 999, fixedTime.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if firstEvent.FactKey != secondEvent.FactKey || firstEvent.EventID != secondEvent.EventID || firstEvidence.EvidenceID != secondEvidence.EvidenceID {
			t.Fatal("normalization identity changed across replay time or sequence")
		}
	})
}
