/*
 * Shared, live range-selector state. Calendar presets are resolved in the
 * browser's IANA timezone and re-resolved while a visible tab is open, so a
 * dashboard left across midnight never keeps yesterday's frozen `to`.
 */
import { useEffect, useMemo, useState } from "react";

export type RangeKey = "day" | "week" | "month" | "six_months" | "year" | "all_time";
export type BucketGranularity = "hourly" | "daily" | "weekly" | "monthly";

export interface RangeOption {
  value: RangeKey;
  label: string;
  granularity: BucketGranularity;
  refreshMs: number;
}

export const RANGE_OPTIONS: readonly RangeOption[] = [
  { value: "day", label: "Last 24 hours", granularity: "hourly", refreshMs: 30_000 },
  { value: "week", label: "Last 7 days", granularity: "daily", refreshMs: 60_000 },
  { value: "month", label: "Last 30 days", granularity: "daily", refreshMs: 60_000 },
  { value: "six_months", label: "Last 6 months", granularity: "weekly", refreshMs: 300_000 },
  { value: "year", label: "Last 12 months", granularity: "monthly", refreshMs: 300_000 },
  { value: "all_time", label: "Last 5 years", granularity: "monthly", refreshMs: 300_000 },
];

const DEFAULT_RANGE: RangeKey = "month";

function optionFor(key: RangeKey): RangeOption {
  return RANGE_OPTIONS.find((option) => option.value === key) ?? RANGE_OPTIONS[2];
}

function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

function startOfLocalDay(now: Date, daysBack: number): Date {
  const result = new Date(now);
  result.setHours(0, 0, 0, 0);
  result.setDate(result.getDate() - daysBack);
  return result;
}

function startOfLocalMonth(now: Date, monthsBack: number): Date {
  const result = new Date(now);
  result.setHours(0, 0, 0, 0);
  result.setDate(1);
  result.setMonth(result.getMonth() - monthsBack);
  return result;
}

export function computeWindow(key: RangeKey, now = new Date()): { from: string; to: string } {
  let from: Date;
  switch (key) {
    case "day":
      from = new Date(now.getTime() - 24 * 60 * 60 * 1000);
      break;
    case "week":
      from = startOfLocalDay(now, 6);
      break;
    case "month":
      from = startOfLocalDay(now, 29);
      break;
    case "six_months":
      from = startOfLocalMonth(now, 5);
      break;
    case "year":
      from = startOfLocalMonth(now, 11);
      break;
    case "all_time":
      from = startOfLocalMonth(now, 59);
      break;
  }
  return { from: from.toISOString(), to: now.toISOString() };
}

export interface UseRangeResult {
  rangeKey: RangeKey;
  setRangeKey: (key: RangeKey) => void;
  /** RFC3339 half-open [from,to), plus its explicit calendar semantics. */
  from: string;
  to: string;
  granularity: BucketGranularity;
  timezone: string;
  options: readonly RangeOption[];
}

export function useRange(initial: RangeKey = DEFAULT_RANGE): UseRangeResult {
  const [rangeKey, setRangeKey] = useState<RangeKey>(initial);
  const [now, setNow] = useState(() => new Date());
  const option = optionFor(rangeKey);
  const timezone = useMemo(browserTimezone, []);

  useEffect(() => {
    setNow(new Date());
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") setNow(new Date());
    }, option.refreshMs);
    return () => window.clearInterval(timer);
  }, [rangeKey, option.refreshMs]);

  useEffect(() => {
    const refresh = () => {
      if (document.visibilityState === "visible") setNow(new Date());
    };
    document.addEventListener("visibilitychange", refresh);
    window.addEventListener("focus", refresh);
    return () => {
      document.removeEventListener("visibilitychange", refresh);
      window.removeEventListener("focus", refresh);
    };
  }, []);

  const windowRange = useMemo(() => computeWindow(rangeKey, now), [rangeKey, now]);
  return {
    rangeKey,
    setRangeKey,
    from: windowRange.from,
    to: windowRange.to,
    granularity: option.granularity,
    timezone,
    options: RANGE_OPTIONS,
  };
}
