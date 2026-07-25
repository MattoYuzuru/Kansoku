# Session 09 reconciliation — local runtime, appliance and operations

Date: 2026-07-24

Status: implementation and runtime validation complete. The Session 09 appliance was assembled,
the previously fakes-only `internal/runtime` claims are now backed by a real pinned-PostgreSQL 18
integration suite, and the accelerated soak that ADR 0012 decision 9 requires was executed for real
against a live Docker-Compose appliance stack — 168 logical cycles across three real restart faults,
all seven durability assertions passing. No aggregate seven-day wall-clock claim is made; the run is
recorded as an accelerated logical-cycle harness exactly as the contract requires.

## Scope reconciled

- `cmd/kansoku` assembles one appliance process over one `pgxpool.Pool`, one observability
  ingestor/OTLP receiver, one runtime API, one operations worker set and one integrity assembly,
  reusing Sessions 02–08 components without a second event schema, store or installer protocol.
- Strict versioned JSON config, file-only pairwise-distinct secrets, loopback/container-bridge HTTP
  policy, the reservation-capable durable ingress queue, separated ingress/read/mutation bearers,
  PostgreSQL row-lease jobs, native `pg_dump`/`pg_restore` backup with isolated restore verification,
  deterministic diagnostics and bounded shutdown were already implemented and contract-locked before
  this session's closing pass; the four `contracts/runtime/*.yaml` registries and
  `contracts/runtime-policy-locks.yaml` remain byte-identical and pass their authoritative digest
  checks unchanged (they were not edited).

## What this closing pass added

- `internal/runtime/postgres_integration_test.go` (build tag `postgres_integration`): the first tests
  that exercise `internal/runtime` against a real Postgres 18 instead of fakes/mocks. It covers
  (a) job leases — acquisition, an `already_running` contender blocked by the partial unique lease
  index while a live lease is held, terminal `passed` with the lease cleared, `RecoverInterrupted`
  reclaiming a stale expired lease as `interrupted`, and a fresh post-recovery run succeeding;
  (b) a full `Backup` → `RestoreVerify` round trip through real `pg_dump --format=custom` /
  `pg_restore` into a brand-new randomly named database, including the count/constraint/migration-
  ledger/formula-lineage checks, plus a checksum-mismatch rejection and a separate corrupted-archive
  `pg_restore` failure that both prove `dropRestoreDatabase` leaves no orphaned `restore_*` database
  on either the success or failure path; (c) `DurableIngressQueue` reserve→commit against a real
  `dataplatform.NewObservabilityHandoff` (not a fake sink), proving a fact lands durably in
  PostgreSQL, a duplicate replay commits again with zero fact inflation (replay_count increments),
  and the sanitized spools stay empty because PostgreSQL owned every record.
