# Catalog and glossary reconciliation — 2026-07-29

## Scope

This follow-up reconciles user testing of
`reports/2026-07-28-p0-p1-incident-reconciliation.md` and
`adr/0020-postgresql-authoritative-durability-and-versioned-component-resolution.md`.
It covers repeated Skills/Plugins rows, misleading counts, component usefulness views and
unexplained dashboard terminology. It does not change agent collection, durable identity or
PostgreSQL retention/capacity.

## Validated causes

### Repeated Skills and Plugins rows

`SkillObservatory` and `PluginObservatory` start from `component_inventory_state` and return one row
per `component_installation_id`. That is correct for durable evidence identity: agent installation,
source scope, owner, version and inventory candidate can differ. The React lists rendered those
rows directly and labelled them as human-facing skills/plugins.

The observed `search-workflow`, `architecture-agent` and `architecture-capacity` repetitions are
therefore not merely unstable React keys. They are installation/source/profile variants exposed at
the wrong information-architecture level. Some may be exact copies and some may be collisions;
deleting them or merging them by name in PostgreSQL would lose the distinction.

### `Installed 274`

The former KPI incremented once for every current skill installation row. It did not mean “274
distinct skill names” and the UI did not disclose that population.

### `Invoked 3`

The former list KPI incremented once for each installation row whose `invoked_count` was greater
than zero. It meant “three installation rows had at least one invocation,” not “three invocations.”
The per-row `invoked_count` itself remains the exact event count inside the selected range.

### Plugin usage claims

Plugin inventory/load assertions and child activity are separate. A plugin is active only when
load evidence or an exactly attributed child action exists. A child action remains on the child and
does not prove a plugin-level invocation or success. The existing SRE canaries support exact child
attribution; this follow-up only changes how those facts are summarized.

## Decision

ADR 0021 introduces a presentation-only catalog family:

- one list row per normalized declared name inside one agent;
- all marketplace/cache/profile/version installations retained as visible variants;
- no database deletion, rewrite or durable identity promotion;
- exact invocation/load/child-use assertions summed across variants;
- distinct-session totals omitted at family level because the current list API cannot union their
  hidden session IDs exactly;
- family detail fan-out bounded to eight most-used variants and any exclusion stated;
- five-year retention-horizon range selected by default for Skills and Plugins.

## Implemented behavior

### Skills

- primary list is one row per catalog family and sorted by exact invocations;
- KPIs distinguish skill names, installed variants, used skills, invocation events, loads and cold
  skills;
- each row shows variant count, enabled variants, exposures, invocation/load counts, last
  invocation and conservative activity state;
- detail combines exact exposure/invocation/load dots into a metadata-only timeline;
- detail lists all current variants and merges assertions/sources by durable IDs;
- opaque installation IDs are no longer shown in the page header; variant tables use stable
  per-family profile ordinals instead of exposing those IDs.

### Plugins

- primary list is one row per plugin family and sorted by exact child activity then loads;
- KPIs distinguish plugin names, installed variants, active plugins, child uses, loads and cold
  plugins;
- detail ranks bundled children by exact use, keeps zero-use children visible and charts the top
  distribution;
- marketplace/cache/profile/version variants and collision counts remain visible;
- explanatory text does not claim plugin invocation or plugin success from child activity.

### Glossary and contextual help

- `/glossary` is a lazy, searchable route under Operations;
- the page and contextual links are generated from `contracts/glossary.yaml`;
- plain definitions now cover installed, enabled, exposed, invoked, loaded, cold, call, success,
  catalog family/variant, bundle and child activity, collision, population/exclusions, database and
  checkpoint budgets, Docker filesystem, backpressure rejection, estimated exhaustion, mirror and
  `fsync`;
- Skills, Plugins, MCP, Tools and System labels link directly to their definitions;
- contextual info targets retain a 40×40 CSS hit area.

## Privacy and observer properties

- no prompt, response, source, tool input/output, environment value, credential or raw path field
  was added;
- no agent files/configuration are written;
- no collector/parser/core domain branch was added;
- no new API mutation or external request exists;
- PostgreSQL identities, assertions, resolution history and telemetry are untouched;
- profile fan-out consists only of existing read-only GETs after a user opens one family.

## Validation evidence

Passed:

- `npm run typecheck`;
- `npm run test:component-catalog`: 5/5 tests;
- `npm run build`;
- `web/scripts/build-and-embed.sh`;
- `diff -qr web/dist internal/webui/dist`;
- `python3 scripts/validate_contracts.py`;
- `python3 scripts/validate_privacy.py`;
- `python3 -m unittest tests.test_contracts tests.test_privacy_contracts`: 37/37 tests;
- `npm run verify:a11y-tokens`: 44 checks, minimum ratio 4.5399;
- `go test ./internal/webui ./internal/runtime ./internal/dataplatform`;
- `GOCACHE=/private/tmp/kansoku-go-cache-review go test ./...` passed every package except two
  loopback-listener tests described below.

Environment-limited:

- two `internal/observability` tests attempted `listen tcp 127.0.0.1:0` and failed with
  `operation not permitted`; the sandbox prohibits loopback binds;
- Vite preview similarly failed to bind `127.0.0.1:4173`, so no new browser screenshot/live DOM
  matrix was produced in this session;
- Docker/PostgreSQL runtime access remained unavailable in the sandbox, so the exact current
  production family cardinalities and UI rendering against the live database were not re-read.

The previous 2026-07-28 report remains the runtime evidence for exact Codex replay/deduplication and
plugin child attribution. This report does not elevate static/build evidence into a new live claim.

## Residual risks and next gate

1. Same-name catalog families are intentionally a browsing convenience and can contain semantically
   different variants. The UI exposes variant counts; durable consumers must still use installation
   identity.
2. A detail with more than eight variants shows the full variant table but bounds combined
   assertion/source detail to the eight most-used variants.
3. The live acceptance gate is: start the production embedded appliance, open Skills/Plugins with
   the existing database, confirm one `search-workflow` catalog row with its expected variants and
   exact summed invocation count, open both detail charts, search the Glossary, and run the
   responsive browser matrix.
4. PostgreSQL/Docker 5 GB soak and appliance capacity remain NO-GO from the prior report; this UI
   work neither consumes nor resolves that capacity gate.
