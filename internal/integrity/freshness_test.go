package integrity_test

import (
	"context"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/adaptersdk/fakeadapter"
	"kansoku.local/kansoku/internal/integrity"
	"kansoku.local/kansoku/internal/observability"
)

func watermarkLookupFor(sourceID string, watermark observability.Watermark, exists bool) integrity.WatermarkLookup {
	return func(ctx context.Context, requested string) (observability.Watermark, bool, error) {
		if requested != sourceID {
			return observability.Watermark{}, false, nil
		}
		return watermark, exists, nil
	}
}

func singleSource(installationID, sourceID string) integrity.SourceEnumerator {
	return func(ctx context.Context, adapterID string) ([]integrity.SourceTarget, error) {
		if adapterID != fakeadapter.AdapterID {
			return nil, nil
		}
		return []integrity.SourceTarget{{InstallationID: installationID, SourceID: sourceID}}, nil
	}
}

// TestSourceFreshnessCheckInactiveSourceNeverOpensGapIncident proves a
// genuinely-inactive source (Watermark.Inactivity=true: no agent/session/
// process evidence and no events) is classified true_inactivity_flagged and
// PASSES -- it must never be reported as a watermark_stall (gap) failure,
// matching contracts/observability/reconciliation.yaml's silence.
// true_inactivity rule.
func TestSourceFreshnessCheckInactiveSourceNeverOpensGapIncident(t *testing.T) {
	registry := registryWithLoomwright(t)
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	watermark := observability.Watermark{
		SourceID:          "loomwright.spindle",
		Lifecycle:         observability.SourceDiscovered,
		Inactivity:        true,
		ExpectedCadenceMS: 30_000,
		// Even though LastEligibleActivity is zero and cadence has "elapsed"
		// by any naive clock-only check, Inactivity=true must dominate.
	}
	check := integrity.NewSourceFreshnessCheck(registry, singleSource("install-1", "loomwright.spindle"), watermarkLookupFor("loomwright.spindle", watermark, true))

	in := integrity.CheckInput{AuditRunID: "run_1", Now: now}
	targets, err := check.Targets(context.Background(), in)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("Targets = %+v, want exactly one", targets)
	}
	outcome, err := check.Evaluate(context.Background(), in, targets[0])
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusPass {
		t.Fatalf("outcome = %+v, want pass: true inactivity is a flag, never a failure", outcome)
	}
	if outcome.Category != string(integrity.FailureClassTrueInactivityFlagged) {
		t.Fatalf("Category = %s, want %s (never watermark_stall)", outcome.Category, integrity.FailureClassTrueInactivityFlagged)
	}
	if outcome.Category == string(integrity.FailureClassWatermarkStall) {
		t.Fatalf("inactive source must never be classified as a stall/gap incident")
	}
}

// TestSourceFreshnessCheckStalledActiveSourceOpensGapIncidentWithinSLO
// proves a genuinely-eligible-but-stalled source (Inactivity=false, but no
// activity observed well beyond its declared heartbeat cadence) opens a
// watermark_stall failure within its declared SLO (ExpectedCadenceMS).
func TestSourceFreshnessCheckStalledActiveSourceOpensGapIncidentWithinSLO(t *testing.T) {
	registry := registryWithLoomwright(t)
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	cadence := int64(30_000) // 30s heartbeat SLO
	staleSince := now.Add(-90 * time.Second)
	watermark := observability.Watermark{
		SourceID:             "loomwright.spindle",
		Lifecycle:            observability.SourceProducing,
		Inactivity:           false,
		LastDiscovered:       now.Add(-1 * time.Hour),
		LastEligibleActivity: staleSince,
		ExpectedCadenceMS:    cadence,
	}
	check := integrity.NewSourceFreshnessCheck(registry, singleSource("install-1", "loomwright.spindle"), watermarkLookupFor("loomwright.spindle", watermark, true))

	in := integrity.CheckInput{AuditRunID: "run_1", Now: now}
	target := integrity.CheckTarget{InstallationID: "install-1", CapabilityID: string(adaptersdk.CapabilityIngestionLiveStream), SourceID: "loomwright.spindle"}
	outcome, err := check.Evaluate(context.Background(), in, target)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusFail {
		t.Fatalf("outcome = %+v, want fail: eligible activity stalled beyond declared cadence", outcome)
	}
	if outcome.Category != string(integrity.FailureClassWatermarkStall) {
		t.Fatalf("Category = %s, want %s", outcome.Category, integrity.FailureClassWatermarkStall)
	}
	// Detection must be within the declared SLO: the elapsed time since last
	// eligible activity to "now" (90s) exceeds the declared 30s cadence, so
	// this run correctly detects the stall on this very tick rather than
	// waiting arbitrarily longer.
	elapsed := now.Sub(staleSince)
	if elapsed <= time.Duration(cadence)*time.Millisecond {
		t.Fatalf("test fixture invalid: elapsed %v must exceed declared cadence %dms to prove SLO detection", elapsed, cadence)
	}
}

