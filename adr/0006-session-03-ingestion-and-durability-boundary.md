# ADR 0006: Session 03 ingestion and durability boundary

- Status: accepted
- Date: 2026-07-21
- Owners: Session 03 core architecture

## Context

Session 03 must prove that hook, OTLP and transcript sources converge on one canonical fact without
waiting for the PostgreSQL data platform owned by Session 04. It must also obey Session 02's reviewed
privacy contract: only typed safe values may cross a durable boundary, compressed ingress remains
rejected until streaming decompression/bomb limits are reviewed, and local ingestion reuses the
loopback/auth route boundary.

An in-memory receipt cannot prove crash/restart or checkpoint atomicity. Conversely, introducing a
provisional SQL schema here would pre-empt the partition, migration, rollup and query-budget work of
Session 04 and risk turning spike assumptions into an accidental production contract.

## Decision

1. The authoritative envelope, lifecycle, ingress and reconciliation contracts are the closed
   registries in `contracts/observability/`, locked by
   `contracts/observability-policy-locks.yaml`.
2. `internal/observability` uses a bounded typed file snapshot as the Session 03 durability spike.
   Every mutation writes a `0600` temporary file, fsyncs it, atomically renames it and fsyncs the
   directory. Event, evidence, idempotency outcome, watermark and importer checkpoint share that
   transaction. This is durable for the single-process spike; it is not called PostgreSQL, a
   multi-process database, or production backup/restore.
3. Fact identity is lane-independent. Evidence identity includes source kind and source idempotency
   identity. A replay increments evidence replay metadata and never fact cardinality. Removing or
   degrading a source recomputes completeness without deleting the fact or historical evidence.
4. OTLP/HTTP and OTLP/gRPC use the official binary protobuf messages for logs, metrics and traces.
   HTTP uses the standard `/v1/logs`, `/v1/metrics` and `/v1/traces` paths and
   `application/x-protobuf`; gRPC registers the official unary Export services. Both authenticate on
   loopback. Only registered safe attributes are extracted; log body, span name/events/links,
   descriptions and unknown attributes are never durable.
5. OTLP JSON is not implemented. Gzip is rejected consistently with the Session 02 privacy policy.
   OTLP 1.10.0 requires servers to support `none` and `gzip`, so Kansoku's receiver remains an
   Experimental, explicitly non-conformant spike until bounded streaming gzip is designed and the
   privacy contract receives a reviewed version transition. No Supported/Beta claim may cite this
   spike as full OTLP conformance.
6. Go protocol dependencies are exact versions, recorded in `go.sum`, vendored for offline builds
   and enumerated in `reports/session-03-sbom.json`. Verification uses the immutable Go 1.26.5 image
   from Sessions 01–02 with network disabled.

## Consequences

- Session 04 must replace the snapshot with PostgreSQL unique constraints and a real
  event/evidence/checkpoint transaction while preserving the contracts and replay fixtures.
- Snapshot rewrite cost is quadratic across many inserts and the 16 MiB vendored dependency tree is
  larger than the Session 02 stdlib-only core. The snapshot is bounded and load-tested only as a
  correctness spike, not accepted for production throughput.
- A durable sanitized client spool exists as a typed `0600` primitive. A future generated hook
  client may use it only after local sanitization; server code never spools raw failed requests.
- Rollup persistence/recomputation, PostgreSQL backup/restore, multi-process locking, gzip, partial
  OTLP success, TLS/non-loopback deployment and signed provenance remain outside this session.

## Rejected alternatives

- **Acknowledge after decode and keep state in memory:** cannot satisfy crash/restart or checkpoint
  atomicity.
- **Persist raw OTLP then sanitize asynchronously:** violates the Session 02 boundary.
- **Add a provisional SQLite/PostgreSQL schema:** duplicates Session 04 and creates migration debt.
- **Claim full OTLP support while rejecting gzip:** contradicts the protocol specification.
- **Import an entire Collector distribution:** expands supply chain and runtime scope without
  improving the canonical-domain proof.
