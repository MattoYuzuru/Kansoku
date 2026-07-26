# Kansoku architecture map

Fast orientation for an agent picking up a task in this repo. Read this **before** opening
`README.md`, `ROADMAP.md`, an Engineering Proposal, or a Technical Design Document (TDD) — it
tells you which of those to open for the module you're actually touching, instead of reading all
of them. This file describes structure (what exists, where); it does not restate the normative
rules already in `AGENTS.md` or the contracts — go there for those.

Kansoku (観測) is a local-first observability platform for AI coding agents (Codex, Claude Code,
...): Go backend + PostgreSQL + a React/TS dashboard, one Docker Compose stack, no raw
prompts/responses/code ever persisted. Full pitch: `README.md`.

## How the pieces fit together

```
Codex / Claude Code (real agent processes)
   │  OTel (OTLP HTTP/gRPC)         hooks (stdin JSON)        rollout/transcript files (disk scan)
   ▼                                    ▼                            ▼
internal/observability (OTLP+hook ingress, envelope normalize)   internal/codexadapter
   │                                                              internal/claudeadapter
   ▼                                                              (parse+reconcile source-specific
internal/privacy (sanitize/redact, typed SafeRecord/SafeError)     evidence into canonical events)
   ▼
internal/dataplatform (Postgres: ingest, partitions, rollups, retention, queries)
   ▼                                                     ▲
internal/localhttp (auth'd HTTP API + dashboard static)  │  internal/integrity (daily audit, drift,
   ▼                                                     │  health, incidents, live-canary)
web/ (React dashboard SPA)  ← served by internal/webui ──┘

internal/adaptersdk   — shared Adapter/Capability/HostView/ChangePlan contract both real adapters
                         implement; internal/adaptersdk/fakeadapter + /wayfinder are conformance-only
                         fixture adapters proving core never branches on agent name.
internal/installer     — simulate-only config-writer plans (OTel + hook install) for Codex/Claude;
                         shared Plan/Approval/SimulateApply/SimulateRollback machinery, no real
                         filesystem writer today (ADR 0002/0008).
internal/runtime       — wires everything above into the one appliance process (cmd/kansoku):
                         API, scheduler/jobs, backup/restore, secrets, queue, soak harness.
internal/crossagent    — test-only package: one logical scenario asserted once per real agent to
                         prove cross-agent portability of the canonical schema. No production code.
cmd/privacy-canary     — standalone binary, independently verifies no raw content reaches any sink.
```

## Module index (`internal/`, `cmd/`)

| Path | Purpose | Key files | Governing contracts |
|---|---|---|---|
| `internal/observability` | OTLP + hook ingress, canonical event envelope, normalization, durable spool | `otlp.go`, `routes.go`, `normalize.go`, `ingest.go`, `types.go` | `contracts/observability/*` |
| `internal/privacy` | Sanitization boundary: typed `SafeRecord`/`SafeError`, redaction, sinks | `sanitizer.go`, `classification.go`, `sinks.go` | `contracts/privacy/*` |
| `internal/dataplatform` | PostgreSQL schema, ingest, partitions, rollups, retention, durable component inventory/current state, all dashboard queries | `db.go`, `migrate.go`, `rollup.go`, `retention.go`, `inventory.go`, `component_inventory.go`, per-panel files (`activity_timeline.go`, `tool_analytics.go`, `mcp_topology.go`, ...) | `contracts/data-platform/*` |
| `internal/adaptersdk` | Shared `Adapter` interface, capability model, `HostView`, inventory graph, `ChangePlan` | `adapter.go`, `manifest.go`, `plan.go`, `hostview.go`; `fakeadapter/`, `wayfinder/` (conformance fixtures) | `contracts/adapter-sdk/*` |
| `internal/codexadapter` | Codex adapter: hooks, OTel, rollout files, discovery, skill/plugin/MCP evidence, reconciliation | `codexadapter.go`, `otel.go`, `hook.go`, `rollout.go`, `discover.go`, `reconcile.go` | `contracts/codex/*` |
| `internal/claudeadapter` | Claude Code adapter: same shape as codexadapter | `claudeadapter.go`, `otel.go`, `hook.go`, `discover.go`, `reconcile.go`, `transcript.go` | `contracts/claude/*` |
| `internal/crossagent` | Cross-agent invariant test only — no production code | `crossagent_test.go` | `contracts/cross-agent/*` |
| `internal/installer` | Config-writer plan protocol shared by both adapters' `PlanConfiguration` (OTel + hook targets); simulate-only | `protocol.go` | `contracts/privacy/installer.yaml`, `contracts/adapter-sdk/capabilities.yaml` |
| `internal/integrity` | Daily audit: drift/schema/freshness/health checks, incidents, fault injection, live canary, backup cycle | `check.go`, `drift.go`, `health.go`, `incident.go`, `livecanary.go`, `scheduler.go` | `contracts/integrity/*` |
| `internal/localhttp` | Local HTTP server security: auth (bearer tokens), CSRF, loopback binding | `security.go` | `contracts/privacy/deployment.yaml`, `contracts/runtime/auth-and-plans.yaml` |
| `internal/runtime` | Assembles the appliance process: API surface, jobs/scheduler, read-only inventory collector, backup/export, secrets, queue, soak harness | `assembly.go`, `api.go`, `inventory.go`, `jobs.go`, `backup.go`, `secrets.go`, `soak.go` | `contracts/runtime/*` |
| `internal/webui` | Embeds the built `web/dist` SPA (Go `embed`) and serves it | `webui.go`, `dist/` (generated, do not hand-edit) | — |
| `cmd/kansoku` | Main binary entrypoint; wires `internal/runtime` assembly, exposes `soak` subcommand | `main.go`, `soak.go` | — |
| `cmd/privacy-canary` | Standalone binary: independently verifies no raw content reaches any sink | `main.go` | `contracts/privacy/sinks.yaml` |

