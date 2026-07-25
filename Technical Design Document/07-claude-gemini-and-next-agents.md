# TDD 07 — Claude, Gemini and future adapters

## Claude Code adapter

### Discovery/inventory

Resolve executable/version, user/project/managed settings, plugins/marketplaces/cache, standalone
skills, commands, agents, hooks and MCP configuration. Preserve active vs cached distinction and
plugin → bundled component relationships.

### Sources

- Global hooks for session, prompt, pre/post/failure tool, subagent and stop events where supported.
- OTel metrics/events/traces with prompts and raw tool content disabled.
- Local project/session JSONL imported read-only with checkpoints.
- `Skill` tool calls mapped to explicit/implicit mode only when native input distinguishes it.

Claude OTel may expose `skill.name`, `plugin.name`, `agent.name`, token/cost and tool attributes.
Kansoku allowlists identity/timing/count/result fields and drops `tool_input`, parameters and content
even if users enabled detailed upstream telemetry. `skill.name` resolves against inventory; unknown
names become scoped transient components, not arbitrary stored prompt text.

### Reconciliation

Compare `Skill` transcript calls, skill OTel attribution and tool hooks; session/prompt/tool counts;
plugin ownership and MCP server/tool identities; parent/subagent relationships. Attribute cost/tokens
to skills/plugins only with native source semantics and retain potential double-attribution rules.

## Gemini CLI adapter

### Discovery/inventory

Resolve version/settings, extensions, hooks, MCP servers/tools and project/user context sources.
Telemetry config uses `enabled=true`, local OTLP endpoint, `logPrompts=false`; detailed traces remain
off unless a capability requires and privacy review approves them.

### OTel mapping

- `gemini_cli.config` → installation/surface/model/MCP/extension inventory observation;
- `gemini_cli.user_prompt.prompt_length` → exact native `prompt.submitted` size field;
- GenAI token/duration metrics → `model.*`/token facts;
- hook/tool call events and GenAI tool spans → `tool.*`/collector evidence;
- session/installation IDs scope correlation.

Never persist `prompt`, input/output messages, system instructions or tool definitions even when
tracing/logging upstream exposes them.

### Hooks

Map `SessionStart/End`, `Before/AfterAgent`, `Before/AfterTool`, model and compression hooks according
to active version schemas. The hook helper follows stdin/stdout/exit contract, performs prompt
features at boundary and remains non-blocking for observation.

## Cursor experimental adapter

Initial implementation only declares capabilities proven by official hooks/skills/MCP inventory and
local fixtures. Unknown local databases or undocumented endpoints are not reverse-engineered into a
stable claim without explicit research/security review. Token/model/session support remains gray if
no reliable contract exists.

## Cross-agent invariant tests

One logical scenario is represented with different source vocabularies:

```text
session -> prompt metadata -> skill activation -> MCP tool call -> model tokens -> success
```

Assertions use canonical capabilities, not agent IDs. Agent-specific extra events survive as
allowlisted namespaced attributes/evidence and do not require core table changes.

## Future adapter validation

Create `fixture-agent` external adapter with:

- no OTel, only a versioned local event file;
- component named “recipe” instead of skill;
- non-UUID session identifiers;
- missing token capability;
- one deliberately unknown schema.

It must inventory, ingest, degrade, reconcile and render correctly through the public SDK.

## Tests

- Claude Skill/OTel/hook/transcript golden and privacy fixtures;
- Gemini OTel protobuf/log/metric/trace and hook schemas;
- detailed telemetry enabled upstream to prove ingress stripping;
- agent upgrades/fingerprints and partial capabilities;
- plugin/extension ownership and duplicate component names;
- cross-agent formula equality and unsupported-state rendering;
- external fixture-agent conformance.

## Exit gate

Claude and Gemini reach declared support levels without core agent conditionals; Cursor is labeled
only to proven capability; all content-rich fields are rejected before durable storage; the fake
adapter validates future extensibility.

## Claude OTel mapping (reconciled 2026-07-25)

`user_prompt`, `tool_result`, `api_request`/`api_error`, `plugin_installed`, `plugin_loaded` and
`skill_activated` map onto canonical facts using only documented bounded metadata. Prompt IDs are
device-keyed turn pseudonyms. `tool_decision` becomes `source.observed` so it remains auditable
without doubling the execution counted from `tool_result`.
