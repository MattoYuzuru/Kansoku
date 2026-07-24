package observability

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	otlpContentType   = "application/x-protobuf"
	fixtureOTLPSchema = "fixture.agent-otlp/1"
	maxOTLPFrameBytes = 1 << 20
)

// FixtureOTLPSchemaID, FixtureHookSchemaID, FixtureAdapterID and
// FixtureAdapterVersion re-export the already contract-public identifiers
// contracts/observability/ingress.yaml declares for the conformance
// fixture-agent lane ("fixture.agent-otlp/1"/"fixture.agent-hook/1" source
// schemas, "fixture-agent"/"1.0.0" resource identity). They are not secrets:
// the values are the same literals ingress.yaml, otlp.go's knownResource and
// privacy.FixtureSourceSchema already hardcode. Exporting them lets
// internal/integrity's synthetic pipeline probe (stage_5) build a real OTLP
// request accepted by this package's own knownResource check without
// internal/integrity forking a second copy of these contract literals.
const (
	FixtureOTLPSchemaID   = fixtureOTLPSchema
	FixtureHookSchemaID   = "fixture.agent-hook/1"
	FixtureAdapterID      = "fixture-agent"
	FixtureAdapterVersion = "1.0.0"
)

type OTLPReceiver struct {
	ingestor *Ingestor
	maxBytes int64
}

func NewOTLPReceiver(ingestor *Ingestor, maxBytes int64) (*OTLPReceiver, error) {
	if ingestor == nil || maxBytes != maxOTLPFrameBytes {
		return nil, errors.New("invalid_otlp_receiver_configuration")
	}
	return &OTLPReceiver{ingestor: ingestor, maxBytes: maxBytes}, nil
}

func (r *OTLPReceiver) exportLogs(ctx context.Context, request *collectorlogsv1.ExportLogsServiceRequest) (*collectorlogsv1.ExportLogsServiceResponse, error) {
	if err := r.ingestLogs(request, SourceOTLPLog); err != nil {
		return nil, grpcError(err)
	}
	return &collectorlogsv1.ExportLogsServiceResponse{}, nil
}

// Generated OTLP services all name their RPC Export, so a single Go type
// cannot implement all three interfaces directly. The wrappers below keep one
// receiver implementation while preserving the official service definitions.
type LogsGRPC struct {
	collectorlogsv1.UnimplementedLogsServiceServer
	Receiver *OTLPReceiver
}

func (s LogsGRPC) Export(ctx context.Context, req *collectorlogsv1.ExportLogsServiceRequest) (*collectorlogsv1.ExportLogsServiceResponse, error) {
	return s.Receiver.exportLogs(ctx, req)
}

type MetricsGRPC struct {
	collectormetricsv1.UnimplementedMetricsServiceServer
	Receiver *OTLPReceiver
}

func (s MetricsGRPC) Export(ctx context.Context, req *collectormetricsv1.ExportMetricsServiceRequest) (*collectormetricsv1.ExportMetricsServiceResponse, error) {
	if err := s.Receiver.ingestMetrics(req, SourceOTLPMetric); err != nil {
		return nil, grpcError(err)
	}
	return &collectormetricsv1.ExportMetricsServiceResponse{}, nil
}

type TraceGRPC struct {
	collectortracev1.UnimplementedTraceServiceServer
	Receiver *OTLPReceiver
}

func (s TraceGRPC) Export(ctx context.Context, req *collectortracev1.ExportTraceServiceRequest) (*collectortracev1.ExportTraceServiceResponse, error) {
	if err := s.Receiver.ingestTraces(req, SourceOTLPSpan); err != nil {
		return nil, grpcError(err)
	}
	return &collectortracev1.ExportTraceServiceResponse{}, nil
}

func (r *OTLPReceiver) register(registrar grpc.ServiceRegistrar) {
	collectorlogsv1.RegisterLogsServiceServer(registrar, LogsGRPC{Receiver: r})
	collectormetricsv1.RegisterMetricsServiceServer(registrar, MetricsGRPC{Receiver: r})
	collectortracev1.RegisterTraceServiceServer(registrar, TraceGRPC{Receiver: r})
}

func grpcError(err error) error {
	if errors.Is(err, ErrBackpressure) {
		return status.Error(codes.Unavailable, "backpressure_retryable")
	}
	return status.Error(codes.InvalidArgument, "invalid_otlp")
}

func (r *OTLPReceiver) HTTPMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", func(w http.ResponseWriter, req *http.Request) { r.httpLogs(w, req) })
	mux.HandleFunc("/v1/metrics", func(w http.ResponseWriter, req *http.Request) { r.httpMetrics(w, req) })
	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, req *http.Request) { r.httpTraces(w, req) })
	return mux
}

