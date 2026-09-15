# Design system tokens — Session 10 dashboard

Concrete companion to ADR 0013. This file is the single source of truth for exact colors,
sizes, durations, icon names and file names; the ADR records only the decisions and their
rationale. Referenced by `contracts/dashboard.yaml`'s route/title fields and by every
frontend component.

Creative direction: "Observatory" instrumentation base (warm near-black dark theme, warm-paper
light theme, hairline rules — no cards, shadows, gradients or glass) with two borrowings from a
"Grid Terminal" alternative: monospace section headers, and an animated keyboard-focus-ring that
visibly travels between elements plus a persistent active-section marker.

## 1. Design tokens

CSS custom properties on `:root` (dark, default) with a `[data-theme="light"]` override block.
The neutral base is fixed and never user-adjustable; only `--accent-purple` / `--accent-gold`
are runtime-mutable (§9).

### 1.1 Dark theme (`:root`)

```css
:root {
  --bg:              #0E0E11;
  --surface:         #16161B;
  --surface-raised:  #1E1E24;
  --surface-sunken:  #0A0A0C;

  --border:          #2A2A31;
  --border-strong:   #3A3A43;
  --hairline:        #232329;

  --text-primary:    #EDEDF0;  /* 16.5:1 on --bg */
  --text-muted:      #9A9AA3;  /* 6.91:1 on --bg */
  --text-faint:      #808089;  /* 4.61:1 on surface; AA for 12px metadata */
  --text-on-accent:  #0E0E11;

  --accent-purple:        #8B7FD6;  /* 5.6:1 on --bg */
  --accent-purple-hover:  #9D93DE;
  --accent-purple-press:  #7A6EC8;
  --accent-gold:          #D9B45B;  /* 9.76:1 on --bg */
  --accent-gold-hover:    #E3C273;
  --accent-gold-press:    #C7A24A;

  --focus-ring:      #A99CF0;  /* 7.47:1 on --surface */
  --focus-ring-halo: rgba(169,156,240,0.28);

  --active-marker:   var(--accent-gold);
  --row-hover:       #1C1C22;
  --row-selected:    #22212B;

  --status-complete:     #5FB98A;  /* 8.07:1 */
  --status-partial:      #D9B45B;  /* 9.76:1 */
  --status-degraded:     #E06C6C;  /* 5.99:1 */
  --status-unsupported:  #808089;
  --status-not-observed: #808089;
  --status-redacted:     #8B7FD6;
  --status-unknown:      #808089;
  --status-zero:         #9A9AA3;
}
```

### 1.2 Light theme (`[data-theme="light"]`)

```css
[data-theme="light"] {
  --bg:              #FBFBFA;
  --surface:         #FFFFFF;
  --surface-raised:  #FFFFFF;   /* raised via border only, no shadow */
  --surface-sunken:  #F4F3F0;

  --border:          #E4E3DF;
  --border-strong:   #CFCEC8;
  --hairline:        #EEEDE9;

  --text-primary:    #1A1A1E;  /* 16.76:1 on --bg */
  --text-muted:      #6A6A72;  /* 5.18:1 on --bg */
  --text-faint:      #73737B;  /* 4.54:1 on background; AA for 12px metadata */
  --text-on-accent:  #FFFFFF;

  /* On-light "deep" variants — required for AA text/stroke use */
  --accent-purple:        #6F63C4;  /* 4.76:1 on --bg */
  --accent-purple-hover:  #5F53B4;
  --accent-purple-press:  #4E4499;
  --accent-gold:          #8A6D1F;  /* 4.73:1 on --bg — NOT #D9B45B/#B8922F, both fail on light */
  --accent-gold-hover:    #7A5F16;
  --accent-gold-press:    #665013;
  --accent-gold-fill:     #D9B45B;  /* large chart marks only, >24px graphical-object rule */

  --focus-ring:      #6F63C4;  /* 4.76:1 on --bg */
  --focus-ring-halo: rgba(111,99,196,0.24);

  --active-marker:   var(--accent-gold);
  --row-hover:       #F4F3F0;
  --row-selected:    #F0EEF8;

  --status-complete:     #2E7D5B;  /* 4.83:1 */
  --status-partial:      #8A6D1F;  /* 4.73:1 */
  --status-degraded:     #C0392B;  /* 5.25:1 */
  --status-unsupported:  #73737B;
  --status-not-observed: #73737B;
  --status-redacted:     #6F63C4;
  --status-unknown:      #73737B;
  --status-zero:         #6A6A72;
}
```

