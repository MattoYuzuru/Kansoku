# Wave F reconciliation — Fleet IA and model analytics

Date: 2026-07-31  
Scope: R07 and the visualization portion of R06  
Implementation state: complete in the working tree; local commit and production replacement are
blocked by the managed sandbox controls described below.

## Exit-gate result

- `/agents` remains the canonical route and is presented as **Fleet**. `/agents/:id` remains the
  installation root and exposes the hierarchy
  `Fleet → Installation → Models / Sources / Components / Incidents`.
- Installation model rows and global model rows link to
  `/models?model=<percent-encoded-id>`. Reserved characters round-trip through the query parser.
  The parser rejects an empty filter but preserves the durable `TEXT` identifier domain, including
  long and control-character-bearing identifiers, so every returned row remains addressable.
  Query failures render a retryable error and are never mislabeled as `not_observed`.
- `/models` remains the canonical unfiltered global cross-installation comparison. A selected
  model adds a drill-down panel; it does not remove or filter the global table. The harness now
  checks SPA navigation, direct entry, reload, browser Back and global-row preservation.
- No new donut was introduced. Existing line, stacked/grouped bar and comparison-table forms remain
  the primary analytical views.
- Every model context now displays cost coverage, success/failure outcome coverage and explicit
  outcome exclusions. Cancellation, interruption and unknown outcomes remain outside failure and
  are not silently coerced.
- `model_breakdown/2` adds exact registered success/failure counts to per-model
  response/token/cost rows.
  Query-contract 1.9.0 and append-only lock `data-platform.query-contract/13` record the semantic
  transition. The set-based cost query and 150 ms budget are unchanged.
- ADR 0022 records the stable-route, encoded-query and global-vs-installation scope decisions.
  Engineering Proposal 13 and TDD 13 are reconciled with the implemented behavior.

## Validation evidence

- `python3 scripts/validate_data_platform.py --runtime-only`: pass, including migrations,
  replay/restore/privacy checks, the 5,000-response Models regression and 330,000-event profile
  skew; suite time reported by Go was 36.268 s.
- `python3 scripts/validate_data_platform.py --contracts-only`: pass.
- `python3 scripts/validate_contracts.py --formula-history-ref HEAD` and
  `python3 -m unittest tests.test_contracts`: pass.
- All static adapter, Codex, Claude, component-evidence, incident, integrity, MCP, observability,
  plugin, privacy and runtime contract validators: pass.
- `go vet ./...`: pass.
- `go test -race ./internal/dataplatform ./internal/runtime`: pass.
- `go test ./...` passed every package except two existing observability tests whose attempt to
  bind `127.0.0.1:0` was denied by the managed sandbox. Re-running the observability package while
  excluding only those two socket-owning tests passed in 16.770 s.
- Frontend normalization, catalog, formatter, glossary, theme, range and model-drilldown tests:
  pass. The model suite includes reserved-ID round-trip, direct selection, unfiltered global state,
  empty-input rejection, long/control-character identifier round-trip, query-error state
  separation and canonical route assertions.
- TypeScript typecheck, 44 accessibility-token checks, production build and byte-for-byte
  `web/dist` / `internal/webui/dist` parity: pass.
- The post-review production candidate `kansoku:wave-f2-20260731` built successfully with
  byte-for-byte embedded-bundle parity as
  `kansoku@sha256:ef16ea218e0c1738458013136f13b081d26689f09a4d9043ac13084cbdb06597`.
- `node --check reports/artifacts/2026-07-30/browser-research.mjs`: pass.

## Deployment and browser evidence boundary

The rollout preview limited the change to service `kansoku`, retained PostgreSQL and every volume,
and named `kansoku:wave-e5-20260730` as rollback. The final image build passed, including Docker's
embedded-bundle parity gate. Task-scoped Compose replacement attempts were then rejected before
mutation because the managed sandbox denied the Colima Docker socket. The existing Wave E service
was not replaced.

The same sandbox denied loopback access and Chrome exited before opening its DevTools endpoint.
The saved Wave E browser evidence and screenshots were therefore not overwritten and are not
claimed as Wave F runtime proof. `browser-research.mjs` and its manifest now explicitly distinguish
the previously executed baseline from the syntax-checked Wave F assertions.

## Resource, privacy and retention review

- The model query adds two filtered counts to an already materialized per-operation population. It
  adds no scan, index, migration, retained row or query fan-out; the 5,000-response budget test
  remains green.
- URL state contains only an already displayed model identifier. It contains no prompt, response,
  tool content, source code, environment, credential or filesystem path.
- No telemetry, source evidence, incidents, manifests, user configuration, backup or retention
  class was mutated. The failed Compose attempts occurred before replacement.
- The drill-down reuses the existing two model queries and keeps the global table mounted; it adds
  no backend request and no persistent browser storage.

## Residual risks

- A post-deploy authenticated API/UI reconciliation and responsive Chrome rerun remain required
  when the Docker socket, loopback and Chrome process are available to the execution environment.
- The managed sandbox also changed `.git` to read-only. The required local commit failed while
  creating `.git/index.lock`; no commit or push is claimed.
- Component and incident destinations from an installation remain explicitly global until exact
  installation-filtered list contracts exist.
- The existing ECharts chunk-size warning and previously recorded high-severity npm audit finding
  remain; no dependency rewrite was authorized in Wave F.
