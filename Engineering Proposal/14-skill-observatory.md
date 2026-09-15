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

## 2026-07-28 evidence-source amendment

Skill populations and exclusions filter `component_kind = skill`; plugin assertions never
contaminate the denominator. Claude 2.1.197 `skill_activated` maps to native invocation with
`user-slash`, `claude-proactive` and `nested-skill` mapped to explicit, proactive and nested modes,
including safe source/owner metadata. For ordinary Codex CLI, a `$skill` marker is only requested;
loaded/invoked reconstruction additionally requires a matching `SKILL.md` read or independent child
activity and remains visibly reconstructed. Hosted orchestration outside local collectors is
`unsupported`/`not_configured`, not numeric zero.

## 2026-07-29 live exactness amendment

A production-image canary sent a typed 0.145.0 skill item through normal `serve`. PostgreSQL
recorded `search-workflow` as `component_kind=skill`, `invoked`, `mode=explicit`,
`identity_source=native_bridge`, native evidence at confidence 1.0 and one exact inventory
candidate. Restart/reconnect duplicate replay left two assertions (invoked and loaded) and only
incremented evidence replay counts. Claude 2.1.197 plugin/skill controls still terminate with exit
137 before `skill_activated`; that source remains degraded/not-observed rather than zero.

## 2026-07-29 catalog presentation amendment

The Skills list no longer presents component-installation rows as if each were a different
human-facing skill. It groups same-named variants inside one agent into a catalog family, ranks
families by exact invocation count and separately reports skill names, installed variants, used
skills, invocation events, loads and cold skills. This is a read-only presentation fold: database
identity, collisions, source/profile/version variants and historical assertions remain unchanged.

Opening a family shows its variants and a combined metadata-only exposure/invocation/load timeline.
The profile fan-out is bounded to the eight most-used variants and states any exclusion. The default
view uses the existing five-year retention-horizon range. `ADR 0021` owns these semantics.

## Bounded rollout trust amendment (2026-07-30)

An ordinary CLI `$identity` marker is now held only in bounded process memory. It becomes
reconstructed requested/loaded/invoked evidence only after a matching `SKILL.md` read completes.
Shell variables, environment-like identifiers and currency markers without that corroboration
produce zero durable skill assertions. This narrows ordinary CLI evidence without changing exact
typed App Server selection.

The rollout reader retains at most 1 MiB of one line plus a 64 KiB reader buffer. An oversized
newline-terminated record is streamed into a one-way digest, metadata-only quarantined,
checkpointed and skipped; later valid records continue in the same scan. Raw JSONL has no durable
destination. The Skills view now presents relevant source lifecycle/health independently from
metric completeness, so a producing source cannot make an incomplete metric look complete or
vice versa.

## Exposure-plane and identity amendment (2026-08-01)

Claude skill telemetry was arriving and being persisted, yet no Claude skill was ever counted as
invoked. The cause was not ingestion but two later stages: identity resolution silently produced
zero candidates, and exposure eligibility could never hold.

Identity failed for two compounding reasons. Claude sends `skill.name` already qualified with its
owner plugin, and the appliance prepended the owner a second time. Separately, Claude's
`skill.source` carries its own vocabulary — `"plugin"` — which was used verbatim as a filter against
Kansoku's closed source-scope vocabulary, narrowing every candidate set to nothing. Either defect
alone produces a zero, so both are corrected together.

The exposure question is a product decision, not a bug fix. Claude publishes no event or snapshot
describing the model-visible skill set, so exposure cannot be observed for it at all. Three options
were considered.

Deriving exposure from the enabled list was rejected: it contradicts the standing rule that a global
enabled list is not exposure when the agent filters or applies context limits, and it would turn
`cold` — a claim shown to users — into a partly inferred number. Implementing the SessionStart hook
helper was rejected because Claude's hook payload contains no skill list, so a helper would only run
the same rejected inference inside the agent process.

The accepted option is to make the absence explicit. An adapter declares, per component kind, whether
its exposed plane is native, reconstructed or unsupported. Claude declares unsupported; Codex
declares native, pinning existing behaviour by contract rather than by absence. Where the plane is
unsupported, cold eligibility falls back to a complete inventory snapshot — exactly the basis
`plugin.active_share/2` already uses, which makes skills the outlier rather than the precedent.

This is what preserves the distinction between "we looked and saw nothing" and "there is no surface
to look at". An unsupported exposure plane renders as `unsupported`, never as zero. Because the
fallback rests on inventory completeness, an unreadable or dangling skill entry must downgrade a
snapshot to `partial` rather than being skipped silently — otherwise a mis-mounted host would produce
a confident cold count over a truncated inventory.

Two coverage limits are recorded rather than hidden. Built-in Claude skills ship compiled into the
executable with no on-disk manifest, so they stay unresolved behind a typed exclusion. Repository
skills sharing a name across several bound projects resolve as ambiguous, because Claude emits no
project attribute to separate them.

`ADR 0023` owns these semantics.
