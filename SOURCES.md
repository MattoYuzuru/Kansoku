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

2026-07-25 correction (source: `openai/codex` main-branch code read, not documentation, plus a
live debug capture against the locally-installed `codex-cli 0.145.0`): the OTel resource
`service.name` is not a single fixed literal. Real Codex builds set it from the process
"originator" string (`codex-rs/core/src/otel_init.rs`), which defaults to `codex_cli_rs`
(`codex-rs/login/src/auth/default_client.rs`) for the interactive TUI, but is overridden per
surface: `codex_exec` for `codex exec` (`codex-rs/exec/src/lib.rs`, confirmed live), `codex_mcp_server`
for `codex mcp-server`, and `codex-app-server` for `codex app-server` (both `OTEL_SERVICE_NAME`
constants in their respective `lib.rs`). `contracts/codex/hooks-and-otel.yaml`'s `resource_identity`
and `internal/codexadapter/otel.go`'s `OTLPResourceServiceNames` were updated to recognize all four;
the earlier `codex_cli_rs`-only match caused every real `codex exec` session to be quarantined as an
unrecognized OTLP resource.

2026-07-25 official-documentation re-check: the current Codex advanced-configuration reference
states that OTel is disabled by default, `[otel]` exports asynchronously and flushes on shutdown,
and prompt text stays redacted unless explicitly enabled. The documented event vocabulary includes
`codex.conversation_starts`, `codex.api_request`, `codex.sse_event`, `codex.user_prompt`,
`codex.tool_decision` and `codex.tool_result`. A live `codex-cli 0.145.0` capture established the
typed attributes used by the implementation: `codex.tool_result` carries `tool_name`,
`duration_ms` and boolean `success`; the `response.completed` SSE record carries model and
input/output token counts. Kansoku counts `tool_result` as the execution and leaves
`tool_decision` unmapped so one physical call is not counted twice. Retrieved 2026-07-25; relevant
local version `codex-cli 0.145.0`.

2026-07-25 component/cost re-check:

- Codex manual snapshot: <https://developers.openai.com/codex/codex-manual.md>
- ChatGPT/Codex plugin documentation: <https://developers.openai.com/plugins>
- OpenAI API pricing: <https://developers.openai.com/api/docs/pricing>
- Model references:
  <https://developers.openai.com/api/docs/models/gpt-5.6-sol>,
  <https://developers.openai.com/api/docs/models/gpt-5.6-terra>,
  <https://developers.openai.com/api/docs/models/gpt-5.6-luna>

Retrieved 2026-07-25; relevant local Codex version `0.145.0`. The manual still provides no stable
Codex skill/plugin activation event. The pricing page publishes per-million-token API rates for
the three observed model IDs, including separate input, cached-input and output rates and
short/long-context tiers. Kansoku therefore labels its result **API-equivalent estimate** and
versions the price catalog: OTel token metadata is not an OpenAI/ChatGPT invoice, subscription
entitlements and provider-specific routing may differ, and absent input/output/cache splits cannot
be silently priced as one token class.

2026-07-26 Session 13 App Server verification:

- Official Codex manual snapshot:
  <https://developers.openai.com/codex/codex-manual.md>
- Local schema command:
  `codex app-server generate-json-schema --experimental --out TARGET`
- Locally observed version: `codex-cli 0.145.0`.

The current manual describes App Server as an experimental local-development JSONL protocol,
requires one `initialize`/`initialized` handshake, and documents typed thread, turn, item, skill and
MCP methods. The exact local 0.145.0 generator was then used as the implementation authority for
`thread/started`, `turn/started`, `turn/completed`, `item/started`, `item/completed`,
`skills/changed`, `skills/list` and `mcpServerStatus/list`. This is version-pinned experimental
support, not an unversioned compatibility claim. Generated item schemas also prove that the same
frames can contain messages, reasoning, commands, paths, MCP arguments/results/errors and resource
URIs. The bridge therefore projects only typed lifecycle/identity/status fields and destroys those
content surfaces before its safe sink; an unknown method or schema becomes metadata-only rejection
and bridge-local degraded health. Retrieval evidence is official documentation plus a locally
generated schema, followed by sanitized fixture and cross-lane end-to-end tests.

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

