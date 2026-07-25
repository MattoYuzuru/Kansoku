/*
 * ChartContainer — a thin Apache ECharts wrapper. ECharts is imported
 * dynamically (import()) so it lands in its own code-split chunk and never
 * loads with the initial shell; a page that renders no chart pays nothing for
 * it. Honors prefers-reduced-motion by disabling ECharts' own animation flag
 * (§4 #4: reduced-motion => animation:false, final frame direct).
 *
 * Pages pass `var(--x)` design-token strings for any color field instead of
 * concrete hex — this component resolves them to live computed colors right
 * before every `setOption` call (see resolveOptionTokens below) and re-runs
 * that resolution whenever the active theme/preset/custom-accent changes, not
 * just when the `option` prop itself changes. Resolving at build time (in the
 * page) would bake a stale hex into ECharts' canvas that never updates until
 * the page remounts — this is why that resolution lives here instead.
 *
 * This is scaffold infrastructure: pages (a later task) pass a fully-formed
 * ECharts `option`. It resizes with its container and disposes on unmount.
 */
import { useEffect, useRef, useState } from "react";
import { resolveColor } from "./chartOptions";
import { useTheme } from "../theme";
import "./ChartContainer.css";

/** Deep-walk a plain option tree, resolving any `var(--x)` string leaf to its live computed color. */
function resolveOptionTokens<T>(value: T): T {
  if (typeof value === "string") {
    return (value.startsWith("var(") ? resolveColor(value) : value) as T;
  }
  if (Array.isArray(value)) {
    return value.map(resolveOptionTokens) as T;
  }
  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value).map(([k, v]) => [k, resolveOptionTokens(v)]),
    ) as T;
  }
  return value;
}

// Minimal structural type so the shell chunk never imports echarts' types.
type EChartsOption = Record<string, unknown>;
interface EChartsInstance {
  setOption(option: EChartsOption, opts?: { notMerge?: boolean }): void;
  resize(): void;
  dispose(): void;
}

export interface ChartContainerProps {
  option: EChartsOption;
  /** CSS height; charts need an explicit box. */
  height?: number | string;
  className?: string;
  ariaLabel: string;
}

const reduceMotion = () =>
  typeof window !== "undefined" &&
  window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;

export function ChartContainer({
  option,
  height = 280,
  className,
  ariaLabel,
}: ChartContainerProps) {
  const hostRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<EChartsInstance | null>(null);
  const [ready, setReady] = useState(false);
  const { appearance } = useTheme();
  // Changes whenever anything resolveOptionTokens cares about changes, even if
  // `option` itself didn't (e.g. the page never re-rendered on theme change).
  const paletteKey = [
    appearance.theme,
    appearance.accentPurple,
    appearance.accentGold,
    appearance.accentPurpleLight,
    appearance.accentGoldLight,
  ].join("|");
  const prevOptionRef = useRef<EChartsOption | null>(null);

  // Lazy-init the chart the first time the host mounts.
  useEffect(() => {
    let disposed = false;
    const host = hostRef.current;
    if (!host) return;
    void (async () => {
      const echarts = await import("echarts");
      if (disposed || !hostRef.current) return;
      const instance = echarts.init(hostRef.current) as unknown as EChartsInstance;
      chartRef.current = instance;
      setReady(true);
    })();
    return () => {
      disposed = true;
      chartRef.current?.dispose();
      chartRef.current = null;
    };
  }, []);

  // Apply/refresh option whenever it (or the live theme/preset/accent) changes.
  useEffect(() => {
    if (!ready || !chartRef.current) return;
    const resolved = resolveOptionTokens(option);
    // A pure recolor (option reference unchanged, only paletteKey fired the
    // effect) shouldn't replay the entrance animation — just repaint colors.
    const isPureRecolor = prevOptionRef.current === option;
    prevOptionRef.current = option;
    const withMotion: EChartsOption = {
      ...resolved,
      animation: reduceMotion() ? false : (option.animation ?? true),
      animationDuration: reduceMotion() || isPureRecolor ? 0 : (option.animationDuration ?? 400),
    };
    chartRef.current.setOption(withMotion, { notMerge: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [option, ready, paletteKey]);

  // Resize with the container.
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const ro = new ResizeObserver(() => chartRef.current?.resize());
    ro.observe(host);
    return () => ro.disconnect();
  }, []);

  return (
    <div
      ref={hostRef}
      className={`k-chart${className ? " " + className : ""}`}
      style={{ height: typeof height === "number" ? `${height}px` : height }}
      role="img"
      aria-label={ariaLabel}
    />
  );
}
