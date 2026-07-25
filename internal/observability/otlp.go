package observability

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
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
	"kansoku.local/kansoku/internal/claudeadapter"
	"kansoku.local/kansoku/internal/codexadapter"
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
		return status.Error(codes.ResourceExhausted, "backpressure_retryable")
	}
	if errors.Is(err, ErrDurabilityUnavailable) {
		return status.Error(codes.Unavailable, "durability_unavailable_retryable")
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
		"observed_at": time.Unix(0, int64(timestamp)).UTC().Format(time.RFC3339Nano),
	}
	// kansoku.outcome/kansoku.value_state are optional on the wire, exactly
	// as internal/privacy's sanitizer already treats them (optionalEnum
	// defaults an absent field to "unknown", never an error): the
	// fixture-agent lane always sends both, so this is behavior-preserving
	// for it, but several real, documented Codex/Claude OTel events (for
	// example session.started/prompt.submitted) have no natural outcome or
	// measurement to report at all, and their contract-declared safe
	// attribute fingerprint (codexadapter/claudeadapter's
	// ExpectedOTelAttributeFingerprint) does not include either key for
	// those events. Only setting the field when the attribute is actually
	// present keeps "unknown is not zero": an absent measurement becomes
	// the sanitizer's honest "unknown" classification, never a fabricated
	// value.
	for _, optional := range []struct{ attr, field string }{
		{"kansoku.outcome", "outcome"}, {"kansoku.value_state", "value_state"},
		{"kansoku.model.id", "model"}, {"kansoku.tool.id", "tool_name"},
	} {
		if value := stringAttribute(attributes, optional.attr); value != "" {
			fields[optional.field] = value
		}
	}
	for _, required := range []string{"event_id", "session_id", "event_type"} {
		if fields[required] == "" {
			return nil, 0, errors.New("missing_safe_attribute")
		}
	}
	return fields, uint64(intAttribute(attributes, "kansoku.sequence")), nil
}

// nativeAttributeSafeSlot resolves one real, native (non-kansoku.*) OTLP
// attribute key observed on a matched adapter's record onto the existing
// kansoku.*-shaped OTLPSafeAttributes() slot contracts/codex/hooks-and-
// otel.yaml / contracts/claude/hooks-and-otel.yaml document it maps onto,
// by delegating to that adapter's own already-implemented
// NativeOTLPAttributeSafeSlot/ComponentAttributeSafeSlot helpers -- this
// package never carries a second copy of that translation table. A key
// neither adapter recognizes resolves to ok=false and is simply dropped by
// the caller, exactly like today's fixture-only safeFields already drops
// every attribute outside OTLPSafeAttributes().
func nativeAttributeSafeSlot(kind otlpAdapterKind, key string) (string, bool) {
	switch kind {
	case adapterCodex:
		return codexadapter.NativeOTLPAttributeSafeSlot(codexadapter.NativeOTLPAttribute(key))
	case adapterClaude:
		if slot, ok := claudeadapter.NativeOTLPAttributeSafeSlot(claudeadapter.NativeOTLPAttribute(key)); ok {
			return slot, true
		}
		return claudeadapter.ComponentAttributeSafeSlot(claudeadapter.ClaudeComponentAttribute(key))
	default:
		return "", false
	}
}

// translateToSafeAttributes builds the kansoku.*-namespaced attribute list
// safeFields/canonicalEventTypeForAdapter actually operate on, for one
// observed record matched to a real (non-fixture) adapter. Design decision A
// / TDD section A step 4 requires extracting only the attributes the
// relevant contract declares safe: an attribute already spelled
// kansoku.*-shaped is passed through unchanged (this keeps the
// fixture-agent-shaped wire format usable in fixtures/tests without a second
// code path); a real, documented native attribute name
// (conversation.id/model/skill.name/plugin.name/agent.name/session.id/
// tool_name/tool_status) is translated onto its existing safe slot via
// nativeAttributeSafeSlot; any other attribute -- including every dropped
// surface each adapter's otel.go documents (log bodies, span names, tool
// payloads, prompt/response text) -- is silently excluded, never passed
// through raw. kansoku.event.id has no documented native-attribute
// equivalent on either adapter's wire shape (it is Kansoku's own opaque
// per-record identity, exactly as it already is for the fixture-agent
// lane), so when it is genuinely absent the caller derives a synthetic,
// stable identity from the record's own already-safe material instead of
// rejecting every real record outright; that derivation never fabricates a
// semantic value -- it is an opaque identity string exactly like the
// fixture lane's kansoku.event.id already is.
func translateToSafeAttributes(kind otlpAdapterKind, attributes []*commonv1.KeyValue) []*commonv1.KeyValue {
	if kind == adapterFixture || kind == adapterNone {
		return attributes
	}
	translated := make([]*commonv1.KeyValue, 0, len(attributes))
	for _, attribute := range attributes {
		key := attribute.GetKey()
		if strings.HasPrefix(key, "kansoku.") {
			translated = append(translated, attribute)
			continue
		}
		if slot, ok := nativeAttributeSafeSlot(kind, key); ok {
			translated = append(translated, &commonv1.KeyValue{Key: slot, Value: attribute.GetValue()})
		}
	}
	return translated
}

