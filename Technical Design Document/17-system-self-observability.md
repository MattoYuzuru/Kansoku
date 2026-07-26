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
