# 2026-08-01 — Claude skill observatory reconciliation and implementation handoff

Scope: why Claude Code skill telemetry never reaches the Skill observatory, and what the next agent
must change to fix it without altering Codex behaviour.

Status: **investigation complete, documentation updated, code not changed.** This report is the
handoff. Decisions are recorded in
`adr/0023-qualified-component-identity-and-exposure-plane-support.md`.

Environment: local stack `kansoku-kansoku-1` / `kansoku-postgres-1`, branch
`fix/skill-observatory-cold-state-2026-07-26`, Claude Code `2.1.220`, Codex CLI `0.145.0`.
All observation was read-only: SQL `SELECT`, `GET /api/v1/skills`, `docker inspect`, read-only binds
and already-flowing loopback telemetry. No agent configuration, skill root or telemetry record was
written.

## 1. Symptom

Claude telemetry is healthy in every other respect. Event counts by type for the Claude
installation:

```text
tool.called        4833   max 2026-08-01 16:16
source.observed    4735
model.responded    4280
prompt.submitted    249
component.loaded     82
component.invoked    33   max 2026-08-01 16:10
component.executed   17
```

Skill assertions exist and are current, but none is usable:

```text
agent  kind   assertion  tier    resolution   source   count  last
claude skill  invoked    native  unresolved   native      22  2026-08-01 16:10:02
codex  skill  invoked    native  exact     native_bridge  15  2026-07-30 20:22:27
codex  skill  invoked    reconstructed exact rollout      16  2026-07-28 23:47:07
```

`GET /api/v1/skills?from=2026-07-01T00:00:00Z&to=2026-08-02T00:00:00Z`:

```text
total rows 274   claude 140   codex 134
cold_state: (claude, not_observed) 140, (codex, not_observed) 133, (codex, used) 1
exclusions: unresolved_identity 39, partial_or_missing_exposure_window 104, ambiguous_identity 0
completeness: degraded, covered_ratio 0.0095
```

So the appliance lists all 140 Claude skills and reports zero use for every one of them, while 22
real invocations sit in the database excluded.

## 2. Root causes

Four independent defects. Each alone is sufficient to produce the zero.

### A — the owner plugin is prepended to an already-qualified `skill.name`

`internal/observability/ingest.go:554-557`:

```go
qualifiedIdentity := componentIdentity
if ownerPlugin != "" && componentKind == "skill" {
    qualifiedIdentity = ownerPlugin + ":" + componentIdentity
}
```

Live wire capture (`reports/artifacts/2026-08-01-component-audit/evidence/plugins/03-claude-otlp-capture-strings.jsonl`):

```text
event.name="skill_activated"           skill.name="sre-agent:verification-strategy"
invocation_trigger="claude-proactive"  skill.source="plugin"
plugin.name="sre-agent"                marketplace.name="yuzuru-engineering"
```

`skill.name` already carries the owner. Stored `qualified_identity` values today:

```text
sre-agent:sre-agent:sre-agent
t-skills-dev-workflow:t-skills-dev-workflow:jira-workflow
t-skills-kotlin:t-skills-kotlin:kotlin-jpa-entity
t-skills-tbank-tools:t-skills-tbank-tools:nestor-search-workflow
write-kotlin                                  <- user scope, no owner, correct
```

The resolver in `internal/dataplatform/observability_handoff.go:320-371` compares against
`owner.declared_name || ':' || c.declared_name`, which no doubled value can equal.

### B (A-bis) — a non-vocabulary `skill.source` narrows the resolver to zero

The resolver's final predicate is `AND ($5 = '' OR node.source_scope = $5)`, where `$5` is
`scope.ComponentSourceScope`, carried through from `skill.source`. Only the attribute key is
remapped; the value passes through raw.

Claude sends `skill.source="plugin"`. That is not a member of `adaptersdk.SourceScope`
(`system|user|repository|admin|marketplace|plugin_cache|transient_session`). Live inventory stores
every plugin-bundled Claude skill at `plugin_cache`.

Production CTE replayed verbatim for `ain_c2e4a67af4327907cd7a66172c685713`:

