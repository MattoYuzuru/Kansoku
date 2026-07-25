#!/usr/bin/env bash
# Build the dashboard frontend and sync the production output into the Go embed
# directory. Run from anywhere; paths are resolved relative to this script.
#
#   web/scripts/build-and-embed.sh
#
# Steps:
#   1. npm ci            (reproducible install from package-lock.json)
#   2. npm run build     (gen routes -> tsc typecheck -> vite build -> web/dist)
#   3. rsync web/dist    -> internal/webui/dist (the go:embed source)
#
# After this, `go build ./...` produces a binary with the dashboard embedded.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
web_dir="$(cd "${here}/.." && pwd)"
repo_root="$(cd "${web_dir}/.." && pwd)"
embed_dir="${repo_root}/internal/webui/dist"

cd "${web_dir}"

if [ -f package-lock.json ]; then
  npm ci
else
  npm install
fi

npm run build

rm -rf "${embed_dir}"
cp -R "${web_dir}/dist" "${embed_dir}"

echo "build-and-embed: synced ${web_dir}/dist -> ${embed_dir}"
