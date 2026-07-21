# TDD 01 — Product contracts and SLO harness

## Required artifacts

Session 01 creates versioned machine-readable registries before feature code:

- `contracts/glossary.yaml` — canonical terms and forbidden aliases;
- `contracts/capabilities.yaml` — capability identifiers and lifecycle states;
- `contracts/metrics.yaml` — metric IDs, units, formula versions and dimensions;
- `contracts/formula-version-locks.yaml` — review-controlled append-only formula identities;
- `contracts/slo.yaml` — targets, windows, exclusions and reference hardware/load;
- `contracts/dashboard.yaml` — route/panel/metric ownership;
- `adr/0001-*.md` — measured technology baseline.

These files later generate API enums, documentation checks and UI formula help.

## Canonical lifecycle

```text
installed -> enabled -> exposed -> invoked -> loaded -> executed -> succeeded
                     \-> opportunity_detected (parallel inferred evidence)
```

- `installed`: an inventory source confirms an artifact/version exists.
- `enabled`: active agent configuration permits it.
- `exposed`: evidence says it was available to this session/model. Inventory alone is insufficient.
- `invoked`: explicit native/tool/user invocation or supported activation evidence.
- `loaded`: instructions/resources were actually loaded when observable.
- `executed`: a uniquely attributed script/tool/MCP/hook action occurred.
- `succeeded`: the component-specific terminal contract succeeded; task success remains separate.

Events may skip unobservable stages, but the system MUST NOT synthesize missing stages as facts.

## Outcome model

Sessions/turns/components use: `succeeded`, `failed`, `cancelled`, `interrupted`, `timed_out`,
`abandoned`, `unknown`. Outcome source and confidence are mandatory. `Stop` is not automatically
`succeeded`.

## Requirement IDs

- `KAN-COL-001`: acknowledged supported events are durable and idempotent.
- `KAN-COL-002`: known source gaps become incidents; charts receive completeness intervals.
- `KAN-PRV-001`: prohibited content never crosses durable ingress.
- `KAN-ADP-001`: a new adapter uses capability interfaces without core agent-name branching.
- `KAN-MET-001`: every metric exposes formula, population, provenance and completeness.
- `KAN-UX-001`: global time/filter/comparison semantics are consistent across routes.
- `KAN-OPS-001`: one local Compose stack survives restarts and supports verified restore.
- `KAN-AUD-001`: daily audit detects all claimed fault classes within defined SLO.

All later TDD tests reference these or more specific child IDs.

## Reference load profile

Define three reproducible profiles:

| Profile | Events/day | Components | Retention | Purpose |
|---|---:|---:|---:|---|
| personal | 10k | 500 | 1 year | expected use |
| enthusiast | 100k | 5k | 3 years | comfortable headroom |
| stress | 1M | 20k | 5 years synthesized | design validation |

Distributions MUST include bursts, late/out-of-order/duplicate events, multiple timezones, DST,
unknown schemas, long idle gaps and agent upgrades.

## SLO measurement

SLOs are computed by Kansoku about Kansoku and stored separately from user activity. Each SLO has:

- service level indicator query;
- rolling window;
- reference profile/hardware;
- allowed exclusions (explicit maintenance only);
- completeness requirement;
- error budget and alert threshold.

The bootstrap registry encodes allowed exclusions as stable codes and completeness as a policy plus
required evidence scopes. Each required scope resolves to `measured`, `excluded` or `missing`.
`measured` requires at least one eligible, complete, non-excluded row for that exact scope; an
ineligible row cannot satisfy the requirement. `missing`, incomplete evidence and unauthorized
exclusions produce `unknown` plus a failing gate. An authorized exclusion may cover a whole scope
only when its contract explicitly allows that effect; the result then exposes the scope and
exclusion count as `partial` plus a failing gate, never a false pass. In particular, an unscanned
raw-content sink cannot disappear through a SQL `WHERE` clause and become a passing numeric zero.

Initial candidates:

```text
live_ingest_latency_p95 <= 10s
rollup_freshness_p95    <= 120s
common_query_p95        <= 500ms
daily_drift_detection   <= 24h
active_source_gap       <= 5m after eligible activity
raw_content_persisted   == 0
```

## Technology spikes

### Backend

Implement equivalent bounded OTLP HTTP decode + batch insert + health endpoint in Go and Python.
Measure image size, cold/idle RSS, CPU, throughput, shutdown flush, implementation complexity and
dependency surface. Go is accepted only if the measured whole-product trade-off wins.

### Database

Run query/replay/concurrency/migration spikes on PostgreSQL and SQLite with personal/enthusiast
profiles. Keep raw results and ADR rationale. ClickHouse/DuckDB receive paper evaluation unless
PostgreSQL fails a requirement.

### Frontend

Prototype one dense time-series page and one component funnel with candidate chart libraries.
Measure bundle, accessibility, linked brushing/filtering and export behavior.

## Contract tests

- metric registry has unique stable IDs and valid referenced dimensions;
- every dashboard panel references registered metrics;
- every formula version has a stable population ID, an exact typed evaluator and semantic SHA-256
  binding its registry population/expression/policy to deterministic normalized-record fixtures and
  an independent version lock; those fixtures cover filtering, dedupe, selection by a preclassified
  `in_interval` flag, completeness, exclusions and ordering where relevant, and p95 matches
  PostgreSQL `percentile_cont`;
- lifecycle state rules reject invalid inferred promotion;
- every SLO has a runnable SLI query and test load;
- glossary terms and support labels are consistent across docs.

The formula harness proves registry-bound aggregation over normalized metric records. It does not
derive `in_interval` from timestamps and therefore does not prove exact `[from,to)` boundary
behavior, production raw-event parsing, lineage derivation or SQL; those gates remain in Sessions
03–04. ADR 0003 defines bootstrap review trust and the post-commit append-only history check.

## Exit gate

Approved contract registries, benchmark artifacts and ADRs exist; generated checks pass; the next
session can write privacy tests without resolving product semantics. This automated gate does not
authorize public Supported/Beta claims: bounded privacy/replay/audit/end-to-end evidence and two
independent approved human classification reviews remain a separate blocking governance gate under
ADR 0002.

## Implemented Session 01 artifacts

- Product and privacy defaults: `contracts/product.yaml`.
- Required registries: `contracts/glossary.yaml`, `contracts/capabilities.yaml`,
  `contracts/metrics.yaml`, `contracts/formula-version-locks.yaml`, `contracts/slo.yaml`, and
  `contracts/dashboard.yaml`.
- Executable checks: `scripts/validate_contracts.py` and `tests/test_contracts.py`.
- Deterministic lifecycle, formula and SLI loads: `tests/fixtures/session-01/`.
- Reproducible spikes and raw measurements: `benchmarks/session-01/`.
- Measured decision: `adr/0001-technology-baseline.md`.
- Gate interpretation: `adr/0002-session-exit-and-support-governance.md`.
- Formula identity and proof boundary: `adr/0003-formula-version-identity-and-proof-boundary.md`.
- Exit reconciliation: `reports/session-01-reconciliation.md`.

The bootstrap registries use the JSON subset of YAML 1.2 so checks have no package-manager
dependency. Production schema/formula generators may later use native YAML syntax without changing
the versioned registry semantics.
