# Session 18 — Design system, browser regression and content access

## Status

Approved as a late but complete priority on 2026-07-26. Implementation has not started.

## Purpose

Close theme/layout regressions as a class and deliver the complete, explicitly authorized local
viewer for skill/plugin documentation and code. The viewer is not split across earlier sessions.

## Design-system scope

- semantic palette tokens derived for hover, press, focus, selected rows and charts;
- consistent presets across light/dark themes and collapsed/expanded navigation;
- symmetric switch geometry;
- shared precision/unit formatting;
- accessibility, keyboard, reduced-motion and contrast checks;
- Playwright interaction and screenshot regression for production embedded assets.

## Content-access scope

The inventory/profile pages from Sessions 14 and 16 gain an opt-in transient viewer for SKILL.md,
plugin manifests, scripts and references. It:

- reads only explicit allowed roots;
- validates containment and symlink targets;
- never persists, logs, exports or caches file content;
- uses opaque locators instead of unredacted paths;
- rejects binaries and oversized/unsupported files;
- sanitizes Markdown/HTML and renders code as text;
- requires a separate local content-access authorization, not the normal read bearer;
- remains read-only and performs no enable/disable or edits.

## Alternatives rejected

- shipping a partial metadata/Markdown viewer in Session 14;
- exposing host files through the ordinary dashboard read API;
- storing indexed source text in PostgreSQL;
- fixing individual preset colors without visual regression coverage.

## Exit gate

Every preset updates all semantic accents and charts, switch spacing is symmetric, accessibility and
production screenshot matrices pass, and the viewer can read an approved canary bundle while path
escape, symlink escape, binary, oversize, XSS and ten-sink content canaries are rejected.
