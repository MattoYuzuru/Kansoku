package dataplatform

import "time"

const (
	SchemaSpecVersion = "kansoku.data-platform-schema/1"
)

// FactRow is the normalized row shape written to the partitioned `events`
// table. It intentionally mirrors the closed Session 03
// internal/observability.Event allowlist rather than introducing a second
// divergent canonical shape; Session 04 is the durable system of record that
// replaces internal/observability.FileStore.
type FactRow struct {
	EventID             string
	FactKey             string
	EventType           string
	ObservedAt          time.Time
	IngestedAt          time.Time
	TimestampQuality    string
	SourceInstanceID    string
	SourceNativeEventID string
	Sequence            int64
	AgentInstallationID string
	SurfaceID           string
	ProjectID           string
	SessionID           string
	TurnID              string
	ComponentID         string
	DurationMS          *int64
	Success             *bool
	Count               *int64
	ValueState          string
	Outcome             string
	CorrelationStatus   string
}

// EvidenceRow mirrors internal/observability.Evidence.
type EvidenceRow struct {
	EvidenceID        string
	EventID           string
	ObservedAt        time.Time
	SourceInstanceID  string
	Tier              string
	Confidence        float64
	Completeness      string
	ReplayCount       int64
	FirstSeenAt       time.Time
	LastSeenAt        time.Time
	SanitizerVersion  string
	PrivacyContractID string
	AssertEventType   string
	AssertOutcome     string
	AssertValueState  string
}

// Granularity is a rollup bucket size.
type Granularity string

const (
	GranularityHourly Granularity = "hourly"
	GranularityDaily  Granularity = "daily"
)

// Percentiles holds exact percentile_cont results for one metric bucket.
// Never averaged across buckets: each value here is computed directly from
// normalized facts inside its own bucket.
type Percentiles struct {
	P50 *float64 `json:"p50"`
	P90 *float64 `json:"p90"`
	P95 *float64 `json:"p95"`
	P99 *float64 `json:"p99"`
}

// RollupRow is one hourly/daily aggregate for one metric family, bucket and
// dimension scope, matching contracts/data-platform/rollups.yaml
// `rollup_row_fields`.
type RollupRow struct {
	MetricFamily           string
	Granularity            Granularity
	BucketStart            time.Time
	DimensionScope         string
	FormulaVersion         string
	EventCount             int64
	UnknownCount           int64
	CompletenessDurationMS int64
	ValueNumeric           *float64
	Percentiles            Percentiles
	ComputedAt             time.Time
}

// RepairKey identifies one late-data repair unit, coalesced by the queue's
// unique constraint before a worker claims it.
type RepairKey struct {
	MetricFamily   string
	Granularity    Granularity
	BucketStart    time.Time
	DimensionScope string
}

// Population is the completeness-aware query contract's numerator/denominator pair.
type Population struct {
	Numerator   int64 `json:"numerator"`
	Denominator int64 `json:"denominator"`
}

// Completeness mirrors contracts/data-platform/query-contract.yaml `completeness_fields`.
type Completeness struct {
	Status       string   `json:"status"`
	CoveredRatio float64  `json:"covered_ratio"`
	Intervals    []string `json:"intervals"`
}

// Freshness mirrors the query contract's `freshness_fields`.
type Freshness struct {
	RollupWatermark   time.Time `json:"rollup_watermark"`
	LateEventsPending int64     `json:"late_events_pending"`
}

// QueryResponse is the closed completeness-aware analytics envelope from
// contracts/data-platform/query-contract.yaml `response_fields`.
type QueryResponse struct {
	Data           []RollupPoint `json:"data"`
	FormulaVersion string        `json:"formula_version"`
	Population     Population    `json:"population"`
	Completeness   Completeness  `json:"completeness"`
	Freshness      Freshness     `json:"freshness"`
}

// RollupPoint is one bucketed datum inside a QueryResponse's `data` series.
type RollupPoint struct {
	BucketStart time.Time    `json:"bucket_start"`
	Value       *float64     `json:"value"`
	Percentiles *Percentiles `json:"percentiles,omitempty"`
	EventCount  int64        `json:"event_count"`
}

// EntityRow is one grouped-entity datum inside an EntityBreakdownResponse,
// e.g. one agent, one model or one component within the requested range.
// Percentiles is nil when the caller did not request a latency aggregate for
// this entity kind (e.g. an agent breakdown has no latency dimension).
type EntityRow struct {
	EntityID            string       `json:"entity_id"`
	AgentID             string       `json:"agent_id,omitempty"`
	EventCount          int64        `json:"event_count"`
	SuccessCount        int64        `json:"success_count"`
	FailureCount        int64        `json:"failure_count"`
	CostedCount         int64        `json:"costed_count,omitempty"`
	EstimatedCostMicros int64        `json:"estimated_cost_micros,omitempty"`
	Value               *float64     `json:"value,omitempty"`
	Percentiles         *Percentiles `json:"percentiles,omitempty"`
}