// syntheticEventID derives a stable, opaque per-record identity for a real
// adapter record whose wire shape carries no kansoku.event.id-equivalent
// native attribute (see translateToSafeAttributes). It is keyed off the
// record's own already-safe, already-present material -- the resolved
// adapter kind, the instrumentation scope name, the record timestamp and the
// sorted safe attribute key/value pairs actually observed -- never off any
// dropped or content-bearing surface. Two distinct real records never
// collide onto the same synthetic id unless every one of those inputs is
// identical, and the same record replayed twice (for example after a
// spool/retry) deterministically reproduces the same id, preserving the
// existing dedup/idempotency behavior IngestSafeFields already relies on.
func syntheticEventID(kind otlpAdapterKind, scopeName string, timestamp uint64, safeAttributes []*commonv1.KeyValue) string {
	pairs := make([]string, 0, len(safeAttributes))
	for _, attribute := range safeAttributes {
		pairs = append(pairs, attribute.GetKey()+"="+attribute.GetValue().GetStringValue())
	}
	sort.Strings(pairs)
	values := append([]string{strconv.Itoa(int(kind)), scopeName, strconv.FormatUint(timestamp, 10)}, pairs...)
	return "otel-synthetic-record/1:" + stableID("otel-synthetic-record/1", values...)
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

// otlpAdapterKind is the closed set of adapters otlp.go's resource dispatch
// recognizes, mirroring routes.go's hookAdapterHandler adapter switch
// (fixture-agent literal first, then each registered adapter's own
// resource matcher). adapterNone means the observed resource matched
// neither the fixture-agent identity nor any registered adapter's
// MatchesOTLPResource -- the caller must still fall through to the
// existing unknown()/IngestUnknown quarantine path unchanged.
type otlpAdapterKind int

const (
	adapterNone otlpAdapterKind = iota
	adapterFixture
	adapterCodex
	adapterClaude
)

// matchAdapterResource resolves an observed OTLP resource onto the closed
// otlpAdapterKind vocabulary: the Session 03 fixture-agent identity is
// checked first (unchanged behavior), then every registered real adapter's
// own MatchesOTLPResource, reusing each adapter's existing otel.go resource
// fingerprint rather than duplicating it here. A resource matching none of
// them resolves to adapterNone.
func matchAdapterResource(resource *resourcev1.Resource) otlpAdapterKind {
	if knownResource(resource) {
		return adapterFixture
	}
	service, _, _ := resourceIdentity(resource)
	switch {
	case codexadapter.MatchesOTLPResource(service):
		return adapterCodex
	case claudeadapter.MatchesOTLPResource(service):
		return adapterClaude
	default:
		return adapterNone
	}
}

// canonicalEventTypeForAdapter resolves one observed OTLP record's
// canonical event type for a real (non-fixture) adapter match, by calling
// that adapter's own already-implemented, already-unit-tested
// CanonicalEventForOTel with the record's instrumentation scope name as the
// documented OTel event name and the closed set of already-safe attribute
// keys actually present on the record as the schema-fingerprint input. The
// fixture-agent lane never calls this: its canonical event_type continues
// to come straight from the trusted kansoku.event.type attribute exactly as
// before.
func canonicalEventTypeForAdapter(kind otlpAdapterKind, scopeName string, attributes []*commonv1.KeyValue) (string, error) {
	presentKeys := presentSafeAttributeKeys(attributes)
	switch kind {
	case adapterCodex:
		shape := codexadapter.OTelAttributeShape{InstrumentationScope: scopeName, PresentAttributeKeys: presentKeys}
		return codexadapter.CanonicalEventForOTel(codexadapter.OTelEventName(scopeName), shape)
	case adapterClaude:
		shape := claudeadapter.OTelAttributeShape{InstrumentationScope: scopeName, PresentAttributeKeys: presentKeys}
		return claudeadapter.CanonicalEventForOTel(claudeadapter.OTelEventName(scopeName), shape)
	default:
		return "", errors.New("otlp_adapter_not_recognized")
	}
}

// presentSafeAttributeKeys returns the closed set of already-safe
// (kansoku.*-namespaced) attribute keys actually present on one observed
// OTLP record, in the exact shape both codexadapter.OTelAttributeShape and
// claudeadapter.OTelAttributeShape expect: only keys already on
// OTLPSafeAttributes() ever contribute, and only the key names -- never
// their values -- ever leave this function.
func presentSafeAttributeKeys(attributes []*commonv1.KeyValue) []string {
	keys := make([]string, 0, len(attributes))
	for _, attribute := range attributes {
		keys = append(keys, attribute.GetKey())
	}
	return keys
}

// ingestOneRecord routes one observed OTLP record (log, span or metric data
// point) for a resolved adapter match. The fixture-agent lane keeps its
// exact existing behavior: safeFields trusts the kansoku.event.type
// attribute directly. A real adapter match instead translates the record's
// real, native attribute names onto their kansoku.*-shaped safe slots first
// (translateToSafeAttributes -- Design decision A / TDD section A step 4),
// synthesizes the one required identity attribute that has no native
// wire equivalent when it is genuinely absent, derives the canonical event
// type from that adapter's own CanonicalEventForOTel, and only then reaches
// safeFields/IngestSafeFields's existing allowlist -- never a second,
// parallel allowlist and never a raw native attribute name passed through.
func (r *OTLPReceiver) ingestOneRecord(kind otlpAdapterKind, scopeName string, attributes []*commonv1.KeyValue, timestamp uint64, sourceKind SourceKind) error {
	safeAttributes := translateToSafeAttributes(kind, attributes)
	if kind != adapterFixture && kind != adapterNone {
		if stringAttribute(safeAttributes, "kansoku.event.id") == "" {
			safeAttributes = append(safeAttributes, &commonv1.KeyValue{
				Key:   "kansoku.event.id",
				Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: syntheticEventID(kind, scopeName, timestamp, safeAttributes)}},
			})
		}
		// A real Codex/Claude record declares its event type through its
		// OTel instrumentation scope/event name, never through a
		// kansoku.event.type-shaped attribute (that is a fixture-agent-only
		// wire convention). codexadapter/claudeadapter's schema-fingerprint
		// mechanism (ExpectedOTelAttributeFingerprint) nonetheless expects
		// kansoku.event.type's mere presence as one of the fingerprinted
		// keys for several documented events, exactly mirroring how the
		// fixture-agent lane always sends it. The marker's value is never
		// trusted -- canonicalEventTypeForAdapter below always overwrites
		// event_type with the value CanonicalEventForOTel actually derives
		// from the scope name and schema fingerprint, so a wrong or absent
		// marker value can never smuggle a fabricated event type through.
		if stringAttribute(safeAttributes, "kansoku.event.type") == "" {
			safeAttributes = append(safeAttributes, &commonv1.KeyValue{
				Key:   "kansoku.event.type",
				Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: scopeName}},
			})
		}
	}
	fields, sequence, err := safeFields(safeAttributes, timestamp)
	if err != nil {
		return err
	}
	if kind != adapterFixture {
		canonical, err := canonicalEventTypeForAdapter(kind, scopeName, safeAttributes)
		if err != nil {
			return err
		}
		fields["event_type"] = canonical
	}
	_, err = r.ingestor.IngestSafeFields(fields, adapterIdentity(kind), sourceKind, sequence)
	return err
}

