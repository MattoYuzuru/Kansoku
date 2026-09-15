# ADR 0021 — Catalog families, preserved variants and contract-backed glossary

## Status

Accepted on 2026-07-29.

## Context

The PostgreSQL observatory correctly retained component installations separately by agent
installation, source scope, owner, version and inventory identity. The Skills and Plugins lists
rendered those installation rows directly but labelled their cardinalities as human-facing
component counts. This produced repeated names such as `search-workflow` and
`architecture-agent`, made `Installed` look inflated, and made the `Invoked` KPI misleading:
that KPI counted installation rows with at least one invocation rather than invocation events.

Deleting or rewriting the rows would violate the append-only evidence and collision contracts.
Automatically merging same-named components as one durable identity would also erase meaningful
marketplace, profile, owner, version and fingerprint disagreements.

The UI additionally exposed contract terms without a user-facing reference surface. Definitions
already existed in `contracts/glossary.yaml`, but there was no route or contextual navigation to
them.

## Decision

1. PostgreSQL component installations, assertions, resolutions and history remain unchanged.
2. Skills and Plugins gain a presentation-only **catalog family** keyed by normalized declared
   name inside one agent. One family is one list row; every source/profile/version installation is
   retained beneath it as a visible **variant**.
3. Family grouping is not identity resolution. Same-named divergent variants remain separate in
   storage, keep their collision/completeness state, and are never rewritten to make a count
   smaller.
4. Family invocation, load and exact-child-use totals are sums of already idempotent exact
   installation-level assertions. Distinct-session counts are not presented as exact family totals
   because the current list API does not expose the session IDs required to union them.
5. `Used skills` means catalog families with at least one exact invocation in the selected range.
   `Invocations` means the number of exact deduplicated invocation events. `Installed variants`
   exposes the underlying current installation-row cardinality.
6. Family cold state is conservative. A family with an invocation is used; it is cold only when
   every displayed variant is eligible and cold; otherwise it is not observed.
7. A family detail loads installation profiles only after navigation and is bounded to the eight
   most-used variants. The full variant list remains visible and any bounded detail exclusion is
   stated in the UI.
8. Skills and Plugins default to the existing five-year coarse retention-horizon range. The range
   remains explicit and user-selectable.
9. `/glossary` is added under Operations. Its content and contextual term links are generated from
   `contracts/glossary.yaml`; the frontend does not maintain a second definition registry.

## Consequences

- The primary lists answer the human question “which named components do I have and use?” without
  deleting evidence or pretending colliding identities are exact.
- Counts now distinguish logical browsing rows, underlying variants and event totals.
- A same-name family can still contain semantically different variants. The variant count and
  detail table make that uncertainty visible; consumers that require durable identity must
  continue using component installation IDs.
- Detail fan-out is bounded and read-only. It increases GET traffic only for the opened family,
  never during passive list rendering.
- The glossary becomes part of the dashboard acceptance contract and gains searchable plain
  definitions for lifecycle, plugin attribution and durability/capacity terms.

## Alternatives rejected

- **Delete duplicate database rows:** destroys provenance and historical telemetry.
- **Merge by name in PostgreSQL:** turns a display preference into unsupported identity promotion.
- **Keep installation rows and only rename `Installed`:** leaves the main browsing and ranking
  problem unresolved.
- **Hard-code tooltips in each page:** creates definition drift from the glossary contract.
- **Fetch every profile on list load:** creates unbounded read amplification as the catalog grows.
