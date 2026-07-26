# Kansoku: deliberate-session roadmap

Каждая сессия рассчитана на отдельный рабочий цикл: исследование, утверждение решений,
реализацию, тесты, review и обновление документации. Это порядок зависимостей, а не обещание
одинаковой длительности.

| Session | Тема | Главный результат | Exit gate |
|---:|---|---|---|
| 01 | Product contract and SLOs | Глоссарий, user journeys, scope, измеримые SLO и ADR baseline | Нет неоднозначных терминов или скрытых product decisions |
| 02 | Privacy, security and trust | Threat model, data classes, retention, consent и безопасная установка | Raw-content canary не попадает ни в один storage/log path |
| 03 | Core observability architecture | Canonical event envelope, lifecycle, ingestion paths и lineage | Один fixture проходит hook, OTLP и transcript routes идемпотентно |
| 04 | Data platform and analytics | PostgreSQL schema, partitions, rollups, quantiles и query budgets | Replay даёт точные агрегаты и укладывается в performance budget |
| 05 | Adapter SDK and inventory | Capability model, discovery, versioning, plugin API и inventory graph | Dummy third-party adapter работает без изменения core |
| 06 | Codex adapter | Hooks, OTel, sessions, skills/plugins/MCP inventory и reconciliation | Поддерживаемый Codex canary даёт полную ожидаемую цепочку или incident |
| 07 | Claude adapter and next-agent portability proof | Claude adapter, второй fixture-agent, cross-agent conformance (Codex + Claude) | Claude данные сосуществуют с Codex в одной canonical model без agent-specific core schema |
| 07b | Gemini and Cursor | Gemini adapter, Cursor probe, обновлённая portability validation на три реальных источника | Gemini/Cursor нормализуются без agent-specific core schema; Cursor остаётся Experimental |
| 08 | Integrity and drift detection | Daily audit, schema drift, watermarks, canaries и health scoring | Намеренная поломка каждого source обнаруживается в пределах SLO |
| 09 | Local runtime and backend | API, Docker Compose, scheduler, backup/restore и resource controls | 7-day soak переживает restarts без silent loss и duplicate inflation |
| 10 | Dashboard, hardening and release | Полный UX, accessibility, review, packaging и evolution loop | Privacy/reliability/performance gates зелёные; fresh install воспроизводим |
| 11 | Real-agent gap closure | Adapter-aware OTLP dispatch, hook installer file-writer, host inventory scan | Реальная локальная сессия Codex/Claude Code даёт видимую активность на дашборде без ручных правок кода |
| 12 | Incident workbench and safe quarantine | Единое пагинированное расследование incidents/quarantine в Reliability | Unknown schema дедуплицируется, объясняется безопасным manifest и закрывается только свежим recovery evidence |
| 13 | Agent evidence bridge and model observatory | Расширяемый rich-evidence bridge плюс per-agent/per-model профили | Codex/Claude/fake bridge не дублируют факты и не требуют agent-name branching в core |
| 14 | Skill observatory | Availability/runtime evidence planes и кликабельные skill-профили | Exact invocation/load evidence сходится; universal executed/succeeded больше не обещаются |
| 15 | MCP observatory | Server/tool inventory, protocol connection и call lifecycle profiles | No-op MCP доказывает inventory/connect/call/error/timeout/cancel без raw content |
| 16 | Plugin observatory | Plugin bundle graph, load evidence и child usage | Canary plugin показывает точные skill/MCP relations без fabricated plugin success |
| 17 | System self-observability | CPU/RSS/disk/growth/ingest/query/backup/restore telemetry | Controlled load и restore failure видимы при измеренном bounded overhead |
| 18 | Design system and content access | Visual regression и полный opt-in skill/plugin content viewer | Все presets/UI states согласованы; viewer проходит containment/privacy gates |
| 19 | Local control plane and assisted remediation | Reversible component changes и approval-gated incident agent workflow | Ни один write/commit/restart/resolution не обходит preview, confirmation и fresh evidence |
| 20 | Opportunity evaluation lab | Research contract для eligible/selected/missed | До privacy/false-positive/formula approval production metric отсутствует |

## Progress