// TestSourceFreshnessCheckGapCountAloneAlsoOpensStall proves a source whose
// watermark already recorded a sequence gap (GapCount > 0) is stalled
// regardless of cadence bookkeeping, matching ingest.go's own gap detection
// output.
func TestSourceFreshnessCheckGapCountAloneAlsoOpensStall(t *testing.T) {
	registry := registryWithLoomwright(t)
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	watermark := observability.Watermark{
		SourceID:             "loomwright.spindle",
		Inactivity:           false,
		LastEligibleActivity: now,
		GapCount:             3,
		ExpectedCadenceMS:    30_000,
	}
	check := integrity.NewSourceFreshnessCheck(registry, singleSource("install-1", "loomwright.spindle"), watermarkLookupFor("loomwright.spindle", watermark, true))
	in := integrity.CheckInput{AuditRunID: "run_1", Now: now}
	target := integrity.CheckTarget{InstallationID: "install-1", CapabilityID: string(adaptersdk.CapabilityIngestionLiveStream), SourceID: "loomwright.spindle"}
	outcome, err := check.Evaluate(context.Background(), in, target)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusFail || outcome.Category != string(integrity.FailureClassWatermarkStall) {
		t.Fatalf("outcome = %+v, want fail/watermark_stall for a recorded sequence gap", outcome)
	}
}

// TestSourceFreshnessCheckFreshEligibleSourcePasses proves a source that is
// eligible, active and within its declared cadence passes cleanly.
func TestSourceFreshnessCheckFreshEligibleSourcePasses(t *testing.T) {
	registry := registryWithLoomwright(t)
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	watermark := observability.Watermark{
		SourceID:             "loomwright.spindle",
		Inactivity:           false,
		LastEligibleActivity: now.Add(-5 * time.Second),
		ExpectedCadenceMS:    30_000,
	}
	check := integrity.NewSourceFreshnessCheck(registry, singleSource("install-1", "loomwright.spindle"), watermarkLookupFor("loomwright.spindle", watermark, true))
	in := integrity.CheckInput{AuditRunID: "run_1", Now: now}
	target := integrity.CheckTarget{InstallationID: "install-1", CapabilityID: string(adaptersdk.CapabilityIngestionLiveStream), SourceID: "loomwright.spindle"}
	outcome, err := check.Evaluate(context.Background(), in, target)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusPass {
		t.Fatalf("outcome = %+v, want pass for a fresh, within-cadence source", outcome)
	}
}

// TestSourceFreshnessCheckNoDeclaredCadenceNeverStallsByElapsedTimeAlone
// proves a source declaring no heartbeat (ExpectedCadenceMS=0) is never
// judged stalled purely by elapsed time, matching "sources with declared
// heartbeats follow their heartbeat SLO" (silence about cadence is not
// itself a stall signal for a source that never declared one).
func TestSourceFreshnessCheckNoDeclaredCadenceNeverStallsByElapsedTimeAlone(t *testing.T) {
	registry := registryWithLoomwright(t)
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	watermark := observability.Watermark{
		SourceID:             "loomwright.spindle",
		Inactivity:           false,
		LastEligibleActivity: now.Add(-72 * time.Hour),
		ExpectedCadenceMS:    0,
	}
	check := integrity.NewSourceFreshnessCheck(registry, singleSource("install-1", "loomwright.spindle"), watermarkLookupFor("loomwright.spindle", watermark, true))
	in := integrity.CheckInput{AuditRunID: "run_1", Now: now}
	target := integrity.CheckTarget{InstallationID: "install-1", CapabilityID: string(adaptersdk.CapabilityIngestionLiveStream), SourceID: "loomwright.spindle"}
	outcome, err := check.Evaluate(context.Background(), in, target)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusPass {
		t.Fatalf("outcome = %+v, want pass: no declared cadence means no time-based stall claim", outcome)
	}
}

// TestSourceFreshnessCheckMissingWatermarkIsEligibilityUnknown proves a
// source with no watermark row at all (never discovered) is classified
// eligibility_unknown, matching "missing eligibility evidence = unknown
// after threshold" -- distinct from both true_inactivity_flagged and
// watermark_stall.
func TestSourceFreshnessCheckMissingWatermarkIsEligibilityUnknown(t *testing.T) {
	registry := registryWithLoomwright(t)
	check := integrity.NewSourceFreshnessCheck(registry, singleSource("install-1", "loomwright.spindle"), nil)
	in := integrity.CheckInput{AuditRunID: "run_1", Now: time.Now()}
	target := integrity.CheckTarget{InstallationID: "install-1", CapabilityID: string(adaptersdk.CapabilityIngestionLiveStream), SourceID: "loomwright.spindle"}
	outcome, err := check.Evaluate(context.Background(), in, target)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusFail || outcome.Category != string(integrity.FailureClassEligibilityUnknown) {
		t.Fatalf("outcome = %+v, want fail/eligibility_unknown for a source with no watermark row", outcome)
	}
}

// TestSourceFreshnessCheckTargetsGenericAcrossAdapters proves Targets
// iterates Registry.IDs() generically (never a hardcoded agent name) by
// enumerating sources for the Loomwright fake adapter through the same
// SourceEnumerator contract a real adapter would use.
func TestSourceFreshnessCheckTargetsGenericAcrossAdapters(t *testing.T) {
	registry := registryWithLoomwright(t)
	seen := map[string]bool{}
	sources := func(ctx context.Context, adapterID string) ([]integrity.SourceTarget, error) {
		seen[adapterID] = true
		return []integrity.SourceTarget{{InstallationID: "install-x", SourceID: "loomwright.spindle"}}, nil
	}
	check := integrity.NewSourceFreshnessCheck(registry, sources, nil)
	if _, err := check.Targets(context.Background(), integrity.CheckInput{}); err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if !seen[fakeadapter.AdapterID] {
		t.Fatalf("Targets never queried adapter_id=%s via Registry.IDs()", fakeadapter.AdapterID)
	}
}
