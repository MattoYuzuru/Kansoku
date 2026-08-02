# ADR 0022 — Fleet, installation and model analytics hierarchy

## Status

Accepted on 2026-07-31.

## Context

The dashboard exposed agent installations and models as sibling navigation items, but did not
make their analytical relationship explicit. Installation profiles already contained exact
per-model rows and source evidence, component counts and incident counts. The global Models page
already answered the separate fleet-wide comparison question.

Replacing `/models` with an installation-only view would break bookmarks, erase the useful
cross-installation comparison and create a migration obligation. A path such as `/models/:id`
would also be unsafe for opaque provider model IDs that may contain slashes or reserved URL
characters. Components and incidents do not yet have an exact installation-filtered list API, so
the UI must not imply that their global pages are installation-scoped.

## Decision

1. `/agents` remains the canonical, stable URL and becomes the **Fleet** landing page. No duplicate
   `/fleet` alias is introduced.
2. `/agents/:id` remains the installation root. It presents the hierarchy
   `Fleet → Installation → Models / Sources / Components / Incidents`.
3. Models and sources use the exact installation profile. Component and incident links are
   explicitly labelled global until an exact installation-filtered API exists; the installation
   KPI summaries remain visible.
4. `/models` remains the canonical global cross-installation comparison.
5. Selecting a model opens `/models?model=<encoded-id>`. The query form preserves model IDs with
   slashes and reserved characters, supports direct entry and reload, and avoids path ambiguity.
6. A selected model adds a filtered drill-down panel but does not filter or remove the global
   comparison table. Returning to `/models` restores the unfiltered state through ordinary browser
   history and links.
7. Model links are available both from the global comparison and from installation profiles.
8. Every model context exposes cost coverage, success/failure outcome coverage and the count of
   unknown, cancelled, interrupted or otherwise excluded outcomes. None is coerced to failure.
9. The primary visuals remain time-series lines, stacked or grouped bars and comparison tables.
   No donut is added because the model population is neither fixed nor bounded to a small set.

## Consequences

- Existing `/agents`, `/agents/:id` and `/models` bookmarks remain valid; no redirect or data
  migration is required.
- Direct model links can be shared without weakening the global fleet comparison.
- `model_breakdown/2` is required because model success/failure fields now contain the exact
  registered success/failure population instead of legacy zero values; other outcomes remain
  explicit exclusions.
- Components and incidents remain honest global escape hatches rather than fabricated
  installation-filtered results.
- A later exact installation-scoped component or incident API can replace the labelled global
  links without changing the hierarchy or canonical routes.

## Alternatives rejected

- **Replace `/models` with installation models:** breaks global comparison and existing deep links.
- **Add `/models/:id`:** provider model IDs may contain path separators and reserved characters.
- **Infer an installation filter for components/incidents:** current durable query contracts do not
  prove that population.
- **Introduce `/fleet` and redirect `/agents`:** adds two URLs for one resource without product
  value and creates unnecessary history/redirect risk.
- **Use donut charts for model share:** model cardinality is open-ended and comparison tables or
  bars retain exact values and exclusions more clearly.
