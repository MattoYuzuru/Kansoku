package integrity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/localhttp"
	"kansoku.local/kansoku/internal/observability"
)

// SyntheticPipelineCheckID is the check_id every SyntheticPipelineCheck
// outcome reports, matching audit-run-and-schedule.yaml's
// stage_5_synthetic_pipeline_probe stage and
// fault-injection-and-live-canary.yaml's
// "stage_5_synthetic_pipeline_probe sends a uniquely tagged safe record
// through the public /v1/hooks/{adapter}/{event} ingress" target detection
// claim.
const SyntheticPipelineCheckID = "stage_5_synthetic_pipeline_probe"

// syntheticProbeCapabilityID is the CheckTarget.CapabilityID/InstallationID
// this check reports under: the probe exercises the SHARED ingress pipeline
// itself (both the hook_http route and the OTLP log signal), not any one
// discovered installation, so it is filed under a single fixed identity
// rather than once per adapter/installation.
const (
	syntheticProbeCapabilityID   = string(adaptersdk.CapabilityIngestionLiveStream)
	syntheticProbeInstallationID = "kansoku-synthetic-probe"
)

// syntheticSessionNamespacePrefix and syntheticEventNamespacePrefix mark
// every session_id/event_id this check ever sends as belonging to the
// audit engine's own reserved test namespace, never a real user session.
// Combined with adapter_id="fixture-agent" (the same conformance-only
// identity Session 03 already never counts as a real Codex/Claude/etc.
// installation), a query that excludes rows whose Source.AdapterID equals
// observability.FixtureAdapterID or whose tag carries this prefix can never
// return one of this check's own probe records, matching the "tagged
// records are provably excluded from real reconciliation/usage aggregates"
// requirement.
const (
	syntheticSessionNamespacePrefix = "kansoku-synthetic-probe-session-"
	syntheticEventNamespacePrefix   = "kansoku-synthetic-probe-event-"
)

// SyntheticGuardConfig is the fixed, already-validated localhttp.Guard
// construction parameters a real caller already uses to build the
// production ingress guard (see internal/localhttp/security.go's NewGuard
// closed validation: exact canonical hosts/origins, >=32-byte bearer/csrf,
// maxBodyBytes==1MiB, requests==120, window==1 minute). SyntheticPipelineCheck
// never relaxes or reimplements this guard's checks: it authenticates its
// own synthetic request with the SAME bearer token production already uses.
type SyntheticGuardConfig struct {
	Hosts        []string
	Origins      []string
	Bearer       []byte
	CSRF         []byte
	MaxBodyBytes int64
	Requests     int
	Window       time.Duration
}

// SyntheticPipelineCheck implements stage_5_synthetic_pipeline_probe: it
// builds a uniquely-tagged, test-namespaced safe hook record and a
// structurally equivalent OTLP log record, sends BOTH through the real
// public ingress (observability.NewIngressHTTPHandler's
// "/v1/hooks/{adapter}/{event}" and "/v1/logs" routes -- the exact same
// http.Handler production traffic crosses, invoked in-process via
// net/http/httptest rather than a second parallel test-only ingress),
// verifies each record's durable Fact/Evidence appearance in the SAME
// FileStore snapshot every other stage reads, and then explicitly expires
// both records via FileStore.PurgeFacts before returning -- synthetic
// probe data never lingers in durable state as if it were real usage.
type SyntheticPipelineCheck struct {
	Guard           *localhttp.Guard
	Ingestor        *observability.Ingestor
	OTLPReceiver    *observability.OTLPReceiver
	Store           *observability.FileStore
	Postgres        *pgxpool.Pool
	Handoff         *dataplatform.ObservabilityHandoff
	RequirePostgres bool
	Bearer          []byte
	Now             func() time.Time
}

var _ Check = (*SyntheticPipelineCheck)(nil)

// NewSyntheticPipelineCheck constructs a SyntheticPipelineCheck from an
// already-wired production ingress (the same guard/ingestor/receiver/store
// quadruple a real caller already constructs once at process start). bearer
// must be the SAME token guard was built with, since the probe authenticates
// through the real Guard.Wrap boundary rather than bypassing it.
func NewSyntheticPipelineCheck(guard *localhttp.Guard, ingestor *observability.Ingestor, receiver *observability.OTLPReceiver, store *observability.FileStore, bearer []byte) *SyntheticPipelineCheck {
	return &SyntheticPipelineCheck{Guard: guard, Ingestor: ingestor, OTLPReceiver: receiver, Store: store, Bearer: append([]byte(nil), bearer...), Now: time.Now}
}

