# ADR 0012 — Session 09 local appliance, durable admission and operations boundary

- Status: accepted for Session 09 implementation
- Date: 2026-07-24
- Owners: Kansoku runtime
- Supersedes: the unreachable Session 02 Compose placeholder only
- Extends: ADR 0001, ADR 0004–0007 and ADR 0011

## Context

Sessions 02–08 established the privacy boundary, canonical ingress, PostgreSQL system of record,
adapter SDK and daily integrity scheduler, but no production process assembled them into one
restart-safe local appliance. The Session 03 `FileStore` remains useful as a bounded local
transaction boundary and the typed `DurableSpool` remains useful as a sanitized fallback, but
neither may become a parallel production system of record beside Session 04 PostgreSQL. Session 08
also left integrity audit/report/incident tables outside the old data-platform-only logical backup.

Docker port publication creates a second topology detail: a process bound only to container
loopback cannot be reached through a published port, while a request forwarded through the
container bridge no longer has a loopback peer address. The final policy must keep host publication
on `127.0.0.1`, retain exact Host/origin/auth checks, reject forwarded headers and isolate the
Compose network, rather than pretending the Session 02 placeholder was reachable.

## Decision

1. `cmd/kansoku` is one Go appliance process. It owns one bounded `pgxpool.Pool`, one
   `observability.Ingestor`/OTLP receiver, one runtime API, one operations worker set and one
   `integrity.ProductionAssembly`. It does not create a second event schema, adapter registry,
   installer protocol or analytics query path.
2. Configuration is strict, versioned JSON. Unknown fields fail startup. Secret values are loaded
   only from individually configured regular files (or Compose secret mounts), bounded to 4096
   bytes, at least 32 bytes after one optional trailing newline, pairwise distinct and never copied
   into logs, diagnostics, URLs or process arguments.
3. The production Compose profile publishes `43100`, `4317` and `4318` only on `127.0.0.1`; it
   publishes no PostgreSQL port. In explicit appliance mode the shared `localhttp.Guard` may accept
   a private container-bridge peer only when exact loopback Host, route-specific bearer,
   origin/CSRF, no-forwarded-header and internal-network assumptions all remain enforced. Legacy
   loopback-only guard construction remains unchanged for non-container tests and direct use.
4. Production ingress uses a reservation-capable `observability.DurableFactSink`. Each closed source
   lane has an independent bounded queue and one worker. Reservation happens before the legacy
   FileStore commit, so a full lane returns retryable backpressure before acceptance. A successful
   response is emitted only after PostgreSQL commit or sanitized spool fsync. FileStore durability
   alone is not a production acknowledgement. PostgreSQL remains the system of record; spool replay
   calls the same idempotent handoff and drains only after every record commits.
5. `/api/v1` has separate ingress, read and mutation bearer secrets. Mutation/admin routes also
   require exact same-origin and CSRF. Plan preview/apply reuses `installer.PlanSHA256`,
   `installer.Approval` and `installer.SimulateApply`; preview never writes and apply binds every
   plan/original/planned hash plus a single-use nonce. The default container mounts no agent config,
   so it returns an approved materialized result/receipt without claiming a host-file mutation.
6. Runtime migrations own only operation job/approval/import receipt tables. Job leases are
   PostgreSQL rows with bounded attempts and safe error classes. Existing integrity scheduling
   keeps its PostgreSQL session advisory lock; Session 09 does not replace it with a second job
   system.
7. Native backup uses PostgreSQL 18 `pg_dump --format=custom --no-owner --no-acl` as an argv list,
   never shell text. Its checksum/version/privacy manifest covers data-platform, integrity and
   runtime tables. Restore verification targets a new isolated database, compares counts,
   constraints/migration ledgers and formula lineage, and removes the target even on failure.
   Portable import validates local formula/schema references and never accepts exported formula
   definitions, schema definitions, migration SQL or executable commands.
8. Diagnostics are a deterministic bounded metadata bundle: versions, safe health/counts, queue/job/
   backup/resource state and config fingerprint. Paths, environment, credentials, SQL, event rows,
   content and user notes are structurally absent.
9. The accelerated soak represents 168 hourly cycles across seven logical days. It records an
   acknowledged-event ledger and actually executes the process-restart, database-restart and
   stop-the-world upgrade-boundary transitions its report names. It is not described as a literal
   seven-day wall-clock soak.
10. Shutdown follows Go `http.Server.Shutdown`: stop accepts, wait for HTTP/grpc shutdown, drain
    lanes within a bounded deadline, fsync remaining sanitized work, stop scheduler and close the
    shared pool. Timeout exits nonzero rather than silently discarding work.

## Consequences

- The appliance can be tested as one graph without inventing a release UI (Session 10 remains
  untouched).
- Docker bridge acceptance is a narrowly declared deployment mode, not a general proxy-trust or
  forwarded-header mode.
- Successful spool acknowledgement means the event can remain temporarily absent from analytics;
  queue/spool age and completeness expose that delay until replay.
- A native archive is local and checksum-verifiable but not application-layer encryption. At-rest
  encryption remains the host/volume responsibility unless a later release adds a reviewed key
  management design.
- The default plan apply endpoint is useful for exact preview/approval/materialization and audit,
  but does not claim to mutate unmounted host agent configuration.
- Release image signing/vulnerability attestation and the frontend UX remain Session 10 work.

## Rejected alternatives

- **A new SQLite/file production store:** duplicates PostgreSQL identities, migrations, retention and
  reconciliation semantics.
- **Acknowledging after the Session 03 FileStore commit alone:** can report success while the
  system-of-record and replay spool both lack the event.
- **One global queue:** allows a noisy OTLP lane to starve hooks or transcript import.
- **Unbounded goroutines/retry loops:** hide load, defeat shutdown and amplify database outages.
- **Binding host ports without `127.0.0.1`:** Compose documents that omission binds all interfaces.
- **Trusting `X-Forwarded-*` from the Docker bridge:** creates an unnecessary proxy trust boundary.
- **Tokens in Compose environment values:** exposes secrets through rendered config and process
  inspection; per-service secret files are the reviewed mechanism.
- **Shelling out through `sh -c` for backup:** turns DSN/path data into command syntax and makes argv
  review impossible.
- **Restoring a backup over the live database as a test:** makes validation destructive and cannot
  distinguish a usable archive from a partially applied restore.
- **Importing formula/schema definitions from export:** lets a data artifact rewrite executable
  query semantics or compatibility policy.
- **Calling an accelerated harness a real seven-day soak:** overstates evidence; reports retain both
  logical-cycle and wall-clock scope.