// EntityBreakdownResponse is the closed completeness-aware envelope for a
// "group and rank across entities within a time range" query (per-agent,
// per-model, per-component/tool breakdowns), matching the same
// response_fields shape as QueryResponse from
// contracts/data-platform/query-contract.yaml so callers never need a
// second parallel completeness convention.
type EntityBreakdownResponse struct {
	Data           []EntityRow  `json:"data"`
	FormulaVersion string       `json:"formula_version"`
	Population     Population   `json:"population"`
	Completeness   Completeness `json:"completeness"`
	Freshness      Freshness    `json:"freshness"`
}

// FunnelStageRow is one lifecycle stage's eligible-component count inside a
// ComponentLifecycleFunnel response, matching the canonical progression in
// contracts/capabilities.yaml `lifecycle.canonical_progression` (plus the
// parallel `opportunity_detected` state).
type FunnelStageRow struct {
	Stage          string `json:"stage"`
	ComponentCount int64  `json:"component_count"`
	EventCount     int64  `json:"event_count"`
	ValueState     string `json:"value_state"`
}

// FunnelResponse is the closed completeness-aware envelope for a component
// lifecycle funnel grouped by canonical stage within a component kind
// (skill/plugin/mcp/hook/command) and time range.
type FunnelResponse struct {
	Data           []FunnelStageRow `json:"data"`
	FormulaVersion string           `json:"formula_version"`
	Population     Population       `json:"population"`
	Completeness   Completeness     `json:"completeness"`
	Freshness      Freshness        `json:"freshness"`
}

// InventoryComponentRow is the privacy-safe current inventory projection
// shown on component pages. It contains no raw path or manifest content.
type InventoryComponentRow struct {
	ComponentID         string    `json:"component_id"`
	DeclaredName        string    `json:"declared_name"`
	Kind                string    `json:"kind"`
	SourceScope         string    `json:"source_scope"`
	Version             string    `json:"version,omitempty"`
	VersionState        string    `json:"version_state"`
	Enabled             bool      `json:"enabled"`
	AgentID             string    `json:"agent_id"`
	AgentInstallationID string    `json:"agent_installation_id"`
	FirstSeenAt         time.Time `json:"first_seen_at"`
	LastSeenAt          time.Time `json:"last_seen_at"`
}

type InventoryComponentResponse struct {
	Data           []InventoryComponentRow `json:"data"`
	FormulaVersion string                  `json:"formula_version"`
	Population     Population              `json:"population"`
	Completeness   Completeness            `json:"completeness"`
	Freshness      Freshness               `json:"freshness"`
}

// ReliabilityDayRow is one source's one calendar day of coverage inside a
// ReliabilityTimelineResponse.
type ReliabilityDayRow struct {
	Day              time.Time `json:"day"`
	SourceInstanceID string    `json:"source_instance_id"`
	Status           string    `json:"status"`
	IntervalCount    int64     `json:"interval_count"`
}

// ReliabilityTimelineResponse is the closed completeness-aware envelope for
// the /reliability "coverage timeline; source gaps/watermarks" panel: one
// row per (source, day) combination observed in completeness_intervals
// within the requested range.
type ReliabilityTimelineResponse struct {
	Data           []ReliabilityDayRow `json:"data"`
	FormulaVersion string              `json:"formula_version"`
	Population     Population          `json:"population"`
	Completeness   Completeness        `json:"completeness"`
	Freshness      Freshness           `json:"freshness"`
}

// ComponentTreeNode is one component in the MCP server/tool topology tree:
// an MCP server (parent) and its declared child tools/components, plus the
// most recent observed connection state when available. Only opaque IDs are
// carried -- never a raw command, path or credential.
type ComponentTreeNode struct {
	ComponentID           string     `json:"component_id"`
	Kind                  string     `json:"kind"`
	ChildComponentIDs     []string   `json:"child_component_ids"`
	LatestConnectionState string     `json:"latest_connection_state,omitempty"`
	ConnectionObservedAt  *time.Time `json:"connection_observed_at,omitempty"`
}