// NewPostgresSyntheticPipelineCheck verifies the production system of record
// directly. It intentionally has no FileStore dependency: production no
// longer mirrors fact/evidence payloads into local JSON.
func NewPostgresSyntheticPipelineCheck(
	guard *localhttp.Guard,
	ingestor *observability.Ingestor,
	receiver *observability.OTLPReceiver,
	pool *pgxpool.Pool,
	handoff *dataplatform.ObservabilityHandoff,
	bearer []byte,
) *SyntheticPipelineCheck {
	return &SyntheticPipelineCheck{
		Guard: guard, Ingestor: ingestor, OTLPReceiver: receiver,
		Postgres: pool, Handoff: handoff, RequirePostgres: true,
		Bearer: append([]byte(nil), bearer...), Now: time.Now,
	}
}

// NewProductionSyntheticPipelineCheck extends the shared public-ingress
// check through the Session 04 PostgreSQL system of record, rollup worker
// and budgeted query surface. Production assembly uses this constructor;
// the FileStore-only constructor remains useful for Session 03 conformance
// tests but cannot satisfy the production stage-5 gate.
func NewProductionSyntheticPipelineCheck(guard *localhttp.Guard, ingestor *observability.Ingestor, receiver *observability.OTLPReceiver, store *observability.FileStore, pool *pgxpool.Pool, bearer []byte) (*SyntheticPipelineCheck, error) {
	if guard == nil || ingestor == nil || receiver == nil || store == nil {
		return nil, fmt.Errorf("production synthetic pipeline dependencies are incomplete")
	}
	check := NewSyntheticPipelineCheck(guard, ingestor, receiver, store, bearer)
	handoff, err := dataplatform.NewObservabilityHandoff(pool, 15*time.Second)
	if err != nil {
		return nil, err
	}
	if err := ingestor.ConfigureDurableFactSink(handoff); err != nil {
		return nil, fmt.Errorf("configure production observability handoff: %w", err)
	}
	check.Postgres = pool
	check.Handoff = handoff
	check.RequirePostgres = true
	return check, nil
}

func (c *SyntheticPipelineCheck) StageID() StageID { return Stage5SyntheticPipelineProbe }
func (c *SyntheticPipelineCheck) CheckID() string  { return SyntheticPipelineCheckID }

func (c *SyntheticPipelineCheck) validateProductionReady(sharedPool *pgxpool.Pool) error {
	if c == nil || c.Guard == nil || c.Ingestor == nil || c.OTLPReceiver == nil {
		return fmt.Errorf("public ingress dependencies are incomplete")
	}
	if !c.RequirePostgres || c.Postgres == nil {
		return fmt.Errorf("PostgreSQL event/evidence/rollup path is required")
	}
	if c.Postgres != sharedPool {
		return fmt.Errorf("PostgreSQL path must reuse the assembly pool")
	}
	if c.Handoff == nil || c.Handoff.Pool() != sharedPool || !c.Ingestor.HasDurableFactSink() {
		return fmt.Errorf("public ingress PostgreSQL handoff is not production-wired")
	}
	if len(c.Bearer) < 32 || c.Now == nil {
		return fmt.Errorf("probe authentication or clock is incomplete")
	}
	return nil
}

// Targets always returns exactly one target: the shared ingress pipeline
// this check probes end-to-end is not scoped to any one adapter or
// installation.
func (c *SyntheticPipelineCheck) Targets(_ context.Context, _ CheckInput) ([]CheckTarget, error) {
	return []CheckTarget{{CapabilityID: syntheticProbeCapabilityID, InstallationID: syntheticProbeInstallationID}}, nil
}