- `scripts/validate_runtime.py` gained `run_postgres_integration_suite()` plus `--runtime-only`,
  mirroring `scripts/validate_data_platform.py`'s pinned-digest / isolated-internal-network /
  readiness-poll / deterministic-teardown pattern. The only structural difference is a combined test
  image: because the runtime backup path shells to `pg_dump`/`pg_restore`, the harness layers the
  pinned Go 1.26 toolchain onto the pinned Postgres image filesystem (the same "postgres filesystem +
  static Go binary" strategy `deploy/Dockerfile` itself uses), so both the compiler and pg tools 18
  are on PATH inside one hermetic image. The ephemeral Postgres runs with `POSTGRES_DB`/`POSTGRES_USER
  == kansoku` so the strict runtime `Config` validates unchanged.
- `cmd/kansoku/soak.go` (`package main`, host-side): `dockerSoakDriver`, the real `SoakDriver`
  implementation ADR 0012 decision 9 requires. It replaces the fake-only mechanism: `Ingest` posts a
  unique fixture-agent hook fact through the real ingress HTTP surface, `QueryRollup` hits the real
  budgeted analytics route, `BackupCountSnapshot` triggers a real native backup, `ExecuteFault`
  issues real `docker restart <kansoku>` / `docker restart <postgres>` / `docker compose stop`+`start`
  operations, `Recover` polls the real health endpoint until healthy within a bounded deadline, and
  `IsDurable`/`Snapshot` read the running system (completeness, health queue depth, operations jobs,
  a final quiescent backup and diagnostics) to fill every assertion. All `docker` invocations are
  argv-only; `exec.Command` lives in `cmd/kansoku`, never in `internal/runtime`, so the appliance
  package keeps its no-shell/no-exec invariant. `cmd/kansoku soak` was rewired to build this driver
  and call the already-correct `runtime.RunAcceleratedSoakWithDriver` orchestration (unchanged). The
  in-appliance `runtime.RunAcceleratedSoak` remains a deliberate refusal — the harness supplies the
  real driver from outside the container, as its doc comment always intended.
- `scripts/validate_runtime.py` also gained `run_accelerated_soak()` plus a `--soak` flag: the
  host-side harness that builds the release image and the `kansoku` binary, generates a temporary
  run directory, secrets, config and Compose file, brings up the ephemeral stack, runs the real soak
  binary against it and tears everything down deterministically. A human runs it with
  `python3 scripts/validate_runtime.py --soak`.
- `scripts/session09_supply_chain.py` and `reports/session-09-sbom.json`: a deterministic Session 09
  CycloneDX SBOM over the Session 09 source scope, mirroring `scripts/session08_supply_chain.py`.

## Real soak evidence (all assertions passed)

`python3 scripts/validate_runtime.py --soak` produced, against a live Compose stack:

- `driver_kind`: `real_docker_compose_appliance` (not the in-memory fake).
- `cycles_completed`: 168; `logical_days`: 7; `cycles_per_day`: 24; `wall_clock_seven_day_claim`: false.
- `faults_executed`: `database_restart`, `process_restart`, `stop_the_world_upgrade_boundary` — each a
  real Docker operation at cycles 48/96/144, each followed by a real health-endpoint recovery poll.
- `acknowledged_count`/`unique_event_count`: 168/168 (every cycle ingested a unique fact).
- `final_snapshot`: `FactCount` 168, `EvidenceReplayCount` 0, `SpoolDepth` 0, `NonTerminalJobs` 0,
  `BackupCountsMatch` true, `DiagnosticsSafe` true.
- `assertions`: all seven true — `acknowledged_equals_durable_or_spooled`,
  `event_fact_count_no_inflation`, `replay_count_tracks_duplicates`,
  `backup_counts_match_source_snapshot`, `all_spools_empty_after_recovery`, `all_jobs_terminal`,
  `no_prohibited_diagnostics_fields`.
- Wall clock of the soak itself: ~2m00s (well under the "a few minutes" budget); only the three
  fault cycles perform real container restarts, the other 165 cycles are fast real HTTP round trips
  paced to stay under the appliance's 120-requests/minute-per-peer rate limit.
- Restart reality was independently confirmed: after the run both containers' `State.StartedAt`
  fell inside the soak's `started_at`/`finished_at` window (near the cycle-144 stop-the-world
  boundary), and all 168 facts remained durable in PostgreSQL afterwards — the facts survived the
  process restart, the database restart and the full stack stop/start without silent loss or
  duplicate inflation, which is precisely the roadmap exit gate.

## PostgreSQL integration results

`python3 scripts/validate_runtime.py --runtime-only` ran the tagged suite against the ephemeral
pinned Postgres 18 and passed (overall status pass). The suite includes the whole default runtime
package plus the three new tagged tests:

- `TestJobLeaseAcquireRenewAlreadyRunningAndStaleRecovery` — pass.
- `TestNativeBackupRestoreVerifyRoundTripAndCleanup` — pass (real pg_dump/pg_restore round trip plus
  checksum-mismatch cleanup).
