# Wave A reconciliation — 2026-07-30

## Scope and result

Wave A from `reports/2026-07-30-next-agent-prompt.md` is implemented as five isolated source
commits:

- `b4e77e8` — versioned page-scoped range presets with storage fallback and cross-tab merge;
- `8b84a4c` — shared at-most-two-decimal metric/percentile presentation with raw-value access;
- `e60437b` — deterministic glossary target pulse and reduced-motion fallback;
- `55f9dc1` — IEC database-size formatting and generic KPI containment;
- `ccf34f4` — theme-derived sidebar hover/selected tokens shared by pre-paint and React paths.

No metric formula, historical telemetry, incident state, retention rule, or agent configuration was
changed. Detail routes intentionally inherit their parent list preference (`agents`, `skills`,
`plugins`, or `mcp`) rather than creating an opaque-ID-specific preference.

## Verification evidence

- Frontend unit tests: component catalog 5/5, range preference 4/4, formatter 3/3, glossary target
  2/2, theme tokens 2/2.
- `npm run typecheck`: pass.
- `npm run verify:a11y-tokens`: pass, 44 static checks, minimum ratio 4.5399:1.
- Production Vite build: pass; `web/dist` and `internal/webui/dist` are byte-identical.
- `go test ./internal/webui`: pass.
- Local production image build: pass.
- Live Chrome 150 harness:
  - Activity remains `Last 7 days`;
  - Models remains `Last 12 months` after route changes and a full reload;
  - no Reliability/System KPI overflow at 1440, 1024, 390, or 720 CSS pixels (200% desktop
    equivalent);
  - custom dark and light preset interaction tokens change with the active accent;
  - glossary target receives the deterministic pulse and focus; reduced-motion reports no
    animation;
  - no browser network failure was captured.

The live evidence is in `reports/artifacts/2026-07-30/browser-evidence.json`. Existing P0
reproductions remain intentionally visible there: the skill profile still throws on `file_tree:null`
and agent profile requests still return 503. They are Wave B/C exit gates, not Wave A regressions.

## Migration-startup reconciliation

The first clean Wave A image correctly failed closed on dataplatform migration `0012`: the live
ledger contained the pre-commit SQL checksum whose only difference was one additional trailing
newline. Commit `aa32683` records the applied/committed checksum pair explicitly and accepts no
other mismatch. `TestProjectionReceiptMigrationMatchesAppliedTrustRoot` proves the exact pair and
the fail-closed arbitrary-mismatch path. The rebuilt appliance then started without editing the
ledger or database schema.

## Resource and retention review

Range preferences add one bounded localStorage document with a fixed page-key and preset
vocabulary. The glossary cue runs for five seconds, changes paint-only properties, removes its
class/timer, and has a static reduced-motion path. Formatting/theme work adds no database rows,
network egress, collector work, or retention surface.

## Residual risks

- Full root/route failure containment, nullable API normalization, and visible query errors remain
  Wave B.
- Agent profile query budget/identity/classification remain Wave C.
- Rollout oversized-line recovery and telemetry terminal correctness remain Wave D.
- The Vite 6.3.6 high-severity development dependency finding remains R14 and is not represented as
  a production compromise.