### 1.3 Spacing scale

Base unit 4px: `--space-1`=4, `--space-2`=8, `--space-3`=12, `--space-4`=16, `--space-5`=24,
`--space-6`=32, `--space-7`=48, `--space-8`=64, `--space-9`=96 (px).

### 1.4 Radius scale (flat instrumentation aesthetic — nothing above 4px)

`--radius-0`=0 (tables/panels/hairline containers/section headers, the house default),
`--radius-1`=2 (inputs/dropdown surface/buttons/status chips),
`--radius-2`=4 (brand-mark placeholder/focus-ring outer bound/KPI value pill).

### 1.5 Typography scale

```css
--font-ui:   "Inter", system-ui, sans-serif;
--font-mono: "JetBrains Mono", ui-monospace, "SF Mono", monospace;
```

Weights shipped: Inter 400/500/600; JetBrains Mono 400/500.
`font-feature-settings: "tnum" 1, "cv05" 1` on all numeric mono contexts.

| Role | Family | Size | Line-height | Weight | Letter-spacing |
|---|---|---|---|---|---|
| Page title | Inter | 20px | 28px | 600 | -0.01em |
| Section header (mono) | JetBrains Mono | 12px | 16px | 500 | 0.06em, uppercase |
| Table header | JetBrains Mono | 12px | 16px | 500 | 0.04em, uppercase |
| Table cell | JetBrains Mono | 13px | 20px | 400 | 0 |
| Body | Inter | 14px | 20px | 400 | 0 |
| Body-strong | Inter | 14px | 20px | 600 | 0 |
| Caption / muted | Inter | 12px | 16px | 400 | 0 |
| KPI large-number | JetBrains Mono | 32px | 36px | 500 | -0.01em |
| KPI unit/delta | JetBrains Mono | 12px | 16px | 400 | 0.02em |

## 2. Sidebar component spec

- Expanded width **248px**; collapsed (icon-rail) width **60px**. Full height, `--surface`
  background, single 1px `--border` right hairline, no shadow. Auto-collapses at ≤1024px;
  manual toggle persisted locally.
- **Brand block** (top, 60px tall): a 28×28px placeholder square (`--radius-2`, `--surface-raised`
  fill, 1px `--border` inset stroke, centered mono "K" glyph in `--accent-purple`) — an explicit
  temporary chip to be replaced by the real brand mark later; renders at the same size in both
  states, `--space-4` left inset. Wordmark "Kansoku" (Inter 16px/600) sits `--space-2` right of the
  chip, expanded-state only. A small `LOCAL` gold tag (10px mono) sits under the wordmark as a
  persistent no-egress reassurance badge.
- **Route grouping** (14 `contracts/dashboard.yaml` routes → 7 nav entries, each with one Tabler icon
  for the collapsed rail):

  | Order | Group (mono label) | Routes | Collapsed icon(s) |
  |---|---|---|---|
  | 1 | — | Overview `/` | `layout-dashboard` |
  | 2 | ACTIVITY | Activity `/activity`; Prompts `/prompts` | `pulse` → `timeline`, `message-2` |
  | 3 | FLEET | Agents `/agents` (+ `/agents/:id`); Models `/models` | `robot` → `robot`, `cpu` |
  | 4 | COMPONENTS | Skills, Plugins, MCP, Tools | `stack-2` → `sparkles`, `puzzle`, `plug-connected`, `tool` |
  | 5 | — | Reliability `/reliability` | `heartbeat` |
  | 6 | OPERATIONS | Privacy `/privacy`; System `/system` | `shield-lock` → `shield-lock`, `server-2` |
  | 7 | — (pinned bottom) | Settings `/settings` | `settings` |

  `/agents/:id` has no independent nav entry; reached via the Agents table, breadcrumb shows
  `Agents / {alias}` (opaque alias only, per `safe_url_policy`). Settings is pinned via
  `margin-top:auto` behind a full-width hairline.
