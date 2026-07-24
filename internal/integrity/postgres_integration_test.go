//go:build postgres_integration

// Package integrity's Postgres-backed tests require a real ephemeral
// PostgreSQL instance, exactly like internal/dataplatform's own
// postgres_integration_test.go. They are excluded from the default
// `go test ./...` sweep by the postgres_integration build tag; run them via
// a validator harness that starts the pinned Postgres image in an isolated
// Docker network, points KANSOKU_TEST_POSTGRES_DSN at it, runs this suite,
// and tears the container down deterministically (mirroring
// scripts/validate_data_platform.py's pattern for internal/dataplatform).
package integrity

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/observability"
)

type controlledSchedulerCheck struct {
	stage       StageID
	id          string
	status      CheckStatus
	evaluations atomic.Int32
}

type persistentFaultCheck struct {
	definition FaultDefinition
	observedAt time.Time
	healthy    bool
}

type measuredOutcomeCheck struct{ inner Check }

func (c measuredOutcomeCheck) StageID() StageID { return c.inner.StageID() }
func (c measuredOutcomeCheck) CheckID() string  { return c.inner.CheckID() }
func (c measuredOutcomeCheck) Targets(ctx context.Context, in CheckInput) ([]CheckTarget, error) {
	return c.inner.Targets(ctx, in)
}
func (c measuredOutcomeCheck) Evaluate(ctx context.Context, in CheckInput, target CheckTarget) (CheckOutcome, error) {
	outcome, err := c.inner.Evaluate(ctx, in, target)
	outcome.ObservedAt = time.Now().UTC()
	return outcome, err
}

func (c *persistentFaultCheck) StageID() StageID { return c.definition.Stages[0] }
func (c *persistentFaultCheck) CheckID() string  { return "fault." + c.definition.FaultID }
func (c *persistentFaultCheck) Targets(context.Context, CheckInput) ([]CheckTarget, error) {
	return []CheckTarget{{
		CapabilityID:   "ingestion.historical_import",
		InstallationID: "fault-harness", SourceID: c.definition.FaultID,
	}}, nil
}
func (c *persistentFaultCheck) Evaluate(ctx context.Context, _ CheckInput, _ CheckTarget) (CheckOutcome, error) {
	if c.healthy {
		return CheckOutcome{
			CheckID: c.CheckID(), Status: CheckStatusPass,
			DetailRef: "fresh_recovery_evidence", ObservedAt: c.observedAt,
		}, nil
	}
	outcome, err := evaluateInjectedFault(ctx, c.definition, c.observedAt)
	outcome.CheckID = c.CheckID()
	outcome.ObservedAt = time.Now().UTC()
	return outcome, err
}

func (c *controlledSchedulerCheck) StageID() StageID { return c.stage }
func (c *controlledSchedulerCheck) CheckID() string  { return c.id }
func (c *controlledSchedulerCheck) Targets(context.Context, CheckInput) ([]CheckTarget, error) {
	return []CheckTarget{{
		CapabilityID:   "ingestion.historical_import",
		InstallationID: "test-installation",
		SourceID:       "test-source",
	}}, nil
}
func (c *controlledSchedulerCheck) Evaluate(_ context.Context, in CheckInput, _ CheckTarget) (CheckOutcome, error) {
	c.evaluations.Add(1)
	outcome := CheckOutcome{
		CheckID: c.id, Status: c.status, ObservedAt: in.Now.UTC(),
	}
	if c.status == CheckStatusFail {
		outcome.Category = string(FailureClassDBIntegrityViolation)
		outcome.DetailRef = "controlled_failure"
	}
	return outcome, nil
}

func TestCrashRecoveryReusesOnlyFreshPassAndExecutesMissingCheck(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	reusedCheck := &controlledSchedulerCheck{stage: Stage1DiscoveryAndConfiguration, id: "controlled.reused", status: CheckStatusPass}
	missingCheck := &controlledSchedulerCheck{stage: Stage1DiscoveryAndConfiguration, id: "controlled.missing", status: CheckStatusPass}
	registry := NewCheckRegistry()
	registry.Register(reusedCheck)
	registry.Register(missingCheck)
	scheduler := NewScheduler(pool, registry)
	if err := scheduler.ConfigureReportSigning("recovery-test-key", bytes.Repeat([]byte{0x2a}, 32)); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 11, 0, 0, 0, time.UTC)
	observed := now.Add(-time.Hour)
	prior := reusablePasses{
		reusablePassKey(reusedCheck.id, "ingestion.historical_import", "test-installation", "test-source"): {
			AuditCheckKey: AuditCheckKey{
				AuditRunID: "interrupted", CheckID: reusedCheck.id,
				CapabilityID: "ingestion.historical_import", InstallationID: "test-installation", SourceID: "test-source",
			},
			StageID: Stage1DiscoveryAndConfiguration, Status: CheckStatusPass, ObservedAt: &observed,
		},
	}
	result, err := scheduler.startRun(
		ctx, RunModeReduced, TriggerStartup,
		[]StageID{Stage1DiscoveryAndConfiguration, Stage11PersistReportAndIncidents},
		nil, prior, nil, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.State != RunPassed {
		t.Fatalf("run state=%s", result.Run.State)
	}
	if reusedCheck.evaluations.Load() != 0 || missingCheck.evaluations.Load() != 1 {
		t.Fatalf("evaluations reused=%d missing=%d", reusedCheck.evaluations.Load(), missingCheck.evaluations.Load())
	}
	checks, err := ListChecksForRun(ctx, pool, result.Run.AuditRunID)
	if err != nil {
		t.Fatal(err)
	}
	var reusedEvidence bool
	for _, check := range checks {
		if check.CheckID == reusedCheck.id && check.Status == CheckStatusPass &&
			check.DetailRef == "fresh_pass_reused_after_crash_recovery" {
			reusedEvidence = true
		}
	}
	if !reusedEvidence {
		t.Fatalf("checks=%+v, want explicit reused-pass evidence", checks)
	}
}

