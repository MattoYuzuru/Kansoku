# ADR 0016 — Durable component inventory and evidence-aware lifecycle funnel

- Status: accepted
- Date: 2026-07-25
- Owners: Kansoku adapter/runtime/data-platform/dashboard
- Extends: ADR 0008, ADR 0014, ADR 0015

## Context

Adapter inventory scanning existed but was reachable only from tests and transient callers.
The appliance did not mount agent state, schedule scans or persist inventory, so Overview's
component lifecycle funnel and the Skills/Plugins/MCP pages remained empty. Treating tool calls,
model requests or prompt text as proof of skill/plugin use would violate both privacy and the
evidence-tier contract.

## Decision

1. The appliance receives only explicitly configured read-only state roots. It scans them through
   bounded `HostView` probes; runtime collection never writes agent configuration.
2. Persist immutable inventory snapshots, nodes and edges plus a transactional current component
   projection and per-target collection status. IDs are deterministic/idempotent and paths are
   pseudonymized.
3. Exclude cache-only discoveries from installed component counts. Keep installed and enabled as
   separate current states.
4. Define the lifecycle funnel as an evidence funnel, not a conversion funnel:
   installed/enabled come from current inventory; opportunity/exposed/invoked/loaded/executed/
   succeeded require their own lifecycle evidence. Missing evidence is `not_observed`; measured
   absence inside a complete eligible population is numeric zero; no eligible population is
   unknown.
5. Expose current sanitized inventory through a generic component API and render it on component
   pages independently from usage telemetry.
6. Include inventory tables in retention, backup/restore and audit reconciliation.

## Consequences

- Codex skills and plugins are visible immediately after a successful scan even though current
  Codex OTel cannot prove activation.
- Claude lifecycle events may populate later stages when their documented schema/version is
  observed.
- A disabled installed plugin is visible as such; marketplace/cache presence is not promoted to
  installation.
- Operators must explicitly mount each agent profile and skill root. A missing mount is a durable
  collection-status gap, not an empty inventory.
- The no-op plugin/MCP fixture remains uninstalled until a user previews and confirms the agent
  configuration change.

## Rejected alternatives

- Infer execution from a skill name in a prompt or from a generic tool call. This is both weak
  evidence and a pressure to retain content.
- Treat every cached marketplace package as installed/enabled. This inflates inventory and makes
  the funnel misleading.
- Rescan host configuration inside dashboard queries. This breaks query budgets, lineage and
  backup/audit reconciliation.
