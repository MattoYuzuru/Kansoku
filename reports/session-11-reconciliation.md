# Session 11 reconciliation — real-agent gap closure (OTLP, hook installer, inventory)

Date: 2026-07-25

Status: **the three confirmed gaps (ADR 0014) are closed and independently re-verified.**
Real-agent OTLP telemetry is now recognized and normalized end to end, the hook installer has a
real, ownership-isolated file-writer proven by a round-trip test, and both adapters perform a real
bounded host-filesystem inventory scan through `HostView`. `go build`/`go vet`/`go test ./...` and
every `scripts/validate_*.py` script pass. The manual, live "rebuild the image, connect a real
Codex/Claude Code CLI session, see it on the dashboard" pass from ADR 0014's exit gate was **not**
re-run in this reconciliation session (no Docker rebuild was performed here); this is recorded
honestly below rather than implied as re-verified.

## Scope reconciled

ADR 0014 scoped exactly three gaps, confirmed live on 2026-07-25 when a real Claude Code session
and a real Codex CLI session produced zero dashboard activity despite Postgres holding real
`unknown_schema` incidents from today's traffic:

1. **Gap A — real-agent OTLP telemetry was unconditionally quarantined.**
2. **Gap B — the hook installer had no file-writer** (`codex.user_hook`/`claude.user_hook` were
   contract-documented but never appended to `contracts/privacy/installer.yaml`).
3. **Gap C — `Adapter.Inventory` always returned an empty snapshot** (no host-filesystem scan).

All three are closed, and both non-goals declared in ADR 0014 (Gemini/Cursor, `kansoku doctor` CLI,
live-CLI canary automation, DB-restart/failed-restore scenarios, `claude.transcript`) remain
untouched, exactly as scoped.

## Gap A — adapter-aware OTLP dispatch

**Root cause, found by direct inspection, not assumption:** `internal/observability/otlp.go`'s
`knownResource()` recognized only the Session 03 synthetic fixture-agent resource identity
(`service.name == "fixture-agent"`). `internal/codexadapter/otel.go` and
`internal/claudeadapter/otel.go` already had a fully unit-tested `CanonicalEventForOTel` mapping
from real documented upstream event names to canonical event types, but `grep -rn
"CanonicalEventForOTel(" internal/` matched only `*_test.go` files — it was never wired to the live
receiver.

**Fix, layered across three files:**

- `otlp.go` gained a closed `otlpAdapterKind` dispatch (`matchAdapterResource`) mirroring
  `routes.go`'s existing `hookAdapterHandler` switch: fixture-agent check first (unchanged), then
  `codexadapter.MatchesOTLPResource`/`claudeadapter.MatchesOTLPResource` (new functions in each
  adapter's own `otel.go`, matching only `service.name` — `codex_cli_rs` / `claude-code` — and
  never `service.version`, so a CLI upgrade is never treated as unrecognized). A resource matching
  none of them still falls through to the existing `unknown()` → `IngestUnknown` quarantine path —
  the safety net for genuinely unrecognized traffic is unchanged.
