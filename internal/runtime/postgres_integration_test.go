//go:build postgres_integration

// Package runtime's Postgres-backed tests require a real ephemeral PostgreSQL
// 18 instance, exactly like internal/dataplatform's and internal/integrity's
// own postgres_integration_test.go files. They are excluded from the default
// `go test ./...` sweep by the postgres_integration build tag; run them via
// `python3 scripts/validate_runtime.py --runtime-only` (or the full
// `python3 scripts/validate_runtime.py`), which builds a combined
// go-toolchain + pinned-postgres-client test image (mirroring the production
// deploy/Dockerfile's postgres-filesystem + static-binary strategy so
// pg_dump/pg_restore 18 are on PATH), starts the pinned Postgres image in an
// isolated Docker network with POSTGRES_DB/POSTGRES_USER == kansoku (so the
// strict runtime Config validates unchanged), points KANSOKU_TEST_POSTGRES_DSN
// at it, runs this suite, and tears everything down deterministically.
//
// Unlike internal/dataplatform (schema-isolated) these tests exercise the real
// runtime job-lease rows, the pg_dump/pg_restore backup round trip and the
// DurableIngressQueue-to-ObservabilityHandoff commit path against live rows.
package runtime

import (
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
	"kansoku.local/kansoku/internal/integrity"
	"kansoku.local/kansoku/internal/observability"
)

// testDSN returns the ephemeral Postgres DSN provided by the runtime
// validator harness, or skips the test if it is absent, mirroring the
// dataplatform/integrity testDSN helpers.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("KANSOKU_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("KANSOKU_TEST_POSTGRES_DSN not set; run via scripts/validate_runtime.py")
	}
	return dsn
}

// applyAllMigrations applies the three real migration sets in the exact
// production order used by NewAppliance/MigrateOnly: data platform, then
// integrity, then runtime. The DurableIngressQueue commit test needs the
// data-platform fact tables (its ObservabilityHandoff writes into them) as
// well as the runtime ledger, so every runtime-backed test bootstraps the
// full pool the same way assembly.go does in production.
func applyAllMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if err := dataplatform.Migrate(ctx, pool); err != nil {
		t.Fatalf("dataplatform migrate: %v", err)
	}
	if err := integrity.Migrate(ctx, pool); err != nil {
		t.Fatalf("integrity migrate: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("runtime migrate: %v", err)
	}
}

// freshSchema creates a uniquely named schema for full test isolation between
// test functions sharing one Postgres instance, binds a dedicated pool to it
// via a connection-time search_path startup parameter (matching dataplatform's
// helper exactly, and for the same reason: pgxpool.Pool.Config() returns a
// defensive copy so a post-hoc search_path mutation would never apply), then
// applies all three migration sets. Used by tests that only touch tables and
// never CREATE DATABASE.
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
	applyAllMigrations(t, ctx, pool)
	return pool
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

func pgIdent(name string) string { return `"` + name + `"` }

// mustPrivateSpoolDir creates the private data directory and its 0700 spool
// subdirectory the same way production NewAppliance does before building the
// DurableIngressQueue, so the secure spool's strict leaf-directory ownership
// and 0700 mode checks pass.
func mustPrivateSpoolDir(t *testing.T, dataDir string) {
	t.Helper()
	if err := ensurePrivateDirectory(dataDir); err != nil {
		t.Fatalf("ensure private data dir: %v", err)
	}
	if err := ensurePrivateDirectory(filepath.Join(dataDir, "spool")); err != nil {
		t.Fatalf("ensure private spool dir: %v", err)
	}
}

// --- (a) job leases against real runtime_job_runs rows ------------------------

// countingHandler counts invocations and can block until released, so the
// concurrent-lease test can pin exactly one run in the 'running' state while a
// second concurrent Run observes the partial unique-lease index and records
// 'already_running' against a real row.
type countingHandler struct {
	calls   atomic.Int32
	release chan struct{}
	blocked chan struct{}
	once    sync.Once
}

