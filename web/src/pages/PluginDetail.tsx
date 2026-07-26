import { useMemo } from "react";
import type { ViewState } from "../api/client";
import { usePluginProfile } from "../api/queries";
import type { PluginChildRow, PluginVersionRow, SkillAssertionRow, SkillSourceRow } from "../api/types";
import { DataTable, type Column } from "../components/DataTable";
import { KpiCard } from "../components/KpiCard";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { useRange } from "../hooks/useRange";

export function PluginDetail({ id }: { id: string }) {
  const range = useRange();
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const query = usePluginProfile(id, rangeParams);
  const profile = query.data?.data;
  const identity = profile?.identity;
  const graphState = (identity?.bundle_completeness as ViewState | undefined) ?? "unknown";
  const childColumns: Column<PluginChildRow>[] = [
    { key: "kind", header: "Kind", render: (row) => row.component_kind },
    { key: "name", header: "Child", render: (row) => row.declared_name },
    { key: "version", header: "Version", render: (row) => row.version || row.version_state },
    { key: "relation", header: "Relation", render: (row) => `${row.relation_kind} · ${row.relation_completeness}` },
    { key: "usage", header: "Direct usage", align: "right", render: (row) => row.usage_count.toLocaleString() },
  ];
  const versionColumns: Column<PluginVersionRow>[] = [
    { key: "version", header: "Version", render: (row) => row.version || row.version_state },
    { key: "state", header: "State", render: (row) => row.current ? "current" : "historical" },
    { key: "seen", header: "Observed", render: (row) =>
      row.last_seen_at ? new Date(row.last_seen_at).toLocaleString() : "Not observed" },
  ];
  const assertionColumns: Column<SkillAssertionRow>[] = [
    { key: "kind", header: "Assertion", render: (row) => row.assertion_kind },
    { key: "time", header: "Observed", render: (row) => new Date(row.observed_at).toLocaleString() },
    { key: "source", header: "Source", render: (row) => row.source_kind },
    { key: "identity", header: "Identity", render: (row) => row.identity_resolution },
    { key: "outcome", header: "Outcome", render: () => "Unsupported" },
  ];
  const sourceColumns: Column<SkillSourceRow>[] = [
    { key: "source", header: "Source", render: (row) => row.source_kind },
    { key: "assertions", header: "Assertions", align: "right", render: (row) => row.assertion_count },
    { key: "exact", header: "Exact", align: "right", render: (row) => row.exact_count },
    { key: "state", header: "Completeness", render: (row) => row.completeness },
  ];
  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">{identity?.declared_name ?? "Plugin profile"}</h1>
        <p className="k-page__wire t-caption">
          {identity ? `${identity.source_scope} · ${identity.agent_id} · ${identity.version || identity.version_state}` : id}
        </p>
      </header>
      <Panel title="Bundle activity" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Loads" value={identity?.loaded_count ?? null} state={graphState} />
          <KpiCard label="Load sessions" value={identity?.loaded_sessions ?? null} state={graphState} />
          <KpiCard label="Child activity" value={identity?.child_activity_count ?? null} state={graphState} />
          <KpiCard label="Children" value={identity?.child_count ?? null} state={graphState} />
          <KpiCard label="Collisions" value={identity?.collision_count ?? null} state={graphState} />
          <KpiCard label="Incidents" value={profile?.incident_count ?? null} state={profile ? "complete" : "unknown"} />
        </div>
        <GapNote>
          This profile exposes metadata and lineage only. No plugin or child content endpoint exists.
          Child outcomes are not promoted into a plugin-level success state.
        </GapNote>
      </Panel>
      <Panel title="Current bundle tree">
        <DataTable columns={childColumns} rows={profile?.children ?? []} rowKey={(row) => `${row.relation_kind}:${row.component_id}`} />
      </Panel>
      <Panel title="Version history">
        <DataTable columns={versionColumns} rows={profile?.versions ?? []} rowKey={(row) => `${row.version_state}:${row.version ?? ""}`} />
      </Panel>
      <Panel title="Plugin assertion timeline">
        <DataTable columns={assertionColumns} rows={profile?.assertions ?? []} rowKey={(row) => row.assertion_id} />
      </Panel>
      <Panel title="Source and evidence matrix">
        <DataTable columns={sourceColumns} rows={profile?.sources ?? []} rowKey={(row) => row.source_instance_id} />
      </Panel>
    </section>
  );
}