// testDSN returns the ephemeral Postgres DSN, or skips the test if absent,
// exactly mirroring internal/dataplatform's testDSN helper.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("KANSOKU_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("KANSOKU_TEST_POSTGRES_DSN not set; run via the Session 08 validator harness")
	}
	return dsn
}

// freshSchema creates a uniquely named schema for full test isolation
// between test functions sharing one Postgres instance, then returns a
// pool bound to that schema via a connection-time search_path startup
// parameter, and applies this package's own migrations against it. This
// mirrors internal/dataplatform's freshSchema helper exactly (same
// isolation strategy, same reason: pgxpool.Pool.Config() returns a
// defensive copy so a post-hoc search_path mutation would never actually
// apply to already-open or future pool connections).
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

func freshBareSchema(t *testing.T, dsn string, label string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("t_%s_%s", sanitizeSchemaName(t.Name()), sanitizeSchemaName(label))

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
		admin.Close()
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
		_, _ = admin.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgIdent(name)))
		admin.Close()
	})
	return pool
}

// TestIntegrityMigrationsUpgradeTheExistingDataPlatformSchema exercises the
// real deployment order. Session 04 and Session 08 share one PostgreSQL
// schema, while their physical audit tables remain independently owned.
func TestIntegrityMigrationsUpgradeTheExistingDataPlatformSchema(t *testing.T) {
	dsn := testDSN(t)
	pool := freshBareSchema(t, dsn, "layered")
	ctx := context.Background()

	if err := dataplatform.Migrate(ctx, pool); err != nil {
		t.Fatalf("dataplatform migrate: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("integrity migrate over data platform: %v", err)
	}
	for _, table := range []string{
		"audit_runs", "audit_checks", "integrity_audit_runs",
		"integrity_audit_checks", "integrity_audit_reports",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("lookup %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s missing after layered migration", table)
		}
	}
	var incidentFKCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_constraint
		WHERE conname = 'fk_integrity_incident_details_base'
		  AND conrelid = 'integrity_incident_details'::regclass
		  AND confrelid = 'integrity_incidents'::regclass
	`).Scan(&incidentFKCount); err != nil {
		t.Fatalf("lookup incident detail FK: %v", err)
	}
	if incidentFKCount != 1 {
		t.Fatalf("incident detail FK count=%d, want one", incidentFKCount)
	}

	if err := Downgrade(ctx, pool, ""); err != nil {
		t.Fatalf("integrity downgrade: %v", err)
	}
	for _, table := range []string{"audit_runs", "audit_checks"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("lookup retained %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("integrity downgrade removed Session 04 table %s", table)
		}
	}
	for _, table := range []string{"integrity_audit_runs", "integrity_audit_checks"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("lookup removed %s: %v", table, err)
		}
		if exists {
			t.Fatalf("integrity downgrade left owned table %s", table)
		}
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("integrity re-upgrade: %v", err)
	}
}

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

func pgIdent(name string) string {
	return `"` + name + `"`
}

// distinctLockKey derives a per-test advisory lock key from t.Name() so
// concurrent test functions sharing one Postgres instance never contend on
// the same lock key by coincidence (they still each independently exercise
// the exact same acquire/mutual-exclusion/release code path as the
// package-default AdvisoryLockKey()).
func distinctLockKey(t *testing.T) int64 {
	t.Helper()
	h := int64(0)
	for _, r := range t.Name() {
		h = h*131 + int64(r)
	}
	if h == 0 {
		h = 1
	}
	return h
}

// TestAdvisoryLockMutualExclusionUnderConcurrentGoroutines proves the core
// single-writer guarantee: of many concurrent goroutines racing to acquire
// the same advisory lock key, exactly one succeeds at a time, and every
// other concurrent attempt observes ErrLockNotAcquired rather than
// blocking or racing into a false "acquired" state.
func TestAdvisoryLockMutualExclusionUnderConcurrentGoroutines(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	key := distinctLockKey(t)

	const attempts = 12
	var acquiredCount int64
	var notAcquiredCount int64
	var otherErrCount int64
	var wg sync.WaitGroup
	held := make(chan *HeldLock, attempts)

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock, err := acquireLockWithKey(ctx, pool, key)
			switch {
			case err == nil:
				atomic.AddInt64(&acquiredCount, 1)
				held <- lock
			case err == ErrLockNotAcquired:
				atomic.AddInt64(&notAcquiredCount, 1)
			default:
				atomic.AddInt64(&otherErrCount, 1)
				t.Logf("unexpected acquire error: %v", err)
			}
		}()
	}
	wg.Wait()
	close(held)

	if otherErrCount != 0 {
		t.Fatalf("unexpected non-lock errors: %d", otherErrCount)
	}
	if acquiredCount != 1 {
		t.Fatalf("acquiredCount = %d, want exactly 1 (mutual exclusion violated)", acquiredCount)
	}
	if notAcquiredCount != attempts-1 {
		t.Fatalf("notAcquiredCount = %d, want %d", notAcquiredCount, attempts-1)
	}

	for lock := range held {
		if err := lock.Release(ctx); err != nil {
			t.Fatalf("release: %v", err)
		}
	}

	// After releasing, the lock must be acquirable again by a fresh caller.
	lock2, err := acquireLockWithKey(ctx, pool, key)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	if err := lock2.Release(ctx); err != nil {
		t.Fatalf("release second lock: %v", err)
	}
}

// TestAdvisoryLockReleasedOnConnectionClose proves the session-scoped
// crash-safety property ADR 0011 relies on: closing the pinned connection
// (simulating a crashed process) releases the lock automatically, with no
// manual unlock step required, so a second caller can then acquire it.
func TestAdvisoryLockReleasedOnConnectionClose(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	key := distinctLockKey(t)

	lock, err := acquireLockWithKey(ctx, pool, key)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// A concurrent acquire attempt must fail while lock is held.
	if _, err := acquireLockWithKey(ctx, pool, key); err != ErrLockNotAcquired {
		t.Fatalf("concurrent acquire while held: got %v, want ErrLockNotAcquired", err)
	}
	// Simulate a crash: close the underlying connection directly rather
	// than calling pg_advisory_unlock via this package's Release method, so
	// PostgreSQL's own session-scoped release behavior (not this package's
	// Release method) is what is under test. lock.conn.Release() (the
	// pgxpool-level release, distinct from this test's own simulated
	// "crash") still must be called after closing the underlying *pgx.Conn:
	// pgxpool.Conn.Release() is what returns the puddle resource to the
	// pool, and it correctly destroys rather than reuses a resource whose
	// underlying conn.IsClosed() is true. Skipping it would leak the puddle
	// resource forever and hang pool.Close() in this test's cleanup.
	lock.conn.Conn().Close(ctx)
	lock.conn.Release()
	lock.released = true // avoid a double-release attempt in test cleanup

	// PostgreSQL releases a session-scoped advisory lock when the holding
	// backend's connection terminates, but the server-side teardown that
	// actually releases the lock is not guaranteed to have completed by the
	// moment the client-side Close() call returns (the client only knows the
	// socket was torn down, not that the server has finished processing the
	// disconnect). Poll briefly rather than asserting instantaneous release,
	// matching how a real caller must treat "the crash just happened" --
	// eventually-but-not-instantly released, never assumed still held
	// forever.
	var lock2 *HeldLock
	deadline := time.Now().Add(5 * time.Second)
	for {
		var acquireErr error
		lock2, acquireErr = acquireLockWithKey(ctx, pool, key)
		if acquireErr == nil {
			break
		}
		if acquireErr != ErrLockNotAcquired || time.Now().After(deadline) {
			t.Fatalf("acquire after simulated crash: %v", acquireErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := lock2.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// TestIsHeldByAnySessionReflectsLiveHolder proves IsHeldByAnySession (used
// by crash recovery) correctly distinguishes a live holder from no holder,
// without itself blocking or mutating lock state.
func TestIsHeldByAnySessionReflectsLiveHolder(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	key := distinctLockKey(t)

	held, err := isKeyHeldByAnySession(ctx, pool, key)
	if err != nil {
		t.Fatalf("isKeyHeldByAnySession (before acquire): %v", err)
	}
	if held {
		t.Fatalf("lock reported held before anyone acquired it")
	}

	lock, err := acquireLockWithKey(ctx, pool, key)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lock.Release(ctx)

	held, err = isKeyHeldByAnySession(ctx, pool, key)
	if err != nil {
		t.Fatalf("isKeyHeldByAnySession (while held): %v", err)
	}
	if !held {
		t.Fatalf("lock reported not held while a session holds it")
	}

	if err := lock.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	held, err = isKeyHeldByAnySession(ctx, pool, key)
	if err != nil {
		t.Fatalf("isKeyHeldByAnySession (after release): %v", err)
	}
	if held {
		t.Fatalf("lock reported held after release")
	}
}

// TestSchedulerStartRunConcurrentCallersExactlyOneRuns proves the
// scheduler-level, not just lock-level, guarantee: of two goroutines
// calling Scheduler.StartRun concurrently against the same pool, exactly
// one actually creates and runs an audit_run row, and the other cleanly
// observes ErrAlreadyRunning without inserting a second row.
func TestSchedulerStartRunConcurrentCallersExactlyOneRuns(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	scheduler := NewScheduler(pool, NewCheckRegistry())

	var wg sync.WaitGroup
	results := make(chan error, 2)
	runIDs := make(chan string, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := scheduler.StartRun(ctx, RunModeReduced, TriggerStartup, nil, time.Now())
			results <- err
			if err == nil {
				runIDs <- result.Run.AuditRunID
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(runIDs)

	var successCount, alreadyRunningCount int
	for err := range results {
		switch err {
		case nil:
			successCount++
		case ErrAlreadyRunning:
			alreadyRunningCount++
		default:
			t.Fatalf("unexpected StartRun error: %v", err)
		}
	}
	if successCount != 1 {
		t.Fatalf("successCount = %d, want exactly 1", successCount)
	}
	if alreadyRunningCount != 1 {
		t.Fatalf("alreadyRunningCount = %d, want exactly 1", alreadyRunningCount)
	}

	runs, err := ListRunsInState(ctx, pool, RunFailed)
	if err != nil {
		t.Fatalf("ListRunsInState: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected exactly 1 persisted failed audit_run row (mandatory stages/signing are intentionally unwired in this lock-only test), got %d", len(runs))
	}
	attempts, err := ListAuditAttempts(ctx, pool)
	if err != nil {
		t.Fatalf("ListAuditAttempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != "already_running" {
		t.Fatalf("attempts = %#v, want one already_running record", attempts)
	}
}

func TestSchedulerFailsClosedWithoutReportSigningKey(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()

	registry := NewCheckRegistry()
	registry.Register(&controlledSchedulerCheck{
		stage: Stage1DiscoveryAndConfiguration, id: "controlled.pass", status: CheckStatusPass,
	})
	scheduler := NewScheduler(pool, registry)
	result, err := scheduler.startRun(
		ctx, RunModeReduced, TriggerStartup,
		[]StageID{Stage1DiscoveryAndConfiguration, Stage11PersistReportAndIncidents},
		nil, nil, nil, time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("startRun: %v", err)
	}
	if result.Run.State != RunFailed {
		t.Fatalf("run state = %s, want failed when signing is not configured", result.Run.State)
	}
	checks, err := ListChecksForRun(ctx, pool, result.Run.AuditRunID)
	if err != nil {
		t.Fatalf("ListChecksForRun: %v", err)
	}
	var signingFailure bool
	for _, check := range checks {
		if check.StageID == Stage11PersistReportAndIncidents &&
			check.Status == CheckStatusFail &&
			check.DetailRef == "report_signing_not_configured" {
			signingFailure = true
		}
	}
	if !signingFailure {
		t.Fatalf("checks = %+v, want persisted stage-11 signing failure", checks)
	}
	var reportCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM integrity_audit_reports WHERE audit_run_id = $1
	`, result.Run.AuditRunID).Scan(&reportCount); err != nil {
		t.Fatalf("count reports: %v", err)
	}
	if reportCount != 0 {
		t.Fatalf("reportCount = %d, want zero without a signing key", reportCount)
	}
}

