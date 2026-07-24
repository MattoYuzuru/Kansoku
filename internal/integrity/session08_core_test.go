package integrity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/adaptersdk"
)

type memoryLiveCanaryStateStore struct {
	rows map[string]LiveCanaryRunState
}

func (s *memoryLiveCanaryStateStore) Load(_ context.Context, recipeID string) (LiveCanaryRunState, error) {
	return s.rows[recipeID], nil
}

func (s *memoryLiveCanaryStateStore) MarkStarted(_ context.Context, recipeID string, startedAt time.Time) error {
	state := s.rows[recipeID]
	state.LastStartedAt = startedAt.UTC()
	s.rows[recipeID] = state
	return nil
}

func (s *memoryLiveCanaryStateStore) MarkFinished(_ context.Context, recipeID string, status CheckStatus, finishedAt time.Time) error {
	state := s.rows[recipeID]
	state.LastFinishedAt = finishedAt.UTC()
	state.LastStatus = status
	s.rows[recipeID] = state
	return nil
}

func TestStructuralFingerprintCanonicalAndRejectsValuesByType(t *testing.T) {
	fields := []StructuralField{{Path: "$.count", PrimitiveType: PrimitiveInteger}, {Path: "$.status", PrimitiveType: PrimitiveString}}
	one, err := EventSchemaFingerprint("session.started", fields)
	if err != nil {
		t.Fatal(err)
	}
	two, err := EventSchemaFingerprint("session.started", []StructuralField{fields[1], fields[0]})
	if err != nil || one != two {
		t.Fatalf("canonical fingerprint mismatch %q %q err=%v", one, two, err)
	}
	if _, err := EventSchemaFingerprint("session.started", []StructuralField{{Path: "$.prompt", PrimitiveType: PrimitiveString}}); err == nil {
		t.Fatal("prohibited content field was accepted")
	}
}

func TestTargetedFingerprintRevalidationNeverRunsCanaryOrRetention(t *testing.T) {
	for _, kind := range []FingerprintKind{
		FingerprintExecutableVersion, FingerprintConfigRecipe, FingerprintAdapterVersion,
		FingerprintFixtureVersion, FingerprintFormulaRegistry, FingerprintEventSchema,
	} {
		stages := TargetedStagesForFingerprint(kind)
		for _, stage := range stages {
			if stage == Stage10OptionalLiveCanary || stage == Stage9RetentionDiskAndBackup {
				t.Fatalf("%s unexpectedly targets %s", kind, stage)
			}
		}
	}
}

func TestHealthGrayDefaultFreshGreenStaleYellowAndFailureRed(t *testing.T) {
	now := time.Now().UTC()
	capability := adaptersdk.CapabilityIngestionLiveStream
	snapshot := DeriveHealth("inst", capability, adaptersdk.StateHealthy, nil, now, time.Hour)
	if snapshot.WorstApplicable != HealthGray {
		t.Fatalf("empty evidence overall=%s", snapshot.WorstApplicable)
	}
	observed := now.Add(-time.Minute)
	pass := AuditCheck{AuditCheckKey: AuditCheckKey{AuditRunID: "run1", CheckID: EndpointAndHookCheckID, CapabilityID: string(capability), InstallationID: "inst", SourceID: "source"}, StageID: Stage2EndpointAndHookVerification, Status: CheckStatusPass, ObservedAt: &observed}
	snapshot = DeriveHealth("inst", capability, adaptersdk.StateHealthy, []AuditCheck{pass}, now, time.Hour)
	if dimensionState(snapshot, HealthConnectivity) != HealthGreen || snapshot.WorstApplicable != HealthGray {
		t.Fatalf("fresh pass snapshot=%#v", snapshot)
	}
	stale := now.Add(-2 * time.Hour)
	pass.ObservedAt = &stale
	snapshot = DeriveHealth("inst", capability, adaptersdk.StateHealthy, []AuditCheck{pass}, now, time.Hour)
	if dimensionState(snapshot, HealthConnectivity) != HealthYellow {
		t.Fatalf("stale state=%s", dimensionState(snapshot, HealthConnectivity))
	}
	pass.Status, pass.Category, pass.ObservedAt = CheckStatusFail, string(FailureClassEndpointUnreachable), &observed
	snapshot = DeriveHealth("inst", capability, adaptersdk.StateHealthy, []AuditCheck{pass}, now, time.Hour)
	if dimensionState(snapshot, HealthConnectivity) != HealthRed || snapshot.WorstApplicable != HealthRed {
		t.Fatalf("failure snapshot=%#v", snapshot)
	}
}

