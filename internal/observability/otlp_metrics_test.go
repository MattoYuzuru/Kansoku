package observability

import (
	"testing"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"

	"kansoku.local/kansoku/internal/claudeadapter"
)

// Until now the metric lane read a data point's attributes and nothing else --
// not the metric's name, not its unit, not the point's value. Because a metric
// point carries no event.name, routing it through the event lane made every
// point fall back to the instrumentation-scope name, fail event
// canonicalization and land in quarantine. Four documented measurements
// including the only "time actually spent" signal any agent reports were lost
// that way. These tests pin the name/unit/value read and the closed-world
// behaviour that replaced it.

// captureMetricSink records samples so a test can assert on what the lane
// produced without needing PostgreSQL.
type captureMetricSink struct {
	samples []MetricSample
}

func (c *captureMetricSink) PersistNormalizedFact(Event, Evidence) error { return nil }

func (c *captureMetricSink) PersistMetricSample(sample MetricSample) error {
	c.samples = append(c.samples, sample)
	return nil
}

func claudeMetricRequest(serviceName, scopeName, metricName, unit string, point *metricsv1.NumberDataPoint) *collectormetricsv1.ExportMetricsServiceRequest {
	metric := &metricsv1.Metric{
		Name: metricName,
		Unit: unit,
		Data: &metricsv1.Metric_Sum{Sum: &metricsv1.Sum{
			DataPoints: []*metricsv1.NumberDataPoint{point},
		}},
	}
	scope := &metricsv1.ScopeMetrics{
		Scope:   &commonv1.InstrumentationScope{Name: scopeName},
		Metrics: []*metricsv1.Metric{metric},
	}
	return &collectormetricsv1.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricsv1.ResourceMetrics{{
			Resource: realResource(serviceName), ScopeMetrics: []*metricsv1.ScopeMetrics{scope},
		}},
	}
}

func metricReceiver(t *testing.T) (*FileStore, *OTLPReceiver, *captureMetricSink) {
	t.Helper()
	store, ingestor, _ := testIngestor(t, 4<<20)
	sink := &captureMetricSink{}
	if err := ingestor.ConfigureDurableFactSink(sink); err != nil {
		t.Fatalf("ConfigureDurableFactSink: %v", err)
	}
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)
	return store, receiver, sink
}

func TestDocumentedMetricNameUnitAndIntegerValueAreAllRead(t *testing.T) {
	store, receiver, sink := metricReceiver(t)
	point := &metricsv1.NumberDataPoint{
		TimeUnixNano: uint64(fixedTime.UnixNano()),
		Value:        &metricsv1.NumberDataPoint_AsInt{AsInt: 4_294_967_296},
		Attributes: []*commonv1.KeyValue{
			stringKV(string(claudeadapter.MetricAttributeSessionID), "metric-session-01"),
			stringKV(string(claudeadapter.MetricAttributeModel), "claude-opus-5"),
			stringKV(string(claudeadapter.MetricAttributeTokenType), "cacheRead"),
			stringKV(string(claudeadapter.MetricAttributeStartType), "fresh"),
		},
	}
	request := claudeMetricRequest(claudeadapter.OTLPResourceServiceName,
		"com.anthropic.claude_code", string(claudeadapter.MetricTokenUsage), "tokens", point)

	if err := receiver.ingestMetrics(request, SourceOTLPMetric); err != nil {
		t.Fatalf("documented metric rejected: %v", err)
	}
	if len(sink.samples) != 1 {
		t.Fatalf("samples=%d, want 1", len(sink.samples))
	}
	sample := sink.samples[0]
	if sample.MetricName != string(claudeadapter.MetricTokenUsage) {
		t.Errorf("metric_name=%q", sample.MetricName)
	}
	if sample.Unit != "tokens" {
		t.Errorf("unit=%q, want tokens: the unit was never read before", sample.Unit)
	}
	// A token count above 2^32 must survive exactly. Reading it as a double
	// would still be "a number" and would still look plausible.
	if sample.ValueInt == nil || *sample.ValueInt != 4_294_967_296 {
		t.Errorf("value_int=%v, want 4294967296 with no precision loss", sample.ValueInt)
	}
	if sample.ValueDouble != nil {
		t.Error("an integer point must not also be recorded as a double")
	}
	if sample.Dimension != "cacheRead" {
		t.Errorf("dimension=%q, want cacheRead: without it the metric is one opaque total", sample.Dimension)
	}
	if sample.ModelID != "claude-opus-5" || sample.StartType != "fresh" {
		t.Errorf("dimensions model=%q start_type=%q", sample.ModelID, sample.StartType)
	}
	if state := store.Snapshot(); len(state.Quarantine) != 0 {
		t.Errorf("quarantine=%d, want 0 for a documented metric", len(state.Quarantine))
	}
}

