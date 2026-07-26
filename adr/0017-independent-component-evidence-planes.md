# ADR 0017 — Independent component evidence planes

- Status: accepted
- Date: 2026-07-26
- Owners: Kansoku adapter/runtime/data-platform/dashboard
- Supersedes in part: ADR 0016's universal lifecycle funnel
- Extends: ADR 0003, ADR 0014, ADR 0015

## Context

ADR 0016 made the original component funnel evidence-aware, but it retained a category error:
instruction components and executable components do not share universal `executed` and
`succeeded` states. A skill can be installed, exposed to a model, selected and loaded without
starting a process. A successful agent session, tool call or hook delivery does not prove that a
skill achieved a terminal outcome.

Runtime identity is also weaker than inventory identity. Promoting a declared name when zero or
multiple inventory candidates match would fabricate attribution and contaminate per-skill
analytics.

## Decision

1. Replace the universal funnel in current analytics with independent availability
   (`installed`, `enabled`, `exposed`) and runtime (`invoked`, `loaded`, uniquely attributed child
   activity, contract-defined outcome) evidence planes. Reserve optimization planes for Session 20.
2. Preserve historical `executed`/`succeeded` facts exactly. Do not rewrite or silently reinterpret
   them as invocation, load or success.
3. Persist every assertion with source lineage, bridge/adapter/schema versions, evidence tier,
   confidence, observed time, identity resolution and idempotency key.
4. Resolve runtime identity against scoped inventory. Promote exactly one candidate. Persist zero
   and multiple candidates as unresolved/ambiguous evidence under a keyed internal pseudonym and
   open idempotent incidents; do not select a winner.
5. Define cold only for enabled skills covered by complete exposure windows with zero invocation.
   Missing exposure changes completeness or demand state to `not_observed`; it is not numeric zero.
6. Allow outcome only through a registered versioned terminal contract that names start,
   terminal mapping, correlation, deadline and supported evidence tiers. Otherwise outcome is
   `unsupported`.
7. Keep agent-specific protocol mapping inside adapters/bridges. For the reviewed Codex App Server
   0.145.0 schema, `skills/list` proves exposure and a typed skill `item/started` proves invocation;
   its documented versioned rule also emits a distinct load assertion. Core queries branch only on
   assertion kinds and capabilities.
8. Expose metadata-only file-tree summaries. Session 14 adds no file-content endpoint.

## Consequences

- Installed, exposed, invoked and loaded counts may differ legitimately and each publishes its own
  population and exclusions.
- Duplicate lanes add evidence lineage without inflating a logical assertion.
- Ambiguous child ownership remains excluded from per-skill counts.
- The Skills list/profile can explain evidence without retaining instructions, prompts, responses,
  paths or tool payloads.
- Experimental App Server behavior is supported only for the reviewed version. Unknown shapes are
  quarantined visibly and degrade only that bridge capability.

## Rejected alternatives

- Keep `executed`/`succeeded` as universal empty stages. This presents unsupported semantics as a
  conversion failure.
- Infer selection from prompt text, a successful session or downstream tool activity. This is
  content-dependent and cannot prove ownership or outcome.
- Attach ambiguous runtime names to the newest or nearest inventory row. This silently invents a
  winner.
- Treat an enabled global inventory as exposure without a model-visible source assertion. This
  turns missing observation into a false fact.
- Add a read-only-looking filesystem browser. Content access has a separate Session 18 threat
  model and must not bypass it.
