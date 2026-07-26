# Session 14 — Skill observatory

## Status

Implemented on 2026-07-26. The fixture, PostgreSQL, live Codex App Server, production browser,
privacy, backup and repeated restore exit gates are green. See
`reports/session-14-reconciliation.md`.

## Purpose

Make the skill inventory explain which skills are available, which were actually selected, and
which runtime assertions are trustworthy. Replace the misleading universal component funnel with
evidence planes that fit both instruction components and executable components.

## Lifecycle redesign

The existing linear sequence is replaced by three independent planes:

1. **Availability:** installed, enabled, exposed.
2. **Runtime evidence:** invoked, loaded, attributed child activity, terminal outcome when defined.
3. **Optimization:** eligible, selected, missed; deferred to Session 20.

`executed` is not a universal skill state because a skill is an instruction bundle, not a process.
`succeeded` is not inferred from a successful session, tool call or hook delivery. A skill success
ratio exists only when that skill kind and source have a versioned terminal outcome contract.

## Evidence policy

- Inventory/configuration may prove installed and enabled.
- A documented model-visible list or equivalent native event may prove exposed.
- A typed skill selection or native activation event may prove invoked.
- A documented content-load event may prove loaded.
- A uniquely owned helper/tool/MCP action may be attributed as child activity.
- Ambiguous ownership remains plural and unpromoted.
- Reconstructed and inferred evidence never appears as native exact activation.
- Unsupported stages are compact capability explanations, not zero bars.

Claude Code's native `skill_activated` is consumed only after identity resolution and version
verification. Codex uses the optional generic bridge from Session 13 for typed selection/exposure;
stable OTel alone remains insufficient for exact skill activation.

## User experience

The Skills page provides:

- inventory and scope;
- installed/enabled/exposed/invoked/loaded counts;
- last invocation and active days;
- unique sessions;
- explicit/proactive/nested mode when native evidence distinguishes it;
- evidence tier and completeness;
- unused/cold classifications only over complete exposure windows;
- sortable links to a skill profile.

The profile shows identity, provenance, owning plugin, inventory revisions, lifecycle timeline,
evidence sources, attributed children, incidents and exact formula populations. It shows file-tree
metadata only. Reading SKILL.md, scripts and references is deliberately deferred as one complete
content-access feature in Session 18 rather than shipping an unsafe or partial viewer here.

## Opportunity detection

Opportunity is not implemented in this session. “A model thinks this skill could help” is too
subjective and would require content processing with no stable denominator. Session 20 will define
eligible/selected/missed only for versioned deterministic rules or controlled local classifiers.

## Alternatives rejected

- **Keep the eight-stage funnel and add more Unsupported labels:** preserves a category error and
  disappoints users with impossible universal metrics.
- **Treat every downstream tool as skill execution:** creates false attribution.
- **Use session success as skill success:** conflates user outcome, agent outcome and component
  behavior.
- **Ship a read-only-looking raw filesystem browser now:** violates the approved decision to make
  content access a complete, separately secured late-priority feature.

## Deliverables

- revised lifecycle/capability/formula contracts;
- durable evidence assertions and exact inventory resolver;
- Skills list and profile routes;
- source support/evidence matrices and child-attribution model;
- cold/unused formulas with complete population disclosure;
- Claude and Codex bridge fixtures plus a no-op skill live canary;
- removal/migration of the misleading universal funnel without rewriting historical facts.

## Exit gate

A namespaced no-op skill is installed, enabled, exposed and invoked through supported evidence; the
profile reconciles inventory and runtime facts exactly; duplicate lanes do not inflate counts;
ambiguous child activity stays unattributed; a missing source changes completeness rather than
usage; and no terminal success is shown without a component-specific contract.

## Implementation reconciliation

The implemented contract stores availability and runtime assertions independently and preserves
legacy lifecycle history without promotion. Codex CLI 0.145.0 App Server proved exposure through
`skills/list`; an explicit skill input appeared as a typed `userMessage` in `item/started`, not in
the empty `turn/started.items` array. That observed mapping is pinned to
`codex.bridge/0.145.0`. It yields separate `invoked` and `loaded` assertions only because this
reviewed bridge rule declares both; core component logic has no Codex branch.

The live no-op population reconciled to installed=1, enabled=1, exposed>=1, invoked=1 and loaded=1,
with exact identity and one unique session. Outcome remains `unsupported`. The production list
reports cold as `enabled AND exposed in a complete observation window AND invoked=0`, formula
`skill.cold_count/1`, with population and identity/source exclusions returned beside it.
