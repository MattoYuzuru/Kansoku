# TDD 17 — Kansoku system self-observability

## Storage and collection

Add bounded-retention `system_samples`, `query_observations` and backup/restore status projections.
Collect process/container CPU quota and usage, RSS, selected Go runtime gauges, filesystem/database/
spool/backup bytes and queue/freshness values. Never collect argv, environment, cwd, raw mount paths,
SQL text/parameters or URL query values.

Instrument ingress stages and API route templates with monotonic durations and safe outcome/error
classes. Operational data bypasses agent ingress and uses a separate bounded writer to avoid
recursive telemetry.

## Queries and UI

Register current, range, p50/p95/p99 and growth formulas with sampling coverage. System renders
readable IEC units, sparklines, sampling windows, capacity thresholds, slow route classes and
backup/restore history.

## Tests

Controlled CPU/RSS/load, DB growth, queue delay, slow route, backup, isolated restore and failed
restore scenarios; retention and overhead benchmarks; missing cgroup/filesystem signal states;
privacy scans and restart recovery.

## Exit gate

All proposal metrics are either measured with declared coverage or explicitly unsupported, failed
restore opens an incident, and self-observation stays within registered resource budgets.

## P0 capacity implementation (2026-07-28)

`runtime.CapacitySnapshot` samples `pg_database_size`, table heap, indexes, cumulative temp bytes,
backup bytes, compact checkpoint bytes and filesystem statistics. Database thresholds are advisory:
70 percent warning, 85 degraded and 95 critical. Filesystem state is independently critical below
the configured 25 GiB recommendation or operational free-space envelope. WAL/headroom and current
temp occupancy are emitted as `not_observed` exclusions rather than zero. Health cannot be
`pass` while durability, projection repair, filesystem or source freshness is degraded.

## Restart-durable ingestion health (2026-07-29)

Queue initialization loads per-source success/rejection state from `runtime_ingestion_health`.
Rejection increments are persisted synchronously; success timestamps are limited to one write per
source per second. Health combines persisted and process state and degrades if counter persistence
fails. The process-local availability flag is current state: a failed recorder write sets it, and a
subsequent successful recorder write after recovery clears it; durable counter totals themselves
are loaded from PostgreSQL. Pending projection receipts are retried from the source spool every
15 seconds and remain visible until completion.

Source health is an input to the overall rank, not an exclusions-only annotation. A runtime source
in `degraded`, an `unknown` source value, a watermark with a gap/inactivity/missing commit, or a
source timestamp more than five minutes in the future makes `source_state` and overall health at
least `degraded`. `configured/not_observed` and `unsupported/unsupported` remain completeness
exclusions because they are not write failures. Every watermark exposes its `freshness_state` and
`clock_state`; future clock skew can therefore never masquerade as fresh ingestion.

## Operator projection repair visibility (2026-07-29)

The projection-repair preview exposes pending/retryable/permanent counts, repairable PostgreSQL
input count, legacy input-absent count, maximum attempts, oldest/last timestamps, bounded safe error
classes and every spool lane's depth/bytes/budget. It explicitly returns
`payloads_exposed=false`, `automatic_discard=false`, completeness and exclusions. Legacy
input-absent or permanent-error receipts make preview completeness partial. Apply remains failed
and health remains degraded while any receipt survives; a successful apply proves the database
projection pass and spool replay reconciled to zero.
