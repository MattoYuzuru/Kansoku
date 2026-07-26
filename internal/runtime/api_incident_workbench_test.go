//go:build postgres_integration

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/integrity"
	"kansoku.local/kansoku/internal/observability"
	"kansoku.local/kansoku/internal/privacy"
)

func persistUnknownForWorkbench(
	t *testing.T,
	handoff *dataplatform.ObservabilityHandoff,
	at time.Time,
	fingerprint string,
) (observability.Quarantine, observability.Incident) {
	t.Helper()
	quarantine := observability.Quarantine{
		QuarantineID: "qua_" + fingerprint[:32], SourceKind: observability.SourceOTLPLog,
		SchemaFingerprint: fingerprint, Category: "unknown_schema",
		ByteCount: 97, RecordCount: 3, ObservedAt: at.UTC(),
	}
	incident := observability.NewSchemaIncident(
		"unknown_schema", observability.SourceOTLPLog, fingerprint, at,
	)
	if err := handoff.PersistQuarantineMetadata(quarantine, incident); err != nil {
		t.Fatalf("persist quarantine: %v", err)
	}
	return quarantine, incident
}

func decodeDataInto(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	envelope := decodeEnvelope(t, response.Body.Bytes())
	raw, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		t.Fatalf("decode data: %v body=%s", err, response.Body.String())
	}
}