2026-07-25 official-documentation re-check: Anthropic currently documents `service.name` values
`claude-code` (terminal) and `claude-code-desktop` (desktop Code tab), a default log export
interval of five seconds and a default metric interval of sixty seconds. Per-record
`event.name` values are short forms such as `user_prompt`, `api_request`, `api_error`,
`tool_decision`, `tool_result`, `plugin_installed`, `plugin_loaded` and `skill_activated`.
`user_prompt` supplies `prompt_length` while prompt content remains gated; `tool_result` supplies
`tool_name`, string-valued `success` and `duration_ms`; `api_request` supplies model,
input/output/cache token counts, duration and `cost_usd_micros`. Kansoku maps only those bounded,
non-content attributes and unconditionally drops bodies, prompt/response text, tool input/output
and raw API data. Retrieved 2026-07-25; relevant local Claude Code version `2.1.197`, while
documented availability remains version/feature dependent.

2026-07-25 component-lifecycle re-check:

- Plugin structure/reference: <https://code.claude.com/docs/en/plugins-reference>
- Plugin creation and MCP bundling: <https://code.claude.com/docs/en/plugins>
- MCP: <https://code.claude.com/docs/en/mcp>

The current monitoring reference explicitly documents `claude_code.skill_activated`,
`claude_code.plugin_installed` and `claude_code.plugin_loaded`, plus `skill.name`/`plugin.name`
attribution. It also warns that `OTEL_LOG_TOOL_DETAILS=1` exposes commands, paths, MCP names,
skill names and tool input. Kansoku keeps that detail gate off by default and only consumes the
dedicated bounded lifecycle attributes supported by the active adapter version. Retrieved
2026-07-25; local Claude Code remains `2.1.197`, so documentation support and locally proven
version support stay distinct.

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

## Session 03 OTLP and Go protocol implementation

- OTLP specification: <https://opentelemetry.io/docs/specs/otlp/>
- Language-independent protobuf definitions:
  <https://github.com/open-telemetry/opentelemetry-proto>
- Generated Go OTLP protobuf module: <https://pkg.go.dev/go.opentelemetry.io/proto/otlp>
- gRPC-Go module: <https://pkg.go.dev/google.golang.org/grpc>
- Go protobuf API: <https://pkg.go.dev/google.golang.org/protobuf/proto>
- Go module transparency metadata:
  <https://proxy.golang.org/go.opentelemetry.io/proto/otlp/@latest>,
  <https://proxy.golang.org/google.golang.org/grpc/@latest>,
  <https://proxy.golang.org/google.golang.org/protobuf/@latest>
- Retrieved: 2026-07-21.
- Relevant versions: OTLP specification `1.10.0`; generated OTLP Go module `v1.10.0`
  (origin commit `5abb227a3efbfea092a8db5b89a8a9e59117cee1`); gRPC-Go `v1.82.1`
  (origin commit `ebd8f06a09426fbece97157c95c3917abff28f4e`); Go protobuf `v1.36.11`
  (origin commit `96a179180f0ad6bba9b1e7b6e38d0affb0168e9a`). Exact module content hashes are
  in `go.sum`, the offline build set is in `vendor/modules.txt`, and the resolved inventory is in
  `reports/session-03-sbom.json`.

Design note: OTLP 1.10.0 marks trace, metric and log signals stable and defines the same protobuf
request/response schemas for unary gRPC and HTTP. Binary HTTP uses proto3 wire bytes,
`application/x-protobuf`, standard `/v1/traces`, `/v1/metrics` and `/v1/logs` paths, and a successful
Export response only after acceptance. Retryable HTTP failures are 429/502/503/504. The protocol
also requires receivers to support both no compression and gzip. Session 03 implements authenticated
loopback binary protobuf for all three signals over HTTP and gRPC, bounds messages at one MiB, and
returns acknowledgement only after a durable typed transaction. It deliberately rejects gzip under
the reviewed Session 02 compression policy and therefore remains an Experimental non-conformant
spike; ADR 0006 prevents a full-OTLP or adapter support claim. JSON, partial success, remote/TLS
deployment and exporter environment configuration are not implemented.

No OpenTelemetry GenAI semantic-convention names were adopted into the core. The fixture uses a
Kansoku-owned, versioned safe attribute namespace solely to exercise protocol transport and source-
independent normalization; real agent field mappings remain version-bounded adapter work.

## Session 04 PostgreSQL driver and data platform implementation

- pgx driver documentation: <https://pkg.go.dev/github.com/jackc/pgx/v5>
- pgx source repository: <https://github.com/jackc/pgx>
- PostgreSQL 18 `percentile_cont`/window function reference:
  <https://www.postgresql.org/docs/18/functions-aggregate.html>
