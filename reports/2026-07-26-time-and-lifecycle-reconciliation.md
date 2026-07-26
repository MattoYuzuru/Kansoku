# Time and lifecycle reconciliation — 2026-07-26

## Exit gate

This slice is complete when:

1. every timeline preset changes both its server bucket resolution and its rendered axis;
2. a page left open across a bucket boundary advances without a reload;
3. database and chart buckets use the browser's IANA timezone while retaining UTC timestamps on
   the wire;
4. missing sparse buckets remain unknown rather than becoming numeric zero;
5. chart presentation rounds long floating-point values without changing stored precision;
6. a native component lifecycle assertion reaches the funnel only after a unique inventory match,
   survives replay idempotently and is not promoted to a later stage;
7. unsupported lifecycle stages say so explicitly;
8. a real adapter hook commits instead of being quarantined;
9. unit, contract, privacy, runtime and live-canary checks reconcile.

## Root causes and corrections

### Timelines

`useRange` captured the upper boundary once at page mount. The range selector changed timestamps,
but all bespoke timeline queries still truncated to sparse UTC days, so 24 hours, seven days and
30 days could render as the same single July 25 bar. A five-year request was also rejected by the
old fixed 366-day server limit.

The shared range contract now selects hourly, daily, weekly or monthly calendar buckets, sends the
browser IANA timezone, validates resolution-specific maximum spans and refreshes the live upper
boundary while the page is visible. The UI constructs the full expected axis and overlays sparse
server rows; missing values are `null`, never zero. Tooltips format numbers to at most two
fractional digits while API and database values retain full precision.

### Component lifecycle

Inventory and runtime evidence are distinct. An installed skill or plugin proves only inventory
state. Runtime assertions now enter `component_lifecycle_events` only when the exact tuple
`(agent_installation_id, component kind, declared name)` resolves to exactly one current inventory
item. Zero or multiple matches remain durable unmatched evidence and do not affect the funnel.
The normalized event ID is reused as the lifecycle event ID, so replay is idempotent, and the
compatibility union excludes projected rows to prevent double counting.

`opportunity_detected` remains intentionally unimplemented, as requested. Universal
skill/plugin `succeeded` is explicitly `unsupported`: neither a successful session nor a child
tool call proves that the component itself achieved a terminal outcome.

Claude Code publishes native `skill_activated` and `plugin_loaded` events, so those may support
invoked and loaded after exact inventory correlation. Codex's stable OTel contract does not
publish skill/plugin activation. A low-effort Codex canary invoking `kansoku-noop-canary` emitted
model and tool facts but no component identity, confirming that Kansoku must not fabricate those
stages. A future opt-in, version-pinned Codex App Server source could consume its typed skill and
plugin identities; content-rich rollout/session files remain outside the privacy-safe design.

### Real hook ingress

Codex and Claude hook handlers previously sanitized adapter payloads and then sent their output
through the fixture-only hook decoder. Real hook payloads consequently reached quarantine.
Already-sanitized adapter output now shares the canonical safe-field validator,
pseudonymization, idempotency and durable handoff used by OTLP while retaining `hook_http`
lineage and the adapter-specific `*.hook/1` schema.

No agent configuration was modified. Installation/configuration still requires a preview and
explicit confirmation.

## Verification

- `go test ./...`: pass.
- `npm --prefix web run build`: pass.
- `python3 scripts/validate_data_platform.py --runtime-only`: pass.
- `python3 scripts/validate_runtime.py --runtime-only`: pass.
- `python3 scripts/validate_observability.py`: pass.
- `python3 scripts/validate_privacy.py`: pass.
- `python3 scripts/validate_codex.py`: pass.
- `python3 scripts/validate_claude.py`: pass.
- production Docker service and Postgres: healthy.
- production embed matches the Vite build byte-for-byte; the Docker build now rejects stale
  `internal/webui/dist` output before compiling the Go binary.
- live hourly activity in `Europe/Moscow`: July 26 buckets appeared for new sessions.
- live daily activity in `Europe/Moscow`: separate July 25 and July 26 local-day buckets.
- live five-year monthly model usage: HTTP 200 with `model_usage/3`.
- real Codex `SessionStart` hook: accepted at revision 5134 and persisted as
  `hook_http / codex / session.started / observed`; quarantine did not increase.
- low-effort `gpt-5.6-terra` no-op skill canary at `2026-07-26T07:05:28Z`: three model responses,
  five tool calls and two source observations were durable; zero component lifecycle assertions
  were emitted, which is the expected negative result for current Codex OTel.
- live `component_lifecycle_events`: zero before any natively identified component assertion;
  integration tests prove unique resolution, replay idempotency and non-promotion.

## Residual risks

- The current Codex hook adapter predates the complete current hook event/input surface. Expanding
  it requires sanitized fixtures and version-specific contract tests; this slice fixes the known
  real-ingress break without claiming full hook coverage.
- Codex skill/plugin invocation cannot be proved from stable OTel today. App Server integration is
  the strongest documented route but requires a new opt-in adapter and compatibility policy.
- Claude third-party component names may be redacted without detailed tool telemetry. Enabling
  detailed telemetry would also expose content-bearing tool data and is therefore not a safe
  default.
- A retention-derived true all-time boundary is not yet available; the current preset is explicitly
  “Last 5 years”.
- Agent-detail unsupported fields remain intentionally deferred.
