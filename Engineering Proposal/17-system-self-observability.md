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