- PostgreSQL 18 partitioning reference: <https://www.postgresql.org/docs/18/ddl-partitioning.html>
- Go module transparency metadata: <https://proxy.golang.org/github.com/jackc/pgx/v5/@latest>
- Retrieved: 2026-07-22.
- Relevant versions: `github.com/jackc/pgx/v5 v5.7.6` plus resolved transitives
  `github.com/jackc/pgpassfile v1.0.0`, `github.com/jackc/pgservicefile` (pseudo-versioned commit
  `5a60cdf6a761`), `github.com/jackc/puddle/v2 v2.2.2`, `golang.org/x/crypto v0.50.0` and
  `golang.org/x/sync v0.20.0`. Exact module content hashes are in `go.sum`, the offline build set is
  in `vendor/modules.txt`, and the resolved inventory independent of Session 03's is in
  `reports/session-04-sbom.json`.

Design note: Session 04 replaces the Session 03 `FileStore` durability spike with a real PostgreSQL
18 system of record, matching the engine ADR 0001 already selected. Facts are range-partitioned
monthly; rollups use PostgreSQL's exact `percentile_cont` and never average two already-computed
percentiles, per the vendored aggregate-function reference above. `contracts/data-platform/
schema.yaml.engine.image_digest` pins the same PostgreSQL image digest already used in
`deploy/compose.security-baseline.yaml`, so the ephemeral validator harness and the eventual
Session 09 runtime agree on exactly which image is authoritative. `pgx/v5` was chosen as the
first-party Go driver over `database/sql` + `lib/pq` for native PostgreSQL protocol support
(binary parameters, connection pooling via `puddle`) without an additional `database/sql` shim
layer; this is an implementation detail of ADR 0001's already-selected engine, not a re-litigation
of the engine choice itself.

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
Session 02 packages have no third-party Go dependency; Session 03 later added pinned protocol
modules to the shared workspace. All Go tests/vet/fuzz/benchmark runs use the pinned toolchain with
network disabled. The local Docker installation exposes neither Scout nor an
SBOM plugin, and no production Kansoku image exists, so `reports/session-02-sbom.json` is a source
inventory rather than a signed release SBOM. Production image scanning/provenance remains a blocking
Session 09/10 gate. Compose documentation supports the static service/network/secret fields, but a
successful `compose config` is not runtime reachability or soak evidence.

## Session 08 integrity scheduler interfaces

- PostgreSQL 18 advisory-lock functions:
  <https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADVISORY-LOCKS>
- pgx v5 `pgxpool` package:
  <https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool>
- Retrieved: 2026-07-23.
- Relevant versions: PostgreSQL 18; the repository remains pinned to
  `github.com/jackc/pgx/v5 v5.7.6` from Session 04.

Design note: `pg_try_advisory_lock(bigint)` is an exclusive session-level, non-blocking lock.
PostgreSQL releases session-level advisory locks when the session ends, including an ungraceful
disconnect. Kansoku therefore holds the lock on one explicitly acquired `pgxpool.Conn` for the
whole audit and releases that connection only after the run terminates; calling `Pool.Exec` for
lock/unlock would be incorrect because two calls may use different pooled sessions. No agent
interface changed in Session 08, so no Codex/Claude/Gemini/Cursor documentation was re-fetched.
Live-canary execution stayed simulated and did not exercise a provider CLI or credential path.

## Session 09 local runtime and operations

- Docker Compose services reference (ports, healthchecks, restart, read-only root filesystem,
  capability drops and immutable image references):
  <https://docs.docker.com/reference/compose-file/services/>
- Docker Compose secrets:
  <https://docs.docker.com/compose/how-tos/use-secrets/>
- PostgreSQL 18 backup and restore overview:
  <https://www.postgresql.org/docs/18/backup.html>
- PostgreSQL 18 `pg_dump`:
  <https://www.postgresql.org/docs/18/app-pgdump.html>
- PostgreSQL 18 `pg_restore`:
  <https://www.postgresql.org/docs/18/app-pgrestore.html>
- Go `net/http`, including `http.Server.Shutdown`:
  <https://pkg.go.dev/net/http>
- Retrieved: 2026-07-24.
- Relevant versions: Docker Compose specification current on the retrieval date; PostgreSQL 18;
  Go standard library for the repository's `go 1.26` module/toolchain baseline.

Design note: Compose short-syntax ports without a host IP bind to all interfaces, so the production
file names `127.0.0.1` for every published application port and publishes no database port.
Compose secrets are granted per service and mounted as files under `/run/secrets`; Session 09
therefore passes only secret file locators through environment/config, never secret values.
`read_only`, `cap_drop`, `security_opt`, healthchecks and restart policy are validated from the
fully rendered `docker compose config`, not inferred from prose. PostgreSQL documents SQL dumps,
filesystem backups and continuous archiving as distinct approaches; the local appliance uses
PostgreSQL 18 custom-format `pg_dump`/`pg_restore` for its native logical backup job, with checksum
manifest and an isolated restore verification rather than treating a successful dump command as
restore evidence. Go `http.Server.Shutdown` closes listeners, closes idle connections and waits for
active connections until the supplied context expires; the appliance therefore waits for Shutdown,
then drains bounded ingress lanes and attempts durable spool persistence before closing the shared
database pool. Session 09 changes no agent interface, so Codex/Claude/Gemini/Cursor documentation
was not re-fetched and no adapter support claim changes.