func dimensionState(snapshot HealthSnapshot, dimension HealthDimension) HealthState {
	for _, item := range snapshot.Dimensions {
		if item.Dimension == dimension {
			return item.State
		}
	}
	return ""
}

func TestLiveCanaryDisabledConsentBudgetDAGAndCleanup(t *testing.T) {
	now := time.Now().UTC()
	recipe := fixtureLiveCanaryRecipe()
	if err := recipe.Validate(); err != nil {
		t.Fatal(err)
	}
	recipe.Enabled = false
	if ok, reason := (LiveCanaryGate{}).Authorize(recipe, now); ok || reason != "disabled_by_default" {
		t.Fatalf("disabled gate=%v %s", ok, reason)
	}
	recipe.Enabled = true
	gate := LiveCanaryGate{ExplicitCredentialsPresent: true, ExplicitUserConsentRecorded: true, ConsentRecordedAt: now.Add(-time.Hour)}
	if ok, reason := gate.Authorize(recipe, now); !ok || reason != "" {
		t.Fatalf("authorized gate=%v %s", ok, reason)
	}
	cleaned := false
	check := NewLiveCanaryCheck(
		[]LiveCanaryRecipe{recipe}, map[string]LiveCanaryGate{recipe.RecipeID: gate},
		func(context.Context, LiveCanaryRecipe) (LiveCanaryObservation, error) {
			return LiveCanaryObservation{EventDAG: append([]string(nil), recipe.ExpectedEventDAG...), ObservedAt: now}, nil
		},
		func(context.Context, LiveCanaryRecipe) error { cleaned = true; return nil },
	)
	check.State = &memoryLiveCanaryStateStore{rows: map[string]LiveCanaryRunState{}}
	targets, err := check.Targets(context.Background(), CheckInput{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := check.Evaluate(context.Background(), CheckInput{Now: now}, targets[0])
	if err != nil || outcome.Status != CheckStatusPass || !cleaned {
		t.Fatalf("live canary outcome=%#v cleaned=%v err=%v", outcome, cleaned, err)
	}
	second, err := check.Evaluate(context.Background(), CheckInput{Now: now.Add(time.Minute)}, targets[0])
	if err != nil || second.Status != CheckStatusSkippedUnsupported || second.DetailRef != "cooldown_active" {
		t.Fatalf("durable cooldown outcome=%#v err=%v", second, err)
	}
}

func TestLiveCanaryMeasuredBudgetFailureStillCleansAndPersistsFailure(t *testing.T) {
	now := time.Now().UTC()
	recipe := fixtureLiveCanaryRecipe()
	gate := LiveCanaryGate{
		ExplicitCredentialsPresent: true, ExplicitUserConsentRecorded: true,
		ConsentRecordedAt: now.Add(-time.Hour),
	}
	cleaned := false
	state := &memoryLiveCanaryStateStore{rows: map[string]LiveCanaryRunState{}}
	check := NewLiveCanaryCheck(
		[]LiveCanaryRecipe{recipe}, map[string]LiveCanaryGate{recipe.RecipeID: gate},
		func(context.Context, LiveCanaryRecipe) (LiveCanaryObservation, error) {
			return LiveCanaryObservation{
				EventDAG: append([]string(nil), recipe.ExpectedEventDAG...),
				Turns:    recipe.MaxTurns + 1, Tokens: recipe.MaxTokens,
				CostUSD: recipe.MaxCostUSD, Duration: recipe.MaxDuration,
				ObservedAt: now,
			}, nil
		},
		func(context.Context, LiveCanaryRecipe) error { cleaned = true; return nil },
	)
	check.State = state
	targets, err := check.Targets(context.Background(), CheckInput{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := check.Evaluate(context.Background(), CheckInput{Now: now}, targets[0])
	if err != nil || outcome.Status != CheckStatusFail ||
		outcome.DetailRef != "canary_budget_exceeded" || !cleaned {
		t.Fatalf("budget outcome=%#v cleaned=%v err=%v", outcome, cleaned, err)
	}
	if state.rows[recipe.RecipeID].LastStatus != CheckStatusFail {
		t.Fatalf("durable last status=%s, want fail", state.rows[recipe.RecipeID].LastStatus)
	}
}

func TestLiveCanaryNonCooperativeObserverIsBoundedAndClassifiesDAGPrecisely(t *testing.T) {
	now := time.Now().UTC()
	recipe := fixtureLiveCanaryRecipe()
	recipe.MaxDuration = 20 * time.Millisecond
	release := make(chan struct{})
	cleaned := false
	check := NewLiveCanaryCheck(
		[]LiveCanaryRecipe{recipe},
		map[string]LiveCanaryGate{recipe.RecipeID: {
			ExplicitCredentialsPresent: true, ExplicitUserConsentRecorded: true,
			ConsentRecordedAt: now.Add(-time.Hour),
		}},
		func(context.Context, LiveCanaryRecipe) (LiveCanaryObservation, error) {
			<-release // deliberately ignores context until the test releases it
			return LiveCanaryObservation{}, nil
		},
		func(context.Context, LiveCanaryRecipe) error { cleaned = true; return nil },
	)
	check.State = &memoryLiveCanaryStateStore{rows: map[string]LiveCanaryRunState{}}
	targets, err := check.Targets(context.Background(), CheckInput{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	outcome, err := check.Evaluate(context.Background(), CheckInput{Now: now}, targets[0])
	close(release)
	if err != nil || outcome.Status != CheckStatusFail ||
		outcome.Category != string(FailureClassLiveCanaryProviderTimeout) ||
		time.Since(started) > time.Second || !cleaned {
		t.Fatalf("outcome=%+v elapsed=%s cleaned=%v err=%v", outcome, time.Since(started), cleaned, err)
	}
	if got := eventDAGMismatch([]string{"a", "b"}, []string{"b", "a"}); got != "event_dag_misordered" {
		t.Fatalf("misordered detail=%q", got)
	}
	if got := eventDAGMismatch([]string{"a", "b"}, []string{"a"}); got != "event_dag_missing count=1" {
		t.Fatalf("missing detail=%q", got)
	}
	if got := eventDAGMismatch([]string{"a"}, []string{"a", "b"}); got != "event_dag_extra count=1" {
		t.Fatalf("extra detail=%q", got)
	}
}

func TestStorageChecksFailStaleRepairAgeAndOverdueRetention(t *testing.T) {
	now := time.Now().UTC()
	rollup := NewRollupFormulaDBIntegrityCheck(
		new(pgxpool.Pool),
		func(context.Context, *pgxpool.Pool) (int64, error) { return 1, nil },
		func(context.Context, *pgxpool.Pool) ([]FormulaVersionExpectation, error) { return nil, nil },
		func(context.Context, *pgxpool.Pool) (bool, string, error) { return true, "", nil },
	)
	rollup.RollupFreshness = func(context.Context, *pgxpool.Pool) (RollupFreshnessEvidence, error) {
		return RollupFreshnessEvidence{OldestRepairEnqueuedAt: now.Add(-DefaultRepairAgeBudget - time.Second)}, nil
	}
	outcome, err := rollup.Evaluate(context.Background(), CheckInput{Now: now}, CheckTarget{})
	if err != nil || outcome.Status != CheckStatusFail ||
		outcome.Category != string(FailureClassRollupStale) ||
		!strings.Contains(outcome.DetailRef, "oldest_repair_age") {
		t.Fatalf("stale repair outcome=%+v err=%v", outcome, err)
	}
	rollup.RepairQueueDepth = func(context.Context, *pgxpool.Pool) (int64, error) { return 0, nil }
	rollup.RollupFreshness = func(context.Context, *pgxpool.Pool) (RollupFreshnessEvidence, error) {
		return RollupFreshnessEvidence{OldestPendingWatermark: now.Add(-DefaultRollupWatermarkBudget - time.Second)}, nil
	}
	outcome, err = rollup.Evaluate(context.Background(), CheckInput{Now: now}, CheckTarget{})
	if err != nil || outcome.Status != CheckStatusFail ||
		outcome.Category != string(FailureClassRollupStale) ||
		!strings.Contains(outcome.DetailRef, "pending_rollup_watermark_age") {
		t.Fatalf("stale zero-depth watermark outcome=%+v err=%v", outcome, err)
	}

	retention := NewRetentionDiskBackupCheck(
		func(context.Context, time.Time, int) (RetentionDryRunResult, error) {
			return RetentionDryRunResult{EligibleForDrop: map[string][]string{"events": {"events_2025_01"}}}, nil
		},
		nil, nil, nil,
	)
	finding := retention.checkRetentionDryRun(context.Background(), now)
	if !finding.failed || finding.category != string(FailureClassRetentionJobFailed) ||
		!strings.Contains(finding.detail, "retention_overdue") {
		t.Fatalf("retention finding=%+v", finding)
	}
	if !projectedDiskBudgetCrossing(0.89, 0.91, 1, 0.90, 1) {
		t.Fatal("89% -> 91% growth did not cross the configured 90% threshold")
	}
	if projectedDiskBudgetCrossing(0.89, 0.91, 1, 1.0, 1) {
		t.Fatal("89% -> 91% growth incorrectly crossed physical 100% capacity")
	}
	if !projectedDiskBudgetCrossing(0.87, 0.89, 1, 0.90, 1) {
		t.Fatal("89% current usage did not forecast next-day 90% threshold crossing")
	}
}

func TestTargetedFingerprintChangesFilterByAdapterSourceAndCapability(t *testing.T) {
	change := FingerprintChange{Current: &DriftFingerprint{
		Kind: FingerprintEventSchema, SubjectID: "adapter-a",
		SourceID: "source-a", CapabilityID: "ingestion.live_stream",
		ValueRef: "sha256:" + strings.Repeat("a", 64), ObservedAt: time.Now().UTC(),
	}}
	matching := CheckTarget{
		AdapterID: "adapter-a", SourceID: "source-a",
		CapabilityID: "ingestion.live_stream", InstallationID: "install-a",
	}
	if !targetMatchesFingerprintChanges(matching, []FingerprintChange{change}) {
		t.Fatal("exact adapter/source/capability target was filtered out")
	}
	for _, other := range []CheckTarget{
		{AdapterID: "adapter-b", SourceID: "source-a", CapabilityID: "ingestion.live_stream"},
		{AdapterID: "adapter-a", SourceID: "source-b", CapabilityID: "ingestion.live_stream"},
		{AdapterID: "adapter-a", SourceID: "source-a", CapabilityID: "ingestion.historical_import"},
	} {
		if targetMatchesFingerprintChanges(other, []FingerprintChange{change}) {
			t.Fatalf("unrelated target matched: %+v", other)
		}
	}
}

func TestSignedAuditReportTamperFailsVerification(t *testing.T) {
	now := time.Now().UTC()
	run := AuditRun{AuditRunID: "run-report", Mode: RunModeFull, Trigger: TriggerScheduledDaily, State: RunPassed}
	key := []byte(strings.Repeat("k", 32))
	signed, err := BuildSignedAuditReport(run, nil, now, "device-key-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignedAuditReport(signed, key); err != nil {
		t.Fatal(err)
	}
	signed.Report.State = RunFailed
	if err := VerifySignedAuditReport(signed, key); err == nil {
		t.Fatal("tampered report verified")
	}
}