- **01 — complete (2026-07-21):** registries, SLO harness, reproducible spikes, ADR 0001 and
  reconciliation report exist; automated contract gates pass. ADR 0002 separates that sequencing
  gate from the still-blocked public adapter Supported/Beta gate, which requires two independent
  approved human reviews plus bounded privacy/replay/audit/canary evidence.
- **02 — complete (2026-07-21):** typed bounded sanitizer, data/threat/host/retention registries,
  local HTTP and container policy, virtual installer consent/rollback, and the independent
  ten-sink raw-content canary pass. Versioned privacy-policy locks and independent exact invariants
  reject coherent registry/runtime/checksum weakening; protected review/CI remains the external
  trust root. This synthetic privacy proof does not satisfy adapter-specific public support evidence.
- **03 — complete (2026-07-21):** closed canonical envelope/lifecycle/ingress/reconciliation
  contracts and a typed Go spike converge one synthetic fact across authenticated hook, OTLP
  HTTP/gRPC protobuf and checkpointed transcript lanes. Replay/reorder/crash/source-loss/unknown-
  schema/privacy gates pass. ADR 0006 keeps the bounded file writer explicitly pre-PostgreSQL and
  records unsupported OTLP gzip as a conformance gap.
- **04 — complete (2026-07-22):** closed schema/rollups/query-contract/retention registries and a
  real PostgreSQL 18 `internal/dataplatform` package replace the Session 03 file-durability spike.
  Monthly-partitioned facts, exact `percentile_cont` rollups with a never-average late-data repair
  path, two-sided query-budget enforcement, partition-drop retention and lineage-verified
  backup/restore all pass against an ephemeral, pinned-digest Postgres harness. ADR 0007 records
  million-event query-budget evidence, mergeable percentile sketches, time-range preset resolution
  and cost-formula computation as explicit downstream gaps.
- **05 — complete (2026-07-22):** closed manifest/capabilities/inventory-graph/discovery-and-plans
  registries and a typed `internal/adaptersdk` package deliver the `Adapter` interface, a
  permission-checked `HostView`, an immutable inventory entity graph and a reversible `ChangePlan`
  that reuses `internal/installer`'s existing `Plan`/`Approval`/`SimulateApply`/`SimulateRollback`/
  `SimulateRemove`/`PlanSHA256` machinery instead of a second mechanism. A fake external-vocabulary
  conformance adapter ("Loomwright") passes the full discovery/inventory/normalization/
  reconciliation/audit suite through the same `Registry`/`CapabilityMatrix`/`HostView` APIs any
  built-in adapter would use, with zero agent-name branch in core code. ADR 0008 records
  external-process/Wasm adapter execution, compatibility-registry persistence and the
  `kansoku doctor`/`configure`/`adapter verify` CLI as explicit downstream gaps.
- **06 — complete (2026-07-22):** closed manifest/discovery, hooks-and-otel, rollout-and-inventory
  and skill-evidence-and-reconciliation registries and a typed `internal/codexadapter` package
  deliver the first real `internal/adaptersdk` registration: Codex. Four independently-degrading
  sources (`codex.hook`, `codex.otel`, `codex.rollout`, `codex.inventory`) reconcile into one session
  view without ever fabricating a whole-session zero, and the five-tier skill-evidence model never
  promotes inferred or reconstructed evidence to a native exact activation. The adapter reuses the
  existing `codex.user_otel` installer target and the generic `/v1/hooks/{adapter}/{event}` ingress
  route verbatim, adding only a new `codex.user_hook` target. ADR 0009 records the sequential
  checkpointed build order and lists live-CLI canary evidence and CLI surface as explicit
  downstream gaps.
- **07 — complete (2026-07-23):** closed manifest/hooks-and-otel/transcript-and-inventory/
  skill-evidence-and-reconciliation registries under `contracts/claude/` and a typed
  `internal/claudeadapter` package deliver the second real `internal/adaptersdk` registration —
  Claude Code — with zero new agent-name branch inside `internal/adaptersdk` itself. `claude.hook`'s
  seven-event vocabulary and `claude.otel` (reusing the existing `claude.user_otel` installer target
  verbatim) unconditionally strip prompt/tool/response/raw-body content regardless of upstream
  `OTEL_LOG_*` settings, and the seven-tier skill-evidence model never promotes inferred or
  ambiguous-ownership evidence to a native exact activation. A second, differently-shaped fictional
  fixture-agent ("Wayfinder", `contracts/cross-agent/`) and a Codex+Claude cross-agent invariant test
  (`internal/crossagent`) prove the SDK needs no core change for either a second real adapter or a
  second synthetic agent. Session scope was narrowed from the original Claude+Gemini+Cursor grouping:
  Gemini and the Cursor probe are deferred to a new **Session 07b**, so Claude adapter evidence lands
  sooner without waiting on two more agents. ADR 0010 records the scope-narrowing rationale and lists
  the missing `claude.transcript` JSONL importer, live-CLI canary evidence and nil `Audit` as explicit
  downstream gaps.