- `TestRestoreVerifyDropsTemporaryDatabaseWhenRestoreFails` — pass (pg_restore-failure cleanup).
- `TestDurableQueueReserveCommitAgainstRealHandoff` — pass (reserve→commit, duplicate replay, zero
  inflation).

## Verification

- `go build ./...`, `go vet ./...`: pass.
- `go test ./...`: pass for every package except the two pre-existing macOS/Darwin-only syscall
  cases that also fail on the base commit and are explicitly out of Session 09 scope —
  `internal/observability` (`TestDurableSpoolIsBounded0600AndReplaySafe`,
  `TestDurableSpoolRejectsUnsafeParentsFilesAndLinksWithoutModification`) and `internal/privacy`
  (`TestKeyFileIsCreateOnceNoFollowAndMode0600`). These were left untouched.
- `python3 scripts/validate_runtime.py`: pass, zero errors (the two previously-missing report
  artifacts now exist).
- `python3 -m unittest tests.test_runtime_contracts`: pass (14 tests).
- `python3 scripts/validate_runtime.py --runtime-only`: pass (real pinned Postgres 18 tagged suite).
- `python3 scripts/validate_runtime.py --soak`: pass (real Docker-Compose accelerated soak, evidence
  summarized above).
- `python3 scripts/session09_supply_chain.py --verify`: pass; Session 09 dependency delta is zero
  (no new third-party module; `go.mod`/`go.sum` digests unchanged from Session 08).

## Residual risks and honest gaps

- The soak harness attaches the `kansoku` service to the internal network AND a second non-internal
  bridge so its loopback-published ports actually forward to the host on Docker Desktop (an
  internal-only container's published ports are not forwarded by the Desktop VM). `postgres` stays
  internal-only with no published port, exactly as production. This is a test-topology accommodation
  for the local Docker Desktop host, not a change to the production `deploy/compose.yaml`, which is
  untouched and on real Linux Docker publishes its loopback ports from the internal-network container
  directly. A reviewer running on Linux may simplify the harness to a single internal network.
- The soak's ephemeral config sets `integrity_enabled: true` to match production, but the run is
  short enough that the daily integrity/retention/backup scheduler cycle does not fire; only the
  per-minute rollup-repair job runs, and the snapshot briefly polls for it to reach a terminal state.
  The soak therefore proves ingest/query/backup/queue durability across restarts, not a full daily
  integrity audit under load (that is Session 08's already-validated territory).
- `IsDurable` confirms aggregate durability (the live fact count caught up to the acknowledged set)
  with one cached read rather than a per-event id lookup, because the read API exposes no per-event
  probe. Since every acknowledged id is unique and the final `FactCount == UniqueEventCount`
  assertion holds, this is a sound aggregate proof, not a per-id one.
- `deploy/compose.security-baseline.yaml` still carries the literal
  `"KANSOKU_SESSION02_REACHABILITY": "unreachable-static-placeholder-until-session09"`. After tracing
  its consumers this file is deliberately left frozen: it is the **Session 02** privacy static
  placeholder, validated by `scripts/validate_privacy.py`/`tests/test_privacy_contracts.py` against
  an exact `app`/`database`-only service set and a `compose_reachability` contract that literally
  states "a process bound to container loopback is not reachable through Docker port publishing;
  Session 09 owns a tested secure topology." `deploy/compose.yaml` is the sole Session 09 Compose
  authority; the baseline file's `database` image digest is the only thing Session 04's data-platform
  validator consumes from it. The placeholder string is a truthful historical Session 02 marker and
  editing it would only muddy that boundary, so it was intentionally not changed.
- Native archive is local, checksum-verifiable and count/lineage-verified but is not application-layer
  encryption; at-rest encryption remains the host/volume responsibility, and release image signing /
  vulnerability attestation remain Session 10 work.
- Session 07b (Gemini/Cursor) remains deliberately deferred and was not revived or modified.
