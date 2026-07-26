# Session 12 reconciliation — incident workbench and safe quarantine

Date: 2026-07-26

Status: **exit gate green; production Compose appliance upgraded and verified.**

Session 12 provides the unified metadata-only incident read model, occurrence history, structural
quarantine manifests, HMAC-signed keyset pagination, Reliability health/incidents/quarantine views,
typed safe debug bundles, detector-owned recovery, separate triage state, daily audit checks and
aggregate-preserving detail retention.

## Baseline and migration

The read-only production baseline before the upgrade was:

```text
schema_migrations/integrity_schema_migrations/runtime_schema_migrations = 7/5/1
legacy ingress incidents/open = 2/2
integrity incidents/open = 0/0
schema quarantine fingerprints = 14
running image = sha256:04c4e799bfc441c3a307e7f34203aefaab69f3b917f8afd70589507f79fddb56
```

After migration the ledgers are `7/6/2`. Both legacy incidents and all 14 quarantine rows remain.
Migration created two explicit legacy summary occurrences and 14 manifests. Thirteen manifests
remain honestly `inc_unlinked_*`; a fresh exact observation proved the relation for the active
fingerprint and deterministically replaced only that link. The same fresh value-free protobuf
descriptor upgraded its shape from `not_observed` to `observed`. No unrelated historical record
was merged, deleted or reinterpreted.

## Contracts, migrations and implementation

- `contracts/incidents/model.yaml`, `contracts/incidents/quarantine.yaml` and
  `contracts/incidents-policy-locks.yaml` close identity, occurrence, cursor, detector/triage,
  recovery and metadata-only manifest semantics.
- Dashboard, metrics/formulas, privacy sinks, integrity, data-platform budgets and runtime/backup
  registries have reviewed append-only lock transitions.
- `internal/integrity/migrations/0006_incident_triage.*.sql` separates detector and triage state.
- `internal/runtime/migrations/0002_incident_workbench.*.sql` adds occurrences, explicit identity
  states, recovery lineage and structural manifests after both legacy migration ledgers.
- Exact idempotency replay does not increment counts. Independently keyed observations append an
  occurrence and update the aggregate transactionally.
- Recovery requires a newer durable `source.observed` event and a later passing targeted audit.
  There is no detector-state resolve mutation.
- Restore verification checks the immutable archive against its manifest, compiled ledgers,
  constraints, formula invariants and incident lineage. It does not compare an archive with later
  mutable source rollups.

## Live proof and reconciliation

The production appliance was rebuilt from the final source and restarted with:

```text
image/tag = kansoku:local
image ID = sha256:11b2aeffd5c4a20a3e01f84621598c53d8c6850d679b6ae20d542215fc5d8ca4
Compose kansoku = healthy
Compose postgres = healthy
container start = 2026-07-26T13:37:04Z
```

Live Codex evidence during the interval used `codex-cli 0.145.0`, adapter `codex@1.0.0`,
`otlp_log`, and model `gpt-5.6-sol`. The database also contains earlier measured
`gpt-5.6-luna` evidence; no Session 12 claim relabels the active Sol run as Luna.

The final bounded snapshot is moving under active ingestion; at the recorded read it contained:

```text
incidents/open = 3/3
incident occurrence rows = 74
legacy summary occurrence rows = 2
schema quarantine rows/manifests = 14/14
explicit legacy unlinked manifests = 13
orphan occurrences = 0
non-legacy aggregate/detail mismatches = 0
```

The active known-safe unknown fingerprint produced one incident and one manifest. Repeated live
observations increased its independent occurrence history without creating another fingerprint.
The PostgreSQL integration gate separately proves exact replay remains count-neutral, cursor
stability under a concurrent insert, parser-fixture recovery only after fresh supported evidence
and a later targeted audit, and confirmed metadata retention.

Cursor walks returned all three incidents over two `limit=2` pages and all 14 manifests over seven
pages, with no duplicates. Tampered cursors fail closed in the PostgreSQL/API test. Production JSON
and Markdown debug bundles were scanned for the raw fixture markers, unredacted user paths and
secret formats: zero matches.

`EXPLAIN (ANALYZE, BUFFERS)` measured:

```text
incident list: 0.598 ms
occurrence page: 0.237 ms (idx_incident_occurrences_page)
quarantine list: 0.040 ms
```

The small incident/manifest tables correctly chose bounded sequential scans; all measurements are
well below their registered budgets.

## Backup, privacy and browser proof

A production native backup captured `incidents=3`, `incident_occurrences=64` and
`quarantine_structural_manifests=14` at its immutable snapshot. Two consecutive
`restore-verify` calls both returned `status=pass` after live source data continued changing.
Temporary restore databases were removed by the verified runtime path.

The pinned network-isolated ten-sink canary scanned 10 accepted and 10 rejection artifacts:

```text
canary matches = 0
secret-format matches = 0
backup checksum match = true
isolated backup exact-byte match = true
safe-record exact fields = true
```

Chrome 150 headless production screenshots verified:

- `/reliability?tab=health`;
- `/reliability?tab=incidents`;
- `/reliability?tab=quarantine`.

The first browser run exposed a virtual-clock KPI animation underflow; the counter now clamps
progress to `[0,1]`. The rebuilt production screenshot displays only non-negative values.

## Verification commands

```text
python3 scripts/validate_contracts.py
python3 scripts/validate_privacy.py
python3 scripts/validate_observability.py
python3 scripts/validate_data_platform.py --contracts-only
python3 scripts/validate_adapter_sdk.py
python3 scripts/validate_codex.py
python3 scripts/validate_claude.py
python3 scripts/validate_integrity.py
python3 scripts/validate_runtime.py --contracts-only
python3 scripts/validate_incidents.py
python3 -m unittest discover -s tests
  159 tests: pass

python3 scripts/validate_data_platform.py --runtime-only
python3 scripts/validate_runtime.py --runtime-only
  PostgreSQL 18 integration suites: pass

go vet ./...
go test ./...
(cd web && npm run typecheck)
web/scripts/build-and-embed.sh
diff -qr web/dist internal/webui/dist
python3 scripts/run_privacy_canary.py
docker build -f deploy/Dockerfile -t kansoku:local .
docker compose -f deploy/compose.yaml up -d --no-deps --force-recreate kansoku
docker compose -f deploy/compose.yaml ps
```

## Debug scaffolding and cleanup

Kept as bounded developer evidence:

- `scripts/validate_incidents.py`;
- `scripts/reconcile_session12.sql`;
- `tests/fixtures/session-12/unknown-schema-canary.json`;
- PostgreSQL-tagged replay/pagination/recovery/retention/restore regressions.

No payload dump, catch-all parser, manual database repair, detector resolve endpoint or unbounded
debug logger was added. Browser screenshots and HTTP/backup responses stayed in temporary storage
and are not product telemetry.

## Residual risks and explicit unsupported states

- Thirteen historical quarantine rows cannot prove an incident relationship and remain explicitly
  unlinked. They require future fresh exact evidence, never heuristic assignment.
- Raw unknown payload replay is intentionally unsupported. Parser work uses a sanitized fixture and
  only fresh supported evidence can recover the detector.
- Hook/JSON unknown structural paths remain `not_observed`; only fixed reviewed protocol
  descriptors may produce an `observed` shape.
- Keyset totals remain a lower bound rather than a fabricated exact count.
- The package audit reports one direct high-severity Vite advisory with a non-major `6.4.3` fix;
  dependency remediation is not folded into the Session 12 behavioral commit and must be handled
  as a separately reviewed supply-chain update.
