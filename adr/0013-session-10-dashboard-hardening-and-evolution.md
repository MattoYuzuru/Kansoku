# ADR 0013 — Session 10 frontend, design system and release

- Status: accepted for Session 10 implementation
- Date: 2026-07-24
- Owners: Kansoku frontend/dashboard
- Supersedes: none (first UI-facing ADR)
- Extends: ADR 0001 (technology baseline / Session 01 frontend spike), and confirms the frontend
  and hardening scope described in `Engineering Proposal/10-dashboard-hardening-and-evolution.md`
  and `Technical Design Document/10-dashboard-hardening-and-evolution.md`

## Context

Sessions 01-09 built the full backend (privacy boundary, ingestion, data platform, adapters,
integrity, local runtime) with no web UI. Session 10 is the first and only session that ships one.
The Session 10 Proposal and TDD already lock the information architecture, route list, response/
view-state model, per-page visual specs and hardening/release gates, and are registered
machine-readably in `contracts/dashboard.yaml` (14 routes) and `contracts/metrics.yaml`. They
deliberately leave two things open for this ADR: the exact frontend framework/library choices
("final ADR compares bundle/accessibility/performance and may choose a smaller framework"), and
the entire visual design system (colors, typography, icons, motion, sidebar behavior) — none of
which either document specifies beyond "quiet, dense and deliberate... no external fonts/CDNs."

ADR 0001's Session 01 spike already measured React 19.2.8 + Apache ECharts 6.1.0 at 238,198 B
gzip against a provisional 250 KiB analytics-chunk budget, and evaluated uPlot as an alternative
(rejected as a sole library, kept as a possible future optimization for one specialized chart).
That evidence is reused here rather than re-litigated.

A research pass plus a two-stage creative process (brainstorm of three concrete directions, then
one detailed design-system specification) ran to fill exactly the gaps above; the full token/
component-level specification lives in
`Technical Design Document/design-system-tokens.md` and is the source of truth for exact values.
This ADR records only the decisions and rejects the alternatives considered.

The same research also found that the current `/api/v1` surface (`inventory`, `analytics`,
`health`, `incidents`, `completeness`, `operations/jobs`, plus admin mutation routes) is a thin,
generic, privacy-hardened envelope. It does not yet expose the dimensioned, per-agent/
per-component/per-source data (lifecycle funnels, co-activation matrices, per-entity tables,
reliability timelines, MCP/tool analytics) that the 14-route dashboard contract requires. This is
named as a decision below rather than left to be discovered mid-implementation.

## Decision

1. Frontend stack is confirmed, not re-opened: TypeScript + React 19.2.8, Apache ECharts 6.1.0
   for charts, per ADR 0001's measured spike. uPlot is not adopted now; it remains a targeted
   future optimization only if a specific chart's ECharts cost proves too high in practice.
2. Router: **wouter** (~2 KB gzip) over React Router/TanStack Router — the 14-route, mostly-flat
   navigation (one nested `/agents/:id`) does not need loaders or nested-layout routing, and the
   analytics chunk is already close to its provisional budget. Server-state cache: **TanStack
   Query v5** (core only, no devtools in the production bundle) to implement the TDD's
   loading/stale-while-revalidate view-state requirement without hand-rolled cache invalidation.
3. Styling is plain CSS custom properties (no CSS-in-JS runtime, no Tailwind) — matches the
   Proposal's "internal token/component layer" wording, keeps every style statically inspectable,
   and avoids either a build-time class-generation step or client-side style injection. Full token
   values (color/spacing/radius/typography) are defined once in
   `Technical Design Document/design-system-tokens.md` §1 and consumed everywhere else.
4. Visual identity is a "quiet instrumentation" aesthetic: warm near-black dark theme, warm-paper
   light theme, hairline rules instead of cards/shadows/gradients/glass, and a softened
   Lakers-inspired purple/gold pair used strictly as accents (never as base-UI color), so the
   light/dark toggle stays a strongly felt change. Typography is Inter (UI) + JetBrains Mono
   (tabular data, table/section headers), both self-hosted OFL-licensed WOFF2 subsets — no CDN, no
   Google Fonts. Exact hex/size values: design-system-tokens.md §1, §1.5.