func readProtoRequest(w http.ResponseWriter, req *http.Request, maximum int64, message proto.Message) bool {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeProtoStatus(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return false
	}
	if req.Header.Get("Content-Type") != otlpContentType {
		writeProtoStatus(w, http.StatusUnsupportedMediaType, "unsupported_content_type")
		return false
	}
	if req.Header.Get("Content-Encoding") != "" && req.Header.Get("Content-Encoding") != "identity" {
		// Session 02's reviewed privacy contract rejects compressed input until
		// streaming bomb limits are implemented; ADR 0006 records this OTLP gap.
		writeProtoStatus(w, http.StatusUnsupportedMediaType, "compressed_input_rejected")
		return false
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, maximum+1))
	if err != nil || int64(len(body)) > maximum {
		writeProtoStatus(w, http.StatusRequestEntityTooLarge, "oversized_input")
		return false
	}
	if err := proto.Unmarshal(body, message); err != nil {
		writeProtoStatus(w, http.StatusBadRequest, "invalid_otlp")
		return false
	}
	return true
}

func writeProtoStatus(w http.ResponseWriter, code int, message string) {
	encoded, _ := proto.Marshal(status.New(codes.InvalidArgument, message).Proto())
	w.Header().Set("Content-Type", otlpContentType)
	w.WriteHeader(code)
	_, _ = w.Write(encoded)
}

func writeProtoSuccess(w http.ResponseWriter, response proto.Message) {
	encoded, _ := proto.Marshal(response)
	w.Header().Set("Content-Type", otlpContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

func writeIngestError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrBackpressure) {
		w.Header().Set("Retry-After", "1")
		writeProtoStatus(w, http.StatusServiceUnavailable, "backpressure_retryable")
		return
	}
	writeProtoStatus(w, http.StatusBadRequest, "invalid_otlp")
}

func (r *OTLPReceiver) httpLogs(w http.ResponseWriter, req *http.Request) {
	message := &collectorlogsv1.ExportLogsServiceRequest{}
	if !readProtoRequest(w, req, r.maxBytes, message) {
		return
	}
	if err := r.ingestLogs(message, SourceOTLPLog); err != nil {
		writeIngestError(w, err)
		return
	}
	writeProtoSuccess(w, &collectorlogsv1.ExportLogsServiceResponse{})
}
func (r *OTLPReceiver) httpMetrics(w http.ResponseWriter, req *http.Request) {
	message := &collectormetricsv1.ExportMetricsServiceRequest{}
	if !readProtoRequest(w, req, r.maxBytes, message) {
		return
	}
	if err := r.ingestMetrics(message, SourceOTLPMetric); err != nil {
		writeIngestError(w, err)
		return
	}
	writeProtoSuccess(w, &collectormetricsv1.ExportMetricsServiceResponse{})
}
func (r *OTLPReceiver) httpTraces(w http.ResponseWriter, req *http.Request) {
	message := &collectortracev1.ExportTraceServiceRequest{}
	if !readProtoRequest(w, req, r.maxBytes, message) {
		return
	}
	if err := r.ingestTraces(message, SourceOTLPSpan); err != nil {
		writeIngestError(w, err)
		return
	}
	writeProtoSuccess(w, &collectortracev1.ExportTraceServiceResponse{})
}

func resourceIdentity(resource *resourcev1.Resource) (string, string, string) {
	if resource == nil {
		return "", "", ""
	}
	return stringAttribute(resource.Attributes, "service.name"), stringAttribute(resource.Attributes, "kansoku.adapter.version"), stringAttribute(resource.Attributes, "kansoku.source.schema")
}

func safeFields(attributes []*commonv1.KeyValue, timestamp uint64) (map[string]any, uint64, error) {
	fields := map[string]any{
		"event_id":    stringAttribute(attributes, "kansoku.event.id"),
		"session_id":  stringAttribute(attributes, "kansoku.session.id"),
		"event_type":  stringAttribute(attributes, "kansoku.event.type"),
		"outcome":     stringAttribute(attributes, "kansoku.outcome"),
		"value_state": stringAttribute(attributes, "kansoku.value_state"),
		"observed_at": time.Unix(0, int64(timestamp)).UTC().Format(time.RFC3339Nano),
	}
	for _, optional := range []struct{ attr, field string }{{"kansoku.model.id", "model"}, {"kansoku.tool.id", "tool_name"}} {
		if value := stringAttribute(attributes, optional.attr); value != "" {
			fields[optional.field] = value
		}
	}
	for _, required := range []string{"event_id", "session_id", "event_type", "outcome", "value_state"} {
		if fields[required] == "" {
			return nil, 0, errors.New("missing_safe_attribute")
		}
	}
	return fields, uint64(intAttribute(attributes, "kansoku.sequence")), nil
}