// Evaluate runs one full synthetic-probe cycle: send hook, send OTLP log,
// verify both landed durably, purge both. Any failure to observe durable
// appearance within this one call (its own bounded synchronous verification
// window) is reported as CheckStatusFail with
// FailureClassSyntheticPipelineProbeFailed, matching
// fault-injection-and-live-canary.yaml's synthetic_pipeline_probe_failed
// target_detection_claim; a purge failure after a successful verification is
// reported as skipped_unsupported detail rather than silently masking the
// verification's own pass (verification is this check's primary claim,
// cleanup is a secondary durability hygiene step).
func (c *SyntheticPipelineCheck) Evaluate(ctx context.Context, in CheckInput, _ CheckTarget) (outcome CheckOutcome, err error) {
	now := c.Now()
	if !in.Now.IsZero() {
		now = in.Now
	}
	if c.Guard == nil || c.Ingestor == nil || c.OTLPReceiver == nil {
		return syntheticPipelineFailure(now, "synthetic_pipeline_probe_not_wired"), nil
	}
	if c.RequirePostgres && c.Postgres == nil {
		return syntheticPipelineFailure(now, "synthetic_postgres_path_not_wired"), nil
	}
	tag := in.AuditRunID
	if tag == "" {
		tag = fmt.Sprintf("%d", now.UnixNano())
	}
	sessionID := syntheticSessionNamespacePrefix + tag
	hookEventID := syntheticEventNamespacePrefix + tag + "-hook"
	otlpEventID := syntheticEventNamespacePrefix + tag + "-otlp"
	spanEventID := syntheticEventNamespacePrefix + tag + "-span"
	metricEventID := syntheticEventNamespacePrefix + tag + "-metric"

	handler, err := observability.NewIngressHTTPHandler(c.Guard, c.Ingestor, c.OTLPReceiver)
	if err != nil {
		return CheckOutcome{}, fmt.Errorf("build synthetic probe ingress handler: %w", err)
	}

	expectedNativeIDs := map[string]bool{
		c.Ingestor.SyntheticProbeIdentity("source-record/1", observability.FixtureAdapterID+"\x00"+hookEventID):   true,
		c.Ingestor.SyntheticProbeIdentity("source-record/1", observability.FixtureAdapterID+"\x00"+otlpEventID):   true,
		c.Ingestor.SyntheticProbeIdentity("source-record/1", observability.FixtureAdapterID+"\x00"+spanEventID):   true,
		c.Ingestor.SyntheticProbeIdentity("source-record/1", observability.FixtureAdapterID+"\x00"+metricEventID): true,
	}
	defer func() {
		var postgresErr error
		var scopeErr error
		if c.Postgres != nil {
			var scope syntheticPostgresScope
			var buildErr error
			if c.Store != nil {
				currentFacts, _ := exactSyntheticFacts(c.Store.Snapshot(), expectedNativeIDs)
				scope, buildErr = syntheticPostgresScopeFromFacts(currentFacts)
			} else {
				scope, buildErr = c.syntheticPostgresScopeFromNativeIDs(context.Background(), expectedNativeIDs)
			}
			if buildErr != nil {
				scopeErr = buildErr
			} else {
				postgresErr = c.cleanupPostgresProbe(context.Background(), scope)
			}
		}
		var fileErr error
		if c.Store != nil {
			fileErr = c.cleanupFileFacts(expectedNativeIDs)
		}
		if fileErr != nil || postgresErr != nil || scopeErr != nil {
			outcome = syntheticPipelineFailure(now, "synthetic_probe_cleanup_failed")
			err = nil
		}
	}()

	hookStatus, _, hookErr := c.sendHook(handler, sessionID, hookEventID, now)
	if hookErr != nil || hookStatus != http.StatusOK {
		return syntheticPipelineFailure(now, fmt.Sprintf("hook_probe_rejected status=%d transport_error=%t", hookStatus, hookErr != nil)), nil
	}
	otlpStatus, otlpErr := c.sendOTLPLog(handler, sessionID, otlpEventID, now)
	if otlpErr != nil || otlpStatus != http.StatusOK {
		return syntheticPipelineFailure(now, fmt.Sprintf("otlp_probe_rejected status=%d transport_error=%t", otlpStatus, otlpErr != nil)), nil
	}
	spanStatus, spanErr := c.sendOTLPSpan(handler, sessionID, spanEventID, now)
	if spanErr != nil || spanStatus != http.StatusOK {
		return syntheticPipelineFailure(now, fmt.Sprintf("otlp_span_probe_rejected status=%d transport_error=%t", spanStatus, spanErr != nil)), nil
	}
	metricStatus, metricErr := c.sendOTLPMetric(handler, sessionID, metricEventID, now)
	if metricErr != nil || metricStatus != http.StatusOK {
		return syntheticPipelineFailure(now, fmt.Sprintf("otlp_metric_probe_rejected status=%d transport_error=%t", metricStatus, metricErr != nil)), nil
	}

	var scope syntheticPostgresScope
	if c.Store != nil {
		after := c.Store.Snapshot()
		facts, factKeys := exactSyntheticFacts(after, expectedNativeIDs)
		if len(facts) != 4 || len(factKeys) != 4 {
			return syntheticPipelineFailure(now, fmt.Sprintf("expected_4_exact_synthetic_facts_observed=%d", len(facts))), nil
		}
		for _, fact := range facts {
			if len(fact.EvidenceIDs) == 0 {
				return syntheticPipelineFailure(now, "synthetic_fact_missing_evidence"), nil
			}
			for _, evidenceID := range fact.EvidenceIDs {
				if _, ok := after.Evidence[evidenceID]; !ok {
					return syntheticPipelineFailure(now, "synthetic_evidence_not_durable"), nil
				}
			}
		}
		scope, err = syntheticPostgresScopeFromFacts(facts)
	} else {
		scope, err = c.syntheticPostgresScopeFromNativeIDs(ctx, expectedNativeIDs)
	}
	if err != nil {
		return syntheticPipelineFailure(now, "postgres_scope_verification_failed"), nil
	}

	detail := "verified_facts=4"
	if c.Postgres != nil {
		verifyErr := c.verifyPostgresPath(ctx, scope, now)
		if verifyErr != nil {
			return syntheticPipelineFailure(now, "postgres_event_evidence_rollup_query_verification_failed"), nil
		}
		detail += " postgres_events=4 postgres_evidence=4 rollup_query=verified"
	} else {
		detail += " postgres_path=not_requested"
	}
	return CheckOutcome{
		CheckID: SyntheticPipelineCheckID, Status: CheckStatusPass,
		Category: "", DetailRef: detail, ObservedAt: now,
	}, nil
}