func TestSchedulerStage11ReportFailureRollsBackAndOpensIncidentAtomically(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION reject_integrity_audit_report() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
		    RAISE EXCEPTION 'injected report persistence failure';
		END
		$$;
		CREATE TRIGGER reject_integrity_audit_report
		BEFORE INSERT OR UPDATE ON integrity_audit_reports
		FOR EACH ROW EXECUTE FUNCTION reject_integrity_audit_report()
	`); err != nil {
		t.Fatalf("install report failure trigger: %v", err)
	}
	registry := NewCheckRegistry()
	registry.Register(&controlledSchedulerCheck{
		stage: Stage1DiscoveryAndConfiguration, id: "controlled.atomic-report", status: CheckStatusPass,
	})
	scheduler := NewScheduler(pool, registry)
	if err := scheduler.ConfigureReportSigning("atomic-test-key", bytes.Repeat([]byte{0x6a}, 32)); err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.startRun(
		ctx, RunModeReduced, TriggerStartup,
		[]StageID{Stage1DiscoveryAndConfiguration, Stage11PersistReportAndIncidents},
		nil, nil, nil, time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("startRun fallback should persist terminal failure: %v", err)
	}
	if result.Run.State != RunFailed {
		t.Fatalf("run state = %s, want failed", result.Run.State)
	}
	var reportCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM integrity_audit_reports WHERE audit_run_id = $1`, result.Run.AuditRunID).Scan(&reportCount); err != nil {
		t.Fatal(err)
	}
	if reportCount != 0 {
		t.Fatalf("report count = %d, want zero after rolled-back finalization", reportCount)
	}
	checks, err := ListChecksForRun(ctx, pool, result.Run.AuditRunID)
	if err != nil {
		t.Fatal(err)
	}
	var stage11Failures int
	for _, check := range checks {
		if check.StageID == Stage11PersistReportAndIncidents &&
			check.Status == CheckStatusFail &&
			check.DetailRef == "signed_report_persistence_failed" {
			stage11Failures++
		}
	}
	if stage11Failures != 1 {
		t.Fatalf("checks = %+v, want one durable Stage11 persistence failure", checks)
	}
	open, err := ListOpenIncidents(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].SourceID != "integrity-finalization" {
		t.Fatalf("open incidents = %+v, want Stage11 finalization incident", open)
	}
}

