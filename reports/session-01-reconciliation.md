# Session 01 reconciliation report

- Session: 01 — Product contract and SLO harness
- Date: 2026-07-21
- Result: automated exit gate passed; product semantics are ready for Session 02
- Public support claims: blocked until bounded adapter fixtures, privacy tests, audits, canaries and
  two independent human classification reviews exist

## Delivered contract

| Acceptance item | Evidence | Result |
|---|---|---:|
| Canonical glossary and ambiguous-state separation | shared state registry; lifecycle/value fixtures; dashboard checks | pass |
| Capability IDs, full adapter×capability baseline and support governance | verified canonical artifact bytes plus typed exact-bound receipts and mutations | pass |
| Prioritized metric-specific formulas, dimensions, lineage and completeness | 34 independent version locks, strict evaluators and normalized-record fixtures | pass |
| SLO targets, evidence scopes, exclusions and runnable SLI queries | measured/excluded/missing scopes plus ineligible/incomplete/unauthorized mutations | pass |
| Every planned route/panel maps to metrics and user questions | `contracts/dashboard.yaml` | pass |
| Sprint, cost, retention, export, scope and non-goal decisions | `contracts/product.yaml` | pass |
| Measured backend/database/frontend baseline | `benchmarks/session-01/`; ADR 0001 | pass |
| Official agent interface refresh | `SOURCES.md`, retrieved 2026-07-21 | pass |

`python3 scripts/validate_contracts.py` passes. The 24-test unittest suite covers registry
references; independently locked metric populations/expressions, exact evaluator schemas and safe
records; filtering, deduplication, preclassified `in_interval` selection, exclusions, ordering and continuous quantiles;
invalid inferred promotion; strict typed public-support receipt and version mutations; complete
adapter×capability coverage; canonical unsupported/not_observed/redacted/unknown/numeric_zero
states; canonical evidence bytes, hashes and path confinement; adversarial SLO evidence; benchmark
artifact hashes; idempotent replay; provisional query/bundle budgets; and concrete privacy-safe
durable spike rows.

## Exact benchmark rerun evidence

`python3 benchmarks/session-01/run.py` completed a successful pinned `full_run` from
`2026-07-21T17:01:20Z` through `2026-07-21T17:01:48Z`. The manifest retains all three successful run
records and the final artifact hashes:

- backend: `6cc71d2d8aadd0ae42d06f0f8bdcf8b6ae4dd606efa283676812978242762c41`;
- database: `89244d87f98edc3460fa7eeb1c8b3f443fefd2624bc8f6c78e277e9e8e19c99b`;
- frontend: `7ce1ce495667fa0f9fde50dfdfdfff86327a3641757f261fd5cc67774564f545`.

The manifest is authoritative if these hashes change in a later rerun. Go, Python, PostgreSQL and
the temporary Alpine volume-owner helper are immutable `@sha256` inputs; frontend dependencies are
locked by package-lock hash `a8448066fa4e7fcf5e94ae9f8eb8f9e7ad1bf69498d86a17d61c3cd1ad6476e8`.
Cleanup inspection found no remaining `kansoku-s01-*` container or volume.

## Metric and requirement reconciliation

- `KAN-COL-001/002`: durability, idempotency, gap and completeness semantics have stable IDs, SLOs
  and fixtures; production ingestion is intentionally deferred to Sessions 03–04.
- `KAN-PRV-001`: the target is zero across every durable/output sink. Each backend implementation
  retained 420 rows matching the exact five-field allowlist. Ten content-bearing aliases/keys were
  rejected, recursive key/value scans found none of five unique canaries, and two concrete retained
  sanitized rows per implementation are recorded. The full cross-sink canary suite still belongs to
  Session 02.
- `KAN-ADP-001`: metrics and panels depend only on capability IDs. No core contract branches on an
  agent name; agent rows are bounded evidence claims.
- `KAN-MET-001`: each registered metric declares formula version, stable population ID, population,
  unit, dimensions, provenance, completeness and exactness. The validator recomputes a semantic
  SHA-256 over population, expression, exact evaluator ID/parameters, fixture policy and ratio
  operands, then matches both the deterministic fixture and an independent version lock. Existing
  locks become append-only after their first trusted commit. All 34 formula versions exercise
  normalized-record population/filter/dedupe, selection by a preclassified `in_interval` boolean,
  exclusion semantics and ordering where relevant. Ratios declare distinct numerator/denominator;
  p95 uses PostgreSQL `percentile_cont` linear interpolation. This proves normalized aggregation,
  not timestamp-to-interval classification, exact `[from,to)` boundaries, raw-event parsing,
  lineage derivation or production SQL; those remain Sessions 03–04 gates.
- SLO required scopes resolve explicitly to `measured`, `excluded` or `missing`. Only an eligible,
  complete, non-excluded row can produce `measured`; an authorized whole-scope exclusion is exposed
  with a count as `partial` plus a failing gate, while missing/ineligible evidence is `unknown` plus
  a failing gate. The backup scope of the zero-raw-content SLO is mutation-tested exactly.
- Public Supported/Beta evidence uses strict ordered SemVer-core ranges and one typed passing receipt
  per required evidence kind. Every artifact/fixture ID resolves to repo-bounded canonical JSON
  bytes under `tests/fixtures`: paths reject traversal and symlink escape, the validator recomputes
  SHA-256 and payload kind, and the ID must equal that content address. Receipts and both distinct
  approved human reviews must bind the exact adapter/capability/range tuple; reviews also cite
  verified classification fixtures and the exact receipt set. Synthetic sanitized files exercise
  this mechanism but are not public adapter evidence or support claims.

