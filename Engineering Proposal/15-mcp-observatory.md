# Session 15 — MCP observatory

## Status

Approved for planning on 2026-07-26. Implementation has not started.

## Purpose

Implement precise MCP inventory, connection and call observability down to a server and tool
profile. Help users remove unused servers, identify slow or failing tools and distinguish
configuration, protocol and execution failures without storing tool arguments or results.

## Conceptual model

MCP evidence is split into three independent contours:

1. **Configuration/inventory:** configured instances, scope, transport and advertised primitives.
2. **Connection/protocol:** initialization, negotiated version/capabilities and state transitions.
3. **Call lifecycle:** decision, start, progress, terminal result, cancellation and timeout.

Configured does not mean connected; connected does not mean exposed; exposed does not mean invoked.
The word endpoint is avoided for stdio servers, which have no network endpoint. The canonical
identity is a server instance plus transport, installation and safe configuration fingerprint.

## Metrics

Inventory and capability:

- configured/enabled server instances and plugin ownership;
- transport class without raw URL, command, arguments or environment;
- negotiated protocol and safe server implementation version;
- advertised tools/resources/prompts and pagination completeness;
- list errors, list-changed events and inventory churn;
- structural tool schema fingerprint and description/schema byte counts;
- server-provided annotations, clearly labeled as untrusted claims.

Connections:

- attempts, connected/failed/disconnected transitions and reconnects;
- handshake/connect p50/p95/p99;
- version mismatch, capability negotiation, auth, transport, process-exit and timeout classes;
- observable uptime with explicit observed seconds and exclusions;
- freshness and in-flight state.

Calls:

- calls, unique sessions, last used and active days by server/tool/version;
- completed with `isError=false`, tool execution error, JSON-RPC/protocol error, cancellation,
  timeout, policy denial, transport loss and incomplete terminal state;
- p50/p95/p99 duration, progress/stall and cancellation latency where observed;
- approvals and decision source;
- retries only with causal correlation;
- request/result byte counts and result-type counts, never content;
- output-schema validation only when the client or bridge supplies explicit evidence.

Token and cost attribution requires a native causal relation. Temporal proximity to the next model
response is insufficient.

## Profiles

The server profile combines safe identity, configuration, protocol capability, tool/resource/prompt
inventory, connection timeline, call reliability/latency, approval/auth state, plugin ownership,
drift and incidents.

The tool profile shows its parent server/version, safe structural metadata, exposure/enumeration
history, calls and sessions, terminal outcome taxonomy, latency/bytes/progress/retries, approval
policy and fingerprint changes.

## Evidence sources

- Codex App Server bridge: `mcpServerStatus/list` and typed `mcpToolCall` metadata;
- Claude Code native OTel connection/tool events with default-safe detail settings;
- bounded config inventory;
- optional native hooks where a versioned contract proves MCP identity;
- a direct protocol observer/proxy only in a later separately threat-modeled session because it is
  invasive.

Arguments, results, error messages, URLs, commands, resource URIs, OAuth material and environment
values are discarded before durable or diagnostic handling.

## Anti-metrics

Zero calls means only “no observed demand in a complete exposure window”. It does not prove that a
tool is useless or badly described. `isError=false` proves protocol-level execution success, not
completion of the user's task. Uptime is never extrapolated outside real state observations.
Annotations are not security facts.

## Deliverables

- typed MCP inventory/connection/call contracts and state enums;
- generic adapter evidence mappings for Codex and Claude;
- server/tool durable facts, relations, formulas and audit checks;
- MCP list, server profile and tool profile UI;
- privacy-safe no-op MCP canary with deterministic success/error/timeout/cancel/deny scenarios;
- incident links for drift, connection failure, orphan calls and incomplete terminals.

## Exit gate

A two-tool no-op MCP advertises through paginated `tools/list`, changes its list, connects,
disconnects and reconnects, then produces one deterministic success and one `isError` result plus
bounded timeout/cancel/deny cases. Inventory reconciles exactly, starts and terminals pair 1:1,
percentile and uptime denominators are auditable, Codex and Claude share the same core model, and
secret-like arguments/results/errors/resource identifiers are absent from every sink.
