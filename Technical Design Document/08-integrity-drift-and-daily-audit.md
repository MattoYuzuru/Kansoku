# TDD 08 — Daily integrity and drift engine

## Scheduler

One durable `daily-integrity` workflow runs at configurable local time with jitter and also in
reduced mode after startup/version change. A PostgreSQL advisory lock prevents concurrent runs.
Each step is idempotent, timeout-bounded and writes `audit_run/check` rows with versioned inputs.

Workflow state:

```text
scheduled -> running -> passed|degraded|failed|cancelled
                         \-> incident open/update
```

Crash recovery marks stale runs interrupted and safely retries eligible checks.

## Checks

### 1. Discovery and configuration

- executable/version/surface still resolvable;
- expected state roots readable;
- hooks/OTel/endpoints enabled and trusted where applicable;
- config fingerprint equals applied plan or drift is explained;
- plugin/skill/MCP inventory snapshot completes within bounds.

### 2. Source freshness

Track watermark and eligible activity independently. Rules:

- no agent/session/process evidence + no events = inactive, not failed;
- eligible activity + stalled source = gap incident;
- missing eligibility evidence = unknown after threshold;
- source with declared heartbeats follows heartbeat SLO.

### 3. Schema/parser

- compare agent version and event fingerprint to compatibility registry;
- replay bundled active-version fixtures through sanitizer/normalizer;
- count new event/attribute shapes without storing values;
- verify parser version/checkpoint compatibility.

### 4. Synthetic pipeline

Send a uniquely tagged safe hook record and OTLP log/span/metric through public ingress. Verify
durable event, evidence, rollup and query appearance, then expire the synthetic facts by explicit
test namespace retention. This tests Kansoku without agent/provider cost.

### 5. Reconciliation

Reconcile recent completed sessions after terminal-delay window. Compare source counts/IDs and mark
time intervals. Track ratio, mismatch class and regression vs previous agent/adapter version.

### 6. Storage/operations/privacy

Check migrations, constraints, rollup watermark, queue/spool, partitions, disk forecast, backup age,
restore-test age, retention jobs, raw-content canary, auth/egress violations and resource budget.

### 7. Optional live canary

Per adapter recipe declares command, fixture workspace, expected event DAG, max turns/tokens/cost/
duration, cooldown and cleanup. Secrets remain in agent's normal auth path; Kansoku never copies
them. Canary events use a namespace and are excluded from personal usage metrics by default.

## Expected event graph

Canary success requires nodes and relations, not only count:

```text
session.started
  -> prompt.submitted
  -> component.invoked(canary)
  -> tool.called(mcp.echo)
  -> tool.succeeded
  -> component.succeeded
  -> session.stopped
```

Capabilities without native nodes have adapter-specific expected alternatives. Missing/extra/
misordered events create precise check failures.

## Drift fingerprints

Fingerprint source schema from safe structural metadata: event/type names, field paths and primitive
types after prohibited-field categorization—not values. Version fingerprints for executable, config
recipe, adapter, fixtures and formula registry. A change triggers targeted revalidation.

## Incident model

Incident key: installation + source + capability + failure class. Store first/last seen, affected
interval, severity, check evidence, agent/adapter versions, recovery criteria and user notes. Repeated
runs update one incident; recovery requires fresh positive evidence and closes without deletion.

## Health API

Return decomposed checks plus overall worst applicable state. `green/yellow/red/gray` is derived and
never stored as the only evidence. Dashboard can answer why and which metrics are affected.

## Fault-injection tests

- remove/disable/untrust hook;
- wrong OTLP port/protocol/auth;
- truncate/rotate/change transcript schema/permissions;
- active process with absent events;
- duplicate and stalled watermarks;
- parser panic/timeout/unknown field;
- delayed rollup, full disk, DB restart, corrupt spool;
- stale backup/failed restore/privacy canary;
- live canary partial DAG and provider timeout.

## Exit gate

Every advertised detection has evidence matching its declared level; only actual mutation/runtime
tests may claim measured end-to-end detection time. Audit runs are durable and non-overlapping;
incidents map to completeness intervals; canaries are bounded, excluded and private.

Current reconciliation: 17 component classifiers pass without an end-to-end SLO claim; 2
PostgreSQL-tagged deterministic mutation integrations passed on pinned PostgreSQL 18 and measured
actual scheduler-to-durable incident detection at `Incident.OpenedAt`. DB restart and failed restore
remain runtime-required. Therefore there is no aggregate 21-fault runtime claim and the complete
fault-injection exit gate is not reported green.

## Implemented design notes (2026-07-24)

- `AuditCheckKey` is `(audit_run_id, check_id, capability_id, installation_id, source_id)`;
  `source_id` is never encoded into the closed Adapter SDK capability vocabulary.
- Every selected stage gets a real context deadline from the locked registry. A pending row is
  written before evaluation; missing/unsupported evidence remains explicit rather than becoming a
  pass. Stage 8 treats an aged `late_events_pending` watermark as actionable even when current queue
  depth is zero; repair age remains conditional on non-zero depth.
- Stage 9 forecasts crossing the configured disk budget (default `0.90`) rather than physical
  exhaustion; the regression boundary is `0.89 -> 0.91` crossing `0.90`. Stage errors persist
  bounded categories, never raw error strings.
- Version-change runs use the union of per-fingerprint targeted stages: executable/adapter
  `1,2,3,4,7`; config `1,2`; fixture `4`; formula registry `8`; event schema `4,7`.
  Stages 9 and 10 are never silently pulled into reduced revalidation. Non-global changes also
  filter targets by source, capability and adapter identity; the durable baseline advances only
  after the targeted run passes.
- Structural schema fingerprints accept only event name, sorted field paths and the seven primitive
  types. The shared privacy categorizer rejects prohibited durable path segments before hashing.
- The Health API derives all nine dimensions from latest per-check/per-source evidence plus open
  incidents. Gray outranks green in the overall worst-applicable summary so missing evidence cannot
  disappear behind one fresh pass.
- Stage 10 contains no process-spawn or network client. Its fixture/simulation observer is the only
  executable Session 08 path; it measures all declared budgets, bounds even a non-cooperative
  observer, and stores credential confirmation, explicit consent and cooldown state in PostgreSQL.
  Production wiring validates the recipe adapter/capability against the shared registry. A real
  provider canary requires a future explicit opt-in.
- Stage 11 canonicalizes a versioned metadata-only report and atomically stores its SHA-256 plus
  device-local HMAC-SHA256 signature/key ID, Stage-11 check, incident reconciliation and terminal
  run state. Strict report loading rejects unknown fields and mismatched duplicated DB envelope
  columns. This proves local tamper detection, not public release signing.
- `NewProductionAssembly` validates the dependency graph before applying migrations or starting
  triggers. It rejects a FileStore-only synthetic check, a second PostgreSQL pool, incomplete
  rollup/storage/privacy dependencies, an enabled live canary without durable state/cleanup, and
  absent report signing.
- Integrity migration tables are namespaced (`integrity_audit_runs` /
  `integrity_audit_checks`) so an upgrade over the existing Session 04 schema cannot collide with
  or delete Session 04's separate `audit_runs` / `audit_checks` history.
- Parser replay is context-bounded and panic-contained. Structural shape collection deduplicates
  array element paths, is cardinality/order independent, rejects heterogeneous path types, and
  delegates to the same strict privacy-aware `EventSchemaFingerprint` function used by drift
  tracking.
