# Gap inventory and fix plan — 2026-07-25

Companion to `reports/2026-07-25-live-diagnostic-audit.md`. That doc explains what's actually
happening right now; this one consolidates every gap the project has ever acknowledged in its own
ADRs/reports plus what this pass found live, and proposes a prioritized order for a future
fixer/reviewer agent loop. **No fixes have been applied yet — this is planning only**, per the
developer's request to gather information first and run fixer agents in a separate, later pass.

## How to read this doc

Three different kinds of "gap" get conflated when the dashboard looks empty, and they need very
different responses:

- **(BUG)** — something that should work today and doesn't; a real defect to fix.
- **(UNVERIFIED)** — the code may well work, but nobody has proven it live since the last change;
  needs a real test, not necessarily a code change.
- **(BY DESIGN / BACKLOG)** — the project's own ADRs explicitly scoped this out or deferred it;
  not a bug, don't "fix" it without a deliberate scope decision.

## Live-test findings (2026-07-25, later same day) — fresh live test run, with a real bug found

The previously-planned "restart everything and run one fresh real session" action was executed:
fresh, correctly-configured Codex T-Bank and Claude Code processes were run against the current
(rebuilt) stack while watching the DB directly. The result was **not** the optimistic "it was just
stale data" outcome — a fresh Codex process still quarantines, and the exact reason is now known
with certainty. This is what produced the two new P0 items (1 and 2) below. Full method and
evidence:

- A temporary (never committed, since reverted) debug print was added to
  `matchAdapterResource`'s `adapterNone` branch in `internal/observability/otlp.go`, dumping every
  resource attribute key/value to stderr. The image was rebuilt, the container restarted, one real
  `tbank codex exec` invocation was run against it, and the container logs were read directly.
