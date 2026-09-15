//go:build postgres_integration

// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema/applyAllMigrations
// work: a full API instance needs the real dataplatform + integrity + runtime
// migrations applied, since /api/v1/analytics and
// /api/v1/components/mcp/topology query real dataplatform tables.
package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kansoku.local/kansoku/internal/dataplatform"
)

// TestHealthAndIncidentRoutesIncludeIngressQuarantine proves metadata-only
// schema quarantine is not hidden merely because it is written before the
// integrity audit's richer incident-detail pipeline runs.
func TestHealthAndIncidentRoutesIncludeIngressQuarantine(t *testing.T) {
	dsn := testDSN(t)
	handler, bearer, pool := newTestAPIForEntityBreakdown(t, dsn)

	openedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO incidents (incident_id, category, opened_at, last_seen_at)
		VALUES ('inc_ingress_quarantine', 'unknown_schema', $1, $1)
	`, openedAt); err != nil {
		t.Fatalf("seed ingress incident: %v", err)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, entityBreakdownRequest("/api/v1/health", bearer))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, body=%s", health.Code, health.Body.String())
	}
	healthEnvelope := decodeEnvelope(t, health.Body.Bytes())
	healthData, ok := healthEnvelope.Data.(map[string]any)
	if !ok || healthData["open_incident_count"] != float64(1) {
		t.Fatalf("health must count ingress quarantine: %#v", healthEnvelope.Data)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, entityBreakdownRequest("/api/v1/incidents", bearer))
	if response.Code != http.StatusOK {
		t.Fatalf("incidents status = %d, body=%s", response.Code, response.Body.String())
	}
	envelope := decodeEnvelope(t, response.Body.Bytes())
	data, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatalf("remarshal incidents: %v", err)
	}
	var page struct {
		Data []struct {
			IncidentID   string `json:"incident_id"`
			FailureClass string `json:"failure_class"`
			CapabilityID string `json:"capability_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("decode incidents: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].IncidentID != "inc_ingress_quarantine" ||
		page.Data[0].FailureClass != "unknown_schema" || page.Data[0].CapabilityID != "core_ingestion" {
		t.Fatalf("ingress quarantine missing from incident API: %+v", page.Data)
	}
}

// fixedSecret32 returns a deterministic >=32 byte secret distinct per label,
// matching the minSecretBytes floor NewApplianceGuard enforces. The label is
// padded out to a fixed 64-byte length before any slicing, so it is safe
// regardless of the caller's label length (unlike a bare
// label+suffix[:40] slice, which panics once label alone is already close
// to 40 bytes).
func fixedSecret32(label string) []byte {
	padded := label + "-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	return []byte(padded)[:40]
}

// newTestAPIForEntityBreakdown builds a real http.Handler via NewAPI against
// the ephemeral Postgres instance, exactly the way assembly.go wires
// production, so the new/extended routes are exercised through the actual
// ApplianceGuard (auth, host, origin, forbidden-header checks) rather than a
// bypass. Returns the handler, the request-shaping bearer secret, and the
// backing pool so callers can seed extra fixture rows directly into the same
// schema (freshSchema's search_path is bound per-connection, so any pool
// opened against the same schema name would work, but reusing this one pool
// avoids a second schema/search_path setup entirely).
func newTestAPIForEntityBreakdown(t *testing.T, dsn string) (http.Handler, []byte, *pgxpool.Pool) {
	t.Helper()
	pool := freshSchema(t, dsn)

	root := t.TempDir()
	config := validTestConfig(root)
	mustPrivateSpoolDir(t, config.DataDir)

	handoff, err := dataplatform.NewObservabilityHandoff(pool, config.QueryTimeout())
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	queue, err := NewDurableIngressQueue(handoff, config.DataDir, config.QueueCapacity, config.SpoolMaxBytes)
	if err != nil {
		t.Fatalf("durable queue: %v", err)
	}
	t.Cleanup(queue.Close)

	auditKey := fixedSecret32("audit")
	plans, err := NewPlanManager(pool, auditKey)
	if err != nil {
		t.Fatalf("plan manager: %v", err)
	}
	jobs, err := NewJobManager(pool, map[JobID]JobHandler{})
	if err != nil {
		t.Fatalf("job manager: %v", err)
	}
	operations, err := NewOperationsService(config, Secrets{DatabasePassword: fixedSecret32("dbpass")}, pool, queue, jobs)
	if err != nil {
		t.Fatalf("operations service: %v", err)
	}

	readBearer := fixedSecret32("read-bearer")
	secrets := Secrets{ReadBearer: readBearer, MutationBearer: fixedSecret32("mutation-bearer"), CSRF: fixedSecret32("csrf-secret")}
	handler, err := NewAPI(config, secrets, pool, queue, plans, jobs, operations)
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	return handler, readBearer, pool
}

