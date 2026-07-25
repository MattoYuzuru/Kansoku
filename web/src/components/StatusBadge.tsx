/*
 * StatusBadge — the 8-state view-state vocabulary (§7). Every state carries a
 * glyph + shape + text label; color is reinforcement only, so the badge stays
 * legible in monochrome and for colorblind users. Glyph precedes the label,
 * matching "glyph precedes value in tables" from the spec.
 */
import type { ViewState } from "../api/client";
import "./StatusBadge.css";

type StatusState = Exclude<ViewState, "loading">;

interface StatusSpec {
  glyph: string;
  label: string;
  colorVar: string;
}

// Glyph/shape/label/color exactly per §7. The glyphs are chosen so fill
// fraction, outline style and overlay differ enough to read in monochrome.
const SPEC: Record<StatusState, StatusSpec> = {
  complete: { glyph: "●", label: "Complete", colorVar: "var(--status-complete)" }, // ● filled
  partial: { glyph: "◑", label: "Partial", colorVar: "var(--status-partial)" }, // ◑ right-fill
  degraded: { glyph: "◐", label: "Degraded", colorVar: "var(--status-degraded)" }, // ◐ left-fill + ! overlay
  unsupported: { glyph: "○", label: "Unsupported", colorVar: "var(--status-unsupported)" }, // ○ hollow
  not_observed: { glyph: "◌", label: "Not observed", colorVar: "var(--status-not-observed)" }, // ◌ dotted
  redacted: { glyph: "▨", label: "Redacted", colorVar: "var(--status-redacted)" }, // ▨ cross-hatched
  unknown: { glyph: "?", label: "Unknown", colorVar: "var(--status-unknown)" }, // explicit unknown marker
  numeric_zero: { glyph: "0", label: "Zero", colorVar: "var(--status-zero)" }, // mono zero
};

export interface StatusBadgeProps {
  state: StatusState;
  /** Glyph-only mode (e.g. leading a table cell); still exposes the label to AT. */
  glyphOnly?: boolean;
  className?: string;
  /** Concrete panel-specific explanation. Falls back to the glossary meaning. */
  reason?: string;
}

const DEFAULT_REASON: Record<StatusState, string> = {
  complete: "All required evidence for this value is available.",
  partial: "Some eligible evidence is available; a bounded portion is missing.",
  degraded: "A known source, schema, parser, reconciliation, or freshness failure affects this value.",
  unsupported: "The active source does not provide a reliable signal for this value.",
  not_observed: "The signal is observable, but no qualifying event was seen in this range.",
  redacted: "The source value was intentionally removed by the privacy policy.",
  unknown: "Kansoku cannot establish the value or its denominator from the available evidence.",
  numeric_zero: "A complete eligible population was measured and the result is exactly zero.",
};

export function StatusBadge({ state, glyphOnly = false, className, reason }: StatusBadgeProps) {
  const spec = SPEC[state];
  const explanation = reason ?? DEFAULT_REASON[state];
  return (
    <span
      className={`k-status k-status--${state}${className ? " " + className : ""}`}
      style={{ color: spec.colorVar }}
      role="img"
      aria-label={`${spec.label}: ${explanation}`}
      title={explanation}
    >
      <span className="k-status__glyph" aria-hidden="true">
        {spec.glyph}
        {state === "degraded" && <span className="k-status__overlay" aria-hidden="true">!</span>}
      </span>
      {!glyphOnly && <span className="k-status__label">{spec.label}</span>}
    </span>
  );
}

export function statusLabel(state: StatusState): string {
  return SPEC[state].label;
}
