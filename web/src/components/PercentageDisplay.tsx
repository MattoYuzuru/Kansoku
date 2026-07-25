/*
 * PercentageDisplay — enforces the Engineering Proposal rule that a percentage
 * is never shown as a bare number: it always carries its raw numerator /
 * denominator, the formula version that produced it, the sample size, and the
 * completeness state. A ratio with a zero denominator renders as the
 * "not_observed" state rather than "0%" or "NaN".
 */
import { StatusBadge } from "./StatusBadge";
import type { ViewState } from "../api/client";
import "./PercentageDisplay.css";

export interface PercentageDisplayProps {
  numerator: number;
  denominator: number;
  formulaVersion?: string;
  /** Sample size backing the ratio (often == denominator, but not always). */
  sampleSize?: number;
  /** Completeness of the underlying data; drives the trailing status badge. */
  completeness?: Exclude<ViewState, "loading">;
  className?: string;
}

export function PercentageDisplay({
  numerator,
  denominator,
  formulaVersion,
  sampleSize,
  completeness,
  className,
}: PercentageDisplayProps) {
  const hasDenominator = denominator > 0;
  const pct = hasDenominator ? (100 * numerator) / denominator : null;

  return (
    <span className={`k-pct${className ? " " + className : ""}`}>
      {pct === null ? (
        <StatusBadge state="not_observed" glyphOnly />
      ) : (
        <span className="k-pct__value tabular" aria-hidden="true">
          {pct.toFixed(1)}%
        </span>
      )}
      <span className="k-pct__raw tabular">
        {numerator.toLocaleString()} / {denominator.toLocaleString()}
      </span>
      {(formulaVersion || sampleSize !== undefined) && (
        <span className="k-pct__meta">
          {formulaVersion && <span className="k-pct__formula">f{formulaVersion}</span>}
          {sampleSize !== undefined && (
            <span className="k-pct__n">n={sampleSize.toLocaleString()}</span>
          )}
        </span>
      )}
      {completeness && completeness !== "complete" && (
        <StatusBadge state={completeness} glyphOnly />
      )}
    </span>
  );
}
