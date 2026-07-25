/*
 * Panel — a titled section container matching the k-page__pending visual
 * convention already established by Placeholder.css (hairline border,
 * --surface fill, --space-5 padding), generalized into a reusable shell every
 * page uses per contracts/dashboard.yaml panel. UnsupportedPanel is the
 * explicit "no durable backing table" rendering the task's honesty
 * convention requires whenever every metric in a panel is unsupported.
 */
import type { ReactNode } from "react";
import { StatusBadge } from "./StatusBadge";
import "./Panel.css";

export interface PanelProps {
  title: string;
  /** Optional trailing controls (e.g. a RangeControl or in-panel filter). */
  actions?: ReactNode;
  /** Optional muted caption under the title (e.g. a data-provenance note). */
  caption?: ReactNode;
  children: ReactNode;
  className?: string;
}

export function Panel({ title, actions, caption, children, className }: PanelProps) {
  return (
    <section className={`k-panel${className ? " " + className : ""}`}>
      <header className="k-panel__head">
        <div className="k-panel__title-row">
          <h2 className="t-section-header k-panel__title">{title}</h2>
          {actions && <div className="k-panel__actions">{actions}</div>}
        </div>
        {caption && <p className="k-panel__caption t-caption">{caption}</p>}
      </header>
      <div className="k-panel__body">{children}</div>
    </section>
  );
}

export interface UnsupportedPanelProps {
  title: string;
  /** One calm sentence explaining why (per task's honesty convention). */
  reason: ReactNode;
}

/** Renders an entire panel as unsupported when every declared metric lacks a
 * durable backing table — never a fabricated number. */
export function UnsupportedPanel({ title, reason }: UnsupportedPanelProps) {
  return (
    <Panel title={title}>
      <div className="k-panel__unsupported">
        <StatusBadge state="unsupported" />
        <p className="t-body">{reason}</p>
      </div>
    </Panel>
  );
}

/** A muted note for a declared-but-unbuildable feature (e.g. an hourly
 * heatmap the API's daily granularity cannot support) — states the gap
 * rather than fabricating data to fill it. */
export function GapNote({ children }: { children: ReactNode }) {
  return <p className="k-panel__gap t-caption">{children}</p>;
}
