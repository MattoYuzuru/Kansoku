# Session 07 — Claude, Gemini and next agents

## Purpose

Prove portability with two agents that expose richer native signals and then test a fourth surface
without promising support prematurely.

## Claude Code

Claude is expected to provide the strongest initial skill evidence:

- `Skill` tool calls in local transcripts;
- hooks with transcript path and tool lifecycle;
- OTel attributes for `skill.name`, `plugin.name`, `agent.name`, tokens and cost;
- plugin/marketplace/settings inventory;
- MCP and subagent tool events.

Detailed telemetry may also expose tool parameters. Kansoku must retain names/approved metadata and
drop arguments/content at ingress. Hook, OTel and transcript sources are reconciled independently.

## Gemini CLI

Gemini is the portability validator because its documented OTel catalog and hook vocabulary differ:

- OTel logs, metrics and traces with standard GenAI attributes;
- direct prompt length with prompt logging disabled;
- `Before/AfterTool`, agent/model and lifecycle hooks;
- extension and MCP inventory;
- session/installation identifiers and model/token metadata.

Kansoku maps those capabilities to canonical events without introducing Gemini-specific core
columns. Gemini's native prompt-length field should bypass any need to inspect prompt content.

## Cursor probe

Cursor begins as an experimental adapter:

- inventory Agent Skills, hooks and MCP config;
- validate hook event stability with sanitized fixtures;
- discover local session/export sources only from official/observed contracts;
- mark unsupported token/model/implicit-activation capabilities explicitly.

It becomes “supported” only after passive and live contract suites meet the same exit gate as other
adapters.

## Future-agent onboarding checklist

1. Identify surfaces and installation identity.
2. Enumerate official OTel/hooks/export/transcript/config sources.
3. Classify every field before parsing.
4. Implement discovery and inventory first.
5. Add sanitized fixtures and schema fingerprints.
6. Map capabilities, evidence tier and unsupported gaps.
7. Add passive health and optional live canary.
8. Reconcile at least two sources for high-value capabilities when possible.
9. Publish compatibility/version matrix.
10. Refuse “complete support” until drift detection is operational.

## Deliverables

- Production Claude adapter.
- Production or beta Gemini adapter, depending on fixture/live results.
- Experimental Cursor inventory adapter.
- Cross-agent conformance report proving core independence.
- Adapter author guide refined from three different telemetry models.
- Shared dashboards with capability-driven empty/degraded states.

## Exit gate

Claude and Gemini data coexist in one canonical model; native skill/plugin attributes remain
traceable to their source; unsupported fields render as unsupported rather than zero; adding Cursor
inventory requires no core branch; privacy and reconciliation tests pass across all fixtures.

## 2026-07-25 Claude OTel correction

Claude’s short `event.name` values and typed bounded metadata now drive prompt, tool, model,
skill and plugin facts. `tool_decision` is retained only as `source.observed`; `tool_result` is the
single execution count. Prompt/response bodies and tool input/output remain unrepresentable.