## 2026-07-26 lifecycle and telemetry re-audit

- Codex advanced configuration, observability and telemetry:
  <https://learn.chatgpt.com/docs/config-file/config-advanced>
- Codex hooks:
  <https://learn.chatgpt.com/docs/hooks>
- Codex App Server protocol:
  <https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md>
- Claude Code monitoring:
  <https://code.claude.com/docs/en/monitoring-usage>
- Claude Code skills:
  <https://code.claude.com/docs/en/skills>
- Claude Code hooks:
  <https://code.claude.com/docs/en/hooks>
- Retrieved: 2026-07-26.
- Locally observed versions: Codex CLI `0.145.0`; Claude Code `2.1.197`.

Design note: Codex's stable OTel event list covers conversations, API/SSE/websocket activity,
prompt length, tool decisions and tool results, but does not publish a native skill/plugin
activation or terminal component-success event. Current Codex hooks observe shell, unified exec,
`apply_patch`, MCP and most local function tools, while hosted tools and specialized opt-out paths
remain outside hook coverage. App Server provides typed `skills/list`, explicit `type: "skill"`
turn inputs and plugin-attributed command/MCP items; Kansoku treats a future bridge as a
version-pinned optional source, not as permission to parse content-rich raw rollout files.

Claude Code documents native `plugin_loaded` (once per enabled plugin at session start) and
`skill_activated` events. Third-party identity is redacted unless detailed tool telemetry is
enabled; enabling that setting also exposes content-bearing tool parameters/input and therefore
cannot be Kansoku's privacy-safe default. Claude's skill documentation says full skill content
loads on invocation and can persist across turns, but neither that persistence nor a successful
session proves a terminal skill outcome. Kansoku therefore records only identity-resolved native
loaded/invoked assertions and leaves universal component success unsupported.

## 2026-07-26 Sessions 12–20 planning re-check

- Codex App Server protocol:
  <https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md>
- Codex skills:
  <https://learn.chatgpt.com/docs/build-skills>
- Codex hooks:
  <https://learn.chatgpt.com/docs/hooks>
- Codex advanced configuration and OTel:
  <https://learn.chatgpt.com/docs/config-file/config-advanced>
- Claude Code monitoring:
  <https://code.claude.com/docs/en/monitoring-usage>
- Claude Code hooks:
  <https://code.claude.com/docs/en/hooks>
- Claude Code skills:
  <https://code.claude.com/docs/en/skills>
- Model Context Protocol lifecycle:
  <https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle>
- Model Context Protocol tools:
  <https://modelcontextprotocol.io/specification/2025-11-25/server/tools>
- Model Context Protocol progress and cancellation:
  <https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/progress>,
  <https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/cancellation>
- OpenTelemetry HTTP metrics semantic conventions:
  <https://opentelemetry.io/docs/specs/semconv/http/http-metrics/>
- OpenTelemetry process/runtime resource semantic conventions:
  <https://opentelemetry.io/docs/specs/semconv/resource/process/>
- Retrieved: 2026-07-26.
- Locally observed versions remain Codex CLI `0.145.0` and Claude Code `2.1.197`; documentation
  behavior beyond those local versions is not live evidence.

Design note: the Codex App Server README documents `skills/list`, skill configuration writes,
plugin inventory/read methods, `mcpServerStatus/list` and typed `mcpToolCall` items. It is a
main-branch local application protocol, not a stable OTel contract. Session 13 therefore treats it
as the first optional version-pinned implementation of a generic adapter-owned evidence bridge.
Content-bearing prompts/messages/reasoning, tool or MCP arguments/results/errors, resources and
paths remain prohibited before any queue, log or durable assertion.

The MCP specification separates initialization/version/capability negotiation, normal operation
and shutdown. `tools/list` is paginated and may emit list-changed notifications. `tools/call`
distinguishes JSON-RPC/protocol errors from tool execution errors reported with `isError=true`.
Progress, cancellation and timeouts are independent signals. Session 15 keeps configuration,
connection and call evidence separate and never interprets `isError=false` as success of the user's
task.

