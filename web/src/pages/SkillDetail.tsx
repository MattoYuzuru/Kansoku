import { useMemo } from "react";
import type { ViewState } from "../api/client";
import { useSkillProfiles, useSkills } from "../api/queries";
import type { SkillAssertionRow, SkillObservatoryRow, SkillSourceRow } from "../api/types";
import { ChartContainer } from "../components/ChartContainer";
import { eventTimelineOption } from "../components/chartOptions";
import { DataTable, type Column } from "../components/DataTable";
import { GlossaryTerm } from "../components/GlossaryTerm";
import { KpiCard } from "../components/KpiCard";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { useRange } from "../hooks/useRange";
import { groupSkillCatalog, mergeSkillProfiles } from "../lib/componentCatalog";

const PROFILE_VARIANT_LIMIT = 8;

export function SkillDetail({ id }: { id: string }) {
  const range = useRange("all_time");
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const listQuery = useSkills(rangeParams);
  const inventoryRows = listQuery.data?.data?.data ?? [];
  const catalog = useMemo(() => groupSkillCatalog(inventoryRows), [inventoryRows]);
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
  const profileQueries = useSkillProfiles(variantIDs, rangeParams);
  const profiles = profileQueries.flatMap((query) => query.data?.data ? [query.data.data] : []);
  const merged = useMemo(() => mergeSkillProfiles(profiles), [profiles]);
  const display = family ?? (directVariant ? groupSkillCatalog([directVariant])[0] : undefined);
  const state = (display?.completeness as ViewState | undefined) ?? "unknown";

  const assertionColumns: Column<SkillAssertionRow>[] = [
    {
      key: "kind",
      header: "Event",
      render: (row) => row.assertion_kind.replaceAll("_", " "),
    },
    { key: "time", header: "When", render: (row) => new Date(row.observed_at).toLocaleString() },
    { key: "source", header: "Observed by", render: (row) => row.source_kind.replaceAll("_", " ") },
    {
      key: "evidence",
      header: <GlossaryTerm id="evidence">Evidence</GlossaryTerm>,
      render: (row) => `${row.evidence_tier} · ${Math.round(row.confidence * 100)}% confidence`,
    },
    { key: "mode", header: "Invocation mode", render: (row) => row.mode.replaceAll("_", " ") },
    {
      key: "identity",
      header: "Identity match",
      render: (row) =>
        row.identity_resolution === "exact"
          ? "Exact"
          : `${row.identity_resolution.replaceAll("_", " ")} · ${row.candidate_count} candidates`,
    },
    {
      key: "outcome",
      header: <GlossaryTerm id="succeeded">Outcome</GlossaryTerm>,
      render: (row) =>
        row.terminal_contract_id ? `${row.outcome} · ${row.terminal_contract_id}` : "Not supported",
    },
  ];
  const variantColumns: Column<SkillObservatoryRow>[] = [
    { key: "source", header: "Source", render: (row) => row.source_scope.replaceAll("_", " ") },
    { key: "version", header: "Version", render: (row) => row.version || "Not observed" },
    {
      key: "profile",
      header: "Agent / profile",
      render: (row) => `${row.agent_id} · profile ${profileOrdinals.get(row.agent_installation_id) ?? "?"}`,
    },
    {
      key: "state",
      header: <GlossaryTerm id="enabled">Availability</GlossaryTerm>,
      render: (row) => `${row.enabled ? "Enabled" : "Disabled"} · ${row.completeness}`,
    },
    { key: "invoked", header: "Invocations", align: "right", render: (row) => row.invoked_count.toLocaleString() },
    { key: "loaded", header: "Loads", align: "right", render: (row) => row.loaded_count.toLocaleString() },
  ];
  const sourceColumns: Column<SkillSourceRow>[] = [
    { key: "source", header: "Source", render: (row) => row.source_kind.replaceAll("_", " ") },
    { key: "assertions", header: "Events", align: "right", render: (row) => row.assertion_count },
    { key: "exact", header: "Exact matches", align: "right", render: (row) => row.exact_count },
    { key: "state", header: "Completeness", render: (row) => row.completeness },
  ];
  const timelineRows = merged.assertions.filter((row) =>
    ["exposed", "invoked", "loaded"].includes(row.assertion_kind),
  );
  const timelineOption = useMemo(
    () => eventTimelineOption(timelineRows, ["exposed", "invoked", "loaded"]),
    [timelineRows],
  );

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">{display?.declared_name ?? "Skill profile"}</h1>
        <p className="k-page__wire t-caption">
          {display
            ? `${display.agent_id} · ${display.variants.length} ${display.variants.length === 1 ? "variant" : "variants"}`
            : "Waiting for a matching catalog entry."}
        </p>
      </header>

      <Panel
        title="Usage summary"
        actions={<RangeControl range={range} />}
        caption="All counts are deduplicated exact evidence inside the selected range."
      >
        <div className="k-grid k-grid--kpis">
          <KpiCard
            label={<GlossaryTerm id="exposed">Exposures</GlossaryTerm>}
            value={display?.exposed_count ?? null}
            state={state}
          />
          <KpiCard
            label={<GlossaryTerm id="invoked">Invocations</GlossaryTerm>}
            value={display?.invoked_count ?? null}
            state={state}
          />
          <KpiCard
            label={<GlossaryTerm id="loaded">Loads</GlossaryTerm>}
            value={display?.loaded_count ?? null}
            state={state}
          />
          <KpiCard
            label={<GlossaryTerm id="component_variant">Variants</GlossaryTerm>}
            value={display?.variants.length ?? null}
            state={state}
          />
          <KpiCard
            label="Incidents"
            value={profiles.length > 0 ? merged.incident_count : null}
            state={profiles.length > 0 ? (merged.completeness as ViewState) : "unknown"}
          />
        </div>
        <GapNote>
          A catalog family is a browsing view, not an identity rewrite. Marketplace, plugin-cache,
          version and profile variants remain separate below. A model response is not treated as
          skill success unless a registered terminal contract was observed.
          {variants.length > PROFILE_VARIANT_LIMIT
            ? ` The timeline is bounded to the ${PROFILE_VARIANT_LIMIT} most-used variants; ${variants.length - PROFILE_VARIANT_LIMIT} additional variants remain visible in the inventory table.`
            : ""}
        </GapNote>
      </Panel>

      <Panel
        title="When this skill was available and used"
        caption="Each dot is one evidence event; the exact accessible rows are in the table below."
      >
        <ChartContainer
          option={timelineOption}
          height={260}
          ariaLabel={`Timeline of exposures, invocations and loads for ${display?.declared_name ?? "skill"}`}
        />
      </Panel>

      <Panel title="Observed variants">
        <DataTable
          columns={variantColumns}
          rows={display?.variants ?? []}
          rowKey={(row) => row.component_installation_id}
          emptyMessage={listQuery.isLoading ? "Loading…" : "No matching variants found."}
        />
      </Panel>

      <Panel title="Evidence timeline">
        <DataTable
          columns={assertionColumns}
          rows={merged.assertions}
          rowKey={(row) => row.assertion_id}
          emptyMessage={profileQueries.some((query) => query.isLoading) ? "Loading…" : "No evidence in this range."}
        />
      </Panel>

      <Panel title="Evidence sources">
        <DataTable
          columns={sourceColumns}
          rows={merged.sources}
          rowKey={(row) => row.source_instance_id}
        />
      </Panel>

      <Panel title="File inventory metadata">
        <p className="t-body">
          {merged.file_tree.length > 0
            ? `${merged.file_tree.reduce((total, row) => total + row.file_count, 0)} files · ` +
              `${merged.file_tree.reduce((total, row) => total + row.directory_count, 0)} directories · ` +
              `${merged.file_tree.reduce((total, row) => total + row.total_bytes, 0).toLocaleString()} bytes across observed variants`
            : "Not observed"}
        </p>
        <GapNote>
          Only counts, depth and byte totals are available here. Paths and file content are not persisted.
        </GapNote>
      </Panel>
    </section>
  );
}