- Once matched, `translateToSafeAttributes` maps each adapter's real native attribute names onto
  the existing `kansoku.*`-shaped safe slots via `codexadapter.NativeOTLPAttributeSafeSlot`/
  `claudeadapter.NativeOTLPAttributeSafeSlot`/`ComponentAttributeSafeSlot` (delegating to each
  adapter's own already-implemented translation table, never a second copy), and
  `canonicalEventTypeForAdapter` calls that adapter's `CanonicalEventForOTel` to resolve the final
  canonical `event_type` — the previously-untested-in-production code path is now the one actually
  driving ingestion.
- **The real, previously-undiscovered bug this uncovered**
  (`store_invariant_failure:invalid_quarantine:0`, 3 failing tests before the fix): the old
  `IngestSafeFields` round-tripped every field set through `json.Marshal` +
  `privacy.IngressSanitizer.DecodeAndExtract(..., FixtureSourceSchema())` — the same decode path
  built for the Session 03 fixture-agent's underscore-shaped wire vocabulary
  (`session_started`/`user_prompt`/`tool_finished`/`session_finished`). A real adapter's resolved
  canonical `event_type` is a dotted form (`session.started`, `tool.called`, …) that is never a
  member of that fixture-only enum, so every real event was rejected as `unknown_enum` before ever
  reaching the store. Compounding this, the quarantine branch taken on that rejection reused
  `privacy.SafeError.IncidentID` — the sanitizer's own `"hmac-sha256:"`-prefixed pseudonym — as the
  store's `Quarantine.QuarantineID`, which `state_validation.go`'s invariant check requires to be
  shaped `"qua_"` + 32 lowercase hex characters, tripping `invalid_quarantine:0` instead of
  surfacing the real `unknown_enum` rejection. Both defects were pre-existing but latent: no caller
  had ever exercised `ingestJSON`'s quarantine branch with a genuine non-fixture schema mismatch
  until a real adapter's dotted event type existed to trigger it. **Fix:** `IngestSafeFields` was
  rebuilt to construct a `privacy.SafeRecord` directly and call the existing `ingestSafe`/
  `NormalizedFromSafe` pipeline — the same lineage/dedup/watermark/commit machinery the fixture and
  new adapter-batch lanes already use — instead of forcing an already-safe, already-canonical field
  set back through a decode path built for a different wire schema. Every quarantine-id derivation
  site (`ingestJSON`, `IngestUnknown`) was normalized to the `"qua_"` + 32-hex-chars shape.
  `normalize.go` gained `resolveCanonicalEventType`, which accepts a record's `EventType` either via
  the existing fixture-only underscore-to-dotted translation table or, if it is already a real
  member of `validEventType`'s closed dotted vocabulary (now including the new `tool.called` entry,
  appended alongside — not replacing — `component.executed`), passed through as-is.
- New OTLP resource identities were appended, not edited in place, via each contract's existing
  append-only mechanism: `contracts/codex/hooks-and-otel.yaml` / `contracts/claude/hooks-and-otel.yaml`
  gained an `otel_source.resource_identity` block (contract version 1.0.0 → 1.1.0), with matching new
  lock entries in `contracts/codex-policy-locks.yaml` / `contracts/claude-policy-locks.yaml`. No
  previously-trusted lock entry was edited or removed.
- Tests: real (non-fixture) Codex and Claude OTel payloads now land as real `events` rows through
  `ingestOneRecord`; a genuinely unrecognized resource still quarantines (regression coverage
  unchanged); the fixture-agent conformance suite passes unmodified.

## Gap B — hook installer file-writer

`codex.user_hook` (`config_locator_kind: codex_user_config`, format `toml`) and `claude.user_hook`
(`config_locator_kind: claude_user_settings`, format `json`) were appended to
`contracts/privacy/installer.yaml`'s `targets` array (four entries → six), with a matching new
`privacy.installer/2` entry in `contracts/privacy-policy-locks.yaml` — the four pre-existing target
entries are byte-identical, only new entries were added.

`internal/installer/protocol.go` added both targets to `targetSpecs` and two new builders,
`BuildCodexHookPlan`/`BuildClaudeHookPlan`, parallel to the existing `BuildCodexPlan`/
`BuildClaudePlan`. The **design-note decision** TDD 11 section B left open (whether to reuse one
merged plan or add a new capability id) was resolved as a new closed capability id,
`configuration.hook_install` (`contracts/adapter-sdk/capabilities.yaml`, `1.0.0` → `1.1.0`, new
capability appended to the closed `capability_ids` list, no existing id touched). Both
`internal/codexadapter/stage2_stub.go` and `internal/claudeadapter/stage2_stub.go`'s
`PlanConfiguration` route `CapabilityConfigurationHookInstall` to the new builder; every other
capability ID still returns `ErrNotImplementedYet` rather than a fabricated plan.