Claude Code documents `skill_activated`, `plugin_loaded` and MCP server connection events, but
third-party identity and detailed MCP/tool names can be gated with a setting that also increases
upstream exposure of arguments/input. Kansoku does not enable that setting by default. Redacted
identity remains redacted or unresolved rather than being guessed.

OpenTelemetry HTTP metrics use low-cardinality route templates for server duration dimensions.
Process resource attributes may include command lines, arguments, executable paths and owner data;
Session 17 explicitly excludes those surfaces from system self-observability.

## 2026-07-26 Session 12 implementation re-check

- OpenTelemetry OTLP exporter specification 1.11.0:
  <https://opentelemetry.io/docs/specs/otel/protocol/exporter/>
- OpenTelemetry Protocol specification 1.11.0:
  <https://opentelemetry.io/docs/specs/otlp/>
- PostgreSQL 18 query ordering:
  <https://www.postgresql.org/docs/18/queries-order.html>
- PostgreSQL 18 multicolumn indexes:
  <https://www.postgresql.org/docs/18/indexes-multicolumn.html>
- Retrieved: 2026-07-26.
- Local executable evidence: Codex CLI `0.145.0`, Claude Code `2.1.197`, Go `1.26.5`
  (`darwin/arm64`), Node `26.3.0`, npm `11.16.0`.

The OTLP 1.11.0 documents confirm protocol envelope names only; they do not prove that an unknown
payload's values are safe. Session 12 persists only fixed, reviewed protocol field names and leaves
shape `not_observed` when the privacy boundary cannot expose a value-free descriptor. PostgreSQL 18
documents the row-order and B-tree behavior used by `(last_seen_at DESC, incident_id DESC)` and
occurrence cursors. Live database plans and timings remain a separate runtime proof.

## 2026-07-26 Session 14 implementation verification

- Codex App Server protocol source generated by local Codex CLI `0.145.0`, especially
  `skills/list`, `SkillsListResponse`, `SkillMetadata`, `turn/started` and `item/started`.
- Codex skills documentation: <https://learn.chatgpt.com/docs/build-skills>
- Retrieved and live-checked: 2026-07-26.

The live executable refined the planning assumption without widening the accepted schema:
`skills/list` returned the enabled model-visible skill inventory; an explicit typed skill input was
an `item/started` `userMessage` content item while `turn/started.items` was empty. Notifications
carried top-level `emittedAtMs`. The version-pinned bridge accepts only reviewed identifiers,
timestamps and closed state fields. Skill descriptions, prompt/message content, filesystem paths,
tool/MCP arguments/results/errors and environment values are discarded before the canonical safe
record is constructed. This observed behavior is evidence for Codex CLI 0.145.0 only, not a claim
about future App Server versions.

## 2026-07-26 Session 15 implementation verification

- Model Context Protocol lifecycle 2025-11-25:
  <https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle>
- Model Context Protocol tools 2025-11-25:
  <https://modelcontextprotocol.io/specification/2025-11-25/server/tools>
- Codex App Server protocol source generated by local Codex CLI `0.145.0`, including typed
  `mcp_tool_call` items and approval handling.
- PostgreSQL 18 `pg_dump` snapshot option and synchronized snapshots:
  <https://www.postgresql.org/docs/18/app-pgdump.html>,
  <https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-SNAPSHOT-SYNCHRONIZATION>
- Retrieved and live-checked: 2026-07-26.
- Locally observed: Codex CLI `0.145.0`, model `gpt-5.6-luna` with medium reasoning; Claude Code
  `2.1.197`; fixture protocol `2025-11-25`.

The direct protocol canary negotiated `2025-11-25`, followed two `tools/list` pages, received
`notifications/tools/list_changed`, and distinguished `isError=false` from `isError=true`. In a
real ephemeral Codex run, 0.145.0 emitted actual MCP tool start/completed items and policy denial;
with approval bypass the success item completed and the error item failed. That version did not
follow the fixture's `nextCursor`, so the adapter must expose partial enumeration rather than claim
complete inventory. This is executable evidence for 0.145.0 only.

Claude Code 2.1.197 was not configured for detailed content-bearing telemetry. The generic mapper
therefore records exact connection identity only when a default-safe source supplies it; redacted
third-party identity remains `not_observed`. PostgreSQL's exported snapshot is used so the custom
format dump and its table-count/version manifest observe the same transaction snapshot during
concurrent ingestion.

## 2026-07-26 Session 16 implementation verification

- OpenAI plugin build documentation:
  <https://developers.openai.com/plugins/build/plugins>
- Codex App Server protocol:
  <https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md>
