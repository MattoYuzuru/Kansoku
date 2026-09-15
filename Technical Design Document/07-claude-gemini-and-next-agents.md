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

On `skill_activated`, `skill.name` is **already qualified** as `<plugin>:<skill>` and arrives
alongside a separate `plugin.name`. The owner namespace is applied at most once; a declared name can
never contain `:`, so an identity already carrying the prefix is upstream-qualified rather than bare.
Both the bare owner name and the `name@marketplace` form count as an existing prefix.

`skill.source`, `plugin.scope` and `enabled_via` carry Claude's own vocabulary — observed values
include `plugin`, `user-local` and `user-install` — none of which belongs to Kansoku's closed
source-scope vocabulary. They are advisory evidence and must never narrow inventory resolution; a
value outside the vocabulary is recorded with state `unknown` and opens an idempotent `info`
incident. Coercing such a value into a vocabulary member is prohibited, because a plugin-bundled
skill does not always live in the plugin cache.

`marketplace.name` is present on `skill_activated` and `plugin_loaded`. It is the exact disambiguator
the resolver currently approximates by splitting `owner.declared_name` on `@`.

`hook_registered` and `assistant_response` are emitted but undeclared, so both quarantine on every
session start. Both are metadata-only and map to `source.observed`; `assistant_response`'s response
field stays outside the allowlist and is dropped.

Claude's exposed plane is declared `unsupported`: no documented event or snapshot reports the
model-visible skill set. See TDD 14 for the resulting cold-eligibility rule.

### Reconciliation

Compare `Skill` transcript calls, skill OTel attribution and tool hooks; session/prompt/tool counts;
plugin ownership and MCP server/tool identities; parent/subagent relationships. Attribute cost/tokens
to skills/plugins only with native source semantics and retain potential double-attribution rules.

Per-skill cost and token attribution is not achievable for third-party skills. Claude stamps the
sentinel `skill.name="third-party"` / `plugin.name="third-party"` on `api_request` and on the
cost/token metrics, so those records carry no per-skill identity. This is harmless today because
only component-carrying event types resolve a component, but it bounds what the reconciliation can
ever claim.

### Skill root layout and symlinked libraries

Standalone skill roots are `skills/{user,repository,admin,system}` under the state root, plus
plugin-bundled skills discovered through the plugin cache. Only `SKILL.md` frontmatter is read.

Entries are frequently symlinks into a separate library checkout. Because the appliance scans a
read-only bind of the skill root, an **absolute** symlink resolves only if its target is also
reachable at the identical absolute path inside the container. Operators with such a layout must
bind the narrowest directory containing the targets at its identical path; never `$HOME` and never
`/`. Without that bind the targets dangle and the affected skills are invisible.

A skipped entry is never silently dropped. Each is classified into a closed vocabulary —
`unresolvable_symlink`, `unreadable_component_manifest`, `truncated_component_manifest`,
`unparseable_component_manifest` — carried on the snapshot as a coverage-gap tally, and any non-zero
tally downgrades snapshot completeness to `partial`. This matters beyond visibility: TDD 14's
cold-eligibility fallback for an unsupported exposure plane rests on inventory completeness, so a
silently truncated inventory would otherwise yield a confident cold count.

Two limits are structural rather than defects:

- **Built-in skills** are compiled into the Claude Code executable and have no on-disk `SKILL.md`.
  No filesystem scan can inventory them, so their invocations remain `unresolved` behind a typed
  exclusion. No curated catalogue is maintained, because it would drift with every release and would
  assert availability the appliance cannot observe.
- **Repository scope across several projects** produces distinct component installations for
  identically named skills, and Claude's OTel carries no project or working-directory attribute
  (`cwd` is dropped at the privacy boundary). Such names resolve to `ambiguous`, not `exact`.

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