func TestFaultComponentClassifiersPersistSchedulerStage11IncidentLifecycle(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	for _, definition := range FaultCatalog {
		if definition.Evidence != FaultEvidenceComponentClassifier {
			continue
		}
		t.Run(definition.FaultID, func(t *testing.T) {
			injectedAt := time.Now().UTC()
			check := &persistentFaultCheck{definition: definition, observedAt: injectedAt}
			registry := NewCheckRegistry()
			registry.Register(check)
			scheduler := NewScheduler(pool, registry)
			if err := scheduler.ConfigureReportSigning("fault-lifecycle-key", bytes.Repeat([]byte{0x4f}, 32)); err != nil {
				t.Fatal(err)
			}
			stages := []StageID{definition.Stages[0], Stage11PersistReportAndIncidents}
			failed, err := scheduler.startRun(ctx, RunModeReduced, TriggerStartup, stages, nil, nil, nil, injectedAt)
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
			if len(open) != 1 || open[0].FailureClass != definition.FailureClass ||
				open[0].SourceID != definition.FaultID {
				t.Fatalf("open incidents = %+v", open)
			}
			base, err := GetIncidentBase(ctx, pool, open[0].IncidentID)
			if err != nil {
				t.Fatal(err)
			}
			if base.OpenedAt.Before(injectedAt) {
				t.Fatalf("incident opened_at=%s precedes classifier invocation=%s", base.OpenedAt, injectedAt)
			}
			check.healthy = true
			check.observedAt = time.Now().UTC()
			recovered, err := scheduler.startRun(ctx, RunModeReduced, TriggerStartup, stages, nil, nil, nil, check.observedAt)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.Run.State != RunPassed {
				t.Fatalf("recovery state = %s, want passed", recovered.Run.State)
			}
			open, err = ListOpenIncidents(ctx, pool)
			if err != nil {
				t.Fatal(err)
			}
			if len(open) != 0 {
				t.Fatalf("incidents remained open after fresh scheduler pass: %+v", open)
			}
		})
	}
}