- **Result — confirmed root cause of the Codex quarantine:** the real Codex CLI (`OpenAI Codex
  v0.145.0`, invoked via `codex exec`) sends the OTLP resource attribute
  `service.name = "codex_exec"`. `internal/codexadapter/otel.go:214` hardcodes
  `OTLPResourceServiceName = "codex_cli_rs"` — a **different, non-matching literal**. Every real
  Codex batch therefore hits `matchAdapterResource`'s `default` case, resolves to `adapterNone`,
  and quarantines as `otlp_log/unknown_schema` via `otlp.go`'s `unknown()` path — deterministically
  and 100% reproducibly, regardless of the Session 11 dispatch fix (which is itself correct code;
  the constant it's checking against is simply wrong/stale for this Codex CLI version). This is a
  **(BUG, confirmed live, high confidence)** — promoted to new P0 item below. Full captured
  resource: `service.name="codex_exec"`, `service.version="0.145.0"`, `host.name=<redacted>`,
  `telemetry.sdk.name="opentelemetry"`, `telemetry.sdk.language="rust"`,
  `telemetry.sdk.version="0.31.0"`, `env="dev"` — no `kansoku.adapter.version`/
  `kansoku.source.schema` attributes present either, consistent with a real (non-fixture,
  non-Kansoku-specific) exporter.
- **Claude Code side — no live confirmation possible, but a strong, different explanation found.**
  Five independent real Claude Code invocations across this audit (the original in-session
  activity, plus four fresh `-p`/print-mode processes, one with `--debug-file` capturing Claude
  Code's own internal debug log) produced **zero** trace at Kansoku's ingress — not even a
  quarantine/`adapterNone` dump, meaning no OTLP request from Claude Code ever reached the HTTP
  handler at all (contrast with Codex, whose malformed-for-Kansoku request did arrive and got
  dumped). Claude Code's own `--debug-file` log shows telemetry does initialize
  (`[3P telemetry] isTelemetryEnabled=true`, `getOtlpReaders: types=["otlp"], interval=60000,
  protocol=http/protobuf, endpoint=http://127.0.0.1:4318`, `Created 1 log exporter(s)`, `Event
  logger set successfully`) — so the endpoint/protocol/bearer config is read and accepted. But the
  **metric reader's export interval is a hardcoded 60000ms (60s)**, and every non-interactive `-p`
  test process here lived only ~10-20s before exiting — well under one export tick. The leading
  explanation: telemetry (at least metrics, likely logs too if they share a similarly long-ish
  batch delay) is generated internally but **dropped on process exit before ever being flushed**,
  since one-shot `-p` invocations don't naturally live long enough to hit the next scheduled
  export. This would explain total silence without any Kansoku-side defect — Kansoku simply never
  receives a request to reject or quarantine in the first place. **Category: (UNVERIFIED, but
  well-evidenced) — likely a Claude Code CLI/product behavior for short-lived `-p` sessions, not a
  Kansoku bug.** A genuinely long-lived interactive Claude Code session (not `-p`) would be needed
  to confirm whether Claude Code's real resource attributes (`service.name`, expected to be
  `"claude-code"` per `claudeadapter/otel.go:285`) actually match what `claudeadapter.otel.go`
  expects — this has still never been observed live, in either direction.
- Also noted while reading the clean (reverted) `otlp.go` directly: `readProtoRequest` (~line 140)
  really does hard-reject any `Content-Encoding` other than empty/`identity` with a 415
  (`"compressed_input_rejected"`), settling P1 item 5 below as **confirmed real** — but it did **not**
  cause the Codex quarantine observed here, since the real Codex request that produced the new
  quarantine row was successfully protobuf-parsed (4 records, 2437 bytes counted) before hitting
  `matchAdapterResource`, meaning it was sent uncompressed. Still worth checking during any future
  gzip-enabled Codex/Claude test, since a 415 rejection at this layer leaves **zero** trace
  anywhere (no quarantine row, nothing) — indistinguishable from "no request sent at all" purely
  from DB/API observation, which is exactly the ambiguity the Claude Code finding above is stuck
  behind.
- Two more incidental confirmations from this pass: (1) `incidents` intentionally caps at one row
  per distinct `(category, source_kind)` pair — `NewIncident`'s ID is
  `stableID("incident/1", category, source)`, so repeated identical-category quarantine events
  update/collide onto the same incident row rather than creating new ones; this is by design, not
  the incidents-visibility bug in P0 item 3 below. (2) The `kansoku` container produces **zero** stdout/
  stderr log output ever (`docker logs` is empty from container start) because
  `cmd/kansoku/main.go` contains no logging calls at all — any future live debugging of this
  service will need the same kind of temporary instrumentation used here, not `docker logs`.

## Priority-ordered action list (for the next fixer/reviewer loop)

### P0 — do first, cheap, high-signal

1. **(BUG, confirmed live, root cause known) Real Codex CLI sends `service.name = "codex_exec"`,
   not the `"codex_cli_rs"` literal `internal/codexadapter/otel.go:214` expects — every real Codex
   session is unconditionally quarantined.** See "Live-test findings" above for the full evidence
   (captured live via a temporary debug build). This is a small, well-understood fix: update
   `codexadapter.OTLPResourceServiceName` (or `MatchesOTLPResource`) to recognize `"codex_exec"` —
   decide whether to replace the literal outright, accept both (if interactive `codex` sessions,
   as opposed to `codex exec`, might still send a different value — not yet tested), or match a
   prefix/pattern if Codex versions are expected to keep varying this string. Whichever approach is
   chosen, add a regression test asserting the literal(s) actually observed live, not just the
   previously-assumed one, so a future Codex CLI update can't silently reintroduce this.
2. **(UNVERIFIED, not a Kansoku-side bug so far as tested) Claude Code produced zero telemetry
   across 5 real test invocations.** See "Live-test findings" above. Before spending fixer effort
   here: confirm with a genuinely long-lived interactive Claude Code session (not `-p`) whether (a)
   any request ever reaches Kansoku at all, and (b) if one does, whether its `service.name` matches
   `claudeadapter`'s expected `"claude-code"` literal — this pairing has literally never been
   observed together yet, unlike Codex where both halves (arrival + mismatch) are now confirmed.

3. **(BUG, confirmed live) `/api/v1/incidents` returns `"data": []` and `/api/v1/health` reports
   `open_incident_count: 0` despite 2 real rows in the `incidents` table.** This is a real,
   independently-verified defect regardless of the ingestion outcome above — the reliability/health
   panels are hiding incidents that genuinely exist. Find the query/handler backing these two
   routes (likely in `internal/dataplatform/reliability_counts.go` or a dedicated incidents query,
   and whatever assembles `/api/v1/health`) and check for a wrong filter (e.g. a status/resolved
   filter excluding all current rows, wrong table/column name, or a time-window filter excluding
   today's UTC timestamps due to a timezone bug — worth checking specifically given the timestamps
   involved here span a UTC/local-time boundary).

4. **(cosmetic) `database_size_bytes` is shown unlabeled/raw in the UI** (developer read
   `10483391` as a confusing "10 million 450 thousand"). Confirm whether `web/src` formats this
   field as a human-readable size (KB/MB) anywhere, and if not, add formatting. Low priority but
   easy and removes one source of "something's clearly broken" confusion.

### P1 — secondary findings from the live test, real but not the Codex quarantine cause

5. **(confirmed real by direct code read, ruled out as the Codex quarantine cause) `Content-Encoding:
   gzip` rejection in `internal/observability/otlp.go`.** `readProtoRequest` (~line 140) really does
   hard-reject any `Content-Encoding` other than empty/`identity` with HTTP 415
   `"compressed_input_rejected"` (comment cites ADR 0006's streaming-bomb gap) — this settles the
   earlier open contradiction between two investigation passes: the hypothesis was real. But it did
   **not** cause the Codex quarantine confirmed in "Live-test findings" above — that request was
   successfully protobuf-parsed (4 records, 2437 bytes) before reaching `matchAdapterResource`,
   meaning it was sent uncompressed. Still worth a fix if any real adapter ever does send compressed
   OTLP (a 415 here leaves **zero** trace anywhere — no quarantine row, nothing — indistinguishable
   from "no request sent"), but it is not currently blocking anything observed live. Lower priority
   than items 1-4 above.

6. **(gap, confirmed real, scope unclear) `ingestJSON`'s `hook_http` decode path is
   fixture-schema-only** (`internal/observability/ingest.go:138`, hardcoded
   `privacy.FixtureSourceSchema()`). Real adapter hook payloads (dotted `event_type`s like
   `session.started`) reach the correct adapter-specific handler but still quarantine as
   `unknown_enum`. **Before fixing this, determine whether it's even in scope**: is Claude Code's
   `hooks` block (separate from the `env`/OTel block already verified) actually installed in
   `~/.claude/settings.json`, and is Codex's `notify`/hook config installed in either `~/.codex-home`
   or `~/.codex-tbank`? If hooks aren't configured at all, this gap is currently inert and OTel-only
   ingestion is the whole story. If hooks ARE configured, this becomes a P1 fix: give `ingestJSON`
   an adapter-aware schema the same way Gap A gave OTLP dispatch adapter-aware resource matching
   (add `codexadapter`/`claudeadapter` hook-event schemas parallel to `FixtureSourceSchema()`,
   dispatch on the same adapter-kind signal `routes.go` already uses for `codexHookHandler`/
   `claudeHookHandler`, reuse the existing `ingestSafe` pipeline exactly as Gap A's fix reused it
   for OTLP — do not build a second decode/commit path).

### P2 — legitimate design states, not bugs (verify understanding, don't "fix")

7. **`mcp_uptime`/connection-health panels require ≥2 observations per component**
   (`internal/dataplatform/mcp_uptime.go:75`) before leaving `"unknown"`. This will resolve on its
   own once item 1's fix lands and sustained real Codex/Claude traffic flows — don't treat continued
   "unknown" here as a bug after only one test event.
8. Most other `"unknown"`/`"unsupported"` panels are contractually correct for an empty `events`
   table per `contracts/glossary.yaml`'s formal definitions (see diagnostic-audit doc §
   "Formal view-state semantics"). Re-check panel-by-panel only after real events are confirmed
   landing (item 1's fix, verified live); if they're still `unknown`/`unsupported` *after* real
   events exist, that's a new, genuine bug to log — not yet observed this pass.

## Consolidated backlog — everything the project has ever explicitly acknowledged as deferred

This is **not** a list of things broken today — most of these are intentional scope boundaries
recorded across 14 ADRs and 11 session reconciliation reports. Listed so a future fixer agent
doesn't waste time "fixing" something that was a deliberate decision, and so the developer can
decide which of these (if any) should be promoted into active scope.

| Item | First recorded | Category |
|---|---|---|
| Public adapter Supported/Beta governance gate (needs 2 human reviews + evidence) | ADR 0002 | By design — governance gate |
| SQLite as primary store | ADR 0001 | Rejected, possible future "lite/offline mode" |
| uPlot as sole chart lib | ADR 0001 / 0013 | Rejected, revisit only if perf demands it |
| 24h idle CPU/RSS SLO | ADR 0001 | Unverified — single Docker sample only |
| Prompt hash/HMAC/embeddings | ADR 0004 | By design — privacy boundary, stays disabled |
| OS keychain, atomic config writers, prod DB roles, signed SBOM, physical erasure | ADR 0004 | Backlog |
| OTLP JSON encoding (only protobuf/binary implemented); gzip | ADR 0006 / SOURCES.md | By design (Experimental, protocol scope) — **gzip-rejection confirmed real by code read, see P1 item 5; not the Codex quarantine cause** |
| FileStore quadratic rewrite cost | ADR 0006 | Unverified at production throughput |
| Mergeable percentile sketches | ADR 0007 | Deferred pending measured need |
| 1M-events/day query budget | ADR 0007 | Unverified — tested at hundreds of rows only |
| Named time-range preset resolver | ADR 0007 | Closed in Session 10 — verify still true |
| Cost-formula computation | ADR 0007 | Deferred, schema-only |
| pg_dump-compatible backup | ADR 0007 | Closed in Session 09 — verify still true |
| External-process/Wasm adapter execution | ADR 0008 | Backlog, no Go implementation |
| Compatibility/fixture registry persistence | ADR 0008 | Backlog |
| `kansoku doctor`/`configure`/`adapter verify` CLI | ADR 0008, still open at S11 | Backlog — **this would have caught today's config/connectivity issue immediately; consider promoting** |
| Live canary / signed adapter distribution | ADR 0008, recurring through S11 | Deferred, simulation-only |
| `Audit()` returns nil for Codex/Claude adapters | ADR 0009/0010, still open at S11 | Backlog |
| Real config filesystem writer for hooks/OTel | ADR 0009/0010 | Partially closed S11 — write stays simulate-only, `AuthorizeRealWrite` still refuses all real writes |
| `claude.transcript` JSONL importer | ADR 0010, reaffirmed S11 | Backlog, unbuilt |
| Session 07b: Gemini adapter + Cursor probe | ADR 0010, recurring every session | Deliberately deferred, not cancelled |
| DB-restart / failed-restore fault scenarios | ADR 0011, still open at S11 | Unverified, runtime-required |
| Report signing (local HMAC only) | ADR 0011 | By design, not release provenance |
| At-rest encryption | ADR 0012 | By design — host/volume responsibility |
| Release image signing / vuln attestation | ADR 0012/0013 | Still open |
| 7-day wall-clock soak (accelerated logical-cycle only) | ADR 0012 | Methodological caveat, not a defect |
| Browser/visual-regression test matrix | S10 report | Unverified — no browser automation tool available |
| ARM64/x86_64 build matrix | S10 report | Unverified |
| Disk-forecast / load-test at scale | S10 report | Not executed |
| Phone-width (<640px) responsive support | ADR 0013 | Explicit stretch goal |
| CSP `style-src 'unsafe-inline'` | ADR 0013 | Accepted residual |
| `ingestJSON` hook_http fixture-schema-only | S11 report | **Open — see P1 item 6 above** |
| Live manual dashboard pass not re-executed in S11 | S11 report / ROADMAP | **This is exactly what the 2026-07-25 live test (P0 items 1-2 above) closes** |

## Suggested process for the next (fixing) pass

The developer's stated preference for the next phase: a **sequential**, not parallel,
fixer → reviewer loop (fixer agent makes real infrastructure/code fixes and commits
conventionally, a review agent checks the result and writes findings back, fixer addresses them,
repeat), plus a separate lightweight watcher that polls every ~5 minutes to confirm agents are
still making file changes / commits (not stalled on transient network errors), and nudges a
stalled agent to continue rather than treating a 400 as fatal. That loop should be scoped to
**P0 and P1 above only** for the first iteration — the P2 backlog table is reference material, not
a work queue, until the developer decides to promote specific items.
