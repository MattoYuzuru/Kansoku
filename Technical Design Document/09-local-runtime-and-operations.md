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

## P0 runtime cutover (2026-07-28)

Runtime migration `0003_durability_capacity_state` adds ingestion health, capacity samples, source
health and mirror reconciliation records. Startup performs legacy mirror backup/reconciliation and
only then atomically renames it into `mirror/legacy-archive`; it never deletes an archive.
Dataplatform migration `0012_observability_projection_receipts` preserves canonical fact acceptance
when a derived projection fails and exposes pending repair receipts.

Configuration adds a 4 MiB checkpoint limit, 5 GiB database soft budget, 70/85/95 percent database
thresholds, 25 GiB filesystem recommendation and a five-second rollout watcher interval. Health
returns per-lane queue/spool metrics, PostgreSQL-backed restart-durable ingest counters, last success/rejection,
source freshness and explicit completeness exclusions. A full 5 GiB soak is forbidden when the
Docker filesystem would fall below 20 percent free; synthetic cleanup is namespace-scoped.

Normal `serve` registers `POST /v1/evidence-bridges/codex-app-server` on the existing guarded
ingress listener. The same ingress bearer, loopback/container-bridge policy, 1 MiB request bound and
rate limit apply. An opaque installation header is required; raw frames live only inside a
request-scoped 0.145.0 demultiplexer. Projection failures return the normalized Event/Evidence to
the source spool, and the scheduler retries all lanes every 15 seconds without draining a failed
record.

The Codex inventory target and rollout watcher share one explicit opaque installation ID from
runtime configuration. Normalized source/scope IDs are overridden only with the validated
`ain_<32 hex>` value, and resolution candidates are scoped to it. A syntactically valid marker is
committed as `requested` without synchronously querying inventory; otherwise a transient lookup
failure followed by checkpoint advance would lose evidence. Empty legacy config uses
`normalizedInstallationID("codex")`, never `LatestInstallationForAdapter`; conflicting bindings
for one discovered file degrade `codex.rollout` before emission. App Server clients for the same
logical installation send this same value in `X-Kansoku-Agent-Installation`.

Dataplatform migration `0014_projection_repair_inputs` changes the projection receipt key from
canonical event to evidence assertion and adds an optional `kansoku.projection-input/1` JSONB
value. The value is limited to 32 KiB and serializes only the already-validated closed
`observability.Event`/`Evidence` shapes. It exists only while the projection is pending and is
deleted with the receipt. Historical receipt rows stay `NULL`; neither migration nor repair guesses
the missing identity metadata. The down migration is intentionally non-destructive because
dropping pending retry input or collapsing independent evidence rows would lose owned work.

Runtime migration `0004_projection_repair_approval` additively permits
`projection_repair_retry` in the existing approval ledger. The guarded
`POST /api/v1/admin/projection-repair/preview` response contains aggregate receipt states,
repairable/legacy counts, safe error classes and per-lane spool occupancy only. Apply is bound to
the preview hash and one-use nonce, executes a 256-row database-projection pass followed by one
idempotent spool replay, and succeeds only when receipt reconciliation reaches zero. Failure
retains every receipt/input/spool frame and records a failed approval. Database-only repair calls
projection writers directly, so it does not reinsert facts/evidence or inflate source
`replay_count`.

`NewAppliance` assembles migrations, durable queue and operation services without activating
collectors. `Appliance.Run` exclusively performs the initial inventory scan, rollout scan and
Codex App Server source registration before it binds listeners, then starts their supervised
loops. This split prevents `backup`, `restore-verify`, export/import, diagnostics,
`evidence-bridge` and `mcp-evidence` from changing unrelated inventory/source health in one-shot
containers that intentionally lack agent-state mounts.

`TestOneShotAssemblyDoesNotActivateRuntimeSources` seeds complete/producing durable health,
constructs and shuts down the one-shot assembly with an absent inventory root, and requires all
semantic status columns and timestamps to remain unchanged. Contract
`runtime.operations-backup-and-soak/4` locks the same ownership boundary.
