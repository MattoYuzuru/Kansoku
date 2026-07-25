# ADR 0014 — Session 11 real-agent gap closure (OTLP, hook installer, inventory)

- Status: accepted for Session 11 implementation
- Date: 2026-07-25
- Owners: Kansoku observability/adapter-sdk
- Supersedes: none
- Extends: ADR 0009 (Codex adapter), ADR 0010 (Claude adapter and portability proof), ADR 0008
  (Adapter SDK), ADR 0006 (Session 03 ingress); confirms the scope in
  `Engineering Proposal/11-real-agent-gap-closure.md` and
  `Technical Design Document/11-real-agent-gap-closure.md`

## Context

Sessions 01-10 were each accepted as complete with explicitly recorded downstream gaps (ADR 0008:
`kansoku doctor`/`configure`/`adapter verify` CLI; ADR 0009: live-CLI canary evidence and CLI
surface; ADR 0010: missing `claude.transcript` importer, live-CLI canary evidence, nil `Audit`).
None of those ADRs recorded — because no one had yet tried it — that a real, locally-installed
agent could not produce any visible activity on the dashboard at all.

On 2026-07-25 the user built and ran the full stack (`docker compose up -d`, both containers
healthy) and connected two real agents: a local Claude Code session and a local Codex CLI session
(over a corporate VPN tunnel). The dashboard showed nothing. Direct inspection of the running
Postgres instance found `sessions`, `events` and `source_instances` empty, while
`schema_quarantine_metadata` held 3 rows and `incidents` held 2 `unknown_schema` rows, both
timestamped from today's real traffic — direct proof that requests arrived and were rejected, not
that nothing was sent.

Code and contract inspection confirmed three independent, previously-unrecorded gaps, detailed in
Engineering Proposal 11 and TDD 11:

1. `internal/observability/otlp.go`'s `knownResource()` recognizes only the Session 03 synthetic
   fixture-agent resource identity; `contracts/observability/ingress.yaml` never declared a real
   Codex/Claude OTLP resource identity either. `internal/codexadapter/otel.go` and
   `internal/claudeadapter/otel.go` already implement a fully unit-tested
   `CanonicalEventForOTel` mapping from the real documented upstream event names, but it is called
   only from test files — it was designed and tested in isolation and never wired to the live
   receiver.
