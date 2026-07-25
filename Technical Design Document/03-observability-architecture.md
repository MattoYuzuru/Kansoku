# TDD 03 — Canonical ingestion architecture

## Implemented boundary

`internal/observability` is the executable Session 03 spike. Its `Event`, `Evidence`, `Fact`,
`Correlation`, `Quarantine`, `Incident`, `Watermark` and `Checkpoint` types are closed durable
allowlists. The machine-readable authority is `contracts/observability/`; semantic digests are
version-locked. The implementation depends on the Session 02 sanitizer and local HTTP guard rather
than defining a second privacy/authentication boundary.

The spike uses a bounded fsync/rename file transaction because Session 04 owns PostgreSQL. An
acknowledged hook/OTLP event and a committed transcript checkpoint have survived a durable atomic
revision; in-memory decode is never acknowledgement. ADR 0006 defines exact guarantees and
non-guarantees.

## Components

- **Ingress server:** OTLP gRPC/HTTP, hook HTTP and adapter batch endpoints.
- **Sanitizer/normalizer:** source schema selection, approved feature extraction, canonical mapping.
- **Durable writer:** transactional event/evidence/idempotency persistence.
- **Importer workers:** checkpointed read-only JSONL/config/inventory scans.
- **Correlator:** resolves scoped identities and bounded candidate relationships.
- **Reconciler:** capability-specific expected-vs-observed rules and completeness intervals.
- **Rollup worker:** late-aware hourly/daily aggregates.
- **Audit engine:** watermarks, drift, fixtures, synthetic/live canaries and incidents.
- **Query API/UI:** completeness-aware analytics.

MVP MAY run these in one Go process with isolated packages/queues. Boundaries remain explicit so an
expensive worker can be split later without changing contracts.

## Ingestion protocols

### OTLP

Support OTLP/gRPC and OTLP/HTTP protobuf first; JSON MAY follow. Bind host loopback. Decode resource,
scope and log/span/metric records, then route by adapter/source fingerprint. Unknown resource
service names are rejected or stored only as safe unsupported-source metadata.

Implemented: official Go protobuf messages and unary services for log, metric and trace Export;
binary-protobuf HTTP on the three standard signal paths; one MiB preallocation/body limits;
loopback bearer authentication; resource/schema selection; and closed safe attribute extraction.
Prohibited body/name/event/link/description/unknown-attribute surfaces are discarded before the
typed event exists. OTLP JSON is absent. Gzip is explicitly rejected under the reviewed Session 02
compression policy, so the spike does not claim complete OTLP 1.10.0 conformance.

### Hooks

`POST /v1/hooks/{adapter}/{event}` accepts bounded JSON with local auth. The generated hook client
reads stdin, calculates prompt-safe features where required, sends with short timeout and never
blocks the agent solely because Kansoku is unavailable. Failed sends append already-sanitized
records to a bounded local spool with `0600` permissions.

The server returns retryable `503` without retaining the raw request. `DurableSpool` accepts only a
typed `CommitRequest` containing already-sanitized event/evidence and enforces its byte bound and
`0600` mode; it is the primitive for a future generated hook client, not a raw server retry queue.

### Adapter batches/import

External adapters emit NDJSON frames over stdin/stdout or loopback gRPC in a later SDK. Frames
contain sanitized source records plus checkpoint proposals. A checkpoint commits only in the same
transaction as accepted normalized events.

The implemented transcript importer opens a regular file read-only, seeks only to its durable
file-identity-bound checkpoint, enforces a one MiB line cap and commits each accepted
event/evidence/checkpoint atomically. Adapter stdin/stdout belongs to Session 05.

## Acknowledgement and durability

An event is acknowledged after idempotency key and sanitized envelope/evidence are committed to
PostgreSQL or the bounded durable spool. In-memory receipt is insufficient. Backpressure returns a
retryable status without logging payloads.

For Session 03, “committed” specifically means the typed bounded snapshot was written to a `0600`
temporary file, file-fsynced, atomically renamed and directory-fsynced. Crash injection before sync
or rename exposes the previous revision; a crash after rename exposes the complete next revision on
restart. Session 04 replaces this with PostgreSQL, not with an in-memory receipt.

## Normalization pipeline

1. Authenticate/local-source identify.
2. Enforce byte/rate/depth limits.
3. Select adapter + versioned source schema.
4. Extract safe metadata and redact prohibited fields.
5. Validate canonical event.
6. Resolve/create installation, session and component dimensions.
7. Calculate idempotency key.
8. Insert event/evidence/checkpoint transactionally.
9. Queue correlation/reconciliation/rollup keys.
10. Update source watermark/health.

## Correlation

Adapters provide strategies ordered by strength: native relation ID, exact scoped identifiers,
deterministic mapping, bounded temporal candidate set, inference. The correlator can store multiple
candidates. A threshold never hides ambiguity; `correlation_status` is exact/candidate/ambiguous/
unmatched.

## Reconciliation

Rules are per adapter version and capability. Example:

```yaml
capability: tool_calls
window: session
expected:
  primary: hook.post_tool
  corroborate: [otel.tool_result, transcript.tool_call]
tolerance:
  count_delta: 0
  terminal_delay: 30s
```

A mismatch opens or updates an incident and marks the affected interval partial. Later events can
recover health but do not erase incident history.

The fixture capability requires hook, one OTLP signal and transcript evidence for `complete`.
Duplicate delivery increments replay metadata on the existing evidence. Source disabled/degraded/
error removes that lane from current completeness without deleting its evidence. Conflicting
outcome/value/type evidence opens `evidence_contradiction` and retains the first fact pending an
adapter-specific resolution rule.

## Watermarks and gaps

Each source tracks last discovered/read/emitted/observed/committed sequence, last eligible agent
activity and expected cadence. A silent source is not unhealthy during true inactivity. Process/
session/import freshness is used only as evidence; missing eligibility remains `unknown`.

## Failure recovery

- restart resumes importer checkpoints and durable spool;
- duplicate replay attaches replay evidence without count inflation;
- partial transaction commits are impossible;
- unknown schema becomes metadata-only quarantine + degraded interval;
- rollups are recomputed from earliest affected bucket after late data;
- poison events are bounded and cannot halt unrelated sources.

## Tests

Golden source fixtures, protocol conformance, idempotent replay, crash-at-each-stage, reorder/late/
duplicate/property tests, clock skew, multi-source contradictions, source silence, unknown schema,
privacy canaries and load/backpressure tests.

## Exit gate

One logical multi-source scenario remains correct through replay, crash and reordering; source loss
changes completeness; sanitization precedes every durable step; all contract tests are automated.

Implemented evidence covers: shared three-lane convergence; duplicate/reorder/late/property load;
clock skew; exact/candidate/ambiguous/unmatched correlation; source disable; eligible stall versus
true inactivity; contradiction; unknown schema; poison/backpressure; durable spool; checkpoint
atomicity; three crash stages; HTTP and real loopback gRPC protobuf routes for all three signals;
and Session 02 ten-sink privacy regressions. Production rollups, PostgreSQL, backup/restore and
adapter version support remain later-session gates.

## Native adapter record path (reconciled 2026-07-25)

For matched real resources, the receiver resolves the plain `event.name`, translates only the
adapter’s closed safe slots, preserves integer/boolean types, and then invokes the shared
sanitizer/normalizer/durable sink. Unsupported event names and incompatible countable-event shapes
quarantine metadata. Documented metadata-only events become `source.observed`; non-terminal Codex
SSE records follow that path, while a token-bearing completed record becomes `model.responded`.
This prevents normal exporter chatter from inflating unknown-schema incidents.