- **07b — deferred/backlog (2026-07-23):** Gemini adapter and Cursor probe (deferred from the
  original Session 07 scope). 07b was originally slated as the immediate next session, but the
  sequencing changed on 2026-07-23: it is pulled out of the main dependency chain into backlog so
  Session 08 can start now. It keeps the original TDD/proposal 07 content for Gemini/Cursor and is
  not cancelled, just decoupled from the 08-10 sequence — whoever picks it up later should re-verify
  it against whatever Sessions 08-10 have changed in the meantime.
- **08 — implementation and bounded runtime validation complete; 2 fault claims pending (2026-07-24):** four locked integrity registries and `internal/integrity`
  implement the durable 11-stage daily audit over the existing PostgreSQL pool and a session-scoped
  advisory lock. Checks are source-aware and timeout-bounded; stale runs fail visibly, drift
  fingerprints are structural-only and trigger targeted stages, incidents require later fresh
  positive recovery, and the Health API exposes nine decomposed green/yellow/red/gray dimensions
  without a magic score. The 21-entry fault catalog is evidence-partitioned: 17 component
  classifiers (no end-to-end SLO claim), 2 measured PostgreSQL mutation integrations, and 2
  runtime-required scenarios (DB restart and failed restore). The mutation integrations measure
  scheduler/Stage-11/persistent-incident detection at durable `Incident.OpenedAt`; both passed in
  the full pinned PostgreSQL 18 tagged suite. DB restart and failed restore remain unexecuted, so
  there is no aggregate 21-fault runtime claim. Production assembly
  and report signing fail closed; the synthetic probe uses the shared public-ingress-to-PostgreSQL
  handoff and verifies rollups with exact cleanup. Live canary execution remains disabled and
  simulation-only pending credentials plus explicit consent; Session 07b remains untouched backlog.
- **09 — complete (2026-07-24):** four locked `contracts/runtime/*.yaml` registries and a typed
  `internal/runtime` package assemble one `cmd/kansoku` appliance over a single `pgxpool.Pool`,
  observability ingestor/OTLP receiver, runtime API, operations worker set and integrity assembly,
  reusing Sessions 02–08 with no second event schema, store or installer protocol. Strict versioned
  JSON config, file-only pairwise-distinct secrets, loopback/container-bridge HTTP policy, a
  reservation-capable durable ingress queue (PostgreSQL-first acknowledgement with sanitized spool
  fallback and replay), separated ingress/read/mutation bearers, PostgreSQL row-lease jobs with
  bounded attempts, native `pg_dump`/`pg_restore` backup with isolated randomly-named restore
  verification and drop-on-failure cleanup, deterministic forbidden-field-free diagnostics and
  bounded `http.Server.Shutdown` were contract-locked; the registries and
  `contracts/runtime-policy-locks.yaml` pass their authoritative semantic digests unchanged. The
  closing pass replaced the two remaining fakes-only proofs with real evidence: a pinned-PostgreSQL 18
  `internal/runtime/postgres_integration_test.go` (job-lease acquire/renew/`already_running`/stale
  `RecoverInterrupted`, a full backup→restore-verify round trip through real pg tools with cleanup on
  both success and injected pg_restore failure, and `DurableIngressQueue` reserve→commit against a
  real `dataplatform.NewObservabilityHandoff` with zero duplicate inflation), run via
  `scripts/validate_runtime.py --runtime-only` on the same isolated-network, deterministic-teardown
  harness Sessions 04/08 use; and a real host-side `dockerSoakDriver` (`cmd/kansoku soak`) that makes
  genuine `/api/v1` HTTP calls and issues real `docker`/`docker compose` process-restart,
  database-restart and stop-the-world upgrade-boundary faults. `python3 scripts/validate_runtime.py
  --soak` executed the accelerated soak for real against a live Compose stack: 168 logical cycles,
  all three restart faults with real health-endpoint recovery, 168/168 acknowledged/unique facts,
  all seven durability assertions green, ~2 minutes wall-clock, container restart timing independently
  confirmed inside the soak window — the "7-day soak survives restarts without silent loss and
  duplicate inflation" exit gate met as an explicitly accelerated logical-cycle harness (no seven-day
  wall-clock claim). ADR 0012 records the appliance/durability/backup/soak decisions;
  `reports/session-09-reconciliation.md` documents residual gaps (Docker Desktop harness attaches
  kansoku to an extra host bridge so loopback ports forward locally while postgres stays internal-only;
  aggregate rather than per-id durability probe; at-rest encryption and release image signing remain
  Session 10). `deploy/compose.security-baseline.yaml` is left frozen as the Session 02 privacy static
  placeholder; `deploy/compose.yaml` is the sole Session 09 Compose authority. Session 07b remains
  untouched backlog.
