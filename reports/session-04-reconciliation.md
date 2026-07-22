# Session 04 reconciliation report

- Session: 04 — Data platform and analytics
- Date: 2026-07-22
- Result: automated exit gate passed; Session 05 may begin from the committed/reviewed result
- Public support claims: blocked; all schema/rollup evidence is synthetic fixture-agent only
- Agent configuration reads/writes: none
- Network telemetry/export: none; the only network traffic is the ephemeral PostgreSQL container on
  an isolated Docker bridge network created and torn down per validator run

## Delivered contract

| Acceptance item | Evidence | Result |
|---|---|---:|
| Logical schema (dimensions/facts/quality-operations/rollups) | closed `schema.yaml` registry plus migration 0001 creating every declared table | pass |
| Physical design (monthly range partitioning, BRIN/B-tree indexes, constraints) | migration 0001/0002, `EnsurePartition`, `ExplainNoSeqScan` plan-review test | pass |
| Rollups (hourly/daily, exact percentiles, never averaged) | `RecomputeBucket` exact `percentile_cont`, `TestReplayReconcilesExactlyWithinBudget` naive-average regression | pass |
| Late data algorithm | `rollup_repair_queue` coalescing, `FOR UPDATE SKIP LOCKED` claim, watermark-after-commit | pass |
| Formula registry (append-only) | `SeedFormulaVersion` rejects a mutated `sql_template` for an existing version | pass |
| Completeness-aware query contract | closed `QueryResponse` envelope, `completenessFor`, zero-denominator → `unknown` | pass |
| Query budgets | `statement_timeout` plus measured wall-clock assertion for all four budgeted queries | pass |
| Plan review (no sequential scan) | `EXPLAIN` review of rollup-range and session-drilldown queries | pass |
| Retention (bounded, partition-drop only) | `ApplyRetention` drops only expired whole partitions, idempotent re-run | pass |
| Backup/restore with lineage | isolated-database restore reproduces row counts, constraints, formula lineage and exact p95 | pass |
| Idempotent replay | unique `(source_instance_id, source_native_event_id, observed_at)` index; zero fact inflation on triplicate replay | pass |
| Supply chain | pgx/v5 pinned exact version, vendored, CycloneDX 1.6 inventory independent of Session 03's | pass |
| Ephemeral, deterministic Postgres harness | pinned digest, isolated network, start-clean/run/tear-down every invocation | pass |

## Canonical schema and reconciliation behavior

The authority is the four JSON-subset YAML registries under `contracts/data-platform/`, bound by
`contracts/data-platform-policy-locks.yaml` semantic digests using the same append-only
policy-version mechanism already used for `contracts/privacy` and `contracts/observability`.
`scripts/validate_data_platform.py` independently recomputes each registry's semantic digest and
rejects a coherent registry/lock edit that weakens the PostgreSQL 18 engine pin, the
partition-drop-only retention mechanism, the EAV-avoidance policy, the forbidden JSONB key set
inherited from the Session 02 privacy boundary, the percentile method/forbidden-averaging clause, the
half-open `[from, to)` boundary, the budgeted query id set/ceilings, or the restore-test cleanup
requirement.

`internal/dataplatform/migrations/0001_core_schema.up.sql` creates every table `schema.yaml` declares
across `tables.dimensions`, `tables.activity_facts` (including `prompt_features`, which an earlier
draft of this migration omitted — added and verified against the registry by
`validate_code_and_fixture`'s snippet checks plus a full migrate-then-query smoke path in every
integration test), `tables.quality_operations` and `tables.rollups`. Migration 0002 adds the BRIN
time indexes and B-tree lookup indexes the physical design calls for. The migration ledger
(`schema_migrations`) records a SHA-256 checksum per applied version and fails closed if a previously
applied migration's embedded SQL ever changes.

## Replay, late data and query budgets

`tests/fixtures/session-04/replay-scenario.json` is a synthetic six-bucket, 240-event hourly
scenario. `TestReplayReconcilesExactlyWithinBudget` inserts all 240 events, replays the first
event's fact/evidence triple three times (asserting `replay_count == 3` and exactly one `events` row
for that `fact_key`), computes the first rollup pass, then inserts a late event into an
already-rolled-up bucket. The repair queue is non-empty before the worker runs and exactly zero after
it drains; the recomputed p95 matches an independently computed exact `percentile_cont` value over
*all* facts including the late one, and is asserted to diverge from a naive average of the
pre-late/late values whenever that average would itself diverge from the correct answer — closing the
exact failure mode the contract's `late_data_algorithm.recompute` clause forbids. The subsequent
`hourly_rollup_range_30d` query returns in under its 50ms budget, reports the correct total event
count (240 + 1 late, zero duplicate inflation), `completeness.status == "complete"`, and
`freshness.late_events_pending == 0`.

