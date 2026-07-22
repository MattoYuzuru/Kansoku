//go:build postgres_integration

// Package dataplatform's Postgres-backed tests require a real ephemeral
// PostgreSQL instance. They are excluded from the default `go test ./...`
// sweep (and therefore from scripts/run_go_tests.py's --network none
// container) by the postgres_integration build tag; run them via
// `python3 scripts/validate_data_platform.py`, which starts the pinned
// deploy/compose.security-baseline.yaml Postgres image in an isolated
// Docker network, points KANSOKU_TEST_POSTGRES_DSN at it, runs this suite,
// and tears the container down deterministically.
package dataplatform

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testDSN returns the ephemeral Postgres DSN provided by
// scripts/validate_data_platform.py, or skips the test if it is absent (e.g.
// when running the default `go test ./...` sweep without the
// postgres_integration tag's harness).
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("KANSOKU_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("KANSOKU_TEST_POSTGRES_DSN not set; run via scripts/validate_data_platform.py")
	}
	return dsn
}

// freshSchema creates a uniquely named schema for full test isolation
// between test functions sharing one Postgres instance, then returns a pool
// dedicated to that test whose connections are bound to the schema via a
// `search_path` startup parameter set at connection-creation time (not via a
// post-hoc pool.Config() mutation, since pgxpool.Pool.Config() returns a
// defensive copy that the running pool never observes). Every connection
// pgxpool ever opens for this pool therefore already has the right
// search_path, with no race between connection reuse and schema setup.
func freshSchema(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("t_%s", sanitizeSchemaName(t.Name()))

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect (admin): %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgIdent(name))); err != nil {
		admin.Close()
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, pgIdent(name))); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	config.MaxConns = 8
	// search_path is a Postgres startup parameter, so every connection the
	// pool opens (now or later, after scaling up or reconnecting) is bound
	// to this schema from the moment it is established.
	config.ConnConfig.RuntimeParams["search_path"] = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatalf("connect (scoped): %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		admin.Close()
		t.Fatalf("ping (scoped): %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgIdent(name)))
		admin.Close()
	})
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

// sanitizeSchemaName lowercases the result so the schema name is stable
// whether it is used quoted (DDL via pgIdent) or unquoted (the search_path
// startup parameter, which Postgres folds to lowercase per normal
// identifier rules): the same literal string must resolve to the same
// schema either way.
func sanitizeSchemaName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r-'A'+'a')
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// isolatedDatabase creates a brand-new physical PostgreSQL database on the
// same server (not merely a schema), matching
// contracts/data-platform/retention.yaml `backup.restore_test.target ==
// isolated_temporary_database`. It returns a pool connected to that new
// database plus a cleanup that drops it. Callers still call Migrate/freshSchema-style
// setup on the returned pool as needed.
func isolatedDatabase(t *testing.T, dsn string) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect (admin for isolated db): %v", err)
	}
	name := fmt.Sprintf("restore_%s", sanitizeSchemaName(t.Name()))
	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, pgIdent(name))); err != nil {
		admin.Close()
		t.Fatalf("drop isolated database: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, pgIdent(name))); err != nil {
		admin.Close()
		t.Fatalf("create isolated database: %v", err)
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatalf("connect (isolated db): %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		admin.Close()
		t.Fatalf("ping (isolated db): %v", err)
	}
	cleanup := func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, pgIdent(name)))
		admin.Close()
	}
	return pool, cleanup
}

