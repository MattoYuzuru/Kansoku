import { useMemo } from "react";
import type { ViewState } from "../api/client";
import { usePluginProfiles, usePlugins } from "../api/queries";
import type {
  PluginObservatoryRow,
  PluginVersionRow,
  SkillAssertionRow,
  SkillSourceRow,
} from "../api/types";
import { ChartContainer } from "../components/ChartContainer";
import { rankingBarOption } from "../components/chartOptions";
import { DataTable, type Column } from "../components/DataTable";
import { GlossaryTerm } from "../components/GlossaryTerm";
import { KpiCard } from "../components/KpiCard";
import { QueryErrorState } from "../components/QueryErrorState";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { useRange } from "../hooks/useRange";
import {
  groupPluginCatalog,
  mergePluginProfiles,
  type MergedPluginChildRow,
} from "../lib/componentCatalog";

const PROFILE_VARIANT_LIMIT = 8;

export function PluginDetail({ id }: { id: string }) {
  const range = useRange("plugins", "all_time");
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const listQuery = usePlugins(rangeParams);
  const inventoryRows = listQuery.data?.data?.data ?? [];
  const catalog = useMemo(() => groupPluginCatalog(inventoryRows), [inventoryRows]);
  const family = catalog.find((row) => row.catalog_id === id);
  const directVariant = inventoryRows.find((row) => row.component_installation_id === id);
  const variants = family?.variants ?? (directVariant ? [directVariant] : []);
  const queriedVariants = variants.slice(0, PROFILE_VARIANT_LIMIT);
  const profileOrdinals = useMemo(
    () =>
      new Map(
        [...new Set(variants.map((row) => row.agent_installation_id))]
          .sort()
          .map((installationID, index) => [installationID, index + 1]),
      ),
    [variants],
  );
  const variantIDs = useMemo(
    () => queriedVariants.map((row) => row.component_installation_id),
    [queriedVariants],
  );
  const profileQueries = usePluginProfiles(variantIDs, rangeParams);
  const profiles = profileQueries.flatMap((query) => query.data?.data ? [query.data.data] : []);
  const merged = useMemo(() => mergePluginProfiles(profiles), [profiles]);
  const display = family ?? (directVariant ? groupPluginCatalog([directVariant])[0] : undefined);
  const graphState = (display?.bundle_completeness as ViewState | undefined) ?? "unknown";
  const profileFailed = profileQueries.some((query) => query.isError);
  const unusedChildren = merged.children.filter((row) => row.usage_count === 0).length;
  const chartChildren = merged.children.slice(0, 12);
  const distributionOption = useMemo(
    () =>
      rankingBarOption(
        chartChildren.map((row) => row.declared_name),
        chartChildren.map((row) => row.usage_count),
      ),
    [chartChildren],
  );

  const childColumns: Column<MergedPluginChildRow>[] = [
    { key: "kind", header: "Kind", render: (row) => row.component_kind },
    { key: "name", header: "Bundled component", render: (row) => row.declared_name },
    {
      key: "version",
      header: "Version",
      render: (row) => row.versions.length === 1 ? row.versions[0] : `${row.versions.length} versions`,
    },
    {
      key: "variants",
      header: <GlossaryTerm id="component_variant">Variants</GlossaryTerm>,
      align: "right",
      render: (row) => row.variants.toLocaleString(),
    },
    {
      key: "usage",
      header: <GlossaryTerm id="child_activity">Exact uses</GlossaryTerm>,
      align: "right",
      render: (row) => row.usage_count.toLocaleString(),
    },
    {
      key: "last",
      header: "Last used",
      render: (row) =>
        row.last_activity_at ? new Date(row.last_activity_at).toLocaleString() : "Never in range",
    },
    { key: "evidence", header: "Bundle evidence", render: (row) => row.relation_completeness },
  ];
  const variantColumns: Column<PluginObservatoryRow>[] = [
    { key: "source", header: "Source", render: (row) => row.source_scope.replaceAll("_", " ") },
    { key: "version", header: "Version", render: (row) => row.version || "Not observed" },
    {
      key: "profile",
      header: "Agent / profile",
      render: (row) => `${row.agent_id} · profile ${profileOrdinals.get(row.agent_installation_id) ?? "?"}`,
    },
    {
      key: "state",
      header: "State",
      render: (row) => `${row.enabled ? "Enabled" : "Disabled"} · ${row.activity_state.replaceAll("_", " ")}`,
    },
    { key: "loads", header: "Loads", align: "right", render: (row) => row.loaded_count.toLocaleString() },
    {
      key: "children",
      header: "Exact child uses",
      align: "right",
      render: (row) => row.child_activity_count.toLocaleString(),
    },
  ];
  const versionColumns: Column<PluginVersionRow>[] = [
    { key: "version", header: "Version", render: (row) => row.version || "Not observed" },
    { key: "state", header: "State", render: (row) => row.current ? "Current in at least one variant" : "Historical" },
    {
      key: "seen",
      header: "Last observed",
      render: (row) => row.last_seen_at ? new Date(row.last_seen_at).toLocaleString() : "Not observed",
    },
  ];
  const assertionColumns: Column<SkillAssertionRow>[] = [
    { key: "kind", header: "Event", render: (row) => row.assertion_kind.replaceAll("_", " ") },
    { key: "time", header: "When", render: (row) => new Date(row.observed_at).toLocaleString() },
    { key: "source", header: "Observed by", render: (row) => row.source_kind.replaceAll("_", " ") },
    {
      key: "identity",
      header: "Identity match",
      render: (row) =>
        row.identity_resolution === "exact"
          ? "Exact"
          : `${row.identity_resolution.replaceAll("_", " ")} · ${row.candidate_count} candidates`,
    },
  ];
  const sourceColumns: Column<SkillSourceRow>[] = [
    { key: "source", header: "Source", render: (row) => row.source_kind.replaceAll("_", " ") },
    { key: "assertions", header: "Events", align: "right", render: (row) => row.assertion_count },
    { key: "exact", header: "Exact matches", align: "right", render: (row) => row.exact_count },
    { key: "state", header: "Completeness", render: (row) => row.completeness },
  ];

  if (listQuery.isError || profileFailed) {
    return (
      <section className="k-page">
        <QueryErrorState
          subject="this plugin profile"
          onRetry={() => {
            if (listQuery.isError) void listQuery.refetch();
            for (const query of profileQueries) {
              if (query.isError) void query.refetch();
            }
          }}
          backHref="/components/plugins"
        />
      </section>
    );
  }

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">{display?.declared_name ?? "Plugin profile"}</h1>
        <p className="k-page__wire t-caption">
          {display
            ? `${display.agent_id} · ${display.variants.length} ${display.variants.length === 1 ? "variant" : "variants"}`
            : "Waiting for a matching catalog entry."}
        </p>
      </header>

      <Panel
        title="Bundle activity"
        actions={<RangeControl range={range} />}
        caption="Plugin loads and child uses are separate evidence; all counts use the selected range."
      >
        <div className="k-grid k-grid--kpis">
          <KpiCard
            label={<GlossaryTerm id="loaded">Plugin loads</GlossaryTerm>}
            value={display?.loaded_count ?? null}
            state={graphState}
          />
          <KpiCard
            label={<GlossaryTerm id="child_activity">Exact child uses</GlossaryTerm>}
            value={display?.child_activity_count ?? null}
            state={graphState}
          />
          <KpiCard
            label="Bundled components"
            value={profiles.length > 0 ? merged.children.length : null}
            state={graphState}
          />
          <KpiCard
            label="Unused children"
            value={profiles.length > 0 ? unusedChildren : null}
            state={graphState}
          />
          <KpiCard
            label={<GlossaryTerm id="component_variant">Plugin variants</GlossaryTerm>}
            value={display?.variants.length ?? null}
            state={graphState}
          />
          <KpiCard
            label={<GlossaryTerm id="collision">Collisions</GlossaryTerm>}
            value={display?.collision_count ?? null}
            state={graphState}
          />
        </div>
        <GapNote>
          “Exact child uses” proves that a bundled child was used and had one resolvable current
          plugin owner. It does not prove that the plugin package itself was invoked, and child
          outcomes are not promoted into plugin success.
          {variants.length > PROFILE_VARIANT_LIMIT
            ? ` Detail evidence is bounded to the ${PROFILE_VARIANT_LIMIT} most-active variants; ${variants.length - PROFILE_VARIANT_LIMIT} additional variants remain visible in the variants table.`
            : ""}
        </GapNote>
      </Panel>

      <Panel
        title="Which bundled components are useful"
        caption={`Top ${chartChildren.length} by exact use; zero-use children remain visible.`}
      >
        <ChartContainer
          option={distributionOption}
          height={Math.max(240, chartChildren.length * 34)}
          ariaLabel={`Bundled component usage distribution for ${display?.declared_name ?? "plugin"}`}
        />
      </Panel>

      <Panel title="Bundled components ranked by use">
        <DataTable
          columns={childColumns}
          rows={merged.children}
          rowKey={(row) => `${row.component_kind}:${row.declared_name}`}
          emptyMessage={profileQueries.some((query) => query.isLoading) ? "Loading…" : "No complete bundle evidence."}
        />
      </Panel>

      <Panel title="Observed plugin variants">
        <DataTable
          columns={variantColumns}
          rows={display?.variants ?? []}
          rowKey={(row) => row.component_installation_id}
          emptyMessage={listQuery.isLoading ? "Loading…" : "No matching variants found."}
        />
      </Panel>

      <Panel title="Version history">
        <DataTable
          columns={versionColumns}
          rows={merged.versions}
          rowKey={(row) => `${row.version_state}:${row.version ?? ""}`}
        />
      </Panel>

      <Panel title="Plugin evidence timeline">
        <DataTable
          columns={assertionColumns}
          rows={merged.assertions}
          rowKey={(row) => row.assertion_id}
        />
      </Panel>

      <Panel title="Evidence sources">
        <DataTable
          columns={sourceColumns}
          rows={merged.sources}
          rowKey={(row) => row.source_instance_id}
        />
      </Panel>
    </section>
  );
}
