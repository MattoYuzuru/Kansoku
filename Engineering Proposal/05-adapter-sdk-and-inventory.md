# Session 05 — Adapter SDK and inventory

## Purpose

Make “supports future agents” a testable engineering property. A new adapter must declare what it
can observe and plug into the same inventory/event/health contracts without editing core enums,
database branches or dashboard routing for an agent name.

## Capability model

Adapters declare independent capabilities such as:

- agent and surface discovery;
- installed/enabled component inventory;
- session/turn/prompt metadata;
- token/model/cost metadata;
- skill explicit/implicit activation;
- plugin and custom-command usage;
- MCP server/tool lifecycle;
- built-in tool calls and approvals;
- subagents, compaction and outcomes;
- historical import, live stream, configuration install and live canary.

Each capability has `unsupported`, `available`, `configured`, `healthy`, `degraded` states and an
evidence tier. UI features bind to capabilities, never agent brands.

## Adapter forms considered

1. **Built into Go core:** simplest and safest for first-party adapters; requires Kansoku release.
2. **External process over versioned JSONL/gRPC:** language-neutral and crash-isolated; preferred
   third-party path after MVP.
3. **WASI/Wasm plugin:** strong sandbox potential, but filesystem/process discovery and mature SDK
   ergonomics require investigation.
4. **Container-per-adapter:** isolation but too heavy and awkward for host agent files.

Proposed sequence: built-in adapters first, external process protocol second, evaluate Wasm after
real third-party demand.

## Inventory graph

Inventory is not a list of `SKILL.md` files. It models:

```text
agent installation -> surface -> enabled plugin -> bundled skill/MCP/hook/command
                                      \-> cached versions (not necessarily enabled)
standalone skill -> source scope -> shadow/collision relationship
MCP config -> server instance -> advertised tools
```

Paths are pseudonymized, while users can assign local display aliases. Sources include system,
user, repository, admin, marketplace, plugin cache and transient session components.

## Version and schema drift

Every adapter declares supported agent version ranges, source schemas, discovery probes, fixture
coverage and a fingerprint strategy. Unknown versions may continue in best-effort mode only with a
visible degraded state; parsed unknown fields are retained only after sanitization.

## Setup experience

`kansoku doctor` discovers agents and shows a capability matrix. `kansoku configure <agent>`
generates an exact patch/backup/rollback plan. `kansoku adapter verify` runs fixtures, passive probes
and optional live canary. No automatic config mutation occurs during normal collection.

## Third-party adapter acceptance

- versioned manifest and protocol;
- declared file/network/process permissions;
- prohibited-data tests;
- deterministic fixtures and contract tests;
- bounded output, timeouts and crash isolation;
- no direct database credentials;
- signed distribution considered later; unsigned adapters clearly marked.

## Deliverables

- Adapter capability manifest and lifecycle API.
- External dummy adapter proving agent independence.
- Inventory graph and collision semantics.
- Discovery/configuration preview CLI.
- Version/schema fingerprint and fixture registry.
- Adapter authoring guide and conformance suite.

## Exit gate

A dummy future agent with different config paths, event names and component vocabulary can be
discovered, normalized, health-checked and displayed through the public adapter protocol without a
core schema migration or agent-name conditional outside the adapter registry.

