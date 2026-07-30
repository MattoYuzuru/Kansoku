import { useMemo } from "react";
import { DataTable, type Column } from "../components/DataTable";
import { ChartContainer } from "../components/ChartContainer";
import { stackedBarOption, timeSeriesOption } from "../components/chartOptions";
import { KpiCard } from "../components/KpiCard";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { StatusBadge } from "../components/StatusBadge";
import { QueryErrorState } from "../components/QueryErrorState";
import { deriveViewState } from "../api/client";
import { useAgentProfile } from "../api/queries";
import { useRange } from "../hooks/useRange";
import { formatMetric, formatMetricWithRaw, microsToUsd } from "../lib/format";
import type { AgentProfile } from "../api/types";

export interface AgentDetailProps {
  alias: string;
}

type ModelRow = AgentProfile["models"][number];
type SourceRow = AgentProfile["sources"][number];

function shortID(value: string): string {
  return value.length > 22 ? `${value.slice(0, 18)}…` : value;
}

function displayLabel(value: string): string {
  return value
    .split(/[-_]/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

export function AgentDetail({ alias }: AgentDetailProps) {
  const range = useRange("agents");
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const profile = useAgentProfile(alias, rangeParams);
  const data = profile.data?.data;
  const state = deriveViewState(profile.data, { isLoading: profile.isLoading });
  const identity = data?.identity;
  const activity = data?.activity;
  const models = data?.models;
  const title = identity?.display_alias ||
    (identity?.display_name ? displayLabel(identity.display_name) : "Unknown agent");
  const tokenOption = useMemo(
    () => stackedBarOption(
      (models ?? []).map((row) => row.model_id),
      [
        { name: "Input", data: (models ?? []).map((row) => row.input_tokens), stack: "tokens" },
        {
          name: "Cached input",
          data: (models ?? []).map((row) => row.cached_input_tokens),
          stack: "tokens",
        },
        { name: "Output", data: (models ?? []).map((row) => row.output_tokens), stack: "tokens" },
      ],
    ),
    [models],
  );
  const costOption = useMemo(
    () => timeSeriesOption(
      (models ?? []).map((row) => row.model_id),
      [
        {
          name: "Provider-reported",
          data: (models ?? []).map((row) => microsToUsd(row.provider_cost_micros)),
          type: "bar",
        },
        {
          name: "API-equivalent estimate",
          data: (models ?? []).map((row) => microsToUsd(row.api_equivalent_cost_micros)),
          type: "bar",
        },
      ],
    ),
    [models],
  );

  if (profile.isError) {
    return (
      <section className="k-page">
        <QueryErrorState
          subject="this agent profile"
          onRetry={() => void profile.refetch()}
          backHref="/agents"
        />
      </section>
    );
  }

  const modelColumns: Column<ModelRow>[] = [
    { key: "model", header: "Model", render: (row) => row.model_id },
    { key: "requests", header: "Requests", align: "right", render: (row) => row.request_count.toLocaleString() },
    {
      key: "tokens",
      header: "Input / cache / output",
      align: "right",
      render: (row) =>
        `${row.input_tokens.toLocaleString()} / ${row.cached_input_tokens.toLocaleString()} / ${row.output_tokens.toLocaleString()}`,
    },
    {
      key: "p95",
      header: "p95",
      align: "right",
      render: (row) => row.percentiles?.p95 == null
        ? "Not observed"
        : <span title={formatMetricWithRaw(row.percentiles.p95, "ms")}>{formatMetric(row.percentiles.p95)} ms</span>,
    },
    {
      key: "errors",
      header: "Failed",
      align: "right",
      render: (row) => `${row.failure_count} / ${row.success_count + row.failure_count}`,
    },
    {
      key: "provider_cost",
      header: "Provider-reported cost",
      align: "right",
      render: (row) =>
        row.provider_costed_request_count > 0
          ? `$${microsToUsd(row.provider_cost_micros).toFixed(2)}`
          : "Not observed",
    },
    {
      key: "api_cost",
      header: "API-equivalent estimate",
      align: "right",
      render: (row) =>
        row.api_estimated_request_count > 0
          ? `$${microsToUsd(row.api_equivalent_cost_micros).toFixed(2)}`
          : "Not observed",
    },
  ];

  const sourceColumns: Column<SourceRow>[] = [
    { key: "kind", header: "Evidence lane", render: (row) => row.source_kind },
    {
      key: "state",
      header: "Health",
      render: (row) => (
        <StatusBadge
          state={row.state === "producing" ? "complete" : row.state === "degraded" ? "degraded" : "not_observed"}
          reason={`${row.fact_count} facts, ${row.evidence_count} evidence rows, ${row.gap_count} gaps`}
        />
      ),
    },
    { key: "facts", header: "Facts", align: "right", render: (row) => row.fact_count.toLocaleString() },
    { key: "evidence", header: "Evidence", align: "right", render: (row) => row.evidence_count.toLocaleString() },
    { key: "version", header: "Adapter version", render: (row) => row.adapter_version },
    {
      key: "last_seen",
      header: "Last observed",
      render: (row) => row.last_observed_at ? new Date(row.last_observed_at).toLocaleString() : "Not observed",
    },
  ];

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">{title}</h1>
        <p className="k-page__wire t-caption">
          {identity
            ? `${displayLabel(identity.provider_id)} · ${displayLabel(identity.surface_kind)} · ${shortID(identity.agent_installation_id)}`
            : `Loading installation ${shortID(alias)}`}
        </p>
      </header>

      <Panel title="Installation identity" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Events" value={activity?.event_count ?? null} state={state} />
          <KpiCard label="Sessions" value={activity?.session_count ?? null} state={state} />
          <KpiCard label="Prompts" value={activity?.prompt_count ?? null} state={state} />
          <KpiCard label="Tool calls" value={activity?.tool_call_count ?? null} state={state} />
          <KpiCard label="Components" value={activity?.component_count ?? null} state={state} />
          <KpiCard
            label="Open incidents"
            value={activity?.open_incident_count ?? null}
            state={(activity?.open_incident_count ?? 0) > 0 ? "degraded" : state}
          />
        </div>
        {identity && (
          <p className="t-caption" style={{ color: "var(--text-faint)" }}>
            Agent {identity.agent_version || "version not observed"} · adapter{" "}
            {identity.adapter_version || "version not observed"} · identity provenance{" "}
            {identity.source_provenance} · class {identity.installation_class} (
            {identity.installation_class_provenance}). The opaque{" "}
            <code>{shortID(identity.agent_installation_id)}</code>{" "}
            remains a secondary diagnostic key.
          </p>
        )}
      </Panel>

      <div className="k-grid k-grid--2col">
        <Panel title="Token composition by model">
          <ChartContainer
            option={tokenOption}
            height={280}
            ariaLabel={`Input, cached input and output tokens by model for ${title}`}
          />
        </Panel>
        <Panel title="Cost evidence by model">
          <ChartContainer
            option={costOption}
            height={280}
            ariaLabel={`Provider-reported cost and API-equivalent estimates by model for ${title}`}
          />
          <GapNote>
            Provider-reported cost and API-equivalent estimates are independent evidence lanes.
            A Codex API-equivalent estimate is not billed ChatGPT or subscription spend.
          </GapNote>
        </Panel>
      </div>

      <Panel title="Per-model usage">
        <DataTable
          columns={modelColumns}
          rows={models ?? []}
          rowKey={(row) => row.model_id}
          emptyMessage={profile.isLoading ? "Loading…" : "No exactly attributed model responses in this range."}
        />
        <GapNote>
          Population {data?.population.numerator ?? 0} / {data?.population.denominator ?? 0};
          non-exact installation attribution exclusions{" "}
          {data?.exclusions.non_exact_installation_attribution ?? 0}. Provider cost coverage{" "}
          {(models ?? []).reduce((total, row) => total + row.provider_costed_request_count, 0)}
          {" / "}
          {(models ?? []).reduce((total, row) => total + row.request_count, 0)}; API-equivalent
          estimate coverage{" "}
          {(models ?? []).reduce((total, row) => total + row.api_estimated_request_count, 0)}
          {" / "}
          {(models ?? []).reduce((total, row) => total + row.request_count, 0)}.
        </GapNote>
      </Panel>

      <Panel title="Source and bridge matrix">
        <DataTable
          columns={sourceColumns}
          rows={data?.sources ?? []}
          rowKey={(row) => row.source_instance_id}
          emptyMessage={profile.isLoading ? "Loading…" : "No evidence lane observed in this range."}
        />
        <GapNote>
          Bridge health is independent. A missing evidence bridge does not erase or
          downgrade facts already proven by OTel, hooks, or another lane.
        </GapNote>
      </Panel>
    </section>
  );
}
