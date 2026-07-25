/*
 * Theme + "appearance" playground data model (§9).
 *
 * The neutral base is locked; only --accent-purple / --accent-gold are runtime
 * mutable. State persists to localStorage under `kansoku.appearance.v1` with
 * the exact JSON shape from the spec. The pre-paint application (avoiding a
 * flash of the default theme) is done by an inline <head> script emitted by
 * index.html; this module owns the same logic for React-driven updates and
 * exposes it so the Settings page (a later task) can build the actual UI.
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

export const APPEARANCE_STORAGE_KEY = "kansoku.appearance.v1";

export type ThemeMode = "dark" | "light";
export type PresetId = "observatory" | "slate-copper" | "moss-amber" | "ink-rose";

/** Exact persisted shape (§9). Written verbatim to localStorage. */
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
  /** [dark, light] accent shades, both verified >= 4.5:1 in their theme. */
  purple: readonly [string, string];
  gold: readonly [string, string];
}

/** §9 curated AA-verified presets. */
export const PRESETS: readonly AccentPreset[] = [
  {
    id: "observatory",
    label: "Observatory",
    purple: ["#8B7FD6", "#6F63C4"],
    gold: ["#D9B45B", "#8A6D1F"],
  },
  {
    id: "slate-copper",
    label: "Slate & Copper",
    purple: ["#7C8AC8", "#4E5FA6"],
    gold: ["#CE8F5B", "#8A5A1F"],
  },
  {
    id: "moss-amber",
    label: "Moss & Amber",
    purple: ["#6FA88A", "#2E7D5B"],
    gold: ["#D9A24B", "#8A6D1F"],
  },
  {
    id: "ink-rose",
    label: "Ink & Rose",
    purple: ["#7E7AD6", "#5B4FB0"],
    gold: ["#CE7A8A", "#9A3A4E"],
  },
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

/* ---- Contrast helpers (§9: block a manual value below 4.5:1) ---- */

function hexToRgb(hex: string): [number, number, number] | null {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return null;
  const n = parseInt(m[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function relativeLuminance([r, g, b]: [number, number, number]): number {
  const channel = (c: number) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

/** WCAG contrast ratio between two hex colors, or null if either is malformed. */
export function contrastRatio(a: string, b: string): number | null {
  const ra = hexToRgb(a);
  const rb = hexToRgb(b);
  if (!ra || !rb) return null;
  const la = relativeLuminance(ra);
  const lb = relativeLuminance(rb);
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

/** The neutral --bg for each theme (locked base). */
export const THEME_BG: Record<ThemeMode, string> = { dark: "#0E0E11", light: "#FBFBFA" };

/** True when `accent` clears WCAG AA (4.5:1) against the theme background. */
export function passesAA(accent: string, theme: ThemeMode): boolean {
  const ratio = contrastRatio(accent, THEME_BG[theme]);
  return ratio !== null && ratio >= 4.5;
}

/**
 * Nudge a hex color toward the theme background's opposite luminance until it
 * clears 4.5:1, returning the nearest AA-safe shade (§9). Used by the Settings
 * playground to offer a replacement when a manual value fails.
 */
export function nearestAaShade(accent: string, theme: ThemeMode): string {
  const rgb = hexToRgb(accent);
  if (!rgb) return accent;
  const darken = theme === "light";
  let [r, g, b] = rgb;
  for (let i = 0; i < 255; i++) {
    const hex =
      "#" +
      [r, g, b].map((c) => Math.round(c).toString(16).padStart(2, "0")).join("");
    if (passesAA(hex, theme)) return hex;
    const step = darken ? -3 : 3;
    r = Math.min(255, Math.max(0, r + step));
    g = Math.min(255, Math.max(0, g + step));
    b = Math.min(255, Math.max(0, b + step));
  }
  return theme === "light" ? "#000000" : "#FFFFFF";
}

/* ---- Persistence + application ---- */

export function loadAppearance(): Appearance {
  try {
    const raw = localStorage.getItem(APPEARANCE_STORAGE_KEY);
    if (!raw) return { ...DEFAULT_APPEARANCE };
    const parsed = JSON.parse(raw) as Partial<Appearance>;
    if (parsed.version !== 1) return { ...DEFAULT_APPEARANCE };
    return { ...DEFAULT_APPEARANCE, ...parsed, version: 1 };
  } catch {
    return { ...DEFAULT_APPEARANCE };
  }
}

function saveAppearance(a: Appearance): void {
  try {
    localStorage.setItem(APPEARANCE_STORAGE_KEY, JSON.stringify(a));
  } catch {
    /* private-mode / disabled storage: run in-memory only */
  }
}

/**
 * Apply theme + accents to :root. Mirrors the inline pre-paint head script.
 * Only the two accent families are overridden; the neutral base stays whatever
 * tokens.css defines for the active theme.
 */
export function applyAppearance(a: Appearance): void {
  const root = document.documentElement;
  root.setAttribute("data-theme", a.theme);
  const purple = a.theme === "light" ? a.accentPurpleLight : a.accentPurple;
  const gold = a.theme === "light" ? a.accentGoldLight : a.accentGold;
  root.style.setProperty("--accent-purple", purple);
  root.style.setProperty("--accent-gold", gold);
}

/* ---- React context ---- */

interface ThemeContextValue {
  appearance: Appearance;
  setTheme: (theme: ThemeMode) => void;
  toggleTheme: () => void;
  setSidebarCollapsed: (collapsed: boolean) => void;
  applyPreset: (id: PresetId) => void;
  /** Set an accent for the active theme; rejects (returns false) below AA. */
  setAccent: (which: "purple" | "gold", hex: string) => boolean;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [appearance, setAppearance] = useState<Appearance>(loadAppearance);

  const update = useCallback((next: Appearance) => {
    applyAppearance(next);
    saveAppearance(next);
    setAppearance(next);
  }, []);

  // Ensure the DOM matches state on mount (the head script may not have run in
  // tests / SSR-less previews).
  useEffect(() => {
    applyAppearance(appearance);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Keep multiple tabs in sync.
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key === APPEARANCE_STORAGE_KEY) {
        const next = loadAppearance();
        applyAppearance(next);
        setAppearance(next);
      }
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const value = useMemo<ThemeContextValue>(
    () => ({
      appearance,
      setTheme: (theme) => update({ ...appearance, theme }),
      toggleTheme: () =>
        update({ ...appearance, theme: appearance.theme === "dark" ? "light" : "dark" }),
      setSidebarCollapsed: (sidebarCollapsed) => update({ ...appearance, sidebarCollapsed }),
      applyPreset: (id) => {
        const preset = PRESETS.find((p) => p.id === id) ?? PRESETS[0];
        update({
          ...appearance,
          preset: preset.id,
          accentPurple: preset.purple[0],
          accentPurpleLight: preset.purple[1],
          accentGold: preset.gold[0],
          accentGoldLight: preset.gold[1],
        });
      },
      setAccent: (which, hex) => {
        if (!passesAA(hex, appearance.theme)) return false;
        const isLight = appearance.theme === "light";
        const patch: Partial<Appearance> =
          which === "purple"
            ? isLight
              ? { accentPurpleLight: hex }
              : { accentPurple: hex }
            : isLight
              ? { accentGoldLight: hex }
              : { accentGold: hex };
        update({ ...appearance, ...patch, preset: appearance.preset });
        return true;
      },
    }),
    [appearance, update],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme must be used within ThemeProvider");
  return ctx;
}
