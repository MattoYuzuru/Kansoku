import { useMemo } from "react";
import { Link } from "wouter";
import { deriveViewState, type ViewState } from "../api/client";
import { usePlugins } from "../api/queries";
import { DataTable, type Column } from "../components/DataTable";
import { GlossaryTerm } from "../components/GlossaryTerm";
import { KpiCard } from "../components/KpiCard";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { useRange } from "../hooks/useRange";
import {
  groupPluginCatalog,
  pluginCatalogStats,
  type PluginCatalogRow,
} from "../lib/componentCatalog";

export function Plugins() {
  const range = useRange("all_time");
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const query = usePlugins(rangeParams);
  const data = query.data?.data;
  const state = deriveViewState(query.data, { isLoading: query.isLoading });
  const catalog = useMemo(() => groupPluginCatalog(data?.data ?? []), [data?.data]);
  const stats = useMemo(() => pluginCatalogStats(catalog), [catalog]);
  const columns: Column<PluginCatalogRow>[] = [
    {
      key: "name",
      header: "Plugin",
      render: (row) => (
        <div>
          <Link href={`/components/plugins/${row.catalog_id}`}>{row.declared_name}</Link>
          <div className="t-caption" style={{ color: "var(--text-faint)" }}>
            {row.variants.length} {row.variants.length === 1 ? "variant" : "variants"}
          </div>
        </div>
      ),
    },
    { key: "agent", header: "Agent", render: (row) => row.agent_id },
    {
      key: "version",
      header: "Version",
      render: (row) => row.versions.length === 1 ? row.versions[0] : `${row.versions.length} versions`,
    },
    {
      key: "state",
      header: <GlossaryTerm id="active_plugin">Activity state</GlossaryTerm>,
      render: (row) =>
        `${row.enabled_variants}/${row.variants.length} enabled · ${row.activity_state.replace("_", " ")}`,
    },
    { key: "loads", header: "Loads", align: "right", render: (row) => row.loaded_count.toLocaleString() },
    {
      key: "children",
      header: <GlossaryTerm id="child_activity">Exact child uses</GlossaryTerm>,
      align: "right",
      render: (row) => row.child_activity_count.toLocaleString(),
    },
    {
      key: "bundle",
      header: "Bundle evidence",
      render: (row) =>
        `${row.child_count_bounds.lower}` +
        (row.child_count_bounds.lower === row.child_count_bounds.upper
          ? " children"
          : `–${row.child_count_bounds.upper} variant children`) +
        ` · ${row.bundle_completeness}`,
    },
    {
      key: "last",
      header: "Last load",
      render: (row) => row.last_loaded_at ? new Date(row.last_loaded_at).toLocaleString() : "Not in range",
    },
  ];
  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Plugins</h1>
        <p className="k-page__wire t-caption">
          One catalog row per same-named plugin inside an agent, ranked by exact child use.
        </p>
      </header>
      <Panel
        title="Plugin usage"
        actions={<RangeControl range={range} />}
        caption="Counts use the selected range; the default is the five-year local retention horizon."
      >
        <div className="k-grid k-grid--kpis">
          <KpiCard
            label="Plugin names"
            value={data ? stats.plugin_families : null}
            state={state}
          />
          <KpiCard
            label={<GlossaryTerm id="component_variant">Installed variants</GlossaryTerm>}
            value={data ? stats.installed_variants : null}
            state={state}
          />
          <KpiCard
            label={<GlossaryTerm id="active_plugin">Active plugins</GlossaryTerm>}
            value={data ? stats.active_plugins : null}
            state={(data?.completeness.status as ViewState | undefined) ?? state}
          />
          <KpiCard
            label={<GlossaryTerm id="child_activity">Exact child uses</GlossaryTerm>}
            value={data ? stats.total_child_uses : null}
            state={state}
          />
          <KpiCard
            label={<GlossaryTerm id="loaded">Plugin loads</GlossaryTerm>}
            value={data ? stats.total_loads : null}
            state={state}
          />
          <KpiCard
            label={<GlossaryTerm id="cold">Cold plugins</GlossaryTerm>}
            value={data ? stats.cold_plugins : null}
            state={(data?.completeness.status as ViewState | undefined) ?? state}
          />
        </div>
        <GapNote>
          Same-named rows are folded only for browsing; every marketplace, cache, profile and
          version identity remains a visible variant. A plugin is active only when Kansoku observed
          a plugin load or could attribute a child action to exactly one current owner. Child
          outcomes are never converted into plugin-level success.
        </GapNote>
      </Panel>
      <Panel
        title="Plugins ranked by activity"
        caption={`Active population ${data?.population.numerator ?? 0}/${data?.population.denominator ?? 0}; exclusions ${Object.values(data?.exclusions ?? {}).reduce((a, b) => a + b, 0)}.`}
      >
        <DataTable
          columns={columns}
          rows={catalog}
          rowKey={(row) => row.catalog_id}
          emptyMessage={query.isLoading ? "Loading…" : "No plugins found by completed inventory targets."}
        />
      </Panel>
    </section>
  );
}
