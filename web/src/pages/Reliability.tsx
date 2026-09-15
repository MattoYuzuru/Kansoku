import { useEffect, useMemo, useRef, type FormEvent } from "react";
import { Link, useLocation, useSearch } from "wouter";
import { KpiCard } from "../components/KpiCard";
import { ChartContainer } from "../components/ChartContainer";
import { DataTable, type Column } from "../components/DataTable";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { StatusBadge } from "../components/StatusBadge";
import { Dropdown } from "../components/Dropdown";
import { deriveViewState } from "../api/client";
import {
  useCollectionHealth,
  useIncident,
  useIncidentDebugBundle,
  useIncidentOccurrences,
  useInfiniteIncidents,
  useInfiniteQuarantine,
  useQuarantineManifest,
  useReliabilityCounts,
  useReliabilityCoverageTimeline,
} from "../api/queries";
import { useRange } from "../hooks/useRange";
import { formatMetric } from "../lib/format";
import { dayLabel, sum } from "../lib/format";
import { bucketedStackedBarOption } from "../components/chartOptions";
import type {
  Incident,
  IncidentOccurrence,
  QuarantineManifest,
  ReliabilityDayRow,
} from "../api/types";

type ReliabilityTab = "health" | "incidents" | "quarantine";

function queryState(search: string) {
  const params = new URLSearchParams(search);
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
  const range = useRange("reliability");
  const search = useSearch();
  const [, navigate] = useLocation();
  const page = useMemo(() => queryState(search), [search]);
  const rangeParams = useMemo(
    () => ({
      from: range.from,
      to: range.to,
      granularity: range.granularity,
      timezone: range.timezone,
    }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const healthEnabled = page.tab === "health";
  const coverage = useReliabilityCoverageTimeline(rangeParams, healthEnabled);
  const counts = useReliabilityCounts(rangeParams, healthEnabled);
  const collectionHealth = useCollectionHealth(rangeParams, healthEnabled);
  const incidents = useInfiniteIncidents({
    state: page.state,
    triage: page.triage,
    adapter: page.adapter,
    source: page.source,
    capability: page.capability,
    failure: page.failure,
    limit: 25,
  }, page.tab === "incidents" && !page.incidentID);
  const quarantine = useInfiniteQuarantine({
    fingerprint: page.fingerprint,
    source: page.source,
    limit: 25,
  }, page.tab === "quarantine" && !page.quarantineID);
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
  const incidentPages = incidents.data?.pages ?? [];
  const quarantinePages = quarantine.data?.pages ?? [];
  const incidentRows = incidentPages.flatMap((item) => item.data?.data ?? []).slice(0, 200);
  const quarantineRows = quarantinePages.flatMap((item) => item.data?.data ?? []).slice(0, 200);
  const incidentPage = incidentPages[0]?.data;

  useEffect(() => {
    const key = `kansoku.reliability.scroll:${search}`;
    const saved = Number(sessionStorage.getItem(key) ?? "0");
    const frame = requestAnimationFrame(() => window.scrollTo({ top: saved }));
    return () => {
      cancelAnimationFrame(frame);
      sessionStorage.setItem(key, String(window.scrollY));
    };
  }, [search]);

  const setFilter = (key: string, value: string) => {
    const params = new URLSearchParams(search);
    if (value) params.set(key, value);
    else params.delete(key);
    params.delete("cursor");
    navigate(`/reliability?${params.toString()}`);
  };
  const applyTextFilters = (tab: ReliabilityTab, event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const values = new FormData(event.currentTarget);
    const params = new URLSearchParams(search);
    params.set("tab", tab);
    for (const key of ["adapter", "source", "capability", "failure", "fingerprint"]) {
      const value = String(values.get(key) ?? "").trim();
      if (value) params.set(key, value);
      else params.delete(key);
    }
    params.delete("cursor");
    navigate(`/reliability?${params.toString()}`);
  };

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
        <Link href={`/reliability?tab=incidents&incident=${encodeURIComponent(row.incident_id)}`}>
          {row.incident_id}
        </Link>
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
        <Link href={`/reliability?tab=quarantine&quarantine=${encodeURIComponent(row.quarantine_id)}`}>
          {row.quarantine_id}
        </Link>
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
          <Link
            key={tab}
            href={tabHref(tab)}
            aria-current={page.tab === tab ? "page" : undefined}
            className={page.tab === tab ? "is-active" : ""}
          >
            {tab[0].toUpperCase() + tab.slice(1)}
          </Link>
        ))}
      </nav>

      {page.tab === "health" && (
        <>
          <Panel title="Collection health" actions={<RangeControl range={range} />}>
            <div className="k-grid k-grid--kpis">
              <KpiCard label="Accepted events" value={health?.accepted_event_count ?? null} state={healthState} />
              <KpiCard label="Quarantined records" value={health?.quarantined_record_count ?? null} state={healthState} />
              <KpiCard
                label="Receive-to-commit p95"
                value={health?.receive_to_commit_p95_ms ?? null}
                unit="ms"
                precision={2}
                formatValue={formatMetric}
                state={health?.receive_to_commit_p95_ms == null && health ? "not_observed" : healthState}
                stateReason="A durable per-event commit timestamp is not available; observation age is shown separately."
              />
              <KpiCard
                label="Observation age p95"
                value={health?.observation_age_p95_seconds ?? null}
                unit="s"
                precision={2}
                formatValue={formatMetric}
                state={health?.observation_age_p95_seconds == null && health ? "not_observed" : healthState}
              />
              <KpiCard label="Replays" value={health?.replay_count ?? null} state={healthState} />
              <KpiCard label="Late/backfill candidates" value={health?.late_backfill_candidate_count ?? null} state={healthState} />
              <KpiCard label="Clock-skew events" value={health?.clock_skew_event_count ?? null} state={healthState} />
              <KpiCard label="Active sources" value={health?.active_source_count ?? null} state={healthState} />
              <KpiCard label="Sequence gaps" value={health?.source_gap_count ?? null} state={healthState} />
              <KpiCard label="Queue depth" value={health?.queue_depth ?? null} state={healthState} />
              <KpiCard label="Pending rollups" value={health?.pending_rollup_count ?? null} state={healthState} />
            </div>
            <GapNote>
              Receive-to-commit, observation age, replay, late/backfill candidates and declared
              clock skew are separate populations. Late/backfill candidates use the versioned
              five-minute arrival-gap rule; they are not silently called live latency.
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
          caption={`${incidentPage?.total_state ?? "unknown"} total ≥ ${incidentPage?.total_lower_bound ?? 0}; page formula ${incidentPage?.formula_version ?? "unknown"}; DOM capped at 200 rows`}
        >
          <form className="k-workbench-filters" onSubmit={(event) => applyTextFilters("incidents", event)}>
            <Dropdown
              caption="DETECTOR"
              value={page.state ?? ""}
              onChange={(value) => setFilter("state", value)}
              options={[
                { value: "", label: "All" },
                { value: "open", label: "Open" },
                { value: "recovering", label: "Recovering" },
                { value: "resolved", label: "Resolved" },
              ]}
            />
            <Dropdown
              caption="TRIAGE"
              value={page.triage ?? ""}
              onChange={(value) => setFilter("triage", value)}
              options={[
                { value: "", label: "All" },
                { value: "new", label: "New" },
                { value: "acknowledged", label: "Acknowledged" },
                { value: "investigating", label: "Investigating" },
                { value: "action_ready", label: "Action ready" },
              ]}
            />
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
            emptyMessage={incidents.isLoading ? "Loading…" : incidents.isError ? "Incident query failed." : "No incidents match these filters."}
          />
          {incidents.isError && <p role="alert">Incident history is unavailable. Retry the current view.</p>}
          <LoadMoreControl
            label="incidents"
            rowCount={incidentRows.length}
            hasMore={Boolean(incidents.hasNextPage)}
            loading={incidents.isFetchingNextPage}
            onLoad={() => void incidents.fetchNextPage()}
          />
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
          <form className="k-workbench-filters" onSubmit={(event) => applyTextFilters("quarantine", event)}>
            <label>Fingerprint<input name="fingerprint" defaultValue={page.fingerprint ?? ""} /></label>
            <label>Source<input name="source" defaultValue={page.source ?? ""} /></label>
            <button type="submit">Apply filters</button>
          </form>
          <DataTable
            columns={quarantineColumns}
            rows={quarantineRows}
            rowKey={(row) => row.quarantine_id}
            emptyMessage={quarantine.isLoading ? "Loading…" : quarantine.isError ? "Quarantine query failed." : "No quarantine manifests observed."}
          />
          {quarantine.isError && <p role="alert">Quarantine history is unavailable. Retry the current view.</p>}
          <LoadMoreControl
            label="quarantine manifests"
            rowCount={quarantineRows.length}
            hasMore={Boolean(quarantine.hasNextPage)}
            loading={quarantine.isFetchingNextPage}
            onLoad={() => void quarantine.fetchNextPage()}
          />
        </Panel>
      )}

      {page.tab === "quarantine" && page.quarantineID && (
        <QuarantineProfile manifest={manifest.data?.data} loading={manifest.isLoading} />
      )}
    </section>
  );
}

