# TDD 14 — Skill observatory

## Contract migration

Session 14 revises the canonical component lifecycle from one universal progression to independent
planes. This is a material semantic change and requires:

- a new lifecycle contract version;
- formula version changes;
- an ADR explaining removal of universal `executed`/`succeeded`;
- dashboard contract changes;
- compatibility behavior for historical lifecycle events;
- no rewriting of historical events or evidence tiers.

## Lifecycle assertions

Availability assertion kinds:

- `component.installed`;
- `component.enabled`;
- `component.exposed`.

Runtime assertion kinds:

- `component.invoked`;
- `component.loaded`;
- `component.child_activity`;
- `component.outcome` with a closed outcome enum and required terminal-contract ID.

Optimization assertions are reserved but unsupported until Session 20:

- `component.eligible`;
- `component.selected`;
- `component.missed`.

An assertion stores component installation, agent installation, optional session/turn, event/evidence
reference, assertion kind, mode, outcome/terminal contract when applicable, evidence tier,
confidence, source/adapter/schema versions, observed time and idempotency key.

Historical `component.executed` remains queryable as legacy evidence. It is not silently converted
to invocation or success. Historical `component.succeeded` is shown only with its original evidence
and compatibility warning unless it already has a valid terminal-contract lineage.

## Evidence requirements

### Installed and enabled

Read current inventory state with snapshot coverage. Complete zero is allowed only when every
configured inventory target completed successfully for the selected population.

### Exposed

Requires a source event or snapshot proving the model-visible skill set for a session/turn. A global
enabled list is not automatically exposure if the agent applies context limits or filtering.

Some agents publish no such surface at all. That absence is a property of the agent, not an
observation about a skill, so it is declared rather than inferred. Each adapter manifest states, per
component kind, whether the exposed plane is `native`, `reconstructed` or `unsupported`, with a
bounded reason. The declaration is persisted per installation and read by the data platform as data;
the core never branches on an agent name. An undeclared plane is treated as supported.

`unsupported` and `not_observed` are distinct and must stay distinct end to end. An unsupported
exposure plane renders as `unsupported`, never as `0` and never as "not enough evidence". The rule
above is unchanged by this: an enabled list still never becomes an exposure assertion. It only
becomes an eligibility precondition, and only where no exposure surface exists — see the cold
formula below.

### Invoked

Requires native typed selection or a deterministic reconstructed assertion. Explicit/proactive/
nested mode is recorded only when the source provides it.

### Loaded

Requires a documented load assertion. If a source contract guarantees that invocation loads the
instruction, one native record may support two distinct assertions with the same evidence lineage;
the relationship is explicit and versioned, not an implicit core rule.

### Child activity

Requires a unique ownership relation valid for the component version and interval. Multiple
candidate skills remain an ambiguous relation and are excluded from per-skill counts.

### Outcome

Requires a registered terminal contract naming:

- component kind/version scope;
- start assertion;
- terminal assertion;
- success/failure/cancel/timeout mapping;
- correlation key;
- terminal deadline;
- supported evidence tiers.

No terminal contract means `unsupported`, not zero.

## Identity resolution

Resolve runtime declared identity against the current/historical inventory snapshot scoped by agent
installation, kind, declared name/opaque native ID and observed interval. Exactly one valid match
promotes to inventory-backed analytics. Zero/multiple matches remain durable unresolved evidence and
can open drift/collision incidents.

## Data queries and formulas

The Skills list supports range, installation, source, scope, plugin owner and evidence-tier filters.
Metrics include:

- installed/enabled/exposed/invoked/loaded population;
- invocation sessions and active days;
- last invoked;
- mode breakdown;
- cold skill count with formula;
- outcome counts/ratio only for a common terminal-contract population;
- unresolved/ambiguous assertions;
- source completeness.

Cold means enabled, eligible and never invoked. Eligibility has two mutually exclusive paths,
selected by the adapter's declared exposure-plane support:

```text
eligible = enabled AND (
     exposure_plane <> 'unsupported' AND exposed_count > 0 AND complete_exposure
  OR exposure_plane  = 'unsupported' AND inventory_snapshot.completeness = 'complete'
)
numerator = eligible AND invoked_count = 0
```

Where the plane is supported, the original rule is unchanged: installed but never provably exposed
is not cold, it is `not_observed` for demand. Where the plane is unsupported, exposure cannot be
observed at all, so eligibility falls back to a complete inventory snapshot — the same basis
`plugin.active_share/2` already uses. This is why coverage gaps must downgrade snapshot completeness:
without that, a mis-mounted host would produce a confident cold count over a truncated inventory.

Each row carries `exposure_state ∈ {observed, not_observed, unsupported}`.

Exclusions are disjoint, so no row is counted twice:

- `unresolved_identity`, `ambiguous_identity` — unchanged;
- `partial_or_missing_exposure_window` — supported planes only;
- `exposure_plane_unsupported_without_complete_inventory` — unsupported planes whose inventory
  snapshot is not complete.

