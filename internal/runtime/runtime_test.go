package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/observability"
)

type memorySpool struct {
	mu       sync.Mutex
	requests []observability.CommitRequest
}

func (s *memorySpool) Append(request observability.CommitRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request)
	return nil
}

func (s *memorySpool) Replay(commit func(observability.CommitRequest) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, request := range s.requests {
		if err := commit(request); err != nil {
			return err
		}
	}
	s.requests = nil
	return nil
}

func (s *memorySpool) Stats() (observability.SpoolStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := observability.SpoolStats{Depth: len(s.requests)}
	if len(s.requests) > 0 {
		stats.OldestAt = s.requests[0].Event.IngestedAt
	}
	return stats, nil
}

type factSink struct {
	err error
}

func (s factSink) PersistNormalizedFact(observability.Event, observability.Evidence) error {
	return s.err
}

func testSpools() map[observability.SourceKind]SpoolStore {
	result := map[observability.SourceKind]SpoolStore{}
	for _, source := range productionSources {
		result[source] = &memorySpool{}
	}
	return result
}

func safeFact(source observability.SourceKind, suffix string) (observability.Event, observability.Evidence) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	event := observability.Event{
		SpecVersion: observability.EventSpecVersion, EventID: "evt_" + suffix,
		FactKey: "fact_" + suffix, EventType: "component.executed",
		ObservedAt: now, IngestedAt: now,
		Source: observability.SourceRef{Kind: source},
	}
	evidence := observability.Evidence{EvidenceID: "evd_" + suffix, EventID: event.EventID}
	return event, evidence
}

func TestQueueReservesPerLaneBeforeAcceptanceAndPreservesFairness(t *testing.T) {
	queue, err := NewDurableIngressQueueWithSpools(factSink{}, testSpools(), 64)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	var reservations []observability.DurableFactReservation
	for index := 0; index < 64; index++ {
		event, evidence := safeFact(observability.SourceHook, string(rune('a'+index%26)))
		reservation, err := queue.ReserveNormalizedFact(event, evidence)
		if err != nil {
			t.Fatalf("reservation %d: %v", index, err)
		}
		reservations = append(reservations, reservation)
	}
	event, evidence := safeFact(observability.SourceHook, "full")
	if _, err := queue.ReserveNormalizedFact(event, evidence); !errors.Is(err, observability.ErrBackpressure) {
		t.Fatalf("full hook lane error=%v", err)
	}
	event, evidence = safeFact(observability.SourceOTLPLog, "independent")
	independent, err := queue.ReserveNormalizedFact(event, evidence)
	if err != nil {
		t.Fatalf("independent lane starved: %v", err)
	}
	independent.Cancel()
	for _, reservation := range reservations {
		reservation.Cancel()
	}
}