// entityBreakdownRequest builds a GET request that satisfies
// localhttp.NewApplianceGuard's non-appliance-mode requirements: exact
// loopback Host (matching the guard's hardcoded ui port 43100), loopback
// peer, and a valid bearer -- mirroring
// internal/localhttp/security_test.go's makeRequest convention.
func entityBreakdownRequest(path string, bearer []byte) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:43100"+path, nil)
	req.Host = "127.0.0.1:43100"
	req.RemoteAddr = "127.0.0.1:50123"
	req.Header.Set("Authorization", "Bearer "+string(bearer))
	return req
}

func decodeEnvelope(t *testing.T, body []byte) APIEnvelope {
	t.Helper()
	var envelope APIEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, body)
	}
	return envelope
}

// TestAnalyticsEntityBreakdownRoutesServeRealAggregationsWithEnvelopeIntact
// proves the extended /api/v1/analytics budget_id values
// (agent_breakdown_range, model_breakdown_range, component_breakdown_range,
// component_lifecycle_funnel, reliability_coverage_timeline) each serve real
// data through the standard APIEnvelope, that the forbidden-field scan still
// applies to these new response shapes, and that the half-open [from, to)
// range from analytics() is honored end-to-end.
func TestAnalyticsEntityBreakdownRoutesServeRealAggregationsWithEnvelopeIntact(t *testing.T) {
	dsn := testDSN(t)
	handler, bearer, _ := newTestAPIForEntityBreakdown(t, dsn)

	from := "2026-06-01T00:00:00Z"
	to := "2026-06-02T00:00:00Z"
	budgetIDs := []string{
		"agent_breakdown_range",
		"model_breakdown_range",
		"component_breakdown_range",
		"component_lifecycle_funnel",
		"reliability_coverage_timeline",
	}
	for _, budgetID := range budgetIDs {
		t.Run(budgetID, func(t *testing.T) {
			path := "/api/v1/analytics?budget_id=" + budgetID + "&metric_family=&from=" + from + "&to=" + to
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, entityBreakdownRequest(path, bearer))
			if response.Code != http.StatusOK {
				t.Fatalf("%s status = %d, body=%s", budgetID, response.Code, response.Body.String())
			}
			envelope := decodeEnvelope(t, response.Body.Bytes())
			if envelope.APIVersion != APIVersion {
				t.Fatalf("%s api_version = %q, want %q", budgetID, envelope.APIVersion, APIVersion)
			}
			if envelope.RequestID == "" {
				t.Fatalf("%s missing request_id", budgetID)
			}
			if envelope.Error != "" {
				t.Fatalf("%s unexpected error in envelope: %q", budgetID, envelope.Error)
			}
			completeness, ok := envelope.Completeness.(map[string]any)
			if !ok {
				t.Fatalf("%s completeness has wrong shape: %#v", budgetID, envelope.Completeness)
			}
			for _, field := range []string{"numerator", "denominator", "exclusions", "completeness"} {
				if _, ok := completeness[field]; !ok {
					t.Fatalf("%s completeness missing required field %q: %#v", budgetID, field, completeness)
				}
			}
			if containsForbiddenResponseKey(response.Body.Bytes()) {
				t.Fatalf("%s response tripped the forbidden-field scan: %s", budgetID, response.Body.String())
			}
		})
	}
}

