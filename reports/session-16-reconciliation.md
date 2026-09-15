# Session 16 reconciliation — Plugin observatory

Date: 2026-07-26

Status: **live exit gate green**

## Acceptance result

Session 16 represents plugins as versioned, snapshot-scoped bundle graphs instead of flat
components. Migration 0011 adds immutable relation observations with inventory-snapshot identity,
source lineage, observation time, completeness, adapter/schema versions and idempotency. The shared
inventory vocabulary now covers plugin-provided skills, hooks, MCP servers/tools, commands and apps.

Installed, enabled and loaded remain independent assertions. A child invocation is attributed to a
plugin only when the current snapshot supplies exactly one direct or one-hop owner in the same
installation. Zero or multiple owners produce no aggregate assertion. Child facts are never moved
or duplicated, and no universal plugin-success state or ratio exists.

The API/UI surface is:

```text
/components/plugins
/components/plugins/{plugin_id}
/api/v1/plugins
/api/v1/plugins/{plugin_id}
```

The list and profile expose provenance, scope, versions, installed/enabled/loaded planes, loaded
sessions, current bundle children, exact child activity, collisions, incidents and explicit
population/exclusion/completeness. There is deliberately no plugin-content endpoint.

## Deterministic reconciliation

The PostgreSQL-tagged Session 16 integration creates a namespaced canary bundle containing a skill
and no-op MCP server/tool. It proves:

- one package identity and version remain distinct from their child identities;
- current direct and one-hop ownership resolve to exactly one plugin;
- one child call produces one plugin `child_activity` assertion while preserving the original
  child call;
- replay leaves both child and aggregate counts unchanged;
- a same-name collision is excluded rather than guessed;
- an upgrade changes current graph/version without erasing historical observations;
- disabling changes current state without deleting load or child-activity history;
- source loss becomes incomplete/unknown instead of a silent zero;
- no plugin outcome or success ratio is fabricated.

`plugin.active_share/1` includes only enabled plugins whose current inventory and bundle graph are
complete. Every percentage returns numerator, denominator, exclusions and completeness. The
plugin-list and profile query budgets are both 200 ms.

## Real agent and protocol canary

The real Codex App Server from CLI `0.145.0` returned the local canary through `plugin/read` as one
plugin at local version `0.1.0`, with one enabled namespaced skill and one named MCP server. Only
bounded identity, version, enabled-state and membership fields were projected. Content-bearing
fields, descriptions, prompts, URLs, marketplace paths and local paths were discarded.

The canary's no-op MCP process separately completed initialize/list/call with one named tool and one
completed call. The fixture has no network egress and does not read the user repository. It was
validated as a plugin bundle in an isolated dependency environment; no user marketplace, plugin
installation or agent configuration was changed.

Claude Code `2.1.197` documentation and adapter contracts cover independent
`plugin_installed`/`plugin_loaded` evidence and bounded bundle counts. A live Claude plugin was not
installed for this gate, and detailed telemetry remained disabled.

## Production reconciliation

The final production image is:

```text
sha256:28131981e54aefa1d1d1e0faf267eb0d1864bb4d2ac4d56b5cc2baf6bbc1bed3
```

It is healthy, and migration 0011 applied at `2026-07-26 18:59:57.576891+00`.

Production retained its two pre-existing plugin installations unchanged. Both are disabled, have
version `not_observed`, and have no observed runtime load or child activity. The static inventory
contains two pre-existing MCP relations, but migration 0011 correctly has zero snapshot relation
observations because no native bundle enumeration has yet supplied them. The list therefore reports:

```text
installed=2 enabled=0 loaded=0 active=0 cold=0
active population=0/0 completeness=unknown
```

Each plugin profile returns one version record, its installed assertion and inventory source, zero
current children, zero incidents, population `0/0`, bundle completeness `unknown`, and outcome
`unsupported`. No content field is present. Database reconciliation found zero orphan
installations, zero orphan relations, zero orphan relation observations and zero fabricated
outcomes.

