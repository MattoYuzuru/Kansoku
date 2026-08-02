# Wave E reconciliation — Reliability and formulas

Date: 2026-07-30  
Scope: R04, semantic R05, Reliability workbench portion of R11  
Implementation commits: `01502b2`, `902007b`, `d420da8`, `87492ec`, `a68b715`, `b0c7755`

## Exit-gate result

- `model.error_ratio/1` is total failed terminal operations divided by total terminal operations
  for the selected interval. It is not an average of daily percentages. The API and KPI expose
  value, formula, numerator, denominator, excluded non-terminal/unknown outcomes and completeness.
- `collection_health_snapshot/2` separates receive-to-durable-commit latency, active-source
  observation age, replay evidence, five-minute late/backfill candidates and declared clock skew.
  Receive-to-commit is `not_observed` because the current event schema has no durable receive/commit
  pair. The historical `collection.ingest_latency_seconds/1` lock remains; version 2 carries the
  corrected semantics and explicit missing-timestamp exclusion.
- Incident and quarantine lists use backend-signed keyset cursors through infinite queries,
  IntersectionObserver plus an accessible Load more button, URL filters, per-URL scroll restore and
  a 200-row DOM ceiling. Native selects and document-replacing pagination are absent.
- Only the visible Reliability tab issues list/health queries, keeping the fixed local
  120-request/minute guard effective without hidden-tab request inflation.
- Unknown-schema acceptance remains the sanitized, version-pinned PostgreSQL fixture: replay
  reconciles one incident and manifest with append-only distinct occurrences and exact aggregate
  count; source, adapter, schema, parser, event type and fingerprint remain safe typed fields.
  Cursor tampering is rejected. No incident was deleted, mass-resolved or reinterpreted.

## Performance and live evidence

- The original Models cost lookup performed one full `cost_estimates` scan per response:
  1,849.465 ms and about 1.1 million shared-buffer hits at 7,988 responses.
- The set-based latest-cost relation measured 74.816 ms and 961 shared-buffer hits. Five live
  Models calls returned 200 in 74.434, 67.909, 67.814, 66.163 and 68.153 ms. The per-model
  leaderboard uses the same per-operation lookup and returned in 38.946–45.250 ms.
- The exact 337,293-event Activity contour measured 186.795 ms and retains a 250 ms ceiling rather
  than dropping sessions. Concurrent Reliability counts retain the same 250 ms evidence-backed
  ceiling; hidden-tab queries are disabled.
- Production image `kansoku:wave-e5-20260730` is healthy. Chrome 150 recorded 25 incident rows, one
  Load more fallback, zero native selects, zero legacy Next page links, zero layout overflows at
  desktop/tablet/mobile/200%, zero runtime exceptions, zero transport failures and zero non-200 API
  responses. All five agent profiles returned 200.

## Validation evidence

- `python3 scripts/validate_contracts.py --formula-history-ref HEAD`
- `python3 -m unittest tests.test_contracts`
- `python3 scripts/validate_data_platform.py --runtime-only`, including the 330k agent skew and
  5,000-response Models price/cost regression
- `python3 scripts/validate_runtime.py --runtime-only`
- Go unit tests for `internal/dataplatform` and `internal/runtime`
- frontend component catalog, formatter, glossary target, theme, range preference, a11y token,
  typecheck, production build and embed/dist parity gates
- live authenticated aggregate API calls and
  `node reports/artifacts/2026-07-30/browser-research.mjs`

## Resource, privacy and retention review

- Infinite pages retain bounded query-cache pages but mount at most 200 rows. Hidden tabs do not
  fetch. Scroll storage contains only a local Reliability query string and numeric offset.
- Set-based Models queries trade thousands of repeated scans for one bounded cost relation; they
  add no table, index, migration or retained data.
- The collection snapshot exposes only aggregate counts and states. No raw prompt, response,
  source code, tool content, error body, environment, credential or unredacted path gained a sink.
- No historical telemetry, manifest, occurrence, incident, triage state, agent configuration,
  backup or retention class was changed.

## Residual risks

- Receive-to-durable-commit latency remains `not_observed` until the schema can prove both
  timestamps per event.
- The five-minute arrival gap is a candidate late/backfill classifier, not a final causal label.
- Claude 2.1.197 remains fixture-only and was not launched.
- The frontend build still reports its existing large ECharts chunk warning and one high-severity
  npm audit finding; no forced dependency rewrite was authorized in this wave.
