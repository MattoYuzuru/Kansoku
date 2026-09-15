# Session 09 — Local runtime and operations

## Purpose

Turn components into an appliance-like localhost product that survives restarts, upgrades and long
retention without becoming another system the developer must babysit.

## Compose experience

One command starts the supported stack. Proposed services:

- `kansoku`: collector, API, scheduler and static frontend;
- `postgres`: durable metadata/event store;
- optional `otel-collector` profile only if it materially improves protocol compatibility;
- optional migration/backup jobs invoked explicitly, not permanently running sidecars.

Use `restart: always`, healthchecks, named volumes, pinned images/digests for releases and
loopback-only published ports. A fresh install requires no cloud account.

## Backend choice

Go is the baseline because it supports a small static binary, concurrent HTTP/gRPC ingestion,
embedded migrations/fixtures/UI and low idle overhead. Python remains attractive for parser
experimentation, but a Python production backend needs stronger dependency/image/worker controls.
The choice is validated with measured spikes before ADR acceptance.

## API responsibilities

- OTLP and hook ingestion with bounded payloads/backpressure;
- CLI/importer event batches and acknowledgements;
- inventory, health, incidents and configuration plans;
- analytics query API with global filters/comparisons/completeness;
- export, retention preview, backup/restore status;
- server-sent events or WebSocket only where live dashboard value justifies it.

No API returns prohibited raw fields because they never enter the model.

## Operations

- embedded schema migrations with preflight and rollback/restore guidance;
- bounded queues and disk-backed retry for acknowledged events;
- PostgreSQL backup plus scheduled restore verification in an isolated temporary database;
- database growth forecast, partition maintenance and retention dry-runs;
- graceful shutdown and OTel exporter flush allowance;
- versioned config, compatibility checks and upgrade notes;
- local structured logs with safe fields and bounded rotation;
- diagnostics bundle containing schemas/health/counts, never conversations.

## Resource budgets

Measure idle and load CPU/RSS, ingest latency, database growth and dashboard queries. Apply request
limits, batch writes, compression and rollups only when measurement supports them. The reference
stack should remain comfortable on a developer laptop while Docker is otherwise in use.

## Backup and portability

Exports include normalized privacy-safe data, formula catalog, adapter/schema versions and aliases.
Import verifies compatibility and idempotency. Users can completely remove containers and volumes
with a separately confirmed command. Backups never silently leave the device.

## Deliverables

- Production Dockerfiles and Compose profiles.
- Go/Python ADR with benchmark evidence.
- Versioned API and migration contracts.
- Scheduler, graceful shutdown and backpressure behavior.
- Backup/restore/export/import workflows.
- Resource dashboards and seven-day soak harness.

## Exit gate

A clean machine can install, configure and open Kansoku from documented commands; a seven-day
accelerated soak survives kills/restarts/upgrades without acknowledged-event loss or metric
inflation; backup restore reproduces counts; all ports/mounts/egress match the privacy manifest.

## 2026-07-28 capacity and migration amendment

`spool_max_bytes` limits each emergency spool lane; it is not a database or mirror limit.
`database_soft_limit_bytes` defaults to 5 GiB and is advisory, with warning/degraded/critical
thresholds at 70/85/95 percent. Health exposes database bytes and growth, checkpoint and per-lane
spool use, ingest rejection/durability counters, source freshness, and storage-component
completeness. Filesystem preflight is independently critical below the configured 25 GiB
recommendation; Kansoku never changes Docker Desktop allocation.

Legacy `mirror/state.json` cutover requires checksum-preserving backup, PostgreSQL fact/evidence and
lineage reconciliation, a durable report, then atomic archive. Archive deletion is a separate
preview/confirmation action. Database, indexes, backups, temporary bytes and emergency spool are
reported independently; WAL/rollback headroom stays `not_observed` when it cannot be measured.

## 2026-07-29 runtime follow-through

Normal `serve` owns the authenticated bounded
`POST /v1/evidence-bridges/codex-app-server` lane. It accepts only an explicit opaque installation
header and transient JSONL; it neither discovers nor configures Codex. Ingestion rejection counters
are PostgreSQL-backed and reloaded after restart. A canonical fact whose derived projection fails
keeps its typed sanitized Event/Evidence in the existing per-source spool and retries every
15 seconds; the receipt and spool keep health degraded until idempotent completion.

An approval-gated operator repair handles the crash boundary where PostgreSQL owns the canonical
fact but no spool frame survived. While a projection is pending, its receipt temporarily owns one
closed, maximum-32-KiB normalized Event/Evidence input in PostgreSQL. It contains no generic
attributes or content fields and is deleted on projection success. One apply runs at most 256
database projections, one spool replay pass and a receipt reconciliation; it never re-inserts the
canonical fact/evidence, increments source replay count, discards a failed receipt or returns the
retained input. Pre-`0014` receipts are not rewritten and remain visibly incomplete when they no
longer have an owned spool frame.

Runtime collector activation belongs only to long-running `serve`, immediately before listener
binding. One-shot `backup`, `restore-verify`, export/import, diagnostics and explicit evidence
commands may reuse durable service assembly, but they do not run inventory/rollout scans or
register unrelated source health. Their narrower Compose profiles intentionally omit agent-state
mounts; treating that absence as a serving collector failure would overwrite truthful production
health. One-shot commands may persist only the state owned by the requested operation.