| qualified identity | `$5` | candidates | resolution |
|---|---|---:|---|
| `t-skills-kotlin:t-skills-kotlin:kotlin-jpa-entity` | `''` | 0 | unresolved |
| `sre-agent:sre-agent:sre-agent` | `''` | 0 | unresolved |
| `t-skills-kotlin:kotlin-jpa-entity` | `''` | 1 | **exact** |
| `sre-agent:sre-agent` | `''` | 1 | **exact** |
| `t-skills-dev-workflow:jira-workflow` | `''` | 1 | **exact** |
| `t-skills-tbank-tools:nestor-search-workflow` | `''` | 1 | **exact** |
| `t-skills-dev-workflow:jira-workflow` | `'plugin'` | **0** | unresolved |
| `t-skills-dev-workflow:jira-workflow` | `'plugin_cache'` | 1 | **exact** |
| `whiteboard` | `''` | 1 | exact |
| `write-kotlin` | `''` | 0 | see defect C |

**Fixing A without B still resolves nothing.** They must ship together.

`plugin.scope="user-local"` and `enabled_via="user-install"` are non-vocabulary in the same way.
`enabled_via` escapes the failure only because it lands in the unconstrained `identity_source`
column, which no filter reads — which is why plugin `loaded` still resolves `exact`.

### C — dangling symlinks silently truncate user-scope inventory

Host `~/.claude/skills` holds eight skills; seven are absolute symlinks into a separate library:

```text
central-university-lms -> /Users/.../yuzuru-skills/skills/central-university-lms
github-workflow        -> /Users/.../yuzuru-skills/skills/github-workflow
...
whiteboard             (real directory)
write-kotlin           -> /Users/.../yuzuru-skills/skills/write-kotlin
```

The compose bind maps only the link directory, so inside the container the targets do not exist:

```text
$ docker exec kansoku-kansoku-1 sh -c 'for d in /agent-state/claude/skills/user/*/; do ...'
OK   /agent-state/claude/skills/user/whiteboard/
(all others: SKILL.md absent)
```

Latest snapshot `claudesnap_5190b702beda1c67255fe7e342d27ff9` contains exactly one user-scope
`skill_identity` node (`whiteboard`) against 139 `plugin_cache` nodes — and
`inventory_collection_status` reports `complete`. `claudeadapter.Reconcile`
(`internal/claudeadapter/stage2_stub.go:171-174`) returns `complete` whenever any component node
exists, so a mis-mounted host produces a confident, silently truncated inventory.

The scanner itself is not at fault: `scanSkillRoot` already accepts `entry.IsSymlink`, and the 1 MiB
`ReadConfigProbe` ceiling is far above the largest manifest (12 KiB). It simply `continue`s on an
unreadable manifest without recording anything.

### D — no exposure plane for Claude

`internal/dataplatform/skill_observatory.go:203-212`:

```go
case !row.Enabled || !completeExposure || row.ExposedCount == 0: row.ColdState = "not_observed"
case row.InvokedCount == 0:                                     row.ColdState = "cold"
default:                                                        row.ColdState = "used"
```

`complete_exposure` comes from `component_observation_windows` with `plane='availability'`. Live
counts: 60 `exposed` assertions, **all Codex**, from the App Server `skills/list` bridge. Claude has
none, and Claude Code documents no equivalent surface.

So even with A and B fixed, every Claude skill stays `not_observed`.

## 3. Corrections to earlier assumptions

- Observation windows use `plane='availability'`, not `'exposed'`. The migration check constraint
  permits only `('availability','runtime')`.
- Highest existing migration is `0016_agent_profile_covering_indexes`; the next is **0017**.
- Highest existing ADR is `0022`; this work takes **0023**.
- Contracts, `SOURCES.md` and `contracts/claude/manifest.yaml` record Claude `2.1.197`; the running
  executable is `2.1.220`.
- `hook_registered` and `assistant_response` are emitted but undeclared, so both quarantine on every
  session start.
- `marketplace.name` is emitted and dropped; it is the exact disambiguator the resolver approximates
  by splitting the owner declared name on `@`.
- `skill.name="third-party"` is a sentinel on `api_request` and on cost/token metrics, so per-skill
  cost attribution is impossible for third-party skills.