5. Left sidebar collapses between a 248px expanded state (logo + "KANSOKU" wordmark + a reserved
   28×28px placeholder brand-mark chip + plain-text grouped section list, no icons) and a 60px
   icon-only rail (Tabler Icons, MIT-licensed, self-hosted/inlined SVG). The 14 `dashboard.yaml`
   routes are grouped into 7 nav entries to stay scannable at this density (grouping table:
   design-system-tokens.md §2). Auto-collapses at the ≤1024px breakpoint; manual override
   persists locally.
6. All 8 of `contracts/dashboard.yaml`'s formal `view_states` (complete, partial, degraded,
   unsupported, not_observed, redacted, unknown, numeric_zero) get a distinct glyph+shape+label
   treatment (design-system-tokens.md §7) so color is always reinforcement, never the sole cue,
   in both themes and for colorblind users.
7. No native browser chrome anywhere: scrollbars are CSS-only styled native scrollbars (not a JS
   overlay — zero added JS/DOM, keeps native momentum scrolling), and every `<select>`/dropdown is
   a custom ARIA `combobox`/`listbox` component with full keyboard operability (arrows, Home/End,
   Enter, Escape, typeahead) — required for the global query model's range/timezone/dimension
   filters.
8. All motion is CSS transform/opacity, ECharts' own animation flag, or SVG stroke-dashoffset — no
   animation library. A single global `prefers-reduced-motion` rule collapses every one of the 11
   named motion moments (design-system-tokens.md §4) to its instant final state.
9. An accent-only "appearance" playground lives on `/settings`: only the purple/gold accent
   tokens are user-adjustable (the neutral base stays fixed to protect the guaranteed AA text
   pairs); choices persist to `localStorage` only (`kansoku.appearance.v1`), never a network call;
   4 curated AA-verified presets are offered alongside free adjustment, and a manually chosen
   value that would fail 4.5:1 contrast is blocked with the nearest safe shade offered instead.
10. Document `<title>` updates per route as `Kansoku · {route.title}`, sourced directly from
    `contracts/dashboard.yaml`'s own `title` field rather than a hand-maintained duplicate list.
    Favicon/manifest assets (ico/png/svg/apple-touch-icon/maskable/webmanifest) are reserved as
    placeholder files carrying a temporary "K" mark, to be replaced with real brand art later
    without any component/token-layer code change.
11. Responsive policy: desktop-first with one hard breakpoint at 1024px (sidebar auto-collapse,
    single-column reflow, sticky-first-column horizontal-scroll tables). Phone width (<640px) is
    an explicit stretch goal, not a Session 10 gate.
12. The current `/api/v1` surface must be extended within Session 10, not deferred: where a
    metric already exists in `contracts/metrics.yaml` with the needed dimensions, this is done by
    extending `/api/v1/analytics`'s accepted `metric_family`/`dimension_scope` values and adding
    the corresponding `internal/dataplatform` aggregation queries (per-agent/per-model breakdowns,
    component lifecycle funnels, co-activation, reliability coverage timelines). A new dedicated
    route is added only where no existing metric/dimension can represent the panel at all (e.g. an
    MCP server tree). Per-panel API mapping is the first implementation-plan phase below, not
    resolved by this ADR.

## Consequences

- Session 10 has both a backend-API-surface workstream and a frontend-build workstream; the exit
  gate cannot be met by frontend work alone.
- Design tokens are locked enough to start component implementation immediately; any deviation
  the design-system spec didn't anticipate should return to this ADR rather than being improvised
  ad hoc in a PR.
- Favicon/brand-mark placeholders are explicitly temporary; replacing them later is a pure asset
  swap with no token/component-layer code change.
- Every accessibility/performance/security hardening gate from the Proposal/TDD is unchanged and
  still applies in full before Session 10 can close.

## Rejected alternatives

- **React Router / TanStack Router:** heavier than wouter for 14 mostly-flat routes with no
  loader/nested-layout requirement beyond one agent-detail page.
- **Tailwind CSS or CSS-in-JS (styled-components/emotion):** either adds a build-time
  class-generation step or client-side runtime style injection; plain custom properties keep the
  token layer fully static/auditable and give the exact hairline/spacing control the "quiet,
  dense" aesthetic needs.
- **A glassmorphism/heavy-blur visual trend** (considered during brainstorming): rejected for GPU
  cost on modest local hardware and for reading as decorative rather than instrumentation-grade.
- **Leaving the `/api/v1` surface gap for implementation to discover organically:** rejected —
  it would let frontend work start against an API that cannot serve the committed IA, discovered
  only late in the session.