`TestBudgetedQueriesAvoidSequentialScanOfPartitionedFacts` inserts 20 events, runs the repair worker,
then runs `EXPLAIN` over both the hourly-rollup-range query and the session-drilldown query and
asserts neither plan contains `Seq Scan` over its respective partitioned/rollup table, proving
partition pruning and the migration-0002 indexes are actually used, not merely declared.

## Retention and partition drop

`TestRetentionDropsOnlyExpiredPartitionsAndBoundsData` inserts one fact 380 days before a fixed
`now` (`2026-07-22`, outside the 30-day retention horizon used by the test and in a different
calendar month from the "recent" fact) and one fact one day before `now`. `ApplyRetention` drops
exactly the expired monthly partition, leaves the in-horizon partition and its row intact, and a
direct row count against `events` confirms the expired fact's row is actually gone (partition drop
bounds storage) rather than merely excluded by a filter. A second `ApplyRetention` call over the same
horizon drops nothing new, proving idempotency.

## Backup, restore and lineage

`TestBackupRestoreReproducesFormulaResultsWithLineage` seeds a `formula_versions` row for
`component.duration_ms/1` with its exact `sql_template`, inserts 12 facts, runs the repair worker,
and creates a deterministic logical backup whose manifest checksum is independently re-verified.
Restore targets a brand-new isolated physical database (matching
`retention.yaml backup.restore_test.target == isolated_temporary_database`), and the test verifies
all four `restore_test.verifies` items: row counts match, a negative `duration_ms` is still rejected
by the restored schema's CHECK constraint (proving the restore is not a bare unconstrained data dump),
the restored `formula_versions` row's `sql_template` matches the source exactly, and recomputing the
same hourly p95 rollup against the restored facts reproduces the source's exact value. The temporary
restore database is dropped in the test's cleanup regardless of outcome.

## Supply chain and ephemeral runtime

`go.mod`/`go.sum`/`vendor/` add exactly one new direct dependency for this session,
`github.com/jackc/pgx/v5 v5.7.6`, plus its resolved transitives `pgpassfile`, `pgservicefile`,
`puddle/v2`, `golang.org/x/crypto` and `golang.org/x/sync`. `reports/session-04-sbom.json` is a
deterministic CycloneDX 1.6 inventory scoped to exactly those six modules — the Session 03 OTLP/gRPC
modules remain solely inventoried by `reports/session-03-sbom.json`, so the two reports never
silently duplicate or drift on the same component. `scripts/session04_supply_chain.py --verify`
recomputes the report byte-for-byte and fails if it is stale.

`scripts/validate_data_platform.py` starts the exact `postgres@sha256:9a8a...1de15` image already
pinned in `deploy/compose.security-baseline.yaml` and in `contracts/data-platform/schema.yaml`
(`engine.image_digest`) — the validator asserts those two pins agree — on a freshly created
`--internal` Docker bridge network with no prior state, waits for `pg_isready`, runs the
`postgres_integration`-tagged Go suite from the pinned Go 1.26.5 image with the ephemeral instance's
DSN, and always removes both the container and the network afterward, whether the suite passed or
failed. No state is reused between runs; this is a start-clean/run/tear-down harness, not a shared
long-lived test database.

## Verification

- `python3 scripts/validate_data_platform.py --json` — pass (static contract plus the
  ephemeral-PostgreSQL `postgres_integration` Go suite: 13/13 tests pass).
- `python3 scripts/validate_privacy.py --json` — pass (unaffected; Session 04 does not touch
  `contracts/privacy`). The `contracts/privacy/ingress.yaml` / `contracts/privacy-policy-locks.yaml`
  diff visible in the working tree alongside this session's changes is prior, already-reconciled
  Session 03 scope (the `Lineage.session_pseudonym` fix under appended lock `privacy.ingress/2`,
  explained in `reports/session-03-reconciliation.md`), not a Session 04 edit — it is called out here
  only so a diff of the working tree is not mistaken for an undocumented Session 04 touch of a prior
  session's contract.
- `python3 scripts/validate_observability.py --json` — pass (unaffected; Session 04 does not touch
  `contracts/observability`).