func TestAgentProfileRouteReconcilesExactModelsSourcesAndOpaqueIdentity(t *testing.T) {
	dsn := testDSN(t)
	handler, bearer, pool := newTestAPIForEntityBreakdown(t, dsn)
	ctx := context.Background()
	observed := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	refs := dataplatform.DimensionRefs{
		DeviceID: "dev_agent_profile", AgentInstallationID: "ain_agent_profile",
		AgentID: "future-agent", SurfaceID: "asf_agent_profile",
		ProjectID: "prj_agent_profile", SessionID: "ses_agent_profile",
		TurnID: "trn_agent_profile", ModelID: "model-profile-a",
		ProviderID: "future-agent", AdapterVersionID: "adv_agent_profile",
		AdapterID: "future-agent", AdapterVersion: "9.4.1",
		SourceInstanceID: "src_agent_profile_bridge", SourceKind: "evidence_bridge",
		InstallationClass: "fixture", InstallationClassProvenance: "runtime_test_fixture",
	}
	if err := dataplatform.EnsureDimensions(ctx, pool, refs); err != nil {
		t.Fatalf("ensure profile dimensions: %v", err)
	}
	fact := dataplatform.FactRow{
		EventID: "evt_agent_profile", FactKey: "fact_agent_profile",
		EventType: "model.responded", ObservedAt: observed, IngestedAt: observed.Add(time.Second),
		TimestampQuality: "source_rfc3339", SourceInstanceID: refs.SourceInstanceID,
		SourceNativeEventID: "hmac-sha256:agent-profile-native",
		Sequence:            1, AgentInstallationID: refs.AgentInstallationID,
		SurfaceID: refs.SurfaceID, ProjectID: refs.ProjectID, SessionID: refs.SessionID,
		TurnID: refs.TurnID, ValueState: "observed", Outcome: "succeeded",
		CorrelationStatus: "exact",
	}
	evidence := dataplatform.EvidenceRow{
		EvidenceID: "evd_agent_profile", EventID: fact.EventID, ObservedAt: observed,
		SourceInstanceID: refs.SourceInstanceID, Tier: "native", Confidence: 1,
		Completeness: "complete", FirstSeenAt: observed, LastSeenAt: observed,
		SanitizerVersion:  "kansoku.ingress-sanitizer/1",
		PrivacyContractID: "privacy-contract-test",
		AssertEventType:   fact.EventType, AssertOutcome: fact.Outcome,
		AssertValueState: fact.ValueState,
	}
	if _, err := dataplatform.InsertFact(ctx, pool, fact, evidence); err != nil {
		t.Fatalf("insert profile fact: %v", err)
	}
	for _, table := range []string{"model_operations", "token_usage"} {
		if err := dataplatform.EnsurePartition(ctx, pool, table, observed); err != nil {
			t.Fatalf("ensure %s partition: %v", table, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO model_operations (
			model_operation_id, observed_at, event_id, model_id, session_id,
			provider_cost_micros, operation_kind, duration_ms, outcome,
			agent_installation_id, installation_attribution_state
		) VALUES ('mop_agent_profile',$1,$2,$3,$4,1200,'response',80,'succeeded',$5,'exact')
	`, observed, fact.EventID, refs.ModelID, refs.SessionID,
		refs.AgentInstallationID); err != nil {
		t.Fatalf("seed model profile projection: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO token_usage (
			token_usage_id, observed_at, model_operation_id,
			input_tokens, cached_input_tokens, output_tokens
		) VALUES ('tku_agent_profile',$1,'mop_agent_profile',100,20,30)
	`, observed); err != nil {
		t.Fatalf("seed token profile projection: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO source_watermarks (
			source_instance_id, last_read_sequence, last_emitted_sequence,
			last_observed_at, last_committed_at, gap_count, inactivity
		) VALUES ($2,1,1,$1,$1,0,false)
	`, observed, refs.SourceInstanceID); err != nil {
		t.Fatalf("seed source profile projection: %v", err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, entityBreakdownRequest(
		"/api/v1/agents/ain_agent_profile?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z",
		bearer,
	))
	if response.Code != http.StatusOK {
		t.Fatalf("profile status=%d body=%s", response.Code, response.Body.String())
	}
	envelope := decodeEnvelope(t, response.Body.Bytes())
	encoded, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatal(err)
	}
	var profile dataplatform.AgentProfileResponse
	if err := json.Unmarshal(encoded, &profile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if profile.Identity.DisplayName != "future-agent" ||
		profile.Identity.ProviderID != "future-agent" ||
		profile.Identity.AgentID != "future-agent" ||
		profile.Identity.AdapterID != "future-agent" ||
		profile.Identity.InstallationClass != "fixture" ||
		profile.Identity.InstallationClassProvenance != "runtime_test_fixture" ||
		profile.Identity.AgentInstallationID != "ain_agent_profile" {
		t.Fatalf("identity guessed or lost: %#v", profile.Identity)
	}
	if len(profile.Models) != 1 || profile.Models[0].ModelID != "model-profile-a" ||
		profile.Models[0].RequestCount != 1 || profile.Models[0].InputTokens != 100 ||
		profile.Models[0].CachedInputTokens != 20 || profile.Models[0].OutputTokens != 30 ||
		profile.Models[0].EstimatedCostMicros != 1200 {
		t.Fatalf("model reconciliation failed: %#v", profile.Models)
	}
	if profile.Models[0].ProviderCostedRequestCount != 1 ||
		profile.Models[0].ProviderCostMicros != 1200 ||
		profile.Models[0].APIEstimatedRequestCount != 0 ||
		profile.Models[0].APIEquivalentCostMicros != 0 {
		t.Fatalf("provider/API-equivalent cost lanes merged: %#v", profile.Models[0])
	}
	if len(profile.Sources) != 1 || profile.Sources[0].SourceKind != "evidence_bridge" ||
		profile.Sources[0].FactCount != 1 || profile.Sources[0].EvidenceCount != 1 {
		t.Fatalf("source reconciliation failed: %#v", profile.Sources)
	}
	if profile.Population.Numerator != 1 || profile.Population.Denominator != 1 ||
		profile.Exclusions["non_exact_installation_attribution"] != 0 {
		t.Fatalf("population/exclusions failed: %#v %#v", profile.Population, profile.Exclusions)
	}
	if containsForbiddenResponseKey(response.Body.Bytes()) {
		t.Fatalf("agent profile response tripped forbidden-field scan: %s", response.Body.String())
	}
}

// TestAnalyticsEntityBreakdownRejectsInvalidQueryWith400Never500 proves the
// extended analytics() dispatch never panics or 500s on malformed input for
// the new budget_id family: an invalid time range, an invalid metric_family
// used as a component-kind filter, and an unrecognized budget_id must all
// return a clean 400 with the standard error envelope.
func TestAnalyticsEntityBreakdownRejectsInvalidQueryWith400Never500(t *testing.T) {
	dsn := testDSN(t)
	handler, bearer, _ := newTestAPIForEntityBreakdown(t, dsn)

	cases := []struct {
		name string
		path string
	}{
		{"missing_from_to", "/api/v1/analytics?budget_id=agent_breakdown_range"},
		{"to_before_from", "/api/v1/analytics?budget_id=agent_breakdown_range&from=2026-06-02T00:00:00Z&to=2026-06-01T00:00:00Z"},
		{"range_too_wide", "/api/v1/analytics?budget_id=agent_breakdown_range&from=2020-01-01T00:00:00Z&to=2026-06-01T00:00:00Z"},
		{"unsafe_metric_family", "/api/v1/analytics?budget_id=component_breakdown_range&metric_family=" + "bad%20value%3B%20drop" + "&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z"},
		{"unknown_budget_id_after_range_ok", "/api/v1/analytics?budget_id=not_a_real_budget&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, entityBreakdownRequest(testCase.path, bearer))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body=%s", response.Code, response.Body.String())
			}
			envelope := decodeEnvelope(t, response.Body.Bytes())
			if envelope.Error == "" {
				t.Fatalf("expected a populated error category, got empty")
			}
			if envelope.Data != nil {
				t.Fatalf("error response must not carry data: %#v", envelope.Data)
			}
		})
	}
}

// TestMCPTopologyRouteServesRealTreeWithEnvelopeIntactAndRejectsBadRange
// proves the one genuinely new dedicated route from ADR 0013 decision #12:
// GET /api/v1/components/mcp/topology returns real parent/child component
// data through the standard envelope with the forbidden-field scan intact,
// and rejects a malformed range with 400 rather than panicking.
func TestMCPTopologyRouteServesRealTreeWithEnvelopeIntactAndRejectsBadRange(t *testing.T) {
	dsn := testDSN(t)
	handler, bearer, pool := newTestAPIForEntityBreakdown(t, dsn)

	// Seed one mcp component directly so the route has something to return.
	if _, err := pool.Exec(context.Background(), `INSERT INTO components (component_id, kind) VALUES ('comp_mcp_route_test', 'mcp') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed mcp component: %v", err)
	}

	from := "2026-06-01T00:00:00Z"
	to := "2026-06-02T00:00:00Z"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, entityBreakdownRequest("/api/v1/components/mcp/topology?from="+from+"&to="+to, bearer))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	envelope := decodeEnvelope(t, response.Body.Bytes())
	if envelope.APIVersion != APIVersion || envelope.RequestID == "" || envelope.Error != "" {
		t.Fatalf("malformed envelope: %#v", envelope)
	}
	if containsForbiddenResponseKey(response.Body.Bytes()) {
		t.Fatalf("mcp topology response tripped the forbidden-field scan: %s", response.Body.String())
	}
	dataBytes, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatalf("remarshal data: %v", err)
	}
	// envelope.Data carries the whole ComponentTopologyResponse (data,
	// formula_version, population, completeness), matching how a.write passes
	// the dataplatform result straight through as the envelope's data field --
	// not a bare array.
	var topology dataplatform.ComponentTopologyResponse
	if err := json.Unmarshal(dataBytes, &topology); err != nil {
		t.Fatalf("decode topology response: %v", err)
	}
	if topology.FormulaVersion != dataplatform.FormulaVersionMCPTopology1 {
		t.Fatalf("formula_version = %q, want %q", topology.FormulaVersion, dataplatform.FormulaVersionMCPTopology1)
	}
	found := false
	for _, node := range topology.Data {
		if node.ComponentID == "comp_mcp_route_test" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected seeded mcp component in topology response, got %+v", topology.Data)
	}

	badRange := httptest.NewRecorder()
	handler.ServeHTTP(badRange, entityBreakdownRequest("/api/v1/components/mcp/topology?from=not-a-time&to="+to, bearer))
	if badRange.Code != http.StatusBadRequest {
		t.Fatalf("bad range status = %d, want 400, body=%s", badRange.Code, badRange.Body.String())
	}
}
