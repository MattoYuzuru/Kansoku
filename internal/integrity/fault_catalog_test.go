package integrity

import (
	"context"
	"testing"
	"time"
)

func runFaultClassifier(t *testing.T, id string) {
	t.Helper()
	injectedAt := time.Now().UTC()
	detection, err := InjectFaultForTest(context.Background(), id, injectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if detection.Outcome.Status != CheckStatusFail || detection.Outcome.Category != string(detection.Definition.FailureClass) {
		t.Fatalf("fault %s outcome=%#v definition=%#v", id, detection.Outcome, detection.Definition)
	}
	if detection.Definition.Evidence != FaultEvidenceComponentClassifier {
		t.Fatalf("fault %s evidence=%s, want component classifier", id, detection.Definition.Evidence)
	}
	if !detection.AffectedInterval.From.Equal(injectedAt) ||
		!detection.AffectedInterval.To.Equal(detection.DetectedAt) ||
		detection.AffectedInterval.To.Before(detection.AffectedInterval.From) {
		t.Fatalf("fault %s affected interval=%#v", id, detection.AffectedInterval)
	}
	sameRun := ApplyFreshFaultRecovery(detection, detection.AuditRunID, CheckOutcome{
		Status: CheckStatusPass, ObservedAt: detection.DetectedAt.Add(time.Second),
	})
	if sameRun.ResolvedAt != nil {
		t.Fatalf("fault %s recovered from same run", id)
	}
	recovery := ApplyFreshFaultRecovery(detection, detection.AuditRunID+"-recovery", CheckOutcome{
		Status: CheckStatusPass, ObservedAt: detection.DetectedAt.Add(time.Second),
	})
	if recovery.ResolvedAt == nil {
		t.Fatalf("fault %s did not recover from later fresh positive evidence", id)
	}
}

func TestFaultComponent_hook_removed_disabled_or_untrusted(t *testing.T) {
	runFaultClassifier(t, "hook_removed_disabled_or_untrusted")
}
func TestFaultComponent_otlp_wrong_port_protocol_or_auth(t *testing.T) {
	runFaultClassifier(t, "otlp_wrong_port_protocol_or_auth")
}
func TestFaultComponent_transcript_truncate_rotate_schema_or_permission_change(t *testing.T) {
	runFaultClassifier(t, "transcript_truncate_rotate_schema_or_permission_change")
}
func TestFaultComponent_active_process_with_absent_events(t *testing.T) {
	runFaultClassifier(t, "active_process_with_absent_events")
}
func TestFaultComponent_duplicate_and_stalled_watermarks(t *testing.T) {
	runFaultClassifier(t, "duplicate_and_stalled_watermarks")
}
func TestFaultComponent_parser_panic_timeout_or_unknown_field(t *testing.T) {
	runFaultClassifier(t, "parser_panic_timeout_or_unknown_field")
}
func TestFaultComponent_delayed_rollup(t *testing.T) { runFaultClassifier(t, "delayed_rollup") }
func TestFaultComponent_full_disk(t *testing.T)      { runFaultClassifier(t, "full_disk") }
func TestFaultComponent_stale_backup(t *testing.T)   { runFaultClassifier(t, "stale_backup") }
func TestFaultComponent_privacy_canary_violation(t *testing.T) {
	runFaultClassifier(t, "privacy_canary_violation")
}
func TestFaultComponent_live_canary_partial_dag(t *testing.T) {
	runFaultClassifier(t, "live_canary_partial_dag")
}
func TestFaultComponent_live_canary_provider_timeout(t *testing.T) {
	runFaultClassifier(t, "live_canary_provider_timeout")
}
func TestFaultComponent_endpoint_unreachable(t *testing.T) {
	runFaultClassifier(t, "endpoint_unreachable")
}
func TestFaultComponent_unknown_schema_quarantine(t *testing.T) {
	runFaultClassifier(t, "unknown_schema_quarantine")
}
func TestFaultComponent_cross_source_reconciliation_regression(t *testing.T) {
	runFaultClassifier(t, "cross_source_reconciliation_regression")
}
func TestFaultComponent_ingest_lag(t *testing.T) { runFaultClassifier(t, "ingest_lag") }
func TestFaultComponent_inventory_cache_miscount(t *testing.T) {
	runFaultClassifier(t, "inventory_cache_miscount")
}
