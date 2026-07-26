# TDD 16 — Plugin observatory

## Model

Reuse the inventory graph and lifecycle assertion contracts. Persist versioned plugin package,
version and installation nodes plus `bundles`/`provides` relations to skills, hooks, MCP servers,
commands and apps. Relation validity is interval/snapshot scoped; same-name packages from different
marketplaces/scopes never merge automatically.

Plugin assertions are installed, enabled and loaded. Child invocation/outcome remains on the child
and can be summarized only through an exact ownership relation.

## API and UI

Add plugin list/profile routes with provenance, scope, versions, load sessions, bundle tree, child
usage, incidents and completeness. Active plugin share requires complete enabled and child-graph
populations. No content endpoint exists before Session 18.

## Sources and tests

Map native plugin load/install evidence by adapter version, reconcile inventory and runtime identity,
and retain redacted third-party identity as unresolved. Use a namespaced canary plugin bundling the
Session 14 skill and Session 15 MCP server. Test collision, upgrade, disable, source loss, replay and
privacy.

## Exit gate

The canary bundle graph and child summaries reconcile exactly without duplicate child facts or a
fabricated plugin-success ratio.

## Implemented design

Migration `0011_plugin_bundle_graph` adds `component_relation_observations`. Each row binds an
existing relation identity to one immutable inventory snapshot plus source instance, timestamp,
completeness, adapter/schema versions and an idempotency key. `app_definition` and `provides` are
additive inventory vocabulary; downgrade intentionally leaves those safe enum widenings in place
rather than deleting user inventory.

`persistPluginChildActivity` resolves direct or one-hop nested children against only current
snapshot-observed edges in the same agent installation. It writes one plugin `child_activity`
assertion only when exactly one owner exists. The child's invocation, call and outcome rows remain
unchanged. Replay is deduplicated by source/idempotency key; disable only changes current inventory
state and never deletes historical assertions.

`GET /api/v1/plugins` and `GET /api/v1/plugins/:id` are bounded by the 200 ms
`plugin_observatory_range` and `plugin_profile_range` budgets. The React list/profile views show the
same populations and exclusions and deliberately provide no bundle-content route.
