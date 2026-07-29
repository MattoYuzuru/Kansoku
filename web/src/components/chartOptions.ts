/*
 * Small ECharts option builders shared across pages, so each page constructs
 * a plain option object (per ChartContainer's contract) without duplicating
 * axis/tooltip/color boilerplate. Kept deliberately minimal — no chart-lib
 * abstraction layer, just factored literals.
 */
export interface SeriesSpec {
  name: string;
  data: (number | null)[];
  color?: string;
  type?: "line" | "bar";
  yAxisIndex?: number;
  stack?: string;
}

export interface BucketRange {
  from: string;
  to: string;
  granularity: "hourly" | "daily" | "weekly" | "monthly";
  timezone: string;
}

export interface BucketedRow {
  day: string;
}

export interface BucketSeriesSpec<T extends BucketedRow> extends Omit<SeriesSpec, "data"> {
  value: (row: T) => number | null | undefined;
}

const PALETTE = [
  "var(--accent-purple)",
  "var(--accent-gold)",
  "var(--status-complete)",
  "var(--status-degraded)",
  "var(--text-muted)",
];

/**
 * Resolve a single `var(--x)` token to its live computed hex/color value.
 * Exported for ChartContainer, which resolves the whole option tree at
 * apply-time (see its module comment for why resolution can't happen here).
 */
export function resolveColor(token: string): string {
  if (typeof document === "undefined") return token;
  if (!token.startsWith("var(")) return token;
  const name = token.slice(4, -1).trim();
  const value = getComputedStyle(document.documentElement).getPropertyValue(name);
  return value.trim() || token;
}

/** Presentation-only number formatting; storage and API precision are intact. */
export function formatChartValue(value: unknown): string {
  if (value === null || value === undefined || value === "-") return "—";
  const numeric = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(numeric)) return String(value);
  return new Intl.NumberFormat(undefined, {
    maximumFractionDigits: Number.isInteger(numeric) ? 0 : 2,
  }).format(numeric);
}

function tooltip(): Record<string, unknown> {
  return { trigger: "axis", valueFormatter: formatChartValue };
}

function floorBucket(date: Date, granularity: BucketRange["granularity"]): Date {
  const bucket = new Date(date);
  if (granularity === "hourly") {
    bucket.setMinutes(0, 0, 0);
  } else if (granularity === "daily") {
    bucket.setHours(0, 0, 0, 0);
  } else if (granularity === "weekly") {
    bucket.setHours(0, 0, 0, 0);
    const daysSinceMonday = (bucket.getDay() + 6) % 7;
    bucket.setDate(bucket.getDate() - daysSinceMonday);
  } else {
    bucket.setHours(0, 0, 0, 0);
    bucket.setDate(1);
  }
  return bucket;
}

function nextBucket(date: Date, granularity: BucketRange["granularity"]): Date {
  if (granularity === "hourly") return new Date(date.getTime() + 60 * 60 * 1000);
  const next = new Date(date);
  if (granularity === "daily") next.setDate(next.getDate() + 1);
  else if (granularity === "weekly") next.setDate(next.getDate() + 7);
  else next.setMonth(next.getMonth() + 1);
  return next;
}

function bucketLabel(date: Date, range: BucketRange): string {
  if (range.granularity === "hourly") {
    return new Intl.DateTimeFormat(undefined, {
      timeZone: range.timezone,
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    }).format(date);
  }
  if (range.granularity === "monthly") {
    return new Intl.DateTimeFormat(undefined, {
      timeZone: range.timezone,
      month: "short",
      year: "2-digit",
    }).format(date);
  }
  return new Intl.DateTimeFormat(undefined, {
    timeZone: range.timezone,
    month: "short",
    day: "numeric",
  }).format(date);
}

/**
 * Return every bucket intersecting [from,to). Sparse source rows are merged
 * onto this axis later; a missing row stays null rather than becoming zero.
 */
export function bucketAxis(range: BucketRange): { starts: Date[]; labels: string[] } {
  const from = new Date(range.from);
  const to = new Date(range.to);
  if (!Number.isFinite(from.getTime()) || !Number.isFinite(to.getTime()) || to <= from) {
    return { starts: [], labels: [] };
  }
  const starts: Date[] = [];
  for (let cursor = floorBucket(from, range.granularity); cursor < to; cursor = nextBucket(cursor, range.granularity)) {
    starts.push(cursor);
    if (starts.length > 2000) break;
  }
  const labels = starts.map((start) => bucketLabel(start, range));
  if (range.granularity === "hourly") {
    const duplicates = new Set(labels.filter((label, index) => labels.indexOf(label) !== index));
    starts.forEach((start, index) => {
      if (!duplicates.has(labels[index])) return;
      const offset = new Intl.DateTimeFormat(undefined, {
        timeZone: range.timezone,
        timeZoneName: "shortOffset",
      })
        .formatToParts(start)
        .find((part) => part.type === "timeZoneName")?.value;
      labels[index] = `${labels[index]} ${offset ?? ""}`.trim();
    });
  }
  return { starts, labels };
}

function bucketedSeries<T extends BucketedRow>(
  range: BucketRange,
  rows: readonly T[],
  series: readonly BucketSeriesSpec<T>[],
): { categories: string[]; series: SeriesSpec[] } {
  const axis = bucketAxis(range);
  const rowsByBucket = new Map(rows.map((row) => [new Date(row.day).getTime(), row]));
  return {
    categories: axis.labels,
    series: series.map(({ value, ...spec }) => ({
      ...spec,
      data: axis.starts.map((start) => {
        const row = rowsByBucket.get(start.getTime());
        return row ? (value(row) ?? null) : null;
      }),
    })),
  };
}

