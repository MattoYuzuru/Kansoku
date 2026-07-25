/*
 * ChartContainer — a thin Apache ECharts wrapper. ECharts is imported
 * dynamically (import()) so it lands in its own code-split chunk and never
 * loads with the initial shell; a page that renders no chart pays nothing for
 * it. Honors prefers-reduced-motion by disabling ECharts' own animation flag
 * (§4 #4: reduced-motion => animation:false, final frame direct).
 *
 * This is scaffold infrastructure: pages (a later task) pass a fully-formed
 * ECharts `option`. It resizes with its container and disposes on unmount.
 */
import { useEffect, useRef, useState } from "react";
import "./ChartContainer.css";

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

  // Apply/refresh option whenever it changes.
  useEffect(() => {
    if (!ready || !chartRef.current) return;
    const withMotion: EChartsOption = {
      ...option,
      animation: reduceMotion() ? false : (option.animation ?? true),
      animationDuration: reduceMotion() ? 0 : (option.animationDuration ?? 400),
    };
    chartRef.current.setOption(withMotion, { notMerge: true });
  }, [option, ready]);

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
