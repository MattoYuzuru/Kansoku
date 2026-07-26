/*
 * KpiCard — a single instrument reading. Large mono number (§1.5 KPI role),
 * optional unit/delta, an optional §7 status label shown explicitly whenever
 * the state is not `complete`. Number roll-up is the ~20-line rAF counter from
 * §4 #7 (no animation library); it collapses to the final number immediately
 * under prefers-reduced-motion.
 */
import { useEffect, useRef, useState } from "react";
import { StatusBadge } from "./StatusBadge";
import type { ViewState } from "../api/client";
import "./KpiCard.css";

export interface KpiCardProps {
  label: string;
  value: number | null;
  unit?: string;
  /** Signed delta vs. the comparison period; rendered in KPI unit/delta type. */
  delta?: number;
  /** Non-`complete` states surface an explicit label (spec §7). */
  state?: Exclude<ViewState, "loading"> | "loading";
  /** Decimal places for the displayed number. */
  precision?: number;
  /** Why this KPI is not complete; exposed by the status badge tooltip. */
  stateReason?: string;
}

const prefersReducedMotion = () =>
  typeof window !== "undefined" &&
  window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;

function useCountUp(target: number | null, precision: number): number {
  const [display, setDisplay] = useState<number>(target ?? 0);
  const fromRef = useRef<number | null>(target);
  const rafRef = useRef<number>(0);

  useEffect(() => {
    if (target === null) return;
    // A value that arrives after an async load has no measured predecessor.
    // Render it immediately instead of animating from a fabricated zero. Later
    // measured-to-measured changes may still use the short count-up transition.
    if (fromRef.current === null) {
      setDisplay(target);
      fromRef.current = target;
      return;
    }
    if (prefersReducedMotion()) {
      setDisplay(target);
      fromRef.current = target;
      return;
    }
    const from = fromRef.current;
    const start = performance.now();
    const duration = 220; // §4 #7
    const tick = (now: number) => {
      // Virtual-time/headless clocks and resumed background tabs may report
      // a frame timestamp before the effect's start timestamp. Clamp both
      // bounds so a positive KPI can never animate through a negative value.
      const t = Math.max(0, Math.min(1, (now - start) / duration));
      // ease-out cubic for a settled feel
      const eased = 1 - Math.pow(1 - t, 3);
      const current = from + (target - from) * eased;
      setDisplay(Number(current.toFixed(precision)));
      if (t < 1) rafRef.current = requestAnimationFrame(tick);
      else fromRef.current = target;
    };
    rafRef.current = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(rafRef.current);
  }, [target, precision]);

  return display;
}

export function KpiCard({
  label,
  value,
  unit,
  delta,
  state = "complete",
  precision = 0,
  stateReason,
}: KpiCardProps) {
  const display = useCountUp(value, precision);
  const loading = state === "loading";

  return (
    <div className="k-kpi">
      <div className="k-kpi__label t-section-header">{label}</div>
      <div className="k-kpi__value-row">
        {loading || value === null ? (
          <span className="k-kpi__value t-kpi-number k-kpi__value--muted">—</span>
        ) : (
          <span className="k-kpi__value t-kpi-number">{display.toLocaleString()}</span>
        )}
        {unit && <span className="k-kpi__unit t-kpi-unit">{unit}</span>}
      </div>
      <div className="k-kpi__foot">
        {delta !== undefined && value !== null && (
          <span
            className={`k-kpi__delta t-kpi-unit${delta >= 0 ? " is-up" : " is-down"}`}
          >
            {delta >= 0 ? "▲" : "▼"} {Math.abs(delta).toLocaleString()}
          </span>
        )}
        {state !== "complete" && state !== "loading" && (
          <StatusBadge state={state} reason={stateReason} />
        )}
      </div>
    </div>
  );
}
