# Backlog reconciliation — Waves A–F

Date: 2026-07-31  
Acceptance source: `reports/2026-07-30-next-agent-prompt.md`  
Branch: `fix/skill-observatory-cold-state-2026-07-26`

## Result

The R01–R13 backlog selected by the acceptance prompt is implemented. Waves A–E are committed and
were exercised in the local production appliance. Wave F is implemented, independently reviewed,
validated and packaged as a production candidate, but remains uncommitted and undeployed because
the managed environment denied `.git/index.lock`, the Colima mutation socket, loopback access and
the Chrome DevTools process.

| Wave | Backlog | Reconciled result | Evidence |
|---|---|---|---|
| A | R01, R05-format, R09, R12, R13 | Page-scoped range presets, shared metric formatting, glossary pulse, storage formatting/containment and theme-derived navigation colors | `b4e77e8`–`0712b98`; `reports/2026-07-30-wave-a-reconciliation.md` |
| B | R02, R11 navigation | Initialized/normalized collections, render/query failure containment and SPA Reliability navigation | `f144ff2`–`22d1f3c`; `reports/2026-07-30-wave-b-reconciliation.md` |
| C | R06 profile runtime | Exact snapshot profiles within the reviewed budget, corrected identity/class provenance and separate cost lanes | `41a5925`–`b19f723`; `reports/2026-07-30-wave-c-reconciliation.md` |
| D | R03, R08, R10 | Bounded oversized-record continuation, evidence-safe attribution, exact plugin child activity and terminal outcome matrix | `0881fb6`–`26fe5a5`; `reports/2026-07-30-wave-d-reconciliation.md` |
| E | R04, R05 semantics, R11 workbench | Versioned exact formulas, distinct reliability populations, signed keyset workbench and bounded visible-tab loading | `01502b2`–`e52dc80`; `reports/2026-07-30-wave-e-reconciliation.md` |
| F | R07, R06 visualization | Fleet/Installation hierarchy, exact URL-addressable model drill-down, retained global comparison and explicit cost/outcome coverage | Working tree; `reports/2026-07-31-wave-f-reconciliation.md`; ADR 0022 |

No task deleted canary installations, rewrote telemetry/incidents, mutated agent configuration, or
created a durable sink for prompts, responses, source code, tool payloads, environment values,
credentials or unredacted paths.

## Final validation

- Data-platform runtime gate passed with migration, replay, restore, privacy, 5,000-model and
  330,000-event skew coverage; contracts-only and append-only formula-history gates pass.
- Runtime, privacy, adapter SDK, Codex, Claude fixture, component, incident, integrity, MCP,
  observability and plugin static validators pass.
- `go vet ./...` and race tests for `internal/dataplatform` and `internal/runtime` pass.
  `go test ./...` passes except two pre-existing observability tests that require a loopback bind
  denied by this sandbox; the package passes when only those socket-owning tests are excluded.
- Frontend catalog, formatter, glossary, theme, range and model-drilldown suites pass. The final
  drill-down suite is 6/6 and distinguishes transport failure from `not_observed`.
- TypeScript, 44 accessibility-token checks, production build and byte-for-byte
  `web/dist`/`internal/webui/dist` parity pass.
- Independent SDE review found two boundary defects and one documentation drift; all were fixed.
  The repeat review reports no remaining code or documentation findings.
- Final image:
  `kansoku@sha256:ef16ea218e0c1738458013136f13b081d26689f09a4d9043ac13084cbdb06597`.

## Runtime boundary and residual risk

The rollout preview changed only service `kansoku`, preserved PostgreSQL and every volume, and
retained healthy `kansoku:wave-e5-20260730` as rollback. Compose rejected the Wave F replacement
before mutation because access to the Colima Docker socket was denied. Consequently, saved Wave E
browser evidence remains the latest executed live evidence; Wave F deep-link, reload, Back,
responsive, theme and 200% assertions exist in the syntax-checked harness but still require a
post-deploy Chrome run.

Other explicit residuals:

- receive-to-durable-commit latency remains `not_observed` until both timestamps exist;
- installation-scoped Component and Incident lists remain global and are labeled as such;
- Kotlin compilation is `not_observed` because `kotlinc` is absent; Claude remains fixture-only by
  authorization;
- the existing large ECharts chunk warning and recorded high-severity development dependency
  finding remain outside this backlog;
- Wave F needs a local commit when `.git` becomes writable. No push is requested or claimed.
