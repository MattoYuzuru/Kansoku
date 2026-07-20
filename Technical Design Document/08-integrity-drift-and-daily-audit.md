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

Every advertised detection has a fault test and measured detection time; audit runs are durable and
non-overlapping; incidents map to completeness intervals; canaries are bounded, excluded and private.