- **States**: rest = transparent; hover = `--row-hover` fill, 90ms fade; active route = persistent
  3px `--active-marker` left bar (`::before`, `scaleY` 0→1) + `--row-selected` fill + 600 weight;
  keyboard focus = a single shared travelling focus-ring DOM node (2px `--focus-ring` outline,
  `--radius-1`) that animates `transform: translate/scale` between entries (§4).
- **Collapse/expand transition**: sidebar `width` 248px⇄60px over 200ms
  `cubic-bezier(0.4,0,0.2,1)`; content `margin-left` animates in lockstep. Labels never
  font-size-scale — they `opacity` 1→0 over 90ms then `visibility:hidden`+`width:0` clip; on
  expand, width restores first (150ms) then opacity 0→1 (90ms, 60ms delay). Icons stay pinned to
  the 60px rail column in both states. Collapsed-state group hover opens a `--surface-raised`
  flyout (opacity + `translateX(-4px→0)`, 180ms).

## 3. Icon set

Tabler Icons, MIT license (self-hostable, redistributable; attribution recorded in
`THIRD_PARTY_LICENSES.txt`). 24×24 viewbox, 2px stroke, `currentColor`. No icon font, no CDN,
no runtime SVG fetch — SVG path data for exactly the icons below is vendored into
`src/ui/icons.tsx`, inherits `currentColor` (no per-theme icon assets).

Icons needed (28 total, ≈12-15 KB uncompressed / ~5 KB gzip): `layout-dashboard`, `pulse`,
`timeline`, `message-2`, `robot`, `cpu`, `stack-2`, `sparkles`, `puzzle`, `plug-connected`, `tool`,
`heartbeat`, `shield-lock`, `server-2`, `settings`, `layout-sidebar-left-collapse`,
`layout-sidebar-left-expand`, `chevron-down`, `chevron-right`, `check`, `x`, `search`,
`info-circle`, `alert-triangle`, `sun`, `moon`, `download`, `dots`.

## 4. Motion system

Tokens: `--motion-fast:90ms`, `--motion-base:180ms`, `--motion-slow:220ms`,
`--motion-chart:400ms`; easing `--ease:cubic-bezier(0.4,0,0.2,1)`.

```css
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
    scroll-behavior: auto !important;
  }
}
```

| # | Animation | Trigger | Duration | Properties | Reduced-motion result |
|---|---|---|---|---|---|
| 1 | Sidebar collapse/expand | toggle / ≤1024px | 200ms | `width`, `margin-left`, label opacity | instant snap |
| 2 | Route transition | route change | 180ms | opacity out (90ms) then opacity+translateY in | instant swap |
| 3 | Section entrance stagger | first paint | 30ms/section, 180ms each | opacity + translateY(6px→0) | all visible at once |
| 4 | Chart entrance | panel mount | 400ms | ECharts animationDuration (stroke-dashoffset/scaleY) | `animation:false`, final frame direct |
| 5 | Row hover | pointer enter/leave | 90ms | background-color | instant fill |
| 6 | Focus-ring travel | keyboard focus move | 180ms | shared node transform + opacity | ring appears at target instantly |
| 7 | KPI number roll-up | mount/update | 220ms | `requestAnimationFrame` count-up on textContent | final number immediately |
| 8 | Status-glyph transition | view-state change | 90ms | glyph opacity cross-fade | instant swap |
| 9 | Dropdown open/close | click/Enter/Esc | 180ms | opacity + scaleY(0.98→1) + translateY(-4px→0) | instant show/hide |
| 10 | Scrollbar drag feedback | pointer over/drag | 90ms | thumb background-color + width (8→10px) | instant change |
| 11 | Theme toggle | sun/moon click | 180ms | color/background-color cross-fade on `:root` | instant swap |

