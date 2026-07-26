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