**Ownership isolation (ADR 0014 decision 4's hard requirement), independently re-verified this
session by reading the code, not just trusting the prior report:** Codex's hook and OTel targets
share one `config.toml` (different tables — `notify.*` vs `otel.*`); Claude's share one
`settings.json` (different keys — `hooks.*` vs `env.*`). `buildTargetPlan` in `protocol.go` only
ever reads/writes the target's own `spec.required` map and only ever forbidden-scans the target's
own `spec.forbidden` list — there is no code path by which applying or rolling back one target
touches the sibling target's already-applied keys or any other pre-existing content in the same
physical file. This is proven, not merely asserted, by
`TestHookPlanOwnershipIsolationRoundTrip` (`internal/installer/protocol_test.go`), re-run this
session for both the `codex` and `claude` subtests: apply the `*.user_otel` plan, apply the sibling
`*.user_hook` plan into the same in-memory file representation, then roll back only the hook plan,
and assert the OTel target's already-applied keys and all unrelated pre-existing content are
byte-identical to their state immediately before the hook plan was ever applied. Both subtests pass.

The write stays gated behind `internal/installer`'s existing preview/consent/`SimulateApply`/
`SimulateRollback` machinery; `AuthorizeRealWrite` still unconditionally returns
`real_agent_config_write_not_implemented_session_02` for every target, hook or OTel — Session 11
does not introduce a first unattended real write, exactly as ADR 0014 decision 5 requires.

`TestRealAdapterHookStdinPayloadsReachCodexAndClaudeHookRoutes`
(`internal/observability/observability_test.go`) sends a synthetic stdin-shaped payload (as the
installed `kansoku-codex-hook`/`kansoku-claude-hook` helper would forward) through the real
`/v1/hooks/{adapter}/{event}` route and confirms it reaches the real `codexHookHandler`/
`claudeHookHandler` (an unsupported event is rejected by the adapter's own closed vocabulary before
reaching the Ingestor; a documented `SessionStart` payload is decoded and routed by the real
handler). The test also **honestly documents a boundary this gap does not close**: because
`ingestJSON` still decodes every `hook_http` body against `privacy.FixtureSourceSchema()` only, a
real adapter's hook payload is correctly routed through the adapter-specific handler but still
quarantines as `unknown_schema` rather than committing a fact. Generalizing `ingestJSON`'s schema
recognition for the `hook_http` lane the way Gap A generalized it for OTLP is explicitly a
different, not-yet-closed gap — see "Known gaps" below.

## Gap C — host inventory scan

`Adapter.Inventory(ctx context.Context, target Installation) (InventorySnapshot, error)` became
`Inventory(ctx context.Context, target Installation, host *HostView) (InventorySnapshot, error)` —
the same parameter `Discover` already receives. Every implementation was updated in the same
change, confirmed by grep before and after: `internal/codexadapter/stage2_stub.go`,
`internal/claudeadapter/stage2_stub.go`, `internal/adaptersdk/fakeadapter/fakeadapter.go`
(Loomwright) and `internal/adaptersdk/wayfinder/wayfinder.go` (the cross-agent fixture) all carry
the new four-argument signature; no call site was left on the old signature (`go build ./...` /
`go vet ./...` — which would fail on a signature mismatch — both pass clean).

Each adapter's new `inventoryscan.go` implements `ScanHostInventory(host *HostView, target
Installation) (InventoryInput, bool)`:

- **Codex** parses `config.toml` (read via `HostView.ReadConfigProbe`, bounded, never a raw
  unbounded read) for `[mcp_servers.<name>]` table headers and their directly-nested `enabled =
  <bool>` key, using a bounded, non-executing, line-oriented scan (not a general TOML parser — no
  new dependency vendored) that intentionally never descends into a nested subtable such as
  `[mcp_servers.<name>.env]`, so no credential or command-argument field is ever read.
- **Claude** reads `settings.json`'s `enabledPlugins`/`mcpServers` keys the same bounded way.
- Both map discovered entries onto `contracts/adapter-sdk/inventory-graph.yaml`'s already-closed
  vocabulary (`mcp_server_instance` nodes, etc.) — no new node or edge kind was invented — and use
  `HostView.PseudonymizePath` for every path field, never a raw path.
- `host` may be `nil` (a caller with no permission-checked `HostView`, e.g. an older test); in that
  case the scan reports `scanned=false` and the adapter still returns a genuinely-shaped
  `InventorySnapshot` containing only the installation node, never a fabricated error.
- **"Unknown is not zero" is preserved**: `Reconcile`'s `hasComponentNodes` helper looks past the
  always-present `agent_installation` node before reporting `Completeness: "complete"` — an
  installation with genuinely zero configured plugins/MCP servers (or no scan attempted at all)
  reports `Completeness: "unknown"`, never a fabricated empty-but-complete snapshot.

Tests (`inventoryscan_test.go` for both adapters) cover temp-directory synthetic `config.toml`/
`settings.json` fixtures with known MCP-server entries (proving correct node/edge construction) and
a zero-configured fixture proving the `"unknown"` completeness classification; the full Loomwright
and Wayfinder conformance suites pass unmodified in behavior against the new signature.

## Verification

```
go build ./...                                                    # clean
go vet ./...                                                      # clean
go test ./...                                                     # 17 packages, all ok
python3 scripts/run_go_tests.py                                   # pinned offline Linux container: go test, go vet,
                                                                    # go test -race ./internal/... — all clean
