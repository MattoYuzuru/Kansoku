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