func TestFaultMutationCorruptSpoolPersistsMeasuredIncidentLifecycle(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	spoolDir := filepath.Join(t.TempDir(), "spool")
	if err := os.Mkdir(spoolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	spoolPath := filepath.Join(spoolDir, "events.spool")
	if err := os.WriteFile(spoolPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := observability.CheckDurableSpool(spoolPath, 1<<20); err != nil {
		t.Fatalf("host cannot establish a safe baseline spool: %v", err)
	}
	corrupt := []byte(`{"truncated":`)
	// Start the SLO clock before activating the fault so the measurement
	// includes the mutation itself as well as scheduler detection/persistence.
	injectedAt := time.Now().UTC()
	if err := os.WriteFile(spoolPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	definition, _ := FaultDefinitionByID("corrupt_spool")
	check := NewRetentionDiskBackupCheck(
		func(context.Context, time.Time, int) (RetentionDryRunResult, error) {
			return RetentionDryRunResult{EligibleForDrop: map[string][]string{}}, nil
		},
		func(context.Context, float64) (DiskForecast, error) {
			return DiskForecast{UsedFraction: 0.5}, nil
		},
		func(context.Context) (BackupStatus, error) {
			return BackupStatus{
				LastBackupAt: time.Now().UTC(), LastBackupChecksumOK: true,
				LastRestoreTestAt: time.Now().UTC(), LastRestoreTestRan: true, LastRestoreTestPassed: true,
			}, nil
		},
		func(context.Context) (bool, string, error) { return false, "", nil },
	)
	check.SpoolIntegrity = NewSpoolIntegrityLookup(spoolPath, 1<<20)
	registry := NewCheckRegistry()
	registry.Register(measuredOutcomeCheck{inner: check})
	scheduler := NewScheduler(pool, registry)
	if err := scheduler.ConfigureReportSigning("spool-mutation-key", bytes.Repeat([]byte{0x35}, 32)); err != nil {
		t.Fatal(err)
	}
	failed, err := scheduler.startRun(
		ctx, RunModeReduced, TriggerStartup,
		[]StageID{Stage9RetentionDiskAndBackup, Stage11PersistReportAndIncidents},
		nil, nil, nil, injectedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Run.State != RunFailed {
		t.Fatalf("run state=%s", failed.Run.State)
	}
	open, err := ListOpenIncidents(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].FailureClass != definition.FailureClass ||
		open[0].SourceID != "durable-spool" {
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
	if after, err := os.ReadFile(spoolPath); err != nil || !bytes.Equal(after, corrupt) {
		t.Fatalf("read-only detector rewrote corrupt spool: bytes=%q err=%v", after, err)
	}
	if err := os.WriteFile(spoolPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := scheduler.startRun(
		ctx, RunModeReduced, TriggerStartup,
		[]StageID{Stage9RetentionDiskAndBackup, Stage11PersistReportAndIncidents},
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
		t.Fatalf("incidents after repaired spool=%+v err=%v", open, err)
	}
}

func TestSchedulerStage11SignsReportsDeduplicatesAndRecoversIncidents(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()

	controlled := &controlledSchedulerCheck{
		stage: Stage1DiscoveryAndConfiguration, id: "controlled.lifecycle", status: CheckStatusFail,
	}
	registry := NewCheckRegistry()
	registry.Register(controlled)
	scheduler := NewScheduler(pool, registry)
	key := make([]byte, 32)
	for index := range key {
		key[index] = 0x5a
	}
	if err := scheduler.ConfigureReportSigning("test-device-key", key); err != nil {
		t.Fatalf("ConfigureReportSigning: %v", err)
	}
	stages := []StageID{Stage1DiscoveryAndConfiguration, Stage11PersistReportAndIncidents}
	base := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)

	first, err := scheduler.startRun(ctx, RunModeReduced, TriggerStartup, stages, nil, nil, nil, base)
	if err != nil {
		t.Fatalf("first startRun: %v", err)
	}
	second, err := scheduler.startRun(ctx, RunModeReduced, TriggerStartup, stages, nil, nil, nil, base.Add(time.Minute))
	if err != nil {
		t.Fatalf("second startRun: %v", err)
	}
	if first.Run.State != RunFailed || second.Run.State != RunFailed {
		t.Fatalf("failure run states = %s/%s, want failed/failed", first.Run.State, second.Run.State)
	}
	open, err := ListOpenIncidents(ctx, pool)
	if err != nil {
		t.Fatalf("ListOpenIncidents after failures: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open incidents = %+v, want one deduplicated incident", open)
	}
	baseIncident, err := GetIncidentBase(ctx, pool, open[0].IncidentID)
	if err != nil {
		t.Fatalf("GetIncidentBase: %v", err)
	}
	if baseIncident.OccurrenceCount != 2 {
		t.Fatalf("OccurrenceCount = %d, want 2 after repeated failure", baseIncident.OccurrenceCount)
	}

	controlled.status = CheckStatusPass
	recovered, err := scheduler.startRun(ctx, RunModeReduced, TriggerStartup, stages, nil, nil, nil, base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("recovery startRun: %v", err)
	}
	if recovered.Run.State != RunPassed {
		t.Fatalf("recovery run state = %s, want passed", recovered.Run.State)
	}
	open, err = ListOpenIncidents(ctx, pool)
	if err != nil {
		t.Fatalf("ListOpenIncidents after recovery: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open incidents = %+v, want none after later fresh exact-check pass", open)
	}

	for _, run := range []RunResult{first, second, recovered} {
		signed, err := LoadSignedAuditReport(ctx, pool, run.Run.AuditRunID)
		if err != nil {
			t.Fatalf("LoadSignedAuditReport(%s): %v", run.Run.AuditRunID, err)
		}
		if err := VerifySignedAuditReport(signed, key); err != nil {
			t.Fatalf("VerifySignedAuditReport(%s): %v", run.Run.AuditRunID, err)
		}
		if signed.Report.State != run.Run.State {
			t.Fatalf("signed state = %s, persisted run state = %s", signed.Report.State, run.Run.State)
		}
	}
	signed, err := LoadSignedAuditReport(ctx, pool, recovered.Run.AuditRunID)
	if err != nil {
		t.Fatalf("LoadSignedAuditReport recovery: %v", err)
	}
	signed.Report.State = RunFailed
	if err := VerifySignedAuditReport(signed, key); err == nil {
		t.Fatalf("tampered persisted report envelope verified")
	}
	if _, err := pool.Exec(ctx, `
		UPDATE integrity_audit_reports
		SET canonical_report = canonical_report || '{"unexpected_field":true}'::jsonb
		WHERE audit_run_id = $1
	`, recovered.Run.AuditRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSignedAuditReport(ctx, pool, recovered.Run.AuditRunID); err == nil {
		t.Fatal("strict report load accepted unknown canonical field")
	}
	if _, err := pool.Exec(ctx, `
		UPDATE integrity_audit_reports
		SET canonical_report = canonical_report - 'unexpected_field',
		    generated_at = generated_at + interval '1 second'
		WHERE audit_run_id = $1
	`, recovered.Run.AuditRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSignedAuditReport(ctx, pool, recovered.Run.AuditRunID); err == nil {
		t.Fatal("strict report load accepted mismatched duplicated generated_at")
	}
}

func TestPostgresLiveCanaryStatePersistsCooldownAcrossStoreInstances(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	firstStore := PostgresLiveCanaryStateStore{Pool: pool}
	recipeID := "fixture-canary-v1"
	startedAt := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(5 * time.Second)

	if err := firstStore.RecordAuthorization(ctx, recipeID, "fixture-adapter", startedAt.Add(-2*time.Hour), startedAt.Add(-time.Hour)); err != nil {
		t.Fatalf("RecordAuthorization: %v", err)
	}
	if err := firstStore.MarkStarted(ctx, recipeID, startedAt); err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}
	if err := firstStore.MarkFinished(ctx, recipeID, CheckStatusPass, finishedAt); err != nil {
		t.Fatalf("MarkFinished: %v", err)
	}
	afterRestart := PostgresLiveCanaryStateStore{Pool: pool}
	state, err := afterRestart.Load(ctx, recipeID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !state.LastStartedAt.Equal(startedAt) || !state.LastFinishedAt.Equal(finishedAt) ||
		state.LastStatus != CheckStatusPass {
		t.Fatalf("state = %+v, want durable start/finish/pass", state)
	}
	durableGate, err := afterRestart.LoadAuthorization(ctx, recipeID, "fixture-adapter")
	if err != nil {
		t.Fatalf("LoadAuthorization: %v", err)
	}
	if !durableGate.ExplicitCredentialsPresent || !durableGate.ExplicitUserConsentRecorded ||
		durableGate.ConsentRecordedAt.IsZero() {
		t.Fatalf("durable authorization = %+v", durableGate)
	}
	recipe := fixtureLiveCanaryRecipe()
	if authorized, reason := durableGate.Authorize(recipe, startedAt.Add(time.Minute)); authorized || reason != "cooldown_active" {
		t.Fatalf("post-restart cooldown authorization = %v/%s", authorized, reason)
	}
}

// TestUpsertCheckIdempotentRerunNeverDuplicates proves the idempotency_rule:
// rerunning the same (audit_run_id, check_id, capability_id,
// installation_id) key upserts in place rather than creating a second row,
// and a later call with fresher evidence overwrites the earlier status
// rather than being ignored or duplicated.
func TestUpsertCheckIdempotentRerunNeverDuplicates(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()

	run := AuditRun{AuditRunID: "run_idem", Mode: RunModeReduced, Trigger: TriggerStartup, State: RunScheduled, ScheduledAt: time.Now(), AdvisoryLockKey: 1, RequestedStages: []StageID{Stage1DiscoveryAndConfiguration}, InputsVersionRef: map[string]string{}}
	if err := InsertScheduledRun(ctx, pool, run); err != nil {
		t.Fatalf("InsertScheduledRun: %v", err)
	}

	key := AuditCheckKey{AuditRunID: run.AuditRunID, CheckID: "discovery.executable_resolvable", CapabilityID: "discovery.agent_and_surface", InstallationID: "inst_1"}
	pending := AuditCheck{AuditCheckKey: key, StageID: Stage1DiscoveryAndConfiguration, Status: CheckStatusPending}
	if err := UpsertCheck(ctx, pool, pending); err != nil {
		t.Fatalf("UpsertCheck (pending): %v", err)
	}

	passed := AuditCheck{AuditCheckKey: key, StageID: Stage1DiscoveryAndConfiguration, Status: CheckStatusPass, Category: "configuration", DetailRef: "detail_ref_1", ObservedAt: timePtr(time.Now())}
	if err := UpsertCheck(ctx, pool, passed); err != nil {
		t.Fatalf("UpsertCheck (passed): %v", err)
	}
	// Rerunning with the same outcome must remain idempotent (no duplicate
	// row, same terminal status).
	if err := UpsertCheck(ctx, pool, passed); err != nil {
		t.Fatalf("UpsertCheck (rerun): %v", err)
	}

	checks, err := ListChecksForRun(ctx, pool, run.AuditRunID)
	if err != nil {
		t.Fatalf("ListChecksForRun: %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("expected exactly 1 audit_check row after 3 upserts of the same key, got %d", len(checks))
	}
	if checks[0].Status != CheckStatusPass {
		t.Fatalf("final status = %s, want pass", checks[0].Status)
	}
}

// TestMarkStaleRunsInterruptedReclassifiesEveryUnlockedRunningRun proves
// process-start recovery has no arbitrary age window: if no live session
// owns the advisory lock, every durable running row is abandoned.
func TestMarkStaleRunsInterruptedReclassifiesEveryUnlockedRunningRun(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()

	// Row 1: abandoned -- old StartedAt, no live lock holder.
	staleRun := AuditRun{AuditRunID: "run_stale", Mode: RunModeFull, Trigger: TriggerScheduledDaily, State: RunScheduled, ScheduledAt: time.Now().Add(-1 * time.Hour), AdvisoryLockKey: AdvisoryLockKey(), RequestedStages: FullModeStages(), InputsVersionRef: map[string]string{}}
	if err := InsertScheduledRun(ctx, pool, staleRun); err != nil {
		t.Fatalf("insert stale run: %v", err)
	}
	oldStart := time.Now().Add(-1 * time.Hour)
	if err := Transition(&staleRun, RunRunning, oldStart, FailureReasonNone); err != nil {
		t.Fatalf("transition stale run to running: %v", err)
	}
	if err := TransitionRun(ctx, pool, staleRun); err != nil {
		t.Fatalf("persist stale run running: %v", err)
	}

	// Row 2: recently started but still abandoned because no lock is held.
	recentRun := AuditRun{AuditRunID: "run_recent", Mode: RunModeReduced, Trigger: TriggerStartup, State: RunScheduled, ScheduledAt: time.Now(), AdvisoryLockKey: AdvisoryLockKey(), RequestedStages: ReducedModeStages(TriggerStartup), InputsVersionRef: map[string]string{}}
	if err := InsertScheduledRun(ctx, pool, recentRun); err != nil {
		t.Fatalf("insert recent run: %v", err)
	}
	if err := Transition(&recentRun, RunRunning, time.Now(), FailureReasonNone); err != nil {
		t.Fatalf("transition recent run to running: %v", err)
	}
	if err := TransitionRun(ctx, pool, recentRun); err != nil {
		t.Fatalf("persist recent run running: %v", err)
	}

	// Seed one completed check for the stale run, to prove crash recovery
	// never discards or duplicates already-completed evidence.
	completedKey := AuditCheckKey{AuditRunID: staleRun.AuditRunID, CheckID: "discovery.executable_resolvable", CapabilityID: "discovery.agent_and_surface", InstallationID: "inst_1"}
	completed := AuditCheck{AuditCheckKey: completedKey, StageID: Stage1DiscoveryAndConfiguration, Status: CheckStatusPass, Category: "configuration", ObservedAt: timePtr(time.Now())}
	if err := UpsertCheck(ctx, pool, completed); err != nil {
		t.Fatalf("seed completed check: %v", err)
	}
	// And one still-pending check, proving it stays gray/pending rather
	// than being silently marked passed by the crash-recovery pass.
	pendingKey := AuditCheckKey{AuditRunID: staleRun.AuditRunID, CheckID: "discovery.state_root_readable", CapabilityID: "discovery.agent_and_surface", InstallationID: "inst_1"}
	pending := AuditCheck{AuditCheckKey: pendingKey, StageID: Stage1DiscoveryAndConfiguration, Status: CheckStatusPending}
	if err := UpsertCheck(ctx, pool, pending); err != nil {
		t.Fatalf("seed pending check: %v", err)
	}

	interrupted, err := MarkStaleRunsInterrupted(ctx, pool, time.Now())
	if err != nil {
		t.Fatalf("MarkStaleRunsInterrupted: %v", err)
	}
	if len(interrupted) != 2 || interrupted[0] != staleRun.AuditRunID || interrupted[1] != recentRun.AuditRunID {
		t.Fatalf("interrupted = %v, want both unlocked running rows", interrupted)
	}

	got, err := GetRun(ctx, pool, staleRun.AuditRunID)
	if err != nil {
		t.Fatalf("GetRun (stale): %v", err)
	}
	if got.State != RunFailed || got.FailureReason != FailureReasonCrashRecoveryStale {
		t.Fatalf("stale run state = %s/%s, want failed/crash_recovery_stale_run", got.State, got.FailureReason)
	}

	recentInterrupted, err := GetRun(ctx, pool, recentRun.AuditRunID)
	if err != nil {
		t.Fatalf("GetRun (recent): %v", err)
	}
	if recentInterrupted.State != RunFailed || recentInterrupted.FailureReason != FailureReasonCrashRecoveryStale {
		t.Fatalf("recent run state = %s/%s, want failed/crash recovery", recentInterrupted.State, recentInterrupted.FailureReason)
	}

	checks, err := ListChecksForRun(ctx, pool, staleRun.AuditRunID)
	if err != nil {
		t.Fatalf("ListChecksForRun: %v", err)
	}
	statusByKey := map[string]CheckStatus{}
	for _, c := range checks {
		statusByKey[c.CheckID] = c.Status
	}
	if statusByKey["discovery.executable_resolvable"] != CheckStatusPass {
		t.Fatalf("completed check evidence was altered by crash recovery: %v", statusByKey)
	}
	if statusByKey["discovery.state_root_readable"] != CheckStatusPending {
		t.Fatalf("incomplete check was not left pending/gray by crash recovery: %v", statusByKey)
	}
}

// TestMarkStaleRunsInterruptedNeverTouchesLiveHeldRun proves that a
// "running" row whose advisory lock IS currently held by a live session is
// never reclassified, even if its StartedAt looks old, because a live
// holder means the run is not abandoned.
func TestMarkStaleRunsInterruptedNeverTouchesLiveHeldRun(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()

	lock, err := AcquireLock(ctx, pool)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer lock.Release(ctx)

	run := AuditRun{AuditRunID: "run_live", Mode: RunModeFull, Trigger: TriggerManualOperatorRequest, State: RunScheduled, ScheduledAt: time.Now().Add(-2 * time.Hour), AdvisoryLockKey: lock.key, RequestedStages: FullModeStages(), InputsVersionRef: map[string]string{}}
	if err := InsertScheduledRun(ctx, pool, run); err != nil {
		t.Fatalf("insert: %v", err)
	}
	oldStart := time.Now().Add(-2 * time.Hour)
	if err := Transition(&run, RunRunning, oldStart, FailureReasonNone); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if err := TransitionRun(ctx, pool, run); err != nil {
		t.Fatalf("persist: %v", err)
	}

	interrupted, err := MarkStaleRunsInterrupted(ctx, pool, time.Now())
	if err != nil {
		t.Fatalf("MarkStaleRunsInterrupted: %v", err)
	}
	if len(interrupted) != 0 {
		t.Fatalf("interrupted = %v, want none while the advisory lock is live-held", interrupted)
	}
	got, err := GetRun(ctx, pool, run.AuditRunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.State != RunRunning {
		t.Fatalf("state = %s, want running (must not be reclassified while lock is live-held)", got.State)
	}
}
