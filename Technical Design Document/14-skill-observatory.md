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

Cold means enabled and exposed during a complete observation window with zero invocation. Installed
but never provably exposed is not cold; it is `not_observed` for demand.

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

Codex typed skill selection/exposure comes from the Session 13 bridge. OTel-only Codex remains
unsupported for exact activation. Hook/transcript reconstruction keeps its lower evidence tier.

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
