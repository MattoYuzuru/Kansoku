# 2026-08-02 — Claude skill observatory: implementation reconciliation

Date: 2026-08-02
Scope: the three-phase handoff in `reports/2026-08-01-claude-skill-observatory-reconciliation.md`
(defects A, A-bis, C, D), decided in
`adr/0023-qualified-component-identity-and-exposure-plane-support.md`.
Branch: `fix/skill-observatory-cold-state-2026-07-26`.
Implementation state: **all seventeen handoff items are implemented, verified against the live
stack, and committed** in ten layered commits (`d0d6147`…`0598031`). Three unrelated in-flight
streams (Session 13 `entity_breakdown`, model drill-down / ADR 0022, the 2026-08-01 plugin audit
lane) remain uncommitted in the working tree and were deliberately left there; §7 lists them.

Environment: `kansoku-kansoku-1` on image `kansoku:phase3-20260802`, `kansoku-postgres-1`,
Claude Code `2.1.220`, Codex CLI `0.145.0`. Writes were confined to the repository, the appliance's
own database through its ingest surface, and `deploy/.env`. No agent configuration or skill root was
written.

## 1. Exit-gate result

- **Defect A + A-bis (identity).** A real plugin-bundled Claude skill now resolves. Replaying the
  captured `2.1.220` wire shape produced `qualified_identity` with a single owner prefix,
  `identity_resolution = exact`, `candidate_count = 1`. The raw `skill.source="plugin"` is recorded
  as-is with `source_scope_state = 'unknown'` and never coerced to `plugin_cache`; the resolver is
  no longer narrowed by it.
- **Defect C (coverage).** User-scope Claude skills went from **1 of 8** to **8 of 8** in the live
  snapshot once the link-root bind was configured. A refused or unreadable entry is now a typed
  coverage gap that downgrades snapshot completeness instead of vanishing.
- **Defect D (exposure).** Every Claude skill row now carries
  `exposure_state = "unsupported"` with the adapter-declared reason
  `claude_code_documents_no_model_visible_skill_set_event_or_snapshot`; Codex declares `native`.
  `unsupported` is never rendered as `0` and never as "not enough evidence".
- **Codex is unchanged.** Live exact-resolution counts are byte-identical to §1 of the handoff
  (`native_bridge` 15, `rollout_corroborated` 16), and the API returns the same 133 `not_observed` +
  1 `used` Codex rows with `exposure_state` `not_observed`/`observed`.
- **Budget holds.** `GET /api/v1/skills` over a 33-day range answers in 17–27 ms against the 200 ms
  `skill_observatory_range` ceiling, after the two new joins.
- **One acceptance criterion is only partly met**: an invoked *plugin-bundled* skill reaches
  `invoked_count ≥ 1` and `exposure_state = unsupported`, but stays `cold_state = not_observed`
  because the inventory graph models enablement on the plugin, not on its bundled children. See §5.

## 2. What changed, by handoff item

Phase 1 — identity (items 1–4)

| # | Landing place | Result |
|---|---|---|
| 1 | `internal/observability/ingest.go` | `qualifyComponentIdentity` is idempotent: `f(o,f(o,x)) == f(o,x)`, recognises both `owner:` and `split(owner,'@')[0]:`. |
| 2 | `internal/dataplatform/observability_handoff.go` | `canonicalSourceScopeFilter` maps vocabulary → `(raw,"observed")`, empty → `("","not_observed")`, anything else → `("","unknown")`; `ComponentSourceScopeState` on the fact scope; idempotent `info` incident `component_source_scope_unknown`. |
| 3 | `internal/claudeadapter/otel.go` | `hook_registered` and `assistant_response` declared as `source.observed`; `DocumentedSourceScopeValues()` records the observed Claude vocabulary as advisory. |
| 4 | `migrations/0017_component_source_scope_and_plane_support.{up,down}.sql` | `component_assertions.source_scope`, `source_scope_state` checked `observed|unknown|not_observed`. |

Phase 2 — exposure plane (items 5–12)

