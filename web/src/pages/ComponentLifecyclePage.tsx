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
import { DataTable, type Column } from "../components/DataTable";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { deriveViewState, type ViewState } from "../api/client";
import { useComponentInventory, useComponentLifecycleFunnel } from "../api/queries";
import { useRange } from "../hooks/useRange";
import { funnelBarOption } from "../components/chartOptions";
import type { InventoryComponentRow } from "../api/types";

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
  const inventory = useComponentInventory(componentKind);

  const rows = funnel.data?.data?.data ?? [];
  const state = deriveViewState(funnel.data, { isLoading: funnel.isLoading });
  const installedRow = rows.find((r) => r.stage === "installed");
  const enabledRow = rows.find((r) => r.stage === "enabled");
  const executedRow = rows.find((r) => r.stage === "executed");
  const succeededRow = rows.find((r) => r.stage === "succeeded");
  const installed = installedRow?.component_count ?? 0;
  const enabled = enabledRow?.component_count ?? 0;
  const executed = executedRow?.component_count ?? 0;
  const succeeded = succeededRow?.component_count ?? 0;
  const rowState = (valueState: string | undefined): ViewState =>
    funnel.isLoading ? "loading" : ((valueState as ViewState | undefined) ?? state);
  const inventoryColumns: Column<InventoryComponentRow>[] = [
    { key: "declared_name", header: "Component", render: (row) => row.declared_name },
    { key: "agent_id", header: "Agent", render: (row) => row.agent_id },
    { key: "source_scope", header: "Scope", render: (row) => row.source_scope },
    {
      key: "version",
      header: "Version",
      render: (row) => row.version_state === "observed" ? row.version : "Not observed",
    },
    { key: "enabled", header: "State", render: (row) => row.enabled ? "Enabled" : "Disabled" },
  ];

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">{title}</h1>
        <p className="k-page__wire t-caption">{wireframe}</p>
      </header>

      <Panel title="Lifecycle funnel" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Installed" value={installed} state={rowState(installedRow?.value_state)} />
          <KpiCard
            label="Enablement ratio"
            value={installed > 0 ? Math.round((100 * enabled) / installed) : null}
            unit="%"
            state={installed > 0 ? rowState(enabledRow?.value_state) : "not_observed"}
          />
          <KpiCard
            label="Success ratio"
            value={executed > 0 ? Math.round((100 * succeeded) / executed) : null}
            unit="%"
            state={executed > 0 ? rowState(succeededRow?.value_state) : "not_observed"}
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
          Installed and enabled come from the latest bounded, read-only inventory
          snapshot in this range. Exposed, invoked, loaded, executed, and succeeded
          require runtime evidence; zero with “Not observed” means no qualifying
          native or reconstructed signal was seen, not that the component failed.{" "}
          Cold/unused reason codes are not shown: neither inventory nor native
          lifecycle telemetry provides a bounded reason-code dimension.
          {extraGapNote ? ` ${extraGapNote}` : ""}
        </GapNote>
      </Panel>

      <Panel
        title="Current inventory"
        caption="Declared names and state from the latest bounded read-only scan; raw paths and manifest content are never retained."
      >
        <DataTable
          columns={inventoryColumns}
          rows={inventory.data?.data?.data ?? []}
          rowKey={(row) => row.component_id}
          emptyMessage={
            inventory.isLoading
              ? "Loading…"
              : `No ${componentKind} components found by completed inventory targets.`
          }
        />
      </Panel>
    </section>
  );
}
