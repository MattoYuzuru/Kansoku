/*
 * Theme + "appearance" playground data model (§9).
 *
 * The neutral base is locked; accent and derived interaction roles are runtime
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
import {
  APPEARANCE_STORAGE_KEY,
  DEFAULT_APPEARANCE,
  PRESETS,
  deriveAppearanceTokens,
  passesAA,
  type Appearance,
  type PresetId,
  type ThemeMode,
} from "./appearance";
export {
  APPEARANCE_STORAGE_KEY,
  DEFAULT_APPEARANCE,
  PRESETS,
  THEME_BG,
  contrastRatio,
  deriveAppearanceTokens,
  nearestAaShade,
  passesAA,
} from "./appearance";
export type { AccentPreset, Appearance, PresetId, ThemeMode } from "./appearance";

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
 * Accent families and their derived row interactions are overridden; the
 * neutral base stays whatever tokens.css defines for the active theme.
 */
export function applyAppearance(a: Appearance): void {
  const root = document.documentElement;
  root.setAttribute("data-theme", a.theme);
  const purple = a.theme === "light" ? a.accentPurpleLight : a.accentPurple;
  const gold = a.theme === "light" ? a.accentGoldLight : a.accentGold;
  root.style.setProperty("--accent-purple", purple);
  root.style.setProperty("--accent-gold", gold);
  const derived = deriveAppearanceTokens(a.theme, purple);
  root.style.setProperty("--row-hover", derived.rowHover);
  root.style.setProperty("--row-selected", derived.rowSelected);
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