// ComponentTopologyResponse is the closed completeness-aware envelope for
// the dedicated MCP server tree route: no existing metric/dimension can
// represent a parent/child relationship tree, so this is the one genuinely
// new route added per ADR 0013 decision #12.
type ComponentTopologyResponse struct {
	Data           []ComponentTreeNode `json:"data"`
	FormulaVersion string              `json:"formula_version"`
	Population     Population          `json:"population"`
	Completeness   Completeness        `json:"completeness"`
}

// ActivityDayRow is one calendar day's activity volume inside an
// ActivityTimelineResponse: distinct session/prompt counts observed that
// day, plus a reconstructed active-duration estimate (see
// ActivityTimeline's doc comment for why this is "reconstructed" rather
// than "exact").
type ActivityDayRow struct {
	Day                   time.Time `json:"day"`
	SessionCount          int64     `json:"session_count"`
	PromptCount           int64     `json:"prompt_count"`
	ActiveDurationSeconds *float64  `json:"active_duration_seconds"`
}

// ActivityTimelineResponse is the closed completeness-aware envelope for the
// "/" overview-activity panel and the /activity activity-timeline panel:
// one row per calendar day inside the requested range.
type ActivityTimelineResponse struct {
	Data           []ActivityDayRow `json:"data"`
	FormulaVersion string           `json:"formula_version"`
	Population     Population       `json:"population"`
	Completeness   Completeness     `json:"completeness"`
	Freshness      Freshness        `json:"freshness"`
}

// PromptShapeDayRow is one calendar day's submitted-prompt count and exact
// byte-length percentiles inside a PromptShapeResponse.
type PromptShapeDayRow struct {
	Day                  time.Time    `json:"day"`
	PromptCount          int64        `json:"prompt_count"`
	Percentiles          *Percentiles `json:"percentiles,omitempty"`
	CharacterPercentiles *Percentiles `json:"character_percentiles,omitempty"`
}

// PromptShapeResponse is the closed completeness-aware envelope for the
// /prompts "prompt-shape" panel: one row per calendar day inside the
// requested range, sourced from prompt_features.prompt_size_bytes. Raw
// prompt text is never read or returned -- only the byte-length metadata
// column.
type PromptShapeResponse struct {
	Data           []PromptShapeDayRow `json:"data"`
	FormulaVersion string              `json:"formula_version"`
	Population     Population          `json:"population"`
	Completeness   Completeness        `json:"completeness"`
	Freshness      Freshness           `json:"freshness"`
}

// ModelUsageDayRow is one calendar day's model-usage volume/cost inside a
// ModelUsageResponse. Percentiles and ErrorRatio are nil when no native
// request/response observation in that day carries duration/outcome, which
// is an honest "not observed" rather than a fabricated zero.
type ModelUsageDayRow struct {
	Day                 time.Time    `json:"day"`
	RequestCount        int64        `json:"request_count"`
	TotalTokens         int64        `json:"total_tokens"`
	EstimatedCostMicros int64        `json:"estimated_cost_micros"`
	CostedRequestCount  int64        `json:"costed_request_count"`
	ProviderCostCount   int64        `json:"provider_cost_count"`
	UpperBoundCostCount int64        `json:"upper_bound_cost_count"`
	Percentiles         *Percentiles `json:"percentiles,omitempty"`
	ErrorRatio          *float64     `json:"error_ratio,omitempty"`
	MatchedEventCount   int64        `json:"matched_event_count"`
}

// ModelUsageResponse is the closed completeness-aware envelope for the
// /models "model-usage" and "model-cost" panels: one row per calendar day
// inside the requested range, the time-series companion to ModelBreakdown's
// per-model leaderboard.
type ModelUsageResponse struct {
	Data           []ModelUsageDayRow `json:"data"`
	FormulaVersion string             `json:"formula_version"`
	Population     Population         `json:"population"`
	Completeness   Completeness       `json:"completeness"`
	Freshness      Freshness          `json:"freshness"`
}

// ToolAnalyticsDayRow is one calendar day's tool-call volume/latency inside
// a ToolAnalyticsResponse, optionally restricted to one component/MCP
// server.
type ToolAnalyticsDayRow struct {
	Day          time.Time    `json:"day"`
	CallCount    int64        `json:"call_count"`
	SuccessCount int64        `json:"success_count"`
	FailureCount int64        `json:"failure_count"`
	Percentiles  *Percentiles `json:"percentiles,omitempty"`
}

// ToolAnalyticsResponse is the closed completeness-aware envelope shared by
// the /tools "tool-analytics" panel and the /components/mcp "mcp-health"
// panel's call/latency series, filtered by an optional component_id.
type ToolAnalyticsResponse struct {
	Data           []ToolAnalyticsDayRow `json:"data"`
	FormulaVersion string                `json:"formula_version"`
	Population     Population            `json:"population"`
	Completeness   Completeness          `json:"completeness"`
	Freshness      Freshness             `json:"freshness"`
}

