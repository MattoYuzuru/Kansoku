package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

type runtimeSourceStub struct {
	scanErr error
	scans   int
}

func (s *runtimeSourceStub) ScanOnce(context.Context) error {
	s.scans++
	return s.scanErr
}

func (*runtimeSourceStub) Run(context.Context) {}

type appServerRuntimeSourceStub struct {
	configures int
	err        error
}

func (s *appServerRuntimeSourceStub) Configure(context.Context) error {
	s.configures++
	return s.err
}

type recoveringFactSink struct {
	mu       sync.Mutex
	failures int
}

func (s *recoveringFactSink) PersistNormalizedFact(observability.Event, observability.Evidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures > 0 {
		s.failures--
		return errors.New("projection temporarily unavailable")
	}
	return nil
}

type memoryIngestionHealthRecorder struct {
	mu       sync.Mutex
	snapshot DurableIngestionHealth
	fail     bool
}

func (r *memoryIngestionHealthRecorder) Load(context.Context) (DurableIngestionHealth, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return DurableIngestionHealth{}, errors.New("ingestion health persistence unavailable")
	}
	return r.snapshot, nil
}

func (r *memoryIngestionHealthRecorder) Record(
	_ observability.SourceKind,
	outcome IngestionHealthOutcome,
	at time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("ingestion health persistence unavailable")
	}
	switch outcome {
	case ingestionHealthSuccessful:
		r.snapshot.LastSuccessful = at
	case ingestionHealthBackpressureRejected:
		r.snapshot.BackpressureRejected++
		r.snapshot.LastRejected = at
	case ingestionHealthDurabilityUnavailable:
		r.snapshot.DurabilityUnavailable++
		r.snapshot.LastRejected = at
	}
	return nil
}

func (r *memoryIngestionHealthRecorder) setFailure(fail bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = fail
}

func testSpools() map[observability.SourceKind]SpoolStore {
	result := map[observability.SourceKind]SpoolStore{}
	for _, source := range productionSources {
		result[source] = &memorySpool{}
	}
	return result
}

func TestDegradedRolloutInitialScanDoesNotBlockRuntimeActivation(t *testing.T) {
	inventory := &runtimeSourceStub{}
	rollout := &runtimeSourceStub{scanErr: errors.New("rollout scan degraded")}
	appServer := &appServerRuntimeSourceStub{}
	appliance := &Appliance{
		Inventory: inventory,
		Rollout:   rollout,
		AppServer: appServer,
	}

	if err := appliance.activateRuntimeSources(context.Background()); err != nil {
		t.Fatalf("degraded optional rollout source blocked runtime activation: %v", err)
	}
	if inventory.scans != 1 || rollout.scans != 1 || appServer.configures != 1 {
		t.Fatalf(
			"activation calls inventory=%d rollout=%d app_server=%d",
			inventory.scans, rollout.scans, appServer.configures,
		)
	}
}

