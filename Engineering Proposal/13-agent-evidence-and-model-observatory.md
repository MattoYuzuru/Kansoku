# Session 13 — Agent evidence bridge and model observatory

## Status

Implemented and live-verified on 2026-07-26. The reconciliation evidence is recorded in
`reports/session-13-reconciliation.md`.

## Purpose

Give every local agent installation a human-readable, evidence-backed profile and establish a
generic bridge pattern for richer native interfaces. This session is the attribution foundation
for later skill, MCP and plugin analytics, but it must itself deliver useful per-agent and per-model
UI rather than invisible plumbing.

## Confirmed baseline

`ain_*` identifiers are privacy-safe installation keys, but the UI currently presents them as the
primary name. Model facts exist and can show request/token totals while agent detail pages remain
mostly aggregate or unsupported. Events carry installation lineage, but session/model dimensions
do not yet expose a universally reconciled installation relationship suitable for every drill-down.

Codex stable OTel covers conversations, model/API activity, prompt length and tool results but does
not provide exact skill/plugin activation. Codex App Server exposes richer typed skill, plugin and
MCP state. Claude Code exposes richer native OTel lifecycle events. These are different evidence
lanes, not reasons for brand branches in core.

## Product decisions

1. `ain_*` remains the opaque durable key and secondary diagnostic label. The primary display name
   is derived from adapter identity, surface and user alias, never guessed from a model name.
2. Native rich interfaces implement one shared `EvidenceBridge` contract. Codex App Server is the
   first optional, version-pinned bridge, not a special core subsystem.
3. Every bridge declares capabilities, source schema, privacy projection, version bounds,
   reconnect/checkpoint behavior and independent health.
4. Identical facts from OTel, hooks and bridges reconcile into one logical assertion with multiple
   evidence rows. They do not become duplicate requests, calls or component activations.
5. Missing bridge evidence degrades only bridge-owned fields. OTel-backed model usage remains
   usable.

## Extensible evidence-bridge pattern

A bridge is an adapter-owned, core-supervised source that:

- negotiates a versioned source protocol;
- emits only canonical safe assertions or closed safe-source records;
- declares stable source identity and idempotency keys;
- never receives database credentials;
- is bounded by frame size, timeout, restart and reconnect budgets;
- drops content-bearing fields before logging, queueing or persistence;
- exposes health, watermark and compatibility state independently;
- can be implemented for Codex, Claude Code, Gemini CLI or a future agent without changing the
  canonical core schema.

Built-in bridges are allowed first. A later external-process bridge may use the existing Session 05
supervised adapter direction rather than inventing another extension mechanism.

## Agent and model profiles

The agent profile shows:

- adapter/provider, surface, observed agent and adapter versions;
- display alias plus secondary opaque installation ID;
- source/capability support and freshness matrix;
- sessions, prompts and activity timeline;
- requests, input/output/cache tokens and cost by model;
- duration/TTFT/error/retry/fallback where the source documents them;
- component, tool and MCP summaries;
- affected incidents and incomplete intervals;
- per-agent × per-model table and version markers.

Cost remains a versioned API-equivalent estimate, never a billing claim. A row is priced only when
token categories and a matching price snapshot exist; exclusions are visible.

## Alternatives rejected

- **Infer provider from model:** models may be proxied or shared across surfaces and do not identify
  the local agent installation.
- **Put Codex App Server types in core:** creates an architecture that cannot absorb Claude,
  Gemini or a third-party agent cleanly.
- **Count every lane separately:** produces duplicate sessions, model requests and component calls.
- **Hide opaque IDs completely:** makes diagnostics and incident correlation harder; they remain
  available as secondary safe identifiers.

## Deliverables

- adapter-owned generic evidence-bridge interface and conformance fixture;
- optional version-pinned Codex App Server bridge using allowlisted typed fields only;
- expanded Claude evidence mapping where locally version-verified;
- installation/session/model dimensional reconciliation;
- agent list and detail UX with per-model aggregation and source support matrix;
- bridge health, drift, replay and deduplication audit checks;
- bounded live canaries for two Codex models and one Claude source when locally available.

## Exit gate

Two concurrent Codex sessions using different models appear under one correctly named Codex
installation and separate model rows; a Claude installation coexists without core brand branching.
Bridge and OTel duplicates reconcile to one logical fact. Removing the bridge degrades only its
capabilities. All content-rich App Server fields are absent from durable and diagnostic sinks, and
the adapter conformance fixture proves a differently shaped future bridge can participate without a
core schema or routing change.

## Implemented outcome

The accepted design is implemented by the generic adapter-SDK `EvidenceBridge`, a deliberately
non-agent-shaped fake bridge, and the version-pinned Codex App Server 0.145.0 bridge. PostgreSQL
migration 0008 adds fresh exact installation attribution without rewriting ambiguous history.
Agent list/detail APIs and UI now expose identity, activity, per-model usage, exact populations and
independent source health. Cross-lane PostgreSQL replay, source-loss, unknown-schema, ten-sink
privacy, production restart, browser and repeated restore verification gates pass.