- **A JS-based custom scrollbar overlay:** rejected in favor of CSS-native scrollbar styling,
  which needs no extra DOM/JS and keeps native momentum scrolling.

## Implementation roadmap

1. **API surface.** Audit every `contracts/dashboard.yaml` panel's `metrics`/`question_ids`
   against `contracts/metrics.yaml` and the real rollup tables in `internal/dataplatform`; extend
   `/api/v1/analytics` dimension/metric-family support and add aggregation queries for funnels,
   matrices and per-entity tables; add a new route only where no existing metric/dimension can
   represent the panel.
2. **Frontend scaffold.** Repository layout for the frontend, build tooling wired to embed static
   assets into the Go binary/image per the TDD, the design-token CSS file, vendored fonts/icons,
   base layout shell (sidebar + route outlet + theme provider), scrollbar/dropdown primitives, the
   shared status-vocabulary and percentage components.
3. **Page-by-page build** across the 14 routes per `contracts/dashboard.yaml` wireframes, each
   wired to real (extended) API data with all 8 view-states modeled, using the visualization
   vocabulary already specified in the Proposal (heatmaps, funnels, percentile bands, etc.).
4. **Hardening gates:** independent security/privacy review, fuzzing/adversarial payload tests,
   accessibility (automated + manual WCAG AA, keyboard, screen reader) and browser-compatibility
   matrix, performance/load/soak vs SLOs, migration/restore drills, adapter-version-matrix and
   unsupported-claims audit, fresh-install docs test, no-silent-zero UI review.
5. **Release:** dependency/SBOM/image/signing plan, checksums, changelog, compatibility matrix,
   rollback guide, documented known gaps, and the closing `ROADMAP.md` "10 — complete" entry plus
   reconciliation report and SBOM, matching Sessions 01-09's closing pattern.

## Post-implementation addendum (hardening pass, 2026-07-25)

Two decisions were made while executing gate 4 (hardening) that this ADR did not anticipate and
records here for the same reason every other session's addendum exists — so a future reviewer does
not have to reconstruct *why* from the diff alone:

- **CSP `script-src` uses a per-request nonce instead of `'unsafe-inline'`.** `internal/webui`'s
  `index.html` was already rendered per request (`html/template`) to inject the live read/CSRF
  bearer tokens, so the one inline pre-paint theme script (the no-flash-of-default-theme snippet)
  can carry a fresh `crypto/rand`-sourced 128-bit nonce on every response instead of a blanket
  `script-src 'unsafe-inline'` allowance. `style-src 'unsafe-inline'` stays, and is an accepted,
  narrower residual: React writes inline `style=""` via its DOM API at runtime, which no nonce
  scheme can cover, but that surface is CSS-injection-only (never script execution) under the
  loopback-only, no-third-party-content threat model. See `internal/webui/webui.go`'s
  `setStaticSecurityHeaders` doc comment and `reports/session-10-reconciliation.md`.
  The nonce is hex-encoded, not base64: an initial base64 encoding was caught by
  `TestIndexCSPUsesPerRequestNonceNotUnsafeInline` failing on a byte-exact comparison — `html/template`
  HTML-entity-escapes base64's `+` character inside the `nonce="..."` attribute (e.g. `+` becomes
  `&#43;`), which browsers decode back correctly before CSP nonce matching but which is needless
  fragility. Hex uses only `[0-9a-f]`, so the attribute round-trips through `html/template` unescaped
  and byte-identical to the header, with no reliance on entity-decoding equivalence.
- **`github.com/jackc/pgx/v5` bumped 5.7.6 → 5.9.2 and `golang.org/x/text` bumped 0.36.0 → 0.39.0.**
  `govulncheck` (run as gate 5's dependency/SBOM review) found two vulnerabilities with a reachable
  call path from this repository's own code: GO-2026-5004 (pgx placeholder-confusion SQL injection
  risk, reached via `pgxpool.Conn.Query` from `dataplatform.SessionDrilldown`) and GO-2026-5970
  (`x/text` infinite loop on invalid input, reached via `pgxpool.NewWithConfig`'s Postgres identifier
  normalization). Both fixed versions were already vendorable offline; the upgrade was applied,
  re-vendored, and `govulncheck` re-run clean (0 reachable vulnerabilities) before this gate closed.
  This is the one non-Session-10-scoped file change in this pass: it touches `go.mod`/`go.sum`/
  `vendor/`, which the whole dependency tree shares, not just the dashboard.