## Frontend (`web/`)

React 19 + wouter + TanStack Query + ECharts SPA, built with Vite, embedded into the Go binary via
`internal/webui`. **This area has other in-flight agent work as of 2026-07-25 — check current git
status before editing here.**

| Path | Purpose |
|---|---|
| `web/src/pages/` | One file per dashboard route (`Overview.tsx`, `Activity.tsx`, `Agents.tsx`, `Tools.tsx`, `MCP.tsx`, `Models.tsx`, `Reliability.tsx`, `Privacy.tsx`, `Settings.tsx`, ...) — routes/panels are defined in `contracts/dashboard.yaml`, one row per page |
| `web/src/api/` | `client.ts` (HTTP client against `internal/localhttp`), `queries.ts` (TanStack Query hooks), `types.ts` |
| `web/src/components/` | Shared chart/table/panel primitives (`ChartContainer`, `DataTable`, `KpiCard`, `Panel`, `PercentageDisplay`, `StatusBadge`, `Switch`, ...) |
| `web/src/ui/`, `web/src/hooks/`, `web/src/lib/` | Icon set, `useRange` hook, formatting helpers |
| `web/src/generated/routes.ts` | Generated from `web/scripts/gen-routes.mjs` — don't hand-edit |
| `web/dist/`, `internal/webui/dist/` | Vite build output; `internal/webui/dist/` is a copy embedded into the Go binary via `go:embed` — both generated, not source |

## Contracts (`contracts/`)

Versioned, machine-validated registries — the actual product boundary. One directory per domain,
each closed and paired with a `*-policy-locks.yaml` (append-only trust root) and a
`scripts/validate_*.py`. Cross-cutting: `glossary.yaml` (terminology, incl. the
`unsupported`/`not_observed`/`redacted`/`unknown`/`numeric_zero` state model), `product.yaml`,
`dashboard.yaml` (route → panel → metric → question-id map), `metrics.yaml`, `slo.yaml`,
`capabilities.yaml`, `formula-version-locks.yaml`. Full explanation of each registry:
`contracts/README.md`. Domain subdirs: `adapter-sdk/`, `claude/`, `codex/`, `cross-agent/`,
`data-platform/`, `integrity/`, `observability/`, `privacy/`, `runtime/`.

## Documentation map — which doc answers which question

| Question | Go here |
|---|---|
| Why was this decision made, what alternatives were rejected? | `Engineering Proposal/NN-*.md` |
| What exactly must the implementation do — contracts, algorithms, failure behavior? | `Technical Design Document/NN-*.md` |
| What changed after implementation diverged from the plan? | `adr/000N-*.md` |
| What is the versioned, machine-checked invariant for domain X? | `contracts/<domain>/*.yaml` + `contracts/<domain>-policy-locks.yaml` |
| What does term/state Y mean precisely? | `contracts/glossary.yaml` |
| What was verified/measured at the end of a session, with what residual risk? | `reports/session-NN-reconciliation.md` (+ `session-NN-sbom.json`) |
| What's the latest known live-state / open bug picture? | Most recently dated file in `reports/` (not session-numbered) |
| Which official external agent API/version is a piece of code based on? | `SOURCES.md` |
| What's the per-session status and inter-session dependency order? | `ROADMAP.md` |
| What are the day-to-day working rules for this repo? | `AGENTS.md` |
| How do I run this locally / wire a real agent to it? | `README.md` |

`Engineering Proposal/`, `Technical Design Document/`, and `adr/` share the same session numbering
(01–20 for the first two; 12–20 are approved plans rather than implementation claims, and ADRs are
one-per-material-decision so the count doesn't match 1:1).
`tests/fixtures/session-NN/` holds the sanitized fixtures each session's contract tests replay
against; `benchmarks/session-01/` holds the one existing perf-benchmark harness.
