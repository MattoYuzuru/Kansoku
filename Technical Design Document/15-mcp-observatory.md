# TDD 15 — MCP observatory

## Architecture

Extend the existing inventory graph, `mcp_connections` and `tool_calls` rather than creating a
parallel analytics store. Introduce typed inventory/capability snapshots and closed connection/call
state assertions. Every fact carries source lineage, adapter/schema version, evidence tier and
idempotency.

Core consumes generic MCP assertions; adapter packages map Codex App Server, Claude OTel/hooks and
future agents. There is no agent-name branch in data-platform queries or UI.

## Contracts

- `contracts/mcp/inventory-and-capabilities.yaml`;
- `contracts/mcp/connection-lifecycle.yaml`;
- `contracts/mcp/call-lifecycle.yaml`;
- `contracts/mcp/metrics-and-privacy.yaml`;
- data-platform schema/query/retention additions;
- adapter mapping additions for Codex and Claude;
- dashboard routes for server/tool profiles;
- integrity audit/fault/canary additions.

All state, error and result enums are closed. Unknown values quarantine metadata rather than
entering a free-text state column.

## Durable inventory

Server instance facts:

- opaque server instance ID and agent installation;
- scope and enabled/configured state;
- transport enum;
- local/remote classification without URL;
- plugin owner relation;
- safe configuration fingerprint;
- observed protocol/server version;
- capability booleans/subcapabilities;
- inventory snapshot and enumeration completeness;
- first/last observed and source lineage.

Advertised primitive facts:

- kind `tool|resource|resource_template|prompt`;
- opaque identity and approved display alias;
- server/version relation;
- structural schema fingerprint;
- input/output schema presence;
- description/schema byte counts;
- annotations stored as untrusted claims;
- list page/revision and first/last advertised interval.

Do not persist full descriptions, schemas, URLs, commands, arguments, environment, resource URIs or
auth values.

## Connection lifecycle

Closed states:

```text
configured, connecting, connected, failed, disconnected, timed_out, unknown
```

Assertions store attempt/correlation ID, start/terminal timestamps, duration, transport, negotiated
protocol/capability fingerprint and safe failure class. Uptime is the union of intervals bounded by
real connected/disconnected observations. It cannot start at the selected range boundary or extend
past the last observation without explicit heartbeat semantics.

Connect latency distributions use raw attempts. Current state uses the latest compatible assertion
with freshness metadata.

## Call lifecycle

Call state:

```text
decided, denied, started, progressing,
completed, execution_error, protocol_error,
cancelled, timed_out, transport_lost, incomplete
```

`completed` means a terminal result with protocol-level `isError=false`. `execution_error` maps
`isError=true`; JSON-RPC request/response failures are `protocol_error`. None means user-task
success.

Call facts may retain:

- opaque call/server/tool/session/installation IDs;
- timestamps and duration;
- source sequence/correlation/idempotency;
- terminal class and safe error category/code;
- approval requested/decision/source;
- request/result byte counts from a trusted boundary;
- result item counts and content-type categories;
- progress count and last-progress time;
- retry relation only with native causal evidence;
- adapter/schema/tool inventory versions.

Arguments, result values, error messages, response text and resource content are prohibited.

## Adapter mappings

### Codex

The Session 13 bridge uses `mcpServerStatus/list` for configured servers, tools, auth state and safe
server info with source pagination; typed `mcpToolCall` gives server/tool/status/plugin identity.
The bridge drops arguments, result and raw error before producing assertions.

### Claude Code

`mcp_server_connection` maps status, transport, scope, duration and safe error code. Generic tool
events may prove an MCP call without exact third-party identity when detail is redacted. Exact
identity is recorded only when the active version/default-safe source supplies it. Kansoku does not
enable detailed telemetry automatically.

### Config inventory

Config proves only configured state, scope and transport class. It cannot prove advertised,
connected or called.

## Reconciliation

- inventory identities reconcile by installation, scoped native identity and versioned fingerprint;
- same name/different server or fingerprint remains distinct/collision-related;
- one call start has at most one logical terminal, with multiple evidence lanes allowed;
- contradictory terminals open an incident;
- missing terminal after the source-specific deadline becomes incomplete, not failure;
- configured inventory remains visible during runtime source loss while call/connection
  completeness degrades;
- source pagination must complete before an advertised population can be exact.

## APIs and UI

- `/api/v1/components/mcp` list with configured/connected/called state;
- `/api/v1/components/mcp/{server_id}` profile;
- `/api/v1/components/mcp/{server_id}/tools`;
- `/api/v1/components/mcp/{server_id}/tools/{tool_id}` profile;
- range/filter/pagination endpoints for connections and calls.

Server and tool profiles expose support/completeness per inventory, connection and call contour.
Percentages always expose populations. “Unused” is labeled “no observed demand” and requires a
complete exposure window.

## Metrics

- server/tool inventory and enumeration completeness;
- connected/current/failed/reconnect counts;
- observable uptime;
- connect p50/p95/p99;
- calls/unique sessions/last used/active days;
- terminal outcome counts and ratios;
- call p50/p95/p99;
- timeout/cancel/deny/protocol/execution error rates;
- progress stalls;
- request/result byte distributions;
- approval decision breakdown;
- schema/list churn and collisions;
- orphan/incomplete calls.

Token/cost attribution remains unsupported unless a native causal ID joins the MCP call to a model
operation and a registered formula defines the population.

## Canary fixture and live run

Create a namespaced local no-op MCP server with:

- paginated `tools/list`;
- `listChanged`;
- `nothing.success`;
- `nothing.error` returning deterministic `isError=true`;
- bounded delay/cancellation behavior;
- no network egress and no user-repository access.

Exercise connection, disconnect/reconnect, success, execution error, protocol error, timeout,
cancellation and policy denial. Pair it with the Session 14 no-op skill and later Session 16 plugin
without treating their temporal proximity as ownership.

Use `gpt-5.6-luna` with medium reasoning for the bounded implementation canary when locally
available, and record exact model/agent/version/source evidence. Unavailability is an explicit test
result.

## Privacy and security tests

Inject secret-like tool arguments, results, error messages, URL, command, environment, auth header
and resource URI. Scan database, logs, spool, diagnostics, incident bundle, API, export, backup and
browser network responses. Only allowed byte counts/categories/fingerprints may survive.

Server annotations are displayed as untrusted claims. Auth status is categorical; tokens and
headers are never read into the model.

## Audit

Daily checks cover inventory/connection freshness, incomplete pagination, orphan calls, missing
terminals, connection-state contradictions, schema/list drift, bridge source health, canary result,
privacy canary and formula reconciliation.

## Exit gate

The proposal's complete canary chain passes in the rebuilt production image. Database queries prove
inventory/connection/call counts and denominators exactly; replay is idempotent; breaking each
source creates only its capability-specific degradation; and Codex/Claude evidence shares the same
generic model.
