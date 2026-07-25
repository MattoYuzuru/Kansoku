# Session 11 — Real-agent gap closure (OTLP, hook installer, inventory)

## Purpose

Sessions 06/07 documented Codex's and Claude Code's real OTel event names and hook installer
targets in `contracts/codex/hooks-and-otel.yaml` / `contracts/claude/hooks-and-otel.yaml`, and
Session 05 built a full inventory entity-graph model. Live testing on 2026-07-25 with a real,
locally-installed Claude Code session and a real Codex CLI session (over a corporate VPN tunnel)
proved that none of the three deliverables those contracts describe are actually reachable from a
real agent: the dashboard showed zero events, zero sessions, and two live `unknown_schema`
incidents in Postgres. This session closes exactly those three confirmed gaps — nothing else.

## Confirmed gaps

1. **Real-agent OTLP telemetry is unconditionally quarantined.**
   `internal/observability/otlp.go`'s `knownResource()` recognizes only the Session 03 synthetic
   fixture-agent resource identity (`service.name == "fixture-agent"`,
   `kansoku.source.schema == "fixture.agent-otlp/1"`). `contracts/observability/ingress.yaml`'s
   `source_schemas` list never declared a real Codex or Claude OTLP resource identity either — the
   gap is contract-level, not just a missing `if` branch. Meanwhile
   `internal/codexadapter/otel.go` and `internal/claudeadapter/otel.go` already implement a fully
   unit-tested `CanonicalEventForOTel` mapping from the real documented upstream event names to
   canonical event types, but grep confirms it is called only from test files — never from any
   production ingestion path.
2. **The hook installer has no file-writer.** `contracts/codex/hooks-and-otel.yaml` and
   `contracts/claude/hooks-and-otel.yaml` each fully specify a `hook_installer_target`
   (`codex.user_hook`, `claude.user_hook`), but `contracts/privacy/installer.yaml`'s actual
   `targets` registry only carries `codex.user_otel`, `claude.user_otel`, `gemini.user_otel`,
   `cursor.user_hooks` — the two hook targets were never appended. `PlanConfiguration` in both
   `internal/codexadapter/stage2_stub.go` and `internal/claudeadapter/stage2_stub.go` returns
   `ErrNotImplementedYet` for every capability except the OTel install/live-canary pair. This
   matches ADR 0010's own recorded known gap, never closed in a later session as implied.
3. **Inventory (skills/plugins/MCP) is always empty.** `Adapter.Inventory` in both adapters calls
   `BuildInventorySnapshot` with an `InventoryInput` that has no Skills/Plugins/Hooks/MCPServers
   populated, because no host-filesystem scan step exists. `Discover(ctx, host *HostView)` already
   receives a permission-checked `HostView`; `Inventory(ctx, target Installation)` does not, so
   there is currently no bounded way for it to read `~/.claude/settings.json`'s `enabledPlugins`/
   `mcpServers` keys or Codex's `config.toml` MCP section.

## Non-goals

- `claude.transcript` JSONL importer (ADR 0010 gap, untouched).
- Gemini adapter / Cursor probe (Session 07b, backlog).
- A `kansoku doctor`/`configure` CLI surface (ADR 0008 gap) — only added if it falls out naturally
  while wiring `Discover`/`Inventory`/`PlanConfiguration` to something real; not a session goal.
- Live-CLI canary execution — stays simulation/fixture-based, matching the Session 06-09 precedent.
- DB restart / failed restore runtime scenarios (ADR 0011) — unrelated.

## Design decisions

**A. Real-agent OTLP recognition.** `internal/observability/otlp.go` gains an adapter-dispatch step
mirroring `routes.go`'s existing `hookAdapterHandler` switch (`fixture-agent` /
`codexadapter.AdapterID` / `claudeadapter.AdapterID`), instead of one hardcoded
`knownResource()`. Each branch resolves the real resource identity documented in
`contracts/codex/hooks-and-otel.yaml` / `contracts/claude/hooks-and-otel.yaml`'s `otel_source`
blocks, calls that adapter's existing `CanonicalEventForOTel`, and extracts only allowlisted
attributes — reusing `IngestSafeFields`'s allowlist enforcement, never a second one. A resource
matching no known adapter still falls through to the existing `unknown()` → `IngestUnknown`
quarantine path; that safety net is preserved, not removed. New real-agent OTLP source schemas are
appended to the relevant registries via the existing append-only policy-lock mechanism.

