import { useMemo } from "react";
import { Link } from "wouter";
import { deriveViewState, type ViewState } from "../api/client";
import { usePlugins } from "../api/queries";
import type { PluginObservatoryRow } from "../api/types";
import { DataTable, type Column } from "../components/DataTable";
import { KpiCard } from "../components/KpiCard";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { useRange } from "../hooks/useRange";

export function Plugins() {
  const range = useRange();
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const query = usePlugins(rangeParams);
  const data = query.data?.data;
  const state = deriveViewState(query.data, { isLoading: query.isLoading });
  const counts = data?.counts;
  const columns: Column<PluginObservatoryRow>[] = [
    {
      key: "name",
      header: "Plugin",
      render: (row) => (
        <Link href={`/components/plugins/${row.component_installation_id}`}>{row.declared_name}</Link>
      ),
    },
    { key: "version", header: "Version", render: (row) => row.version || row.version_state },
    { key: "scope", header: "Scope", render: (row) => `${row.source_scope} · ${row.agent_id}` },
    { key: "state", header: "State", render: (row) =>
      `${row.enabled ? "enabled" : "disabled"} · ${row.activity_state.replace("_", " ")}` },
    { key: "loads", header: "Loads", align: "right", render: (row) => row.loaded_count.toLocaleString() },
    { key: "sessions", header: "Load sessions", align: "right", render: (row) => row.loaded_sessions.toLocaleString() },
    { key: "children", header: "Bundle", align: "right", render: (row) =>
      `${row.child_count} children · ${row.child_activity_count} facts` },
    { key: "graph", header: "Graph", render: (row) => row.bundle_completeness },
    { key: "outcome", header: "Outcome", render: () => "Unsupported" },
  ];
  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Plugins</h1>
        <p className="k-page__wire t-caption">
          Versioned bundle graph, load evidence and exactly attributed child activity.
        </p>
      </header>
      <Panel title="Plugin evidence" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Installed" value={counts?.installed ?? null} state={state} />
          <KpiCard label="Enabled" value={counts?.enabled ?? null} state={state} />
          <KpiCard label="Loaded" value={counts?.loaded ?? null} state={state} />
          <KpiCard label="Active" value={counts?.active ?? null} state={(data?.completeness.status as ViewState | undefined) ?? state} />
          <KpiCard label="Cold" value={counts?.cold ?? null} state={(data?.completeness.status as ViewState | undefined) ?? state} />
        </div>
        <GapNote>
          Installation, enablement and loading are separate assertions. Active share only includes
          enabled plugins whose current child graph is complete. A child fact remains on the child
          and is summarized once only when one current plugin owner resolves exactly. Plugin
          success is unsupported.
        </GapNote>
      </Panel>
      <Panel
        title="Plugin inventory"
        caption={`Active population ${data?.population.numerator ?? 0}/${data?.population.denominator ?? 0}; exclusions ${Object.values(data?.exclusions ?? {}).reduce((a, b) => a + b, 0)}.`}
      >
        <DataTable
          columns={columns}
          rows={data?.data ?? []}
          rowKey={(row) => row.component_installation_id}
          emptyMessage={query.isLoading ? "Loading…" : "No plugins found by completed inventory targets."}
        />
      </Panel>
    </section>
  );
}
