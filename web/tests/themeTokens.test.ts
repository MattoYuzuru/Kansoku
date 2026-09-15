import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";
import {
  PRESETS,
  contrastRatio,
  deriveAppearanceTokens,
  type Appearance,
  type ThemeMode,
} from "../src/appearance.ts";

const html = readFileSync(new URL("../index.html", import.meta.url), "utf8");
const bootstrap = html.match(/<script nonce="\{\{\.CSPNonce\}\}">([\s\S]*?)<\/script>/)?.[1];
assert.ok(bootstrap, "pre-paint appearance script missing");

function prePaintTokens(appearance: Appearance): Record<string, string> {
  const values: Record<string, string> = {};
  const root = {
    setAttribute: () => undefined,
    style: { setProperty: (name: string, value: string) => { values[name] = value; } },
  };
  vm.runInNewContext(bootstrap, {
    document: { documentElement: root },
    localStorage: { getItem: () => JSON.stringify(appearance) },
    JSON,
  });
  return values;
}

test("pre-paint and React appearance paths derive identical sidebar tokens", () => {
  for (const preset of PRESETS) {
    for (const theme of ["dark", "light"] as const) {
      const appearance: Appearance = {
        version: 1,
        theme,
        sidebarCollapsed: false,
        accentPurple: preset.purple[0],
        accentGold: preset.gold[0],
        accentPurpleLight: preset.purple[1],
        accentGoldLight: preset.gold[1],
        preset: preset.id,
      };
      const purple = theme === "light" ? appearance.accentPurpleLight : appearance.accentPurple;
      const expected = deriveAppearanceTokens(theme, purple);
      const actual = prePaintTokens(appearance);
      assert.equal(actual["--row-hover"], expected.rowHover);
      assert.equal(actual["--row-selected"], expected.rowSelected);
    }
  }
});

test("derived sidebar backgrounds retain AA text contrast for every preset", () => {
  const text: Record<ThemeMode, string> = { dark: "#EDEDF0", light: "#1A1A1E" };
  for (const preset of PRESETS) {
    for (const theme of ["dark", "light"] as const) {
      const purple = theme === "dark" ? preset.purple[0] : preset.purple[1];
      const tokens = deriveAppearanceTokens(theme, purple);
      assert.ok((contrastRatio(text[theme], tokens.rowHover) ?? 0) >= 4.5);
      assert.ok((contrastRatio(text[theme], tokens.rowSelected) ?? 0) >= 4.5);
    }
  }
});