/** A dated line/bar chart with a category x-axis of day/hour labels. */
export function timeSeriesOption(categories: string[], series: SeriesSpec[]): Record<string, unknown> {
  return {
    color: series.map((s, i) => s.color ?? PALETTE[i % PALETTE.length]),
    grid: { left: 48, right: 16, top: 32, bottom: 32 },
    tooltip: tooltip(),
    legend: series.length > 1 ? { top: 0, textStyle: { color: "var(--text-muted)" } } : undefined,
    xAxis: {
      type: "category",
      data: categories,
      axisLine: { lineStyle: { color: "var(--border)" } },
      axisLabel: { color: "var(--text-faint)" },
    },
    yAxis: {
      type: "value",
      splitLine: { lineStyle: { color: "var(--hairline)" } },
      axisLabel: { color: "var(--text-faint)" },
    },
    series: series.map((s) => ({
      name: s.name,
      type: s.type ?? "line",
      data: s.data,
      smooth: false,
      showSymbol: s.data.length <= 60,
      yAxisIndex: s.yAxisIndex,
      stack: s.stack,
      connectNulls: false,
      areaStyle: s.type === "bar" ? undefined : undefined,
    })),
  };
}

/** Dense calendar axis over sparse API rows, retaining missing != zero. */
export function bucketedTimeSeriesOption<T extends BucketedRow>(
  range: BucketRange,
  rows: readonly T[],
  series: readonly BucketSeriesSpec<T>[],
): Record<string, unknown> {
  const dense = bucketedSeries(range, rows, series);
  return timeSeriesOption(dense.categories, dense.series);
}

/** A horizontal funnel-style bar chart over canonical lifecycle stages. */
export function funnelBarOption(stages: string[], counts: number[]): Record<string, unknown> {
  return {
    color: ["var(--accent-purple)"],
    grid: { left: 140, right: 32, top: 16, bottom: 24 },
    tooltip: { ...tooltip(), axisPointer: { type: "shadow" } },
    xAxis: {
      type: "value",
      splitLine: { lineStyle: { color: "var(--hairline)" } },
      axisLabel: { color: "var(--text-faint)" },
    },
    yAxis: {
      type: "category",
      data: stages,
      axisLine: { lineStyle: { color: "var(--border)" } },
      axisLabel: { color: "var(--text-primary)" },
    },
    series: [{ type: "bar", data: counts, barMaxWidth: 24 }],
  };
}

/** A stacked bar chart (e.g. pass/fail counts per day). */
export function stackedBarOption(categories: string[], series: SeriesSpec[]): Record<string, unknown> {
  return {
    color: series.map((s, i) => s.color ?? PALETTE[i % PALETTE.length]),
    grid: { left: 48, right: 16, top: 32, bottom: 32 },
    tooltip: { ...tooltip(), axisPointer: { type: "shadow" } },
    legend: { top: 0, textStyle: { color: "var(--text-muted)" } },
    xAxis: {
      type: "category",
      data: categories,
      axisLine: { lineStyle: { color: "var(--border)" } },
      axisLabel: { color: "var(--text-faint)" },
    },
    yAxis: {
      type: "value",
      splitLine: { lineStyle: { color: "var(--hairline)" } },
      axisLabel: { color: "var(--text-faint)" },
    },
    series: series.map((s) => ({
      name: s.name,
      type: "bar",
      stack: s.stack ?? "total",
      data: s.data,
    })),
  };
}

export function bucketedStackedBarOption<T extends BucketedRow>(
  range: BucketRange,
  rows: readonly T[],
  series: readonly BucketSeriesSpec<T>[],
): Record<string, unknown> {
  const dense = bucketedSeries(range, rows, series);
  return stackedBarOption(dense.categories, dense.series);
}

/** Metadata-only event dots over time; the adjacent table remains the exact accessible equivalent. */
export function eventTimelineOption(
  rows: readonly { observed_at: string; assertion_kind: string }[],
  kinds: readonly string[],
): Record<string, unknown> {
  return {
    color: kinds.map((_, index) => PALETTE[index % PALETTE.length]),
    grid: { left: 100, right: 20, top: 24, bottom: 48 },
    tooltip: { trigger: "item" },
    xAxis: {
      type: "time",
      axisLine: { lineStyle: { color: "var(--border)" } },
      axisLabel: { color: "var(--text-faint)" },
    },
    yAxis: {
      type: "category",
      data: kinds,
      axisLine: { lineStyle: { color: "var(--border)" } },
      axisLabel: { color: "var(--text-primary)" },
    },
    series: kinds.map((kind) => ({
      name: kind,
      type: "scatter",
      symbolSize: 9,
      data: rows
        .filter((row) => row.assertion_kind === kind)
        .map((row) => [row.observed_at, kind]),
    })),
  };
}

/** Horizontal ranking bars for a bounded top-N component distribution. */
export function rankingBarOption(
  labels: readonly string[],
  values: readonly number[],
): Record<string, unknown> {
  return {
    color: ["var(--accent-purple)"],
    grid: { left: 160, right: 32, top: 16, bottom: 32 },
    tooltip: { trigger: "axis", axisPointer: { type: "shadow" }, valueFormatter: formatChartValue },
    xAxis: {
      type: "value",
      minInterval: 1,
      splitLine: { lineStyle: { color: "var(--hairline)" } },
      axisLabel: { color: "var(--text-faint)" },
    },
    yAxis: {
      type: "category",
      inverse: true,
      data: labels,
      axisLine: { lineStyle: { color: "var(--border)" } },
      axisLabel: { color: "var(--text-primary)", width: 140, overflow: "truncate" },
    },
    series: [{ type: "bar", data: values, barMaxWidth: 24 }],
  };
}
