import { useMemo } from "react";
import { Link } from "wouter";
import { deriveViewState, type ViewState } from "../api/client";
import { useSkills } from "../api/queries";
import { DataTable, type Column } from "../components/DataTable";
import { GlossaryTerm } from "../components/GlossaryTerm";
import { KpiCard } from "../components/KpiCard";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { useRange } from "../hooks/useRange";
import {
  groupSkillCatalog,
  skillCatalogStats,
  type SkillCatalogRow,
} from "../lib/componentCatalog";

export function Skills() {
  const range = useRange("skills", "all_time");
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const query = useSkills(rangeParams);
  const data = query.data?.data;
  const state = deriveViewState(query.data, { isLoading: query.isLoading });
  const catalog = useMemo(() => groupSkillCatalog(data?.data ?? []), [data?.data]);
  const stats = useMemo(() => skillCatalogStats(catalog), [catalog]);
  const columns: Column<SkillCatalogRow>[] = [
    {
      key: "name",
      header: "Skill",
      render: (row) => (
        <div>
          <Link href={`/components/skills/${row.catalog_id}`}>{row.declared_name}</Link>
          <div className="t-caption" style={{ color: "var(--text-faint)" }}>
            {row.variants.length} {row.variants.length === 1 ? "variant" : "variants"}
          </div>
        </div>
      ),
    },
    { key: "agent", header: "Agent", render: (row) => row.agent_id },
    {
      key: "availability",
      header: <GlossaryTerm id="enabled">Availability</GlossaryTerm>,
      render: (row) =>
        `${row.enabled_variants}/${row.variants.length} enabled · ${row.exposed_count} exposures`,
    },
    {
      key: "invoked",
      header: <GlossaryTerm id="invoked">Invocations</GlossaryTerm>,
      align: "right",
      render: (row) => row.invoked_count.toLocaleString(),
    },
    {
      key: "loaded",
      header: <GlossaryTerm id="loaded">Loads</GlossaryTerm>,
      align: "right",
      render: (row) => row.loaded_count.toLocaleString(),
    },
    {
      key: "last",
      header: "Last invoked",
      render: (row) =>
        row.last_invoked_at ? new Date(row.last_invoked_at).toLocaleString() : "Not in range",
    },
    {
      key: "demand",
      header: <GlossaryTerm id="cold">Activity state</GlossaryTerm>,
      render: (row) =>
        row.cold_state === "not_observed"
          ? "Not enough evidence"
          : row.cold_state === "cold"
            ? "Cold"
            : "Used",
    },
  ];
  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Skills</h1>
        <p className="k-page__wire t-caption">
          One catalog row per same-named skill inside an agent, ranked by exact invocations.
        </p>
      </header>
      <Panel
        title="Skill usage"
        actions={<RangeControl range={range} />}
        caption="Counts use the selected range; the default is the five-year local retention horizon."
      >
        <div className="k-grid k-grid--kpis">
          <KpiCard
            label={<GlossaryTerm id="skill_family">Skill names</GlossaryTerm>}
            value={data ? stats.skill_families : null}
            state={state}
          />
          <KpiCard
            label={<GlossaryTerm id="component_variant">Installed variants</GlossaryTerm>}
            value={data ? stats.installed_variants : null}
            state={state}
          />
          <KpiCard
            label="Used skills"
            value={data ? stats.used_skills : null}
            state={state}
          />
          <KpiCard
            label={<GlossaryTerm id="invoked">Invocations</GlossaryTerm>}
            value={data ? stats.total_invocations : null}
            state={state}
          />
          <KpiCard
            label={<GlossaryTerm id="loaded">Loads</GlossaryTerm>}
            value={data ? stats.total_loads : null}
            state={state}
          />
          <KpiCard
            label={<GlossaryTerm id="cold">Cold skills</GlossaryTerm>}
            value={data ? stats.cold_skills : null}
            state={(data?.completeness.status as ViewState | undefined) ?? state}
          />
        </div>
        <GapNote>
          Same-named rows are folded only for browsing. Their source, profile and version identities
          remain separate variants and their historical evidence is untouched. “Used skills” counts
          catalog rows with at least one invocation; “Invocations” counts all deduplicated exact
          invocation events. Installed but unobserved skills are not classified as cold.
        </GapNote>
      </Panel>
      <Panel
        title="Skills ranked by use"
        caption={`Population ${data?.population.numerator ?? 0}/${data?.population.denominator ?? 0}; exclusions ${Object.values(data?.exclusions ?? {}).reduce((a, b) => a + b, 0)}.`}
      >
        <DataTable
          columns={columns}
          rows={catalog}
          rowKey={(row) => row.catalog_id}
          emptyMessage={query.isLoading ? "Loading…" : "No skills found by completed inventory targets."}
        />
      </Panel>
    </section>
  );
}
