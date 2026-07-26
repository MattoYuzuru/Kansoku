# TDD 18 — Design system, browser regression and content access

## Semantic design tokens

Replace partial runtime overrides with a complete semantic palette object that derives base,
hover, press, focus, selection, chart and status accents for light/dark themes. Component CSS uses
semantic roles, never stale literal purple values. Switch geometry derives travel from track,
border, inset and thumb custom properties.

Playwright runs production embedded assets across themes, presets, navigation states, core routes,
view states, viewport sizes, keyboard/focus, reduced motion and contrast checks. Screenshot fixtures
are versioned and reviewed.

## Content-access boundary

Introduce a separate local content-access authorization and ephemeral read endpoint. Inventory stores
only opaque locators. At request time the resolver revalidates an explicit allowed root, regular
file type, containment, every symlink, maximum bytes and MIME/extension allowlist.

Responses use `no-store`, a strict CSP, sanitized Markdown with raw HTML disabled and code rendered
as text. File bytes never enter logs, Postgres, cache, diagnostics, export or backup. The normal read
bearer cannot access the endpoint. No write method exists.

## Tests and exit gate

Preset screenshot matrix, switch geometry, formatting, accessibility and embedded-build tests;
allowed canary bundle reads; traversal/symlink/binary/oversize/XSS/content-canary rejections; browser
network scan. Both design-system and viewer gates must pass before Session 18 is complete.