No animation library: 1-6/8-11 are CSS `transition`/`@keyframes`; #7 is a ~20-line rAF counter;
#4 delegates to ECharts' own flag. Keeps design-system JS near zero against the analytics-chunk
budget.

## 5. Custom scrollbar

CSS-only styled native scrollbar (`::-webkit-scrollbar*` + `scrollbar-width`/`scrollbar-color`)
— not a JS overlay, so it stays at zero added JS/DOM and keeps native momentum scrolling.

| Property | Dark | Light |
|---|---|---|
| Track | `--surface-sunken` `#0A0A0C` | `#F4F3F0` |
| Thumb (rest) | `--border-strong` `#3A3A43` | `#CFCEC8` |
| Thumb (hover/drag) | `#4A4A54` | `#B8B7B0` |
| Thickness | 10px (8px thumb + 1px inset); Firefox `scrollbar-width: thin` | same |
| Thumb radius | 2px | 2px |
| Min thumb length | 32px | 32px |

## 6. Custom dropdown / select

No native `<select>` anywhere — an ARIA `combobox`/`listbox` React component, used for every
global-query filter (range/timezone/agent/project/model/component/source/evidence_tier) and any
in-panel select.

- Closed trigger: 32px tall, `--radius-1`, 1px `--border` → `--border-strong` hover →
  `--focus-ring` 2px focus; trailing `chevron-down` (rotates 180° on open, 90ms); leading mono
  caption label above (e.g. `RANGE`).
- Open panel: `--surface-raised`, 1px `--border`, `--radius-1`, max-height 320px with the §5
  scrollbar; options 32px tall, `--row-hover` on hover, selected shows leading `check` in
  `--accent-purple` + Body-strong.
- Keyboard: `Enter`/`Space`/`↓` opens; `↑`/`↓` moves (wraps); `Home`/`End` jump; `Enter` selects
  and closes; `Esc` closes without change; typeahead matches label prefix within a 500ms buffer.
  Focus trapped in the open listbox; `aria-activedescendant` tracks the active option.
- Multi-select filters reuse the component with checkbox-style toggles and a trailing count chip.

## 7. Status vocabulary

Every one of `contracts/dashboard.yaml`'s 8 `view_states` gets glyph + shape + text label; color
is reinforcement only.

| view_state | Glyph | Rationale | Label | Color token |
|---|---|---|---|---|
| `complete` | ● filled circle | solid = fully present | "Complete" | `--status-complete` |
| `partial` | ◑ half circle (right-fill) | some data present, distinct from degraded | "Partial" | `--status-partial` |
| `degraded` | ◐ half circle (left-fill) + `!` overlay | half + warning overlay | "Degraded" | `--status-degraded` |
| `unsupported` | ○ hollow circle | capability absent by design | "Unsupported" | `--status-unsupported` |
| `not_observed` | ◌ dotted-outline circle | could exist, none seen — distinct from hollow | "Not observed" | `--status-not-observed` |
| `redacted` | ▨ cross-hatched square | intentional privacy mask, not missing | "Redacted" | `--status-redacted` |
| `unknown` | – en-dash | no determination | "Unknown" | `--status-unknown` |
| `numeric_zero` | 0 mono zero glyph | a real measured zero, not absence | "Zero" | `--status-zero` |

Fill-fraction, outline style and overlay differ enough to be distinguishable in monochrome and
to colorblind users. Glyph precedes value in tables; KPIs show the state label explicitly when
not `complete`.

## 8. Favicon / tab-title

Placeholder assets (self-hosted, `/public/`), each carrying the temporary "K" mark until real
brand art exists:

| File | Format | Size |
|---|---|---|
| `favicon.ico` | ICO | 16+32+48 multi-res |
| `favicon-16.png` | PNG | 16×16 |
| `favicon-32.png` | PNG | 32×32 |
| `favicon.svg` | SVG | scalable, `prefers-color-scheme` aware |
| `apple-touch-icon.png` | PNG | 180×180 |
| `mask-icon.svg` | SVG monochrome | scalable, `--accent-purple` |
| `site.webmanifest` | JSON | `theme_color`/`background_color` `#0E0E11` |

