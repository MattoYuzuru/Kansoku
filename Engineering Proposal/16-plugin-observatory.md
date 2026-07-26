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
agent-name branching. Codex `plugin/read` is version-pinned and content-bearing fields are discarded.

The list and profile APIs expose installed/enabled/loaded planes, exact loaded-session counts,
current bundle children, collision count, version history, assertion/source timelines, incidents,
population, exclusions and completeness. `plugin.active_share/1` includes only enabled plugins with
a complete current snapshot/child graph. There is no content endpoint and plugin outcome remains
`unsupported`.
