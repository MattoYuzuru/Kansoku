# ADR 0024 — Bundled component enablement and kind preservation

Status: accepted, 2026-08-02.

## Context

Inventory adapters emit package ownership as `bundles` edges and active installation state as
`enabled_for` edges. The data platform intentionally derives a component's enabled state only from
an explicit `enabled_for` edge; it does not infer state by walking the bundle graph.

That split left bundled components in a contradictory state. An enabled plugin had an
`enabled_for` edge, while its skills, commands, hooks, subagents and MCP servers did not. They were
therefore stored as disabled even when the owning plugin was active. An invoked bundled skill could
have a positive invocation count while remaining ineligible for the used/cold population.

Two graph node kinds also lost their identity at the storage boundary: subagents were dropped, and
MCP tools were stored as custom commands. The latter merged two unrelated populations and made
command inventory counts misleading.

## Decision

1. A child bundled by a package inherits enablement only when the owner has an `enabled_for` edge
   to the same active installation. Adapters materialize that inheritance as the child's own
   `enabled_for` edge; data-platform queries remain non-transitive.
2. Cache separation remains strict. A cache-only or otherwise inactive owner has no active
   `enabled_for` edge, so its children inherit nothing.
3. Child-specific safety state still applies. Disabled skills and MCP servers, and disabled or
   untrusted hooks, do not become enabled merely because their owner is active.
4. `subagent` and `mcp_tool` become first-class persisted component kinds. Existing rows are not
   rewritten: new inventory snapshots use the expanded vocabulary, preserving historical evidence.
5. Because inherited enablement changes the eligible skill population,
   `skill.cold_count/2` and `skill_profile/2` advance to `/3`; their arithmetic is unchanged.

## Alternatives rejected

- Derive enablement transitively in every query. This duplicates graph semantics across consumers
  and makes correctness depend on each query implementing the same traversal.
- Treat all bundled children as enabled. This crosses cache separation and ignores a child's own
  disabled or trust state.
- Continue storing MCP tools as commands. This preserves schema shape by destroying the distinction
  users need when auditing their custom commands.
- Rewrite historical command rows into MCP tools. Old rows do not carry enough evidence for a safe,
  universal reclassification.

## Consequences

- Enabled plugin children are represented consistently in inventory and observatory populations.
- Cached-only plugin children remain disabled and cannot create false active inventory.
- Subagents and MCP tools become independently listable and countable after migration 0020.
- Dashboards may show a one-time increase in enabled and cold skill populations; formula version 3
  records that semantic boundary explicitly.
- Downgrade fails rather than deleting observations when rows use either newly accepted kind.