func TestQueueAcknowledgesSpoolOnlyAfterDatabaseFailure(t *testing.T) {
	spools := testSpools()
	queue, err := NewDurableIngressQueueWithSpools(factSink{err: errors.New("database unavailable")}, spools, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	event, evidence := safeFact(observability.SourceOTLPMetric, "spooled")
	if err := queue.PersistNormalizedFact(event, evidence); err != nil {
		t.Fatalf("fsynced spool fallback should acknowledge: %v", err)
	}
	stats, err := spools[observability.SourceOTLPMetric].Stats()
	if err != nil || stats.Depth != 1 {
		t.Fatalf("spool stats=%+v err=%v", stats, err)
	}
	metrics, err := queue.Metrics()
	if err != nil || metrics.Spooled != 1 || metrics.Accepted != 1 {
		t.Fatalf("metrics=%+v err=%v", metrics, err)
	}
}

func TestLoadConfigRejectsUnknownFieldsAndNonLoopbackDirectMode(t *testing.T) {
	config := validTestConfig(t.TempDir())
	encoded, _ := json.Marshal(config)
	var document map[string]any
	_ = json.Unmarshal(encoded, &document)
	document["unknown"] = true
	invalid, _ := json.Marshal(document)
	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(path, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("unknown config field accepted")
	}
	config.HTTPListen = "0.0.0.0:43100"
	if err := config.Validate(); err == nil {
		t.Fatal("wildcard direct-mode listener accepted")
	}
}

func TestSecretsAreFileOnlyDistinctAnd0600(t *testing.T) {
	root := t.TempDir()
	files := SecretFiles{}
	targets := []*string{
		&files.IngressBearer, &files.ReadBearer, &files.MutationBearer,
		&files.CSRF, &files.IdentityHMAC, &files.AuditHMAC, &files.DatabasePassword,
	}
	for index, target := range targets {
		*target = filepath.Join(root, string(rune('a'+index)))
		value := bytes.Repeat([]byte{byte(index + 1)}, 32)
		if err := os.WriteFile(*target, append(value, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadSecretFiles(files); err != nil {
		t.Fatalf("valid secret files: %v", err)
	}
	if err := os.Chmod(files.ReadBearer, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecretFiles(files); err == nil {
		t.Fatal("world-readable secret accepted")
	}
}

type deterministicSoakDriver struct {
	mu     sync.Mutex
	ledger []string
	faults []SoakFault
}

func (d *deterministicSoakDriver) DriverKind() string { return "unit_fixture" }
func (d *deterministicSoakDriver) Ingest(_ context.Context, cycle int) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := "evt"
	if cycle%2 == 0 {
		id = "evt_even"
	}
	d.ledger = append(d.ledger, id)
	return id, nil
}
func (*deterministicSoakDriver) QueryRollup(context.Context, int) error         { return nil }
func (*deterministicSoakDriver) BackupCountSnapshot(context.Context, int) error { return nil }
func (d *deterministicSoakDriver) ExecuteFault(_ context.Context, fault SoakFault) error {
	d.faults = append(d.faults, fault)
	return nil
}
func (*deterministicSoakDriver) Recover(context.Context) error { return nil }
func (*deterministicSoakDriver) IsDurable(context.Context, string) (bool, error) {
	return true, nil
}
func (d *deterministicSoakDriver) Snapshot(context.Context) (SoakSnapshot, error) {
	return SoakSnapshot{
		FactCount: 2, EvidenceReplayCount: int64(len(d.ledger) - 2),
		BackupCountsMatch: true, DiagnosticsSafe: true,
	}, nil
}
func (*deterministicSoakDriver) Close(context.Context) error { return nil }

func TestAcceleratedSoakRuns168CyclesAndAllNamedFaultsWithoutSevenDayClaim(t *testing.T) {
	driver := &deterministicSoakDriver{}
	path := filepath.Join(t.TempDir(), "soak.json")
	evidence, err := RunAcceleratedSoakWithDriver(context.Background(), driver, path, func() time.Time {
		return time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.CyclesCompleted != 168 || evidence.WallClockSevenDays ||
		len(evidence.FaultsExecuted) != 3 || evidence.Status != "pass" {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func TestForbiddenResponseKeysAreRejectedRecursively(t *testing.T) {
	if !containsForbiddenResponseKey([]byte(`{"data":[{"tool_input":"x"}]}`)) {
		t.Fatal("nested prohibited response key accepted")
	}
	if containsForbiddenResponseKey([]byte(`{"data":{"event_id":"safe"}}`)) {
		t.Fatal("safe response rejected")
	}
}

func validTestConfig(root string) Config {
	return Config{
		Version: ConfigVersion, AppVersion: AppVersion,
		HTTPListen: "127.0.0.1:43100", OTLPHTTPListen: "127.0.0.1:4318",
		OTLPGRPCListen: "127.0.0.1:4317", DataDir: filepath.Join(root, "data"),
		BackupDir: filepath.Join(root, "backup"),
		Database:  DBConfig{Host: "127.0.0.1", Port: 5432, Name: "kansoku", User: "kansoku", SSLMode: "disable", ConnectTimeout: 5},
		Secrets: SecretFiles{
			IngressBearer: filepath.Join(root, "s1"), ReadBearer: filepath.Join(root, "s2"),
			MutationBearer: filepath.Join(root, "s3"), CSRF: filepath.Join(root, "s4"),
			IdentityHMAC: filepath.Join(root, "s5"), AuditHMAC: filepath.Join(root, "s6"),
			DatabasePassword: filepath.Join(root, "s7"),
		},
		QueueCapacity: 64, SpoolMaxBytes: 64 << 20, ShutdownTimeoutMS: 30_000,
		QueryTimeoutMS: 500, ResponseMaxBytes: 1 << 20, RetentionDays: 400,
		DiskBudgetFraction: .90, DiagnosticsMaxBytes: 1 << 20,
		PrivacyCanaryFixture: filepath.Join(root, "privacy-canary.json"),
	}
}