func stringAttribute(attributes []*commonv1.KeyValue, name string) string {
	for _, attribute := range attributes {
		if attribute.GetKey() == name {
			return attribute.GetValue().GetStringValue()
		}
	}
	return ""
}
func intAttribute(attributes []*commonv1.KeyValue, name string) int64 {
	for _, attribute := range attributes {
		if attribute.GetKey() == name {
			return attribute.GetValue().GetIntValue()
		}
	}
	return 0
}

func knownResource(resource *resourcev1.Resource) bool {
	service, version, schema := resourceIdentity(resource)
	return service == "fixture-agent" && version == "1.0.0" && schema == fixtureOTLPSchema
}

func (r *OTLPReceiver) unknown(message proto.Message, kind SourceKind, records int, service, version, schema string) error {
	fingerprint := r.ingestor.keyedIdentity("unknown-otlp-schema/1", string(kind)+"\x00"+string(message.ProtoReflect().Descriptor().FullName())+"\x00"+service+"\x00"+version+"\x00"+schema)
	if err := r.ingestor.IngestUnknown(kind, fingerprint, int64(proto.Size(message)), records); err != nil {
		return err
	}
	return errors.New("unknown_otlp_schema")
}

func (r *OTLPReceiver) ingestLogs(request *collectorlogsv1.ExportLogsServiceRequest, kind SourceKind) error {
	count := 0
	for _, resourceLogs := range request.GetResourceLogs() {
		for _, scope := range resourceLogs.GetScopeLogs() {
			count += len(scope.GetLogRecords())
		}
		if !knownResource(resourceLogs.GetResource()) {
			service, version, schema := resourceIdentity(resourceLogs.GetResource())
			return r.unknown(request, kind, count, service, version, schema)
		}
		for _, scope := range resourceLogs.GetScopeLogs() {
			for _, record := range scope.GetLogRecords() {
				fields, sequence, err := safeFields(record.GetAttributes(), logTimestamp(record))
				if err != nil {
					return err
				}
				if _, err := r.ingestor.IngestSafeFields(fields, kind, sequence); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func logTimestamp(record *logsv1.LogRecord) uint64 {
	if record.GetObservedTimeUnixNano() != 0 {
		return record.GetObservedTimeUnixNano()
	}
	return record.GetTimeUnixNano()
}

func (r *OTLPReceiver) ingestTraces(request *collectortracev1.ExportTraceServiceRequest, kind SourceKind) error {
	count := 0
	for _, resourceSpans := range request.GetResourceSpans() {
		for _, scope := range resourceSpans.GetScopeSpans() {
			count += len(scope.GetSpans())
		}
		if !knownResource(resourceSpans.GetResource()) {
			service, version, schema := resourceIdentity(resourceSpans.GetResource())
			return r.unknown(request, kind, count, service, version, schema)
		}
		for _, scope := range resourceSpans.GetScopeSpans() {
			for _, span := range scope.GetSpans() {
				fields, sequence, err := safeFields(span.GetAttributes(), span.GetStartTimeUnixNano())
				if err != nil {
					return err
				}
				if _, err := r.ingestor.IngestSafeFields(fields, kind, sequence); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (r *OTLPReceiver) ingestMetrics(request *collectormetricsv1.ExportMetricsServiceRequest, kind SourceKind) error {
	count := 0
	for _, resourceMetrics := range request.GetResourceMetrics() {
		if !knownResource(resourceMetrics.GetResource()) {
			service, version, schema := resourceIdentity(resourceMetrics.GetResource())
			return r.unknown(request, kind, count, service, version, schema)
		}
		for _, scope := range resourceMetrics.GetScopeMetrics() {
			for _, metric := range scope.GetMetrics() {
				points := numberPoints(metric)
				count += len(points)
				for _, point := range points {
					fields, sequence, err := safeFields(point.GetAttributes(), point.GetTimeUnixNano())
					if err != nil {
						return err
					}
					if _, err := r.ingestor.IngestSafeFields(fields, kind, sequence); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func numberPoints(metric *metricsv1.Metric) []*metricsv1.NumberDataPoint {
	if metric.GetGauge() != nil {
		return metric.GetGauge().GetDataPoints()
	}
	if metric.GetSum() != nil {
		return metric.GetSum().GetDataPoints()
	}
	return nil
}