func loadFixture(t *testing.T) map[string]any {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	path := filepath.Join(root, "tests", "fixtures", "session-04", "replay-scenario.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return value
}

func testDimensionRefs(sourceInstanceID string) DimensionRefs {
	return DimensionRefs{
		DeviceID: "dev_fixture", AgentInstallationID: "ain_fixture", AgentID: "fixture-agent",
		SurfaceID: "cli", ProjectID: "proj_fixture", SessionID: "ses_fixture", TurnID: "turn_fixture",
		ComponentID: "inventory/tool-safe", AdapterVersionID: "fixture-agent/1.0.0", AdapterID: "fixture-agent",
		AdapterVersion: "1.0.0", SourceInstanceID: sourceInstanceID, SourceKind: "hook_http",
	}
}

func makeFact(index int, observedAt time.Time, durationMS int64, sourceInstanceID string) (FactRow, EvidenceRow) {
	eventID := fmt.Sprintf("evt_%04d", index)
	success := true
	fact := FactRow{
		EventID: eventID, FactKey: "fact_" + eventID, EventType: "component.executed",
		ObservedAt: observedAt, IngestedAt: observedAt.Add(time.Millisecond), TimestampQuality: "source_rfc3339",
		SourceInstanceID: sourceInstanceID, SourceNativeEventID: eventID, Sequence: int64(index),
		AgentInstallationID: "ain_fixture", SurfaceID: "cli", ProjectID: "proj_fixture", SessionID: "ses_fixture",
		TurnID: "turn_fixture", ComponentID: "inventory/tool-safe", DurationMS: &durationMS, Success: &success,
		ValueState: "observed", Outcome: "succeeded", CorrelationStatus: "exact",
	}
	evidence := EvidenceRow{
		EvidenceID: "evd_" + eventID, EventID: eventID, ObservedAt: observedAt, SourceInstanceID: sourceInstanceID,
		Tier: "native", Confidence: 1.0, Completeness: "complete", FirstSeenAt: observedAt, LastSeenAt: observedAt,
		SanitizerVersion: "fixture-sanitizer/1", PrivacyContractID: "fixturecontractsha256",
		AssertEventType: "component.executed", AssertOutcome: "succeeded", AssertValueState: "observed",
	}
	return fact, evidence
}

// TestReplayReconcilesExactlyWithinBudget is the Session 04 exit-gate proof:
// a deterministic synthetic dataset replays to exact aggregates (event
// counts, percentiles) with zero duplicate-fact inflation, a late event
// triggers a full bucket recompute (never an incremental percentile
// average), and the budgeted range query returns in time.
func TestReplayReconcilesExactlyWithinBudget(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()

	fixture := loadFixture(t)
	generation := fixture["generation"].(map[string]any)
	buckets := int(generation["buckets"].(float64))
	eventsPerBucket := int(generation["events_per_bucket"].(float64))
	lateEvent := generation["late_event"].(map[string]any)
	lateBucketIndex := int(lateEvent["target_bucket_index"].(float64))
	lateDurationMS := int64(lateEvent["duration_ms"].(float64))

	sourceInstanceID := "src_fixture_hook"
	if err := EnsureDimensions(ctx, pool, testDimensionRefs(sourceInstanceID)); err != nil {
		t.Fatalf("ensure dimensions: %v", err)
	}

	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	index := 0
	var allDurations []int64
	perBucketDurations := make([][]int64, buckets)
	for bucketIdx := 0; bucketIdx < buckets; bucketIdx++ {
		for eventIdx := 0; eventIdx < eventsPerBucket; eventIdx++ {
			observedAt := base.Add(time.Duration(bucketIdx) * time.Hour).Add(time.Duration(eventIdx) * time.Second)
			duration := int64(100 + bucketIdx*10 + eventIdx*3)
			fact, evidence := makeFact(index, observedAt, duration, sourceInstanceID)
			index++
			result, err := InsertFact(ctx, pool, fact, evidence)
			if err != nil {
				t.Fatalf("insert fact %d: %v", index, err)
			}
			if !result.FactInserted {
				t.Fatalf("expected new fact to be inserted at index %d", index)
			}
			allDurations = append(allDurations, duration)
			perBucketDurations[bucketIdx] = append(perBucketDurations[bucketIdx], duration)
		}
	}
	totalBeforeLate := index
	if want := int(fixture["expected"].(map[string]any)["total_events_before_late"].(float64)); totalBeforeLate != want {
		t.Fatalf("total events before late = %d, want %d", totalBeforeLate, want)
	}

	// Duplicate replay: resend the very first fact/evidence pair three times.
	firstFact, firstEvidence := makeFact(0, base, 100, sourceInstanceID)
	replays := int(generation["duplicate_replay_count"].(float64))
	var lastDuplicate bool
	for i := 0; i < replays; i++ {
		result, err := InsertFact(ctx, pool, firstFact, firstEvidence)
		if err != nil {
			t.Fatalf("replay insert: %v", err)
		}
		if result.FactInserted {
			t.Fatalf("replay must never insert a second fact")
		}
		lastDuplicate = result.DuplicateReplay
	}
	if !lastDuplicate {
		t.Fatalf("expected replay to be detected as duplicate")
	}
	var replayCount int64
	if err := pool.QueryRow(ctx, `SELECT replay_count FROM event_evidence WHERE evidence_id = $1`, firstEvidence.EvidenceID).Scan(&replayCount); err != nil {
		t.Fatalf("read replay_count: %v", err)
	}
	if replayCount != int64(replays) {
		t.Fatalf("replay_count = %d, want %d", replayCount, replays)
	}
	var factCount int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE fact_key = $1`, firstFact.FactKey).Scan(&factCount); err != nil {
		t.Fatalf("count facts: %v", err)
	}
	if factCount != 1 {
		t.Fatalf("duplicate replay caused fact inflation: %d rows", factCount)
	}

	// Compute rollups for the first time (before the late event arrives).
	if _, err := RunRepairWorker(ctx, pool); err != nil {
		t.Fatalf("initial repair worker: %v", err)
	}

	lateBucketStart := BucketStart(base.Add(time.Duration(lateBucketIndex)*time.Hour), GranularityHourly)
	scope := dimensionScope(FactRow{AgentInstallationID: "ain_fixture", SurfaceID: "cli", ComponentID: "inventory/tool-safe", EventType: "component.executed"})
	var firstComputedP95 *float64
	if err := pool.QueryRow(ctx, `SELECT value_p95 FROM metric_rollups_hourly WHERE metric_family = $1 AND bucket_start = $2 AND dimension_scope = $3`, MetricFamilyLatencyMS, lateBucketStart, scope).Scan(&firstComputedP95); err != nil {
		t.Fatalf("read first computed p95: %v", err)
	}
	if firstComputedP95 == nil {
		t.Fatalf("expected a computed p95 before the late event")
	}
	wantFirstP95 := exactPercentile(perBucketDurations[lateBucketIndex], 0.95)
	if math.Abs(*firstComputedP95-wantFirstP95) > 0.001 {
		t.Fatalf("first p95 = %v, want %v", *firstComputedP95, wantFirstP95)
	}

	// Late event: arrives in the same bucket after the first rollup exists.
	lateObservedAt := base.Add(time.Duration(lateBucketIndex) * time.Hour).Add(500 * time.Millisecond)
	lateFact, lateEvidence := makeFact(100000, lateObservedAt, lateDurationMS, sourceInstanceID)
	result, err := InsertFact(ctx, pool, lateFact, lateEvidence)
	if err != nil {
		t.Fatalf("insert late fact: %v", err)
	}
	if !result.FactInserted {
		t.Fatalf("late fact must be inserted as a new fact")
	}

	pendingBefore, err := RepairQueueDepth(ctx, pool)
	if err != nil {
		t.Fatalf("repair queue depth: %v", err)
	}
	if pendingBefore == 0 {
		t.Fatalf("expected the late event to enqueue a repair")
	}

	recomputed, err := RunRepairWorker(ctx, pool)
	if err != nil {
		t.Fatalf("repair worker after late event: %v", err)
	}
	if recomputed == 0 {
		t.Fatalf("expected at least one bucket recompute after the late event")
	}

	pendingAfter, err := RepairQueueDepth(ctx, pool)
	if err != nil {
		t.Fatalf("repair queue depth after: %v", err)
	}
	if pendingAfter != 0 {
		t.Fatalf("repair queue should drain to zero, got %d", pendingAfter)
	}

	// The bucket must now reflect an exact recompute over all facts
	// including the late one -- never an average of the old and new
	// percentiles.
	withLate := append(append([]int64{}, perBucketDurations[lateBucketIndex]...), lateDurationMS)
	wantRecomputedP95 := exactPercentile(withLate, 0.95)
	var recomputedP95 *float64
	var eventCount int64
	if err := pool.QueryRow(ctx, `SELECT value_p95, event_count FROM metric_rollups_hourly WHERE metric_family = $1 AND bucket_start = $2 AND dimension_scope = $3`, MetricFamilyLatencyMS, lateBucketStart, scope).Scan(&recomputedP95, &eventCount); err != nil {
		t.Fatalf("read recomputed p95: %v", err)
	}
	if recomputedP95 == nil {
		t.Fatalf("expected recomputed p95")
	}
	if math.Abs(*recomputedP95-wantRecomputedP95) > 0.001 {
		t.Fatalf("recomputed p95 = %v, want %v (exact recompute)", *recomputedP95, wantRecomputedP95)
	}
	naiveAverage := (*firstComputedP95 + float64(lateDurationMS)) / 2
	if math.Abs(*recomputedP95-naiveAverage) < 0.001 && math.Abs(wantRecomputedP95-naiveAverage) > 0.5 {
		t.Fatalf("recomputed p95 matches a naive average of precomputed percentiles, which the contract forbids")
	}
	if int(eventCount) != len(withLate) {
		t.Fatalf("event_count = %d, want %d", eventCount, len(withLate))
	}

	// Budgeted range query must return the exact aggregates within budget.
	started := time.Now()
	response, err := RollupRange(ctx, pool, "hourly_rollup_range_30d", MetricFamilyLatencyMS, GranularityHourly, scope, base, base.AddDate(0, 0, 30))
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("RollupRange: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("hourly_rollup_range_30d exceeded its 50ms budget: %v", elapsed)
	}
	if len(response.Data) != buckets {
		t.Fatalf("expected %d rollup points, got %d", buckets, len(response.Data))
	}
	var totalEventCount int64
	for _, point := range response.Data {
		totalEventCount += point.EventCount
	}
	wantTotal := int64(totalBeforeLate + 1) // +1 late event; duplicates never inflate.
	if totalEventCount != wantTotal {
		t.Fatalf("total rollup event_count = %d, want %d", totalEventCount, wantTotal)
	}
	if response.Completeness.Status != "complete" {
		t.Fatalf("expected complete completeness, got %s", response.Completeness.Status)
	}
	if response.Freshness.LateEventsPending != 0 {
		t.Fatalf("expected zero pending late events after repair, got %d", response.Freshness.LateEventsPending)
	}
}

// TestRetentionDropsOnlyExpiredPartitionsAndBoundsData is the Session 04
// exit-gate proof that "retention is bounded": events older than the
// configured horizon are removed by dropping their whole monthly partition
// (contracts/data-platform/retention.yaml `event_expiration.mechanism`), a
// recent partition inside the horizon is untouched, and the dropped
// partition's rows are actually gone rather than merely hidden.
func TestRetentionDropsOnlyExpiredPartitionsAndBoundsData(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	sourceInstanceID := "src_fixture_retention"
	if err := EnsureDimensions(ctx, pool, testDimensionRefs(sourceInstanceID)); err != nil {
		t.Fatalf("ensure dimensions: %v", err)
	}

	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	horizonDays := 30
	oldObservedAt := now.AddDate(0, 0, -(horizonDays + 60)) // safely before the horizon, different month
	recentObservedAt := now.AddDate(0, 0, -1)                // inside the horizon

	oldFact, oldEvidence := makeFact(1, oldObservedAt, 111, sourceInstanceID)
	if _, err := InsertFact(ctx, pool, oldFact, oldEvidence); err != nil {
		t.Fatalf("insert old fact: %v", err)
	}
	recentFact, recentEvidence := makeFact(2, recentObservedAt, 222, sourceInstanceID)
	if _, err := InsertFact(ctx, pool, recentFact, recentEvidence); err != nil {
		t.Fatalf("insert recent fact: %v", err)
	}

	oldPartition := partitionName("events", oldObservedAt)
	recentPartition := partitionName("events", recentObservedAt)
	if oldPartition == recentPartition {
		t.Fatalf("test fixture must span two different monthly partitions, got %s twice", oldPartition)
	}

	dropped, err := ApplyRetention(ctx, pool, now, horizonDays)
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	droppedEvents := dropped["events"]
	found := false
	for _, name := range droppedEvents {
		if name == oldPartition {
			found = true
		}
		if name == recentPartition {
			t.Fatalf("retention dropped the in-horizon partition %s", recentPartition)
		}
	}
	if !found {
		t.Fatalf("expected retention to drop expired partition %s, dropped=%v", oldPartition, droppedEvents)
	}

	// The dropped partition's data must actually be gone (partition-drop
	// bounds storage), not merely excluded by a filter.
	var oldCount int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE fact_key = $1`, oldFact.FactKey).Scan(&oldCount); err != nil {
		t.Fatalf("count old facts after retention: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("expired fact still present after retention: %d rows", oldCount)
	}
	var recentCount int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE fact_key = $1`, recentFact.FactKey).Scan(&recentCount); err != nil {
		t.Fatalf("count recent facts after retention: %v", err)
	}
	if recentCount != 1 {
		t.Fatalf("in-horizon fact was lost by retention: %d rows, want 1", recentCount)
	}

	// Retention must be idempotent: a second run drops nothing new.
	droppedAgain, err := ApplyRetention(ctx, pool, now, horizonDays)
	if err != nil {
		t.Fatalf("ApplyRetention (second run): %v", err)
	}
	if len(droppedAgain["events"]) != 0 {
		t.Fatalf("second retention run dropped partitions again: %v", droppedAgain["events"])
	}
}

// TestBudgetedQueriesAvoidSequentialScanOfPartitionedFacts is the Session 04
// exit-gate plan-review proof from contracts/data-platform/query-contract.yaml
// `budgets.plan_review`: every budgeted query's plan must not sequentially
// scan a partitioned fact table (partition pruning plus the lookup indexes
// from migration 0002 must be used instead).
func TestBudgetedQueriesAvoidSequentialScanOfPartitionedFacts(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	sourceInstanceID := "src_fixture_planreview"
	if err := EnsureDimensions(ctx, pool, testDimensionRefs(sourceInstanceID)); err != nil {
		t.Fatalf("ensure dimensions: %v", err)
	}
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		fact, evidence := makeFact(i, base.Add(time.Duration(i)*time.Minute), int64(100+i), sourceInstanceID)
		if _, err := InsertFact(ctx, pool, fact, evidence); err != nil {
			t.Fatalf("insert fact %d: %v", i, err)
		}
	}
	if _, err := RunRepairWorker(ctx, pool); err != nil {
		t.Fatalf("repair worker: %v", err)
	}

	scope := dimensionScope(FactRow{AgentInstallationID: "ain_fixture", SurfaceID: "cli", ComponentID: "inventory/tool-safe", EventType: "component.executed"})
	plan, err := ExplainNoSeqScan(ctx, pool, `
		SELECT bucket_start, event_count FROM metric_rollups_hourly
		WHERE metric_family = $1 AND dimension_scope = $2 AND bucket_start >= $3 AND bucket_start < $4
	`, MetricFamilyLatencyMS, scope, base, base.AddDate(0, 0, 30))
	if err != nil {
		t.Fatalf("explain hourly rollup range: %v", err)
	}
	if strings.Contains(plan, "Seq Scan on metric_rollups_hourly") {
		t.Fatalf("hourly_rollup_range_30d plan sequentially scans the rollup table:\n%s", plan)
	}

	plan, err = ExplainNoSeqScan(ctx, pool, `
		SELECT event_id FROM events WHERE session_id = $1 AND observed_at >= $2 AND observed_at < $3
	`, "ses_fixture", base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("explain session drilldown: %v", err)
	}
	if strings.Contains(plan, "Seq Scan on events") {
		t.Fatalf("session_drilldown plan sequentially scans the partitioned events table:\n%s", plan)
	}
}

// TestBackupRestoreReproducesFormulaResultsWithLineage is the Session 04
// exit-gate proof that "restore reproduces formula results with lineage": a
// logical backup of a populated database is restored into an isolated
// temporary database (contracts/data-platform/retention.yaml
// `backup.restore_test`), row counts match, the formula_versions lineage
// row is present with its exact SQL template, and a sample formula result
// (the same p95 rollup) matches the source exactly. The temporary restore
// target is dropped after verification.
func TestBackupRestoreReproducesFormulaResultsWithLineage(t *testing.T) {
	dsn := testDSN(t)
	source := freshSchema(t, dsn)
	ctx := context.Background()
	sourceInstanceID := "src_fixture_backup"
	if err := EnsureDimensions(ctx, source, testDimensionRefs(sourceInstanceID)); err != nil {
		t.Fatalf("ensure dimensions: %v", err)
	}
	if err := SeedFormulaVersion(ctx, source, MetricFamilyLatencyMS, 1,
		"percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms)", "milliseconds",
		[]string{"agent_installation_id", "surface_id", "component_id", "event_type"},
		nil, nil, nil, 1, []string{"complete", "partial"}, nil); err != nil {
		t.Fatalf("seed formula version: %v", err)
	}

	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		fact, evidence := makeFact(i, base.Add(time.Duration(i)*time.Minute), int64(100+i*7), sourceInstanceID)
		if _, err := InsertFact(ctx, source, fact, evidence); err != nil {
			t.Fatalf("insert fact %d: %v", i, err)
		}
	}
	if _, err := RunRepairWorker(ctx, source); err != nil {
		t.Fatalf("repair worker: %v", err)
	}

	backup, err := CreateBackup(ctx, source, "kansoku/test", FormulaVersionLatencyMS1, "fixture-privacy-sha256", []string{"fixture-agent/1.0.0"}, base)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if err := VerifyBackupChecksum(backup); err != nil {
		t.Fatalf("VerifyBackupChecksum (source): %v", err)
	}
	sourceCounts := CountRows(backup)
	if sourceCounts["events"] != 12 {
		t.Fatalf("source backup events count = %d, want 12", sourceCounts["events"])
	}
	if sourceCounts["formula_versions"] != 1 {
		t.Fatalf("source backup formula_versions count = %d, want 1", sourceCounts["formula_versions"])
	}

	var sourceP95 *float64
	scope := dimensionScope(FactRow{AgentInstallationID: "ain_fixture", SurfaceID: "cli", ComponentID: "inventory/tool-safe", EventType: "component.executed"})
	bucketStart := BucketStart(base, GranularityHourly)
	if err := source.QueryRow(ctx, `SELECT value_p95 FROM metric_rollups_hourly WHERE metric_family = $1 AND bucket_start = $2 AND dimension_scope = $3`, MetricFamilyLatencyMS, bucketStart, scope).Scan(&sourceP95); err != nil {
		t.Fatalf("read source p95: %v", err)
	}
	if sourceP95 == nil {
		t.Fatalf("expected a computed source p95")
	}

	// Restore into an isolated temporary database, matching
	// backup.restore_test.target == isolated_temporary_database.
	restore, cleanupRestore := isolatedDatabase(t, dsn)
	defer cleanupRestore()
	if err := Migrate(ctx, restore); err != nil {
		t.Fatalf("migrate restore target: %v", err)
	}
	if err := RestoreBackup(ctx, restore, backup); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}

	// verifies: row_counts
	var restoredEventCount int64
	if err := restore.QueryRow(ctx, `SELECT count(*) FROM events`).Scan(&restoredEventCount); err != nil {
		t.Fatalf("count restored events: %v", err)
	}
	if restoredEventCount != int64(sourceCounts["events"]) {
		t.Fatalf("restored events count = %d, want %d", restoredEventCount, sourceCounts["events"])
	}

	// verifies: constraints (duration_ms >= 0 CHECK must still be enforced
	// after restore, proving the restored schema is not a bare data dump).
	if _, err := restore.Exec(ctx, `INSERT INTO events (event_id, fact_key, event_type, observed_at, timestamp_quality, source_instance_id, source_native_event_id, sequence, duration_ms, value_state, outcome, correlation_status) VALUES ('evt_bad', 'fact_bad', 'component.executed', now(), 'source_rfc3339', $1, 'evt_bad', 0, -1, 'observed', 'succeeded', 'exact')`, sourceInstanceID); err == nil {
		t.Fatalf("restored schema accepted a negative duration_ms, constraints were not preserved")
	}

	// verifies: formula_version_lineage
	var restoredSQLTemplate string
	if err := restore.QueryRow(ctx, `SELECT sql_template FROM formula_versions WHERE formula_id = $1 AND version = 1`, MetricFamilyLatencyMS).Scan(&restoredSQLTemplate); err != nil {
		t.Fatalf("read restored formula lineage: %v", err)
	}
	if restoredSQLTemplate != "percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms)" {
		t.Fatalf("restored formula_versions lineage does not match source: %q", restoredSQLTemplate)
	}

	// verifies: sample_formula_results -- recomputing the same rollup
	// against the restored facts must reproduce the exact source p95.
	if _, err := RunRepairWorker(ctx, restore); err != nil {
		t.Fatalf("repair worker (restore): %v", err)
	}
	var restoredP95 *float64
	if err := restore.QueryRow(ctx, `SELECT value_p95 FROM metric_rollups_hourly WHERE metric_family = $1 AND bucket_start = $2 AND dimension_scope = $3`, MetricFamilyLatencyMS, bucketStart, scope).Scan(&restoredP95); err != nil {
		t.Fatalf("read restored p95: %v", err)
	}
	if restoredP95 == nil {
		t.Fatalf("expected a computed restored p95")
	}
	if math.Abs(*restoredP95-*sourceP95) > 0.001 {
		t.Fatalf("restored p95 = %v, want exact source p95 %v", *restoredP95, *sourceP95)
	}
}

// exactPercentile mirrors PostgreSQL's percentile_cont linear interpolation
// so the Go-side expectation is computed the same way, never by averaging
// other percentiles.
func exactPercentile(values []int64, fraction float64) float64 {
	sorted := append([]int64{}, values...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1] > sorted[j]; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return float64(sorted[0])
	}
	rank := fraction * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return float64(sorted[lower])
	}
	weight := rank - float64(lower)
	return float64(sorted[lower])*(1-weight) + float64(sorted[upper])*weight
}
