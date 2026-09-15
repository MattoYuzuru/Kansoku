# TDD 11 — Real-agent gap closure (OTLP, hook installer, inventory)

## Confirmed baseline (2026-07-25, live environment)

- `docker compose ps` showed `kansoku`/`postgres` healthy; `sessions`/`events`/`source_instances`
  tables empty; `schema_quarantine_metadata` held 3 rows and `incidents` held 2 `unknown_schema`
  rows, both timestamped from real Claude Code + Codex CLI traffic received today.
- `grep -rn "CanonicalEventForOTel(" internal/` matches only `*_test.go` files.
- `grep -rn "codexadapter.Discover\|claudeadapter.Discover" internal/` (outside tests) matches
  nothing — no production caller drives `Discover`/`Inventory`/`PlanConfiguration` today.
- `contracts/privacy/installer.yaml`'s `targets` array has exactly four entries: `codex.user_otel`,
  `claude.user_otel`, `gemini.user_otel`, `cursor.user_hooks`.

## A. OTLP adapter-aware dispatch

**Files:** `internal/observability/otlp.go`, `internal/observability/routes.go`,
`internal/runtime/assembly.go`, `contracts/observability/ingress.yaml` and/or
`contracts/codex/hooks-and-otel.yaml` / `contracts/claude/hooks-and-otel.yaml`,
`contracts/observability-policy-locks.yaml` (and `contracts/codex-policy-locks.yaml` /
`contracts/claude-policy-locks.yaml` if the schema lands in the adapter-owned files instead).

1. Determine the real resource/attribute shape Codex and Claude Code's OTel exporters actually
   send today — `SOURCES.md`'s Codex/Claude OTel sections plus the `otel_source.documented_events`
   / `documented_attributes` blocks already in `contracts/codex/hooks-and-otel.yaml` /
   `contracts/claude/hooks-and-otel.yaml` are the authoritative reference; do not guess a shape.
2. Add a `knownResource`-equivalent per adapter (e.g. `codexadapter.MatchesOTLPResource(resource)`,
   `claudeadapter.MatchesOTLPResource(resource)`) living beside each adapter's existing `otel.go`,
   so the mapping/fingerprint logic that already exists there is reused, not duplicated inside
   `internal/observability`.
3. In `otlp.go`'s `ingestLogs`/`ingestMetrics`/`ingestTraces`, replace the single `knownResource`
   call with a dispatch: fixture-agent check first (unchanged), then each registered adapter's
   resource matcher, in the same spirit as `routes.go:hookAdapterHandler`'s adapter switch. A
   resource matching none of them still calls `r.unknown(...)` exactly as today — no regression to
   the quarantine safety net.
4. Once a resource matches an adapter, call that adapter's `CanonicalEventForOTel(name, shape)` to
   get the canonical event type, then extract only the attributes
   `contracts/codex/hooks-and-otel.yaml` / `contracts/claude/hooks-and-otel.yaml` declare safe, and
   route them into `IngestSafeFields`'s existing allowlist (extend the allowlist only with fields
   those contracts already document as safe — never add a field ad hoc in Go without a matching
   contract entry).
5. `internal/runtime/assembly.go`'s `NewOTLPReceiver`/`NewIngressHTTPHandler` wiring needs no
   signature change if the dispatch lives inside `otlp.go`; confirm this while implementing, and
   only touch `assembly.go` if a genuinely new dependency (e.g. an adapter registry reference) must
   be threaded through.
6. Contract change: append the real resource identity + safe-attribute set as new entries (not
   edits) via each registry's existing append-only lock mechanism
   (`contracts/observability-policy-locks.yaml` and/or `contracts/codex-policy-locks.yaml` /
   `contracts/claude-policy-locks.yaml`, whichever registry ends up owning the new schema). Update
   `scripts/validate_observability.py` / `scripts/validate_codex.py` / `scripts/validate_claude.py`
   only if their existing checks do not already generalize to the new entries.
7. Tests: real (non-fixture) Codex OTel payload and real Claude OTel payload, built from the
   documented shape, landing as real `events` rows end to end; a genuinely unknown resource/schema
   still quarantines (regression test); the existing fixture-agent conformance path
   (`internal/integrity`'s synthetic pipeline, `TestFixtureOTelGoldenMapMatchesCanonicalEventForOTel`
   and siblings) passes unmodified.

## B. Hook installer file-writer

**Files:** `contracts/privacy/installer.yaml`, `contracts/privacy-policy-locks.yaml`,
`internal/installer/protocol.go`, `internal/codexadapter/stage2_stub.go`,
`internal/claudeadapter/stage2_stub.go`, `contracts/adapter-sdk/capabilities.yaml` (only if a new
capability id is genuinely required — see design note below).

1. Append `codex.user_hook` (`config_locator_kind: codex_user_config`, format `toml`) and
   `claude.user_hook` (`config_locator_kind: claude_user_settings`, format `json`) to
   `contracts/privacy/installer.yaml`'s `targets` list, matching each adapter contract's already-
   declared `hook_installer_target` block exactly (`ownership`, `default_scope`, forbidden keys).
   Add corresponding new entries to `contracts/privacy-policy-locks.yaml` — append-only, no
   existing trusted entry touched.
