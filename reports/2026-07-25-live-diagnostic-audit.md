# Live diagnostic audit — 2026-07-25

> **2026-07-25, later same day — superseding update.** Section 0 below predicted that a fresh,
> correctly-configured Claude Code/Codex process would resolve the empty dashboard. It did not. A
> follow-up live test (documented in full in `reports/2026-07-25-gap-inventory-and-fix-plan.md`,
> "Live-test findings" section) used a temporary debug build to observe raw OTLP resource attributes
> and found the **real root cause**: real Codex CLI (v0.145.0) sends `service.name = "codex_exec"`,
> not the `"codex_cli_rs"` literal `codexadapter.OTLPResourceServiceName` expects — a live, 100%
> reproducible mismatch, not stale test data. Separately, real Claude Code CLI produced **zero**
> trace at Kansoku's ingress across 5 independent invocations, most likely because its OTel metric
> export interval is a hardcoded 60000ms (confirmed from Claude Code's own `--debug-file` output)
> while every non-interactive `-p` test process lived only 10-20s — telemetry is very likely
> generated and then dropped on exit before its first scheduled flush, not blocked by Kansoku. Read
> the other report's "Live-test findings" section for the full evidence trail before trusting
> this document's optimistic §0 tl;dr.

Ad-hoc audit requested by the developer after observing an all-zero/"unsupported"/"unknown"
dashboard despite real Claude Code and Codex CLI sessions being configured against the running
stack. This is **not** a numbered project Session — it's a point-in-time forensic pass (DB, live
API, host agent configs, and source code) done with four parallel read-only investigation agents,
zero files modified. Companion doc: `reports/2026-07-25-gap-inventory-and-fix-plan.md` for the
consolidated backlog and next-step plan.

## TL;DR

The dashboard is not lying, and the ingestion pipeline is not structurally broken. What actually
happened:

1. The real test traffic that IS in the database (3 quarantine records + 2 incidents, all
   timestamped **09:30–09:48 UTC** today) was sent to an **older Docker image** — one built before
   the Session 11 OTLP-adapter-recognition fix (commit `1d8a26f`, `otlp.go` changes actually landed
   in `d66d280`) existed in a built container. That traffic was legitimately quarantined as
   `unknown_schema` by the pre-fix binary, exactly as the old (fixture-agent-only) code would do.
2. The current running container (`kansoku:local`, image `5915057db914`, built
   **2026-07-25T17:06:50+03:00 = 14:06:50 UTC**, started 17:07:06+03:00) **does** include the
   Session 11 fix — verified both by git history and by direct code read (see below). But **no new
   telemetry has been sent since that rebuild.** The dashboard is showing stale pre-fix quarantine
   artifacts, not a live failure of the current binary.
3. Separately, two **real, confirmed-live bugs** exist regardless of the above: `/api/v1/incidents`
   and `/api/v1/health` do not surface the incident/quarantine rows that genuinely exist in
   Postgres (see "Confirmed bug" below).