## 4. Why Codex is unaffected

Verified, not assumed:

- The qualification helper is reached only from `IngestSafeFields` (OTLP) and `IngestSafeHookFields`
  (hooks).
- `codexHookSafeFields` and `claudeHookSafeFields` (`internal/observability/routes.go`) set no
  `component_*` key at all, so the hook lane never qualifies.
- Codex OTel has no skill event (`contracts/codex/hooks-and-otel.yaml` documented events).
- Every Codex skill assertion in the live database originates from `native_bridge`,
  `rollout_corroborated`, `rollout_skill_md_read`, `rollout_marker` or `inventory` — all of which set
  `QualifiedIdentity` directly (`appserver_bridge.go:798`, `codex_rollout_watcher.go:483`) and bypass
  this code entirely.
- No agent-name branch exists anywhere in `internal/dataplatform` today; the fix must not add one.

Additionally, the qualification fix is idempotent, so it is a no-op for any caller whose identity is
unprefixed — Codex safety does not depend on the reachability argument alone.

## 5. Implementation handoff

Phases are ordered by dependency. Phase 1 alone moves plugin-bundled Claude skills from 0% to exact.

### Phase 1 — identity resolution (defects A + B)

| # | File | Change |
|---|---|---|
| 1 | `internal/observability/ingest.go:554-557` | Replace the unconditional prepend with an idempotent `qualifyComponentIdentity(ownerPlugin, identity)`. Recognise both `owner:` and `split(owner,'@')[0]:` as an existing prefix. Safe because a declared name can never contain `:`. |
| 2 | `internal/dataplatform/observability_handoff.go` | Add `canonicalSourceScopeFilter(raw) (filter, state)`. Vocabulary member → `(raw,"observed")`; empty → `("","not_observed")`; anything else → `("","unknown")`. Pass the filtered value as `$5`. Add `ComponentSourceScopeState` to `ObservabilityFactScope`. Open an idempotent `info` incident `component_source_scope_unknown` keyed on `(adapter, kind, raw)`. Do **not** coerce `"plugin"` → `plugin_cache`. |
| 3 | `internal/claudeadapter/otel.go` | Declare `hook_registered` and `assistant_response` → `source.observed` in `otelEventCanonical` and `DocumentedOTelEvents()`. Add `DocumentedSourceScopeValues()` recording the observed Claude vocabulary as advisory. |
| 4 | `internal/dataplatform/migrations/0017_*.{up,down}.sql` | `component_assertions` gains `source_scope`, `source_scope_state` (checked `observed|unknown|not_observed`). |

### Phase 2 — exposure plane support (defect D)

Per ADR 0023 decision 3, exposure becomes an adapter-declared support state.

| # | File | Change |
|---|---|---|
| 5 | `internal/adaptersdk/types.go`, `manifest.go` | `ComponentPlaneSupport{kind, plane, state, reason}` with closed vocabularies; `Manifest.ComponentPlaneSupport`; bounds validation. A manifest omitting the field must still parse. |
| 6 | `internal/claudeadapter/claudeadapter.go` | Declare `{skill, exposed, unsupported, "claude_code_documents_no_model_visible_skill_set_event_or_snapshot"}`. |
| 7 | `internal/codexadapter/codexadapter.go` | Declare `{skill, exposed, native, "app_server_skills_list_response"}` — pins existing behaviour so the regression test has an assertion target. |
| 8 | `0017` migration + `internal/dataplatform/plane_support.go` | `agent_component_plane_support` table and upsert. |
| 9 | `internal/runtime/inventory.go` | Upsert plane support per scan from the registered adapter's manifest. Read from the registry; no name branch. |
| 10 | `internal/dataplatform/skill_observatory.go` | `skill.cold_count/2`, `skill_profile/2`. Join inventory snapshot completeness and plane support. Four-branch cold switch per ADR 0023 decision 4. Row gains `exposure_state`. Split exclusions: `partial_or_missing_exposure_window` (supported planes only) and new `exposure_plane_unsupported_without_complete_inventory`. **A `NULL` plane support must behave exactly as today.** |
| 11 | `internal/runtime/backup.go` | Add `agent_component_plane_support` to the backup table list. |
| 12 | `web/src/api/types.ts`, `pages/Skills.tsx`, `pages/SkillDetail.tsx`, `lib/componentCatalog.ts` | Render `unsupported` exposure distinctly; never as `0` and never as "not enough evidence". Rebuild `internal/webui/dist` **last** — the tree already has staged deletions there from in-flight work. |

