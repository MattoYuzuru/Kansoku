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

## P1 native plugin evidence (2026-07-28)

Claude `plugin_loaded` becomes a native `loaded` assertion with `component_kind = plugin`.
`qualified_identity`, scope/`enabled_via`, owner identity and HMAC-only upstream identity are
allowed; raw IDs and plugin content are not. Resolution compares marketplace-qualified identities
first, then accepts a basename only when exactly one current inventory candidate exists. Skill and
plugin observatory SQL filters its exclusions and denominators by component kind.

## Codex App Server plugin metadata (2026-07-29)

Bridge `0.2.0` tracks the exact 0.145.0 `plugin/read` request ID and projects its response only
after demultiplexing the matching response. A UTC-day bucket and sequence-independent idempotency
key bound retry/reconnect amplification. Plugin rows are `requested`, plus `installed` and
`enabled` only when the native summary says so. Named bundled skill/MCP/hook/app children receive
conditional installed/enabled assertions with `owner_plugin_identity`; unsafe child names become
redacted HMAC-only assertions instead of being dropped. Raw plugin/app IDs, paths, descriptions,
URLs, source objects, app templates and scheduled-task prompts are never persisted. `plugin/read`
is inventory/lifecycle metadata and is never represented as plugin `invoked` or `loaded`.

## Codex plugin catalog inventory (2026-07-29)

`ScanHostInventory` enumerates only explicitly mounted Codex homes: the direct state root and at
most 64 immediate `state/<profile>` directories. For each home it parses the bounded
`[plugins.*]` config surface, then walks only
`plugins/cache/<marketplace>/<plugin>/<version>/skills/<skill>/SKILL.md`. Bounds are 64
marketplaces per home, eight versions per plugin, 512 versions and 2,048 skill directories across
the complete scan; `HostView` separately bounds every directory and file read. No recursion,
manifest execution or agent-directory write is permitted.

Only skill name, description byte/character counts, version, HMAC path pseudonym and content
fingerprint leave the scanner. Body, description text and raw path are discarded. A configured
`plugin@marketplace` merges with one cache version and emits an active `bundles` relation to
marketplace-scoped skills. A configured plugin with multiple cache versions stays unversioned;
all candidate versions and their children remain `cached_only`, and no ownership edge is guessed.
Cache-only children carry the cache flag through the graph and are excluded from component
install/enabled projections. When the same package is mounted through several profiles, descriptors
deduplicate only if identity, version, enabled state and every bundled child fingerprint are exact;
the deterministic minimum HMAC path pseudonym represents that logical declaration. Divergent
profiles remain distinct and collide. Any unreadable, truncated or limit-exceeded cache aborts the
partial graph; runtime derives `unknown` completeness from adapter reconciliation and stores
`inventory_source_coverage_absent`/`not_observed` instead of hard-coding `complete`.

The mounted catalog, ordinary-CLI rollout evidence and explicitly routed App Server evidence must
refer to the same logical installation ID. This prevents a qualified `plugin@marketplace:skill`
marker from being corroborated against another profile's catalog and keeps owner resolution within
one installation.

## Catalog-family UI projection (2026-07-29)

The list remains backed by `/api/v1/plugins`; the frontend derives one catalog family per
normalized declared name and agent. All plugin installation rows remain variants. Loads and exact
child-activity assertions sum across variants; loaded-session and child cardinalities are retained
as bounds when their underlying identities cannot be unioned from the list response.

Opening a family fetches at most eight existing plugin profiles. Children merge for presentation by
kind and normalized name, retain a variant count and version set, and rank by the sum of exact
usage assertions. Assertions deduplicate by assertion ID and sources by source-instance ID.
PostgreSQL relations and identities are not changed.

## Child activity non-promotion check (2026-07-30)

The PostgreSQL graph integration test now asserts both sides of the boundary: a uniquely owned
child action creates exactly one child invocation and one plugin `child_activity`, while the owner
plugin receives no fabricated `invoked`, `loaded` or terminal-success assertion. Replaying the same
source evidence preserves those counts. This remains independent from App Server `plugin/read`,
which is metadata-only installed/enabled evidence.
