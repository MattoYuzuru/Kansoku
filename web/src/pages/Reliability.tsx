import { useMemo } from "react";
import { KpiCard } from "../components/KpiCard";
import { ChartContainer } from "../components/ChartContainer";
import { DataTable, type Column } from "../components/DataTable";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { StatusBadge } from "../components/StatusBadge";
import { deriveViewState } from "../api/client";
import {
  useCollectionHealth,
  useIncident,
  useIncidentDebugBundle,
  useIncidentOccurrences,
  useIncidents,
  useQuarantine,
  useQuarantineManifest,
  useReliabilityCounts,
  useReliabilityCoverageTimeline,
} from "../api/queries";
import { useRange } from "../hooks/useRange";
import { dayLabel, sum } from "../lib/format";
import { bucketedStackedBarOption } from "../components/chartOptions";
import type {
  Incident,
  IncidentOccurrence,
  QuarantineManifest,
  ReliabilityDayRow,
} from "../api/types";

type ReliabilityTab = "health" | "incidents" | "quarantine";

function queryState() {
  const params = new URLSearchParams(window.location.search);
  const requested = params.get("tab");
  const tab: ReliabilityTab =
    requested === "incidents" || requested === "quarantine" ? requested : "health";
  return {
    tab,
    incidentID: params.get("incident") ?? undefined,
    quarantineID: params.get("quarantine") ?? undefined,
    cursor: params.get("cursor") ?? undefined,
    state: params.get("state") ?? undefined,
    triage: params.get("triage") ?? undefined,
    adapter: params.get("adapter") ?? undefined,
    source: params.get("source") ?? undefined,
    capability: params.get("capability") ?? undefined,
    failure: params.get("failure") ?? undefined,
    fingerprint: params.get("fingerprint") ?? undefined,
  };
}

function tabHref(tab: ReliabilityTab) {
  return `/reliability?tab=${tab}`;
}

function valueLabel(value: { state: string; value: string | null }) {
  return value.value ?? value.state;
}