func syntheticPipelineFailure(now time.Time, detail string) CheckOutcome {
	return CheckOutcome{
		CheckID: SyntheticPipelineCheckID, Status: CheckStatusFail,
		Category:  string(FailureClassSyntheticPipelineProbeFailed),
		DetailRef: detail, ObservedAt: now,
	}
}

// syntheticProbeToolName is the ONLY tool_name value
// privacy.FixtureSourceSchema's closed Tools allowlist accepts
// ("inventory/tool-safe" -- the same literal internal/observability's own
// tests already hardcode as testRaw's tool_name). The synthetic probe cannot
// invent a new tool identity: SafeRecord extraction rejects any tool_name
// outside this closed catalog as unknown_catalog_value, so the probe reuses
// this exact fixture-catalog value rather than a self-describing name; the
// uniqueness/tagging this check needs for identification instead lives
// entirely in event_id/session_id's reserved test-namespace prefixes.
const syntheticProbeToolName = "inventory/tool-safe"

// sendHook builds a uniquely-tagged, already-allowlisted fixture-agent
// tool_finished hook payload (the same closed field shape testRaw builds in
// internal/observability's own tests) and posts it through the real
// "/v1/hooks/fixture-agent/tool_finished" route.
func (c *SyntheticPipelineCheck) sendHook(handler http.Handler, sessionID, eventID string, now time.Time) (int, string, error) {
	payload := map[string]any{
		"event_id":    eventID,
		"session_id":  sessionID,
		"observed_at": now.UTC().Format(time.RFC3339Nano),
		"event_type":  "tool_finished",
		"outcome":     "succeeded",
		"value_state": "numeric_zero",
		"tool_name":   syntheticProbeToolName,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, "", err
	}
	url := fmt.Sprintf("http://127.0.0.1:4318/v1/hooks/%s/tool_finished", observability.FixtureAdapterID)
	request := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	request.Host, request.RemoteAddr = "127.0.0.1:4318", "127.0.0.1:52099"
	request.Header.Set("Authorization", "Bearer "+string(c.Bearer))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String(), nil
}

// sendOTLPLog builds a uniquely-tagged OTLP ExportLogsServiceRequest whose
// resource identity matches observability's own knownResource acceptance
// (FixtureAdapterID/FixtureAdapterVersion/FixtureOTLPSchemaID, the exact
// literals contracts/observability/ingress.yaml already declares) and posts
// it through the real "/v1/logs" route as protobuf, matching the exact
// content-type/transport a real OTLP exporter uses.
func (c *SyntheticPipelineCheck) sendOTLPLog(handler http.Handler, sessionID, eventID string, now time.Time) (int, error) {
	resource := syntheticOTLPResource()
	attributes := syntheticOTLPAttributes(sessionID, eventID)
	record := &logsv1.LogRecord{ObservedTimeUnixNano: uint64(now.UTC().UnixNano()), Attributes: attributes}
	request := &collectorlogsv1.ExportLogsServiceRequest{ResourceLogs: []*logsv1.ResourceLogs{{
		Resource: resource, ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{record}}},
	}}}
	encoded, err := proto.Marshal(request)
	if err != nil {
		return 0, err
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4318/v1/logs", bytes.NewReader(encoded))
	httpRequest.Host, httpRequest.RemoteAddr = "127.0.0.1:4318", "127.0.0.1:52098"
	httpRequest.Header.Set("Authorization", "Bearer "+string(c.Bearer))
	httpRequest.Header.Set("Content-Type", "application/x-protobuf")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpRequest)
	return recorder.Code, nil
}