func TestIncidentWorkbenchReplayPaginationProfilesAndDebugBundle(t *testing.T) {
	dsn := testDSN(t)
	handler, bearer, pool := newTestAPIForEntityBreakdown(t, dsn)
	handoff, err := dataplatform.NewObservabilityHandoff(pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	fingerprint := strings.Repeat("a", 64)
	quarantine, incident := persistUnknownForWorkbench(t, handoff, base, fingerprint)
	// A migration cannot prove the relationship for historical quarantine
	// rows, so it deliberately uses inc_unlinked_*. A fresh observation with
	// the exact same quarantine identity, source and fingerprint can prove
	// that relationship and must repair the manifest link deterministically.
	if _, err := pool.Exec(context.Background(), `
		UPDATE quarantine_structural_manifests
		SET incident_id='inc_unlinked_session12',
		    structural_field_paths='[]'::jsonb,
		    primitive_types='["object"]'::jsonb,
		    shape_value_state='not_observed'
		WHERE quarantine_id=$1
	`, quarantine.QuarantineID); err != nil {
		t.Fatalf("simulate legacy unlinked manifest: %v", err)
	}
	persistUnknownForWorkbench(t, handoff, base, fingerprint) // exact replay
	persistUnknownForWorkbench(t, handoff, base.Add(time.Second), fingerprint)

	var incidentCount, occurrenceCount, manifestCount int
	var durableOccurrenceCount int64
	var manifestIncidentID string
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM incidents WHERE incident_id=$1),
			(SELECT count(*) FROM incident_occurrences WHERE incident_id=$1),
			(SELECT count(*) FROM quarantine_structural_manifests WHERE incident_id=$1),
			(SELECT occurrence_count FROM incidents WHERE incident_id=$1),
			(SELECT incident_id FROM quarantine_structural_manifests WHERE quarantine_id=$2)
	`, incident.IncidentID, quarantine.QuarantineID).Scan(
		&incidentCount, &occurrenceCount, &manifestCount, &durableOccurrenceCount,
		&manifestIncidentID,
	); err != nil {
		t.Fatal(err)
	}
	if incidentCount != 1 || occurrenceCount != 2 || manifestCount != 1 ||
		durableOccurrenceCount != 2 || manifestIncidentID != incident.IncidentID {
		t.Fatalf(
			"incident=%d occurrences=%d manifests=%d durable_count=%d manifest_incident=%s",
			incidentCount, occurrenceCount, manifestCount, durableOccurrenceCount,
			manifestIncidentID,
		)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE incidents
		SET adapter_version='codex@0.145.0', source_id='source_fixture',
		    source_value_state='observed'
		WHERE incident_id=$1
	`, incident.IncidentID); err != nil {
		t.Fatal(err)
	}

	for index, char := range []string{"b", "c", "d", "e"} {
		persistUnknownForWorkbench(
			t, handoff, base.Add(time.Duration(index+2)*time.Second),
			strings.Repeat(char, 64),
		)
	}
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, entityBreakdownRequest("/api/v1/incidents?limit=2", bearer))
	var firstPage dataplatform.IncidentPage
	decodeDataInto(t, first, &firstPage)
	if len(firstPage.Data) != 2 || !firstPage.HasMore || firstPage.NextCursor == "" {
		t.Fatalf("first page=%+v", firstPage)
	}
	filtered := httptest.NewRecorder()
	handler.ServeHTTP(filtered, entityBreakdownRequest(
		"/api/v1/incidents?adapter=codex@0.145.0&source=source_fixture&failure=unknown_schema",
		bearer,
	))
	var filteredPage dataplatform.IncidentPage
	decodeDataInto(t, filtered, &filteredPage)
	if len(filteredPage.Data) != 1 || filteredPage.Data[0].IncidentID != incident.IncidentID {
		t.Fatalf("exact incident filters returned %+v", filteredPage.Data)
	}

	// A newer concurrent insert belongs before page one and therefore must
	// not shift or duplicate rows after the signed keyset cursor.
	newestFingerprint := strings.Repeat("f", 64)
	_, newestIncident := persistUnknownForWorkbench(
		t, handoff, base.Add(30*time.Second), newestFingerprint,
	)
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, entityBreakdownRequest(
		"/api/v1/incidents?limit=2&cursor="+firstPage.NextCursor, bearer,
	))
	var secondPage dataplatform.IncidentPage
	decodeDataInto(t, second, &secondPage)
	seen := map[string]bool{}
	for _, row := range append(firstPage.Data, secondPage.Data...) {
		if seen[row.IncidentID] {
			t.Fatalf("cursor duplicated incident %s", row.IncidentID)
		}
		seen[row.IncidentID] = true
		if row.IncidentID == newestIncident.IncidentID {
			t.Fatalf("new concurrent row shifted into a later page")
		}
	}
	tampered := httptest.NewRecorder()
	handler.ServeHTTP(tampered, entityBreakdownRequest(
		"/api/v1/incidents?limit=2&cursor="+firstPage.NextCursor+"x", bearer,
	))
	if tampered.Code != http.StatusBadRequest {
		t.Fatalf("tampered cursor status=%d body=%s", tampered.Code, tampered.Body.String())
	}

	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, entityBreakdownRequest(
		"/api/v1/incidents/"+incident.IncidentID, bearer,
	))
	var profile dataplatform.IncidentRecord
	decodeDataInto(t, detail, &profile)
	if profile.OccurrenceCount != 2 || profile.Installation.State != "not_observed" ||
		profile.Installation.Value != nil || profile.Source.State != "observed" ||
		profile.Source.Value == nil || *profile.Source.Value != "source_fixture" ||
		profile.SchemaFingerprint == nil ||
		*profile.SchemaFingerprint != fingerprint {
		t.Fatalf("profile lost explicit lineage states: %+v", profile)
	}

	occurrences := httptest.NewRecorder()
	handler.ServeHTTP(occurrences, entityBreakdownRequest(
		"/api/v1/incidents/"+incident.IncidentID+"/occurrences?limit=1", bearer,
	))
	var occurrencePage cursorPage[dataplatform.IncidentOccurrence]
	decodeDataInto(t, occurrences, &occurrencePage)
	if len(occurrencePage.Data) != 1 || !occurrencePage.HasMore ||
		occurrencePage.NextCursor == "" {
		t.Fatalf("occurrence page=%+v", occurrencePage)
	}

	manifest := httptest.NewRecorder()
	handler.ServeHTTP(manifest, entityBreakdownRequest(
		"/api/v1/quarantine/"+quarantine.QuarantineID, bearer,
	))
	var shape dataplatform.QuarantineManifest
	decodeDataInto(t, manifest, &shape)
	if shape.OccurrenceCount != 2 || shape.ShapeValueState != "observed" ||
		len(shape.StructuralFieldPaths) == 0 || shape.EventType.State != "unknown" {
		t.Fatalf("manifest=%+v", shape)
	}

	debug := httptest.NewRecorder()
	handler.ServeHTTP(debug, entityBreakdownRequest(
		"/api/v1/incidents/"+incident.IncidentID+"/debug-bundle?format=json", bearer,
	))
	if debug.Code != http.StatusOK || containsForbiddenResponseKey(debug.Body.Bytes()) {
		t.Fatalf("unsafe debug bundle status=%d body=%s", debug.Code, debug.Body.String())
	}
	for _, marker := range []string{
		"session12-secret-sk-canary", "SYNTHETIC_RAW_PROMPT",
		"SYNTHETIC_RAW_TOOL_RESULT", "/Users/canary/private",
	} {
		if strings.Contains(debug.Body.String(), marker) {
			t.Fatalf("debug bundle leaked canary marker %q", marker)
		}
	}

	if err := integrity.SetIncidentTriage(
		context.Background(), pool, incident.IncidentID, "acknowledged", "parser_fix_prepared",
	); err != nil {
		t.Fatalf("set triage: %v", err)
	}
	var detectorState, triageState string
	if err := pool.QueryRow(context.Background(), `
		SELECT detector_state, triage_state FROM incidents WHERE incident_id=$1
	`, incident.IncidentID).Scan(&detectorState, &triageState); err != nil {
		t.Fatal(err)
	}
	if detectorState != "open" || triageState != "acknowledged" {
		t.Fatalf("triage changed detector: detector=%s triage=%s", detectorState, triageState)
	}
	store, err := observability.OpenFileStore(
		filepath.Join(t.TempDir(), "session12-supported-state.json"), 4<<20,
	)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, err := observability.NewIngestor(
		store, bytes.Repeat([]byte("s"), 32), privacy.DefaultLimits(), 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	supportedAt := base.Add(4 * time.Second)
	ingestor.SetClockForTest(func() time.Time { return supportedAt })
	if err := ingestor.ConfigureDurableFactSink(handoff); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestor.IngestSafeFields(map[string]any{
		"event_id":    "session12-supported-occurrence",
		"session_id":  "session12-recovery-session",
		"observed_at": supportedAt.Format(time.RFC3339Nano),
		"event_type":  "source.observed",
		"outcome":     "unknown",
		"value_state": "observed",
	}, observability.FixtureAdapterID, observability.SourceOTLPLog, 100); err != nil {
		t.Fatalf("ingest supported recovery fixture: %v", err)
	}
	var supportedEventID string
	for _, fact := range store.Snapshot().Facts {
		supportedEventID = fact.Event.EventID
	}
	if supportedEventID == "" {
		t.Fatal("supported recovery fixture produced no durable event")
	}
	if recovered, err := integrity.RecordIngressRecovery(
		context.Background(), pool, incident.IncidentID, "missing-audit",
		supportedEventID, supportedAt,
	); err == nil || recovered {
		t.Fatalf("recovery without targeted audit succeeded: recovered=%v err=%v", recovered, err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO integrity_audit_runs (
			audit_run_id, run_mode, trigger, state, scheduled_at, started_at,
			finished_at, advisory_lock_key, requested_stages, inputs_version_ref
		) VALUES (
			'session12-recovery-audit','reduced','manual_operator_request','passed',
			$1,$1,$1,42,'["stage_4_parser_fixture_replay"]'::jsonb,'{}'::jsonb
		)
	`, base.Add(5*time.Second)); err != nil {
		t.Fatalf("seed targeted audit run: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO integrity_audit_checks (
			audit_run_id, check_id, capability_id, installation_id, source_id,
			stage_id, status, observed_at, started_at, finished_at
		) VALUES (
			'session12-recovery-audit','session12.parser-recovery','core_ingestion',
			'not_observed','not_observed','stage_4_parser_fixture_replay','pass',$1,$1,$1
		)
	`, base.Add(5*time.Second)); err != nil {
		t.Fatalf("seed targeted audit check: %v", err)
	}
	recovered, err := integrity.RecordIngressRecovery(
		context.Background(), pool, incident.IncidentID, "session12-recovery-audit",
		supportedEventID, supportedAt,
	)
	if err != nil || !recovered {
		t.Fatalf("fresh supported evidence recovery failed: recovered=%v err=%v", recovered, err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT detector_state FROM incidents WHERE incident_id=$1
	`, incident.IncidentID).Scan(&detectorState); err != nil {
		t.Fatal(err)
	}
	if detectorState != "resolved" {
		t.Fatalf("detector state after recovery=%s", detectorState)
	}
	var recoveryAuditRunID, recoveryEvidenceRef *string
	var recoveryObservedAt *time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT recovery_audit_run_id, recovery_observed_at, recovery_evidence_ref
		FROM incidents WHERE incident_id=$1
	`, incident.IncidentID).Scan(
		&recoveryAuditRunID, &recoveryObservedAt, &recoveryEvidenceRef,
	); err != nil {
		t.Fatal(err)
	}
	if recoveryAuditRunID == nil || *recoveryAuditRunID != "session12-recovery-audit" ||
		recoveryObservedAt == nil || !recoveryObservedAt.Equal(supportedAt) ||
		recoveryEvidenceRef == nil || *recoveryEvidenceRef != supportedEventID {
		t.Fatalf(
			"recovery lineage audit=%v observed=%v evidence=%v",
			recoveryAuditRunID, recoveryObservedAt, recoveryEvidenceRef,
		)
	}
	auditCheck := integrity.NewIncidentWorkbenchAuditCheck(pool)
	targets, err := auditCheck.Targets(context.Background(), integrity.CheckInput{})
	if err != nil || len(targets) != 1 {
		t.Fatalf("incident audit targets=%v err=%v", targets, err)
	}
	outcome, err := auditCheck.Evaluate(
		context.Background(),
		integrity.CheckInput{Now: base.Add(6 * time.Second)},
		targets[0],
	)
	if err != nil || outcome.Status != integrity.CheckStatusPass {
		t.Fatalf("incident workbench audit outcome=%+v err=%v", outcome, err)
	}
	expired, err := ApplyIncidentMetadataRetention(
		context.Background(), pool, base.AddDate(0, 0, 401), 400,
	)
	if err != nil {
		t.Fatalf("incident metadata retention: %v", err)
	}
	if expired["incident_occurrences"] != 7 ||
		expired["quarantine_structural_manifests"] != 6 ||
		expired["schema_quarantine_metadata"] != 6 {
		t.Fatalf("incident metadata retention counts=%v", expired)
	}
	var retainedOccurrences, retentionExclusions int64
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM incident_occurrences WHERE incident_id=$1),
			occurrence_retention_excluded_count
		FROM incidents WHERE incident_id=$1
	`, incident.IncidentID).Scan(
		&retainedOccurrences, &retentionExclusions,
	); err != nil {
		t.Fatal(err)
	}
	if retainedOccurrences != 0 || retentionExclusions != 2 {
		t.Fatalf(
			"retained occurrences=%d exclusions=%d",
			retainedOccurrences, retentionExclusions,
		)
	}
}