2. Add `targetSpecs["codex.user_hook"]` / `targetSpecs["claude.user_hook"]` to
   `internal/installer/protocol.go` alongside the existing four, and
   `BuildCodexHookPlan`/`BuildClaudeHookPlan` functions parallel to `BuildCodexPlan`/
   `BuildClaudePlan`.
3. **Design note — resolve during implementation, do not assume upfront:** Codex's hook and OTel
   targets both live in `config.toml` (different tables); Claude's both live in `settings.json`
   (different keys). Decide whether `PlanConfiguration`'s existing `configuration.install`
   capability should return one merged plan touching both the OTel and hook portions of that single
   file in one pass (no new capability id, `ownership` enforced per-key inside one plan), or whether
   a new closed capability id (e.g. `configuration.hook_install`) should be added to
   `contracts/adapter-sdk/capabilities.yaml` via its documented append-only extension path (schema
   version bump + lock entry + proposal/TDD update, per `contracts/README.md`). Whichever is chosen,
   the hard requirement is: applying or rolling back the hook plan must never touch already-applied
   OTel keys or other pre-existing user content in that file, and vice versa. Prove this with a
   round-trip test (apply OTel, then apply hook, then roll back hook, assert OTel keys and any
   unrelated pre-existing content are byte-identical to before the hook was ever applied).
4. `PlanConfiguration` in both `stage2_stub.go` files routes the resolved capability/target
   combination to the new builder instead of `ErrNotImplementedYet`; the hook helper writes into
   the target's notify/hook table for exactly the 7 supported events
   `contracts/claude/hooks-and-otel.yaml` declares (`SessionStart`, `UserPromptSubmit`,
   `PreToolUse`, `PostToolUse`, `SubagentStart`, `SubagentStop`, `Stop`) and Codex's documented
   equivalent.
5. The write stays gated behind `internal/installer`'s existing preview/consent/
   `SimulateApply`/`SimulateRollback` machinery — Session 11 does not promote to a first real
   unattended write; a human still explicitly approves and applies, exactly as every prior session's
   installer scope requires (ADR 0002).
6. Tests: full `ChangePlan` build + `SimulateApply` + `SimulateRollback` round trip for both hook
   targets; the ownership-isolation round trip from step 3; a synthetic stdin payload sent to the
   installed hook helper's config resulting in a real event through the existing
   `codexHookHandler`/`claudeHookHandler` routes.
7. README: add a "Connect via hooks" section mirroring the existing OTel section, generated from
   whatever the implementation actually produces (exact config keys/table names), not written
   speculatively before the code exists.

## C. Host inventory scan

**Files:** `internal/adaptersdk/adapter.go` (interface), `internal/adaptersdk/hostview.go`,
`internal/codexadapter/stage2_stub.go`, `internal/claudeadapter/stage2_stub.go`,
`internal/adaptersdk`'s `fakeadapter` (Loomwright) conformance implementation, cross-agent
Wayfinder fixture, any call site constructing an `Adapter` value or calling `Inventory` directly.

1. Change `Adapter.Inventory(ctx context.Context, target Installation) (InventorySnapshot, error)`
   to also accept `host *HostView`, the same parameter `Discover` already receives — confirm via
   `grep -rn "\.Inventory(" internal/` every call site before changing the signature, and update
   every one (adapter implementations, conformance suites, any test harness) in the same change so
   nothing is left calling the old signature.
2. Implement the scan: read `~/.claude/settings.json`'s `enabledPlugins` and `mcpServers` keys and
   Codex's `config.toml` MCP section through `HostView.ReadProbe` (bounded, already
   permission-checked — never a raw unbounded filesystem read), inside `target.StateRoot`'s
   `AllowedRoots`.
3. Map each discovered entry onto `contracts/adapter-sdk/inventory-graph.yaml`'s closed vocabulary:
   `plugin_package`/`plugin_version` nodes for plugins, `mcp_server_instance`/`mcp_tool` nodes for
   MCP servers, `bundles`/`provides`/`enabled_for` edges connecting them to the
   `agent_installation` node — no new node or edge kind invented; `path_pseudonym` fields use
   `HostView.PseudonymizePath`, never a raw path.
