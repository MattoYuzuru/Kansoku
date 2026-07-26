# ADR 0019 — Snapshot-scoped plugin bundle attribution

Status: accepted, 2026-07-26.

## Context

`component_relations` is a stable identity dimension. On its own it cannot say whether a plugin
still owns a child after upgrade, disablement or source loss. Summing child activity through every
historical relation would double count shared children and would silently turn incomplete
inventory into a complete graph. Plugin success is also not a portable agent concept.

## Decision

Keep relation identities append-only and add `component_relation_observations` as immutable
snapshot-scoped evidence with full source/version/completeness/idempotency lineage.

Attribute a child fact to a plugin only when current observed edges resolve exactly one plugin owner
in the same agent installation. Support direct children and MCP tool grandchildren. Preserve the
child fact and append one metadata-only `child_activity` assertion to the owner. Zero or multiple
owners produce no plugin aggregate.

Compute active share over enabled plugins whose current inventory snapshot and bundle graph are
complete. Keep installed, enabled and loaded independent. Keep plugin outcome unsupported unless a
future plugin-specific terminal contract is explicitly registered. Expose no content endpoint
before Session 18.

`app_definition` and `provides` are additive inventory vocabulary. A downgrade removes the new
observation table but does not narrow those enums, because doing so could require deleting user
inventory.

## Rejected alternatives

- Treating static relations as current: loses source-loss and upgrade validity.
- Copying child outcome to the plugin: fabricates a universal success semantic.
- Splitting one child fact among owners: invents fractional evidence and hides ambiguity.
- Deleting relations or assertions on disable: destroys history and breaks replay auditability.
- Storing plugin manifests or skill/MCP content: violates the default privacy boundary.

## Consequences

Plugin queries require a current inventory snapshot and relation-observation joins. Incomplete
graphs reduce the denominator and appear as explicit exclusions. Adapters may add new child kinds
through shared inventory capabilities without core brand branching.
