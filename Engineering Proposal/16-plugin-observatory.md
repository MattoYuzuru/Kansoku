# Session 16 — Plugin observatory

## Status

Approved for later planning priority on 2026-07-26. Implementation has not started.

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
