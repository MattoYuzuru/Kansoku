# Session 13 reconciliation — Agent evidence bridge and model observatory

Date: 2026-07-26

Status: **live exit gate green**

## Acceptance result

Session 13 adds one generic adapter-owned evidence path rather than a Codex subsystem. The
`EvidenceBridge` contract declares versions, capabilities, safe fields, prohibited surfaces,
permissions, bounds, checkpoint strategy and independent lifecycle health. The
`constellation-pulse` fake bridge proves that a differently shaped protocol reaches the canonical
sink without agent-name routing changes. Codex App Server support is optional and pinned to the
locally generated 0.145.0 schema.

Migration 0008 adds installation profiles plus source/session attribution and fresh exact
installation columns on model operations and tool calls. It deliberately does not rewrite
historical telemetry. Agent list/detail now shows display identity, the secondary opaque
installation key, activity, exact per-model populations, source/evidence health and exclusions.

## Cross-lane fact and evidence proof

The real PostgreSQL runtime test sends the same logical `prompt.submitted` assertion through a
Codex App Server bridge and OTel:

```text
canonical facts = 1
evidence rows = 2
distinct source lanes = 2
```

The test initially exposed a real durability defect: the second lane conflicted on the canonical
event primary key and was spooled. `InsertFact` now treats every event-row conflict as an existing
canonical fact, then independently inserts the evidence row. Replaying the same evidence still
increments only its `replay_count`. The FileStore and PostgreSQL semantics now match.

Removing or degrading the bridge affects only its declared bridge-owned capabilities. The
App Server unknown-schema test records structural rejection metadata and bridge-local degraded
health while the OTel lane and its historical facts remain intact.

## Live installation and model reconciliation

The production database contains one Codex and one Claude installation in the shared canonical
schema. No core data-platform, runtime query or dashboard branch selects behavior by those names.
The Codex installation profile is `codex · cli`; its `ain_*` value remains only a secondary
diagnostic key.

Within the verified production range, the profile API returns three separate response rows under
that one Codex installation:

```text
gpt-5.6-sol    responses=1411
gpt-5.6-terra  responses=51
gpt-5.6-luna   responses=11
exact attribution population=1533/1533; exclusions=0
```

Two bounded concurrent `codex exec --ephemeral` canaries selected Sol and Terra explicitly and
both exited successfully. Their source emitted session/prompt and model-request evidence; it did
not emit a terminal model-response record before the ephemeral processes closed. The separate
Sol/Terra response rows above are therefore live durable observations already present under the
same installation, not a claim that those two ephemeral response terminals were observed.

The production agent API returned HTTP 200 with independently reconciled `hook_http` and
`otlp_log` source rows. A query-plan check over the current monthly partitions completed the
agent/model grouping in 4.533 ms, below the registered 500 ms profile budget.

## Privacy proof

Content-bearing App Server fixtures include prompt/message text, reasoning-shaped content, an
unredacted path, MCP arguments/result/error fields and a secret-format token. Projection keeps only
closed lifecycle, pseudonymous correlation, identity and status fields.

Both accepted bridge records and metadata-only bridge rejections were serialized through the
closed ten-sink set:

```text
accepted artifacts scanned = 10
rejection artifacts scanned = 10
content canary matches = 0
secret-format matches = 0
```

Unknown methods and incompatible/oversize schemas do not fall back to transcript parsing.

## Production, browser and restore proof

Production image `sha256:76e7189334d43ef35f36877d0a316b70500dee154f6e7c25f072f410eed80d74`
started at `2026-07-26T14:17:49Z`, applied migration 0008 and remained healthy after forced
replacement. Headless Chrome 152 rendered the real agent detail route with identity KPIs,
Sol/Terra/Luna rows, exact population/exclusion text and the independent source matrix.

Native backup `backup_dcb84a68bd6fcf09f11038b63e769da8` captured:

```text
schema_migrations=8
events=4780
event_evidence=4780
model_operations=1528
agent_installation_profiles=1
source_installation_attributions=1
session_installation_attributions=3
```

Two independent `restore-verify` runs returned `status=pass` with exact table counts and removed
their temporary databases.

## Verification

```text
python3 scripts/validate_adapter_sdk.py
python3 scripts/validate_codex.py
python3 scripts/validate_data_platform.py
python3 scripts/validate_runtime.py
python3 scripts/validate_privacy.py
python3 -m unittest discover -s tests -p 'test_*.py'  # 159 pass
go test ./internal/adaptersdk/... ./internal/codexadapter/... \
  ./internal/observability/... ./internal/dataplatform/... ./internal/runtime/...
npm run typecheck
```

All commands pass, including the PostgreSQL-tagged runtime suite, native backup/restore tests and
the production browser check.

## Residual risks and unsupported states

- App Server is experimental and supported only for the reviewed local 0.145.0 schema subset.
  Reconnect/replay is not claimed where the protocol supplies no replay cursor.
- Claude Code had no newly observed rich-interface version that justified widening beyond its
  existing documented hook/OTel mappings. Its installation coexists, but no speculative bridge was
  added.
- Agent-version metadata is `not_observed` for current OTel facts; the UI says so explicitly.
- Cost is a versioned API-equivalent estimate, never subscription billing.
- Historical ambiguous attribution remains untouched and excluded; only fresh exact evidence
  populates the new direct attribution columns.