- `python3 scripts/run_go_tests.py` — pass; the default `go test ./...` sweep (network disabled,
  `postgres_integration`-tagged tests skipped via `t.Skip` when `KANSOKU_TEST_POSTGRES_DSN` is unset)
  and the isolated-network Go image sweep both green across `internal/dataplatform`,
  `internal/installer`, `internal/localhttp`, `internal/observability` and `internal/privacy`.
- `python3 scripts/session04_supply_chain.py --write --verify` — pass, 6 components, deterministic
  report checksum.
- `go build`/`go vet -mod=vendor -tags postgres_integration ./...` inside the pinned Go image — pass.

## Fixed during this reconciliation pass

1. **`internal/dataplatform/partitions.go` `partitionUpperBound`:** used `fmt.Sscanf` with a
   `"FOR VALUES FROM ('%[^']') TO ('%[^']')"` format string. Go's `fmt.Sscanf` "%[...]" scanset verb
   does not compose across the literal `'` characters the way a C `scanf` caller would expect, so
   every call failed with `bad verb '%[' for string` and `ApplyRetention` could never determine which
   partitions were expired — `TestRetentionDropsOnlyExpiredPartitionsAndBoundsData` failed with
   exactly that error before this fix. Replaced with an explicit regexp
   (`partitionBoundPattern`) and extracted the pure parsing logic into `parsePartitionUpperBound` so
   it is covered by a connection-free unit test (`TestParsePartitionUpperBound`) as well as the live
   integration test.
2. **`internal/dataplatform/migrations/0001_core_schema.up.sql` missing `prompt_features`:**
   `contracts/data-platform/schema.yaml tables.activity_facts` declares `prompt_features`, but the
   migration never created it, so `Migrate` produced a schema that did not match the contract it
   claims to implement. Added the table (and its down-migration `DROP TABLE`) to migration 0001,
   which was still fully uncommitted/untracked at the time of this fix and therefore not yet subject
   to the append-only-after-first-trusted-commit migration policy.
3. **Missing `reports/session-04-reconciliation.md` and ADR 0007:** neither existed prior to this
   pass, unlike Sessions 01–03 which each recorded both. Both are now present; this report and
   `adr/0007-session-04-data-platform-and-metrics.md` are the required Session 04 artifacts.

## Residual risks and downstream gates

1. **Percentile method.** Only exact `percentile_cont` over normalized facts is implemented.
   Mergeable sketches and precomputed distribution buckets remain a documented, unimplemented
   `rollups.yaml percentile_policy.future_options` gap until a real unbounded-range percentile need is
   measured.
2. **1,000,000-events/day query-budget evidence.** The `postgres_integration` suite proves exact
   reconciliation, late-data repair, plan-review and budget enforcement at a schema-scale synthetic
   fixture (hundreds of rows), not TDD 04's reference million-event/day load. The four budgeted query
   ceilings are enforced and unit-tested at this scale but not yet validated at the reference scale; a
   materialized million-event fixture and re-measurement is a follow-up gate before any query-budget
   number is cited as scale-tested.
3. **Time-range presets and sprint resolution.** `rollups.yaml time_ranges` defines
   day/week/month/six-month/year/sprint/custom presets and DST-safe UTC bucket exposure;
   `BucketStart` proves the UTC bucket-boundary/DST-safety primitive, but no preset resolver exists
   yet. Deferred to the session that first builds a dashboard query surface consuming it.
4. **Cost formulas.** `price_catalog_versions`/`cost_estimates` exist as schema only; no code computes
   a cost estimate from token usage and a price-catalog snapshot. Deferred to the session that first
   needs a real cost panel.
5. **Backup/restore transport.** `CreateBackup`/`RestoreBackup` is a deterministic in-process logical
   export/import that fixes the manifest shape and restore-test verification method now; it is not
   `pg_dump`/`pg_basebackup`-compatible. `retention.yaml backup.strategy` already names Session 09 as
   the owner of the PostgreSQL-native transport.
6. **No real agent fixture, runtime canary or agent configuration was observed or changed.** All
   dimension/fact data remains synthetic fixture-agent only; public Supported/Beta governance remains
   blocked, consistent with Sessions 01–03.
7. **Supply-chain evidence is unsigned** and no production application image exists yet; vulnerability
   scanning, signed provenance and production resource/soak evidence remain Session 09/10 gates, same
   as recorded in `reports/session-03-sbom.json` and ADR 0006.