`<title>` template: `Kansoku · {route.title}`, sourced directly from `contracts/dashboard.yaml`'s
`title` field per route (e.g. `Kansoku · Overview`) so the tab stays in sync with the
authoritative route registry. Agent detail: `Kansoku · Agent {alias}` (opaque alias only).

## 9. Accent "appearance" playground

- Lives on `/settings`, an "Appearance" panel above the read-only policy panels (pure client
  preference, not an operational preview/apply flow).
- Only `--accent-purple` / `--accent-gold` are exposed; the neutral base is locked. Each has a
  color input plus a live contrast readout against `--bg` for the active theme; a value that
  would fail 4.5:1 is blocked with the nearest AA-safe shade offered instead.
- Persists to `localStorage` key `kansoku.appearance.v1`:
  ```json
  { "version": 1, "theme": "dark", "sidebarCollapsed": false,
    "accentPurple": "#8B7FD6", "accentGold": "#D9B45B",
    "accentPurpleLight": "#6F63C4", "accentGoldLight": "#8A6D1F",
    "preset": "observatory" }
  ```
  Applied to `:root` before first paint via an inline head script (no flash of default theme).
- Curated AA-verified presets: **Observatory** (default, purple `#8B7FD6`/`#6F63C4`, gold
  `#D9B45B`/`#8A6D1F`), **Slate & Copper** (`#7C8AC8`/`#4E5FA6`, `#CE8F5B`/`#8A5A1F`), **Moss &
  Amber** (`#6FA88A`/`#2E7D5B`, `#D9A24B`/`#8A6D1F`), **Ink & Rose** (`#7E7AD6`/`#5B4FB0`,
  `#CE7A8A`/`#9A3A4E`) — all four verified ≥4.5:1 in both themes.

## 10. Responsive behavior

Desktop-first, single hard breakpoint at 1024px. Above it: sidebar expanded (unless manually
collapsed), multi-column panel grids, content max-width 1440px centered with `--space-8` gutters.
At or below it: sidebar auto-collapses to the 60px rail, panels reflow to one column, gutters
shrink to `--space-5`. Any table wider than its container gets a horizontal scroll (§5 scrollbar)
with the identity column `position:sticky; left:0`. Phone width (<640px) is an explicit stretch
goal only — not a Session 10 gate; QA sign-off targets 1024px and up.

## 11. Compliance checklist

- **WCAG AA contrast pairs verified** (wire into a CI contrast test): dark
  text-primary/bg 16.5, text-muted/bg 6.91, accent-purple/bg 5.6, accent-gold/bg 9.76,
  focus-ring/surface 7.47, status-complete/degraded/partial all ≥5.99; light text-primary/bg
  16.76, text-muted/bg 5.18, accent-purple(deep)/bg 4.76, accent-gold(deep)/bg 4.73,
  focus-ring/bg 4.76, status colors all ≥4.73. `--border`/`--hairline` are decorative dividers,
  not state carriers — state always comes from the focus ring, active-marker bar, or status
  glyph+label. Raw brand gold `#D9B45B` fails on light (2.82:1) and is never used as light-theme
  text — only as `--accent-gold-fill` for large (>24px) chart marks.
- **Zero external network dependencies**: fonts `Inter-Regular/Medium/SemiBold.woff2`,
  `JetBrainsMono-Regular/Medium.woff2` (self-hosted `/fonts/`, `font-display:swap`, no CDN);
  icons vendored in `src/ui/icons.tsx` (no `@tabler` CDN, no icon font); favicons in `/public/`;
  no remote analytics/telemetry/map calls; recommend `Content-Security-Policy: default-src 'self'`.
- **Reduced-motion**: the universal media-query rule plus a documented static fallback covers all
  11 named animations.
- **No native chrome**: CSS-only scrollbar styling covers both browser engine families; every
  select/filter is the custom combobox — no native `<select>` renders anywhere.
