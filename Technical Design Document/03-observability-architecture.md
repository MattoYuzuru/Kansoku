# TDD 03 — Canonical ingestion architecture

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

### Hooks

`POST /v1/hooks/{adapter}/{event}` accepts bounded JSON with local auth. The generated hook client
reads stdin, calculates prompt-safe features where required, sends with short timeout and never
blocks the agent solely because Kansoku is unavailable. Failed sends append already-sanitized
records to a bounded local spool with `0600` permissions.

### Adapter batches/import

External adapters emit NDJSON frames over stdin/stdout or loopback gRPC in a later SDK. Frames
contain sanitized source records plus checkpoint proposals. A checkpoint commits only in the same
transaction as accepted normalized events.

## Acknowledgement and durability

An event is acknowledged after idempotency key and sanitized envelope/evidence are committed to
PostgreSQL or the bounded durable spool. In-memory receipt is insufficient. Backpressure returns a
retryable status without logging payloads.

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

