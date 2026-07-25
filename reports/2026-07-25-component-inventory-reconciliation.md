# Component inventory and lifecycle reconciliation

Date: 2026-07-25

## Outcome

The production appliance now scans explicitly mounted Codex roots read-only, persists idempotent
inventory snapshots/current state, exposes current components through the API and separates
installed/enabled state from activation evidence in the Overview funnel.

## Live evidence

- `inventory_collection_status`: target `codex-local` complete; 17 nodes, 14 edges.
- Current components: 14 skills installed and enabled; two plugins installed and disabled; no
  configured MCP component.
- Funnel for 2026-07-25: all components installed 16/16, enabled 14/16; later stages have zero
  observations and state `not_observed`.
- Low-effort no-op canary window `21:26:48Z–21:27:13Z`: one session start, one prompt metadata
  event, 22 model requests, five model responses and five tool calls were ingested. No component
  lifecycle event was emitted and model events carried no `component_id`. The provider stream
  disconnected after five retries, so the requested terminal canary response was not produced.

This proves the ingestion path was active during the test and also proves that the tested Codex
surface did not export native skill activation evidence. Kansoku therefore does not synthesize
`executed` or `opportunity detected`.

## Privacy and operations

- Agent roots are mounted read-only; missing variables fall back to an empty repository directory.
- Persisted inventory excludes manifest bodies, MCP commands/arguments, environment values and
  credentials. Paths are HMAC pseudonyms.
- Snapshot and component-installation identities are deterministic; replay does not inflate
  current state.
- Inventory tables are covered by backup, retention and validation paths.

## Residual risks

- Codex's current documented OTel surface cannot populate skill/plugin activation or opportunity
  stages. A future versioned upstream event can close this without changing the core funnel.
- Claude's newly documented lifecycle events were not exercised live in this pass because the
  local Claude version is older than the current documentation behavior.
- The no-op plugin and MCP fixture exist outside the Kansoku repository but were not installed or
  enabled: doing so is an agent-configuration write and still requires preview plus explicit user
  confirmation.
- The canary provider stream failed; DB evidence confirms telemetry ingress but not a successful
  end-user canary completion.
