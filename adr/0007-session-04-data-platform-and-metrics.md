# ADR 0007: Session 04 data platform and metrics boundary

- Status: accepted
- Date: 2026-07-22
- Owners: Session 04 core architecture

## Context

Session 04 must replace the Session 03 `FileStore` durability spike with the real PostgreSQL 18
system of record ADR 0001 selected: range-partitioned fact tables, hourly/daily rollups with exact
`percentile_cont` quantiles, a late-data repair algorithm that never averages already-computed
percentiles, a completeness-aware query contract with enforced query budgets, bounded retention via
partition drop, and backup/restore that reproduces formula results with lineage. The exit gate is
"reference datasets reconcile exactly, common queries meet the defined query budget, late data
repairs correctly, retention is bounded, and restore reproduces formula results with lineage" (TDD
04 / ROADMAP.md).

Session 04 does not re-litigate PostgreSQL vs. SQLite: ADR 0001 already selected PostgreSQL 18 and
Docker Compose, and this ADR only records the schema/rollup/query/retention decisions layered on top
of that baseline.

## Decision

1. The authoritative logical/physical/rollup/query/retention contract is the closed registry set in
   `contracts/data-platform/{schema,rollups,query-contract,retention}.yaml`, locked by
   `contracts/data-platform-policy-locks.yaml` using the same append-only semantic-digest mechanism
   already used for `contracts/privacy` and `contracts/observability`.
2. `internal/dataplatform` implements the full logical schema from TDD 04 (dimensions, activity
   facts including `prompt_features`, quality/operations tables, rollups) via two ordered,
   checksummed, up/down migration pairs, embedded and applied inside `Migrate`. A ledger row whose
   checksum no longer matches the embedded migration fails closed rather than silently re-running or
   skipping a changed migration.
3. `events`, `event_evidence`, `model_operations`, `token_usage`, `tool_calls` and `mcp_connections`
   are range-partitioned monthly by `observed_at`. Idempotency is enforced by a unique index scoped
   to `(source_instance_id, source_native_event_id, observed_at)`; a replay increments
   `event_evidence.replay_count` and never inserts a second fact row, mirroring the Session 03
   `FileStore` contract exactly but now backed by real Postgres constraints instead of an in-process
   snapshot.
4. Rollups are computed by a single write path — `RecomputeBucket` — used both for on-time facts and
   for late-arriving ones. A late fact enqueues its `(metric_family, bucket_start, dimension_scope)`
   key into `rollup_repair_queue` (coalesced by a `UNIQUE` constraint); a worker claims batches with
   `SELECT ... FOR UPDATE SKIP LOCKED`, fully recomputes the bucket's `percentile_cont` values from
   normalized facts, and only then advances `rollup_status.rollup_watermark`. There is no code path
   that averages, blends or otherwise recombines two already-computed percentiles.
5. Every budgeted query (`hourly_rollup_range_30d`, `daily_rollup_range_1y`, `session_drilldown`,
   `percentile_recompute_bucket`) is enforced two ways: a server-side `SET statement_timeout` scoped
   to the budget ceiling on the connection actually used, and a measured Go-side wall-clock
   assertion. `EXPLAIN` plan review confirms partition pruning plus the migration-0002 lookup indexes
   are used instead of a sequential scan of a partitioned fact table.
6. Retention drops whole expired monthly partitions (`DropPartitionsOlderThan`); it never issues a
   row-by-row `DELETE` against a partitioned fact table. `ApplyRetention` is idempotent: a second run
   over the same horizon drops nothing new.
7. Backup is a deterministic logical export (`CreateBackup`) with a checksum-verified manifest
   matching `retention.yaml backup.manifest_fields`. Restore targets an isolated temporary database
   (`isolatedDatabase`), verifies row counts, verifies that CHECK constraints are still enforced
   (not merely a bare data dump), verifies `formula_versions` lineage (the exact registered
   `sql_template`), and verifies that recomputing the same rollup against the restored facts
   reproduces the exact source `percentile_cont` result. The temporary restore target is always
   dropped after verification.
8. `contracts/data-platform/schema.yaml.engine.image_digest` is pinned to the same digest already
   pinned in `deploy/compose.security-baseline.yaml`, so the ephemeral test harness
   (`scripts/validate_data_platform.py`) and the eventual Session 09 runtime always agree on exactly
   which PostgreSQL 18 image is authoritative.
9. `github.com/jackc/pgx/v5` (plus its resolved transitives `pgpassfile`, `pgservicefile`,
   `puddle/v2`, `golang.org/x/crypto`, `golang.org/x/sync`) is vendored, exact-pinned in `go.mod`,
   and inventoried in `reports/session-04-sbom.json`, independent of the Session 03 OTLP/gRPC
   inventory in `reports/session-03-sbom.json`.