- Codex App Server JSON Schema generated from local Codex CLI `0.145.0`, including
  `plugin/read`, `PluginReadParams`, `PluginReadResponse`, `PluginDetail`, `PluginSummary`,
  `SkillSummary`, `PluginHookSummary`, `AppSummary` and MCP server names.
- Claude Code monitoring:
  <https://code.claude.com/docs/en/monitoring-usage>
- Claude Code plugins reference:
  <https://code.claude.com/docs/en/plugins-reference>
- Retrieved and live-checked: 2026-07-26.
- Locally observed versions: Codex CLI `0.145.0`; Claude Code `2.1.197`; Go `1.26.5`;
  Node `26.3.0`; npm `11.16.0`.

The current Codex protocol exposes `plugin/list`, `plugin/installed` and `plugin/read`.
`plugin/read` returned the Session 16 local canary as one plugin with local version `0.1.0`, one
enabled namespaced skill and one named MCP server. Kansoku uses only those bounded identity and
membership fields; marketplace/plugin paths, descriptions, prompts, URLs and content are discarded
before persistence. The App Server methods remain experimental and the bridge stays pinned to
0.145.0.

Claude documents `plugin_installed` and one `plugin_loaded` event per enabled plugin at session
start. The load event includes plugin identity, marketplace/version/scope and bounded bundle counts;
third-party identity is redacted unless detailed telemetry is explicitly enabled. Kansoku maps
installed and loaded independently and does not infer plugin success from either event or from a
child outcome.

## 2026-07-28 P0/P1 incident implementation re-check

- Codex App Server protocol:
  <https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md>
- Local generated JSON Schema command:
  `codex app-server generate-json-schema --experimental --out TARGET`
- Claude Code monitoring:
  <https://code.claude.com/docs/en/monitoring-usage>
- Claude Code plugins reference:
  <https://code.claude.com/docs/en/plugins-reference>
- PostgreSQL 18 database object-size functions:
  <https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADMIN-DBSIZE>
- PostgreSQL 18 cumulative statistics:
  <https://www.postgresql.org/docs/18/monitoring-stats.html>
- Retrieved and executable versions checked: 2026-07-28.
- Local versions: Codex CLI `0.145.0`; Claude Code `2.1.197`; PostgreSQL `18`; Go `1.26.5`.

The generated 0.145.0 App Server aggregate schema SHA-256 is
`6008369088efb49582c6d90299ffcb378cddddf22a14ac67faa0b81b2030f27c`; selected
JSON-RPC, `SkillsListResponse`, `ItemStartedNotification` and
`ThreadStatusChangedNotification` hashes are recorded in
`contracts/codex/generated/app-server-0.145.0-schema-manifest.json`. The bridge is exact-version
only and now demultiplexes concurrent request IDs; unrelated responses and documented service
notifications are not bridge schema failures.

Claude 2.1.197 documentation and the installed executable confirm the bounded
`skill_activated`/`invocation_trigger` and `plugin_loaded` metadata used by the mapper. Kansoku
keeps detailed tool-content, user-prompt and raw-API-body flags disabled. PostgreSQL size functions
support the advisory database/table/index observations; current WAL rollback headroom and current
temporary-file occupancy are not derivable from the application role and remain explicit
`not_observed` exclusions. The 25–30 GiB Docker free-space recommendation is a Kansoku rollback,
backup and WAL operating envelope, not a PostgreSQL hard limit.

## 2026-07-29 supervised bridge/runtime re-check

- Codex App Server protocol:
  <https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md>
- Local generated JSON Schema command:
  `codex app-server generate-json-schema --experimental --out TARGET`
- Claude Code CLI help and monitoring:
  <https://code.claude.com/docs/en/monitoring-usage>
- Retrieved/rechecked: 2026-07-29.
- Executable versions unchanged: Codex CLI `0.145.0`; Claude Code `2.1.197`; PostgreSQL `18`;
  Go `1.26.5`.

The local 0.145.0 generator was rerun twice. Individual files were stable:
`ClientRequest.json` SHA-256
`03e30c97136d6618273e3e9197d8621bad9ac6cfd733c0cfe09dc8754ee6ac5c` and
`v2/PluginReadResponse.json` SHA-256
`c2819939ba7a71a9deb3a1574b489124b0a20c7d2023f963ce38729300a20c45`.
The combined aggregate schema file changed byte order between runs, so its historical hash is not
used alone as a compatibility proof; selected generated-file hashes are authoritative in the
manifest and validator. The Kansoku route is a local authenticated transport around this reviewed
JSON-RPC demultiplexer and does not claim ordinary-CLI coverage. Per operator direction no further
Claude process was launched and no Claude configuration was changed during this phase.