func TestRolloutHealthPersistenceFailureBlocksRuntimeActivation(t *testing.T) {
	inventory := &runtimeSourceStub{}
	rollout := &runtimeSourceStub{scanErr: errCodexRolloutHealthPersistence}
	appServer := &appServerRuntimeSourceStub{}
	appliance := &Appliance{
		Inventory: inventory,
		Rollout:   rollout,
		AppServer: appServer,
	}

	err := appliance.activateRuntimeSources(context.Background())
	if err == nil || err.Error() != "codex_rollout_source_health_initialization_failed" {
		t.Fatalf("health persistence failure did not fail closed: %v", err)
	}
	if inventory.scans != 1 || rollout.scans != 1 || appServer.configures != 0 {
		t.Fatalf(
			"activation calls inventory=%d rollout=%d app_server=%d",
			inventory.scans, rollout.scans, appServer.configures,
		)
	}
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

func TestQueueReplaysSanitizedProjectionRetryAndDrainsSpool(t *testing.T) {
	spools := testSpools()
	sink := &recoveringFactSink{failures: 1}
	queue, err := NewDurableIngressQueueWithSpools(sink, spools, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	event, evidence := safeFact(observability.SourceEvidenceBridge, "projection-retry")
	if err := queue.PersistNormalizedFact(event, evidence); err != nil {
		t.Fatalf("sanitized spool fallback should acknowledge: %v", err)
	}
	before, _ := spools[observability.SourceEvidenceBridge].Stats()
	if before.Depth != 1 {
		t.Fatalf("spool depth before replay=%d, want 1", before.Depth)
	}
	if err := queue.ReplaySpools(); err != nil {
		t.Fatalf("replay: %v", err)
	}
	after, _ := spools[observability.SourceEvidenceBridge].Stats()
	if after.Depth != 0 {
		t.Fatalf("spool depth after replay=%d, want 0", after.Depth)
	}
}

func TestQueueHealthCountersReloadAcrossRestart(t *testing.T) {
	recorder := &memoryIngestionHealthRecorder{snapshot: DurableIngestionHealth{
		BackpressureRejected:  3,
		DurabilityUnavailable: 2,
	}}
	queue, err := NewDurableIngressQueueWithSpools(factSink{}, testSpools(), 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.ConfigureHealthRecorder(context.Background(), recorder); err != nil {
		t.Fatal(err)
	}
	var reservations []observability.DurableFactReservation
	for index := 0; index < 64; index++ {
		event, evidence := safeFact(observability.SourceHook, string(rune('a'+index%26)))
		reservation, reserveErr := queue.ReserveNormalizedFact(event, evidence)
		if reserveErr != nil {
			t.Fatalf("reservation %d: %v", index, reserveErr)
		}
		reservations = append(reservations, reservation)
	}
	event, evidence := safeFact(observability.SourceHook, "durable-counter")
	if _, err := queue.ReserveNormalizedFact(event, evidence); !errors.Is(err, observability.ErrBackpressure) {
		t.Fatalf("backpressure error=%v", err)
	}
	for _, reservation := range reservations {
		reservation.Cancel()
	}
	queue.Close()

	restarted, err := NewDurableIngressQueueWithSpools(factSink{}, testSpools(), 64)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.ConfigureHealthRecorder(context.Background(), recorder); err != nil {
		t.Fatal(err)
	}
	metrics, err := restarted.Metrics()
	if err != nil {
		t.Fatal(err)
	}
	if metrics.BackpressureRejected != 4 || metrics.DurabilityUnavailable != 2 ||
		metrics.LastRejectedIngest.IsZero() {
		t.Fatalf("reloaded metrics=%+v", metrics)
	}
}

func TestQueueCounterPersistenceHealthClearsAfterSuccessfulWrite(t *testing.T) {
	recorder := &memoryIngestionHealthRecorder{}
	queue, err := NewDurableIngressQueueWithSpools(factSink{}, testSpools(), 64)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	if err := queue.ConfigureHealthRecorder(context.Background(), recorder); err != nil {
		t.Fatal(err)
	}
	recorder.setFailure(true)
	event, evidence := safeFact(observability.SourceHook, "health-write-failed")
	if err := queue.PersistNormalizedFact(event, evidence); err != nil {
		t.Fatal(err)
	}
	failed, err := queue.Metrics()
	if err != nil || !failed.CounterPersistenceUnavailable {
		t.Fatalf("failed recorder health=%+v err=%v", failed, err)
	}
	recorder.setFailure(false)
	event, evidence = safeFact(observability.SourceHook, "health-write-recovered")
	if err := queue.PersistNormalizedFact(event, evidence); err != nil {
		t.Fatal(err)
	}
	recovered, err := queue.Metrics()
	if err != nil || recovered.CounterPersistenceUnavailable {
		t.Fatalf("recovered recorder health=%+v err=%v", recovered, err)
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

func TestDatabaseSoftBudgetThresholdsAreAdvisoryAndOrdered(t *testing.T) {
	config := validTestConfig(t.TempDir())
	for _, test := range []struct {
		bytes int64
		state string
	}{
		{0, "pass"},
		{int64(float64(config.DatabaseSoftLimitBytes) * .70), "warning"},
		{int64(float64(config.DatabaseSoftLimitBytes) * .85), "degraded"},
		{int64(float64(config.DatabaseSoftLimitBytes) * .95), "critical"},
		{config.DatabaseSoftLimitBytes + 1, "critical"},
	} {
		measure := databaseBudgetMeasure(test.bytes, config, nil)
		if measure.State != test.state || measure.BudgetBytes != 5<<30 {
			t.Fatalf("bytes=%d measure=%+v want state=%s", test.bytes, measure, test.state)
		}
	}
}

func TestSourceFreshnessCannotRemainPassWhenEvidenceIsDegraded(t *testing.T) {
	now := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	committed := now.Add(-time.Second)
	observed := now.Add(-2 * time.Second)
	state, clock := sourceWatermarkHealthState(
		&committed, &observed, 0, false, now,
	)
	if state != "pass" || clock != "source_rfc3339" {
		t.Fatalf("healthy watermark state=%q clock=%q", state, clock)
	}
	future := now.Add(6 * time.Minute)
	state, clock = sourceWatermarkHealthState(
		&committed, &future, 0, false, now,
	)
	if state != "degraded" || clock != "source_clock_skewed" {
		t.Fatalf("future watermark state=%q clock=%q", state, clock)
	}
	for name, test := range map[string]struct {
		committed *time.Time
		gaps      int64
		inactive  bool
	}{
		"missing_commit": {committed: nil},
		"sequence_gap":   {committed: &committed, gaps: 1},
		"inactive":       {committed: &committed, inactive: true},
	} {
		t.Run(name, func(t *testing.T) {
			state, _ := sourceWatermarkHealthState(
				test.committed, &observed, test.gaps, test.inactive, now,
			)
			if state != "degraded" {
				t.Fatalf("state=%q, want degraded", state)
			}
		})
	}
	for name, test := range map[string]struct {
		state      string
		valueState string
		want       string
	}{
		"producing":  {state: "producing", valueState: "observed", want: "pass"},
		"configured": {state: "configured", valueState: "not_observed", want: "pass"},
		"unsupported": {
			state: "unsupported", valueState: "unsupported", want: "pass",
		},
		"degraded": {state: "degraded", valueState: "observed", want: "degraded"},
		"unknown":  {state: "producing", valueState: "unknown", want: "degraded"},
		"invalid":  {state: "producing", valueState: "not_observed", want: "degraded"},
	} {
		t.Run("runtime_"+name, func(t *testing.T) {
			if got := runtimeSourceHealthState(test.state, test.valueState); got != test.want {
				t.Fatalf("state=%q value=%q got=%q want=%q", test.state, test.valueState, got, test.want)
			}
		})
	}
	if got := worstHealthState("pass", "degraded"); got != "degraded" {
		t.Fatalf("overall source degradation masked as %q", got)
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

func TestAPIResponseRejectsSecretLikeValues(t *testing.T) {
	api := &API{config: Config{ResponseMaxBytes: 1 << 20}}
	response := httptest.NewRecorder()
	api.write(
		response,
		http.StatusOK,
		map[string]string{"safe_id": "sk-abcdefghijklmnop"},
		map[string]any{"status": "complete"},
	)
	if response.Code != http.StatusInternalServerError ||
		response.Body.String() != "response_policy_violation\n" {
		t.Fatalf("secret-like API response was emitted: status=%d body=%q", response.Code, response.Body.String())
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
		CheckpointStateMaxBytes: 4 << 20, DatabaseSoftLimitBytes: 5 << 30,
		DatabaseBudgetWarning: .70, DatabaseBudgetDegraded: .85, DatabaseBudgetCritical: .95,
		StoragePreflightMinFreeBytes: 25 << 30,
		QueryTimeoutMS:               500, ResponseMaxBytes: 1 << 20, RetentionDays: 400,
		DiskBudgetFraction: .90, DiagnosticsMaxBytes: 1 << 20,
		PrivacyCanaryFixture:        filepath.Join(root, "privacy-canary.json"),
		RolloutWatchIntervalSeconds: 5,
	}
}
