//go:build postgres_integration

// Stage 5 unit tests: reconciliation + storage/operations/privacy checks +
// incident lifecycle, all requiring a real ephemeral PostgreSQL instance
// (mirroring postgres_integration_test.go's own testDSN/freshSchema
// pattern). These prove the four behaviors the Session 08 Stage 5 task
// brief names explicitly:
//
//  1. A real backup+restore checksum-verify round-trip using
//     internal/dataplatform's existing ephemeral Postgres test harness (via
//     RunBackupCycle, which itself calls dataplatform.CreateBackup/
//     VerifyBackupChecksum/CountRows/RestoreBackup -- never a second backup
//     mechanism).
//  2. A rollup-watermark-behind-budget correctly opens an incident (via
//     RollupFormulaDBIntegrityCheck + OpenOrUpdateIncident).
//  3. Repeated failing runs update (never duplicate) the same incident.
//  4. A recovery only closes the incident after a run with fresh positive
//     evidence from a later audit_run, never merely the absence of a new
//     failure.
package integrity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/adaptersdk/fakeadapter"
	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/localhttp"
	"kansoku.local/kansoku/internal/observability"
	"kansoku.local/kansoku/internal/privacy"
)

// dataplatformSchema mirrors freshSchema but also applies
// internal/dataplatform's own migrations (rather than this package's), for
// tests exercising RunBackupCycle/CreateBackup/RestoreBackup against a real
// data-platform schema. label disambiguates multiple independent
// data-platform schemas requested by the SAME test function (e.g. a source
// schema and a separate, isolated restore-target schema), since t.Name()
// alone is identical for both calls within one test.
//
// PostgreSQL silently truncates identifiers longer than 63 bytes (NAMEDATALEN
// - 1), so naively concatenating a long t.Name() with a "_dp_<label>" suffix
// can produce two DIFFERENT intended schema names that collide onto the SAME
// truncated identifier once t.Name() alone is already close to the limit --
// which would make the second dataplatformSchema call's CREATE SCHEMA
// silently DROP and recreate the first call's schema out from under its own
// already-connected pool. To make this collision-proof regardless of
// t.Name()'s length, the base name is bounded to leave deterministic room for
// a short content hash of (t.Name(), label) plus the label itself, so two
// different labels for the same (possibly long) test name always produce
// distinct, stable identifiers within the 63-byte limit.
func dataplatformSchemaName(testName, label string) string {
	const maxIdentifierBytes = 63
	sanitizedLabel := sanitizeSchemaName(label)
	suffix := "_dp_" + sanitizedLabel + "_" + shortHash(testName+"\x00"+label)
	base := "t_" + sanitizeSchemaName(testName)
	if room := maxIdentifierBytes - len(suffix); len(base) > room {
		if room < 0 {
			room = 0
		}
		base = base[:room]
	}
	return base + suffix
}

// shortHash returns an 8-hex-character (32-bit) content hash, short enough to
// always fit inside the 63-byte identifier budget alongside a base name and
// label, while still making two different (testName, label) pairs
// overwhelmingly unlikely to collide after truncation.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func dataplatformSchema(t *testing.T, dsn string, label string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	name := dataplatformSchemaName(t.Name(), label)

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect (admin): %v", err)
	}
	if _, err := admin.Exec(ctx, `DROP SCHEMA IF EXISTS `+pgIdent(name)+` CASCADE`); err != nil {
		admin.Close()
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+pgIdent(name)); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	config.MaxConns = 8
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
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+pgIdent(name)+` CASCADE`)
		admin.Close()
	})
	if err := dataplatform.Migrate(ctx, pool); err != nil {
		t.Fatalf("dataplatform migrate: %v", err)
	}
	return pool
}