## 2026-07-29 Codex plugin catalog inventory re-check

- Codex manual, plugin build and plugin use sections:
  <https://developers.openai.com/plugins/build/plugins> and
  <https://learn.chatgpt.com/docs/plugins>
- Retrieved through the official Codex manual helper: 2026-07-29.
- Locally observed Codex CLI version: `0.145.0`.

The current official material defines a plugin as an installable package that may bundle skills,
MCP and other capabilities; its minimal package shape includes
`.codex-plugin/plugin.json` and `skills/<skill>/SKILL.md`. It also says Codex CLI installs plugins
from a marketplace through `/plugins`, requires a new session for bundled skills/tools, and
separates installed from enabled state. The official material does **not** promise the internal
`CODEX_HOME/plugins/cache/<marketplace>/<plugin>/<version>` storage layout.

That cache layout was therefore treated as a version-observed 0.145.0 local source, not as a stable
public interface. The mounted personal Codex home contained 2 marketplace directories, 12 plugin
version directories and 67 immediate skill manifests at retrieval time, all within the declared
64/512/2,048 scanner bounds. Kansoku reads this source through `HostView` only, never writes agent
directories, never infers enabled from cache presence, and persists only identity metadata, HMAC
path pseudonyms, counts and fingerprints. An absent cache and an unreadable/ambiguous cache remain
distinct coverage states.

Kansoku's installation binding is an internal lineage decision rather than an upstream Codex
claim: the explicitly mounted home, its inventory snapshot and its ordinary-CLI rollout watcher use
one configured opaque ID. App Server evidence joins that population only when its producer supplies
the same ID. No official source is interpreted as permission to infer a logical installation from
the latest database row.

## 2026-07-29 browser verification re-check

- Chrome Headless mode:
  <https://developer.chrome.com/docs/chromium/headless/>
- Chrome DevTools Protocol:
  <https://chromedevtools.github.io/devtools-protocol/>
- DevTools Protocol Emulation domain:
  <https://chromedevtools.github.io/devtools-protocol/tot/Emulation/>
- DevTools Protocol Page domain:
  <https://chromedevtools.github.io/devtools-protocol/tot/Page/>
- WCAG 2.2:
  <https://www.w3.org/TR/WCAG22/>
- Retrieved/rechecked: 2026-07-29.
- Local executable versions: Google Chrome `150.0.7871.187`; Node `26.3.0`; npm `11.16.0`.

The repository browser verifier launches local Chrome headless with an ephemeral profile and uses
CDP only for navigation, viewport/media/scale emulation, DOM/accessibility assertions and runtime
error collection. It covers WCAG 2.2 text contrast, both half-width reflow and actual CDP page
scale `2.0`, reduced motion, keyboard-named controls and desktop/mobile overflow. CDP tip-of-tree
is explicitly not treated as a stable wire contract; the harness uses the local
browser-advertised protocol endpoint.

## 2026-07-30 defect-research interface re-check

- Codex App Server:
  <https://developers.openai.com/codex/app-server/>
- Codex skill authoring and invocation:
  <https://developers.openai.com/codex/skills/>
- Claude Code monitoring and usage:
  <https://code.claude.com/docs/en/monitoring-usage>
- Claude organization skill analytics:
  <https://platform.claude.com/docs/en/api/admin/analytics/skills>
- PostgreSQL 18 ordered-set aggregates:
  <https://www.postgresql.org/docs/18/functions-aggregate.html>
- Retrieved/rechecked: 2026-07-30.
- Locally observed versions: Codex CLI `0.145.0`; Claude Code `2.1.197`;
  PostgreSQL `18.4`; Google Chrome `150.0.7871.187`; Go `1.26.5`;
  Node `26.3.0`; npm `11.16.0`.

Current Codex documentation distinguishes inventory from invocation. `skills/list` and
`skills/changed` expose discovery and invalidation, while an App Server client can make one
explicit request observable by sending both a `$<skill-name>` text marker and the recommended
typed `skill` input item. The skill guide also documents implicit activation by the model. Neither
page defines an ordinary-CLI skill-activation lifecycle notification. Kansoku may therefore count
typed App Server requests exactly, but its ordinary-CLI rollout reconstruction remains
version-pinned evidence with explicit coverage and must not be presented as universal exact
activation telemetry. Current App Server plugin list/read methods are marked under development.

