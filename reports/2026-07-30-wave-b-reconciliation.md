# Wave B reconciliation — 2026-07-30

## Scope and result

Wave B is implemented in two source commits:

- `f144ff2` — initialized backend Skill/Plugin profile collections, legacy-null normalization at
  the query boundary, and defensive detail aggregation;
- `5834fd1` — root/route render containment, visible query Retry/Back states, Wouter Reliability
  navigation, production bundle, and browser regression evidence.

No raw response, prompt, source, tool payload, environment value, credential, or host path is
stored. The work does not rewrite telemetry, incidents, migration state, or agent configuration.

## Contract and browser evidence

- Go JSON contract: empty Skill collections (`assertions`, `sources`, `file_tree`) and Plugin
  collections (`children`, `versions`, `assertions`, `sources`) serialize as arrays.
- Frontend unit regression: every corresponding legacy `null` collection normalizes to `[]`;
  direct merge helpers also contain unnormalized nulls.
- Live Chrome 150 malformed fixture: three Skill profile responses were response-intercepted with
  nullable collections; the profile rendered, the shell remained present, and no exception or
  error fallback appeared.
- A controlled route-render exception retained the shell, exposed Retry and Back, and recovered
  through Retry. The root boundary provides the equivalent non-empty recovery surface above the
  shell.
- Existing Agent profile 503 responses now resolve to a visible `role=alert` Retry/Back state
  after the bounded query retries instead of remaining at `Loading`.
- Reliability tab changes preserve an in-memory sentinel through two SPA transitions and browser
  Back. A direct incident URL and full refresh render the same tab with the shell present.
- The final harness reports zero unexpected runtime exceptions and zero failed network requests.

Evidence is recorded in `reports/artifacts/2026-07-30/browser-evidence.json`.

## Verification

- profile collection Go regression and targeted dataplatform/runtime/webui tests: pass;
- component catalog and nullable normalization tests: pass;
- formatter, glossary, theme and range-preference tests: pass;
- TypeScript typecheck: pass;
- accessibility token verification: 44 checks, minimum ratio 4.5399:1;
- production build and embed/dist byte parity: pass;
- desktop, tablet, mobile and 200%-equivalent KPI containment: no overflow.

## Resource and retention review

Initialization and normalization allocate only bounded empty arrays or shallow response copies.
Error boundaries retain no thrown values and deliberately do not log them. SPA navigation removes
document reload work. There are no new database rows, collectors, network egress paths, or
retention classes.

## Residual risks

- Agent profile requests still exceed/fail their current query path and remain Wave C.
- Source rollout health, telemetry false positives, child attribution and terminal outcomes remain
  Wave D.
- Native Reliability selects and cursor paging remain until the Wave E workbench.
- The Vite dependency finding remains R14.
