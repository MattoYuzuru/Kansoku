package observability

import (
	"errors"
	"time"
)

// MetricSample is the closed, content-free durable shape of one OTLP metric
// data point. It is deliberately a separate lane from Event: a metric is not
// an activity, it has no outcome, and folding it into the event stream would
// have added rows to counts that mean "something happened".
//
// The two value fields preserve the wire's own distinction. An integer point
// and a floating-point point are different measurements, and coercing an int
// counter into a float would silently lose exactness on large token counts.
type MetricSample struct {
	SampleID    string    `json:"sample_id"`
	MetricName  string    `json:"metric_name"`
	Unit        string    `json:"unit"`
	ValueInt    *int64    `json:"value_int"`
	ValueDouble *float64  `json:"value_double"`
	ObservedAt  time.Time `json:"observed_at"`
	IngestedAt  time.Time `json:"ingested_at"`

	AdapterID           string     `json:"adapter_id"`
	AdapterVersion      string     `json:"adapter_version"`
	SourceKind          SourceKind `json:"source_kind"`
	SchemaFingerprint   string     `json:"schema_fingerprint"`
	AgentInstallationID string     `json:"agent_installation_id"`

	// Dimensions are the allowlisted attributes the point carried. Handles
	// are the same prefixed pseudonyms the event lane derives, so a sample
	// joins to sessions and models without a second identity scheme.
	SessionID    string `json:"session_id"`
	UserID       string `json:"user_id"`
	ModelID      string `json:"model_id"`
	TerminalType string `json:"terminal_type"`
	QuerySource  string `json:"query_source"`
	StartType    string `json:"start_type"`
	// Dimension splits a metric into its reported kinds -- for
	// claude_code.token.usage this is input/output/cacheCreation/cacheRead.
	// An unrecognized value is kept verbatim, never coerced.
	Dimension string `json:"dimension"`

	IdempotencyKey string `json:"idempotency_key"`
}

// MetricSampleSink is an optional capability of a DurableFactSink. The metric
// lane is additive: a sink that does not implement it simply causes metrics
// to be rejected as undurable rather than silently discarded.
type MetricSampleSink interface {
	PersistMetricSample(MetricSample) error
}

// ErrMetricLaneUnavailable is returned when a metric sample was recognized
// and sanitized but no durable sink can store it. It is deliberately distinct
// from quarantine: the data was understood, the storage was not there, and
// the caller should retry rather than record a schema failure.
var ErrMetricLaneUnavailable = errors.New("metric_lane_unavailable_retryable")

// IngestMetricSample records one recognized metric point. The sample must
// already be sanitized: this method never sees a raw attribute.
func (i *Ingestor) IngestMetricSample(sample MetricSample) error {
	if !i.acquire() {
		return ErrBackpressure
	}
	defer i.release()
	if sample.MetricName == "" || sample.SampleID == "" {
		return errors.New("invalid_metric_sample")
	}
	if sample.ValueInt == nil && sample.ValueDouble == nil {
		// A point with no value is not a zero measurement; it is a shape this
		// build does not understand, and the caller quarantines it.
		return errors.New("metric_sample_without_value")
	}
	sample.IngestedAt = i.now().UTC()
	i.sinkMu.RLock()
	sink := i.durableSink
	i.sinkMu.RUnlock()
	metricSink, ok := sink.(MetricSampleSink)
	if !ok {
		return ErrMetricLaneUnavailable
	}
	return metricSink.PersistMetricSample(sample)
}