export function Reliability() {
  const range = useRange();
  const page = queryState();
  const rangeParams = useMemo(
    () => ({
      from: range.from,
      to: range.to,
      granularity: range.granularity,
      timezone: range.timezone,
    }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const coverage = useReliabilityCoverageTimeline(rangeParams);
  const counts = useReliabilityCounts(rangeParams);
  const collectionHealth = useCollectionHealth(rangeParams);
  const incidents = useIncidents({
    cursor: page.tab === "incidents" ? page.cursor : undefined,
    state: page.state,
    triage: page.triage,
    adapter: page.adapter,
    source: page.source,
    capability: page.capability,
    failure: page.failure,
    limit: 25,
  });
  const quarantine = useQuarantine({
    cursor: page.tab === "quarantine" ? page.cursor : undefined,
    fingerprint: page.fingerprint,
    source: page.source,
    limit: 25,
  });
  const incident = useIncident(page.incidentID);
  const occurrences = useIncidentOccurrences(page.incidentID);
  const debugBundle = useIncidentDebugBundle(page.incidentID);
  const manifest = useQuarantineManifest(page.quarantineID);

  const health = collectionHealth.data?.data;
  const healthState = deriveViewState(collectionHealth.data, {
    isLoading: collectionHealth.isLoading,
  });
  const coverageRows = coverage.data?.data?.data ?? [];
  const coverageState = deriveViewState(coverage.data, { isLoading: coverage.isLoading });
  const countsRows = counts.data?.data?.data ?? [];
  const countsState = deriveViewState(counts.data, { isLoading: counts.isLoading });
  const unknownSchemaTotal = sum(countsRows.map((row) => row.unknown_schema_count));
  const mismatchTotal = sum(countsRows.map((row) => row.reconciliation_mismatch_count));
  const incidentRows = incidents.data?.data?.data ?? [];
  const quarantineRows = quarantine.data?.data?.data ?? [];

  const coverageColumns: Column<ReliabilityDayRow>[] = [
    { key: "day", header: "Day", render: (row) => dayLabel(row.day) },
    { key: "source", header: "Source instance", render: (row) => row.source_instance_id },
    { key: "status", header: "Status", render: (row) => row.status },
    {
      key: "intervals",
      header: "Intervals",
      align: "right",
      render: (row) => row.interval_count.toLocaleString(),
    },
  ];
  const incidentColumns: Column<Incident>[] = [
    {
      key: "incident_id",
      header: "Incident",
      render: (row) => (
        <a href={`/reliability?tab=incidents&incident=${encodeURIComponent(row.incident_id)}`}>
          {row.incident_id}
        </a>
      ),
    },
    { key: "detector", header: "Detector", render: (row) => row.detector_state },
    { key: "triage", header: "Triage", render: (row) => row.triage_state },
    { key: "capability", header: "Capability", render: (row) => row.capability_id },
    { key: "failure", header: "Failure class", render: (row) => row.failure_class },
    {
      key: "occurrences",
      header: "Occurrences",
      align: "right",
      render: (row) => row.occurrence_count.toLocaleString(),
    },
    { key: "last_seen", header: "Last seen", render: (row) => dayLabel(row.last_seen_at) },
  ];
  const quarantineColumns: Column<QuarantineManifest>[] = [
    {
      key: "quarantine_id",
      header: "Manifest",
      render: (row) => (
        <a href={`/reliability?tab=quarantine&quarantine=${encodeURIComponent(row.quarantine_id)}`}>
          {row.quarantine_id}
        </a>
      ),
    },
    { key: "source", header: "Source", render: (row) => row.source_kind },
    { key: "shape", header: "Shape", render: (row) => row.shape_value_state },
    { key: "disposition", header: "Disposition", render: (row) => row.disposition },
    {
      key: "occurrences",
      header: "Occurrences",
      align: "right",
      render: (row) => row.occurrence_count.toLocaleString(),
    },
    { key: "last_seen", header: "Last seen", render: (row) => dayLabel(row.last_seen_at) },
  ];

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Reliability</h1>
        <p className="k-page__wire t-caption">
          Collection health, incidents, and metadata-only quarantine.
        </p>
      </header>

      <nav aria-label="Reliability views" className="k-reliability-tabs">
        {(["health", "incidents", "quarantine"] as const).map((tab) => (
          <a
            key={tab}
            href={tabHref(tab)}
            aria-current={page.tab === tab ? "page" : undefined}
            className={page.tab === tab ? "is-active" : ""}
          >
            {tab[0].toUpperCase() + tab.slice(1)}
          </a>
        ))}
      </nav>

      {page.tab === "health" && (
        <>
          <Panel title="Collection health" actions={<RangeControl range={range} />}>
            <div className="k-grid k-grid--kpis">
              <KpiCard label="Accepted events" value={health?.accepted_event_count ?? null} state={healthState} />
              <KpiCard label="Quarantined records" value={health?.quarantined_record_count ?? null} state={healthState} />
              <KpiCard
                label="Ingest latency p95"
                value={health?.ingest_latency_p95_ms ?? null}
                unit="ms"
                precision={1}
                state={health?.ingest_latency_p95_ms == null && health ? "not_observed" : healthState}
              />
              <KpiCard label="Active sources" value={health?.active_source_count ?? null} state={healthState} />
              <KpiCard label="Sequence gaps" value={health?.source_gap_count ?? null} state={healthState} />
              <KpiCard label="Queue depth" value={health?.queue_depth ?? null} state={healthState} />
              <KpiCard label="Pending rollups" value={health?.pending_rollup_count ?? null} state={healthState} />
            </div>
            <GapNote>
              Snapshot values remain separate from historical interval coverage.
            </GapNote>
          </Panel>

          <Panel title="Source coverage timeline" actions={<RangeControl range={range} />}>
            <DataTable
              columns={coverageColumns}
              rows={coverageRows}
              rowKey={(row) => `${row.day}-${row.source_instance_id}-${row.status}`}
              emptyMessage={coverage.isLoading ? "Loading…" : "No coverage intervals observed in this range."}
            />
            {coverageState !== "complete" && coverageState !== "loading" && coverageRows.length === 0 && (
              <p className="t-caption">Coverage state: {coverageState}</p>
            )}
          </Panel>

          <Panel title="Drift and mismatch">
            <div className="k-grid k-grid--kpis">
              <KpiCard label="Quarantined schema batches" value={unknownSchemaTotal} state={countsState} />
              <KpiCard label="Reconciliation mismatches" value={mismatchTotal} state={countsState} />
              <KpiCard label="Drift detection time" value={null} unit="s" state="unsupported" />
            </div>
            {countsRows.length > 0 ? (
              <ChartContainer
                ariaLabel="Quarantined schema batches and reconciliation mismatches"
                option={bucketedStackedBarOption(range, countsRows, [
                  { name: "Quarantined schema batch", value: (row) => row.unknown_schema_count },
                  {
                    name: "Reconciliation mismatch",
                    value: (row) => row.reconciliation_mismatch_count,
                    color: "var(--status-degraded)",
                  },
                ])}
              />
            ) : (
              <p className="t-body">{counts.isLoading ? "Loading…" : "No drift or mismatch events observed."}</p>
            )}
            <GapNote>
              Drift detection time remains <StatusBadge state="unsupported" glyphOnly /> until a durable timer exists.
            </GapNote>
          </Panel>
        </>
      )}

      {page.tab === "incidents" && !page.incidentID && (
        <Panel
          title="Incident history"
          caption={`${incidents.data?.data?.total_state ?? "unknown"} total ≥ ${incidents.data?.data?.total_lower_bound ?? 0}; page formula ${incidents.data?.data?.formula_version ?? "unknown"}`}
        >
          <form method="get" action="/reliability" className="k-workbench-filters">
            <input type="hidden" name="tab" value="incidents" />
            <label>Detector<select name="state" defaultValue={page.state ?? ""}><option value="">All</option><option value="open">Open</option><option value="recovering">Recovering</option><option value="resolved">Resolved</option></select></label>
            <label>Triage<select name="triage" defaultValue={page.triage ?? ""}><option value="">All</option><option value="new">New</option><option value="acknowledged">Acknowledged</option><option value="investigating">Investigating</option><option value="action_ready">Action ready</option></select></label>
            <label>Adapter<input name="adapter" defaultValue={page.adapter ?? ""} /></label>
            <label>Source<input name="source" defaultValue={page.source ?? ""} /></label>
            <label>Capability<input name="capability" defaultValue={page.capability ?? ""} /></label>
            <label>Failure<input name="failure" defaultValue={page.failure ?? ""} /></label>
            <button type="submit">Apply filters</button>
          </form>
          <DataTable
            columns={incidentColumns}
            rows={incidentRows}
            rowKey={(row) => row.incident_id}
            emptyMessage={incidents.isLoading ? "Loading…" : "No incidents match this page."}
          />
          {incidents.data?.data?.has_more && incidents.data.data.next_cursor && (
            <a href={pageHref("incidents", incidents.data.data.next_cursor)}>
              Next page
            </a>
          )}
        </Panel>
      )}

      {page.tab === "incidents" && page.incidentID && (
        <IncidentProfile
          incident={incident.data?.data}
          occurrences={occurrences.data?.data?.data ?? []}
          agentPrompt={debugBundle.data?.data?.agent_prompt}
          loading={incident.isLoading}
        />
      )}

      {page.tab === "quarantine" && !page.quarantineID && (
        <Panel
          title="Safe structural manifests"
          caption="Values and raw payloads are never retained."
        >
          <form method="get" action="/reliability" className="k-workbench-filters">
            <input type="hidden" name="tab" value="quarantine" />
            <label>Fingerprint<input name="fingerprint" defaultValue={page.fingerprint ?? ""} /></label>
            <label>Source<input name="source" defaultValue={page.source ?? ""} /></label>
            <button type="submit">Apply filters</button>
          </form>
          <DataTable
            columns={quarantineColumns}
            rows={quarantineRows}
            rowKey={(row) => row.quarantine_id}
            emptyMessage={quarantine.isLoading ? "Loading…" : "No quarantine manifests observed."}
          />
          {quarantine.data?.data?.has_more && quarantine.data.data.next_cursor && (
            <a href={pageHref("quarantine", quarantine.data.data.next_cursor)}>
              Next page
            </a>
          )}
        </Panel>
      )}

      {page.tab === "quarantine" && page.quarantineID && (
        <QuarantineProfile manifest={manifest.data?.data} loading={manifest.isLoading} />
      )}
    </section>
  );
}

function pageHref(tab: ReliabilityTab, cursor: string) {
  const params = new URLSearchParams(window.location.search);
  params.set("tab", tab);
  params.set("cursor", cursor);
  params.delete("incident");
  params.delete("quarantine");
  return `/reliability?${params.toString()}`;
}

function IncidentProfile({
  incident,
  occurrences,
  agentPrompt,
  loading,
}: {
  incident?: Incident;
  occurrences: IncidentOccurrence[];
  agentPrompt?: string;
  loading: boolean;
}) {
  if (!incident) {
    return <Panel title="Incident profile">{loading ? "Loading…" : "Incident unavailable."}</Panel>;
  }
  const occurrenceColumns: Column<IncidentOccurrence>[] = [
    { key: "observed", header: "Observed", render: (row) => dayLabel(row.observed_at) },
    { key: "class", header: "Safe error class", render: (row) => row.safe_error_class },
    { key: "records", header: "Records", align: "right", render: (row) => row.record_count.toLocaleString() },
    { key: "bytes", header: "Bytes", align: "right", render: (row) => row.byte_count.toLocaleString() },
    { key: "evidence", header: "Evidence", render: (row) => row.evidence_ref },
  ];
  return (
    <>
      <Panel title="Incident profile" actions={<a href={tabHref("incidents")}>Back to incidents</a>}>
        <dl className="k-kv">
          <div className="k-kv__row"><dt>Detector / triage</dt><dd>{incident.detector_state} / {incident.triage_state}</dd></div>
          <div className="k-kv__row"><dt>Installation</dt><dd>{valueLabel(incident.installation)}</dd></div>
          <div className="k-kv__row"><dt>Source</dt><dd>{valueLabel(incident.source)}</dd></div>
          <div className="k-kv__row"><dt>Capability</dt><dd>{incident.capability_id}</dd></div>
          <div className="k-kv__row"><dt>Failure / severity</dt><dd>{incident.failure_class} / {incident.severity}</dd></div>
          <div className="k-kv__row"><dt>Interval</dt><dd>{dayLabel(incident.affected_interval_from)} – {dayLabel(incident.affected_interval_to)}</dd></div>
          <div className="k-kv__row"><dt>Occurrences</dt><dd>{incident.occurrence_count.toLocaleString()}</dd></div>
          <div className="k-kv__row"><dt>Expired occurrence detail</dt><dd>{incident.occurrence_retention_excluded_count.toLocaleString()}</dd></div>
          <div className="k-kv__row"><dt>Recovery</dt><dd>{incident.recovery_criteria}</dd></div>
          <div className="k-kv__row"><dt>Schema fingerprint</dt><dd>{incident.schema_fingerprint ?? "unknown"}</dd></div>
        </dl>
      </Panel>
      <Panel title="Occurrence history">
        <DataTable columns={occurrenceColumns} rows={occurrences} rowKey={(row) => row.occurrence_id} />
      </Panel>
      <Panel title="Read-only agent prompt">
        <pre className="t-caption" style={{ whiteSpace: "pre-wrap", margin: 0 }}>
          {agentPrompt ?? "Loading metadata-only debug bundle…"}
        </pre>
      </Panel>
    </>
  );
}

function QuarantineProfile({
  manifest,
  loading,
}: {
  manifest?: QuarantineManifest;
  loading: boolean;
}) {
  if (!manifest) {
    return <Panel title="Structural manifest">{loading ? "Loading…" : "Manifest unavailable."}</Panel>;
  }
  return (
    <Panel title="Structural manifest" actions={<a href={tabHref("quarantine")}>Back to quarantine</a>}>
      <dl className="k-kv">
        <div className="k-kv__row"><dt>Source / signal</dt><dd>{manifest.source_kind} / {manifest.signal_kind}</dd></div>
        <div className="k-kv__row"><dt>Event type</dt><dd>{valueLabel(manifest.event_type)}</dd></div>
        <div className="k-kv__row"><dt>Shape evidence</dt><dd>{manifest.shape_value_state}</dd></div>
        <div className="k-kv__row"><dt>Primitive types</dt><dd>{manifest.primitive_types.join(", ")}</dd></div>
        <div className="k-kv__row"><dt>Disposition</dt><dd>{manifest.disposition}</dd></div>
        <div className="k-kv__row"><dt>Occurrences</dt><dd>{manifest.occurrence_count.toLocaleString()}</dd></div>
      </dl>
      <div>
        <div className="t-section-header">Structural field paths</div>
        {manifest.structural_field_paths.length > 0 ? (
          <ul>{manifest.structural_field_paths.map((path) => <li key={path}><code>{path}</code></li>)}</ul>
        ) : (
          <p className="t-caption">Path structure is {manifest.shape_value_state}; no values were retained.</p>
        )}
      </div>
    </Panel>
  );
}
