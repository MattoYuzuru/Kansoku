import { useMemo } from "react";
import { Link } from "wouter";
import { deriveViewState, type ViewState } from "../api/client";
import { useSkills } from "../api/queries";
import type { SkillObservatoryRow } from "../api/types";
import { DataTable, type Column } from "../components/DataTable";
import { KpiCard } from "../components/KpiCard";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { useRange } from "../hooks/useRange";

export function Skills() {
  const range = useRange();
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const query = useSkills(rangeParams);
  const data = query.data?.data;
  const state = deriveViewState(query.data, { isLoading: query.isLoading });
  const counts = data?.counts;
  const columns: Column<SkillObservatoryRow>[] = [
    {
      key: "name",
      header: "Skill",
      render: (row) => <Link href={`/components/skills/${row.component_installation_id}`}>{row.declared_name}</Link>,
    },
    { key: "scope", header: "Scope", render: (row) => row.source_scope },
    { key: "available", header: "Availability", render: (row) =>
      `${row.installed ? "installed" : "—"} · ${row.enabled ? "enabled" : "disabled"} · ${row.exposed_count > 0 ? "exposed" : "not observed"}` },
    { key: "invoked", header: "Invoked", align: "right", render: (row) => row.invoked_count.toLocaleString() },
    { key: "loaded", header: "Loaded", align: "right", render: (row) => row.loaded_count.toLocaleString() },
    { key: "sessions", header: "Sessions", align: "right", render: (row) => row.unique_sessions.toLocaleString() },
    { key: "cold", header: "Demand", render: (row) =>
      row.cold_state === "not_observed" ? "Not observed" : row.cold_state === "cold" ? "Cold" : "Used" },
    { key: "outcome", header: "Outcome", render: (row) =>
      row.outcome_state === "unsupported" ? "Unsupported" : "Observed terminal contract" },
  ];
  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Skills</h1>
        <p className="k-page__wire t-caption">
          Independent availability and runtime evidence planes with exact populations.
        </p>
      </header>
      <Panel title="Evidence planes" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Installed" value={counts?.installed ?? 0} state={state} />
          <KpiCard label="Enabled" value={counts?.enabled ?? 0} state={state} />
          <KpiCard label="Exposed" value={counts?.exposed ?? 0} state={state} />
          <KpiCard label="Invoked" value={counts?.invoked ?? 0} state={state} />
          <KpiCard label="Loaded" value={counts?.loaded ?? 0} state={state} />
          <KpiCard label="Cold" value={counts?.cold ?? 0} state={(data?.completeness.status as ViewState | undefined) ?? state} />
        </div>
        <GapNote>
          Availability and runtime are independent. “Executed” is not a universal skill state.
          Outcome is unsupported unless an assertion names a registered terminal contract.
          Cold requires a complete exposure window; installed but unexposed is not observed.
          Optimization eligibility and missed opportunities remain unsupported until Session 20.
        </GapNote>
      </Panel>
      <Panel
        title="Skill inventory and evidence"
        caption={`Population ${data?.population.numerator ?? 0}/${data?.population.denominator ?? 0}; exclusions ${Object.values(data?.exclusions ?? {}).reduce((a, b) => a + b, 0)}.`}
      >
        <DataTable
          columns={columns}
          rows={data?.data ?? []}
          rowKey={(row) => row.component_installation_id}
          emptyMessage={query.isLoading ? "Loading…" : "No skills found by completed inventory targets."}
        />
      </Panel>
    </section>
  );
}
