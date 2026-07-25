//go:build postgres_integration

// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema/applyAllMigrations
// work. This file is the Wave 1b (Session 10 continuation) companion to
// api_entity_breakdown_test.go: it proves the eight new read-only routes
// wired onto the eight new internal/dataplatform aggregation functions
// (ActivityTimeline, PromptShape, ModelUsage, ToolAnalytics, MCPUptime,
// ReliabilityCounts, SystemSnapshot, PrivacyCanaryHistory) serve real data
// through the standard APIEnvelope and reject malformed input with 400,
// never 500. It reuses newTestAPIForEntityBreakdown/entityBreakdownRequest/
// decodeEnvelope verbatim rather than redefining them.
package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedWave1bFixtures inserts one row into each table the eight new routes
// read from, all inside the [base, base+1day) window the tests query, so
// every route has real data to aggregate.
func seedWave1bFixtures(t *testing.T, pool *pgxpool.Pool, base time.Time) {
	t.Helper()
	ctx := context.Background()

	for _, table := range []string{"events", "tool_calls", "mcp_connections", "model_operations", "token_usage"} {
		if err := dataplatformEnsurePartition(t, ctx, pool, table, base); err != nil {
			t.Fatalf("ensure partition %s: %v", table, err)
		}
	}

	// events + prompt_features (activity timeline).
	if _, err := pool.Exec(ctx, `INSERT INTO devices (device_id) VALUES ('dev_wave1b') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO agent_installations (agent_installation_id, device_id, agent_id) VALUES ('ain_wave1b', 'dev_wave1b', 'fixture-agent') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed agent_installation: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (project_id) VALUES ('proj_wave1b') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sessions (session_id, project_id, started_at) VALUES ('ses_wave1b', 'proj_wave1b', now()) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO turns (turn_id, session_id, started_at) VALUES ('turn_wave1b', 'ses_wave1b', now()) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO adapter_versions (adapter_version_id, adapter_id, version) VALUES ('fixture-agent/1.0.0', 'fixture-agent', '1.0.0') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed adapter_version: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO source_instances (source_instance_id, adapter_version_id, source_kind) VALUES ('src_wave1b', 'fixture-agent/1.0.0', 'hook_http') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed source_instance: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO events (event_id, fact_key, event_type, observed_at, source_instance_id, source_native_event_id, sequence, session_id, value_state, outcome, correlation_status)
		VALUES ('evt_wave1b_1', 'evt_wave1b_1', 'component.executed', $1, 'src_wave1b', 'evt_wave1b_1', 1, 'ses_wave1b', 'observed', 'succeeded', 'exact')
	`, base.Add(time.Minute)); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO prompt_features (prompt_feature_id, turn_id, observed_at, prompt_size_bytes, value_state)
		VALUES ('pf_wave1b_1', 'turn_wave1b', $1, 512, 'observed')
	`, base.Add(2*time.Minute)); err != nil {
		t.Fatalf("seed prompt_feature: %v", err)
	}

	// model_operations + token_usage + cost_estimates (model usage).
	if _, err := pool.Exec(ctx, `INSERT INTO providers (provider_id) VALUES ('prov_wave1b') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO models (model_id, provider_id) VALUES ('model_wave1b', 'prov_wave1b') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO model_operations (model_operation_id, observed_at, model_id) VALUES ('mop_wave1b_1', $1, 'model_wave1b')`, base.Add(3*time.Minute)); err != nil {
		t.Fatalf("seed model_operation: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO token_usage (token_usage_id, observed_at, model_operation_id, input_tokens, output_tokens) VALUES ('tu_wave1b_1', $1, 'mop_wave1b_1', 100, 50)`, base.Add(3*time.Minute)); err != nil {
		t.Fatalf("seed token_usage: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO price_catalog_versions (price_catalog_version_id, model_id, effective_at, input_price_micros, output_price_micros)
		VALUES ('pcv_wave1b', 'model_wave1b', $1, 10, 30) ON CONFLICT DO NOTHING
	`, base); err != nil {
		t.Fatalf("seed price_catalog_version: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO cost_estimates (cost_estimate_id, token_usage_id, price_catalog_version_id, cost_micros) VALUES ('ce_wave1b_1', 'tu_wave1b_1', 'pcv_wave1b', 1500)`); err != nil {
		t.Fatalf("seed cost_estimate: %v", err)
	}

	// tool_calls (tool analytics).
	if _, err := pool.Exec(ctx, `INSERT INTO components (component_id, kind) VALUES ('comp_wave1b_mcp', 'mcp') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed component: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tool_calls (tool_call_id, observed_at, component_id, duration_ms, outcome) VALUES ('tc_wave1b_1', $1, 'comp_wave1b_mcp', 120, 'succeeded')`, base.Add(4*time.Minute)); err != nil {
		t.Fatalf("seed tool_call: %v", err)
	}

	// mcp_connections (mcp uptime).
	if _, err := pool.Exec(ctx, `INSERT INTO mcp_connections (mcp_connection_id, observed_at, component_id, state) VALUES ('mc_wave1b_1', $1, 'comp_wave1b_mcp', 'connected')`, base.Add(5*time.Minute)); err != nil {
		t.Fatalf("seed mcp_connection 1: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mcp_connections (mcp_connection_id, observed_at, component_id, state) VALUES ('mc_wave1b_2', $1, 'comp_wave1b_mcp', 'disconnected')`, base.Add(15*time.Minute)); err != nil {
		t.Fatalf("seed mcp_connection 2: %v", err)
	}

	// schema_quarantine_metadata + reconciliation_runs/mismatches (reliability counts).
	if _, err := pool.Exec(ctx, `
		INSERT INTO schema_quarantine_metadata (quarantine_id, source_kind, schema_fingerprint, category, byte_count, record_count, observed_at)
		VALUES ('sqm_wave1b_1', 'hook_http', 'fp_wave1b', 'unknown_field', 64, 1, $1)
	`, base.Add(6*time.Minute)); err != nil {
		t.Fatalf("seed schema_quarantine_metadata: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO reconciliation_runs (reconciliation_run_id, started_at, finished_at, status) VALUES ('rr_wave1b_1', $1, $1, 'failed')`, base.Add(7*time.Minute)); err != nil {
		t.Fatalf("seed reconciliation_run: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO reconciliation_mismatches (reconciliation_mismatch_id, reconciliation_run_id, fact_key, category) VALUES ('rm_wave1b_1', 'rr_wave1b_1', 'fact_wave1b', 'missing_bucket')`); err != nil {
		t.Fatalf("seed reconciliation_mismatch: %v", err)
	}

	// integrity_backup_status (system snapshot).
	if _, err := pool.Exec(ctx, `
		INSERT INTO integrity_backup_status (id, last_backup_at, last_backup_checksum_ok, last_restore_test_at, last_restore_test_ran, last_restore_test_passed)
		VALUES (1, $1, true, $1, true, true)
		ON CONFLICT (id) DO UPDATE SET last_backup_at = EXCLUDED.last_backup_at, last_backup_checksum_ok = EXCLUDED.last_backup_checksum_ok,
			last_restore_test_at = EXCLUDED.last_restore_test_at, last_restore_test_ran = EXCLUDED.last_restore_test_ran, last_restore_test_passed = EXCLUDED.last_restore_test_passed
	`, base); err != nil {
		t.Fatalf("seed integrity_backup_status: %v", err)
	}

	// integrity_audit_runs/checks (privacy canary history).
	if _, err := pool.Exec(ctx, `
		INSERT INTO integrity_audit_runs (audit_run_id, run_mode, trigger, state, scheduled_at, started_at, finished_at, advisory_lock_key, requested_stages, inputs_version_ref)
		VALUES ('iar_wave1b_1', 'full', 'scheduled_daily', 'passed', $1, $1, $1, 1, '[]'::jsonb, '{}'::jsonb)
	`, base.Add(8*time.Minute)); err != nil {
		t.Fatalf("seed integrity_audit_run: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO integrity_audit_checks (audit_run_id, check_id, capability_id, installation_id, source_id, stage_id, status, observed_at)
		VALUES ('iar_wave1b_1', 'stage_9_retention_disk_and_backup', 'disk-forecast', 'ain_wave1b', 'privacy-canary', 'stage_9', 'pass', $1)
	`, base.Add(8*time.Minute)); err != nil {
		t.Fatalf("seed integrity_audit_check: %v", err)
	}
}

// dataplatformEnsurePartition mirrors internal/dataplatform.EnsurePartition's
// monthly-partition DDL for the runtime package's own tests, which do not
// import dataplatform's unexported partition helpers directly but still need
// partitions created on the shared pool before inserting into partitioned
// fact tables (events, tool_calls, mcp_connections, model_operations,
// token_usage).
func dataplatformEnsurePartition(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, at time.Time) error {
	t.Helper()
	start := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	name := table + "_" + start.Format("2006_01")
	ddl := "CREATE TABLE IF NOT EXISTS " + pgIdent(name) + " PARTITION OF " + pgIdent(table) +
		" FOR VALUES FROM ('" + start.UTC().Format("2006-01-02T15:04:05.000000Z") + "') TO ('" + end.UTC().Format("2006-01-02T15:04:05.000000Z") + "')"
	_, err := pool.Exec(ctx, ddl)
	return err
}

// TestWave1bRoutesServeRealAggregationsWithEnvelopeIntact proves each of the
// eight new Wave 1b routes returns 200 with a valid envelope (api_version,
// request_id, no error, completeness fields present) and passes the
// forbidden-field scan, when real fixture rows exist in range.
func TestWave1bRoutesServeRealAggregationsWithEnvelopeIntact(t *testing.T) {
	dsn := testDSN(t)
	handler, bearer, pool := newTestAPIForEntityBreakdown(t, dsn)

	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	seedWave1bFixtures(t, pool, base)

	from := "2026-06-01T00:00:00Z"
	to := "2026-06-02T00:00:00Z"
	rangedRoutes := []string{
		"/api/v1/activity",
		"/api/v1/prompts/shape",
		"/api/v1/models/usage",
		"/api/v1/tools/analytics",
		"/api/v1/components/mcp/uptime",
		"/api/v1/reliability/counts",
		"/api/v1/privacy/canary-history",
	}
	for _, route := range rangedRoutes {
		t.Run(route, func(t *testing.T) {
			path := route + "?from=" + from + "&to=" + to
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, entityBreakdownRequest(path, bearer))
			if response.Code != http.StatusOK {
				t.Fatalf("%s status = %d, body=%s", route, response.Code, response.Body.String())
			}
			assertWave1bEnvelope(t, route, response.Body.Bytes())
		})
	}

	t.Run("/api/v1/system/snapshot", func(t *testing.T) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, entityBreakdownRequest("/api/v1/system/snapshot", bearer))
		if response.Code != http.StatusOK {
			t.Fatalf("system/snapshot status = %d, body=%s", response.Code, response.Body.String())
		}
		assertWave1bEnvelope(t, "/api/v1/system/snapshot", response.Body.Bytes())
	})

	t.Run("/api/v1/tools/analytics with component_id filter", func(t *testing.T) {
		path := "/api/v1/tools/analytics?component_id=comp_wave1b_mcp&from=" + from + "&to=" + to
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, entityBreakdownRequest(path, bearer))
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
		}
		assertWave1bEnvelope(t, "/api/v1/tools/analytics?component_id=...", response.Body.Bytes())
	})
}

func assertWave1bEnvelope(t *testing.T, label string, body []byte) {
	t.Helper()
	envelope := decodeEnvelope(t, body)
	if envelope.APIVersion != APIVersion {
		t.Fatalf("%s api_version = %q, want %q", label, envelope.APIVersion, APIVersion)
	}
	if envelope.RequestID == "" {
		t.Fatalf("%s missing request_id", label)
	}
	if envelope.Error != "" {
		t.Fatalf("%s unexpected error in envelope: %q", label, envelope.Error)
	}
	completeness, ok := envelope.Completeness.(map[string]any)
	if !ok {
		t.Fatalf("%s completeness has wrong shape: %#v", label, envelope.Completeness)
	}
	for _, field := range []string{"numerator", "denominator", "exclusions", "completeness"} {
		if _, ok := completeness[field]; !ok {
			t.Fatalf("%s completeness missing required field %q: %#v", label, field, completeness)
		}
	}
	if containsForbiddenResponseKey(body) {
		t.Fatalf("%s response tripped the forbidden-field scan: %s", label, body)
	}
}

// TestWave1bRoutesRejectInvalidRangeWith400Never500 proves every range-taking
// Wave 1b route returns a clean 400 (never a panic or 500) on a malformed
// [from, to) range or an invalid component_id, mirroring
// TestAnalyticsEntityBreakdownRejectsInvalidQueryWith400Never500's pattern.
func TestWave1bRoutesRejectInvalidRangeWith400Never500(t *testing.T) {
	dsn := testDSN(t)
	handler, bearer, _ := newTestAPIForEntityBreakdown(t, dsn)

	rangedRoutes := []string{
		"/api/v1/activity",
		"/api/v1/prompts/shape",
		"/api/v1/models/usage",
		"/api/v1/tools/analytics",
		"/api/v1/components/mcp/uptime",
		"/api/v1/reliability/counts",
		"/api/v1/privacy/canary-history",
	}
	for _, route := range rangedRoutes {
		t.Run(route+"_missing_range", func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, entityBreakdownRequest(route, bearer))
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
		t.Run(route+"_to_before_from", func(t *testing.T) {
			path := route + "?from=2026-06-02T00:00:00Z&to=2026-06-01T00:00:00Z"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, entityBreakdownRequest(path, bearer))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body=%s", response.Code, response.Body.String())
			}
		})
	}

	t.Run("tools_analytics_unsafe_component_id", func(t *testing.T) {
		path := "/api/v1/tools/analytics?component_id=" + "bad%20value%3B%20drop" + "&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, entityBreakdownRequest(path, bearer))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body=%s", response.Code, response.Body.String())
		}
	})
}