func TestMetricSessionHandleMatchesTheEventLaneHandle(t *testing.T) {
	// A metric sample is only useful if it joins to the sessions the log lane
	// created. Both handles are derived from the same keyed pseudonym, and
	// this test is what stops the two derivations from drifting apart.
	store, ingestor, _ := testIngestor(t, 4<<20)
	sink := &captureMetricSink{}
	if err := ingestor.ConfigureDurableFactSink(sink); err != nil {
		t.Fatalf("ConfigureDurableFactSink: %v", err)
	}
	receiver, _ := NewOTLPReceiver(ingestor, 1<<20)

	const sessionValue = "shared-session-id"
	logRequest := realLogRequest(claudeadapter.OTLPResourceServiceName,
		string(claudeadapter.OTelUserPrompt),
		[]*commonv1.KeyValue{
			stringKV(string(claudeadapter.NativeAttributeSessionID), sessionValue),
			intKV(string(claudeadapter.NativeAttributePromptLength), 12),
		})
	if err := receiver.ingestLogs(logRequest, SourceOTLPLog); err != nil {
		t.Fatalf("log rejected: %v", err)
	}
	point := &metricsv1.NumberDataPoint{
		TimeUnixNano: uint64(fixedTime.UnixNano()),
		Value:        &metricsv1.NumberDataPoint_AsDouble{AsDouble: 0.42},
		Attributes: []*commonv1.KeyValue{
			stringKV(string(claudeadapter.MetricAttributeSessionID), sessionValue),
		},
	}
	metricReq := claudeMetricRequest(claudeadapter.OTLPResourceServiceName,
		"com.anthropic.claude_code", string(claudeadapter.MetricCostUsage), "USD", point)
	if err := receiver.ingestMetrics(metricReq, SourceOTLPMetric); err != nil {
		t.Fatalf("metric rejected: %v", err)
	}
	if len(sink.samples) != 1 {
		t.Fatalf("samples=%d, want 1", len(sink.samples))
	}
	var eventSession string
	for _, fact := range store.Snapshot().Facts {
		eventSession = fact.Event.Scope.SessionID
	}
	if eventSession == "" {
		t.Fatal("log lane produced no session handle to compare against")
	}
	if sink.samples[0].SessionID != eventSession {
		t.Errorf("metric session handle %q != event session handle %q; the two lanes cannot be joined",
			sink.samples[0].SessionID, eventSession)
	}
	if sink.samples[0].ValueDouble == nil || *sink.samples[0].ValueDouble != 0.42 {
		t.Errorf("value_double=%v, want 0.42", sink.samples[0].ValueDouble)
	}
}

func TestUndocumentedMetricNameIsQuarantinedNotStored(t *testing.T) {
	store, receiver, sink := metricReceiver(t)
	point := &metricsv1.NumberDataPoint{
		TimeUnixNano: uint64(fixedTime.UnixNano()),
		Value:        &metricsv1.NumberDataPoint_AsInt{AsInt: 1},
	}
	request := claudeMetricRequest(claudeadapter.OTLPResourceServiceName,
		"com.anthropic.claude_code", "claude_code.something.new", "1", point)
	if err := receiver.ingestMetrics(request, SourceOTLPMetric); err != nil {
		t.Fatalf("ingestMetrics returned a hard error instead of quarantining: %v", err)
	}
	if len(sink.samples) != 0 {
		t.Errorf("samples=%d, want 0: an undocumented metric must not be guessed at", len(sink.samples))
	}
	if state := store.Snapshot(); len(state.Quarantine) == 0 {
		t.Error("an undocumented metric name produced no quarantine record")
	}
}

func TestUnsupportedMetricShapeIsQuarantinedRatherThanVanishing(t *testing.T) {
	// A histogram yields no number points. numberPoints returned an empty
	// slice for it, and the point loop simply never ran -- the metric left no
	// trace at all, which reads exactly like "the agent sent nothing".
	store, receiver, sink := metricReceiver(t)
	metric := &metricsv1.Metric{
		Name: string(claudeadapter.MetricActiveTime),
		Unit: "s",
		Data: &metricsv1.Metric_Histogram{Histogram: &metricsv1.Histogram{
			DataPoints: []*metricsv1.HistogramDataPoint{{TimeUnixNano: uint64(fixedTime.UnixNano())}},
		}},
	}
	request := &collectormetricsv1.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricsv1.ResourceMetrics{{
			Resource: realResource(claudeadapter.OTLPResourceServiceName),
			ScopeMetrics: []*metricsv1.ScopeMetrics{{
				Scope:   &commonv1.InstrumentationScope{Name: "com.anthropic.claude_code"},
				Metrics: []*metricsv1.Metric{metric},
			}},
		}},
	}
	if err := receiver.ingestMetrics(request, SourceOTLPMetric); err != nil {
		t.Fatalf("ingestMetrics: %v", err)
	}
	if len(sink.samples) != 0 {
		t.Errorf("samples=%d, want 0", len(sink.samples))
	}
	if state := store.Snapshot(); len(state.Quarantine) == 0 {
		t.Error("an unsupported metric point shape vanished without a quarantine record")
	}
}

func TestReplayedMetricPointKeepsOneIdempotencyKey(t *testing.T) {
	// The OTLP exporter resends after any 5xx. Two identical points must
	// carry the same idempotency key so the database can reject the second
	// rather than double a cost.
	_, receiver, sink := metricReceiver(t)
	build := func() *collectormetricsv1.ExportMetricsServiceRequest {
		return claudeMetricRequest(claudeadapter.OTLPResourceServiceName, "com.anthropic.claude_code",
			string(claudeadapter.MetricSessionCount), "1", &metricsv1.NumberDataPoint{
				TimeUnixNano: uint64(fixedTime.UnixNano()),
				Value:        &metricsv1.NumberDataPoint_AsInt{AsInt: 1},
				Attributes: []*commonv1.KeyValue{
					stringKV(string(claudeadapter.MetricAttributeSessionID), "replayed-session"),
				},
			})
	}
	for range 2 {
		if err := receiver.ingestMetrics(build(), SourceOTLPMetric); err != nil {
			t.Fatalf("ingestMetrics: %v", err)
		}
	}
	if len(sink.samples) != 2 {
		t.Fatalf("samples=%d, want 2 delivered attempts", len(sink.samples))
	}
	if sink.samples[0].IdempotencyKey != sink.samples[1].IdempotencyKey {
		t.Error("a replayed identical point produced two different idempotency keys")
	}
	if sink.samples[0].SampleID != sink.samples[1].SampleID {
		t.Error("a replayed identical point produced two different sample ids")
	}
}