function LoadMoreControl({
  label,
  rowCount,
  hasMore,
  loading,
  onLoad,
}: {
  label: string;
  rowCount: number;
  hasMore: boolean;
  loading: boolean;
  onLoad: () => void;
}) {
  const sentinel = useRef<HTMLSpanElement>(null);
  const withinLimit = rowCount < 200;
  useEffect(() => {
    const node = sentinel.current;
    if (!node || !hasMore || !withinLimit || loading || !("IntersectionObserver" in window)) {
      return;
    }
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) onLoad();
    }, { rootMargin: "240px" });
    observer.observe(node);
    return () => observer.disconnect();
  }, [hasMore, loading, onLoad, withinLimit]);

  if (!hasMore) return null;
  if (!withinLimit) {
    return <p className="t-caption">200-row DOM limit reached. Refine filters to continue.</p>;
  }
  return (
    <div>
      <span ref={sentinel} aria-hidden="true" />
      <button type="button" onClick={onLoad} disabled={loading}>
        {loading ? "Loading…" : `Load more ${label}`}
      </button>
    </div>
  );
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
      <Panel title="Incident profile" actions={<Link href={tabHref("incidents")}>Back to incidents</Link>}>
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
    <Panel title="Structural manifest" actions={<Link href={tabHref("quarantine")}>Back to quarantine</Link>}>
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