| # | Landing place | Result |
|---|---|---|
| 5 | `internal/adaptersdk/{types,manifest}.go` | `ComponentPlaneSupport{kind,plane,state,reason}` with closed vocabularies and bounds validation; a manifest omitting the field still parses. |
| 6–7 | `internal/{claude,codex}adapter/*adapter.go` | Claude declares `skill/exposed/unsupported`; Codex declares `skill/exposed/native`. |
| 8 | `0017` + `internal/dataplatform/plane_support.go` | `agent_component_plane_support` table and idempotent upsert. |
| 9 | `internal/runtime/inventory.go` | Plane support upserted per scan from the registered adapter's manifest, read from the registry — no agent-name branch. |
| 10 | `internal/dataplatform/skill_observatory.go` | `skill.cold_count/2`, `skill_profile/2`, four-branch cold switch, per-row `exposure_state`, split exclusions. A `NULL` plane support behaves exactly as before. |
| 11 | `internal/runtime/backup.go` | `agent_component_plane_support` added to the backup table list. |
| 12 | `web/src/**`, `internal/webui/dist` | `unsupported` rendered distinctly in Skills and SkillDetail; bundle rebuilt last. |

Phase 3 — coverage and deployment (items 13–17)

| # | Landing place | Result |
|---|---|---|
| 13 | `internal/claudeadapter/inventoryscan.go` | `scanSkillRoot` returns a coverage-gap tally over the closed classes `unresolvable_symlink`, `unreadable_component_manifest`, `truncated_component_manifest`, `unparseable_component_manifest`. |
| 14 | `internal/claudeadapter/{inventory,stage2_stub}.go` | `CoverageGapCount` / `CoverageGapClasses` travel on the snapshot; `Reconcile` returns `partial` on a non-zero tally. |
| 15 | `internal/codexadapter/*` | Mirrored. |
| 16 | `deploy/{compose.yaml,runtime-config.json,.env}`, `internal/runtime/config.go` | Optional identity-path binds (same variable for source and target, defaulting to `./empty-agent-state`), `link_roots` on an inventory target, bounded to 8 absolute non-root paths. |
| 17 | `internal/installer/protocol.go` | `forbidden` split into `forbidden` and `neverWritten`; `env.OTEL_EXPORTER_OTLP_HEADERS` moved to `neverWritten` for `claude.user_otel` with a disclosure; `OTEL_LOGS_EXPORTER_FILE` and `remote_endpoint` remain hard-forbidden. |

Contracts updated with paired policy-lock entries and version bumps:
`component-evidence`, `claude/hooks-and-otel`, `claude/manifest`,
`claude/skill-evidence-and-reconciliation`, `codex/skill-evidence-and-reconciliation`,
`adapter-sdk/manifest`, `adapter-sdk/inventory-graph`, `data-platform/schema`,
`privacy/installer`, `glossary`, `capabilities`. `dashboard.yaml` was verify-only as planned: its
skill panel view states already include `unsupported`.

`adapter-sdk/inventory-graph` 1.1.0 → 1.2.0 (`adapter-sdk.inventory-graph/3`) was the one contract
the handoff listed that had not been written: the snapshot now declares `coverage_gap_count` and
`coverage_gap_classes`, the closed class vocabulary, and the semantics that a non-zero tally
downgrades completeness and that an unreadable entry is never dropped. `validate_adapter_sdk.py`
compares that vocabulary against its own constant, in the same dialect as `source_scopes`.

## 3. Live verification (handoff §7)

Stack rebuilt from the working tree and restarted; one inventory scan completed at
`2026-08-02 06:04:28Z` for both targets.

```text
inventory_collection_status
  claude-local  claude  complete  nodes=171 edges=191
  codex-local   codex   complete  nodes=98  edges=124

claude skill_identity nodes in the latest snapshot
  plugin_cache 139
  user           8      (was 1 of 8 before the link-root bind)

agent_component_plane_support
  claude  skill  exposed  unsupported  claude_code_documents_no_model_visible_skill_set_event_or_snapshot
  codex   skill  exposed  native       app_server_skills_list_response
```

Replay of the captured wire shape
(`reports/artifacts/2026-08-01-skill-observatory-fix/otlp_skill_wire_replay.py`, HTTP 200):