Current Claude Code monitoring documentation still defines `skill_activated` and one
`plugin_loaded` event per enabled plugin at session start. It additionally documents
`skill.name`, `plugin.name`, and `agent.name` attribution on token and cost counters, a
`success` field on `tool_result`, and `skill_name` inside Skill-tool parameters when detailed tool
telemetry is enabled. Detailed tool telemetry also exposes content-bearing fields, so it is not a
privacy-safe Kansoku default. These current pages may describe a newer executable than the locally
installed `2.1.197`; sanitized version-pinned fixtures are required before adapter changes.

The Claude organization analytics endpoint is a separate authenticated remote API. Its
`invocation_count`, when enabled, is a true per-use count that can be summed over a window, while
distinct session/conversation counts can be approximate in range mode. Kansoku does not currently
configure this remote source, and default-deny egress means it cannot be used implicitly as a
substitute for local telemetry.

PostgreSQL `percentile_cont` remains the exact ordered-set calculation used by Kansoku for
continuous percentiles over the selected non-null fact population. Formatting those results to two
decimal places is a presentation decision and must not change the stored or queried population.

## 2026-08-01 Claude Code skill telemetry re-check

- Monitoring and OpenTelemetry: <https://code.claude.com/docs/en/monitoring-usage>
- Skills: <https://code.claude.com/docs/en/skills>
- Hooks reference: <https://code.claude.com/docs/en/hooks>
- Retrieved/rechecked: 2026-08-01.
- Locally observed runtime: Claude Code `2.1.220`. Documentation behavior gates still run through
  `2.1.216`, so `2.1.220` is a locally observed runtime, not documented coverage, and neither alone
  is a support claim. `contracts/claude/manifest.yaml`, `contracts/claude/hooks-and-otel.yaml` and
  `contracts/capabilities.yaml` now record the observed range `2.1.197`–`2.1.220`; earlier dated
  sections of this file keep the version that was actually installed when they were written.

Observed on a live loopback OTLP capture of `skill_activated`, recorded from the operator's own
session and reduced to attribute names and non-content values only:

```text
event.name="skill_activated"        skill.name="sre-agent:verification-strategy"
invocation_trigger="claude-proactive"  skill.source="plugin"
plugin.name="sre-agent"             marketplace.name="yuzuru-engineering"
```

Findings that change the mapper's assumptions:

1. **`skill.name` is already owner-qualified.** It arrives as `<plugin>:<skill>` together with a
   separate `plugin.name`. Prepending the owner a second time yields identities such as
   `sre-agent:sre-agent:verification-strategy`, which no inventory row can match. User-scope skills
   with no owning plugin arrive bare.
2. **`skill.source` is Claude's vocabulary, not Kansoku's.** Observed `skill.source="plugin"`,
   `plugin.scope="user-local"`, `enabled_via="user-install"`. None is a member of
   `system|user|repository|admin|marketplace|plugin_cache|transient_session`. Live inventory stores
   plugin-bundled Claude skills at `plugin_cache`. Using the raw value as a resolution filter reduces
   every candidate set to zero.
3. **`marketplace.name` is emitted** on `skill_activated` and `plugin_loaded` and is currently
   dropped. It is the exact disambiguator the resolver approximates by splitting the owner declared
   name on `@`.
4. **`hook_registered` and `assistant_response` are emitted but undeclared**, so both quarantine on
   every session start. Both are metadata-only; `assistant_response`'s response text stays outside
   the allowlist.
5. **`skill.name="third-party"` / `plugin.name="third-party"` is a sentinel** on `api_request` and
   on `claude_code.cost.usage` / `token.usage`. Per-skill cost and token attribution is therefore not
   achievable for third-party skills.
6. **Built-in skills ship inside the compiled executable.** `find` over the installed
   `@anthropic-ai/claude-code` package returns no `SKILL.md`; the built-in set is compiled into the
   platform binary. No filesystem scan can inventory them.
7. **No exposure surface exists.** Neither the monitoring reference nor the skills reference
   documents an event or snapshot reporting the model-visible skill set, and the SessionStart hook
   payload carries only `session_id`, `transcript_path`, `cwd`, `hook_event_name` and `source`.
   Claude's exposed plane is therefore declared `unsupported` rather than inferred.

No agent configuration was written and no skill root was modified during this re-check; all
observation was read-only against already-flowing loopback telemetry and read-only binds.

## Source maintenance policy

For every supported agent release:

1. record executable version, config source and schema fingerprint;
2. replay pinned sanitized fixtures;
3. run passive health checks daily;
4. run opt-in live canaries on a bounded schedule;
5. review official changelog/docs when a version or fingerprint changes;
6. never auto-adapt a parser based only on prose or an untrusted payload;
7. mark affected intervals incomplete until reconciliation succeeds.