4. The previously-documented gap (`ingestJSON`'s `hook_http` path is fixture-schema-only) is
   real and independently re-confirmed by code read — it only matters if hook-based telemetry
   (not just OTel) is also configured; not verified either way this pass.

**The single highest-value next step is an operational one, not a code fix: run a fresh real
Claude Code or Codex session now, against the current (already-rebuilt) container, and see whether
it actually lands in `events`/`sessions` this time.** Everything below explains why that test is
the right next move and what to check if it still fails.

## 0. Live re-check during this very audit — updates the diagnosis

While writing this report (in an active Claude Code session, well after the 14:06 UTC image
rebuild, with dozens of tool calls executed), the database was re-queried: **still exactly 0
`events`, 0 `sessions`, and still exactly 3 `schema_quarantine_metadata` rows — no new rows of
either kind appeared.** This is a stronger signal than "stale pre-fix quarantine data": if this
session's own Claude Code process were actively exporting OTel telemetry, we'd expect either new
`events` rows (success) or new quarantine rows (rejection) by now — we got neither. That points at
telemetry **not reaching the ingress at all** during this session, not just being rejected once it
arrives.

The leading hypothesis: `~/.claude/settings.json` was edited at **12:28** today, but
environment-variable-driven telemetry config (`CLAUDE_CODE_ENABLE_TELEMETRY`, `OTEL_*`) is
typically read **once at process startup**. If the current Claude Code session's underlying
process started before 12:28, it would still be running with the old (pre-edit) env and never
attempt to export at all — indistinguishable from "broken" but fixed by simply starting a fresh
session. This could not be verified from inside this session itself (a subprocess's env doesn't
reflect the parent CLI process's own telemetry configuration). **Recommended first check for the
next pass: fully quit and restart both Claude Code and each Codex profile, then repeat P0.1's DB
watch during a brand new session** — this is cheaper and more informative than any code change.

## 1. What's actually in the database

Connected read-only via `docker exec kansoku-postgres-1 psql`.

- **Empty (0 rows) tables** — everything the dashboard's activity/session/token panels depend on:
  `events`, `event_evidence`, `sessions`, `turns`, `tool_calls`, `token_usage`, `model_operations`,
  `mcp_connections`, `agent_installations`, `component_installations`, `projects`, `providers`,
  `models`, `devices`.
- **Non-empty**: `schema_quarantine_metadata` (3 rows) and `incidents` (2 rows), all from today,
  09:30–09:48 UTC:
  - `otlp_log` / `unknown_schema` → 2 quarantine entries (12 and 3 records; 7581 and 2020 bytes)
  - `otlp_metric` / `unknown_schema` → 1 quarantine entry (0 records, 464 bytes)
  - `integrity_fingerprints` (11 rows, single snapshot at 14:07:07 UTC) shows
    `executable_version` = `not_observed` for both `claude` and `codex` — consistent with neither
    adapter ever getting past ingestion far enough to confirm which binary/version was talking to
    it.
- **Real DB size**: `10,483,391 bytes` (`10238 kB` pretty) — matches
  `/api/v1/system/snapshot`'s `database_size_bytes` exactly. This is genuinely tiny because only
  quarantine/incident/fingerprint metadata exists; it is not a display bug. (The developer's "10
  million 450 thousand" reading was the byte count, not an error — just an unlabeled unit in the
  UI, worth a cosmetic fix.)

## 2. What the live API actually returns

Curled `http://127.0.0.1:43100` with the real read bearer:

- `GET /api/v1/health` → queues empty, **`open_incident_count: 0`** — despite 2 real rows in
  `incidents`. **Confirmed bug.**
- `GET /api/v1/incidents` → **`"data": []`** — despite 2 real rows in `incidents`. **Confirmed
  bug.**
- `GET /api/v1/completeness` → `"completeness": "unknown", "denominator": 0, "numerator": 0` —
  correct per contract (`unknown` = can't establish a denominator from available evidence); not a
  bug, since `events` really is empty.
- `GET /api/v1/system/snapshot` → `"completeness": {"status": "degraded", "covered_ratio": 0}`,
  `database_size_bytes: 10483391` — correct, real query.
- `GET /api/v1/analytics`, `/api/v1/reliability/counts`, `/api/v1/activity`,
  `/api/v1/models/usage` → all returned `"error": "invalid_analytics_range"` when queried with
  `range=day`. This is a **caller-side param mismatch** in how the investigating agent queried it
  (the API requires explicit RFC3339 `from`/`to`, not a `range=` shorthand) — confirmed by reading
  `internal/runtime/api.go` (~lines 152-159), not a server bug. **Open question, not yet checked
  this pass:** does the actual frontend (`web/src`) always construct `from`/`to` correctly for
  every panel, or could a similar param mismatch be why some UI panels show "unsupported" even
  where the API itself would answer correctly? Worth a targeted check before writing this off.

## 3. Image/container freshness — ruled out as a cause

- `docker images kansoku:local` image ID (`5915057db914`) matches exactly what
  `kansoku-kansoku-1` is running (`docker inspect .Image`) — no stale cached layer running under a
  fresh tag.
- Image built 2026-07-25T17:06:50+03:00; container started 17:07:06+03:00, right after build.
- Latest commit (`85c1fe0`, 17:10:13+03:00) only adds the LICENSE file — no Go source changed since
  `1d8a26f` (16:56:44+03:00), which the running image does include.
- **Conclusion: the running binary is current.** The empty/quarantined dashboard reflects a test
  that predates this build by several hours, not a failure of the current code.

## 4. Host agent OTel configuration — structurally correct

Three real config files found and read in full:

| Profile | Path | Endpoint | Protocol | Bearer |
|---|---|---|---|---|
| "home" | `~/.codex-home/config.toml` | `http://127.0.0.1:4318/v1/logs` | `binary` | matches current `ingress_bearer` |
| "tabunk" | `~/.codex-tbank/config.toml` (via a `tbank()` shell function setting `CODEX_HOME`) | `http://127.0.0.1:4318/v1/logs` | `binary` | matches current `ingress_bearer` |
| Claude Code | `~/.claude/settings.json` | `http://127.0.0.1:4318` | `http/protobuf` | matches current `ingress_bearer` |

- Bearer token **match confirmed live**: `POST /v1/logs` with no `Authorization` → `401`; with the
  token read from `deploy/secrets/ingress_bearer` → `200`.
- Token freshness: `deploy/secrets/ingress_bearer` (Jul 25, 11:59) predates all three config edits
  (12:27–12:48) — the configs were written *after* the current secret existed, so there's no
  stale-token risk.
- No project-local `.claude/settings.json` override in this repo shadows the global one.
- A fourth, unrelated bare `~/.codex` directory exists (default `CODEX_HOME`) but isn't used by
  either the `home` or `tbank` alias — harmless, but worth knowing about if `codex` is ever run
  without the wrapper function.
- Nothing in any config disables/suppresses export (no `log_user_prompt` leak, no zero sampling,
  telemetry env vars correctly set to enabled).
- **Open, not fully resolved this pass:** one investigating agent raised a hypothesis that
  `internal/observability/otlp.go` might hard-reject non-identity `Content-Encoding` (i.e. gzip),
  which some OTel SDKs (Codex's Rust exporter in particular) enable by default — this would cause a
  415 before the payload is ever parsed. A second, deeper code-read pass of the same file focused
  on `matchAdapterResource`/signal dispatch and didn't flag this as an issue. **This needs a direct
  code check (exact file:line) before it's trusted either way** — see fix-plan doc, item 3.

## 5. Code-level verification of the Session 11 fix

Independently re-verified by reading the actual source, not trusting the reconciliation report at
face value:

- `internal/observability/otlp.go`: `ingestLogs`, `ingestMetrics`, `ingestTraces` are three
  independent functions (lines ~519-594), **each** calling `matchAdapterResource` (~lines 386-399)
  before falling through to the quarantine path. All three OTLP signal types (not just logs) are
  wired to adapter recognition, and `HTTPMux()`/`register()` route `/v1/logs`, `/v1/metrics`,
  `/v1/traces` and the equivalent gRPC services to the matching function.
- `codexadapter/otel.go:225-227` / `claudeadapter/otel.go:296-298`: `MatchesOTLPResource` does an
  exact, case-sensitive `service.name ==` comparison (`"codex_cli_rs"` / `"claude-code"`), never
  checks `service.version` — a CLI version bump can't cause false rejection.
- `internal/observability/ingest.go:138`: `ingestJSON`'s hook path still hardcodes
  `privacy.FixtureSourceSchema()` (four underscore event names only) — **confirmed, unchanged**
  gap; a real adapter's dotted `event_type` (`session.started`, `tool.called`) sent via the hook
  lane (not OTel) still quarantines as `unknown_enum`. Only relevant if hooks are configured in
  addition to OTel — not checked this pass whether they are.
- Dashboard aggregation code (`internal/dataplatform/system_snapshot.go`,
  `reliability_counts.go`, `mcp_uptime.go`, `reliability_timeline.go`, `entity_breakdown.go`) is
  real SQL against Postgres, not stubbed. `mcp_uptime.go:75` requires
  `observationCount >= 2` per component before leaving `"unknown"` — by design, not a bug; will
  resolve only after sustained real usage. `"unsupported"` never appears as a literal string
  anywhere in `internal/dataplatform` — it's assigned above the query layer per
  `contracts/glossary.yaml`'s formal definition ("no reliable source exists... or the capability is
  explicitly out of scope"), confirming most "unsupported" panels are an intentional design state
  for genuinely-out-of-scope capabilities, not a query bug.

## Formal view-state semantics (for interpreting the dashboard correctly)

Per `contracts/glossary.yaml` (the authoritative source; `dashboard.yaml` only consumes the enum):

- **`unsupported`** — no reliable source exists for this capability/version scope, or it's
  explicitly out of scope. Not fixable by sending more data.
- **`not_observed`** — the capability is observable and the source is healthy, but nothing
  qualifying happened yet. Fixable by generating real activity.
- **`unknown`** — the system can't establish a value or denominator from available evidence
  (usually: zero rows to compute from). Fixable once events land.
- **`numeric_zero`** — a complete, eligible population was measured and the real result is zero.

Given `events`/`sessions` are still at 0 rows, seeing `unknown` across most panels is the
contractually correct state, not a bug — the actual open question is whether real events will
start landing once a fresh test is run against the current (fixed) build.