Every percentage returns numerator, denominator, exclusions, formula ID/version and completeness.

## UI

`/components/skills` becomes a linked table plus availability/runtime summaries. The old universal
funnel is removed from Overview and Skills or replaced by plane-specific summaries. Profile route:

```text
/components/skills/:opaque_component_installation_id
```

Profile sections:

- identity/provenance/scope/plugin owner;
- inventory revision timeline;
- availability assertions;
- invocation/load timeline;
- attributed child activity;
- outcomes when supported;
- evidence/support matrix;
- incidents and unresolved identities;
- file-tree metadata only.

Session 14 exposes no file-content endpoint.

## Adapter mapping

Claude `skill_activated` maps to invoked after version and identity checks. Its documented load
semantics may support loaded only through an explicit versioned rule.

Claude sends `skill.name` **already qualified** as `<plugin>:<skill>` alongside a separate
`plugin.name`. The owner namespace is therefore applied at most once. A declared name can never
contain `:` — both adapters' frontmatter parsers restrict it to `[A-Za-z0-9][A-Za-z0-9._-]{0,127}` —
so an identity already carrying the owner prefix is always upstream-qualified, never a bare name.
Both the bare owner name and the `name@marketplace` form count as an existing prefix.

`skill.source` and `plugin.scope` carry the agent's own vocabulary, not Kansoku's source-scope
vocabulary. They are advisory evidence only and must never narrow inventory resolution. A value
outside the closed vocabulary is recorded with state `unknown` and opens an idempotent `info`
incident; it is not coerced into a vocabulary member, because a plugin-bundled skill does not always
live in the plugin cache.

Claude's exposed plane is declared `unsupported`: no documented event or snapshot reports the
model-visible skill set.

Codex typed skill selection/exposure comes from the Session 13 bridge, and its exposed plane is
declared `native` so the behaviour is pinned by contract rather than by absence. OTel-only Codex
remains unsupported for exact activation. Hook/transcript reconstruction keeps its lower evidence
tier.

The core processes assertion kinds and capabilities only. It does not branch on these agent names.

## Canary

Create a namespaced no-op skill outside user repositories. The task must deterministically invoke
it and perform at most one harmless, uniquely owned child action. Record a negative canary with an
ambiguous child relation and a source-loss canary.

Use a bounded low-cost agent run. Prefer the locally available `gpt-5.6-luna` with medium reasoning
for the implementation handoff's live test; if unavailable, record the unsupported availability
and use an explicitly named approved fallback rather than relabeling evidence.

## Tests

- contract migration and historical compatibility;
- inventory resolution exact/zero/multiple;
- lifecycle assertion idempotency and lane deduplication;
- exposed/invoked/loaded version mappings;
- terminal-contract positive and unsupported cases;
- cold formula complete/partial/unknown populations;
- source removal and late evidence;
- profile/list/browser accessibility;
- no file-content route;
- privacy canary and live DB reconciliation.

## Exit gate

The no-op skill's evidence planes reconcile exactly, the old funnel no longer promises impossible
universal stages, and every unsupported or excluded result identifies the source/capability reason.

## Implemented reality (2026-07-26)

- Migration 0009 adds durable assertions, terminal contracts, observation windows and metadata-only
  file-tree summaries. Its down migration removes only Session 14 projections; shared incident
  history is retained.
- The exact resolver is scoped by agent installation, component kind, declared identity and
  inventory state. Zero and multiple candidates persist as unresolved/ambiguous evidence with an
  internal keyed pseudonym and create idempotent incidents; neither is promoted to a component.
- Inventory scans create independent installed/enabled assertions. The optional App Server bridge
  creates model-visible exposure windows from the reviewed 0.145.0 `skills/list` response.
- In the local 0.145.0 executable, typed skill selection is emitted as `item/started` with a
  `userMessage` skill-content item. `turn/started.items` was empty. Top-level `emittedAtMs` is the
  emission timestamp. The bridge accepts this exact schema and immediately discards paths and
  content.
- `skill.cold_count/1` uses numerator `cold skills` and denominator `enabled skills with complete
  exposure coverage`; unresolved identity, ambiguous identity and incomplete/missing exposure
  windows are explicit exclusions. An enabled skill without a provable exposure window is
  `not_observed`, not cold.
- `/api/v1/skills` and `/api/v1/skills/:id` expose exact populations, exclusions, completeness,
  evidence/source matrices and metadata-only file-tree summaries. No content route was added.
- The old universal Skills funnel was removed from Overview and Skills. No session/tool/hook state
  is interpreted as terminal skill success without a registered terminal contract.

The live and deterministic evidence is reconciled in `reports/session-14-reconciliation.md`.

## P1 observer mapping (2026-07-28)

