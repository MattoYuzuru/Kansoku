export const RANGE_PREFERENCE_STORAGE_KEY = "kansoku.range.v1";

export const RANGE_PRESETS = [
  "day",
  "week",
  "month",
  "six_months",
  "year",
  "all_time",
] as const;

export type RangePreset = (typeof RANGE_PRESETS)[number];

export const RANGE_PAGE_KEYS = [
  "overview",
  "activity",
  "prompts",
  "agents",
  "models",
  "skills",
  "plugins",
  "mcp",
  "tools",
  "reliability",
  "privacy",
] as const;

export type RangePageKey = (typeof RANGE_PAGE_KEYS)[number];

export interface RangePreferenceDocument {
  version: 1;
  pages: Partial<Record<RangePageKey, RangePreset>>;
}

export interface RangePreferenceStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

const EMPTY_PREFERENCES: RangePreferenceDocument = { version: 1, pages: {} };
const PAGE_KEYS = new Set<string>(RANGE_PAGE_KEYS);
const PRESETS = new Set<string>(RANGE_PRESETS);

function isPreferenceDocument(value: unknown): value is RangePreferenceDocument {
  if (!value || typeof value !== "object") return false;
  const candidate = value as { version?: unknown; pages?: unknown };
  if (candidate.version !== 1 || !candidate.pages || typeof candidate.pages !== "object") {
    return false;
  }
  return Object.entries(candidate.pages).every(
    ([page, preset]) => PAGE_KEYS.has(page) && typeof preset === "string" && PRESETS.has(preset),
  );
}

export function loadRangePreferences(
  storage: RangePreferenceStorage,
): RangePreferenceDocument {
  try {
    const raw = storage.getItem(RANGE_PREFERENCE_STORAGE_KEY);
    if (!raw) return EMPTY_PREFERENCES;
    const parsed: unknown = JSON.parse(raw);
    return isPreferenceDocument(parsed)
      ? { version: 1, pages: { ...parsed.pages } }
      : EMPTY_PREFERENCES;
  } catch {
    return EMPTY_PREFERENCES;
  }
}

export function readRangePreference(
  storage: RangePreferenceStorage,
  page: RangePageKey,
  fallback: RangePreset,
): RangePreset {
  return loadRangePreferences(storage).pages[page] ?? fallback;
}

export function writeRangePreference(
  storage: RangePreferenceStorage,
  page: RangePageKey,
  preset: RangePreset,
): boolean {
  try {
    const current = loadRangePreferences(storage);
    const next: RangePreferenceDocument = {
      version: 1,
      pages: { ...current.pages, [page]: preset },
    };
    storage.setItem(RANGE_PREFERENCE_STORAGE_KEY, JSON.stringify(next));
    return true;
  } catch {
    return false;
  }
}
