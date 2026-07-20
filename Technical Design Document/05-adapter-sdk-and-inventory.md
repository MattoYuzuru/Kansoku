# TDD 05 — Adapter SDK and inventory graph

## Adapter manifest

```yaml
api_version: kansoku.adapter/v1
id: example-agent
version: 0.1.0
execution: builtin
agent_detection:
  executables: [example]
  state_roots: []
capabilities:
  inventory.components: supported
  activity.sessions: supported
  activity.prompt_metadata: partial
  components.skill_invocation: unsupported
sources:
  - id: hooks
    kind: hook
    schemas: [example.hook/v2]
permissions:
  filesystem_read: []
  network: loopback_only
health_checks: [config, fixture_replay, watermark]
```

Manifests are data, validated against JSON Schema and stored with releases. Capabilities use stable
core IDs; agent-specific details live under adapter namespaces.

## Built-in interface

```go
type Adapter interface {
    Manifest() Manifest
    Discover(ctx context.Context, host HostView) ([]InstallationCandidate, error)
    Inventory(ctx context.Context, target Installation) (InventorySnapshot, error)
    PlanConfiguration(ctx context.Context, target Installation) (ChangePlan, error)
    SourceSchemas() []SourceSchema
    Normalize(ctx context.Context, source SafeSourceRecord) ([]CanonicalEvent, error)
    Reconcile(ctx context.Context, scope ReconcileScope) ReconcileResult
    Audit(ctx context.Context, target Installation, mode AuditMode) []CheckResult
}
```

No adapter receives database credentials. `HostView` exposes permission-checked reads/exec probes
and pseudonymization helpers.

## External adapter protocol

After built-ins stabilize, support a supervised subprocess:

- line-delimited framed JSON or gRPC over Unix socket;
- handshake negotiates API version/capabilities/max frame;
- core supplies a capability-scoped request; adapter returns sanitized records/results;
- per-call timeout, output byte limit, crash restart budget and health state;
- environment allowlist and least-privilege filesystem grants;
- signed packages later; unsigned adapters labeled and disabled by default.

Wasm remains an ADR candidate once real permission needs are known.

## Inventory snapshot

Snapshots are immutable observations with source/time/fingerprint. Reconciliation derives current
state. Entity graph nodes:

- device → agent installation → surface/version;
- plugin package/version/source → enabled installation;
- skill identity/version/scope/path pseudonym;
- MCP server instance/transport → advertised tools;
- hook/custom command/subagent definitions;
- cache artifacts separated from active configuration.

Edges: `bundles`, `provides`, `configured_in`, `enabled_for`, `shadows`, `collides_with`, `depends_on`,
`observed_using`. Same declared name never forces identity merge.

## Discovery safety

- resolve state roots from documented env/config before defaults;
- never scan the entire home directory speculatively;
- do not follow symlinks outside allowed roots without explicit target resolution;
- parse manifests with limits and no code execution;
- command probes are credential-free version/help/status reads;
- cache inventory remains separately labeled.

## Configuration plans

`ChangePlan` contains exact target, precondition hash, before/after sanitized diff, backup path,
commands, privacy disclosure and rollback. Apply requires confirmation and rechecks precondition to
avoid overwriting concurrent edits. Normal collector operation never applies plans.

## Compatibility registry

Per agent version/capability store supported source fingerprints, fixtures, last passive/live audit,
known gaps and setup recipe version. Unknown agent versions default to degraded even if parsing
appears to work.

## Conformance suite

Adapter authors run:

- manifest/schema validation;
- deterministic discovery/inventory fixtures for macOS/Linux/Windows layouts;
- normalization golden tests;
- prohibited-content canaries;
- unknown-version/schema behavior;
- idempotent replay/reconciliation;
- permission and output-bound tests;
- dummy live canary contract where available.

The repository ships a fake agent adapter whose vocabulary and state layout differ from all built-ins.

## Exit gate

The fake external adapter passes conformance and appears through existing inventory/health APIs and
UI capability routing; core contains no new agent-name branch; config plans are reversible.

