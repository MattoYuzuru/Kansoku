export const APPEARANCE_STORAGE_KEY = "kansoku.appearance.v1";

export type ThemeMode = "dark" | "light";
export type PresetId = "observatory" | "slate-copper" | "moss-amber" | "ink-rose";

export interface Appearance {
  version: 1;
  theme: ThemeMode;
  sidebarCollapsed: boolean;
  accentPurple: string;
  accentGold: string;
  accentPurpleLight: string;
  accentGoldLight: string;
  preset: PresetId;
}

export interface AccentPreset {
  id: PresetId;
  label: string;
  purple: readonly [string, string];
  gold: readonly [string, string];
}

export const PRESETS: readonly AccentPreset[] = [
  { id: "observatory", label: "Observatory", purple: ["#8B7FD6", "#6F63C4"], gold: ["#D9B45B", "#8A6D1F"] },
  { id: "slate-copper", label: "Slate & Copper", purple: ["#7C8AC8", "#4E5FA6"], gold: ["#CE8F5B", "#8A5A1F"] },
  { id: "moss-amber", label: "Moss & Amber", purple: ["#6FA88A", "#2E7D5B"], gold: ["#D9A24B", "#8A6D1F"] },
  { id: "ink-rose", label: "Ink & Rose", purple: ["#7E7AD6", "#5B4FB0"], gold: ["#CE7A8A", "#9A3A4E"] },
];

export const DEFAULT_APPEARANCE: Appearance = {
  version: 1,
  theme: "dark",
  sidebarCollapsed: false,
  accentPurple: "#8B7FD6",
  accentGold: "#D9B45B",
  accentPurpleLight: "#6F63C4",
  accentGoldLight: "#8A6D1F",
  preset: "observatory",
};

function hexToRgb(hex: string): [number, number, number] | null {
  const match = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!match) return null;
  const value = Number.parseInt(match[1], 16);
  return [(value >> 16) & 255, (value >> 8) & 255, value & 255];
}

function rgbToHex(rgb: readonly number[]): string {
  return `#${rgb.map((channel) => Math.round(channel).toString(16).padStart(2, "0")).join("")}`.toUpperCase();
}

function relativeLuminance([r, g, b]: [number, number, number]): number {
  const channel = (value: number) => {
    const normalized = value / 255;
    return normalized <= 0.03928
      ? normalized / 12.92
      : Math.pow((normalized + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

export function contrastRatio(a: string, b: string): number | null {
  const first = hexToRgb(a);
  const second = hexToRgb(b);
  if (!first || !second) return null;
  const left = relativeLuminance(first);
  const right = relativeLuminance(second);
  const [high, low] = left > right ? [left, right] : [right, left];
  return (high + 0.05) / (low + 0.05);
}

export const THEME_BG: Record<ThemeMode, string> = { dark: "#0E0E11", light: "#FBFBFA" };
const THEME_SURFACE: Record<ThemeMode, string> = { dark: "#16161B", light: "#FFFFFF" };

export function passesAA(accent: string, theme: ThemeMode): boolean {
  const ratio = contrastRatio(accent, THEME_BG[theme]);
  return ratio !== null && ratio >= 4.5;
}

export function nearestAaShade(accent: string, theme: ThemeMode): string {
  const rgb = hexToRgb(accent);
  if (!rgb) return accent;
  const darken = theme === "light";
  let [r, g, b] = rgb;
  for (let i = 0; i < 255; i++) {
    const hex = rgbToHex([r, g, b]);
    if (passesAA(hex, theme)) return hex;
    const step = darken ? -3 : 3;
    r = Math.min(255, Math.max(0, r + step));
    g = Math.min(255, Math.max(0, g + step));
    b = Math.min(255, Math.max(0, b + step));
  }
  return theme === "light" ? "#000000" : "#FFFFFF";
}

function mixHex(base: string, accent: string, weight: number): string {
  const baseRGB = hexToRgb(base);
  const accentRGB = hexToRgb(accent);
  if (!baseRGB || !accentRGB) return base;
  return rgbToHex(baseRGB.map(
    (channel, index) => channel * (1 - weight) + accentRGB[index] * weight,
  ));
}

export interface DerivedAppearanceTokens {
  rowHover: string;
  rowSelected: string;
}

export function deriveAppearanceTokens(
  theme: ThemeMode,
  accentPurple: string,
): DerivedAppearanceTokens {
  const fallback = theme === "light"
    ? DEFAULT_APPEARANCE.accentPurpleLight
    : DEFAULT_APPEARANCE.accentPurple;
  const accent = hexToRgb(accentPurple) ? accentPurple : fallback;
  return {
    rowHover: mixHex(THEME_SURFACE[theme], accent, 0.1),
    rowSelected: mixHex(THEME_SURFACE[theme], accent, 0.18),
  };
}