### Phase 3 — coverage visibility and deployment (defect C)

| # | File | Change |
|---|---|---|
| 13 | `internal/claudeadapter/inventoryscan.go` | `scanSkillRoot` returns a coverage-gap tally instead of silently `continue`-ing. Closed classes: `unresolvable_symlink`, `unreadable_component_manifest`, `truncated_component_manifest`, `unparseable_component_manifest`. |
| 14 | `internal/claudeadapter/inventory.go`, `stage2_stub.go` | Carry `CoverageGapCount` / `CoverageGapClasses` on the snapshot; `Reconcile` returns `partial` when the count is non-zero. |
| 15 | `internal/codexadapter/*` | Mirror 13–14 for symmetry. |
| 16 | `deploy/compose.yaml`, `deploy/runtime-config.json`, `deploy/.env` | Optional identity-path binds for symlink-target libraries (same variable for `source` and `target`, defaulting to `./empty-agent-state`), plus additional repository-scope targets. Constrain to the narrowest directory; never `$HOME` or `/`. |
| 17 | `internal/installer/protocol.go` | Split `forbidden` into `forbidden` and `neverWritten`; move `env.OTEL_EXPORTER_OTLP_HEADERS` to the latter for `claude.user_otel` and emit a disclosure. Keep `OTEL_LOGS_EXPORTER_FILE` and `remote_endpoint` hard-forbidden. |

### Contracts to update alongside the code

Contract YAMLs are machine-validated, so they were deliberately **not** edited in this
documentation pass — editing them without the code would break `scripts/validate_*.py`. Update each
with its paired `*-policy-locks.yaml` entry and version bump:

- `contracts/component-evidence.yaml` — cold formula version and eligibility, new exclusion, plane
  support block.
- `contracts/claude/hooks-and-otel.yaml` — two new events, already-qualified `skill.name` rule,
  advisory source-scope vocabulary, `marketplace.name` observed-and-dropped, version 2.1.197 →
  2.1.220.
- `contracts/claude/skill-evidence-and-reconciliation.yaml` — `exposure_plane_support: unsupported`;
  amend the cost/token attribution clause for the `third-party` sentinel.
- `contracts/codex/skill-evidence-and-reconciliation.yaml` — mirrored `native` declaration.
- `contracts/adapter-sdk/manifest.yaml`, `inventory-graph.yaml` — manifest field, snapshot coverage
  gap fields and closed vocabulary.
- `contracts/data-platform/schema.yaml` — new table and columns.
- `contracts/privacy/installer.yaml` — forbidden / never-written split.
- `contracts/glossary.yaml`, `contracts/capabilities.yaml` — cold definition, exposure term,
  observed version.
- `contracts/dashboard.yaml` — verify only; skill panel view states already include `unsupported`.

Validators to re-run: `validate_component_evidence.py`, `validate_claude.py`, `validate_codex.py`,
`validate_adapter_sdk.py`, `validate_data_platform.py`, `validate_privacy.py`, `validate_runtime.py`,
plus `tests/test_{claude,codex,component_evidence,observability}_contracts.py`.

## 6. Tests the next agent must add

- `internal/observability/ingest_test.go` — idempotence table for `qualifyComponentIdentity`,
  including `f(o,f(o,x)) == f(o,x)` and the `owner@marketplace` form.
- `internal/observability/otlp_dispatch_test.go` — **keep the existing bare-name cases unchanged** as
  the backward-compatibility proof. Add the exact `2.1.220` wire shape asserting
  `QualifiedIdentity == "sre-agent:verification-strategy"`, `SourceScope == "plugin"`,
  `InvocationMode == "proactive"`. Add `hook_registered` / `assistant_response` producing zero
  quarantine records. Add an unrecognised `invocation_trigger` asserting it is recorded rather than
  silently dropped (`otlp.go:347-358` currently `continue`s).
