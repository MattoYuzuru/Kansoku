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

/** A dated line/bar chart with a category x-axis of day/hour labels. */
export function timeSeriesOption(categories: string[], series: SeriesSpec[]): Record<string, unknown> {
  return {
    color: series.map((s, i) => s.color ?? PALETTE[i % PALETTE.length]),
    grid: { left: 48, right: 16, top: 32, bottom: 32 },
    tooltip: { trigger: "axis" },
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

/** A horizontal funnel-style bar chart over canonical lifecycle stages. */
export function funnelBarOption(stages: string[], counts: number[]): Record<string, unknown> {
  return {
    color: ["var(--accent-purple)"],
    grid: { left: 140, right: 32, top: 16, bottom: 24 },
    tooltip: { trigger: "axis", axisPointer: { type: "shadow" } },
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
    tooltip: { trigger: "axis", axisPointer: { type: "shadow" } },
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
