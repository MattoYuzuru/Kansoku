# Session 17 — Kansoku system self-observability

## Status

Approved for later planning priority on 2026-07-26. Implementation has not started.

## Purpose

Make the System page explain the health and cost of running Kansoku itself: process/container
resources, storage growth, ingress/query performance and backup/restore readiness.

## Scope

Measure CPU relative to available quota, RSS, bounded Go runtime metrics, database/spool/backup
bytes, free disk, queue age, ingest stage latency, query latency by route template, rollup freshness,
database growth and backup/isolated-restore state. Values use readable units and sampled
time-series with bounded retention.

Operational telemetry is separate from agent telemetry so Kansoku cannot recursively observe itself
through the same ingress. Logs and metrics never contain SQL parameters, URL query values, command
lines, environment values, raw paths or payloads.

## User experience

System provides current state, p95, growth rate, sampling interval, capacity warning, slow safe
route classes and backup/restore timeline. Unsupported host/container signals remain explicit.

## Deliverables

- operational sample/query observation schemas and retention;
- process/cgroup/filesystem/PostgreSQL collectors;
- ingress/query instrumentation and registered formulas;
- backup/restore status integration;
- System dashboards, sparklines and capacity alerts;
- controlled load, DB growth, backup, restore and failure tests.

## Exit gate

Injected CPU/RSS load, database growth and a slow route become visible within budget; backup and
isolated restore pass; a failed restore creates an incident; and the collector's own measured
overhead remains below its declared CPU, memory, storage and query budgets.

## 2026-07-28 capacity amendment

Capacity health is an explicit budget model rather than a database-size proxy. It reports primary
database, table heap, indexes, cumulative temporary bytes, backups, checkpoint state, per-lane
emergency spool and filesystem free space. WAL/current temporary occupancy remain exclusions when
unobservable. Database growth/day and estimated exhaustion are nullable until enough samples
exist. Any critical filesystem or durability state dominates the overall status even when
PostgreSQL is currently writable.

## 2026-07-29 durable health amendment

`runtime_ingestion_health` now receives throttled per-source success timestamps and synchronous
backpressure/durability rejection increments. Queue startup reloads the aggregate, so health labels
the scope `restart_durable`; a failed counter write independently degrades health. Projection
receipts are retried from the bounded sanitized spool every 15 seconds and pending count remains a
degraded dimension.

Source freshness participates in the health rank. Degraded/unknown runtime sources, committed
watermark gaps/inactivity and future clock skew cannot be hidden in exclusions while overall
health remains green. Optional configured-but-not-yet-observed and explicitly unsupported sources
remain non-numeric completeness exclusions.