func (h *countingHandler) run(ctx context.Context) (map[string]int64, error) {
	h.calls.Add(1)
	if h.blocked != nil {
		h.once.Do(func() { close(h.blocked) })
	}
	if h.release != nil {
		select {
		case <-h.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return map[string]int64{"did_work": 1}, nil
}

func newJobManagerForTest(t *testing.T, pool *pgxpool.Pool, handler JobHandler) *JobManager {
	t.Helper()
	manager, err := NewJobManager(pool, map[JobID]JobHandler{JobRollupRepair: handler})
	if err != nil {
		t.Fatalf("NewJobManager: %v", err)
	}
	return manager
}

// TestJobLeaseAcquireRenewAlreadyRunningAndStaleRecovery proves the four
// PostgreSQL row-lease behaviors runtime/jobs.go claims, against real rows:
//  1. a run acquires the lease, renews it while the handler is in flight and
//     reaches a terminal passed state;
//  2. a concurrent Run of the same job_id while one is running records the
//     'already_running' terminal state without a second live lease (the
//     partial unique index runtime_job_runs_one_active_lease_idx holds);
//  3. RecoverInterrupted reclaims a stale expired lease, marking it
//     'interrupted' and clearing lease ownership;
//  4. after recovery a fresh Run of the same job succeeds again.
func TestJobLeaseAcquireRenewAlreadyRunningAndStaleRecovery(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()

	// (2) Concurrent leases: block the first handler so its lease stays live,
	// launch a second Run of the same job, and require exactly one running
	// lease plus one 'already_running' record.
	blocking := &countingHandler{release: make(chan struct{}), blocked: make(chan struct{})}
	holder := newJobManagerForTest(t, pool, blocking.run)
	contender := newJobManagerForTest(t, pool, func(context.Context) (map[string]int64, error) {
		return map[string]int64{}, nil
	})

	firstDone := make(chan JobRun, 1)
	firstErr := make(chan error, 1)
	go func() {
		run, err := holder.Run(ctx, JobRollupRepair)
		firstDone <- run
		firstErr <- err
	}()
	select {
	case <-blocking.blocked:
	case <-time.After(10 * time.Second):
		close(blocking.release)
		t.Fatal("first job handler never started")
	}

	var liveLeases int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM runtime_job_runs WHERE state='running' AND lease_owner_id IS NOT NULL`).Scan(&liveLeases); err != nil {
		t.Fatalf("count live leases: %v", err)
	}
	if liveLeases != 1 {
		t.Fatalf("live leases while holder runs = %d, want exactly 1", liveLeases)
	}

	contended, err := contender.Run(ctx, JobRollupRepair)
	if err != nil {
		t.Fatalf("contended Run: %v", err)
	}
	if contended.State != JobAlreadyRunning || contended.ErrorClass != "already_running" {
		t.Fatalf("contended run = %s/%s, want already_running", contended.State, contended.ErrorClass)
	}
	if contended.DetailRef != "runtime.job_lease" {
		t.Fatalf("contended detail_ref = %q, want runtime.job_lease", contended.DetailRef)
	}

	// (1) Release the holder; it must renew during flight (lease/2 = 1m tick
	// is too slow for the test, but reaching 'passed' proves the row was owned
	// through completion) and land on a terminal passed state owning no lease.
	close(blocking.release)
	holderRun := <-firstDone
	if err := <-firstErr; err != nil {
		t.Fatalf("holder Run error: %v", err)
	}
	if holderRun.State != JobPassed || holderRun.ResultCounts["did_work"] != 1 {
		t.Fatalf("holder run = %s counts=%v, want passed with work counts", holderRun.State, holderRun.ResultCounts)
	}
	if blocking.calls.Load() != 1 {
		t.Fatalf("holder handler invoked %d times, want exactly 1", blocking.calls.Load())
	}
	var stillLeased int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM runtime_job_runs WHERE lease_owner_id IS NOT NULL OR lease_expires_at IS NOT NULL`).Scan(&stillLeased); err != nil {
		t.Fatalf("count residual leases: %v", err)
	}
	if stillLeased != 0 {
		t.Fatalf("residual lease rows after completion = %d, want 0", stillLeased)
	}

	// (3) RecoverInterrupted: fabricate a stale running row whose lease has
	// already expired and no owner is alive, then require startup recovery to
	// reclaim it as 'interrupted' with lease ownership cleared.
	stale := "job_" + fmt.Sprintf("%032x", 0xdead)
	past := time.Now().UTC().Add(-time.Hour)
	if _, err := pool.Exec(ctx, `
		INSERT INTO runtime_job_runs
			(job_run_id, job_id, state, attempt, scheduled_at, started_at,
			 lease_owner_id, lease_expires_at, result_counts)
		VALUES ($1,'rollup_repair','running',1,$2,$2,'worker_dead',$3,'{}'::jsonb)
	`, stale, past, past.Add(time.Minute)); err != nil {
		t.Fatalf("insert stale running row: %v", err)
	}
	if err := holder.RecoverInterrupted(ctx); err != nil {
		t.Fatalf("RecoverInterrupted: %v", err)
	}
	var state, errorClass, detailRef string
	var owner, expires *string
	if err := pool.QueryRow(ctx, `
		SELECT state, COALESCE(error_class,''), COALESCE(detail_ref,''), lease_owner_id, lease_expires_at::text
		FROM runtime_job_runs WHERE job_run_id=$1
	`, stale).Scan(&state, &errorClass, &detailRef, &owner, &expires); err != nil {
		t.Fatalf("read recovered stale row: %v", err)
	}
	if state != string(JobInterrupted) || errorClass != "lease_expired" || detailRef != "runtime.startup_recovery" {
		t.Fatalf("recovered stale row = %s/%s/%s, want interrupted/lease_expired/runtime.startup_recovery", state, errorClass, detailRef)
	}
	if owner != nil || expires != nil {
		t.Fatalf("recovered stale row still owns a lease: owner=%v expires=%v", owner, expires)
	}

	// (4) A fresh Run after recovery still succeeds (the reclaimed lease slot
	// is free again).
	after := newJobManagerForTest(t, pool, func(context.Context) (map[string]int64, error) {
		return map[string]int64{"post_recovery": 1}, nil
	})
	post, err := after.Run(ctx, JobRollupRepair)
	if err != nil {
		t.Fatalf("post-recovery Run: %v", err)
	}
	if post.State != JobPassed || post.ResultCounts["post_recovery"] != 1 {
		t.Fatalf("post-recovery run = %s counts=%v, want passed", post.State, post.ResultCounts)
	}
}

// --- (b) native backup -> restore-verify round trip --------------------------

// backupTestConfig points a strict runtime Config at the ephemeral Postgres
// server's default `kansoku` database. The validator harness names the
// database and user `kansoku` so Config.Validate() (which pins both) passes
// unchanged; only the host/port come from the DSN.
func backupTestConfig(t *testing.T, dsn string) (Config, Secrets) {
	t.Helper()
	parsed, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	root := t.TempDir()
	config := validTestConfig(root)
	config.Database.Host = parsed.ConnConfig.Host
	config.Database.Port = int(parsed.ConnConfig.Port)
	config.Database.Name = "kansoku"
	config.Database.User = "kansoku"
	if err := config.Validate(); err != nil {
		t.Fatalf("backup test config invalid: %v", err)
	}
	secrets := Secrets{DatabasePassword: []byte(parsed.ConnConfig.Password)}
	if len(secrets.DatabasePassword) < minSecretBytes {
		t.Fatalf("ephemeral database password is shorter than %d bytes; harness must generate a >=32 byte password", minSecretBytes)
	}
	return config, secrets
}

// operationsForBackup builds a real OperationsService bound to the default
// `kansoku` database on the ephemeral server, with all three migration sets
// applied to its public schema exactly as production bootstraps them.
func operationsForBackup(t *testing.T, dsn string) (*OperationsService, *pgxpool.Pool, Config, Secrets) {
	t.Helper()
	ctx := context.Background()
	config, secrets := backupTestConfig(t, dsn)
	realDSN, err := config.DatabaseDSN(secrets.DatabasePassword)
	if err != nil {
		t.Fatalf("build kansoku DSN: %v", err)
	}
	pool, err := pgxpool.New(ctx, realDSN)
	if err != nil {
		t.Fatalf("connect kansoku db: %v", err)
	}
	t.Cleanup(pool.Close)
	applyAllMigrations(t, ctx, pool)
	// Pre-create the private data + spool directories exactly as production
	// NewAppliance does before constructing the durable queue.
	mustPrivateSpoolDir(t, config.DataDir)
	handoff, err := dataplatform.NewObservabilityHandoff(pool, config.QueryTimeout())
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	queue, err := NewDurableIngressQueue(handoff, config.DataDir, config.QueueCapacity, config.SpoolMaxBytes)
	if err != nil {
		t.Fatalf("durable queue: %v", err)
	}
	t.Cleanup(queue.Close)
	jobs, err := NewJobManager(pool, map[JobID]JobHandler{})
	if err != nil {
		t.Fatalf("job manager: %v", err)
	}
	operations, err := NewOperationsService(config, secrets, pool, queue, jobs)
	if err != nil {
		t.Fatalf("operations service: %v", err)
	}
	return operations, pool, config, secrets
}

// TestNativeBackupRestoreVerifyRoundTripAndCleanup proves the ADR 0012
// decision 7 backup/restore boundary against a live Postgres 18 server:
//   - Backup runs real pg_dump --format=custom and produces a checksummed
//     manifest whose table counts include seeded data;
//   - RestoreVerify runs real pg_restore into a brand-new randomly named
//     database, compares counts/constraints/migration ledgers/formula lineage,
//     and (on success) drops that temporary database;
//   - a deliberately corrupted archive fails RestoreVerify AND still drops the
//     temporary restore database (dropRestoreDatabase fires on the failure
//     path too), leaving no orphaned restore_* databases behind.
func TestNativeBackupRestoreVerifyRoundTripAndCleanup(t *testing.T) {
	dsn := testDSN(t)
	operations, pool, _, _ := operationsForBackup(t, dsn)
	ctx := context.Background()

	// Seed one real normalized fact through the same handoff production ingress
	// uses, so the backup covers non-empty fact/evidence tables.
	seedFactThroughHandoff(t, pool, "backup_seed")
	if _, err := pool.Exec(ctx, `
		INSERT INTO metric_rollups_hourly (
			metric_family,bucket_start,dimension_scope,formula_version,
			event_count,unknown_count,completeness_duration_ms,value_numeric
		) VALUES (
			'backup_restore_fixture','2026-07-26T10:00:00Z','fixture',
			'backup_restore_fixture/1',1,0,3600000,1
		)
	`); err != nil {
		t.Fatalf("seed backup rollup: %v", err)
	}

	before := countRestoreDatabases(t, pool)

	backupResult, err := operations.Backup(ctx, BackupRequest{})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	backup := backupResult.(BackupResult)
	if backup.TableCounts["events"] < 1 {
		t.Fatalf("backup events count = %d, want >=1 after seeding", backup.TableCounts["events"])
	}
	if len(backup.ArchiveSHA256) != 64 {
		t.Fatalf("backup archive sha256 = %q, want 64 hex chars", backup.ArchiveSHA256)
	}
	// The source remains live after pg_dump. Verification is against the
	// immutable archive and its manifest, never against a later mutable
	// source snapshot.
	if _, err := pool.Exec(ctx, `
		UPDATE metric_rollups_hourly
		SET event_count=2,value_numeric=2
		WHERE metric_family='backup_restore_fixture'
	`); err != nil {
		t.Fatalf("mutate source after backup: %v", err)
	}

	// Success path: RestoreVerify must pass and drop its temporary database.
	verify, err := operations.RestoreVerify(ctx, RestoreVerifyRequest{BackupID: backup.BackupID})
	if err != nil {
		t.Fatalf("RestoreVerify (success path): %v", err)
	}
	if verify.(RestoreVerifyResult).Status != "pass" {
		t.Fatalf("RestoreVerify status = %q, want pass", verify.(RestoreVerifyResult).Status)
	}
	if after := countRestoreDatabases(t, pool); after != before {
		t.Fatalf("restore_* databases after successful verify = %d, want %d (temporary db not dropped)", after, before)
	}

	// Checksum failure path: truncate the archive so its checksum no longer
	// matches the manifest. RestoreVerify must reject it before creating any
	// restore database (so no orphan is possible on this path either).
	archivePath := restoreArchivePath(operations, backup.BackupID)
	info, err := os.Stat(archivePath)
	if err != nil {
		t.Fatalf("stat archive: %v", err)
	}
	if err := os.Truncate(archivePath, info.Size()/2); err != nil {
		t.Fatalf("truncate archive: %v", err)
	}
	if _, err := operations.RestoreVerify(ctx, RestoreVerifyRequest{BackupID: backup.BackupID}); err == nil {
		t.Fatal("RestoreVerify accepted an archive whose checksum no longer matches its manifest")
	}
	if after := countRestoreDatabases(t, pool); after != before {
		t.Fatalf("restore_* databases after checksum-mismatch verify = %d, want %d", after, before)
	}
}

func restoreArchivePath(operations *OperationsService, backupID string) string {
	return filepath.Join(operations.config.BackupDir, backupID+".dump")
}

// countRestoreDatabases counts residual restore_* databases so the test can
// assert dropRestoreDatabase left none behind on either path.
func countRestoreDatabases(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_database WHERE datname LIKE 'restore%'`).Scan(&count); err != nil {
		t.Fatalf("count restore databases: %v", err)
	}
	return count
}

// TestRestoreVerifyDropsTemporaryDatabaseWhenRestoreFails exercises the
// pg_restore failure branch specifically: a structurally valid custom-format
// archive whose checksum still matches its manifest, but whose body is
// truncated so pg_restore --exit-on-error fails after the restore database is
// already created. dropRestoreDatabase must still run, leaving no orphan.
func TestRestoreVerifyDropsTemporaryDatabaseWhenRestoreFails(t *testing.T) {
	dsn := testDSN(t)
	operations, pool, _, _ := operationsForBackup(t, dsn)
	ctx := context.Background()
	seedFactThroughHandoff(t, pool, "restore_fail_seed")

	backupResult, err := operations.Backup(ctx, BackupRequest{})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	backup := backupResult.(BackupResult)
	before := countRestoreDatabases(t, pool)

	// Corrupt the archive body so pg_restore --exit-on-error fails, then repoint
	// the manifest checksum at the mutated archive so the checksum gate passes
	// and control actually reaches CREATE DATABASE + pg_restore.
	archivePath := restoreArchivePath(operations, backup.BackupID)
	corruptCustomArchiveBody(t, archivePath)
	rewriteManifestChecksum(t, archivePath)

	if _, err := operations.RestoreVerify(ctx, RestoreVerifyRequest{BackupID: backup.BackupID}); err == nil {
		t.Fatal("RestoreVerify accepted a corrupted archive whose pg_restore must fail")
	}
	if after := countRestoreDatabases(t, pool); after != before {
		t.Fatalf("restore_* databases after pg_restore failure = %d, want %d (dropRestoreDatabase deferred cleanup must fire)", after, before)
	}
}

// corruptCustomArchiveBody overwrites bytes in the tail half of a custom-format
// pg_dump archive. The header/TOC at the front stays intact so pg_restore
// accepts the archive, creates its target database and begins restoring, then
// fails with --exit-on-error when it reaches the mangled data blocks -- which
// is exactly the branch that must still trigger dropRestoreDatabase.
func corruptCustomArchiveBody(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open archive for corruption: %v", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatalf("stat archive: %v", err)
	}
	if info.Size() < 512 {
		t.Fatalf("archive unexpectedly small (%d bytes); cannot corrupt only its body", info.Size())
	}
	garbage := make([]byte, info.Size()/2)
	for i := range garbage {
		garbage[i] = 0xff
	}
	if _, err := file.WriteAt(garbage, info.Size()/2); err != nil {
		t.Fatalf("overwrite archive body: %v", err)
	}
	if err := file.Sync(); err != nil {
		t.Fatalf("sync corrupted archive: %v", err)
	}
}

// rewriteManifestChecksum recomputes the archive sha256 after corruption and
// writes it back into the manifest, keeping the strict manifest schema intact,
// so RestoreVerify's checksum gate passes and control reaches pg_restore.
func rewriteManifestChecksum(t *testing.T, archivePath string) {
	t.Helper()
	manifestPath := archivePath + ".manifest.json"
	var manifest NativeBackupManifest
	if err := readStrictJSONFile(manifestPath, 1<<20, &manifest); err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	hash, err := fileSHA256(archivePath, 1<<40)
	if err != nil {
		t.Fatalf("rehash corrupted archive: %v", err)
	}
	manifest.ArchiveSHA256 = hash
	if err := os.Remove(manifestPath); err != nil {
		t.Fatalf("remove manifest before rewrite: %v", err)
	}
	if err := writePrivateJSON(manifestPath, manifest); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}
}

// --- (c) DurableIngressQueue reservation -> commit against real handoff ------

// TestDurableQueueReserveCommitAgainstRealHandoff proves the production
// admission path: a reservation is taken before commit, Commit routes through
// the real dataplatform.NewObservabilityHandoff (not a fake sink), a durable
// fact/evidence pair actually lands in PostgreSQL, a duplicate replay commits
// again with zero fact inflation (replay_count increments instead), and the
// spool stays empty because PostgreSQL owned every record.
func TestDurableQueueReserveCommitAgainstRealHandoff(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()

	handoff, err := dataplatform.NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("NewObservabilityHandoff: %v", err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	mustPrivateSpoolDir(t, dataDir)
	queue, err := NewDurableIngressQueue(handoff, dataDir, 64, 64<<20)
	if err != nil {
		t.Fatalf("NewDurableIngressQueue: %v", err)
	}
	defer queue.Close()

	event, evidence := integrationFact("queue_commit")

	reservation, err := queue.ReserveNormalizedFact(event, evidence)
	if err != nil {
		t.Fatalf("ReserveNormalizedFact: %v", err)
	}
	// A reservation must occupy a lane slot before commit.
	metrics, err := queue.Metrics()
	if err != nil {
		t.Fatalf("Metrics after reserve: %v", err)
	}
	if metrics.Depth[observability.SourceHook] != 1 {
		t.Fatalf("hook lane depth after reserve = %d, want 1", metrics.Depth[observability.SourceHook])
	}
	if err := reservation.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var factCount int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE fact_key=$1`, event.FactKey).Scan(&factCount); err != nil {
		t.Fatalf("count committed fact: %v", err)
	}
	if factCount != 1 {
		t.Fatalf("committed fact count = %d, want exactly 1 in real PostgreSQL", factCount)
	}

	// Duplicate replay: same fact/evidence, commits again, no inflation.
	dupReservation, err := queue.ReserveNormalizedFact(event, evidence)
	if err != nil {
		t.Fatalf("ReserveNormalizedFact (duplicate): %v", err)
	}
	if err := dupReservation.Commit(); err != nil {
		t.Fatalf("Commit (duplicate): %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE fact_key=$1`, event.FactKey).Scan(&factCount); err != nil {
		t.Fatalf("recount fact after duplicate: %v", err)
	}
	if factCount != 1 {
		t.Fatalf("fact count after duplicate replay = %d, want 1 (no inflation)", factCount)
	}
	var replayCount int64
	if err := pool.QueryRow(ctx, `SELECT replay_count FROM event_evidence WHERE evidence_id=$1`, evidence.EvidenceID).Scan(&replayCount); err != nil {
		t.Fatalf("read replay_count: %v", err)
	}
	if replayCount != 1 {
		t.Fatalf("replay_count = %d, want 1 after one duplicate", replayCount)
	}

	// Both commits went to PostgreSQL, so the sanitized spool must be empty and
	// the metrics must show two accepted, zero spooled.
	finalMetrics, err := queue.Metrics()
	if err != nil {
		t.Fatalf("final Metrics: %v", err)
	}
	if finalMetrics.Accepted != 2 || finalMetrics.Spooled != 0 {
		t.Fatalf("metrics accepted/spooled = %d/%d, want 2/0", finalMetrics.Accepted, finalMetrics.Spooled)
	}
	for _, source := range productionSources {
		if depth := finalMetrics.Depth[source]; depth != 0 {
			t.Fatalf("lane %s depth after commits = %d, want 0", source, depth)
		}
	}
}

// --- shared fact builders ----------------------------------------------------

// integrationFact builds a minimal but fully valid Event/Evidence pair the
// ObservabilityHandoff can persist: closed enum values only, one durable
// duration measurement, native evidence tier.
func integrationFact(suffix string) (observability.Event, observability.Evidence) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	success := true
	duration := int64(123)
	event := observability.Event{
		SpecVersion:      observability.EventSpecVersion,
		EventID:          "evt_" + suffix,
		FactKey:          "fact_" + suffix,
		EventType:        "component.executed",
		EmittedAt:        now,
		ObservedAt:       now,
		IngestedAt:       now.Add(time.Millisecond),
		TimestampQuality: "source_rfc3339",
		Source: observability.SourceRef{
			AdapterID: "fixture-agent", AdapterVersion: "1.0.0",
			Kind: observability.SourceHook, InstallationID: "ain_" + suffix,
			NativeEventID: "native_" + suffix, Sequence: 1,
		},
		Subject:           observability.Subject{Kind: "tool", ComponentID: "inventory/tool-safe"},
		Measurements:      observability.Measurements{DurationMS: &duration, Success: &success},
		ValueState:        "observed",
		Outcome:           "succeeded",
		CorrelationStatus: observability.CorrelationExact,
	}
	evidence := observability.Evidence{
		EvidenceID: "evd_" + suffix, EventID: event.EventID,
		Source: event.Source, Tier: observability.TierNative,
		Confidence: 1.0, Completeness: observability.Complete,
		FirstSeenAt: now, LastSeenAt: now,
		Sanitizer:     "integration-sanitizer/1",
		PrivacySHA256: "integrationcontractsha256",
		Assertion: observability.EvidenceAssertion{
			EventType: "component.executed", Outcome: "succeeded", ValueState: "observed",
		},
	}
	return event, evidence
}

// seedFactThroughHandoff commits one real fact directly through the production
// handoff so the backup covers non-empty fact/evidence tables.
func seedFactThroughHandoff(t *testing.T, pool *pgxpool.Pool, suffix string) {
	t.Helper()
	handoff, err := dataplatform.NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatalf("seed handoff: %v", err)
	}
	event, evidence := integrationFact(suffix)
	if err := handoff.PersistNormalizedFact(event, evidence); err != nil {
		t.Fatalf("seed PersistNormalizedFact: %v", err)
	}
}