2. `contracts/codex/hooks-and-otel.yaml` / `contracts/claude/hooks-and-otel.yaml` each fully
   specify a `hook_installer_target` (`codex.user_hook`, `claude.user_hook`), but
   `contracts/privacy/installer.yaml`'s actual `targets` registry never received those two entries,
   and `PlanConfiguration` in both `internal/codexadapter/stage2_stub.go` and
   `internal/claudeadapter/stage2_stub.go` returns `ErrNotImplementedYet` for anything but the
   OTel install/live-canary pair. This is exactly the gap ADR 0010 recorded ("`claude.user_hook`
   has no real filesystem writer yet ... a later stage adds it") — that later stage never arrived.
3. `Adapter.Inventory` in both adapters always returns an empty snapshot: no host-filesystem scan
   populates `InventoryInput.Skills`/`Plugins`/`Hooks`/`MCPServers`. `Discover(ctx, host *HostView)`
   already receives a permission-checked `HostView`; `Inventory(ctx, target Installation)` does
   not, and no CLI or other production caller drives `Discover`/`Inventory`/`PlanConfiguration` at
   all today — this compounds ADR 0008's already-recorded missing CLI surface gap.

All three gaps are honestly self-documented in the code and contracts at the point they were
introduced (none is a silent regression) — but their cumulative effect is that **no real agent has
ever produced a single visible event on the Kansoku dashboard**, which is the product's entire
purpose. Session 11 exists to close exactly these three gaps, confirmed live, before any further
session builds on top of an adapter layer that has never actually processed real traffic.

## Decision

1. **Scope is exactly the three confirmed gaps**, per the user's explicit choice of "all three" over
   narrower alternatives (OTLP-only, or OTLP+hooks without inventory). `claude.transcript`,
   Gemini/Cursor (Session 07b), the `kansoku doctor`/`configure` CLI, live-CLI canary execution, and
   the DB-restart/failed-restore scenarios (ADR 0011) remain explicitly out of scope and are not
   silently folded in.
2. **OTLP dispatch becomes adapter-aware, not fixture-only.** `internal/observability/otlp.go` adds
   a per-adapter resource-matching step (mirroring `routes.go`'s existing `hookAdapterHandler`
   pattern) that calls each adapter's own `CanonicalEventForOTel`, reusing the mapping logic
   Sessions 06/07 already built and tested rather than duplicating it inside
   `internal/observability`. A resource matching no known adapter still quarantines through the
   existing `unknown()` path — the gap is closed by recognizing more real traffic, never by
   weakening what happens to genuinely unrecognized traffic.
3. **New OTLP schema entries are appended, not edited in place**, via each registry's existing
   append-only policy-lock mechanism (`contracts/README.md`'s documented convention) — no previously
   trusted `contracts/observability-policy-locks.yaml` / `contracts/codex-policy-locks.yaml` /
   `contracts/claude-policy-locks.yaml` entry is weakened or rewritten.
4. **The hook installer gets a real, narrowly-scoped file-writer**, reusing
   `internal/installer`'s existing `Plan`/`Approval`/`SimulateApply`/`SimulateRollback`/
   `SimulateRemove`/`PlanSHA256` machinery — never a second, parallel apply mechanism. Because
   Codex's OTel and hook targets share one physical file (`config.toml`, different tables) and
   Claude's share one physical file (`settings.json`, different keys), the implementation must
   prove — with a round-trip test, not an assertion — that applying or rolling back one target's
   plan never touches the other target's already-applied keys or any other pre-existing user
   content in that file.
5. **The hook write stays gated behind the existing installer consent flow.** Session 11 does not
   promote any target to a first unattended real write; ADR 0002's supported/beta trust gate and
   preview/consent requirement remain exactly as strict as for every prior installer target.
6. **Inventory scanning threads a `HostView` into `Inventory`, matching `Discover`'s existing
   pattern**, rather than inventing a second, parallel host-access mechanism. This is an `Adapter`
   interface signature change; every conformance implementation (`fakeadapter`/Loomwright,
   cross-agent Wayfinder) is updated in the same change and must keep passing its existing
   conformance suite unmodified in behavior.
7. **The scan maps only onto the closed inventory vocabulary** `contracts/adapter-sdk/
   inventory-graph.yaml` already declares (`node_kinds`, `edge_kinds`, `source_scopes`) — no new
   node or edge kind is invented to make this session's scan fit.
8. **"Unknown is not zero" governs the new inventory path exactly as it governs every other source**:
   an installation with genuinely zero configured plugins/MCP servers reports
   `Completeness: "unknown"`, never a fabricated empty-but-complete snapshot.
9. **README gains a hook-connection section** alongside the existing OTel section, written from
   the actual implementation's exact config keys — not drafted speculatively ahead of the code.
10. **Implementation runs as an iterative developer → reviewer → fixer loop**, per the user's
    explicit request: an xhigh-effort developer agent implements each gap, a high-effort read-only
    reviewer agent assesses the result against this ADR/TDD without making changes, a high-effort
    fixer agent addresses exactly the reviewer's findings, and review repeats until the reviewer
    reports no remaining issues for that gap, before moving to the next.

## Consequences

- Real Codex/Claude Code CLI sessions, configured only through documented steps, will show up as
  real events/sessions/inventory on the dashboard — the product's core promise becomes true for the
  first time under real-world use, not just fixture/synthetic conformance tests.
- The OTLP ingress surface grows from one recognized resource identity to N (fixture-agent + every
  registered real adapter), following the same dispatch shape hooks already use — no new ingress
  mechanism, so the existing privacy/backpressure/durability guarantees extend automatically rather
  than needing to be re-proven from scratch.
- The `Adapter` interface changes shape (`Inventory` gains a `HostView` parameter), which is a
  breaking change to any future third-party adapter implementation — acceptable now, before any
  adapter beyond the two built-in ones and the two internal conformance fixtures exists, and far
  cheaper than after a real external adapter ecosystem forms.
- The installer's target registry grows from four to six targets, all still funneled through one
  apply/rollback mechanism — no second installer code path to maintain.
- Two files (`config.toml`, `settings.json`) each now have two independently-applied,
  independently-rollback-able logical targets sharing one physical file; this ownership-isolation
  invariant becomes a new thing future sessions touching either file must respect and test for.

## Rejected alternatives

- **Fix only the OTLP gap (highest urgency) and leave hooks/inventory for a later session.**
  Rejected per the user's explicit choice: all three gaps were confirmed live in the same
  investigation, and the hook/inventory gaps are cheap to close incrementally under the same
  investigation's context rather than re-discovering the same facts in a future session.
- **Add a second, OTLP-specific ingress mechanism for real agents instead of extending
  `otlp.go`.** Rejected — this would duplicate the privacy/backpressure/durability guarantees the
  existing receiver already proves, and contradicts the adapter-first principle of no
  agent-specific core code paths.
- **Give the hook installer its own apply/rollback mechanism** (since it writes into a file another
  target already owns part of). Rejected — `internal/installer`'s existing machinery already
  models exactly this kind of narrow-ownership, reversible file mutation; building a second
  mechanism would be redundant and would fork the audit trail two installer targets currently share.
- **Leave `Inventory`'s signature unchanged and have adapters reach for global/ambient filesystem
  state instead of a passed `HostView`.** Rejected — this breaks the permission-checked,
  never-scan-the-home-directory invariant Session 05 established and ADR 0008 recorded as load-
  bearing; a signature change ripples further but preserves the actual safety guarantee.
- **Silently treat "zero plugins/MCP servers found" as a complete, healthy snapshot.** Rejected —
  violates "unknown is not zero," the single principle this entire codebase treats as
  non-negotiable across every prior session.

## Known gaps (recorded explicitly, not silently dropped)

- `claude.transcript` JSONL importer remains unbuilt (ADR 0010).
- Gemini adapter and Cursor probe remain Session 07b backlog.
- No `kansoku doctor`/`configure`/`adapter verify` CLI surface is added; `Discover`/`Inventory`/
  `PlanConfiguration` remain reachable only through direct Go calls (tests, and whatever this
  session's own verification harness uses) until a future session builds that surface (ADR 0008).
- Live-CLI canary execution stays disabled/simulation-only, matching the Session 06-09 precedent;
  this session's proof that real traffic is recognized is a live manual verification pass
  (rebuilt Docker image, real agent connected, dashboard inspected), not an automated live-canary
  harness.
- DB restart and failed-restore runtime scenarios (ADR 0011) remain unexecuted; unrelated to this
  session's scope.
- `Audit()` remains `nil` for both adapters (ADR 0009/0010); unrelated to this session's scope.