Chrome 150 headless verified the final production list and an opaque-id profile. Both rendered the
same counts, populations and unknown/unsupported states returned by the API; no silent zero or
unresolved identifier appeared.

## Privacy, retention, backup and restore

The native privacy canary scanned ten accepted and ten rejected sinks:

```text
content canary matches=0
secret-format matches=0
backup checksum/exact bytes=true
```

Plugin contracts additionally prohibit prompt/response text, source code, tool or MCP arguments and
results, error text, environment values, credentials and unredacted local paths. Unknown source
shapes quarantine as metadata instead of being silently coerced.

Relation observations follow component-inventory retention. Backup table order includes them after
their relation/snapshot parents. Production backup
`backup_f0cfcdcbc296d3078448a058a3acc7c4` has archive SHA-256
`e0eb59450af01c1faee1570938503f2e71baff9f6bd21d11289adc52e6a2c2f5`, records all 11
migrations and the exact zero-row relation-observation state. Three isolated restore-verification
runs passed; the final run reconciled the restored table counts and migration 0011 explicitly.

## Resource and query evidence

The production plugin graph query executed in 0.299 ms and used the relation-observation index;
sequential scans were limited to tiny dimensions. Across ten authenticated API reads, p90 latency
was 4.906 ms and maximum latency was 8.397 ms.

The new empty relation-observation table occupies 40,960 bytes; total database size was 32,847,551
bytes at measurement time. Container memory samples were approximately 206–220 MiB within the
768 MiB limit. CPU samples varied from idle to 109.28% because existing scheduled batch work ran
during the window, so they are not presented as isolated Session 16 CPU cost. Session 17 owns
continuous self-observability and component-level overhead separation.

## Verification

```text
go vet ./...
go test ./...
python3 -m unittest discover -s tests -p 'test_*.py'  # 167 pass
python3 scripts/validate_adapter_sdk.py
python3 scripts/validate_claude.py
python3 scripts/validate_codex.py
python3 scripts/validate_component_evidence.py
python3 scripts/validate_contracts.py
python3 scripts/validate_data_platform.py --contracts-only --json
python3 scripts/validate_data_platform.py --runtime-only --json
python3 scripts/validate_incidents.py
python3 scripts/validate_integrity.py
python3 scripts/validate_mcp.py
python3 scripts/validate_observability.py
python3 scripts/validate_plugins.py
python3 scripts/validate_privacy.py
python3 scripts/validate_runtime.py
python3 scripts/run_privacy_canary.py
npm --prefix web run typecheck
web/scripts/build-and-embed.sh
```

All contract validators passed. All 167 Python tests, Go vet and the full Go suite passed. The
PostgreSQL runtime suite passed migration, exact attribution, collision, upgrade, disable,
source-loss, replay, retention and backup/restore scenarios. TypeScript typecheck, the production
frontend build and embedded-bundle tests passed.

## Residual risks

- Codex App Server plugin methods are experimental and pinned to observed CLI version `0.145.0`;
  unknown future shapes quarantine visibly.
- The scheduled default-safe config scanner observes installation/configuration but does not invoke
  experimental `plugin/read`. Until a native enumerator supplies an explicit complete bundle
  snapshot, a plugin with zero observed children remains `unknown`, not a proven empty bundle.
- Claude exact third-party plugin identity remains `not_observed` when default-safe telemetry
  redacts it; the gate did not enable detailed telemetry or alter Claude configuration.
- The canary covers skill and MCP membership. Hook, command and app membership are covered by the
  closed contracts/model/parser tests, not by a non-empty real bundle in this run.
- CPU samples include existing scheduler work. Session 17 is the planned source of continuous
  isolated operational time-series.
- Plugin content remains intentionally unavailable until Session 18; enable/disable mutations
  remain deferred to Session 19.
- `npm audit` still reports one high-severity development-dependency advisory. Session 16 did not
  force a dependency rewrite unrelated to the plugin gate.