// adapterIdentity maps a resolved otlpAdapterKind onto the stable adapter
// identity string IngestSafeFields keys its cross-lane pseudonyms on --
// FixtureAdapterID/codexadapter.AdapterID/claudeadapter.AdapterID, the same
// identity the fixture schema's DecodeAndExtract path already uses for the
// hook/transcript lanes, so the same logical event correlates into one Fact
// regardless of which lane observed it.
func adapterIdentity(kind otlpAdapterKind) string {
	switch kind {
	case adapterFixture:
		return FixtureAdapterID
	case adapterCodex:
		return codexadapter.AdapterID
	case adapterClaude:
		return claudeadapter.AdapterID
	default:
		return ""
	}
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
		adapterKind := matchAdapterResource(resourceLogs.GetResource())
		if adapterKind == adapterNone {
			service, version, schema := resourceIdentity(resourceLogs.GetResource())
			return r.unknown(request, kind, count, service, version, schema)
		}
		for _, scope := range resourceLogs.GetScopeLogs() {
			scopeName := scope.GetScope().GetName()
			for _, record := range scope.GetLogRecords() {
				if err := r.ingestOneRecord(adapterKind, scopeName, record.GetAttributes(), logTimestamp(record), kind); err != nil {
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
		adapterKind := matchAdapterResource(resourceSpans.GetResource())
		if adapterKind == adapterNone {
			service, version, schema := resourceIdentity(resourceSpans.GetResource())
			return r.unknown(request, kind, count, service, version, schema)
		}
		for _, scope := range resourceSpans.GetScopeSpans() {
			scopeName := scope.GetScope().GetName()
			for _, span := range scope.GetSpans() {
				if err := r.ingestOneRecord(adapterKind, scopeName, span.GetAttributes(), span.GetStartTimeUnixNano(), kind); err != nil {
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
		adapterKind := matchAdapterResource(resourceMetrics.GetResource())
		if adapterKind == adapterNone {
			service, version, schema := resourceIdentity(resourceMetrics.GetResource())
			return r.unknown(request, kind, count, service, version, schema)
		}
		for _, scope := range resourceMetrics.GetScopeMetrics() {
			scopeName := scope.GetScope().GetName()
			for _, metric := range scope.GetMetrics() {
				points := numberPoints(metric)
				count += len(points)
				for _, point := range points {
					if err := r.ingestOneRecord(adapterKind, scopeName, point.GetAttributes(), point.GetTimeUnixNano(), kind); err != nil {
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