func (c *SyntheticPipelineCheck) sendOTLPSpan(handler http.Handler, sessionID, eventID string, now time.Time) (int, error) {
	request := &collectortracev1.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{{
		Resource: syntheticOTLPResource(),
		ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{{
			StartTimeUnixNano: uint64(now.UTC().UnixNano()),
			Attributes:        syntheticOTLPAttributes(sessionID, eventID),
		}}}},
	}}}
	return sendSyntheticProto(handler, "/v1/traces", request, c.Bearer)
}

func (c *SyntheticPipelineCheck) sendOTLPMetric(handler http.Handler, sessionID, eventID string, now time.Time) (int, error) {
	request := &collectormetricsv1.ExportMetricsServiceRequest{ResourceMetrics: []*metricsv1.ResourceMetrics{{
		Resource: syntheticOTLPResource(),
		ScopeMetrics: []*metricsv1.ScopeMetrics{{Metrics: []*metricsv1.Metric{{
			Name: "kansoku.synthetic.probe",
			Data: &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{DataPoints: []*metricsv1.NumberDataPoint{{
				TimeUnixNano: uint64(now.UTC().UnixNano()),
				Attributes:   syntheticOTLPAttributes(sessionID, eventID),
				Value:        &metricsv1.NumberDataPoint_AsInt{AsInt: 0},
			}}}},
		}}}},
	}}}
	return sendSyntheticProto(handler, "/v1/metrics", request, c.Bearer)
}

func syntheticOTLPResource() *resourcev1.Resource {
	return &resourcev1.Resource{Attributes: []*commonv1.KeyValue{
		stringAttr("service.name", observability.FixtureAdapterID),
		stringAttr("kansoku.adapter.version", observability.FixtureAdapterVersion),
		stringAttr("kansoku.source.schema", observability.FixtureOTLPSchemaID),
	}}
}

func syntheticOTLPAttributes(sessionID, eventID string) []*commonv1.KeyValue {
	return []*commonv1.KeyValue{
		stringAttr("kansoku.event.id", eventID),
		stringAttr("kansoku.session.id", sessionID),
		stringAttr("kansoku.event.type", "tool_finished"),
		stringAttr("kansoku.outcome", "succeeded"),
		stringAttr("kansoku.value_state", "numeric_zero"),
		stringAttr("kansoku.tool.id", syntheticProbeToolName),
	}
}

func sendSyntheticProto(handler http.Handler, path string, message proto.Message, bearer []byte) (int, error) {
	encoded, err := proto.Marshal(message)
	if err != nil {
		return 0, err
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4318"+path, bytes.NewReader(encoded))
	request.Host, request.RemoteAddr = "127.0.0.1:4318", "127.0.0.1:52097"
	request.Header.Set("Authorization", "Bearer "+string(bearer))
	request.Header.Set("Content-Type", "application/x-protobuf")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, nil
}

func stringAttr(key, value string) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}}}
}

type syntheticPostgresScope struct {
	EventIDs            []string
	DimensionScope      string
	DeviceID            string
	AgentInstallationID string
	SurfaceID           string
	ProjectID           string
	SessionID           string
	TurnIDs             []string
	SourceInstanceIDs   []string
	ComponentID         string
	AdapterVersionID    string
}

