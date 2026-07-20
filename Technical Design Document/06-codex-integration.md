# TDD 06 — Codex adapter

## Installation discovery

Discover each Codex surface independently:

1. Resolve executable and `codex --version` without login/auth output.
2. Resolve state/config root from documented environment/config (`CODEX_HOME`) before default.
3. Detect CLI/IDE/app surface where observable; do not merge installations solely by state root.
4. Fingerprint active config layers, hooks, skills and plugin manifests without recording values.
5. Record supported version match and adapter recipe version.

All file reads are bounded and symlink targets validated against approved roots.

## Sources

### `codex.hook`

Install a user-level observer covering supported events such as `SessionStart`, `UserPromptSubmit`,
`PreToolUse`, `PostToolUse`, `SubagentStart`, `SubagentStop` and `Stop`. Exact event availability is
version-manifested.

The hook helper is a small standalone binary/script that:

- reads bounded stdin JSON;
- calculates prompt features in memory for prompt events;
- allowlists session/turn/model/tool/status/timing metadata;
- sends to `http://127.0.0.1:<ingest>/v1/hooks/codex/<event>` with local secret;
- uses short timeout and never blocks Codex because Kansoku is down;
- spools only already-sanitized events to a bounded `0600` file;
- emits protocol-correct neutral output.

Hook trust/enabled state is audited. Kansoku does not bypass Codex hook trust.

### `codex.otel`

Generate a user-level `[otel]` plan using loopback OTLP, `log_user_prompt = false` and the minimum
exporters required. Map documented `codex.conversation_starts`, API/SSE/model token events,
`codex.user_prompt`, `codex.tool_decision` and `codex.tool_result` by schema fingerprint. Do not rely
on an event name that is absent from the active version manifest.

OTel attributes pass through source-specific allowlists. Output snippets/tool payloads are dropped.

### `codex.rollout`

Checkpoint importer tracks file identity, inode/file ID where available, byte offset, first/last
record fingerprint and rotation/truncation. It parses JSONL streaming, extracts canonical metadata
and never writes to the session tree.

Historical content-bearing records may be examined transiently only when the user enabled content
feature extraction; default historical mode uses safe structured fields and may report lower skill
coverage. Corrupt/unknown records generate metadata-only incidents.

### `codex.inventory`

Inventory documented system/admin/user/repository skills, enabled/disabled skill config, plugins,
marketplaces/cache, hooks and MCP servers. Repository roots are scanned only for known active
projects or explicit user targets. Cache packages are not considered enabled.

## Source-to-canonical mapping

| Source evidence | Canonical event | Tier |
|---|---|---|
| SessionStart/conversation start | `session.started` | native |
| user prompt hook/OTel metadata | `prompt.submitted` | native |
| tool pre/post or result | `tool.called` + terminal | native |
| MCP tool name | `tool.*` with MCP component relation | native |
| `$skill` explicit mention safely matched to inventory | `component.invoked` explicit | reconstructed/native when source labels it |
| bounded `SKILL.md` load evidence | `component.loaded` | reconstructed |
| uniquely owned helper call | `component.executed` | reconstructed |
| semantic opportunity classifier | `component.opportunity` | inferred |

No rule converts a helper call into `invoked` if ownership is ambiguous.

## Reconciliation

Per session compare:

- hook prompt events vs OTel prompt events vs rollout user messages;
- hook tool terminal events vs OTel results vs rollout calls/outputs;
- SessionStart/Stop/resume vs rollout lifecycle;
- subagent lifecycle vs parent transcript evidence;
- MCP call counts vs configured/advertised tools;
- component explicit/load/execute evidence without forcing equality.

Tolerance is versioned for batching and terminal delay. Missing one expected source marks only its
capabilities/interval degraded.

## Discoverability pressure

Inventory computes description byte/character totals, scope, collision/disabled state and whether
the documented initial catalog budget may be exceeded. A skill is `exposed` only from actual
session/source evidence; a risk estimate is labeled `inferred`.

## Canary

Fixture project contains `kansoku-canary-skill`, local echo MCP and harmless shell/read task. Expected
chain is versioned. Canary runs non-interactively only with consent/budget, uses no user repo and
deletes its generated workspace through a separately controlled temp lifecycle.

## Tests

- sanitized rollout fixtures for each declared version and event variant;
- hook and OTel golden maps;
- offset/rotation/truncation/replay/crash importer tests;
- skill collision and ambiguous ownership;
- `CODEX_HOME`, multi-surface and project-scope inventory layouts;
- configuration concurrent-change and rollback;
- cross-source mismatch and inactive-source logic;
- prohibited-content canaries.

## Exit gate

The compatibility matrix is backed by fixtures and live evidence, every source can fail visibly,
and the adapter never represents inferred Codex skill use as a native exact activation.

