# internal/webui

Serves the Session 10 dashboard SPA (built under `web/`) from the same Go
process and loopback port (`:43100`) that serves `/api/v1`.

## What it does

- `//go:embed all:dist` embeds the committed production build output
  (`internal/webui/dist`, a copy of `web/dist`) into the binary.
- All assets except `index.html` are served as immutable, content-hashed static
  files.
- `index.html` is rendered per request as an `html/template` so the live
  read-bearer and CSRF tokens are injected into the `kansoku-read-token` /
  `kansoku-csrf-token` `<meta>` tags. The mutation bearer is never embedded —
  the dashboard is read-only.
- Any path that is not a real embedded asset falls back to `index.html`, so
  client-side (wouter) routing survives refresh and deep-links.
- The package does not import `internal/runtime`; `NewHandler(readBearer, csrf
  []byte)` takes the raw secret bytes, keeping the dependency direction
  `runtime -> webui`.

## Rebuilding the embedded UI

The `dist` directory is committed so a plain `go build ./...` always produces a
working binary. To regenerate it after changing anything under `web/src`:

```sh
# One shot: install, build, and sync into the embed directory.
web/scripts/build-and-embed.sh

# Or manually:
cd web
npm ci
npm run build                     # -> web/dist
cd ..
rm -rf internal/webui/dist
cp -R web/dist internal/webui/dist
go build ./...                    # binary now embeds the new UI
```

`npm run build` runs, in order: `gen-routes.mjs` (regenerates the route
registry from `contracts/dashboard.yaml`), `tsc --noEmit` (typecheck), then
`vite build`.

The production Docker build compares `web/dist` with `internal/webui/dist` and
fails with a remediation message if they differ. This prevents a successful Go
rebuild from silently embedding an older dashboard bundle.
