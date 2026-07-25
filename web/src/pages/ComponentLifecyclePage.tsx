/*
 * Shared implementation for /components/skills and /components/plugins:
 * both wireframes reduce to the same component_lifecycle_funnel query with a
 * component_kind filter (metric_family=skill|plugin), so this one component
 * is parameterized by kind + title rather than duplicated per route.
 *
 * "Cold/unused reasons" and the "evidence table" from the wireframes are not
 * buildable: the funnel only reports stage/component_count/event_count, with
 * no reason-code or evidence-row dimension — noted as a gap rather than
 * invented.
 */
import { useMemo } from "react";
import { KpiCard } from "../components/KpiCard";
import { ChartContainer } from "../components/ChartContainer";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { deriveViewState } from "../api/client";
import { useComponentLifecycleFunnel } from "../api/queries";
import { useRange } from "../hooks/useRange";
import { funnelBarOption } from "../components/chartOptions";

const STAGE_LABELS: Record<string, string> = {
  opportunity_detected: "Opportunity detected",
  installed: "Installed",
  enabled: "Enabled",
  exposed: "Exposed",
  invoked: "Invoked",
  loaded: "Loaded",
  executed: "Executed",
  succeeded: "Succeeded",
};

export interface ComponentLifecyclePageProps {
  title: string;
  wireframe: string;
  componentKind: "skill" | "plugin";
  /** Extra sentence appended to the gap note for kind-specific wireframe asks. */
  extraGapNote?: string;
}

export function ComponentLifecyclePage({
  title,
  wireframe,
  componentKind,
  extraGapNote,
}: ComponentLifecyclePageProps) {
  const range = useRange();
  const rangeParams = useMemo(() => ({ from: range.from, to: range.to }), [range.from, range.to]);
  const funnel = useComponentLifecycleFunnel(rangeParams, componentKind);

  const rows = funnel.data?.data?.data ?? [];
  const state = deriveViewState(funnel.data, { isLoading: funnel.isLoading });
  const installed = rows.find((r) => r.stage === "installed")?.component_count ?? 0;
  const enabled = rows.find((r) => r.stage === "enabled")?.component_count ?? 0;
  const invoked = rows.find((r) => r.stage === "invoked")?.component_count ?? 0;
  const succeeded = rows.find((r) => r.stage === "succeeded")?.component_count ?? 0;

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">{title}</h1>
        <p className="k-page__wire t-caption">{wireframe}</p>
      </header>

      <Panel title="Lifecycle funnel" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Installed" value={installed} state={state} />
          <KpiCard
            label="Activation ratio"
            value={installed > 0 ? Math.round((100 * enabled) / installed) : null}
            unit="%"
            state={installed > 0 ? state : "not_observed"}
          />
          <KpiCard
            label="Success ratio"
            value={invoked > 0 ? Math.round((100 * succeeded) / invoked) : null}
            unit="%"
            state={invoked > 0 ? state : "not_observed"}
          />
        </div>
        {rows.length > 0 ? (
          <ChartContainer
            ariaLabel={`${title} lifecycle funnel by stage`}
            option={funnelBarOption(
              rows.map((r) => STAGE_LABELS[r.stage] ?? r.stage),
              rows.map((r) => r.component_count),
            )}
          />
        ) : (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            {funnel.isLoading ? "Loading…" : `No ${componentKind} lifecycle events observed in this range.`}
          </p>
        )}
        <GapNote>
          Cold/unused reason codes and a per-component evidence table are not shown: the
          lifecycle funnel reports stage/component/event counts only, with no
          reason-code or evidence-row dimension available to build either honestly.
          {extraGapNote ? ` ${extraGapNote}` : ""}
        </GapNote>
      </Panel>
    </section>
  );
}
