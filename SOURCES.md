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

Session 02 re-check: the current manual still places OTel in user configuration because project
`.codex/config.toml` ignores `otel`. The install contract therefore previews only
`codex.user_otel`, keeps `log_user_prompt=false`, and treats every source event—including the
documented tool-result output snippet—as untrusted content. The re-check was documentation-only;
no user Codex config was read or changed. Effective configuration order was also re-checked as CLI
overrides, trusted project layers, profile, user, system and built-ins; because project `otel` is
explicitly ignored, the effective OTel path skips that project layer. Managed requirements constrain
otherwise resolved values and are checked separately.

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

Session 02 re-check: a proposed user-settings diff must explicitly keep
`OTEL_LOG_USER_PROMPTS`, `OTEL_LOG_ASSISTANT_RESPONSES`, `OTEL_LOG_TOOL_DETAILS`,
`OTEL_LOG_TOOL_CONTENT` and `OTEL_LOG_RAW_API_BODIES` off while targeting loopback OTLP. Hook input
also contains transcript/current-working-directory paths and tool input/output, so flags are only
defense in depth. Official settings precedence was re-checked as managed, command line, local,
project, then user; process environment is additionally inspected as effective runtime state. No
`.claude/settings.json` or environment was read or changed.

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

Session 02 re-check: the consent registry requires `target=local`, a loopback endpoint,
`logPrompts=false` and `useCliAuth=false`; `outfile` and GCP export are forbidden in the default
plan. The official hook schema still exposes absolute transcript/current-working-directory paths,
raw `tool_input` and `tool_response`. Official configuration order was re-checked as built-ins,
system defaults, user, project, system overrides, environment and command line (later layers win).
Gemini CLI was not present locally and no settings file was read or changed.

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

Session 02 re-check: the current hook reference documents `workspace_roots`, `user_email`,
`transcript_path`, prompt, command, tool input/output and agent content. Command hooks block only on
the documented exit code `2`; other failures proceed, so Cursor hooks are a collection surface and
cannot be the privacy enforcement boundary. The same current reference states that all matching
hooks run and merge priority is Enterprise, Team, Project, User. Cursor was not present at the
checked application path, and no hook config was read or changed.

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

## Session 02 implementation infrastructure

- Go release history: <https://go.dev/doc/devel/release>
- Go vulnerability management: <https://go.dev/doc/security/vuln/>
- Docker Compose service reference: <https://docs.docker.com/reference/compose-file/services/>
- Docker Compose network reference: <https://docs.docker.com/reference/compose-file/networks/>
- Docker Compose secrets reference: <https://docs.docker.com/reference/compose-file/secrets/>
- PostgreSQL official container image: <https://hub.docker.com/_/postgres>
- Retrieved: 2026-07-21.
- Relevant versions: offline pinned toolchain reports `go1.26.5 linux/arm64`; local validation uses
  Docker Engine `29.5.3` and Compose `5.1.4`; the Compose template retains the measured PostgreSQL
  18 digest from Session 01 and requires an exact application-image digest.

Design note: the Go official release history records 1.26.5 on 2026-07-07 with security fixes. The
Session 02 module has no third-party Go dependency, and all Go tests/vet/fuzz/benchmark runs use the
pinned toolchain with network disabled. The local Docker installation exposes neither Scout nor an
SBOM plugin, and no production Kansoku image exists, so `reports/session-02-sbom.json` is a source
inventory rather than a signed release SBOM. Production image scanning/provenance remains a blocking
Session 09/10 gate. Compose documentation supports the static service/network/secret fields, but a
successful `compose config` is not runtime reachability or soak evidence.

## Source maintenance policy

For every supported agent release:

1. record executable version, config source and schema fingerprint;
2. replay pinned sanitized fixtures;
3. run passive health checks daily;
4. run opt-in live canaries on a bounded schedule;
5. review official changelog/docs when a version or fingerprint changes;
6. never auto-adapt a parser based only on prose or an untrusted payload;
7. mark affected intervals incomplete until reconciliation succeeds.