```text
qualified_identity                  resolution  candidates  mode       source_scope  scope_state
t-skills-kotlin:kotlin-jpa-entity   exact       1           proactive  plugin        unknown
sre-agent:verification-strategy     exact       1           proactive  plugin        unknown
-- for contrast, rows recorded before the fix:
t-skills-dev-workflow:t-skills-dev-workflow:jira-workflow  unresolved  0
sde-agent:sde-agent:sde-agent                              unresolved  0
```

`incidents`: one open `component_source_scope_unknown`, severity `info`, occurrence_count 4 —
idempotent across replays.

`GET /api/v1/skills?from=2026-07-01&to=2026-08-03`:

```text
formula_version skill.cold_count/2
counts     installed 281  enabled 112  exposed 1  invoked 15  loaded 10  cold 6
exclusions unresolved_identity 37 (was 39)  partial_or_missing_exposure_window 103
           exposure_plane_unsupported_without_complete_inventory 0  ambiguous_identity 0
rows       claude  used 2 / cold 6 / not_observed 139   all exposure_state=unsupported
           codex   used 1 / not_observed 133            exposure_state observed / not_observed
```

Codex regression: exact-resolution counts match handoff §1 exactly (`native_bridge` 15,
`rollout_corroborated` 16); no Codex row changed. Latency of the same request, five runs:
`0.027 / 0.018 / 0.017 / 0.018 / 0.018` s.

## 4. Test and validator state

- `go build ./...` — clean.
- `go test ./internal/...` — green except two pre-existing red audit tests, see §6.
- `postgres_integration` suite — green except two pre-existing red `entity_breakdown` tests, see §6.
  New integration coverage passing: `TestSkillColdEligibilityAcrossEveryExposurePlaneState`,
  `TestComponentPlaneSupportUpsertIsIdempotentAndReplaces`.
- Twelve static validators exit 0, including `validate_privacy.py`, `validate_claude.py`,
  `validate_codex.py`, `validate_adapter_sdk.py`, `validate_component_evidence.py`,
  `validate_runtime.py`.
- Python contract suites: `tests.test_{claude,codex,component_evidence,observability,adapter_sdk,
  privacy,runtime,contracts,integrity,mcp,benchmarks}` — all OK.
  `tests/test_claude_contracts.py` and `tests/test_adapter_sdk_contracts.py` were shifted from
  `.../manifest/2` to `.../manifest/3` (and the non-contiguous probe to `/4`): this session's
  manifest bumps consumed `/2`, exactly as `test_codex_contracts.py` was shifted when
  `codex.manifest/2` landed.
- Frontend: six node test suites green (26 assertions), `verify:a11y-tokens` minimum contrast
  ratio 4.54 over 44 checks, `tsc --noEmit && vite build` clean, `internal/webui/dist` rebuilt from
  the same output.

## 5. Residual risks

1. **Plugin-bundled skills never become cold-eligible, because they are not `enabled`.** The graph
   emits `EdgeEnabledFor` only when `owner == nil`
   (`internal/claudeadapter/inventory.go:241`, unchanged by this work), so all 139 plugin-bundled
   Claude skills carry `enabled = false` while their 21 owning plugins carry `enabled = true`. ADR
   0023 decision 4 keeps `enabled` as an eligibility precondition, so an invoked bundled skill
   lands `invoked_count = 2, enabled = false, cold_state = not_observed`. The row is honest —
   nothing is fabricated — but the appliance now records invocations of components it also reports
   as not enabled, and those rows fall out of the denominator without landing in either exposure
   exclusion bucket. Deciding whether bundled components inherit their plugin's enablement changes
   inventory-graph semantics for skills, commands, subagents, hooks and MCP servers alike, so it is
   left to the owner rather than taken here.
2. **Multi-repository ambiguity** — repository-scope skills with the same declared name across
   several bound projects resolve as `ambiguous`. Claude emits no project attribute and `cwd` is
   dropped at the privacy boundary. Unresolvable in principle.
3. **Built-in Claude skills** (`dataviz`, `simplify`, `run`, `review`, `security-review`, …) are
   compiled into the executable with no `SKILL.md` on disk and stay permanently `unresolved` by ADR
   0023 decision 6; they are the bulk of the remaining `unresolved_identity 37`.
4. **`skill.cold_count/2` and `skill_profile/2` are not registered in `contracts/metrics.yaml`**,
   so the version transition is recorded in the ADR rather than enforced by the metric registry.
