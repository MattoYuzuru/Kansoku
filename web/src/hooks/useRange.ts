/*
 * Shared range-selector state: one hook used by every range-taking page so
 * from/to computation lives in exactly one place (task scope boundary — no
 * per-page reinvention, no general filter/comparison system here).
 *
 * Ranges offered are a sensible subset of GLOBAL_QUERY.ranges
 * (contracts/dashboard.yaml): day/week/month/year/all_time. "all_time" is
 * approximated client-side as 5 years back to now (a reasonable stand-in for
 * "earliest plausible data" — the backend does not expose a data min/max).
 * Default is "month" per the task's scope boundary.
 */
import { useMemo, useState } from "react";

export type RangeKey = "day" | "week" | "month" | "year" | "all_time";

export interface RangeOption {
  value: RangeKey;
  label: string;
  days: number;
}

export const RANGE_OPTIONS: readonly RangeOption[] = [
  { value: "day", label: "Last 24 hours", days: 1 },
  { value: "week", label: "Last 7 days", days: 7 },
  { value: "month", label: "Last 30 days", days: 30 },
  { value: "year", label: "Last 365 days", days: 365 },
  { value: "all_time", label: "All time (5y)", days: 365 * 5 },
];

const DEFAULT_RANGE: RangeKey = "month";

function computeWindow(key: RangeKey): { from: string; to: string } {
  const opt = RANGE_OPTIONS.find((o) => o.value === key) ?? RANGE_OPTIONS[2];
  const to = new Date();
  const from = new Date(to.getTime() - opt.days * 24 * 60 * 60 * 1000);
  return { from: from.toISOString(), to: to.toISOString() };
}

export interface UseRangeResult {
  rangeKey: RangeKey;
  setRangeKey: (key: RangeKey) => void;
  /** RFC3339 from/to computed client-side, half-open [from,to). */
  from: string;
  to: string;
  options: readonly RangeOption[];
}

/** One shared range control's state; instantiate once per page (lifted state). */
export function useRange(initial: RangeKey = DEFAULT_RANGE): UseRangeResult {
  const [rangeKey, setRangeKey] = useState<RangeKey>(initial);
  const window = useMemo(() => computeWindow(rangeKey), [rangeKey]);
  return { rangeKey, setRangeKey, from: window.from, to: window.to, options: RANGE_OPTIONS };
}