- `KAN-UX-001`: every route uses the same half-open time/sprint/filter/comparison contract and all
  panels use the canonical display registry: complete, partial, degraded, unsupported,
  not_observed, redacted, unknown and numeric_zero.
- `KAN-OPS-001` and `KAN-AUD-001`: recovery/audit semantics and SLOs are fixed, but implementation
  and soak/fault proof remain Sessions 08–09 gates.

## Final verification

- `python3 scripts/validate_contracts.py --json` — pass with an empty error list. Because this is the
  uncommitted bootstrap, `HEAD` has no formula lock to compare; the same gate also passes explicitly
  with `--formula-history-ref none`.
- `python3 -m unittest discover -s tests -v` — 24 tests passed (19 contract, 5 benchmark).
- `npm run build:echarts` and `npm run build:uplot` from `benchmarks/session-01/frontend` — both
  production builds passed; the accepted ECharts build still emits its documented chunk-size
  warning and remains under the provisional 250 KiB gzip analytics-chunk budget.
- `npm audit --audit-level=low` from the same directory — zero vulnerabilities.
- `git diff --check` — pass.
- `docker ps -a --filter 'name=kansoku-s01-'` and
  `docker volume ls --filter 'name=kansoku-s01-'` — no matching temporary resources.

## Privacy, retention and external-state review

- No agent configuration, transcripts, user data or telemetry were read or changed.
- All benchmark payloads and rows are deterministic synthetic data. Backend durable rows contain
  only safe counts/times/route/fingerprint; raw bodies are transient.
- No raw prompt, response, source, tool input/output, environment value, credential, unredacted
  path, hostname, serial number or hardware UUID is present in generated result artifacts.
- Official documentation review exposed a high-risk default: Gemini telemetry currently lists
  `logPrompts=true`. Future installation must preview an explicit `false` diff and still sanitize at
  ingress. Codex telemetry is user-level only; Claude detail/body gates remain off.
- Default retention is bounded: normalized metadata 365 days, sanitized envelopes 7 days,
  metadata-only quarantine 30 days, rollups 1095 days, operational SLO samples 90 days, audit and
  installer records 365 days, backups 7 daily plus 4 weekly.
- The five-year 1M/day linear projection is roughly 166 GB (SQLite) or 375 GB (PostgreSQL) before
  optimization. This is not an accepted storage budget; Session 04 must prove partitions, retention,
  rollups and disk forecasting.

## Resource overhead review

- Go spike: 2.37 MB final image, 5.61 MiB idle RSS point sample and 8.15 MiB load RSS point sample.
- Python spike: 18.17 MB image, 15.43 MiB idle RSS and 15.44 MiB load RSS point samples.
- PostgreSQL 18.4 image: 117.8 MB; sample storage about 205 B/event versus SQLite about 91 B/event at
  100k rows.
- ECharts prototype: 238,198 B gzip; uPlot: 83,311 B gzip. ECharts is accepted with route lazy-load
  and a provisional 250 KiB gzip analytics-chunk budget.
- Local npm build cache is ignored and measured at about 103 MB; built prototype output is under
  1 MB and ignored. These are development artifacts, not runtime dependencies.
- No `kansoku-s01-*` temporary container or volume remains. Reproducible tagged benchmark caches do
  remain locally (`docker image ls`: about 8.02 MB Go and 76.6 MB Python); they contain only built
  spike code and can be removed independently of repository or agent data.
- The resolved 57-package frontend dependency graph reports zero known npm-audit vulnerabilities on
  the retrieval date; this is not a substitute for Session 10 SBOM/release scanning.
- Docker CPU values are point samples. The required 24-hour idle and seven-day soak measurements
  have not been claimed.

## Backup/restore behavior known at this phase

The product contract requires 7 daily and 4 weekly backups, version/checksum/privacy manifests and
isolated restore tests. No database volume or user backup was created in Session 01. PostgreSQL 18
changed the official container volume boundary to `/var/lib/postgresql`; Session 09 must pin and
upgrade-test that layout. Until Session 09 passes, recovery is specified but not implemented.

## Residual risks

1. Two independent humans have not signed the lifecycle classification exercise. ADR 0002 keeps
   this public Supported/Beta governance gate blocked; automated checks do not replace approval.
2. Agent documentation is frequently unversioned. Claude docs include behavior gates through
   2.1.216, while the locally observed runtime is 2.1.197 and unverified by fixtures. No adapter is
   above Experimental and no adapter runtime fixture/canary exists yet.
3. The backend spike decodes bounded OTLP JSON structure, not production protobuf/gRPC, unknown
   schema quarantine, or adversarial compression/depth cases.
4. Database samples cover 10k/100k one-day loads. PostgreSQL and SQLite concurrency workloads were
   not identical, and the 1M/day multi-year profile was projected rather than materialized.
5. Frontend accessibility evidence is static. Browser, assistive-technology, visual-state and
   interaction performance tests remain open.
6. Binding idle CPU/RSS, restart durability, seven-day soak, migrations and backup/restore are not
   proven by Session 01 spikes.
7. The current formula lock is in deterministic bootstrap mode until its first reviewed commit.
   Tests prove coherent current-file rewrites fail against a supplied historical lock, but no
   repository-local validator can resist a malicious simultaneous rewrite of validator, contracts,
   tests and Git history. Protected review/CI and an explicit trusted merge-base remain the external
   trust root after commit.

## Exit decision

Session 02 can write privacy/security tests without resolving product semantics: required states,
support claims, source lineages, metric formulas, SLOs, retention and UI ownership are explicit and
machine-checked. This is the automated sequencing gate defined by ADR 0002, not a human sign-off.
Kansoku must not make a public Supported/Beta adapter claim until the bounded privacy, replay,
passive-audit, canary/end-to-end and two-independent-human evidence gates above close.
