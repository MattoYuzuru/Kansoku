#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const source = readFileSync(join(root, "src", "tokens.css"), "utf8");
const darkBlock = source.match(/:root\s*\{([\s\S]*?)\n\}/)?.[1] ?? "";
const lightBlock = source.match(/\[data-theme="light"\]\s*\{([\s\S]*?)\n\}/)?.[1] ?? "";

const parse = (block) => Object.fromEntries(
  [...block.matchAll(/--([a-z0-9-]+):\s*(#[0-9A-Fa-f]{6})/g)]
    .map((match) => [match[1], match[2]]),
);
const themes = { dark: parse(darkBlock), light: parse(lightBlock) };
const checks = [];
for (const [theme, tokens] of Object.entries(themes)) {
  for (const foreground of [
    "text-primary", "text-muted", "text-faint",
    "status-complete", "status-partial", "status-degraded",
    "status-unsupported", "status-not-observed", "status-redacted",
    "status-unknown", "status-zero",
  ]) {
    for (const background of ["bg", "surface"]) {
      const measured = contrast(tokens[foreground], tokens[background]);
      checks.push({ theme, foreground, background, measured });
      if (measured < 4.5) {
        throw new Error(
          `${theme}:${foreground}_on_${background}:${measured.toFixed(2)}_below_4.5`,
        );
      }
    }
  }
}
process.stdout.write(`${JSON.stringify({
  status: "pass",
  minimum_ratio: Math.min(...checks.map((check) => check.measured)),
  checks: checks.length,
}, null, 2)}\n`);

function contrast(left, right) {
  if (!left || !right) throw new Error("token_missing");
  const first = luminance(left);
  const second = luminance(right);
  return (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05);
}

function luminance(hex) {
  const channels = [1, 3, 5].map((offset) =>
    Number.parseInt(hex.slice(offset, offset + 2), 16) / 255
  ).map((value) =>
    value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
  );
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
}