## Consequences

- Session 05+ adapters write facts through `InsertFact`/`EnsureDimensions` instead of the Session 03
  `FileStore`; the fact/evidence identity contract (lane-independent fact key, lane-specific evidence
  replay counting) is preserved across the migration, so Session 03's replay/idempotency proofs
  continue to hold at the new durability boundary.
- The formula registry is append-only (`SeedFormulaVersion` rejects a second call for the same
  `(formula_id, version)` with different `sql_template`); a semantic change to a metric must register
  a new version rather than mutate history, matching `rollups.yaml formula_registry.versioning`.
- `contracts/data-platform/rollups.yaml percentile_policy.current_choice` explicitly records that
  only exact `percentile_cont` is implemented; mergeable sketches and precomputed distribution
  buckets remain a documented gap (below), not a silent scope drop.
- Query budgets are enforced against a schema-only synthetic fixture (hundreds of rows), not the
  million-event/day reference scale TDD 04's test suite calls for; the 1M/day load evidence remains
  an explicit downstream gate (below), exactly as ADR 0001 already anticipated for Session 04.

## Rejected alternatives

- **Re-open PostgreSQL vs. SQLite:** ADR 0001 already selected PostgreSQL 18 with measured evidence;
  Session 04 only had to prove the concurrency/partition/retention/backup claims ADR 0001 deferred to
  it, not re-run the engine choice.
- **Incrementally patch a bucket's percentile when a late event arrives:** cheaper, but
  `percentile_cont` is not decomposable/mergeable across an insert without recomputation; an
  incremental patch would silently produce a mathematically wrong quantile. Full recompute from
  normalized facts is the only exact option available without a sketch library (see gap below).
  `contracts/data-platform/rollups.yaml late_data_algorithm.recompute` and the Go
  `TestReplayReconcilesExactlyWithinBudget` test explicitly forbid the naive-average shortcut.
- **Row-by-row `DELETE` for retention:** simpler to reason about per-row, but does not bound
  partitioned-table storage/vacuum cost the way a partition drop does, and contradicts
  `schema.yaml partitioning.drop_policy`.
- **Skip the isolated-database restore test and only assert on the source connection:** would not
  distinguish "the data was never lost" from "the schema/constraints/lineage were actually
  reconstructed from the backup," which is the literal exit-gate wording.

## Known gaps (explicitly recorded, not silently dropped)

1. **Mergeable percentile sketches / precomputed distribution buckets.** TDD 04 lists these as
   options 2 and 3 for percentile computation "after measured need"; only option 1 (exact
   `percentile_cont` over bounded ranges) is implemented. `rollups.yaml percentile_policy` records
   this as the current choice, not a completed evaluation of the alternatives. A future session must
   pick and integrate a sketch extension/library only once a real unbounded-range percentile need is
   measured.
2. **1,000,000-events/day synthetic load query-plan evidence.** TDD 04's test suite explicitly calls
   for this scale; the current `postgres_integration` suite proves exact reconciliation, late-data
   repair, plan-review (no sequential scan) and budget enforcement against a schema-scale synthetic
   fixture (hundreds of rows), which is sufficient to prove correctness but not the query-budget
   ceilings under the reference load. `contracts/data-platform/query-contract.yaml
   budgets.reference_scale` documents the target; a follow-up session must add a materialized
   million-event fixture and re-measure the four budgeted queries against it before any
   Supported/Beta claim cites the query-budget numbers at that scale.
3. **Time-range preset resolution (day/week/month/six-month/year/sprint/custom) and DST-safe local
   timezone bucket exposure.** `rollups.yaml time_ranges` defines the contract; `BucketStart` proves
   UTC bucket determinism across a DST boundary, but no code resolves a named preset or a
   configurable sprint start date/duration into a concrete `[from, to)` range yet. This is dashboard
   query-surface work that has no consumer before Session 10 and is deferred to it.
4. **Cost formula computation.** `price_catalog_versions` and `cost_estimates` exist as schema only;
   no code populates a cost estimate from a token-usage row and a price-catalog snapshot. TDD 04
   requires cost formulas to reference a price snapshot and effective date (never a live mutable
   price); the schema enforces the foreign key but no formula implementation exists yet. Deferred to
   the session that first needs a real cost dashboard panel.
5. **PostgreSQL-native backup/restore transport.** `retention.yaml backup.strategy` names
   `postgresql_native_logical_and_physical_selected_in_session_09`; Session 04's `CreateBackup`/
   `RestoreBackup` is a deterministic in-process logical export that fixes the manifest shape and
   restore-test verification method now so Session 09 can swap in `pg_dump`/`pg_basebackup` without
   changing the contract. It is not `pg_dump`-compatible and is not a production backup transport.
