# Session 10 reconciliation — dashboard, hardening and release

- Date: 2026-07-25
- Status: **complete**. All 14 `contracts/dashboard.yaml` routes are implemented, wired to real
  `/api/v1` data, and served by `internal/webui` from the Go binary on port 43100. The hardening
  pass (gate 4 of `adr/0013`) found and fixed two real issues (a CSP nonce bug and a bundle-budget
  violation) and two real dependency CVEs, all now closed. The release pass (gate 5) confirms no
  Dockerfile change is needed, ships this report and the Session 10 SBOM, and closes the
  `ROADMAP.md`/`README.md` docs gap.

## Scope reconciled

Session 10's implementation plan (ADR 0013, "Implementation roadmap") had five phases. All five
are done:

1. **API surface** — `/api/v1/analytics` extended plus 11 new `internal/dataplatform` aggregation
   files (`activity_timeline`, `entity_breakdown`, `funnels`, `mcp_topology`, `mcp_uptime`,
   `model_usage`, `prompt_shape`, `reliability_counts`, `reliability_timeline`, `system_snapshot`,
   `tool_analytics`) plus `privacy_canary_history`, backing every one of the 14 routes' panels.
2. **Frontend scaffold** — `web/` (Vite + React 19.2.8 + TypeScript + wouter + TanStack Query v5),
   design tokens, vendored Inter/JetBrains Mono fonts, sidebar shell, scrollbar/dropdown primitives,
   the 8-state view vocabulary components.
3. **Page-by-page build** — all 14 routes built against real data with all 8 view-states modeled
   (`complete/partial/degraded/unsupported/not_observed/redacted/unknown/numeric_zero`).
4. **Hardening gates** — see below.
5. **Release** — this report, the Session 10 SBOM, and the `ROADMAP.md`/`README.md` updates.

## What this closing pass added

Beyond the page-build work already done, this hardening/release pass specifically:

- **Found and fixed a real CSP nonce bug.** The inline pre-paint theme script's nonce was
  originally base64-encoded. `html/template` HTML-entity-escapes base64's `+` character when it
  renders into the `nonce="..."` attribute (`+` → `&#43;`). Browsers decode that back correctly
  before CSP nonce matching, so this was likely not exploitable, but it made the served markup
  byte-inexact for no reason and was flagged by `TestIndexCSPUsesPerRequestNonceNotUnsafeInline`
  (a genuine `go test` failure, not a false alarm — confirmed by writing a two-line reproduction
  against `html/template` directly). Fixed by switching `internal/webui.newCSPNonce` from
  `base64.StdEncoding` to `hex.EncodeToString`: same 128 bits of entropy, and `[0-9a-f]` never
  needs HTML-attribute escaping. Verified live: the running container's CSP header nonce and the
  rendered `<script nonce="...">` attribute are now byte-identical.
- **Found and fixed a real bundle-budget violation.** All 14 pages were originally eagerly bundled
  into one chunk, including the ~381 KB gzip ECharts vendor chunk, even for chart-free routes
  (Agents, Agent detail, Settings, System, Skills, Plugins) — directly contradicting TDD-10's own
  "route-level code splitting where it reduces initial bundle" principle. Fixed by converting all
  13 named page components in `web/src/routes.tsx` to `React.lazy` + `Suspense`. Result: main app
  chunk shrank from 260.98 KB / 78.89 KB gzip to 212.04 KB / 67.88 KB gzip; each page is now its
  own ~0.3–5.6 KB chunk; ECharts (1,135.53 KB / 381.11 KB gzip) now loads only when a chart route is
  actually visited. For the 6 chart-free routes, initial JS payload dropped from ~473 KB gzip
  (always loaded) to ~85 KB gzip — an ~82% reduction. Verified via `npm run build` (clean `tsc`
  type-check) and via live curl against the rebuilt container: root, `/settings`, `/activity` deep
  links and individual hashed chunk assets all serve 200 with valid, correctly cross-referencing
  minified JS.