**B. Hook installer file-writer.** `codex.user_hook` / `claude.user_hook` targets are appended to
`contracts/privacy/installer.yaml`'s `targets` list (new lock entries, no edit to any trusted
entry). New `installer.BuildCodexHookPlan` / `installer.BuildClaudeHookPlan` builders write the
hook registration for the documented seven Claude events / Codex's notify mechanism, reusing
`internal/installer`'s existing `Plan`/`Approval`/`SimulateApply`/`SimulateRollback`/
`SimulateRemove`/`PlanSHA256` machinery — never a second apply mechanism. Because Codex's hook and
OTel targets both live in the same `config.toml` (different tables) and Claude's both live in the
same `settings.json` (different keys), the plan-ownership model must guarantee narrow ownership:
applying or rolling back the hook plan never touches already-applied OTel keys or other user
content in that file, and vice versa — this is a real constraint the implementation stage must
prove with a round-trip test, not just assert. The write stays simulate-only until a human
explicitly approves and applies, per ADR 0002's existing trust gate — Session 11 does not
introduce a first unattended real write. README gains a "connect via hooks" section alongside the
existing OTel one.

**C. Host inventory scan.** `Inventory` is threaded a `*adaptersdk.HostView` the same way
`Discover` already is — an `Adapter` interface signature change, so every conformance
implementation (`fakeadapter`/Loomwright, cross-agent Wayfinder) is updated in lockstep and must
keep passing its existing conformance suite. The scan reads Claude's `settings.json`
(`enabledPlugins`, `mcpServers`) and Codex's `config.toml` MCP section through `HostView`'s
existing bounded `ReadProbe`, mapping entries onto `contracts/adapter-sdk/inventory-graph.yaml`'s
closed `node_kinds`/`edge_kinds` vocabulary — no new node or edge kind invented. An installation
with genuinely zero configured plugins/MCP servers reports `Completeness: "unknown"`, never a
fabricated empty-but-complete snapshot.

## Deliverables

- Contract additions (real OTLP resource identity, `codex.user_hook`/`claude.user_hook` targets)
  with new append-only policy-lock entries — no existing trusted entry edited.
- `internal/observability/otlp.go` + `routes.go` + `internal/runtime/assembly.go` wiring for
  adapter-aware OTLP dispatch.
- `internal/installer` hook plan builders; `PlanConfiguration` wiring in both `stage2_stub.go`
  files.
- `HostView`-threaded `Inventory` across every registered adapter and conformance fixture.
- Unit + integration fixtures for all three gaps, including negative/regression cases (unknown
  resource still quarantines; empty inventory still reports unknown, not zero).
- README hook-connection section.
- This ADR/proposal/TDD triad and a Session 11 reconciliation report.

## Exit gate

A genuinely locally-installed Codex CLI or Claude Code CLI session, configured only via
README-documented steps (existing OTel steps plus the new hook steps), produces real
(non-quarantined) events, sessions and inventory nodes visible on the dashboard within the
existing SLO. The fixture-agent, Loomwright and Wayfinder conformance suites still pass unmodified.
No capability the underlying data cannot actually support is ever marked `configured`/`healthy`.

## 2026-07-25 implementation reconciliation — durable runtime inventory

The original HostView scan made adapter inventory computable but did not make it reachable from
the appliance: no production scheduler called it, no snapshot was persisted, and the dashboard
therefore still had an empty component funnel. ADR 0016 closes that runtime gap with explicit
read-only Compose mounts, a bounded periodic collector, immutable inventory snapshots and a
current-state projection.

The funnel is now deliberately heterogeneous evidence: `installed` and `enabled` are current
inventory state; later stages require lifecycle events. A complete scan with no enabled plugin is
numeric zero. A component population with no activation event is `not_observed`, not zero. A
component kind with no installed population remains unknown. This preserves the proposal's
acceptance rule instead of inferring execution from installation.
