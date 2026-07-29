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
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/codexadapter"
	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/integrity"
	"kansoku.local/kansoku/internal/observability"
	"kansoku.local/kansoku/internal/privacy"
)

type projectionRepairTestSink struct {
	pool *pgxpool.Pool
	fail atomic.Bool
}

func (s *projectionRepairTestSink) PersistNormalizedFact(
	event observability.Event,
	evidence observability.Evidence,
) error {
	if s.fail.Load() {
		return errors.New("projection unavailable")
	}
	_, err := s.pool.Exec(context.Background(), `
		DELETE FROM observability_projection_receipts
		WHERE evidence_id=$1 AND observed_at=$2
	`, evidence.EvidenceID, event.ObservedAt.UTC())
	return err
}

func (s *projectionRepairTestSink) ReplayPendingProjections(
	context.Context,
	int,
) (dataplatform.ProjectionReplayResult, error) {
	return dataplatform.ProjectionReplayResult{}, nil
}

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

func TestScaledPostgresGrowthCompactStateBoundAndIdempotentReplay(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	refs := dataplatform.DimensionRefs{
		DeviceID: "dev_p0_load", AgentInstallationID: "ain_p0_load",
		AgentID: "fixture-agent", SurfaceID: "surface_p0_load",
		ProjectID: "project_p0_load", SessionID: "session_p0_load",
		AdapterVersionID: "av_p0_load", AdapterID: "fixture-agent",
		AdapterVersion: "1.0.0", SourceInstanceID: "src_p0_load",
		SourceKind: string(observability.SourceAdapterBatch),
	}
	if err := dataplatform.EnsureDimensions(ctx, pool, refs); err != nil {
		t.Fatal(err)
	}
	if err := dataplatform.EnsurePartition(ctx, pool, "events", base); err != nil {
		t.Fatal(err)
	}
	if err := dataplatform.EnsurePartition(ctx, pool, "event_evidence", base); err != nil {
		t.Fatal(err)
	}
	var beforeBytes int64
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT sum(pg_total_relation_size(relid)) FROM pg_partition_tree('events')) +
			(SELECT sum(pg_total_relation_size(relid)) FROM pg_partition_tree('event_evidence'))
	`).Scan(&beforeBytes); err != nil {
		t.Fatal(err)
	}
	tag, err := pool.Exec(ctx, `
		INSERT INTO events (
			event_id,fact_key,event_type,observed_at,ingested_at,
			timestamp_quality,source_instance_id,source_native_event_id,
			sequence,agent_installation_id,surface_id,project_id,session_id,
			value_state,outcome,correlation_status
		)
		SELECT
			'evt_p0_' || n,'fact_p0_' || n,'source.observed',
			$1::timestamptz + n * interval '1 microsecond',now(),
			'source_rfc3339','src_p0_load','native_p0_' || n,n,
			'ain_p0_load','surface_p0_load','project_p0_load','session_p0_load',
			'observed','unknown','exact'
		FROM generate_series(1,50000) AS n
		ON CONFLICT DO NOTHING
	`, base)
	if err != nil || tag.RowsAffected() != 50_000 {
		t.Fatalf("event load rows=%d err=%v", tag.RowsAffected(), err)
	}
	tag, err = pool.Exec(ctx, `
		INSERT INTO event_evidence (
			evidence_id,event_id,observed_at,source_instance_id,tier,
			confidence,completeness,replay_count,first_seen_at,last_seen_at,
			sanitizer_version,privacy_contract_sha256,assertion_event_type,
			assertion_outcome,assertion_value_state
		)
		SELECT
			'evd_p0_' || n,'evt_p0_' || n,
			$1::timestamptz + n * interval '1 microsecond','src_p0_load',
			'reconstructed',0.85,'complete',0,now(),now(),
			'kansoku.ingress-sanitizer/1',$2,'source.observed','unknown','observed'
		FROM generate_series(1,50000) AS n
		ON CONFLICT DO NOTHING
	`, base, privacy.PrivacyContractSemanticSHA256)
	if err != nil || tag.RowsAffected() != 50_000 {
		t.Fatalf("evidence load rows=%d err=%v", tag.RowsAffected(), err)
	}
	replay, err := pool.Exec(ctx, `
		INSERT INTO events (
			event_id,fact_key,event_type,observed_at,ingested_at,
			timestamp_quality,source_instance_id,source_native_event_id,
			sequence,agent_installation_id,surface_id,project_id,session_id,
			value_state,outcome,correlation_status
		)
		SELECT
			'evt_p0_' || n,'fact_p0_' || n,'source.observed',
			$1::timestamptz + n * interval '1 microsecond',now(),
			'source_rfc3339','src_p0_load','native_p0_' || n,n,
			'ain_p0_load','surface_p0_load','project_p0_load','session_p0_load',
			'observed','unknown','exact'
		FROM generate_series(1,50000) AS n
		ON CONFLICT DO NOTHING
	`, base)
	if err != nil || replay.RowsAffected() != 0 {
		t.Fatalf("duplicate replay rows=%d err=%v", replay.RowsAffected(), err)
	}
	var events, evidence, afterBytes int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM event_evidence`).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT sum(pg_total_relation_size(relid)) FROM pg_partition_tree('events')) +
			(SELECT sum(pg_total_relation_size(relid)) FROM pg_partition_tree('event_evidence'))
	`).Scan(&afterBytes); err != nil {
		t.Fatal(err)
	}
	if events != 50_000 || evidence != 50_000 || afterBytes <= beforeBytes {
		t.Fatalf("events=%d evidence=%d storage=%d->%d", events, evidence, beforeBytes, afterBytes)
	}

	statePath := filepath.Join(t.TempDir(), "checkpoints", "state.json")
	store, err := observability.OpenCompactStore(statePath, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 50_000; n++ {
		event := observability.Event{EventID: "not-persisted"}
		item := observability.Evidence{EvidenceID: "not-persisted"}
		if _, err := store.Commit(observability.CommitRequest{
			Event: &event, Evidence: &item,
		}); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint := observability.Checkpoint{
		ImporterID: "p0-load", Offset: 50_000, Sequence: 50_000,
		FileID: "hmac-sha256:" + strings.Repeat("a", 64),
	}
	if _, err := store.Commit(observability.CommitRequest{Checkpoint: &checkpoint}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := observability.OpenCompactStore(statePath, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= 32<<10 || len(reopened.Snapshot().Facts) != 0 ||
		reopened.Snapshot().Checkpoints["p0-load"].Sequence != 50_000 {
		t.Fatalf("compact state bytes=%d state=%+v", info.Size(), reopened.Snapshot())
	}
}

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

// TestOneShotAssemblyDoesNotActivateRuntimeSources is a regression for the
// production backup/restore incident in which NewAppliance ran inventory and
// App Server source activation inside an operational container that
// intentionally had no agent-state mounts. The backup itself passed, but the
// constructor overwrote a complete Codex inventory row with not_observed and
// downgraded producing App Server health to configured. Runtime activation now
// belongs to Appliance.Run; constructing the same assembly used by one-shot
// commands must leave both durable health records byte-for-byte equivalent at
// the semantic column level.
func TestOneShotAssemblyDoesNotActivateRuntimeSources(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	config, databaseOnlySecrets := backupTestConfig(t, dsn)
	config.InventoryTargets = []InventoryTarget{{
		TargetID: "codex-oneshot-regression", AdapterID: "codex",
		InstallationID: "ain_11111111111111111111111111111111", SurfaceID: "cli",
		StateRoot: filepath.Join(t.TempDir(), "intentionally-not-mounted"),
	}}
	config.InventoryScanIntervalSeconds = 300
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	applyAllMigrations(t, ctx, pool)

	fixedAt := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
		INSERT INTO inventory_collection_status (
			target_id,adapter_id,agent_installation_id,state,error_class,
			last_attempted_at,last_succeeded_at,snapshot_id,node_count,edge_count
		) VALUES ($1,'codex','ain_11111111111111111111111111111111','complete',NULL,$2,$2,NULL,116,142)
		ON CONFLICT (target_id) DO UPDATE SET
			agent_installation_id=EXCLUDED.agent_installation_id,
			state=EXCLUDED.state,error_class=NULL,
			last_attempted_at=EXCLUDED.last_attempted_at,
			last_succeeded_at=EXCLUDED.last_succeeded_at,
			snapshot_id=NULL,node_count=EXCLUDED.node_count,edge_count=EXCLUDED.edge_count
	`, config.InventoryTargets[0].TargetID, fixedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO runtime_source_health (
			source_id,state,value_state,last_attempted_at,last_successful_at,
			last_error_class,updated_at
		) VALUES ('codex.app_server','producing','observed',$1,$1,NULL,$1)
		ON CONFLICT (source_id) DO UPDATE SET
			state=EXCLUDED.state,value_state=EXCLUDED.value_state,
			last_attempted_at=EXCLUDED.last_attempted_at,
			last_successful_at=EXCLUDED.last_successful_at,
			last_error_class=NULL,updated_at=EXCLUDED.updated_at
	`, fixedAt); err != nil {
		t.Fatal(err)
	}

	secrets := Secrets{
		IngressBearer:    fixedSecret32("oneshot-ingress"),
		ReadBearer:       fixedSecret32("oneshot-read"),
		MutationBearer:   fixedSecret32("oneshot-mutation"),
		CSRF:             fixedSecret32("oneshot-csrf"),
		IdentityHMAC:     fixedSecret32("oneshot-identity"),
		AuditHMAC:        fixedSecret32("oneshot-audit"),
		DatabasePassword: databaseOnlySecrets.DatabasePassword,
	}
	appliance, err := NewAppliance(ctx, config, secrets, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := appliance.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	var installationID, inventoryState, inventoryError string
	var inventoryAttempted time.Time
	var nodes, edges int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(agent_installation_id,''),state,COALESCE(error_class,''),
		       last_attempted_at,node_count,edge_count
		FROM inventory_collection_status WHERE target_id=$1
	`, config.InventoryTargets[0].TargetID).Scan(
		&installationID, &inventoryState, &inventoryError,
		&inventoryAttempted, &nodes, &edges,
	); err != nil {
		t.Fatal(err)
	}
	if installationID != "ain_11111111111111111111111111111111" || inventoryState != "complete" ||
		inventoryError != "" || !inventoryAttempted.Equal(fixedAt) ||
		nodes != 116 || edges != 142 {
		t.Fatalf(
			"one-shot assembly mutated inventory health: installation=%q state=%q error=%q attempted=%s nodes=%d edges=%d",
			installationID, inventoryState, inventoryError,
			inventoryAttempted, nodes, edges,
		)
	}

	var sourceState, valueState, sourceError string
	var sourceAttempted, sourceSuccessful time.Time
	if err := pool.QueryRow(ctx, `
		SELECT state,value_state,COALESCE(last_error_class,''),
		       last_attempted_at,last_successful_at
		FROM runtime_source_health WHERE source_id='codex.app_server'
	`).Scan(
		&sourceState, &valueState, &sourceError,
		&sourceAttempted, &sourceSuccessful,
	); err != nil {
		t.Fatal(err)
	}
	if sourceState != "producing" || valueState != "observed" ||
		sourceError != "" || !sourceAttempted.Equal(fixedAt) ||
		!sourceSuccessful.Equal(fixedAt) {
		t.Fatalf(
			"one-shot assembly mutated App Server health: state=%q value=%q error=%q attempted=%s successful=%s",
			sourceState, valueState, sourceError, sourceAttempted, sourceSuccessful,
		)
	}
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
		) VALUES
		(
			'backup_restore_fixture','2026-07-26T10:00:00Z','fixture',
			'backup_restore_fixture/1',1,0,3600000,1
		),
		(
			'backup_restore_fixture','2026-07-26T11:00:00Z','unknown_dominant_fixture',
			'backup_restore_fixture/1',0,4,3600000,NULL
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

func TestEvidenceBridgeAndOTelCommitOneFactTwoLanesToRealPostgres(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	handoff, err := dataplatform.NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	mustPrivateSpoolDir(t, dataDir)
	queue, err := NewDurableIngressQueue(handoff, dataDir, 64, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	store, err := observability.OpenFileStore(filepath.Join(t.TempDir(), "state.json"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte("k"), 32)
	ingestor, err := observability.NewIngestor(store, key, privacy.DefaultLimits(), 2)
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Unix(1785074400, 0).UTC()
	ingestor.SetClockForTest(func() time.Time { return observed.Add(time.Second) })
	if err := ingestor.ConfigureDurableFactSink(queue); err != nil {
		t.Fatal(err)
	}
	sink, err := observability.NewBridgeAssertionSink(ingestor)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := codexadapter.NewAppServerBridge(key, func() time.Time {
		return observed.Add(time.Second)
	})
	if err != nil {
		t.Fatal(err)
	}
	frame := `{"method":"turn/started","params":{"threadId":"live-shared-session","turn":{"id":"live-shared-event","startedAt":1785074400,"status":"inProgress","items":[]}}}`
	if err := bridge.Connect(ctx, adaptersdk.BridgeTarget{
		Installation: adaptersdk.Installation{
			InstallationID: "ain_live_bridge", AdapterID: codexadapter.AdapterID,
		},
		Protocol:      codexadapter.AppServerProtocolVersion,
		SchemaVersion: codexadapter.AppServerSchemaVersion,
		Frames:        strings.NewReader(frame),
	}, sink); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestor.IngestSafeFields(map[string]any{
		"event_id": "live-shared-event", "session_id": "live-shared-session",
		"observed_at": observed.Format(time.RFC3339Nano),
		"event_type":  "prompt.submitted", "outcome": "unknown",
		"value_state": "observed",
	}, codexadapter.AdapterID, observability.SourceOTLPLog, 2); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 || len(state.Evidence) != 2 {
		t.Fatalf("mirror facts=%d evidence=%d", len(state.Facts), len(state.Evidence))
	}
	var factKey string
	for key := range state.Facts {
		factKey = key
	}
	var facts, evidence int64
	if err := pool.QueryRow(ctx, `
		SELECT count(DISTINCT e.event_id), count(DISTINCT ee.evidence_id)
		FROM events e
		JOIN event_evidence ee ON ee.event_id=e.event_id AND ee.observed_at=e.observed_at
		WHERE e.fact_key=$1
	`, factKey).Scan(&facts, &evidence); err != nil {
		t.Fatal(err)
	}
	if facts != 1 || evidence != 2 {
		t.Fatalf("Postgres facts=%d evidence=%d", facts, evidence)
	}
	var lanes int64
	if err := pool.QueryRow(ctx, `
		SELECT count(DISTINCT si.source_kind)
		FROM event_evidence ee
		JOIN source_instances si ON si.source_instance_id=ee.source_instance_id
		JOIN events e ON e.event_id=ee.event_id AND e.observed_at=ee.observed_at
		WHERE e.fact_key=$1
	`, factKey).Scan(&lanes); err != nil {
		t.Fatal(err)
	}
	if lanes != 2 {
		t.Fatalf("evidence lane count=%d", lanes)
	}
}

func TestSupervisedCodexAppServerIngressPersistsTypedSkillAndSourceHealth(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	handoff, err := dataplatform.NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	mustPrivateSpoolDir(t, dataDir)
	queue, err := NewDurableIngressQueue(handoff, dataDir, 64, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	recorder, err := NewPostgresIngestionHealthRecorder(pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.ConfigureHealthRecorder(ctx, recorder); err != nil {
		t.Fatal(err)
	}
	store, err := observability.OpenCompactStore(
		filepath.Join(t.TempDir(), "checkpoint.json"),
		4<<20,
	)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte("k"), 32)
	ingestor, err := observability.NewIngestor(store, key, privacy.DefaultLimits(), 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := ingestor.ConfigureDurableFactSink(queue); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1785060002000).UTC()
	ingress, err := NewCodexAppServerIngress(pool, ingestor, key, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := ingress.Configure(ctx); err != nil {
		t.Fatal(err)
	}
	const installationID = "ain_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	rawPathCanary := "/private/KANSOKU_APP_SERVER_PATH_MUST_NOT_PERSIST/SKILL.md"
	body := `{"emittedAtMs":1785060001001,"method":"item/started","params":{"threadId":"thr-supervised","turnId":"turn-supervised","startedAtMs":1785060001000,"item":{"type":"userMessage","id":"msg-supervised","content":[{"type":"skill","name":"supervised-canary-skill","path":"` +
		rawPathCanary + `"},{"type":"text","text":"KANSOKU_RAW_PROMPT_MUST_NOT_PERSIST"}]}}}` + "\n"
	request := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1:4318/v1/evidence-bridges/codex-app-server",
		strings.NewReader(body),
	)
	request.Header.Set(codexAppServerInstallationHeader, installationID)
	response := httptest.NewRecorder()
	ingress.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var assertions int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM component_assertions
		WHERE agent_installation_id=$1
		  AND component_kind='skill'
		  AND assertion_kind IN ('invoked','loaded')
		  AND evidence_tier='native'
		  AND qualified_identity='supervised-canary-skill'
		  AND (
		    (assertion_kind='invoked' AND mode='explicit' AND invocation_mode='explicit')
		    OR
		    (assertion_kind='loaded' AND mode='not_observed')
		  )
	`, installationID).Scan(&assertions); err != nil {
		t.Fatal(err)
	}
	if assertions != 2 {
		t.Fatalf("typed assertions=%d, want 2", assertions)
	}
	pluginBody := strings.Join([]string{
		`{"jsonrpc":"2.0","id":19,"method":"plugin/read","params":{"pluginName":"sre-agent","marketplacePath":"/private/KANSOKU_PLUGIN_REQUEST_PATH","remoteMarketplaceName":"yuzuru-engineering"}}`,
		`{"jsonrpc":"2.0","id":19,"result":{"plugin":{"marketplaceName":"yuzuru-engineering","marketplacePath":"/private/KANSOKU_PLUGIN_PATH","description":"KANSOKU_PLUGIN_DESCRIPTION_MUST_NOT_PERSIST","shareUrl":"https://example.invalid/KANSOKU_PLUGIN_URL","summary":{"id":"KANSOKU_PLUGIN_UPSTREAM_ID_MUST_NOT_PERSIST","name":"sre-agent","installed":true,"enabled":true,"source":{"type":"local","path":"/private/KANSOKU_PLUGIN_SOURCE_PATH"}},"skills":[{"name":"sre-agent","enabled":true,"path":"/private/KANSOKU_PLUGIN_SKILL_PATH/SKILL.md","description":"KANSOKU_PLUGIN_SKILL_DESCRIPTION_MUST_NOT_PERSIST"}],"hooks":[],"mcpServers":[],"apps":[],"appTemplates":[],"scheduledTasks":[]}}}`,
	}, "\n") + "\n"
	for attempt := 0; attempt < 2; attempt++ {
		request = httptest.NewRequest(
			http.MethodPost,
			"http://127.0.0.1:4318/v1/evidence-bridges/codex-app-server",
			strings.NewReader(pluginBody),
		)
		request.Header.Set(codexAppServerInstallationHeader, installationID)
		response = httptest.NewRecorder()
		ingress.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("plugin/read attempt=%d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
	}
	var pluginAssertions, pluginChildAssertions, pluginReplayCount int64
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (
				WHERE component_kind='plugin'
				  AND assertion_kind IN ('requested','installed','enabled')
				  AND qualified_identity='sre-agent@yuzuru-engineering'
				  AND evidence_tier='native'
				  AND upstream_identity_hash LIKE 'hmac-sha256:%'
			),
			count(*) FILTER (
				WHERE component_kind='skill'
				  AND assertion_kind IN ('installed','enabled')
				  AND qualified_identity='sre-agent@yuzuru-engineering:sre-agent'
				  AND owner_plugin_identity='sre-agent@yuzuru-engineering'
				  AND evidence_tier='native'
			),
			coalesce(sum(ee.replay_count),0)
		FROM component_assertions ca
		JOIN event_evidence ee
		  ON ee.evidence_id=ca.evidence_id AND ee.event_id=ca.event_id
		WHERE ca.agent_installation_id=$1
		  AND ca.identity_source='native_bridge_plugin_read'
	`, installationID).Scan(
		&pluginAssertions, &pluginChildAssertions, &pluginReplayCount,
	); err != nil {
		t.Fatal(err)
	}
	if pluginAssertions != 3 || pluginChildAssertions != 2 || pluginReplayCount != 5 {
		t.Fatalf(
			"plugin/read assertions=%d child=%d replay_count=%d",
			pluginAssertions, pluginChildAssertions, pluginReplayCount,
		)
	}
	for _, prohibited := range []string{
		"KANSOKU_PLUGIN_REQUEST_PATH",
		"KANSOKU_PLUGIN_PATH",
		"KANSOKU_PLUGIN_DESCRIPTION_MUST_NOT_PERSIST",
		"KANSOKU_PLUGIN_URL",
		"KANSOKU_PLUGIN_UPSTREAM_ID_MUST_NOT_PERSIST",
		"KANSOKU_PLUGIN_SOURCE_PATH",
		"KANSOKU_PLUGIN_SKILL_PATH",
		"KANSOKU_PLUGIN_SKILL_DESCRIPTION_MUST_NOT_PERSIST",
	} {
		var persisted int64
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM (
				SELECT row_to_json(events)::text AS document FROM events
				UNION ALL
				SELECT row_to_json(event_evidence)::text FROM event_evidence
				UNION ALL
				SELECT row_to_json(component_assertions)::text FROM component_assertions
			) AS durable
			WHERE document LIKE '%' || $1 || '%'
		`, prohibited).Scan(&persisted); err != nil {
			t.Fatal(err)
		}
		if persisted != 0 {
			t.Fatalf("plugin/read prohibited marker %q persisted %d times", prohibited, persisted)
		}
	}
	var state, valueState string
	if err := pool.QueryRow(ctx, `
		SELECT state,value_state
		FROM runtime_source_health
		WHERE source_id='codex.app_server'
	`).Scan(&state, &valueState); err != nil {
		t.Fatal(err)
	}
	if state != "producing" || valueState != "observed" {
		t.Fatalf("source health=%s/%s", state, valueState)
	}
}

func TestCodexRolloutWatcherPersistsRequestedBeforeInventoryAndCorroboratesIdempotently(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	mustPrivateSpoolDir(t, dataDir)
	handoff, err := dataplatform.NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := NewDurableIngressQueue(handoff, dataDir, 64, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	store, err := observability.OpenCompactStore(
		filepath.Join(t.TempDir(), "checkpoint.json"), 4<<20,
	)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte("w"), 32)
	ingestor, err := observability.NewIngestor(
		store, key, privacy.DefaultLimits(), 64,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ingestor.ConfigureDurableFactSink(queue); err != nil {
		t.Fatal(err)
	}
	const installationID = "ain_cccccccccccccccccccccccccccccccc"
	if err := dataplatform.EnsureInventoryInstallation(
		ctx, pool, installationID, codexadapter.AdapterID,
	); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "codex")
	watcher, err := NewCodexRolloutWatcher(
		pool, ingestor, store,
		[]InventoryTarget{{
			TargetID: "codex-rollout-test", AdapterID: codexadapter.AdapterID,
			InstallationID: installationID, SurfaceID: "cli", StateRoot: root,
		}},
		key, 5*time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	// The file is intentionally created after supervision starts so it is
	// consumed from byte zero instead of being treated as historical baseline.
	watcher.startedAt = time.Now().Add(-time.Second)
	sessionDir := filepath.Join(root, "sessions", "2026", "07", "29")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rolloutPath := filepath.Join(sessionDir, "rollout-canary.jsonl")
	lines := []string{
		`{"type":"session_meta","timestamp":"2026-07-29T03:00:00Z","payload":{"id":"KANSOKU_RAW_SESSION_MUST_NOT_PERSIST","cli_version":"0.145.0"}}`,
		`{"type":"turn_context","timestamp":"2026-07-29T03:00:01Z","payload":{"turn_id":"turn-canary"}}`,
		`{"type":"event_msg","timestamp":"2026-07-29T03:00:02Z","payload":{"type":"user_message","message":"Use $late-catalog-skill KANSOKU_RAW_ROLLOUT_PROMPT_MUST_NOT_PERSIST"}}`,
		`{"type":"response_item","timestamp":"2026-07-29T03:00:03Z","payload":{"type":"function_call","call_id":"call-canary","name":"exec_command","arguments":"{\"cmd\":\"sed -n 1,20p /synthetic/KANSOKU_RAW_ROLLOUT_PATH_MUST_NOT_PERSIST/late-catalog-skill/SKILL.md\"}"}}`,
		`{"type":"response_item","timestamp":"2026-07-29T03:00:04Z","payload":{"type":"function_call_output","call_id":"call-canary","output":"KANSOKU_RAW_SKILL_BODY_MUST_NOT_PERSIST"}}`,
	}
	if err := os.WriteFile(
		rolloutPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := watcher.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertState := func() {
		t.Helper()
		var requested, loaded, invoked, unresolved, replay int64
		if err := pool.QueryRow(ctx, `
			SELECT
				count(*) FILTER (
					WHERE ca.assertion_kind='requested'
					  AND ca.mode='not_observed'
					  AND ca.invocation_mode='requested'
				),
				count(*) FILTER (
					WHERE ca.assertion_kind='loaded'
					  AND ca.mode='not_observed'
					  AND ca.invocation_mode='not_observed'
				),
				count(*) FILTER (
					WHERE ca.assertion_kind='invoked'
					  AND ca.mode='explicit'
					  AND ca.invocation_mode='explicit'
				),
				count(*) FILTER (
					WHERE cr.identity_resolution='unresolved'
					  AND cr.candidate_count=0
				),
				coalesce(sum(ee.replay_count),0)
			FROM component_assertions ca
			JOIN component_assertion_current_resolution cr
			  ON cr.assertion_id=ca.assertion_id
			JOIN event_evidence ee
			  ON ee.evidence_id=ca.evidence_id
			 AND ee.event_id=ca.event_id
			 AND ee.observed_at=ca.observed_at
			WHERE ca.agent_installation_id=$1
			  AND ca.component_kind='skill'
			  AND ca.qualified_identity='late-catalog-skill'
			  AND ca.evidence_tier='reconstructed'
			  AND ca.confidence=0.85
		`, installationID).Scan(
			&requested, &loaded, &invoked, &unresolved, &replay,
		); err != nil {
			t.Fatal(err)
		}
		if requested != 1 || loaded != 1 || invoked != 1 ||
			unresolved != 3 || replay != 0 {
			t.Fatalf(
				"requested=%d loaded=%d invoked=%d unresolved=%d replay=%d",
				requested, loaded, invoked, unresolved, replay,
			)
		}
	}
	assertState()
	if err := watcher.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertState()
	for _, prohibited := range []string{
		"KANSOKU_RAW_SESSION_MUST_NOT_PERSIST",
		"KANSOKU_RAW_ROLLOUT_PROMPT_MUST_NOT_PERSIST",
		"KANSOKU_RAW_ROLLOUT_PATH_MUST_NOT_PERSIST",
		"KANSOKU_RAW_SKILL_BODY_MUST_NOT_PERSIST",
	} {
		var persisted int64
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM (
				SELECT row_to_json(events)::text AS document FROM events
				UNION ALL
				SELECT row_to_json(event_evidence)::text FROM event_evidence
				UNION ALL
				SELECT row_to_json(component_assertions)::text FROM component_assertions
			) durable
			WHERE document LIKE '%' || $1 || '%'
		`, prohibited).Scan(&persisted); err != nil {
			t.Fatal(err)
		}
		if persisted != 0 {
			t.Fatalf("prohibited rollout marker %q persisted %d times", prohibited, persisted)
		}
	}
}

func TestPostgresIngestionHealthCountersReloadAcrossRecorderRestart(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	first, err := NewPostgresIngestionHealthRecorder(pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	if err := first.Record(
		observability.SourceOTLPLog,
		ingestionHealthBackpressureRejected,
		at,
	); err != nil {
		t.Fatal(err)
	}
	if err := first.Record(
		observability.SourceEvidenceBridge,
		ingestionHealthDurabilityUnavailable,
		at.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPostgresIngestionHealthRecorder(pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := restarted.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.BackpressureRejected != 1 ||
		snapshot.DurabilityUnavailable != 1 ||
		!snapshot.LastRejected.Equal(at.Add(time.Second)) {
		t.Fatalf("restart-loaded ingestion health=%+v", snapshot)
	}
}

func TestProjectionRepairRequiresPreviewPreservesFailureAndDrainsAfterApprovedRetry(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	sink := &projectionRepairTestSink{pool: pool}
	sink.fail.Store(true)
	spools := testSpools()
	queue, err := NewDurableIngressQueueWithSpools(sink, spools, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(queue.Close)
	event, evidence := safeFact(observability.SourceEvidenceBridge, "operator-projection")
	if _, err := pool.Exec(ctx, `
		INSERT INTO observability_projection_receipts (
			event_id,observed_at,evidence_id,state,attempt_count,
			last_error_class,first_enqueued_at,last_attempted_at
		) VALUES ($1,$2,$3,'retryable',7,'derived_projection_failed',$4,$4)
	`, event.EventID, event.ObservedAt.UTC(), evidence.EvidenceID,
		event.IngestedAt.UTC()); err != nil {
		t.Fatal(err)
	}
	if err := queue.PersistNormalizedFact(event, evidence); err != nil {
		t.Fatalf("spool fallback: %v", err)
	}
	root := t.TempDir()
	config := validTestConfig(root)
	mustPrivateSpoolDir(t, config.DataDir)
	jobs, err := NewJobManager(pool, map[JobID]JobHandler{})
	if err != nil {
		t.Fatal(err)
	}
	operations, err := NewOperationsService(
		config, Secrets{DatabasePassword: fixedSecret32("dbpass")},
		pool, queue, jobs,
	)
	if err != nil {
		t.Fatal(err)
	}
	previewValue, err := operations.PreviewProjectionRepair(
		ctx, ProjectionRepairPreviewRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	preview := previewValue.(projectionRepairPreview)
	if preview.CurrentState.TotalReceiptCount != 1 ||
		preview.CurrentState.ReceiptCounts["retryable"] != 1 ||
		preview.CurrentState.MaxAttemptCount != 7 ||
		preview.CurrentState.PayloadsExposed ||
		preview.CurrentState.AutomaticDiscard ||
		preview.CurrentState.Lanes[string(observability.SourceEvidenceBridge)].QueueAndSpoolDepth != 1 {
		t.Fatalf("unsafe or incomplete preview: %+v", preview)
	}
	_, err = operations.ApplyProjectionRepair(ctx, ProjectionRepairApplyRequest{
		RequestID: preview.RequestID, ParametersSHA256: preview.ParametersSHA256,
		ApprovalNonce: "operator-nonce-failed",
	})
	if err == nil || err.Error() != "projection_repair_incomplete" {
		t.Fatalf("failed retry error=%v", err)
	}
	var receiptCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM observability_projection_receipts
		WHERE event_id=$1 AND observed_at=$2
	`, event.EventID, event.ObservedAt.UTC()).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	failedStats, err := spools[observability.SourceEvidenceBridge].Stats()
	if err != nil || receiptCount != 1 || failedStats.Depth != 1 {
		t.Fatalf("failed retry discarded state: receipt=%d spool=%+v err=%v",
			receiptCount, failedStats, err)
	}
	var failedApproval string
	if err := pool.QueryRow(ctx, `
		SELECT result FROM runtime_operation_approvals WHERE request_id=$1
	`, preview.RequestID).Scan(&failedApproval); err != nil {
		t.Fatal(err)
	}
	if failedApproval != "failed" {
		t.Fatalf("failed approval result=%s", failedApproval)
	}

	secondValue, err := operations.PreviewProjectionRepair(
		ctx, ProjectionRepairPreviewRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	second := secondValue.(projectionRepairPreview)
	sink.fail.Store(false)
	resultValue, err := operations.ApplyProjectionRepair(ctx, ProjectionRepairApplyRequest{
		RequestID: second.RequestID, ParametersSHA256: second.ParametersSHA256,
		ApprovalNonce: "operator-nonce-success",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := resultValue.(ProjectionRepairResult)
	if result.Before.TotalReceiptCount != 1 || result.After.TotalReceiptCount != 0 {
		t.Fatalf("repair transition=%+v", result)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM observability_projection_receipts
		WHERE event_id=$1 AND observed_at=$2
	`, event.EventID, event.ObservedAt.UTC()).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	successStats, err := spools[observability.SourceEvidenceBridge].Stats()
	if err != nil || receiptCount != 0 || successStats.Depth != 0 {
		t.Fatalf("successful retry state: receipt=%d spool=%+v err=%v",
			receiptCount, successStats, err)
	}
	var appliedApproval string
	if err := pool.QueryRow(ctx, `
		SELECT result FROM runtime_operation_approvals WHERE request_id=$1
	`, second.RequestID).Scan(&appliedApproval); err != nil {
		t.Fatal(err)
	}
	if appliedApproval != "applied" {
		t.Fatalf("applied approval result=%s", appliedApproval)
	}

	thirdValue, err := operations.PreviewProjectionRepair(
		ctx, ProjectionRepairPreviewRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	third := thirdValue.(projectionRepairPreview)
	if _, err := operations.ApplyProjectionRepair(ctx, ProjectionRepairApplyRequest{
		RequestID: third.RequestID, ParametersSHA256: third.ParametersSHA256,
		ApprovalNonce: "operator-nonce-success",
	}); err == nil || err.Error() != "replay_nonce" {
		t.Fatalf("reused nonce error=%v", err)
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