Claude OTel dispatch recognizes 2.1.197 `skill_activated`, preserving only qualified skill identity,
source scope, optional plugin owner, trigger mode and an HMAC of upstream identity. Codex rollout
watching is read-only, append-only, checkpointed and rotation/truncation-aware. It parses content
transiently and persists only identity/count/trigger/lineage/redaction metadata. A marker creates
`requested`; a matching `SKILL.md` read plus native child activity creates reconstructed
`loaded`/`invoked` at 0.85 confidence.

The App Server bridge is an exact 0.145.0 JSON-RPC demultiplexer with up to 128 concurrent pending
request IDs. Responses are associated with their request method; known service traffic is filtered,
and only owned invalid frames enter quarantine. Normal `serve` supervises explicit streams on the
authenticated bounded evidence-bridge route. This remains no evidence about ordinary CLI sessions
unless their App Server stream is explicitly routed.

## Catalog-family UI projection (2026-07-29)

`web/src/lib/componentCatalog.ts` derives a stable opaque family ID from component kind, agent ID
and normalized declared name. It groups only for presentation and carries every
`component_installation_id` as a variant. Exact invocation/load assertions are additive across
variants; session cardinality is represented internally as lower/upper bounds and is not displayed
as an exact family number. Cold is emitted only when every family variant is eligible and cold.

The list query remains one bounded `/api/v1/skills` request. A family profile first resolves its
variant IDs from that response and then issues at most eight existing bounded profile GETs. Assertions
and sources deduplicate by their durable IDs before the timeline renders. No migration, source
write, collector branch or agent configuration change is introduced.

## Rollout evidence hardening (2026-07-30)

`CodexRolloutWatcher` uses a `bufio.Reader` and drains one newline-terminated record at a time.
Retention is capped at 1 MiB with a 64 KiB reader buffer. Oversized content is reduced in-stream to
a SHA-256 value used only as keyed metadata input, never stored. The watcher commits the byte
offset after metadata-only quarantine and continues with the following record; an incomplete final
line remains uncommitted until its newline arrives.

The broad dollar-marker recognizer no longer writes on recognition. It keeps at most the bounded
turn-local candidate map and requires a matching completed `SKILL.md` read before emitting
reconstructed requested, loaded and invoked assertions. Their identity, evidence tier, confidence,
installation binding and idempotency are explicit. A marker without the read is discarded on
process-memory loss and creates no durable fact. Tests cover `$PATH`, `$HOME`, ordinary identifiers
and currency, as well as oversized-then-valid replay.

Source lifecycle comes from `/api/v1/health`; formula completeness remains attached to the metric
population. The UI renders them as separate evidence surfaces.

## Claude identity and exposure amendment (2026-08-01)

Measured against the running local stack with Claude Code `2.1.220`, Claude skill telemetry was
ingested and persisted but never counted. Four independent defects were isolated; each alone is
sufficient to produce a zero. ADR 0023 records the decisions; the evidence and the ordered handoff
are in `reports/2026-08-01-claude-skill-observatory-reconciliation.md`.

- 22 Claude `skill`/`invoked` assertions existed, all at `identity_resolution='unresolved'` with
  `candidate_count=0`. `GET /api/v1/skills` returned 140 Claude rows, all `not_observed`, all
  `invoked_count=0`.
- The owner-plugin namespace was applied twice, because Claude's `skill.name` is already qualified.
  Stored identities looked like `t-skills-kotlin:t-skills-kotlin:kotlin-jpa-entity`.
- `skill.source="plugin"` was used verbatim as the resolver's source-scope filter. It is not a member
  of the closed vocabulary, and live inventory stores plugin-bundled Claude skills at `plugin_cache`.
  Replaying the production CTE for `t-skills-dev-workflow:jira-workflow` gave 1 candidate with an
  empty filter, 0 with `plugin`, and 1 with `plugin_cache`. Repairing the doubled prefix without
  also gating the scope filter still resolves nothing; the two are one change.
- Claude has no exposure surface, so `exposed_count` was 0 for every Claude skill and eligibility
  never held. Live counts: 60 `exposed` assertions, all Codex.
- Seven of eight user-scope skills were missing because `~/.claude/skills` entries were absolute
  symlinks whose targets were not mounted, yet the snapshot was still marked `complete`.

Naming correction for implementers: observation windows are stored with `plane='availability'`, not
`plane='exposed'`. The migration's check constraint permits only `('availability','runtime')`. Earlier
sections of this document use "exposure window" as prose for the availability-plane window covering
`component.exposed`.

Permanently unresolvable classes, recorded rather than papered over:

- Built-in Claude skills are compiled into the executable and have no on-disk `SKILL.md`, so no
  filesystem scan can inventory them. Their invocations stay `unresolved` behind a typed exclusion.
- Repository-scope skills sharing a declared name across several bound projects resolve to
  `ambiguous`. Claude's OTel carries no project or working-directory attribute, and `cwd` is dropped
  at the privacy boundary, so this is unresolvable in principle rather than a defect.