func (c *SyntheticPipelineCheck) syntheticPostgresScopeFromNativeIDs(
	ctx context.Context,
	nativeIDs map[string]bool,
) (syntheticPostgresScope, error) {
	if c.Postgres == nil || len(nativeIDs) != 4 {
		return syntheticPostgresScope{}, fmt.Errorf("synthetic PostgreSQL scope is incomplete")
	}
	values := make([]string, 0, len(nativeIDs))
	for value := range nativeIDs {
		values = append(values, value)
	}
	sort.Strings(values)
	rows, err := c.Postgres.Query(ctx, `
		SELECT e.event_id, e.agent_installation_id, e.surface_id, e.project_id,
		       e.session_id, e.turn_id, e.component_id, e.source_instance_id,
		       ai.device_id, si.adapter_version_id, e.event_type
		FROM events e
		JOIN agent_installations ai
		  ON ai.agent_installation_id = e.agent_installation_id
		JOIN source_instances si
		  ON si.source_instance_id = e.source_instance_id
		WHERE e.source_native_event_id = ANY($1)
		ORDER BY e.event_id
	`, values)
	if err != nil {
		return syntheticPostgresScope{}, err
	}
	defer rows.Close()
	var result syntheticPostgresScope
	turnIDs := map[string]bool{}
	sourceIDs := map[string]bool{}
	for rows.Next() {
		var eventID, installationID, surfaceID, projectID, sessionID string
		var turnID, componentID, sourceID, deviceID, adapterVersionID, eventType string
		if err := rows.Scan(
			&eventID, &installationID, &surfaceID, &projectID, &sessionID,
			&turnID, &componentID, &sourceID, &deviceID, &adapterVersionID, &eventType,
		); err != nil {
			return syntheticPostgresScope{}, err
		}
		dimensionScope := installationID + "|" + surfaceID + "|" + componentID + "|" + eventType
		if len(result.EventIDs) == 0 {
			result.AgentInstallationID = installationID
			result.SurfaceID = surfaceID
			result.ProjectID = projectID
			result.SessionID = sessionID
			result.ComponentID = componentID
			result.DeviceID = deviceID
			result.AdapterVersionID = adapterVersionID
			result.DimensionScope = dimensionScope
		} else if result.AgentInstallationID != installationID ||
			result.SurfaceID != surfaceID || result.ProjectID != projectID ||
			result.SessionID != sessionID || result.ComponentID != componentID ||
			result.DeviceID != deviceID || result.AdapterVersionID != adapterVersionID ||
			result.DimensionScope != dimensionScope {
			return syntheticPostgresScope{}, fmt.Errorf("synthetic PostgreSQL rows did not share one dimension scope")
		}
		result.EventIDs = append(result.EventIDs, eventID)
		turnIDs[turnID] = true
		sourceIDs[sourceID] = true
	}
	if err := rows.Err(); err != nil {
		return syntheticPostgresScope{}, err
	}
	if len(result.EventIDs) != 4 {
		return syntheticPostgresScope{}, fmt.Errorf("expected four synthetic PostgreSQL events, got %d", len(result.EventIDs))
	}
	for value := range turnIDs {
		result.TurnIDs = append(result.TurnIDs, value)
	}
	for value := range sourceIDs {
		result.SourceInstanceIDs = append(result.SourceInstanceIDs, value)
	}
	sort.Strings(result.TurnIDs)
	sort.Strings(result.SourceInstanceIDs)
	return result, nil
}

func syntheticPostgresScopeFromFacts(facts []observability.Fact) (syntheticPostgresScope, error) {
	var result syntheticPostgresScope
	turnIDs := make(map[string]bool)
	sourceIDs := make(map[string]bool)
	for index, fact := range facts {
		event := fact.Event
		scope := dataplatform.ObservabilityScope(event)
		if index == 0 {
			result.DeviceID = scope.DeviceID
			result.AgentInstallationID = scope.AgentInstallationID
			result.SurfaceID = scope.SurfaceID
			result.ProjectID = scope.ProjectID
			result.SessionID = scope.SessionID
			result.ComponentID = scope.ComponentID
			result.AdapterVersionID = scope.AdapterVersionID
			result.DimensionScope = scope.DimensionScope
		} else if result.DeviceID != scope.DeviceID ||
			result.AgentInstallationID != scope.AgentInstallationID ||
			result.SurfaceID != scope.SurfaceID ||
			result.ProjectID != scope.ProjectID ||
			result.SessionID != scope.SessionID ||
			result.ComponentID != scope.ComponentID ||
			result.AdapterVersionID != scope.AdapterVersionID ||
			result.DimensionScope != scope.DimensionScope {
			return syntheticPostgresScope{}, fmt.Errorf("synthetic facts did not share one bounded dimension scope")
		}
		result.EventIDs = append(result.EventIDs, event.EventID)
		turnIDs[scope.TurnID] = true
		sourceIDs[scope.SourceInstanceID] = true
	}
	for id := range turnIDs {
		result.TurnIDs = append(result.TurnIDs, id)
	}
	for id := range sourceIDs {
		result.SourceInstanceIDs = append(result.SourceInstanceIDs, id)
	}
	sort.Strings(result.EventIDs)
	sort.Strings(result.TurnIDs)
	sort.Strings(result.SourceInstanceIDs)
	return result, nil
}

func exactSyntheticFacts(state observability.DurableState, nativeIDs map[string]bool) ([]observability.Fact, []string) {
	var facts []observability.Fact
	var keys []string
	for key, fact := range state.Facts {
		if fact.Event.Source.AdapterID != observability.FixtureAdapterID ||
			!nativeIDs[fact.Event.Source.NativeEventID] {
			continue
		}
		facts = append(facts, fact)
		keys = append(keys, key)
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].Event.EventID < facts[j].Event.EventID })
	sort.Strings(keys)
	return facts, keys
}