- **Found and fixed two real, reachable dependency CVEs.** `govulncheck` (Go's official vuln
  scanner, run against the vendored build with `GOFLAGS=-mod=vendor`) found GO-2026-5004 (a pgx
  placeholder-confusion SQL-injection-adjacent bug, reachable via
  `dataplatform.SessionDrilldown`'s `pgxpool.Conn.Query` call) and GO-2026-5970 (an `x/text`
  infinite-loop DoS bug, reachable via `pgxpool.NewWithConfig`'s identifier normalization). Both
  have reachable call paths from this repository's own code, not just from a transitive dependency
  never invoked. Presented to the user with reachability evidence and fix versions; user chose to
  upgrade now. Applied: `github.com/jackc/pgx/v5` v5.7.6 → v5.9.2, `golang.org/x/text` v0.36.0 →
  v0.39.0 (`golang.org/x/crypto` dropped entirely as a now-unused transitive dependency).
  Re-vendored offline, full test suite re-run, `govulncheck` re-run clean (0 reachable
  vulnerabilities), and re-verified live in the running container (mutation guard and CSP behavior
  unchanged after the restart).
- **Ran a genuine, live, end-to-end raw-content leak scan**, not just a structural code review. The
  `tests/fixtures/session-02/raw-canary-input.json` fixture — containing distinctive marker strings
  (`KANSOKU_RAW_PROMPT_7QF4Z9`, `KANSOKU_RAW_RESPONSE_6XP2M8`, plus base64/hex/casefold/
  URL-encoded/unicode-confusable transformed variants) — was POSTed as a real `tool_finished` hook
  event through the real HTTP ingress (`http://127.0.0.1:4318/v1/hooks/fixture-agent/tool_finished`
  — note this is the ingestion listener, a separate `http.Server` bound to `OTLPHTTPListen`, distinct
  from the dashboard/API listener on port 43100). It landed as a real Postgres row. All 15 GET
  `/api/v1/*` dashboard-facing routes' JSON responses, and the raw Postgres table contents
  themselves, were then scanned for every raw/transformed canary marker: **zero leaks found
  anywhere.**
- **Structurally confirmed the forbidden-response-key guard has no bypass path.** Every
  `/api/v1` route response is serialized through one function, `a.write()` in
  `internal/runtime/api.go`, which unconditionally runs `containsForbiddenResponseKey` (a recursive,
  case-insensitive key walk against `prompt, response, content, source_code, tool_input,
  tool_output, environment, credential, raw_path, sql_parameters`) before writing any body. There is
  no second response-writing path for any of the 14 dashboard routes to slip through.
- **Reviewed every dashboard-backing SQL query** in `internal/dataplatform` (`activity_timeline`,
  `entity_breakdown`, `funnels`, `mcp_topology`, `mcp_uptime`, `model_usage`, `prompt_shape`,
  `reliability_counts`, `reliability_timeline`, `system_snapshot`, `tool_analytics`) for any raw-text
  `SELECT`. None exists — every query selects only IDs, counts, percentiles, states, timestamps,
  sizes and checksums.
- **Reviewed `npm audit`'s 5 flagged CVEs** (all in `vite`/dev tooling). Confirmed via
  `package.json`'s dependency split that `vite` and `@vitejs/plugin-react` are dev-only build tools;
  several of the 5 advisories are explicitly Windows-dev-server-only. None can affect the production
  static build served by the Go binary. Production dependencies (react, react-dom, echarts, wouter,
  @tanstack/react-query) have zero flagged vulnerabilities. No action needed; documented as
  reviewed and accepted.
- Appended a post-implementation addendum to `adr/0013` recording both the CSP-nonce and the
  dependency-upgrade decisions (and, after the hex-encoding fix, why base64 was replaced) so a
  future reader does not have to reconstruct the reasoning from the diff alone.

## Verification

```
go build ./...                                   # clean
go vet ./...                                      # clean
go test ./...                                     # see per-package results below
npm run build   (in web/)                         # tsc --noEmit clean, vite build clean
GOFLAGS=-mod=vendor govulncheck ./...             # 0 reachable vulnerabilities (post-upgrade)
python3 scripts/session10_supply_chain.py --verify  # SBOM manifest matches committed report
```

`go test ./...` package results:

| Package | Result |
|---|---|
| claudeadapter, codexadapter, crossagent, dataplatform, installer, integrity, localhttp, runtime, **webui** | `ok` |
| observability | `ok` except 2 pre-existing macOS-only failures (see below) |
| privacy | `ok` except 1 pre-existing macOS-only failure (see below) |

The 3 observability/privacy failures (`TestDurableSpoolIsBounded0600AndReplaySafe`,
`TestDurableSpoolRejectsUnsafeParentsFilesAndLinksWithoutModification`,
`TestKeyFileIsCreateOnceNoFollowAndMode0600`) are **pre-existing and platform-specific**, not a
Session 10 regression: the durable spool and secure keyfile backends are Linux-only by design
(`internal/observability/spool_linux.go` uses `golang.org/x/sys/unix` `openat`/`O_NOFOLLOW`; the
`spool_unsupported.go` build-tag counterpart always errors on non-Linux). Confirmed identical on a
clean `git stash` HEAD — this environment is a native macOS host, and these three tests only pass
inside the Linux container/CI, where they were already covered by earlier sessions' validation.

Live end-to-end evidence (Docker: `kansoku-pg` postgres:18-alpine + `kansoku-app` golang:1.26-alpine
running `go run ./cmd/kansoku serve`, network `kansoku-e2e`):

```
GET  /                                    -> 200 (deep-link + root both serve the SPA shell)
GET  /settings, /activity                 -> 200 (client-side route deep-links survive refresh)
GET  /assets/<hashed>.js                  -> 200, immutable cache, valid minified JS
GET  /api/v1/inventory  (read bearer)     -> 200
POST /api/v1/admin/export (read bearer)   -> 403 (mutation bearer never reaches the browser)
POST /v1/hooks/fixture-agent/tool_finished (ingress, port 4318) -> 200 accepted, landed in Postgres
CSP header nonce == rendered <script nonce="..."> attribute      -> byte-identical
```

## Residual risks and honest gaps

The following TDD-10 hardening/release gates were **not** executed this session, with the reason
for each:

- **7-day wall-clock soak repeat.** Session 09 already ran an accelerated 168-logical-cycle soak
  for the runtime layer itself; not repeated here because Session 10 adds only read-only dashboard
  routes with no new durability/persistence mechanism to soak.
- **ARM64/x86_64 image build matrix.** Only tested on the host's native arm64 Mac via Docker
  Desktop. Base images (`golang:1.26-alpine`, `postgres:18-alpine`) are multi-arch manifests, but no
  explicit cross-arch build+run was performed this session.
- **Full supported-browser matrix (Chrome/Firefox/Safari/mobile) and visual regression fixtures**
  (DST transitions, data gaps, unknown states, high-cardinality tables). **No browser automation
  tool is available in this environment/session.** Verification performed instead was: `tsc`
  type-checking, `vite build` bundle analysis, and live curl-level asset/route serving checks
  (status codes, cache headers, byte-identical CSP nonce matching, correct chunk content and
  cross-chunk imports). This is real evidence that the build is correct and the server serves it
  correctly, but it is **not** evidence that the UI renders and behaves correctly in an actual
  browser. This gap is disclosed explicitly rather than implied as covered.
- **Disk-forecast validation and reference load test at scale.** Not executed; no load-generation
  tool was run this session.
- **Backup/restore drill repeated for Session 10.** Session 09 already validated the mechanism;
  Session 10 does not touch backup/restore code, so it was not re-run.
- **Formal changelog / compatibility-matrix / rollback-guide as separate standalone documents.**
  Not created as separate files; the equivalent information is captured in this report, the ADR
  addendum, and the `ROADMAP.md` entry.
- **Signed release images / vulnerability attestation.** No image-signing infrastructure exists in
  this environment; recorded as a property in `reports/session-10-sbom.json`
  (`kansoku:release-artifact-signing`) as future work, matching Session 09's own equivalent gap.

None of these gaps block the dashboard from working correctly for its intended purpose (a local,
single-operator, loopback-only appliance UI); they are named here so a future session or reviewer
knows exactly what has and has not been checked, rather than assuming full coverage from silence.