// MCPUptimeRow is one MCP server component's observed connection-state
// coverage inside an MCPUptimeResponse: ConnectedSeconds is the summed
// duration of "connected" state intervals reconstructed from consecutive
// mcp_connections.state observations, and ObservableSeconds is the summed
// duration between the first and last observed state change for that
// component in range (the only span over which uptime is actually
// observable -- never the full requested range when observation started
// later or stopped earlier).
type MCPUptimeRow struct {
	ComponentID       string   `json:"component_id"`
	ConnectedSeconds  float64  `json:"connected_seconds"`
	ObservableSeconds float64  `json:"observable_seconds"`
	UptimeRatio       *float64 `json:"uptime_ratio,omitempty"`
}

// MCPUptimeResponse is the closed completeness-aware envelope for the MCP
// connection uptime ratio, serving /components/mcp "mcp-health".
type MCPUptimeResponse struct {
	Data           []MCPUptimeRow `json:"data"`
	FormulaVersion string         `json:"formula_version"`
	Population     Population     `json:"population"`
	Completeness   Completeness   `json:"completeness"`
}

// ReliabilityCountsDayRow is one calendar day's reliability incident counts
// inside a ReliabilityCountsResponse.
type ReliabilityCountsDayRow struct {
	Day                         time.Time `json:"day"`
	UnknownSchemaCount          int64     `json:"unknown_schema_count"`
	ReconciliationMismatchCount int64     `json:"reconciliation_mismatch_count"`
}

// ReliabilityCountsResponse is the closed completeness-aware envelope for
// reliability.unknown_schema_count (schema_quarantine_metadata) and
// reliability.reconciliation_mismatch_count (reconciliation_mismatches
// joined to reconciliation_runs for a bucketable day), serving the "/"
// overview-incidents panel and /reliability reliability-drift panel.
type ReliabilityCountsResponse struct {
	Data           []ReliabilityCountsDayRow `json:"data"`
	FormulaVersion string                    `json:"formula_version"`
	Population     Population                `json:"population"`
	Completeness   Completeness              `json:"completeness"`
}

// SystemSnapshotResponse is the closed completeness-aware envelope for a
// single non-time-series snapshot of durable system-size/backup-age facts:
// pg_database_size(current_database()) plus integrity_backup_status's
// last verified backup/restore-test ages. Deliberately does not include
// collector_cpu_ratio, collector_rss_bytes, database_growth_bytes_per_day
// or common_query_latency_seconds -- none of those have a durable backing
// table (they are live-process-only, per internal/runtime/diagnostics.go),
// and this dataplatform function never touches that admin-gated
// diagnostics path.
type SystemSnapshotResponse struct {
	DatabaseSizeBytes     int64        `json:"database_size_bytes"`
	BackupAgeSeconds      *float64     `json:"backup_age_seconds"`
	BackupChecksumOK      *bool        `json:"backup_checksum_ok,omitempty"`
	RestoreTestAgeSeconds *float64     `json:"restore_test_age_seconds"`
	RestoreTestPassed     *bool        `json:"restore_test_passed,omitempty"`
	FormulaVersion        string       `json:"formula_version"`
	Population            Population   `json:"population"`
	Completeness          Completeness `json:"completeness"`
}

// PrivacyCanaryDayRow is one calendar day's pass/fail counts for the
// integrity privacy-canary check inside a PrivacyCanaryHistoryResponse.
// This is a check-history datum, never a literal
// privacy.raw_content_persisted_count -- no such exact count exists
// anywhere in the schema.
type PrivacyCanaryDayRow struct {
	Day       time.Time `json:"day"`
	PassCount int64     `json:"pass_count"`
	FailCount int64     `json:"fail_count"`
}

// PrivacyCanaryHistoryResponse is the closed completeness-aware envelope
// for the privacy-canary integrity check's pass/fail history, sourced from
// integrity_audit_checks (check_id = "stage_9_retention_disk_and_backup",
// source_id = "privacy-canary") joined to integrity_audit_runs. Serves the
// /privacy "privacy-canary" panel as an honest check-history timeline, not
// a fabricated exact violation count.
type PrivacyCanaryHistoryResponse struct {
	Data           []PrivacyCanaryDayRow `json:"data"`
	FormulaVersion string                `json:"formula_version"`
	Population     Population            `json:"population"`
	Completeness   Completeness          `json:"completeness"`
}
