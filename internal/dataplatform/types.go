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
	P50 *float64
	P90 *float64
	P95 *float64
	P99 *float64
}

// RollupRow is one hourly/daily aggregate for one metric family, bucket and
// dimension scope, matching contracts/data-platform/rollups.yaml
// `rollup_row_fields`.
type RollupRow struct {
	MetricFamily             string
	Granularity              Granularity
	BucketStart              time.Time
	DimensionScope           string
	FormulaVersion           string
	EventCount               int64
	UnknownCount             int64
	CompletenessDurationMS   int64
	ValueNumeric             *float64
	Percentiles              Percentiles
	ComputedAt               time.Time
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
	Status       string    `json:"status"`
	CoveredRatio float64   `json:"covered_ratio"`
	Intervals    []string  `json:"intervals"`
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
