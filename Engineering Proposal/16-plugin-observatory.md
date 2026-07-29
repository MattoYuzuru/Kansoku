# Session 16 — Plugin observatory

## Status

Implemented on 2026-07-26. The live exit gate and residual-risk record are in
`reports/session-16-reconciliation.md`.

## Purpose

Represent a plugin as a versioned bundle graph rather than a flat component. Show what a plugin
contributes, when it is loaded and which child components are actually used.

## Product model

The canonical graph is:

```text
plugin package/version
  -> skills
  -> hooks
  -> MCP server instances/tools
  -> commands/apps
```

Installed, enabled and loaded are plugin-level assertions. Invocations and outcomes normally belong
to child components. A plugin has no universal succeeded state.

## User experience

Plugin list and profiles show provenance, scope, version, inventory revisions, loaded sessions,
bundle children, exact child usage, MCP/skill/hook summaries, incidents and completeness. Active
plugin share is computed only when the enabled population and child graph are complete.

Content reading is not included here. Session 18 adds the complete shared content-access boundary
for skill and plugin bundles in one implementation.

## Deliverables

- durable plugin-to-child relations and collision/version semantics;
- plugin load evidence and child attribution;
- plugin list/profile UI with bundle tree;
- formulas for loaded sessions, active share and cold plugins;
- test plugin containing a no-op skill and MCP server;
- drift, replay, privacy and source-loss tests.

## Exit gate

The test plugin appears as one bundle with exact child relations, load evidence is not confused with
installation, child calls are counted once, disabling does not erase history and no aggregate plugin
success is fabricated.

## Implemented reality

Plugin identity remains a regular capability-backed component identity. Inventory snapshots now
append relation observations carrying source lineage, adapter/schema versions, completeness and an
idempotency key; the static relation dimension is not treated as proof that an edge is current.
Codex and Claude adapter-owned inventory descriptors produce bundle children without core
agent-name branching. Codex 0.145.0 `plugin/read` is version-pinned and now demultiplexed by the
supervised bridge. It adds only daily-bucketed `requested` and conditional installed/enabled
metadata assertions for the plugin and owner-qualified children; content-bearing fields are
discarded. It never fabricates plugin invocation, load or success.

The production Codex inventory collector also performs a bounded, one-level, read-only walk of
each explicitly mounted `CODEX_HOME/plugins/cache/<marketplace>/<plugin>/<version>/skills`
catalog. This local layout is observed for Codex 0.145.0, not promoted to a universal Codex
contract. An enabled `plugin@marketplace` is joined to bundled skill identities only when exactly
one cached version exists. Multiple versions remain cache-only and unresolved rather than
selecting an arbitrary owner. Cache-only plugin children never enter installed/enabled component
projections. Scanner truncation, read failure or aggregate-limit exhaustion produces a visible
inventory coverage gap, not a complete zero-component result. Identical copies across mounted
profiles collapse only when identity, version, enabled state and every child fingerprint match;
any disagreement remains an explicit collision.

Codex ownership is resolved only inside one stable logical installation shared by the mounted
inventory target and ordinary-CLI rollout watcher. Exact App Server metadata joins that population
only when its producer supplies the same explicit installation ID; selecting a dynamic “latest”
database installation is not an admissible identity source.

The list and profile APIs expose installed/enabled/loaded planes, exact loaded-session counts,
current bundle children, collision count, version history, assertion/source timelines, incidents,
population, exclusions and completeness. `plugin.active_share/1` includes only enabled plugins with
a complete current snapshot/child graph. There is no content endpoint and plugin outcome remains
`unsupported`.

## 2026-07-28 identity and native-load amendment

Plugin observatory exclusions filter `component_kind = plugin`. Claude 2.1.197 `plugin_loaded`
provides native load evidence with safe scope, `enabled_via`, owner identity and an HMAC of
`plugin_id`; no raw upstream identifier is retained. Plugin-provided skills use owner-qualified
identity. A unique basename may be linked to a marketplace-qualified inventory identity, while
duplicate basenames remain ambiguous and are never selected arbitrarily.

## 2026-07-29 catalog and usefulness amendment

The Plugins list groups same-named variants inside one agent into one browsing row while preserving
marketplace, cache, profile, version and collision variants. Plugin names, installed variants,
active plugins, plugin loads and exact child uses are now separate counts.

Plugin detail ranks bundled children by exact attributed use, keeps zero-use children visible and
charts the top distribution. The UI continues to say that child activity proves a uniquely owned
child action, not a plugin invocation or plugin-level success. `ADR 0021` owns the presentation
fold and its bounded read behavior.