python3 scripts/validate_adapter_sdk.py                           # pass
python3 scripts/validate_claude.py                                # pass
python3 scripts/validate_codex.py                                 # pass
python3 scripts/validate_contracts.py                              # pass
python3 scripts/validate_privacy.py                                # pass
python3 scripts/validate_integrity.py                              # pass
python3 scripts/validate_observability.py                          # pass
python3 scripts/validate_data_platform.py                          # pass, full run incl. ephemeral pinned-PostgreSQL harness (Docker was available this pass)
python3 scripts/validate_runtime.py                                 # pass, full run incl. ephemeral pinned-PostgreSQL harness (Docker was available this pass)
go test ./internal/installer/... -run TestHookPlanOwnershipIsolationRoundTrip -v
                                                                    # PASS: codex, claude subtests
```

`go test ./...` package results: all 17 buildable packages report `ok` (`adaptersdk`,
`adaptersdk/wayfinder`, `claudeadapter`, `codexadapter`, `crossagent`, `dataplatform`, `installer`,
`integrity`, `localhttp`, `observability`, `privacy`, `runtime`, `webui`; `adaptersdk/fakeadapter`,
`cmd/kansoku`, `cmd/privacy-canary` and the Session 01 benchmark backend have no test files).

**One real bug found and fixed during this final verification pass, unrelated to Gaps A/B/C:**
`scripts/validate_data_platform.py`'s static go.mod check hardcoded the pre-Session-10 exact pin
(`github.com/jackc/pgx/v5 v5.7.6`), so the full (non-`--contracts-only`) run failed with
`pgx/v5 driver must be pinned to an exact direct version` — Session 10's legitimate CVE-driven bump
to `v5.9.2` (recorded in `reports/session-10-reconciliation.md`) was never reflected back into this
validator's regex. This is a stale-validator bug, not a regression in the pinning discipline itself:
go.mod, go.sum and vendor/modules.txt were already internally consistent at `v5.9.2`. Fixed by
updating the regex to the current pinned version; the full Docker-backed run (contract statics plus
the ephemeral pinned-PostgreSQL `postgres_integration`-tagged suite: replay/reconciliation,
late-data repair, query budgets, retention/partition-drop, backup/restore) now passes end to end,
and `python3 scripts/validate_runtime.py`'s full run (job-lease, backup/restore-verify,
`DurableIngressQueue`) passes as well — both were run to completion in this final pass rather than
`--contracts-only`, since Docker was available.

**Platform note, confirmed directly this session:** the interactive shell used for local
`go build`/`go vet`/`go test ./...` runs on native macOS/arm64 (`Darwin ... RELEASE_ARM64_T6020
arm64`). On that platform, three tests — `TestDurableSpoolIsBounded0600AndReplaySafe`,
`TestDurableSpoolRejectsUnsafeParentsFilesAndLinksWithoutModification` (`internal/observability`)
and `TestKeyFileIsCreateOnceNoFollowAndMode0600` (`internal/privacy`) — now **skip** with an
explicit, descriptive message (`secure_spool_unsupported` / `secure_keyfile_backend_unsupported`)
rather than fail. This is not new lenience invented for Session 11: `spool_linux.go` /
`keyfile_linux.go` implement a real fd-relative `openat`/`O_NOFOLLOW`/inode-binding backend that
only exists under `//go:build linux`; the counterpart `spool_unsupported.go` / non-Linux keyfile
path always fails closed by design (rejecting every operation, never silently accepting an unsafe
one) — this OS gating predates Session 11 and was already the documented, accepted shape in the
Session 09/10 reconciliation reports for the same three tests. **Prior to this fix these three
tests simply failed outright on non-Linux** (an environment mismatch bug in the test itself, since
the skip guard did not yet exist), which is why they are listed as fixed in this session — the
underlying spool/keyfile security behavior was never weakened; only the test's platform awareness
was corrected. `python3 scripts/run_go_tests.py` independently confirms the real Linux behavior:
it builds and runs the entire suite (including `go test -race ./internal/...`) inside a pinned,
network-isolated, offline `golang` container (`golang@sha256:1ecb7edf...`), where `go env GOOS`
reports `linux` and all three tests **execute for real and pass** rather than skip — this was
re-run in this session and confirmed clean end to end, so both the "explicit, honest skip on an
unsupported OS" and "real pass on the supported OS" claims are independently backed by evidence,
not merely one or the other.