// TestProductionSyntheticPipelineTraversesPostgresRollupAndCleansExactly
// proves the production Stage 5 constructor cannot stop at the Session 03
// FileStore. The probe crosses the real hook and all three OTLP HTTP routes,
// writes four event/evidence pairs into the Session 04 PostgreSQL model,
// performs only its own hourly/daily repairs, reads the budgeted rollup API,
// and removes exactly the rows in its reserved namespace before returning.
func TestProductionSyntheticPipelineTraversesPostgresRollupAndCleansExactly(t *testing.T) {
	dsn := testDSN(t)
	pool := dataplatformSchema(t, dsn, "synthetic")
	ctx := context.Background()

	store, err := observability.OpenFileStore(filepath.Join(t.TempDir(), "state.json"), 4<<20)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	ingestor, err := observability.NewIngestor(store, bytes.Repeat([]byte("k"), 32), privacy.DefaultLimits(), 4)
	if err != nil {
		t.Fatalf("NewIngestor: %v", err)
	}
	receiver, err := observability.NewOTLPReceiver(ingestor, 1<<20)
	if err != nil {
		t.Fatalf("NewOTLPReceiver: %v", err)
	}
	bearer := bytes.Repeat([]byte("b"), 32)
	guard, err := localhttp.NewGuard(
		[]string{"127.0.0.1", "::1", "localhost"},
		[]string{"http://127.0.0.1:3000", "http://[::1]:3000", "http://localhost:3000"},
		bearer, bytes.Repeat([]byte("c"), 32), 1<<20, 120, time.Minute,
	)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}

	check, err := NewProductionSyntheticPipelineCheck(guard, ingestor, receiver, store, pool, bearer)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := check.Evaluate(ctx, CheckInput{
		AuditRunID: "production-stage5-probe",
		Now:        time.Date(2026, 7, 22, 12, 34, 0, 0, time.UTC),
	}, CheckTarget{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != CheckStatusPass {
		t.Fatalf("outcome = %+v, want pass", outcome)
	}

	if got := len(ExcludeTestNamespace(store.Snapshot().Facts)); got != 0 {
		t.Fatalf("FileStore retained %d non-test facts in an otherwise empty harness", got)
	}
	for _, table := range []string{
		"events", "event_evidence", "rollup_repair_queue",
		"metric_rollups_hourly", "metric_rollups_daily", "rollup_status",
	} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+pgIdent(table)).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d synthetic rows after Evaluate returned", table, count)
		}
	}
}

