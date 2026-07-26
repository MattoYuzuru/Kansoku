import { useMemo } from "react";
import type { ViewState } from "../api/client";
import { useSkillProfile } from "../api/queries";
import type { SkillAssertionRow, SkillSourceRow } from "../api/types";
import { DataTable, type Column } from "../components/DataTable";
import { KpiCard } from "../components/KpiCard";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { useRange } from "../hooks/useRange";

export function SkillDetail({ id }: { id: string }) {
  const range = useRange();
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const query = useSkillProfile(id, rangeParams);
  const profile = query.data?.data;
  const identity = profile?.identity;
  const identityState = (identity?.completeness as ViewState | undefined) ?? "unknown";
  const assertionColumns: Column<SkillAssertionRow>[] = [
    { key: "kind", header: "Assertion", render: (row) => row.assertion_kind },
    { key: "time", header: "Observed", render: (row) => new Date(row.observed_at).toLocaleString() },
    { key: "source", header: "Source", render: (row) => row.source_kind },
    { key: "tier", header: "Evidence", render: (row) => `${row.evidence_tier} · ${row.confidence.toFixed(2)}` },
    { key: "mode", header: "Mode", render: (row) => row.mode },
    { key: "identity", header: "Identity", render: (row) => row.identity_resolution },
    { key: "outcome", header: "Outcome", render: (row) =>
      row.terminal_contract_id ? `${row.outcome} · ${row.terminal_contract_id}` : "Unsupported" },
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
        <h1 className="t-page-title">{identity?.declared_name ?? "Skill profile"}</h1>
        <p className="k-page__wire t-caption">
          {identity ? `${identity.source_scope} · ${identity.agent_id} · ${identity.component_installation_id}` : id}
        </p>
      </header>
      <Panel title="Availability and runtime" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Exposed" value={identity?.exposed_count ?? 0} state={identityState} />
          <KpiCard label="Invoked" value={identity?.invoked_count ?? 0} state={identityState} />
          <KpiCard label="Loaded" value={identity?.loaded_count ?? 0} state={identityState} />
          <KpiCard label="Child activity" value={identity?.child_activity_count ?? 0} state={identityState} />
          <KpiCard label="Active days" value={identity?.active_days ?? 0} state={identityState} />
          <KpiCard label="Incidents" value={profile?.incident_count ?? 0} state={profile ? "complete" : "unknown"} />
        </div>
        <GapNote>
          No file-content endpoint exists. File-tree evidence below is limited to pseudonymous
          node counts, entry kinds, depth and byte counts. Skill success is not inferred from a
          session, response, hook or child tool.
        </GapNote>
      </Panel>
      <Panel title="Assertion timeline">
        <DataTable columns={assertionColumns} rows={profile?.assertions ?? []} rowKey={(row) => row.assertion_id} />
      </Panel>
      <Panel title="Source and evidence matrix">
        <DataTable columns={sourceColumns} rows={profile?.sources ?? []} rowKey={(row) => row.source_instance_id} />
      </Panel>
      <Panel title="File-tree metadata only">
        <p className="t-body">
          {(profile?.file_tree ?? []).map((row) =>
            `${row.file_count} files · ${row.directory_count} directories · depth ${row.max_depth} · ${row.total_bytes} bytes`,
          ).join(" | ") || "Not observed"}
        </p>
      </Panel>
    </section>
  );
}
