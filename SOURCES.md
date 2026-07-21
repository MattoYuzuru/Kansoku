# Source registry

Архитектура адаптеров опирается только на документированные контракты и проверенные локальные
fixtures. Внешняя документация может изменяться; `retrieved_at` фиксирует дату проектирования, а
daily audit проверяет runtime contracts, но не считает HTML-документацию API.

## OpenAI Codex

- Skills and progressive disclosure: <https://learn.chatgpt.com/docs/build-skills>
- Hooks and tool coverage: <https://learn.chatgpt.com/docs/hooks>
- Advanced configuration and OpenTelemetry: <https://learn.chatgpt.com/docs/config-file/config-advanced>
- Configuration reference: <https://learn.chatgpt.com/docs/config-file/config-reference>
- Codex manual snapshot used during initial design:
  <https://developers.openai.com/codex/codex-manual.md>
- Retrieved: 2026-07-21.
- Relevant version: local `codex-cli 0.144.6`; official manual snapshot current on retrieval date.

Design note: Codex OTel is opt-in, prompt text is redacted unless `otel.log_user_prompt=true`, and
the `otel` configuration is ignored in project-local `.codex/config.toml`; installation therefore
targets user configuration only after preview/confirmation. Documented OTel events cover runs and
tool usage, but the review did not establish a stable public `skill_activated` event. Tool-result
events may contain an output snippet, so the ingress allowlist cannot persist the source payload.
Kansoku distinguishes native evidence from transcript/hook reconstruction.

## Anthropic Claude Code

- Monitoring and OpenTelemetry: <https://code.claude.com/docs/en/monitoring-usage>
- Hooks reference: <https://code.claude.com/docs/en/hooks>
- Tools reference and the `Skill` tool: <https://code.claude.com/docs/en/tools-reference>
- Skills: <https://code.claude.com/docs/en/skills>
- Retrieved: 2026-07-21.
- Relevant version: current monitoring documentation contains explicit behavior gates through
  Claude Code `2.1.216`: `2.1.214` gates correlation/tool-source/content-limit behavior and fixes
  progressive usage counting; `2.1.216` changes Prometheus unit compatibility and permission
  decision classifications. It also requires `2.1.193` or later for independently gated
  assistant-response text. Local read-only observation on the retrieval date is Claude Code
  `2.1.197`; that runtime is not fixture-verified and does not inherit later documentation behavior.

Design note: Claude telemetry includes documented skill-activation, plugin, MCP, tool and hook
events. Prompt/assistant/tool details and raw API bodies are separately gated and disabled by
default, while tracing remains beta. Detailed gates may expose prompts, arguments, output, paths or
whole request/response bodies, so Kansoku must keep those gates off in its proposed plan and strip
sensitive fields at the first trusted boundary regardless of source settings. Documentation
coverage and locally verified runtime coverage are recorded separately; neither is a support claim.

## Google Gemini CLI

- OpenTelemetry event/metric/trace catalog: <https://geminicli.com/docs/cli/telemetry/>
- Hook schemas: <https://geminicli.com/docs/hooks/reference/>
- Hook implementation guide: <https://geminicli.com/docs/hooks/writing-hooks/>
- Configuration: <https://geminicli.com/docs/reference/configuration/>
- Retrieved: 2026-07-21.
- Relevant version: unversioned current documentation; hooks guide reports last update
  2026-03-20. Runtime version remains unverified until Session 07 fixtures.

Design note: Gemini exposes prompt length directly, but the current telemetry documentation lists
`logPrompts` defaulting to `true`. Any Kansoku installation plan MUST preview and explicitly set it
to `false`; runtime collection must still reject `prompt`, `function_args`, raw hook `tool_input`,
transcript paths and working directories. Gemini's OTel/hook contracts remain the third model for
validating a generic adapter, not a support claim.

## Cursor

- Hooks: <https://cursor.com/docs/hooks.md>
- Agent Skills: <https://cursor.com/docs/skills.md>
- MCP: <https://cursor.com/docs/mcp.md>
- Retrieved: 2026-07-21.
- Relevant version: unversioned current documentation; runtime version remains unverified.

Design note: Cursor now documents project/user hooks, session/tool/MCP/subagent events, portable
skills and MCP configuration. Hook payloads can include prompts, tool input/output, commands and
paths, and command hooks fail open on nonzero exit codes other than the documented block code.
Cursor therefore remains an experimental inventory/hook feasibility probe until sanitized fixtures,
version bounds and end-to-end tests exist. No native OTel export contract was established.

## OpenTelemetry

- Collector receivers: <https://opentelemetry.io/docs/collector/components/receiver/>
- Collector deployment patterns: <https://opentelemetry.io/docs/collector/deploy/>
- GenAI semantic conventions:
  <https://opentelemetry.io/docs/specs/semconv/gen-ai/>
- Dedicated GenAI semantic-conventions repository:
  <https://github.com/open-telemetry/semantic-conventions-genai>
- Retrieved: 2026-07-21.
- Relevant version: the OpenTelemetry documentation renders semantic conventions `1.43.0`, but
  the GenAI page is a move notice and the dedicated repository has no published release.

Design note: Kansoku may map stable source fields into its own versioned envelope, but MUST NOT use
the moving GenAI repository `main` branch as an implicit production schema. Every adopted snapshot
requires a pinned revision, adapter version and schema fingerprint.

## Source maintenance policy

For every supported agent release:

1. record executable version, config source and schema fingerprint;
2. replay pinned sanitized fixtures;
3. run passive health checks daily;
4. run opt-in live canaries on a bounded schedule;
5. review official changelog/docs when a version or fingerprint changes;
6. never auto-adapt a parser based only on prose or an untrusted payload;
7. mark affected intervals incomplete until reconciliation succeeds.