func TestFaultMutationSyntheticPipelineFailurePersistsMeasuredIncidentAndCleans(t *testing.T) {
	dsn := testDSN(t)
	pool := dataplatformSchema(t, dsn, "synthetic_fail_first_insert")
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("integrity migrate: %v", err)
	}
	// Start the SLO clock before activating the trigger so the measurement
	// includes mutation installation as well as scheduler detection/persistence.
	injectedAt := time.Now().UTC()
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION reject_first_synthetic_event() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
		    RAISE EXCEPTION 'injected first InsertFact failure';
		END
		$$;
		CREATE TRIGGER reject_first_synthetic_event
		BEFORE INSERT ON events
		FOR EACH ROW EXECUTE FUNCTION reject_first_synthetic_event()
	`); err != nil {
		t.Fatalf("install event failure trigger: %v", err)
	}
	store, err := observability.OpenFileStore(filepath.Join(t.TempDir(), "state.json"), 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, err := observability.NewIngestor(store, bytes.Repeat([]byte("k"), 32), privacy.DefaultLimits(), 4)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := observability.NewOTLPReceiver(ingestor, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	bearer := bytes.Repeat([]byte("b"), 32)
	guard, err := localhttp.NewGuard(
		[]string{"127.0.0.1", "::1", "localhost"},
		[]string{"http://127.0.0.1:3000", "http://[::1]:3000", "http://localhost:3000"},
		bearer, bytes.Repeat([]byte("c"), 32), 1<<20, 120, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	check, err := NewProductionSyntheticPipelineCheck(guard, ingestor, receiver, store, pool, bearer)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewCheckRegistry()
	registry.Register(measuredOutcomeCheck{inner: check})
	scheduler := NewScheduler(pool, registry)
	if err := scheduler.ConfigureReportSigning("synthetic-mutation-key", bytes.Repeat([]byte{0x42}, 32)); err != nil {
		t.Fatal(err)
	}
	definition, _ := FaultDefinitionByID("synthetic_pipeline_probe_failure")
	failed, err := scheduler.startRun(
		ctx, RunModeReduced, TriggerStartup,
		[]StageID{Stage5SyntheticPipelineProbe, Stage11PersistReportAndIncidents},
		nil, nil, nil, injectedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Run.State != RunFailed {
		t.Fatalf("run state = %s, want failed", failed.Run.State)
	}
	open, err := ListOpenIncidents(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].FailureClass != definition.FailureClass {
		t.Fatalf("open incidents=%+v", open)
	}
	base, err := GetIncidentBase(ctx, pool, open[0].IncidentID)
	if err != nil {
		t.Fatal(err)
	}
	detection := base.OpenedAt.Sub(injectedAt)
	if detection < 0 || detection > definition.DetectionSLO {
		t.Fatalf("measured detection=%s SLO=%s", detection, definition.DetectionSLO)
	}
	for _, table := range []string{
		"events", "event_evidence", "rollup_repair_queue",
		"metric_rollups_hourly", "metric_rollups_daily", "rollup_status",
		"source_instances", "adapter_versions", "components", "turns",
		"sessions", "projects", "agent_surfaces", "agent_installations", "devices",
	} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d rows after first fact insert failure", table, count)
		}
	}
	if _, err := pool.Exec(ctx, `
		DROP TRIGGER reject_first_synthetic_event ON events;
		DROP FUNCTION reject_first_synthetic_event()
	`); err != nil {
		t.Fatalf("remove injected event failure: %v", err)
	}
	recovered, err := scheduler.startRun(
		ctx, RunModeReduced, TriggerStartup,
		[]StageID{Stage5SyntheticPipelineProbe, Stage11PersistReportAndIncidents},
		nil, nil, nil, time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Run.State != RunPassed {
		t.Fatalf("recovery state=%s", recovered.Run.State)
	}
	open, err = ListOpenIncidents(ctx, pool)
	if err != nil || len(open) != 0 {
		t.Fatalf("incidents after repaired handoff=%+v err=%v", open, err)
	}
}

// TestRunBackupCycleRealRoundTripVerifiesChecksumAndRestoreCounts proves
// requirement 1: a REAL backup+restore-checksum-verify round trip using
// internal/dataplatform's own ephemeral Postgres test harness. It creates a
// source data-platform schema, a separate empty restore-target
// data-platform schema, runs RunBackupCycle end to end (CreateBackup against
// the source, VerifyBackupChecksum, then RestoreBackup into the empty
// target and a row-count comparison), and asserts the recorded
// BackupStatus reflects a genuinely passing checksum and restore test --
// never a fabricated result.
func TestRunBackupCycleRealRoundTripVerifiesChecksumAndRestoreCounts(t *testing.T) {
	dsn := testDSN(t)
	sourcePool := dataplatformSchema(t, dsn, "source")
	restorePool := dataplatformSchema(t, dsn, "restore")
	statusPool := freshSchema(t, dsn)
	ctx := context.Background()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

	status, err := RunBackupCycle(ctx, sourcePool, statusPool, restorePool, "app@test", "formula@test", "sha256:test", []string{"codex@1.0.0"}, now)
	if err != nil {
		t.Fatalf("RunBackupCycle: %v", err)
	}
	if status.LastBackupAt.IsZero() {
		t.Fatalf("LastBackupAt is zero, want the backup timestamp")
	}
	if !status.LastBackupChecksumOK {
		t.Fatalf("LastBackupChecksumOK = false, want true for a freshly created backup")
	}
	if !status.LastRestoreTestRan {
		t.Fatalf("LastRestoreTestRan = false, want true: a restore test genuinely ran in this call")
	}
	if !status.LastRestoreTestPassed {
		t.Fatalf("LastRestoreTestPassed = false, want true: row counts must match after a clean restore into an empty schema")
	}

	// The persisted BackupStatusLookup must read back the exact same
	// evidence RunBackupCycle just recorded, never a stale or fabricated
	// value.
	lookup := NewBackupStatusLookup(statusPool)
	readBack, err := lookup(ctx)
	if err != nil {
		t.Fatalf("BackupStatusLookup: %v", err)
	}
	if !readBack.LastBackupChecksumOK || !readBack.LastRestoreTestRan || !readBack.LastRestoreTestPassed {
		t.Fatalf("read-back BackupStatus = %+v, want checksum_ok/restore_ran/restore_passed all true", readBack)
	}
}

// TestRunBackupCycleDetectsRestoreMismatch proves the converse of
// requirement 1: RunBackupCycle's restore-test genuinely compares row
// counts rather than always reporting success. It seeds one extra row in
// the restore target AFTER RestoreBackup would have produced a matching
// state, by instead pointing restorePool at a schema that already diverges
// (a non-empty target with different row counts than the source),
// confirming LastRestoreTestPassed is honestly false rather than
// fabricated true.
func TestRunBackupCycleDetectsRestoreMismatch(t *testing.T) {
	dsn := testDSN(t)
	sourcePool := dataplatformSchema(t, dsn, "source")
	restorePool := dataplatformSchema(t, dsn, "restore")
	statusPool := freshSchema(t, dsn)
	ctx := context.Background()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

	// Insert one formula_versions row directly into the restore target
	// before the restore runs, so RestoreBackup's own INSERTs collide with
	// pre-existing data and the restored row count diverges from the
	// source's backed-up row count (RestoreBackup does not truncate first;
	// a pre-populated target is exactly the divergence scenario the restore
	// count comparison exists to catch).
	if _, err := restorePool.Exec(ctx, `
		INSERT INTO formula_versions (formula_id, version, sql_template, unit, dimensions, allowed_completeness)
		VALUES ('pre_existing_probe', 1, 'SELECT 1', 'count', '{}'::jsonb, '[]'::jsonb)
	`); err != nil {
		t.Fatalf("seed divergent row: %v", err)
	}

	status, err := RunBackupCycle(ctx, sourcePool, statusPool, restorePool, "app@test", "formula@test", "sha256:test", nil, now)
	if err != nil {
		t.Fatalf("RunBackupCycle: %v", err)
	}
	if !status.LastRestoreTestRan {
		t.Fatalf("LastRestoreTestRan = false, want true (a restore test did genuinely run)")
	}
	if status.LastRestoreTestPassed {
		t.Fatalf("LastRestoreTestPassed = true, want false: row counts must diverge with a pre-populated restore target")
	}
}

// TestRollupWatermarkBehindBudgetOpensIncident proves requirement 2: when
// RollupFormulaDBIntegrityCheck observes a repair-queue depth beyond its
// budget, its failing outcome -- fed through OpenOrUpdateIncident exactly as
// a Stage 11 incident-raising pass would -- opens a NEW incident keyed by
// installation+source+capability+failure_class, with FailureClass
// rollup_stale.
func TestRollupWatermarkBehindBudgetOpensIncident(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)

	check := NewRollupFormulaDBIntegrityCheck(
		pool,
		func(context.Context, *pgxpool.Pool) (int64, error) { return RollupFreshnessBudget + 250, nil },
		NewExpectedFormulasLookupFunc(),
		NewMigrationsUpToDateCheckFunc(),
	)
	in := CheckInput{AuditRunID: "run_1", Mode: RunModeFull, Trigger: TriggerScheduledDaily, Now: now}
	target := CheckTarget{CapabilityID: storageOpsCapabilityID, InstallationID: storageOpsInstallationID}
	outcome, err := check.Evaluate(ctx, in, target)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != CheckStatusFail || outcome.Category != string(FailureClassRollupStale) {
		t.Fatalf("outcome = %+v, want fail/rollup_stale", outcome)
	}

	key := IncidentKey{InstallationID: target.InstallationID, SourceID: "kansoku-data-platform", CapabilityID: target.CapabilityID, FailureClass: FailureClassRollupStale}
	finding := IncidentFinding{
		Key: key, ObservedAt: now, AuditRunID: in.AuditRunID, CheckID: outcome.CheckID,
		IntervalFrom: dataplatform.BucketStart(now, dataplatform.GranularityHourly),
		IntervalTo:   dataplatform.BucketStart(now, dataplatform.GranularityHourly).Add(time.Hour),
	}
	detail, err := OpenOrUpdateIncident(ctx, pool, finding)
	if err != nil {
		t.Fatalf("OpenOrUpdateIncident: %v", err)
	}
	if detail.FailureClass != FailureClassRollupStale {
		t.Fatalf("detail.FailureClass = %s, want rollup_stale", detail.FailureClass)
	}
	if detail.FirstSeenAt.IsZero() {
		t.Fatalf("FirstSeenAt is zero, want the finding's ObservedAt")
	}

	base, err := GetIncidentBase(ctx, pool, detail.IncidentID)
	if err != nil {
		t.Fatalf("GetIncidentBase: %v", err)
	}
	if base.OccurrenceCount != 1 {
		t.Fatalf("OccurrenceCount = %d, want 1 for a newly opened incident", base.OccurrenceCount)
	}
	if base.ResolvedAt != nil {
		t.Fatalf("ResolvedAt = %v, want nil for a freshly opened incident", base.ResolvedAt)
	}

	open, err := ListOpenIncidents(ctx, pool)
	if err != nil {
		t.Fatalf("ListOpenIncidents: %v", err)
	}
	if len(open) != 1 || open[0].IncidentID != detail.IncidentID {
		t.Fatalf("ListOpenIncidents = %+v, want exactly the one newly opened incident", open)
	}
}

// TestRepeatedFailingRunsUpdateSameIncidentNeverDuplicate proves requirement
// 3: driving the SAME failure (rollup depth over budget) through three
// separate simulated audit_run IDs updates one incident row -- advancing
// OccurrenceCount and affected_interval.to, and never opening a second
// incident for the same installation+source+capability+failure_class key.
func TestRepeatedFailingRunsUpdateSameIncidentNeverDuplicate(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)

	key := IncidentKey{InstallationID: "inst_dp", SourceID: "kansoku-data-platform", CapabilityID: "storage_rollup_health", FailureClass: FailureClassRollupStale}

	var incidentID string
	for i, runID := range []string{"run_day1", "run_day2", "run_day3"} {
		observedAt := base.Add(time.Duration(i) * 24 * time.Hour)
		finding := IncidentFinding{
			Key: key, ObservedAt: observedAt, AuditRunID: runID, CheckID: RollupFormulaDBIntegrityCheckID,
			IntervalFrom: observedAt.Add(-time.Hour), IntervalTo: observedAt,
		}
		detail, err := OpenOrUpdateIncident(ctx, pool, finding)
		if err != nil {
			t.Fatalf("OpenOrUpdateIncident run %d: %v", i, err)
		}
		if i == 0 {
			incidentID = detail.IncidentID
		} else if detail.IncidentID != incidentID {
			t.Fatalf("run %d opened a DIFFERENT incident ID %s, want the same %s as run 0", i, detail.IncidentID, incidentID)
		}
	}

	base_, err := GetIncidentBase(ctx, pool, incidentID)
	if err != nil {
		t.Fatalf("GetIncidentBase: %v", err)
	}
	if base_.OccurrenceCount != 3 {
		t.Fatalf("OccurrenceCount = %d, want 3 after three repeated failing findings for the same key", base_.OccurrenceCount)
	}
	wantLastObserved := base.Add(2 * 24 * time.Hour).UTC()
	if !base_.LastObserved.Equal(wantLastObserved) {
		t.Fatalf("LastObserved = %v, want %v (advanced to the most recent finding)", base_.LastObserved, wantLastObserved)
	}

	detail, err := GetIncidentDetail(ctx, pool, incidentID)
	if err != nil {
		t.Fatalf("GetIncidentDetail: %v", err)
	}
	wantFrom := base.Add(-time.Hour).UTC() // affected_interval.from never moves once set (run 0's IntervalFrom)
	if !detail.AffectedInterval.From.Equal(wantFrom) {
		t.Fatalf("AffectedInterval.From = %v, want %v (must never move once set)", detail.AffectedInterval.From, wantFrom)
	}
	wantTo := base.Add(2 * 24 * time.Hour).UTC()
	if !detail.AffectedInterval.To.Equal(wantTo) {
		t.Fatalf("AffectedInterval.To = %v, want %v (advanced to the latest finding's IntervalTo)", detail.AffectedInterval.To, wantTo)
	}

	open, err := ListOpenIncidents(ctx, pool)
	if err != nil {
		t.Fatalf("ListOpenIncidents: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("ListOpenIncidents returned %d rows, want exactly 1 (no duplicate incident for repeated failures of the same key)", len(open))
	}
}

// TestRecoveryRequiresFreshPositiveEvidenceFromLaterRun proves requirement
// 4: RecordRecovery refuses to close an incident when the only "evidence"
// is the absence of a new failure within the SAME audit_run that raised it,
// and only closes the incident once a check for the identical incident_key
// genuinely runs again -- and passes -- in a LATER audit_run.
func TestRecoveryRequiresFreshPositiveEvidenceFromLaterRun(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	openedAt := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)

	key := IncidentKey{InstallationID: "inst_recover", SourceID: "kansoku-data-platform", CapabilityID: "storage_rollup_health", FailureClass: FailureClassRollupStale}
	finding := IncidentFinding{
		Key: key, ObservedAt: openedAt, AuditRunID: "run_open", CheckID: RollupFormulaDBIntegrityCheckID,
		IntervalFrom: openedAt.Add(-time.Hour), IntervalTo: openedAt,
	}
	detail, err := OpenOrUpdateIncident(ctx, pool, finding)
	if err != nil {
		t.Fatalf("OpenOrUpdateIncident: %v", err)
	}

	// Attempting recovery from the SAME audit_run that raised the incident
	// must be refused: absence of a further failure within one single pass
	// is not fresh positive evidence.
	recovered, err := RecordRecovery(ctx, pool, key, "run_open", CheckOutcome{Status: CheckStatusPass, ObservedAt: openedAt})
	if err == nil {
		t.Fatalf("RecordRecovery from the SAME run unexpectedly succeeded (recovered=%v), want a refusal error", recovered)
	}
	if recovered {
		t.Fatalf("recovered = true from the same run, want false")
	}
	stillOpen, err := GetIncidentBase(ctx, pool, detail.IncidentID)
	if err != nil {
		t.Fatalf("GetIncidentBase after refused recovery: %v", err)
	}
	if stillOpen.ResolvedAt != nil {
		t.Fatalf("ResolvedAt = %v after a refused same-run recovery attempt, want nil (still open)", stillOpen.ResolvedAt)
	}

	// A later run that never even mentions this incident key (mere silence)
	// must not close it either -- RecordRecovery is only ever invoked by a
	// caller after an ACTUAL passing check for this exact key genuinely ran;
	// this is enforced by the caller discipline (RecordRecovery trusts the
	// caller), so this test simulates that discipline directly: silence
	// alone (never calling RecordRecovery at all) leaves the incident open.
	untouched, err := GetIncidentBase(ctx, pool, detail.IncidentID)
	if err != nil {
		t.Fatalf("GetIncidentBase (silence check): %v", err)
	}
	if untouched.ResolvedAt != nil {
		t.Fatalf("incident was closed by mere silence with no RecordRecovery call, want still open")
	}

	// Now a LATER audit_run's check genuinely passes for this same key:
	// recovery must succeed and close the incident without deleting its
	// history (OccurrenceCount/FirstSeenAt/affected_interval preserved).
	laterRunAt := openedAt.Add(24 * time.Hour)
	recovered, err = RecordRecovery(ctx, pool, key, "run_later_pass", CheckOutcome{Status: CheckStatusPass, ObservedAt: laterRunAt})
	if err != nil {
		t.Fatalf("RecordRecovery from a later run: %v", err)
	}
	if !recovered {
		t.Fatalf("recovered = false from a genuinely later passing run, want true")
	}

	closed, err := GetIncidentBase(ctx, pool, detail.IncidentID)
	if err != nil {
		t.Fatalf("GetIncidentBase after recovery: %v", err)
	}
	if closed.ResolvedAt == nil {
		t.Fatalf("ResolvedAt is nil after a genuine later-run recovery, want set")
	}
	if !closed.ResolvedAt.Equal(laterRunAt.UTC()) {
		t.Fatalf("ResolvedAt = %v, want %v", closed.ResolvedAt, laterRunAt.UTC())
	}
	if closed.OccurrenceCount != 1 {
		t.Fatalf("OccurrenceCount = %d after recovery, want 1 (history preserved, not deleted or reset)", closed.OccurrenceCount)
	}

	closedDetail, err := GetIncidentDetail(ctx, pool, detail.IncidentID)
	if err != nil {
		t.Fatalf("GetIncidentDetail after recovery: %v", err)
	}
	if closedDetail.FirstSeenAt.IsZero() || !closedDetail.FirstSeenAt.Equal(openedAt.UTC()) {
		t.Fatalf("FirstSeenAt = %v after recovery, want unchanged %v (history preserved)", closedDetail.FirstSeenAt, openedAt.UTC())
	}

	open, err := ListOpenIncidents(ctx, pool)
	if err != nil {
		t.Fatalf("ListOpenIncidents: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("ListOpenIncidents = %+v, want empty after the incident closed", open)
	}

	// A second recovery attempt for an already-resolved incident is a safe
	// no-op, never an error and never a second close.
	recoveredAgain, err := RecordRecovery(ctx, pool, key, "run_even_later", CheckOutcome{Status: CheckStatusPass, ObservedAt: laterRunAt.Add(time.Hour)})
	if err != nil {
		t.Fatalf("RecordRecovery on an already-resolved incident: %v", err)
	}
	if recoveredAgain {
		t.Fatalf("recoveredAgain = true, want false (already resolved, nothing to do)")
	}
}

// TestCrossSourceReconciliationRegressionOpensReconciliationMismatchIncident
// proves the reconciliation-check half of Stage 5 end to end: a registered
// adapter's installation reconciliation window that regresses against a
// previous adapter version's last-known ratio produces a failing
// CrossSourceReconciliationCheck outcome, which -- fed through
// OpenOrUpdateIncident exactly as stage_11 would -- opens a
// reconciliation_mismatch incident distinct from the storage-ops incident
// key used by the other tests in this file (proving the four-component
// key genuinely discriminates by capability/failure_class, not just by
// installation).
func TestCrossSourceReconciliationRegressionOpensReconciliationMismatchIncident(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)

	registry := adaptersdk.NewRegistry()
	if err := registry.Register(fakeadapter.New()); err != nil {
		t.Fatalf("register fakeadapter: %v", err)
	}
	installationID := "inst_loomwright_1"
	installations := func(ctx context.Context, adapterID string) ([]adaptersdk.Installation, error) {
		return []adaptersdk.Installation{{InstallationID: installationID, AdapterID: adapterID, SurfaceID: "cli", StateRoot: "/tmp/loomwright"}}, nil
	}
	window := func(ctx context.Context, adapterID, instID string) (ReconciliationWindowSummary, error) {
		return ReconciliationWindowSummary{
			InstallationID: instID, SourceID: fakeadapter.AdapterID, MinimumRatio: 1.0,
			Sessions: []SessionReconciliationSummary{
				{SessionID: "sess_1", CompatibilityVersion: "v1", ToleranceKnown: true, TotalLanes: 4, AgreeingLanes: 3, MismatchLanes: 1, AdapterVersion: "2.0.0"},
			},
		}, nil
	}
	previousRatio := func(ctx context.Context, instID, sourceID, currentAdapterVersion string) (float64, ReconciliationMismatchClass, bool, error) {
		return 1.0, MismatchClassNone, true, nil
	}
	check := NewCrossSourceReconciliationCheck(registry, installations, window, previousRatio)

	in := CheckInput{AuditRunID: "run_recon_1", Mode: RunModeFull, Trigger: TriggerScheduledDaily, Now: now}
	targets, err := check.Targets(ctx, in)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 1 || targets[0].InstallationID != installationID {
		t.Fatalf("Targets = %+v, want exactly one target for %s", targets, installationID)
	}

	outcome, err := check.Evaluate(ctx, in, targets[0])
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != CheckStatusFail || outcome.Category != string(FailureClassReconciliationMismatch) {
		t.Fatalf("outcome = %+v, want fail/reconciliation_mismatch (count_mismatch class + regression vs previous version)", outcome)
	}

	key := IncidentKey{InstallationID: installationID, SourceID: fakeadapter.AdapterID, CapabilityID: string(adaptersdk.CapabilityActivitySessions), FailureClass: FailureClassReconciliationMismatch}
	recFinding := IncidentFinding{
		Key: key, ObservedAt: now, AuditRunID: in.AuditRunID, CheckID: outcome.CheckID,
		IntervalFrom: now.Add(-time.Hour), IntervalTo: now,
	}
	detail, err := OpenOrUpdateIncident(ctx, pool, recFinding)
	if err != nil {
		t.Fatalf("OpenOrUpdateIncident: %v", err)
	}
	if detail.CapabilityID != string(adaptersdk.CapabilityActivitySessions) || detail.FailureClass != FailureClassReconciliationMismatch {
		t.Fatalf("incident detail = %+v, want capability=%s failure_class=reconciliation_mismatch", detail, adaptersdk.CapabilityActivitySessions)
	}

	open, err := ListOpenIncidents(ctx, pool)
	if err != nil {
		t.Fatalf("ListOpenIncidents: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("ListOpenIncidents = %+v, want exactly the one reconciliation incident", open)
	}
}