func (c *SyntheticPipelineCheck) cleanupFileFacts(nativeIDs map[string]bool) error {
	if c.Store == nil {
		return nil
	}
	_, keys := exactSyntheticFacts(c.Store.Snapshot(), nativeIDs)
	if len(keys) == 0 {
		return nil
	}
	removed, err := c.Store.PurgeFacts(keys)
	if err != nil {
		return err
	}
	if removed != len(keys) {
		return fmt.Errorf("exact synthetic FileStore cleanup removed %d of %d facts", removed, len(keys))
	}
	_, remaining := exactSyntheticFacts(c.Store.Snapshot(), nativeIDs)
	if len(remaining) != 0 {
		return fmt.Errorf("exact synthetic FileStore cleanup left %d facts", len(remaining))
	}
	return nil
}

func (c *SyntheticPipelineCheck) verifyPostgresPath(
	ctx context.Context,
	scope syntheticPostgresScope,
	now time.Time,
) error {
	if c.Postgres == nil {
		return fmt.Errorf("postgres pool is not configured")
	}
	if len(scope.EventIDs) != 4 {
		return fmt.Errorf("postgres path expected four events")
	}
	var eventCount, evidenceCount int
	if err := c.Postgres.QueryRow(ctx, `SELECT count(*) FROM events WHERE event_id = ANY($1)`, scope.EventIDs).Scan(&eventCount); err != nil {
		return err
	}
	if err := c.Postgres.QueryRow(ctx, `SELECT count(*) FROM event_evidence WHERE event_id = ANY($1)`, scope.EventIDs).Scan(&evidenceCount); err != nil {
		return err
	}
	if eventCount != 4 || evidenceCount != 4 {
		return fmt.Errorf("postgres durable counts events=%d evidence=%d", eventCount, evidenceCount)
	}
	rows, err := c.Postgres.Query(ctx, `
		SELECT metric_family, granularity, bucket_start, dimension_scope
		FROM rollup_repair_queue
		WHERE dimension_scope = $1
		ORDER BY granularity, bucket_start
	`, scope.DimensionScope)
	if err != nil {
		return err
	}
	var repairs []dataplatform.RepairWorkItem
	for rows.Next() {
		var item dataplatform.RepairWorkItem
		var granularity string
		if err := rows.Scan(&item.MetricFamily, &granularity, &item.BucketStart, &item.DimensionScope); err != nil {
			rows.Close()
			return err
		}
		item.Granularity = dataplatform.Granularity(granularity)
		repairs = append(repairs, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(repairs) != 2 {
		return fmt.Errorf("expected exact hourly/daily repair pair, got %d", len(repairs))
	}
	for _, item := range repairs {
		if err := dataplatform.RecomputeBucket(ctx, c.Postgres, item); err != nil {
			return err
		}
	}
	if _, err := c.Postgres.Exec(ctx, `DELETE FROM rollup_repair_queue WHERE dimension_scope = $1`, scope.DimensionScope); err != nil {
		return err
	}
	from := dataplatform.BucketStart(now, dataplatform.GranularityHourly)
	response, err := dataplatform.RollupRange(
		ctx, c.Postgres, "hourly_rollup_range_30d", dataplatform.MetricFamilyLatencyMS,
		dataplatform.GranularityHourly, scope.DimensionScope, from, from.Add(time.Hour),
	)
	if err != nil {
		return err
	}
	if len(response.Data) != 1 || response.Population.Numerator != 4 ||
		response.Population.Denominator != 4 || response.Freshness.LateEventsPending != 0 {
		return fmt.Errorf("rollup query did not return exact synthetic population")
	}
	return nil
}

func (c *SyntheticPipelineCheck) cleanupPostgresProbe(ctx context.Context, scope syntheticPostgresScope) error {
	if c.Postgres == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return pgx.BeginFunc(cleanupCtx, c.Postgres, func(tx pgx.Tx) error {
		queries := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM event_evidence WHERE event_id = ANY($1)`, []any{scope.EventIDs}},
			{`DELETE FROM events WHERE event_id = ANY($1)`, []any{scope.EventIDs}},
			{`DELETE FROM rollup_repair_queue WHERE dimension_scope = $1`, []any{scope.DimensionScope}},
			{`DELETE FROM metric_rollups_hourly WHERE dimension_scope = $1`, []any{scope.DimensionScope}},
			{`DELETE FROM metric_rollups_daily WHERE dimension_scope = $1`, []any{scope.DimensionScope}},
			{`DELETE FROM rollup_status WHERE dimension_scope = $1`, []any{scope.DimensionScope}},
			{`DELETE FROM turns WHERE turn_id = ANY($1)`, []any{scope.TurnIDs}},
			{`DELETE FROM sessions WHERE session_id = $1`, []any{scope.SessionID}},
			{`DELETE FROM projects WHERE project_id = $1`, []any{scope.ProjectID}},
			{`DELETE FROM source_instances WHERE source_instance_id = ANY($1)`, []any{scope.SourceInstanceIDs}},
			{`DELETE FROM adapter_versions WHERE adapter_version_id = $1`, []any{scope.AdapterVersionID}},
			{`DELETE FROM components WHERE component_id = $1`, []any{scope.ComponentID}},
			{`DELETE FROM agent_surfaces WHERE surface_id = $1`, []any{scope.SurfaceID}},
			{`DELETE FROM agent_installations WHERE agent_installation_id = $1`, []any{scope.AgentInstallationID}},
			{`DELETE FROM devices WHERE device_id = $1`, []any{scope.DeviceID}},
		}
		for _, query := range queries {
			if _, err := tx.Exec(cleanupCtx, query.sql, query.args...); err != nil {
				return err
			}
		}
		var remaining int
		if err := tx.QueryRow(cleanupCtx, `
			SELECT
				(SELECT count(*) FROM events WHERE event_id = ANY($1)) +
				(SELECT count(*) FROM event_evidence WHERE event_id = ANY($1)) +
				(SELECT count(*) FROM rollup_repair_queue WHERE dimension_scope = $2) +
				(SELECT count(*) FROM metric_rollups_hourly WHERE dimension_scope = $2) +
				(SELECT count(*) FROM metric_rollups_daily WHERE dimension_scope = $2) +
				(SELECT count(*) FROM rollup_status WHERE dimension_scope = $2) +
				(SELECT count(*) FROM source_instances WHERE source_instance_id = ANY($3)) +
				(SELECT count(*) FROM adapter_versions WHERE adapter_version_id = $4) +
				(SELECT count(*) FROM components WHERE component_id = $5) +
				(SELECT count(*) FROM turns WHERE turn_id = ANY($6)) +
				(SELECT count(*) FROM sessions WHERE session_id = $7) +
				(SELECT count(*) FROM projects WHERE project_id = $8) +
				(SELECT count(*) FROM agent_surfaces WHERE surface_id = $9) +
				(SELECT count(*) FROM agent_installations WHERE agent_installation_id = $10) +
				(SELECT count(*) FROM devices WHERE device_id = $11)
		`, scope.EventIDs, scope.DimensionScope, scope.SourceInstanceIDs,
			scope.AdapterVersionID, scope.ComponentID, scope.TurnIDs,
			scope.SessionID, scope.ProjectID, scope.SurfaceID,
			scope.AgentInstallationID, scope.DeviceID).Scan(&remaining); err != nil {
			return err
		}
		if remaining != 0 {
			return fmt.Errorf("postgres synthetic cleanup left %d rows", remaining)
		}
		return nil
	})
}

// newSyntheticFactKeys returns every FactKey present in after but absent
// from before, restricted to facts whose Source.AdapterID is the fixture
// conformance identity (observability.FixtureAdapterID) this check itself
// used -- so a concurrent unrelated write during the same window is never
// mistaken for this probe's own record. Because stage_5 runs under the same
// single-writer advisory lock every audit stage runs under, "new fixture
// fact since before" is exactly this probe's own two records in practice;
// the AdapterID restriction is an explicit belt-and-braces check, not the
// only safeguard.
func newSyntheticFactKeys(before, after observability.DurableState) []string {
	var keys []string
	for key, fact := range after.Facts {
		if _, existed := before.Facts[key]; existed {
			continue
		}
		if fact.Event.Source.AdapterID != observability.FixtureAdapterID {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

// ExcludeTestNamespace filters a slice of already-loaded facts (e.g. one
// FileStore.Snapshot().Facts value range) down to only those NOT belonging
// to the synthetic-probe test namespace, i.e. Source.AdapterID is not
// observability.FixtureAdapterID. Real reconciliation/usage-aggregate
// queries in later stages are expected to apply an equivalent filter (or an
// adapter-id allowlist that simply never includes FixtureAdapterID in the
// first place); this helper exists so a unit test can assert the exclusion
// property directly against this package's own probe output without
// depending on a later stage's not-yet-built query engine.
func ExcludeTestNamespace(facts map[string]observability.Fact) map[string]observability.Fact {
	filtered := make(map[string]observability.Fact, len(facts))
	for key, fact := range facts {
		if fact.Event.Source.AdapterID == observability.FixtureAdapterID {
			continue
		}
		filtered[key] = fact
	}
	return filtered
}