4. Populate `InventoryInput.Skills`/`Plugins`/`Hooks`/`MCPServers`/`RepositoryTargets` from the scan
   before calling `BuildInventorySnapshot`, replacing today's always-empty `InventoryInput{
   InstallationID: ...}`.
5. An installation with zero configured plugins/MCP servers must report `Completeness: "unknown"`
   (evidence absent), never a fabricated empty-but-complete snapshot — mirror the same rule
   `stage2_stub.go`'s existing `Reconcile` already applies to an empty current snapshot.
6. Tests: temp-directory synthetic `settings.json`/`config.toml` fixtures with known plugin/MCP
   entries, proving the resulting graph's nodes/edges are correct; a zero-configured-plugin
   fixture proving `Completeness: "unknown"`; the full Loomwright and Wayfinder conformance suites
   passing against the new `Inventory` signature unmodified in behavior.

## Verification

`python3 scripts/validate_observability.py`, `scripts/validate_codex.py`, `scripts/validate_claude.py`,
`scripts/validate_privacy.py`, `scripts/validate_adapter_sdk.py`, `scripts/run_go_tests.py`, and a
manual end-to-end pass: rebuild the Docker image, bring the stack up, connect a real Codex and/or
Claude Code CLI session per the (updated) README, and confirm real events/sessions/inventory nodes
appear on the dashboard — not just that unit tests pass in isolation.

## Exit gate

Same as Engineering Proposal 11: real agent activity is visible end to end, all existing
conformance suites remain green, and no gap is closed by weakening an existing privacy or
"unknown is not zero" invariant.

## D. Durable appliance inventory and lifecycle projection (implemented 2026-07-25)

The implementation required one additional production layer beyond section C:

1. `internal/runtime/inventory.go` schedules every configured target independently at a bounded
   interval. Each target is an explicit absolute read-only root; a failed target records a safe
   error class and cannot erase the last successful snapshot or block other targets.
2. Compose exposes separate personal/corporate Codex state and user/system skill mounts. Missing
   variables resolve to a repository-owned empty directory, never to the host root or home.
3. Migration `0007_component_inventory` stores immutable snapshots/nodes/edges plus
   `component_inventory_state` and `inventory_collection_status`. Snapshot IDs and component
   installation IDs make replay idempotent; cache-only nodes do not become installed components.
4. The current projection stores only bounded normalized metadata and path pseudonyms. Raw
   manifests, config values, commands, arguments, environment values and credentials never enter
   Postgres.
5. `installed`/`enabled` funnel stages read the current inventory projection. Later stages read
   `component_lifecycle_events` and compatible normalized component events. Funnel completeness
   is independently derived from target coverage and eligible population.
6. `/api/v1/components/inventory?kind=skill|plugin|mcp` exposes the current sanitized projection
   and target population. Skills and Plugins pages render it even when activation telemetry is
   absent.
7. Inventory tables participate in retention accounting and backup/restore validation. A daily
   audit can reconcile persisted node/edge counts with the collector status without rereading
   host configuration.

The 2026-07-29 incident amendment makes `component_inventory_state` explicitly replaceable current
state. A complete snapshot at or after the current observation removes absent current rows before
re-resolution; immutable snapshots, component dimensions and historical installed/enabled
assertions remain. Older snapshot replay and partial/degraded/unknown scans cannot resurrect stale
candidates or erase the last complete state. Runtime persists adapter-derived completeness instead
of hard-coding every successful call as `complete`.

Codex collection additionally treats installation identity as target configuration, not as a
query-time guess. The inventory collector and each rollout root use the same explicit ID; the
fallback is deterministic and cannot select a newer unrelated installation row. The rollout
parser emits a syntactically valid marker as unresolved `requested` when inventory is absent; the
versioned resolver later considers only that installation's candidates. Every reconstructed event
receives the ID in both source and scope. One physical file mapped to two IDs is a source error,
not duplicate evidence.

Live proof on 2026-07-25 found 14 installed/enabled Codex skills and two installed but disabled
plugins. A low-effort no-op canary emitted session, prompt, model and tool events but no component
identity or lifecycle event. Consequently later Codex skill stages remain `not_observed`. This is
the required honest result because the current official Codex OTel contract does not expose a
stable native skill activation event.

## E. Evidence-backed lifecycle projection and hook ingress (implemented 2026-07-26)

The follow-up audit proved that the inventory-backed half of the funnel was working while the
runtime half was not: `component_lifecycle_events` was empty, normalized `component.*` events were
not projected, and real adapter hooks were still decoded a second time as fixture-agent JSON and
quarantined.

The production path now:

1. sends already-sanitized Codex/Claude hook output through the same canonical safe-field
   validator, pseudonymization, idempotency and durable handoff used by real OTLP, retaining
   `hook_http` lineage and the adapter's `*.hook/1` schema;
2. resolves a native component identity only against current inventory with the exact tuple
   `(agent_installation_id, kind, declared_name)`; exactly one match is required;
3. persists the resolved assertion idempotently to `component_lifecycle_events` and prevents the
   compatibility union from double-counting its normalized event;
4. keeps zero-match and multi-match identities as durable facts but does not promote them into the
   inventory-backed funnel.

The funnel intentionally reports `opportunity_detected` and universal skill/plugin `succeeded` as
`unsupported` when no component-specific terminal contract exists. A successful hook ingestion,
session, tool call or child action is not component success. Claude's native `skill_activated` and
`plugin_loaded` can populate invoked/loaded after an exact inventory match. Codex's stable OTel
events still have no native skill/plugin activation; exact Codex activation needs an opt-in,
version-pinned App Server bridge consuming typed skill/plugin identities. Ambiguous ownership must
remain unpromoted.

No hook configuration was written to a user's Codex or Claude settings during this slice. The
installer contract still requires preview and explicit confirmation before any agent-facing write.
