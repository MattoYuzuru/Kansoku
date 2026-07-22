# Kansoku contract registries

These registries are the versioned product boundary established by Session 01. Files use the JSON
subset of YAML 1.2 so the bootstrap validator can run with the Python standard library only. Later
generators may emit native YAML without changing the schemas.

Run all contract checks with:

```sh
python3 scripts/validate_contracts.py
python3 scripts/validate_privacy.py
python3 scripts/validate_observability.py
python3 scripts/validate_data_platform.py
python3 -m unittest discover -s tests -v
python3 scripts/run_go_tests.py
python3 scripts/run_privacy_canary.py
python3 scripts/session03_supply_chain.py --verify
python3 scripts/session04_supply_chain.py --verify
```

Session 02 adds the closed privacy/security registry set under `contracts/privacy/`:

- `threat-model.yaml` and `data-classes.yaml` define protected assets, abuse cases and treatments;
- `ingress.yaml` fixes decoder bounds and exact typed durable/error/log allowlists;
- `sinks.yaml` makes database, logs, traces, durable/retry queues, quarantine, errors, dashboard
  traffic, export and backup mandatory zero-canary scopes;
- `installer.yaml` and `host-access.yaml` bind per-target preview/consent/race/rollback and every
  permitted host/config access;
- `deployment.yaml` and `retention.yaml` define local HTTP, container, egress, deletion and backup
  controls.

The executable Session 02 boundary is stdlib-only Go. Every privacy registry is recursively closed
and its canonical aggregate SHA-256 is embedded in the privacy, installer and HTTP packages; a
registry/runtime drift fails validation. That self-updated aggregate is a drift check, not a policy
trust anchor. `contracts/privacy-policy-locks.yaml` independently binds versioned canonical
semantic digests for all eight registries and preserves trusted-history entries append-only;
review-controlled exact invariants reject coherent registry/runtime/checksum weakenings.
`SafeRecord`, `SafeError` and every nested type are exact
typed allowlists; source maps never cross the sanitizer. The installer package exposes typed
Codex/Claude/Gemini/Cursor plan builders, effective-setting/canary verification and virtual apply/
rollback/removal only; it contains no agent-config filesystem writer.

Registry changes require a schema or formula version change, deterministic fixtures, and an update
to the relevant proposal/TDD. Existing entries in `formula-version-locks.yaml` are append-only after
their first trusted commit; privacy-policy locks follow the same reviewed version-transition rule.
In an archive or before the first lock commit, the checked-out policy lock is the deterministic
bootstrap authority. Afterward, protected review/CI must provide the external trusted revision;
local validation cannot resist simultaneous malicious replacement of validator, lock, tests and
history. A support label is a
capability-and-version claim, never a brand-wide claim. Supported and Beta both require an explicitly
parsed and ordered capability version range, typed evidence receipts bound to the exact
adapter/capability/range tuple and validator-recomputed canonical fixture bytes, and two independent
approved human review receipts bound to the same tuple and verified fixtures. Session sequencing
never bypasses that public-claim gate.

Session 03 adds four closed registries under `contracts/observability/`: canonical envelope,
source/event lifecycle, ingress protocols/durability, and reconciliation/watermarks/recovery. Their
versioned semantic digests live in `contracts/observability-policy-locks.yaml`; independent exact
invariants prevent a coherent lock edit from removing ambiguity, privacy, durable acknowledgement,
HTTP/gRPC protobuf, three-lane or metadata-only-quarantine requirements. The synthetic shared
scenario is `tests/fixtures/session-03/shared-scenario.json`. ADR 0006 limits the current durable
writer to a bounded single-process fsync/rename spike and records that OTLP gzip remains an explicit
conformance gap rather than a support claim.

Session 04 adds four closed registries under `contracts/data-platform/`: schema, rollups,
query-contract and retention. Their versioned semantic digests live in
`contracts/data-platform-policy-locks.yaml`; independent exact invariants prevent a coherent lock
edit from weakening the pinned PostgreSQL 18 engine digest, the partition-drop-only retention
mechanism, the percentile method/forbidden-averaging clause, the budgeted query id set/ceilings, or
the restore-test cleanup requirement. `scripts/validate_data_platform.py` starts an ephemeral,
pinned-digest Postgres container to reconcile a synthetic reference dataset exactly, enforce query
budgets on both the server and client side, and verify lineage-preserving backup/restore. ADR 0007
records mergeable percentile sketches, million-event query-budget evidence, time-range preset
resolution and cost-formula computation as explicit downstream gaps rather than silent scope drops.
