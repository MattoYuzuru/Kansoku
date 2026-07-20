# Session 06 — Codex integration

## Purpose

Deliver the first deeply reconciled adapter and prove that Kansoku can handle a market-leading
agent whose documented telemetry does not necessarily expose a dedicated skill-activation event.

## Source inventory

Potential sources are independently capability-scoped:

- user-level lifecycle hooks for session, prompt and tool boundaries;
- Codex OpenTelemetry logs/traces/metrics;
- local `CODEX_HOME` session rollout JSONL and history;
- config, skills and plugin manifests across supported scopes;
- plugin cache metadata and enabled/disabled configuration;
- MCP configuration and observed MCP tool calls;
- executable/surface version and process/session freshness.

Kansoku must use documented paths/config discovery rather than assuming `~/.codex`; `CODEX_HOME`
and surface-specific state are part of installation identity.

## Skill evidence model

Codex can activate a skill explicitly (`$name`) or implicitly from descriptions. Until a stable
native event exists, the adapter records separate evidence:

- explicit user invocation from a safe native field or ephemeral prompt analysis;
- `SKILL.md` load/read evidence when observable;
- agent declaration of skill use in structured/transcript events;
- execution of a uniquely owned helper/MCP dependency;
- inferred opportunity, never promoted to actual invocation.

The dashboard must not combine these into a false exact count. It displays an evidence breakdown
and a best-supported lifecycle stage.

## Discoverability analysis

Codex limits the initial skills catalog. The adapter should calculate available description bytes,
scope precedence, duplicate names, disabled skills and catalog-pressure risk. Whether a specific
skill was actually included in model context is recorded only if the surface exposes evidence.

## Setup proposal

- Generate one trusted user-level observer hook that sends metadata to loopback.
- Configure OTel with prompt logging disabled and minimum necessary tool fields.
- Import historical rollouts read-only with checkpoints.
- Never modify project-local configs unless explicitly selected.
- Verify hook trust/enabled state and show remediation, not silent repair.

## Reconciliation examples

- Prompt hook count vs rollout user-message count.
- Tool hook calls vs OTel tool results vs rollout tool events.
- session start/stop vs rollout file lifecycle.
- executable version change vs schema fingerprint and event freshness.
- plugin/skill inventory vs current enabled config and actual component calls.

## Historical limitations

Old transcripts may contain enough content to identify a skill but violate the default privacy
boundary. Historical import parses content only in memory, emits approved features/evidence and
discards the line. Users can disable historical content parsing entirely. Unsupported old schemas
are quarantined as metadata-only incidents.

## Deliverables

- Codex inventory, hook, OTel and rollout import lanes.
- Skill/plugin/MCP evidence mapping and completeness UI contract.
- Configuration preview, backup and rollback.
- Sanitized fixtures for multiple Codex versions/surfaces.
- Daily passive probe and bounded live-canary scenario.
- Documented unsupported capabilities.

## Exit gate

On every supported Codex version, a canary session produces the expected session/prompt/tool/MCP
chain, inventory is correct, raw prompt content is absent, replay is idempotent, and disabling or
breaking any source yields a capability-specific degraded incident rather than plausible-looking
zero usage.