- **`internal/observability/otlp_codex_regression_test.go` (new, required)** — assert Codex qualified
  identity and owner plugin are byte-identical before and after; assert
  `nativeAttributeSafeSlot(adapterCodex, "skill.source")` is not ok; assert `codexHookSafeFields`
  emits no `component_`-prefixed key.
- `internal/claudeadapter/inventoryscan_test.go` — dangling symlink yields one gap of class
  `unresolvable_symlink` and emits no skill; clean root yields zero gaps.
- `internal/claudeadapter/claudeadapter_test.go` / `codexadapter_test.go` — declared plane support;
  `Reconcile` returns `partial` on non-zero gaps and `complete` on zero.
- `internal/dataplatform/skill_observatory_test.go` — all four cold branches, including the
  `NULL`-plane-support path proving today's behaviour is preserved.
- `internal/installer/protocol_test.go` — a pre-existing `OTEL_EXPORTER_OTLP_HEADERS` now yields a
  successful plan with a disclosure; the key never appears in planned writes;
  `OTEL_LOGS_EXPORTER_FILE` and `remote_endpoint` remain hard failures.

## 7. End-to-end verification procedure

1. Rebuild and restart the stack, then wait one inventory scan (`inventory_scan_interval_seconds`
   is 300) or restart the container to force one.
2. Confirm inventory coverage now includes user-scope skills:

   ```sql
   SELECT n.source_scope, count(*)
   FROM inventory_nodes n
   WHERE n.snapshot_id = (SELECT last_snapshot_id FROM component_inventory_state LIMIT 1)
     AND n.kind = 'skill_identity'
   GROUP BY 1;
   ```

   Expect user-scope > 1 once the link-root bind is configured, and
   `inventory_collection_status.status = 'partial'` while any coverage gap remains.
3. Invoke a real plugin-bundled Claude skill in an ordinary session (this session used
   `sre-agent:sre-agent`), or replay the exact wire shape with
   `reports/artifacts/2026-08-01-component-audit/evidence/skills/otlp_skill_inject.py`.
4. Confirm resolution flipped:

   ```sql
   SELECT qualified_identity, identity_resolution, candidate_count, invocation_mode, observed_at
   FROM component_assertions
   WHERE component_kind = 'skill' AND assertion_kind = 'invoked'
   ORDER BY observed_at DESC LIMIT 5;
   ```

   Expect single-prefix identities and `identity_resolution = 'exact'` with `candidate_count = 1`.
5. Confirm the observatory counts it:

   ```bash
   curl -s -H "Authorization: Bearer $(cat deploy/secrets/read_bearer)" \
     'http://127.0.0.1:43100/api/v1/skills?from=2026-07-01T00:00:00Z&to=2026-08-02T00:00:00Z'
   ```

   Expect the invoked Claude skill at `cold_state = "used"` with `invoked_count >= 1`,
   `exposure_state = "unsupported"`, `exclusions.unresolved_identity` reduced, and no Codex row
   changed.
6. Confirm the Codex regression: re-run the same query filtered to `agent_id = 'codex'` and compare
   `invoked_count`, `exposed_count` and `cold_state` against the values recorded in §1.
7. Verify the query budget still holds — `skill_observatory_range` has a 200 ms ceiling and Phase 2
   adds two joins.

## 8. Residual risks

- Repository-scope skills with identical declared names across several bound projects resolve as
  `ambiguous`. Claude emits no project attribute and `cwd` is dropped at the privacy boundary, so
  this is unresolvable in principle.
- Built-in Claude skills remain permanently `unresolved` by ADR 0023 decision 6.
- `skill.cold_count` and `skill_profile` are not registered in `contracts/metrics.yaml`; the version
  bump to `/2` is therefore recorded in the ADR rather than enforced by the metric registry.
- Per-skill cost and token attribution is unavailable for third-party skills.
- The identity-path bind places a read-only path outside `/agent-state`.
- Pre-existing and unrelated: `scripts/validate_data_platform.py` already fails on this branch from
  in-flight Session 13 `entity_breakdown` work. It was failing before this documentation pass and is
  not caused by it.
