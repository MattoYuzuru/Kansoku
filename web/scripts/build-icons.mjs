/*
 * Regenerate src/ui/icons.tsx from Tabler Icons (MIT). Fetches the 28 named
 * outline icons fresh from the upstream repo, extracts each SVG body, and
 * inlines the path data into a single TSX module (no runtime SVG fetch, no
 * icon font, no CDN). Idempotent; run with: `npm run gen:icons`.
 *
 * Note: Tabler renamed the historical "pulse" icon to "activity"; the design
 * system (§3) still calls it "pulse", so we vendor "activity" under that name.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const outFile = path.join(here, "..", "src", "ui", "icons.tsx");
const BASE = "https://raw.githubusercontent.com/tabler/tabler-icons/main/icons/outline";

// design-system-tokens.md §3, mapped to upstream filenames.
const ICONS = {
  "layout-dashboard": "layout-dashboard",
  pulse: "activity", // renamed upstream
  timeline: "timeline",
  "message-2": "message-2",
  robot: "robot",
  cpu: "cpu",
  "stack-2": "stack-2",
  sparkles: "sparkles",
  puzzle: "puzzle",
  "plug-connected": "plug-connected",
  tool: "tool",
  heartbeat: "heartbeat",
  "shield-lock": "shield-lock",
  "server-2": "server-2",
  settings: "settings",
  "layout-sidebar-left-collapse": "layout-sidebar-left-collapse",
  "layout-sidebar-left-expand": "layout-sidebar-left-expand",
  "chevron-down": "chevron-down",
  "chevron-right": "chevron-right",
  check: "check",
  x: "x",
  search: "search",
  "info-circle": "info-circle",
  "alert-triangle": "alert-triangle",
  sun: "sun",
  moon: "moon",
  download: "download",
  dots: "dots",
};

function extractBody(svg) {
  const m = svg.match(/<svg[\s\S]*?>([\s\S]*?)<\/svg>/);
  if (!m) throw new Error("no <svg> body");
  return m[1]
    .replace(/<!--[\s\S]*?-->/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

const entries = [];
for (const [name, upstream] of Object.entries(ICONS)) {
  const res = await fetch(`${BASE}/${upstream}.svg`);
  if (!res.ok) throw new Error(`fetch ${upstream}: HTTP ${res.status}`);
  const body = extractBody(await res.text());
  entries.push({ name, body });
}
entries.sort((a, b) => a.name.localeCompare(b.name));

let out = `// AUTO-GENERATED from Tabler Icons (https://github.com/tabler/tabler-icons), MIT license.
// 24x24 viewBox, 2px stroke, currentColor. Attribution: THIRD_PARTY_LICENSES.txt.
// Do not edit by hand; regenerate via web/scripts/build-icons.mjs.
import type { SVGProps } from "react";

export type IconName =
`;
out += entries.map((e) => `  | "${e.name}"`).join("\n") + ";\n\n";
out += `type IconProps = SVGProps<SVGSVGElement> & { size?: number };

const paths: Record<IconName, string> = {
`;
out += entries.map((e) => `  "${e.name}": ${JSON.stringify(e.body)},`).join("\n");
out += `
};

export function Icon({ name, size = 24, ...rest }: IconProps & { name: IconName }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...rest}
      dangerouslySetInnerHTML={{ __html: paths[name] }}
    />
  );
}

export const iconNames = Object.keys(paths) as IconName[];
`;

fs.writeFileSync(outFile, out);
console.log(`gen-icons: wrote ${entries.length} icons -> ${path.relative(path.join(here, ".."), outFile)}`);