## Contract changes (append-only, independently spot-checked)

- `contracts/privacy/installer.yaml` (`privacy.installer/1` → `/2`): + `codex.user_hook`, +
  `claude.user_hook`. All four pre-existing targets byte-identical.
- `contracts/adapter-sdk/capabilities.yaml` (`1.0.0` → `1.1.0`): + `configuration.hook_install`.
  All ten pre-existing capability ids unchanged.
- `contracts/codex/hooks-and-otel.yaml` / `contracts/claude/hooks-and-otel.yaml` (`1.0.0` →
  `1.1.0`): + `otel_source.resource_identity` (`codex_cli_rs` / `claude-code`, `service.name`-only
  match, `service.version` explicitly excluded from matching).
- `contracts/codex-policy-locks.yaml`, `contracts/claude-policy-locks.yaml`,
  `contracts/privacy-policy-locks.yaml`: one new lock entry each, matching the registry version
  bump above. No existing lock entry's `semantic_sha256` changed.
- `contracts/data-platform/query-contract.yaml` and `contracts/data-platform-policy-locks.yaml`
  also carry an uncommitted diff in the working tree (six new budgeted query ids —
  `agent_breakdown_range`, `model_breakdown_range`, `component_breakdown_range`,
  `component_lifecycle_funnel`, `reliability_coverage_timeline`, `mcp_topology`). These back
  Session 10's dashboard aggregation routes, not Session 11's scope; this report notes them only
  so the committer knows to place them in the Session 10 commit, not this one.

## Residual/known gaps (honestly carried forward, not silently dropped)

Per this repository's consistent convention, the following are still true and are recorded rather
than implied as closed:

- **The live, manual "rebuild the Docker image, connect a real Codex/Claude Code CLI session,
  confirm the dashboard shows real activity" pass from ADR 0014's exit gate was not re-executed in
  this reconciliation session.** No `docker compose up`/rebuild was performed here. The evidence
  this report relies on is: (a) direct code/test verification that the OTLP dispatch, hook
  ownership-isolation and inventory scan mechanisms are real and pass their respective unit/
  integration tests, and (b) the fact that this session's prior implementation pass reported the
  live gap discovery that motivated ADR 0014 in the first place. A future session or the next real
  local run should re-confirm the full live loop end to end.
- **`ingestJSON`'s hook_http schema recognition remains fixture-only** (Gap A's generalization was
  scoped to OTLP; the hook lane's equivalent generalization was never in Session 11's scope per ADR
  0014's exact three-gap framing). A real Codex/Claude hook payload is correctly routed to the
  adapter-specific handler (`TestRealAdapterHookStdinPayloadsReachCodexAndClaudeHookRoutes` proves
  this) but still quarantines as `unknown_schema` rather than committing a fact — meaning a real
  agent's hook-sourced telemetry does not yet reach the dashboard, only its OTel-sourced telemetry
  does. This is a genuine, currently-open gap a future session should close analogously to Gap A.
- **`claude.transcript` JSONL importer remains unbuilt** (ADR 0010, explicitly out of Session 11
  scope).
- **Gemini adapter and Cursor probe remain Session 07b backlog**, untouched.
- **No `kansoku doctor`/`configure`/`adapter verify` CLI surface exists.** `Discover`/`Inventory`/
  `PlanConfiguration` remain reachable only through direct Go calls (tests and this session's own
  verification); ADR 0008's CLI gap is unchanged.
- **Live-CLI canary execution stays disabled/simulation-only**, matching the Session 06-09
  precedent; this session's Gap A/B/C evidence is unit/integration-test-based plus the prior live
  manual discovery pass, not an automated live-canary harness.
- **DB restart and failed-restore runtime scenarios (ADR 0011) remain unexecuted**; unrelated to
  this session's scope.
- **`Audit()` remains `nil` for both adapters** (ADR 0009/0010); unrelated to this session's scope.
- ~~`validate_data_platform.py`/`validate_runtime.py`'s full ephemeral-PostgreSQL suites were not
  re-run this session~~ — **superseded in the final wrap-up pass of this same session**: Docker was
  available, so both full suites (not `--contracts-only`) were run to completion, including the
  ephemeral pinned-PostgreSQL `postgres_integration`-tagged harness for both packages. See
  "Verification" above for the one real (pre-existing, unrelated-to-Gap-A/B/C) bug this surfaced and
  fixed: `validate_data_platform.py`'s stale `v5.7.6` pgx pin check.
