# TDD 09 — Backend, API and local operations

## Compose topology

```yaml
services:
  kansoku:
    restart: always
    ports:
      - "127.0.0.1:${KANSOKU_HTTP_PORT:-43100}:43100"
      - "127.0.0.1:${KANSOKU_OTLP_GRPC_PORT:-4317}:4317"
      - "127.0.0.1:${KANSOKU_OTLP_HTTP_PORT:-4318}:4318"
    depends_on:
      postgres:
        condition: service_healthy
  postgres:
    restart: always
    # no host-published port
```

The final file pins supported image tags/digests, healthchecks, named volumes, rootless users,
read-only filesystems/capabilities and resource hints. Example is non-authoritative until Session 09.

## Backend packages

```text
cmd/kansoku
internal/ingress/{otlp,hooks,batch}
internal/privacy
internal/adapters
internal/inventory
internal/normalize
internal/reconcile
internal/store
internal/rollup
internal/audit
internal/api
web/dist (embedded)
```

Go contexts/deadlines and bounded worker pools propagate cancellation. Panics at source parsing are
contained into safe incidents where possible; process-level panics are crash/restart tested.

## API groups

- `POST /v1/hooks/{adapter}/{event}` — local hook ingestion;
- standard OTLP `/v1/logs`, `/v1/traces`, `/v1/metrics` and gRPC services;
- `POST /v1/adapter-events/{adapter}` — sanitized batch/external adapter protocol;
- `GET /api/v1/inventory/*` — agents/components/versions/relations;
- `GET /api/v1/analytics/*` — registered metric queries;
- `GET /api/v1/health/*` — sources/audits/incidents/completeness;
- `POST /api/v1/plans/*` — preview/apply separately authenticated local setup actions;
- `POST /api/v1/admin/*` — retention/export/backup commands with CSRF/local auth.

OpenAPI is generated/validated and never includes prohibited content properties.

## Authentication

Generate separate random tokens for hook ingest and dashboard mutations. Read-only dashboard MAY use
a loopback session bootstrap, but mutating endpoints always require session/CSRF. Tokens live in
host secret storage/file, mounted narrowly or passed via Docker secret; never in Compose committed
values, URL query or logs.

## Queues and backpressure

- bounded in-memory channel per ingress class;
- batch DB transactions with max count/latency;
- sanitized disk spool for hook clients and optionally server WAL queue;
- HTTP 429/503 with safe retry hints before acceptance;
- acknowledged only after durable commit/spool;
- per-source rate/payload quotas prevent noisy-agent starvation;
- queue depth/oldest age exported to operational metrics.

## Scheduler and workers

Use PostgreSQL locks/leases for singleton daily audit, rollups, retention and backup. Jobs have run
IDs, attempt policy and terminal states. Mutating external agent operations are not retried
automatically. Shutdown stops accepts, drains within timeout, persists remaining sanitized work and
flushes Kansoku telemetry.

## Migrations

Embed ordered migrations with checksums. Startup performs compatibility preflight; destructive or
long migrations require an explicit upgrade command and verified backup. Application supports one
documented rolling boundary only if multi-container upgrades need it; otherwise stop-the-world local
upgrade is simpler and honest.

## Backup/restore/export

- scheduled encrypted-at-host-rest backup using PostgreSQL-supported tooling;
- manifest/checksum/version/privacy metadata;
- isolated automatic restore test on cadence;
- privacy-safe NDJSON/Parquet export for user analysis;
- import is idempotent and never trusts exported formulas/schemas without validation;
- deletion of material volumes is a separate explicit operation.

## Logging and self-observability

JSON logs with event name, safe IDs, duration, counts and error class. No payloads, SQL parameters,
paths or env dumps. Rotate/limit. Expose Prometheus/OTel internally if useful, but do not require
external backend. Store operational time-series with bounded retention in Kansoku or PostgreSQL.

## Performance tests

- idle 24h and seven-day soak;
- personal/enthusiast/stress burst ingest;
- DB restart and container SIGKILL at acknowledgement boundaries;
- dashboard concurrent with replay/rollup/backup;
- disk-full and slow filesystem;
- migration/restore on multi-year dataset;
- ARM64/x86_64 macOS/Linux; Windows host via Docker where supported.

## Exit gate

Compose is reproducible and hardened, ports are loopback-only, acknowledged events survive tested
faults, backups restore, common queries meet SLO and diagnostics reveal health without content.