- **10 — complete (2026-07-25):** the first and only web UI. `contracts/dashboard.yaml`'s 14 routes
  are built end to end: `/api/v1/analytics` extended plus 11 new `internal/dataplatform` aggregation
  files (`activity_timeline`, `entity_breakdown`, `funnels`, `mcp_topology`, `mcp_uptime`,
  `model_usage`, `prompt_shape`, `reliability_counts`, `reliability_timeline`, `system_snapshot`,
  `tool_analytics`, `privacy_canary_history`) back every panel; a TypeScript/React 19.2.8/wouter/
  TanStack Query v5 frontend (`web/`) renders all 8 formal view-states
  (complete/partial/degraded/unsupported/not_observed/redacted/unknown/numeric_zero) with no silent
  zero; `internal/webui` embeds the built SPA into the Go binary via `go:embed` and serves it
  read-only on port 43100 — the browser only ever receives the read bearer, never the mutation
  bearer (`POST /api/v1/admin/export` with the read bearer alone returns 403, confirmed live). The
  hardening pass (ADR 0013 gate 4) found and fixed two real issues: a CSP `script-src` nonce that
  `html/template` was HTML-entity-escaping (base64's `+` -> `&#43;`), fixed by switching to a
  hex-encoded nonce so the header and the rendered attribute are byte-identical; and an eager-bundle
  violation where all 14 pages shipped the ~381 KB gzip ECharts chunk regardless of use, fixed with
  `React.lazy` route-level code splitting (main chunk 78.89->67.88 KB gzip; chart-free routes'
  initial JS payload cut ~82%). `govulncheck` found two real, reachable dependency CVEs
  (GO-2026-5004 pgx placeholder-confusion SQL-injection risk, GO-2026-5970 `x/text` DoS
  infinite loop); with explicit user consent, `github.com/jackc/pgx/v5` was bumped 5.7.6->5.9.2 and
  `golang.org/x/text` 0.36.0->0.39.0, re-vendored, re-tested, and `govulncheck` re-run clean. A live,
  real end-to-end raw-content scan -- the Session 02 canary fixture POSTed through the real hook
  ingress (port 4318, a separate listener from the dashboard's port 43100), landed in Postgres, then
  all 15 GET `/api/v1/*` responses and the raw Postgres rows scanned for every canary marker --
  found zero leaks. `reports/session-10-reconciliation.md` and `reports/session-10-sbom.json`
  record full verification evidence and the honest list of hardening gates **not** executed this
  session (no browser automation tool available for a real browser/visual-regression matrix;
  7-day wall-clock soak, ARM64/x86_64 build matrix, disk-forecast/load-test-at-scale, and
  signed release images all remain future work, several already covered at the runtime layer by
  Session 09). `deploy/Dockerfile` needed no change: `internal/webui/dist` is a committed,
  pre-built artifact embedded via `go:embed`, exactly like `vendor/` for Go dependencies.
- **11 — implementation complete; live end-to-end pass not re-executed this session
  (2026-07-25):** live testing with a real, locally-installed Claude Code session and a real Codex
  CLI session found the dashboard showed zero activity despite real traffic arriving — confirmed
  via Postgres showing `unknown_schema` incidents/quarantine rows from today while
  `events`/`sessions` stayed empty. Investigation found three previously-unrecorded gaps, all now
  closed. **Gap A (OTLP):** `internal/observability/otlp.go` gained an adapter-dispatch step
  (`matchAdapterResource`) recognizing real Codex (`service.name == "codex_cli_rs"`) and Claude Code
  (`service.name == "claude-code"`) OTel resources alongside the unchanged Session 03 fixture-agent
  identity, wiring the already-tested `CanonicalEventForOTel` mapping to the live receiver for the
  first time; a genuinely unrecognized resource still quarantines unchanged. Closing this also
  surfaced and fixed a real, previously-latent bug it had never been exercised against:
  `IngestSafeFields` round-tripped fields through the fixture-only sanitizer decode path, rejecting
  every real dotted `event_type` (e.g. `session.started`) as `unknown_enum` and then mis-deriving a
  malformed `QuarantineID` that tripped the durable-store invariant check
  (`store_invariant_failure:invalid_quarantine:0`) instead of surfacing the real rejection —
  `IngestSafeFields` was rebuilt to build a `privacy.SafeRecord` directly and reuse the existing
  `ingestSafe` pipeline instead. New resource identities were appended (not edited) to
  `contracts/codex/hooks-and-otel.yaml`/`contracts/claude/hooks-and-otel.yaml` (1.0.0 → 1.1.0) with
  matching new policy-lock entries. **Gap B (hook installer):** `codex.user_hook`/`claude.user_hook`
  were appended to `contracts/privacy/installer.yaml` (four targets → six) and a new closed
  capability id `configuration.hook_install` was added to `contracts/adapter-sdk/capabilities.yaml`
  (1.0.0 → 1.1.0); `installer.BuildCodexHookPlan`/`BuildClaudeHookPlan` and each adapter's
  `PlanConfiguration` wiring reuse the existing `Plan`/`Approval`/`SimulateApply`/
  `SimulateRollback`/`SimulateRemove`/`PlanSHA256` machinery with no second apply mechanism.
  Ownership isolation (Codex's hook/OTel tables and Claude's hook/OTel keys sharing one physical
  file each) is proven, not merely asserted, by `TestHookPlanOwnershipIsolationRoundTrip`. The write
  stays simulate-only; `AuthorizeRealWrite` is unchanged. **Gap C (inventory):**
  `Adapter.Inventory` gained a `*HostView` parameter (matching `Discover`'s existing pattern) across
  every implementation (Codex, Claude, the Loomwright and Wayfinder conformance fixtures); each
  adapter's new bounded `ScanHostInventory` reads Codex's `config.toml` `[mcp_servers.*]` tables and
  Claude's `settings.json` `enabledPlugins`/`mcpServers` keys through `HostView.ReadConfigProbe`,
  mapping results onto the existing closed inventory-graph vocabulary with no new node/edge kind;
  zero configured components still reports `Completeness: "unknown"`, never a fabricated empty
  snapshot. ADR 0014 records the decision to close all three gaps via an iterative developer/
  reviewer/fixer agent loop; `reports/session-11-reconciliation.md` documents full verification
  evidence. `go build`/`go vet`/`go test ./...` (17 packages) and every
  `python3 scripts/validate_*.py` script pass, as does
  `TestHookPlanOwnershipIsolationRoundTrip` and `python3 scripts/run_go_tests.py`'s pinned-Linux
  suite (including `-race`). **What was not re-executed this session, per ADR 0014's exit gate:**
  the live manual pass (rebuild the Docker image, connect a real Codex/Claude Code CLI session,
  confirm dashboard activity) was not re-run here — this reconciliation pass verified the mechanism
  via code inspection and the existing automated test suites, not a fresh live Docker session. A
  The formerly open real-hook gap was closed on 2026-07-26: adapter hook output now enters the
  canonical safe-field path directly with `hook_http` lineage instead of being re-decoded as
  fixture-agent JSON, and uniquely inventory-resolved `component.*` facts project idempotently to
  `component_lifecycle_events`. Universal component success remains explicitly unsupported without
  a component-specific terminal contract; a Codex App Server typed-event bridge remains future
  work. `claude.transcript`, Session 07b (Gemini/Cursor), the `kansoku
  doctor`/`configure` CLI, live-CLI canary automation and the DB-restart/failed-restore scenarios
  (ADR 0011) remain out of scope, unchanged.
- **12 — implemented/P0 (2026-07-26; live gate green):** Reliability has the unified
  metadata-only incident/quarantine workbench, signed keyset pagination, profiles, typed debug
  bundles, detector-separated triage, durable supported-event/audit recovery and aggregate-
  preserving detail retention. Real PostgreSQL migration/replay/recovery tests, production
  upgrade/restart, repeated isolated restore verification, query plans, ten-sink privacy and
  headless-browser checks pass. The measured production store retained all legacy rows and has no
  orphan occurrences or non-legacy aggregate/detail mismatches.
- **13 — implemented/P0 (2026-07-26; live gate green):** the generic adapter-owned
  `EvidenceBridge`, a non-agent-shaped conformance fake and the optional version-pinned Codex App
  Server bridge feed the canonical safe assertion path without core brand branching. Migration
  0008 adds non-rewriting installation/session/model attribution; agent profiles expose exact
  per-model populations and independent source health. Real PostgreSQL cross-lane reconciliation,
  production restart, ten-sink bridge privacy, browser, backup and repeated restore gates pass.
- **14 — planned/P1 (approved 2026-07-26):** skill analytics moves from one false universal funnel
  to availability/runtime/optimization planes; optimization stays unsupported until Session 20.
  Profiles contain evidence and file-tree metadata, not file contents.
- **15 — planned/P1 (approved 2026-07-26):** MCP inventory, protocol connections and calls become
  independent evidence contours with server/tool profiles and a privacy-safe live no-op canary.
- **16 — planned/P2 (approved 2026-07-26):** plugin bundle graph and child usage reuse stabilized
  skill/MCP relations. There is no universal plugin-success metric.
- **17 — planned/P2 (approved 2026-07-26):** Kansoku measures its own bounded operational
  time-series separately from agent ingress.
- **18 — planned/P3 (approved 2026-07-26):** design-system/browser regression and the complete
  opt-in transient skill/plugin content viewer land together; no partial viewer ships earlier.
- **19 — deferred/P4:** enable/disable and assisted incident remediation require a new threat model
  and accepted read-only reconciliation evidence from Sessions 12–18.
- **20 — research backlog:** opportunity detection requires deterministic eligibility semantics,
  privacy proof and false-positive evaluation before any production implementation.

## Dependency graph

```text
01 -> 02 -> 03 -> 04 -> 05 -> 06 -> 07 -> 08 -> 09 -> 10 -> 11
                   \-----------------------> analytics/UI fixtures
                                 \-> 07b (Gemini/Cursor, backlog, non-blocking)

11 -> 12 -> 13 -> 14 -> 15 -> 16 -> 17 -> 18 -> 19
                         \-----------------------> 20 (research backlog)
```

Sessions 06 and 07 могут делить parser fixtures, но не должны идти параллельно до стабилизации
Adapter SDK в Session 05. Frontend spikes допустимы раньше, однако production UI строится только
после фиксации semantics и completeness states. 07b остаётся валидной будущей сессией, но больше не
блокирует основной путь. Sessions 12–15 выполняются строго последовательно: Incident Workbench
должен принять failures новых bridges; Agent Evidence Bridge должен стабилизировать attribution до
Skills/MCP; MCP предшествует Plugins, потому что plugin graph может владеть MCP servers/tools.
Sessions 19–20 не разрешены как скрытая часть более раннего implementation scope.

Самодостаточный implementation handoff для следующего агента, который последовательно закрывает
Sessions 12–15, находится в
[`prompts/session-12-to-15-implementation.md`](prompts/session-12-to-15-implementation.md).

## Cross-session deliverables

Все сессии поддерживают общие артефакты:

- ADR log с rejected alternatives;
- versioned canonical event schemas;
- sanitized fixture corpus по agent/version/source;
- adapter capability and compatibility matrix;
- metrics catalog и formula registry;
- privacy regression corpus;
- reconciliation and coverage reports;
- release notes с известными gaps.