5. **Third-party cost attribution** — `skill.name="third-party"` is a sentinel on `api_request` and
   on cost/token metrics, so per-skill cost for third-party skills remains impossible.
6. **The identity-path bind places a read-only path outside `/agent-state`.** It is opt-in, defaults
   to `./empty-agent-state`, is bounded to eight absolute non-root paths, and is mounted read-only
   at the same absolute path so symlinks resolve without widening the surface further.

## 6. Pre-existing failures, not caused by this work

- `internal/claudeadapter/plugin_discovery_audit_test.go` —
  `TestAuditLane02SymlinkedStateRootDiscoversPlugins` (F-02-1) and
  `TestAuditLane02PluginBundledAgentsHooksCommandsAreAttributed` (F-02-3). Deliberately red audit
  tests from the 2026-08-01 plugin lane; they were already failing when only `otel.go` had been
  touched. F-02-3 (only `skills/` is scanned inside a plugin package) is the same modelling gap as
  residual risk 1.
- `internal/dataplatform` — `TestAuditL05PromptShapeDistinguishesEmptyBucketFromUnobservedBucket`
  and `TestAuditL05PromptShapeExposesPercentileExclusions`, from in-flight Session 13
  `entity_breakdown` work; they are the only reason `scripts/validate_data_platform.py` exits 1.
- `tests/test_plugin_contracts.py::test_active_share_population_excludes_incomplete_graph` — the
  test still reads `plugin.active_share/1` while `contracts/plugins/metrics-and-privacy.yaml` on
  `HEAD` already declares `/2`. Stale on `HEAD`, untouched here.
- Two audit tests in `internal/claudeadapter/inventoryscan_audit_2026_08_01_test.go` that were red
  at the start of the implementation session — the documented-path personal skill and the
  fabricated-zero symlink root — now **pass** as a side effect of item 13.

## 7. Commits and the verified committed tree

```text
d0d6147 feat(dataplatform): store component source scope and skill plane support
83564d6 fix(observability): stop doubling the owner prefix on Claude skill identity
20692e1 feat(skills): declare adapter exposure plane support and second cold path
1e746f1 fix(inventory): report skill scan coverage gaps instead of dropping entries
c49437d feat(deploy): mount read-only link roots for symlinked skill libraries
37d13c7 fix(installer): split never-written settings out of forbidden settings
5fa02ec chore(contracts): record skill observatory contract transitions
370e783 feat(dashboard): render unsupported skill exposure distinctly
9cff3fc docs(skills): reconcile Claude skill observatory implementation
0598031 docs(skills): add live skill telemetry evidence for the 2026-08-01 diagnosis
```

The committed tree was checked out into a scratch worktree and verified in isolation from the
uncommitted neighbours: `go build ./...` clean, `go test ./internal/...` **fully green** (14
packages, no failures — the two red audit-lane tests live in files this split leaves uncommitted),
twelve static validators exit 0, `validate_data_platform.validate()` returns no static errors, and
all eight Python contract suites pass. `deploy/.env` is git-ignored, so the local
`KANSOKU_AGENT_LINK_ROOT_1_PATH` and image pin stay out of history; `deploy/compose.yaml` carries
the bind with its `./empty-agent-state` default.

Two audit artifacts were held back from the evidence commit —
`evidence/skills/otlp-proxy-capture.log` and `…-run2-hostbug.log` — because they contain a captured
`Authorization: Bearer` value. It does not match the current `deploy/secrets/ingress_bearer`, so it
is a stale local credential, but it is still worth scrubbing or rotating before those two files go
anywhere.

## 8. Uncommitted neighbours in the same tree

Present in the working tree, unrelated to this plan, and left untouched: Session 13
`entity_breakdown` (`entity_breakdown*.go`, `model_usage_test.go`,
`prompt_shape_audit_regression_test.go`), model drill-down (`web/src/lib/modelDrilldown.ts`,
`web/tests/modelDrilldown.test.ts`, `web/src/pages/Models.tsx`, `adr/0022-*`), the 2026-08-01 audit
lane test files, and the 2026-07-30/07-31 report artifacts. The rebuilt `web/dist` and
`internal/webui/dist` bundles necessarily contain the model drill-down sources as well, since they
are one build output.
