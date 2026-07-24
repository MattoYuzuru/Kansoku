package integrity

import (
	"errors"
	"testing"
	"time"
)

func newRun(state RunState) AuditRun {
	return AuditRun{AuditRunID: "run_test", State: state}
}

func TestTransitionScheduledToRunningIsLegal(t *testing.T) {
	run := newRun(RunScheduled)
	now := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)
	if err := Transition(&run, RunRunning, now, FailureReasonNone); err != nil {
		t.Fatalf("Transition scheduled->running: %v", err)
	}
	if run.State != RunRunning {
		t.Fatalf("state = %s, want running", run.State)
	}
	if run.StartedAt == nil || !run.StartedAt.Equal(now) {
		t.Fatalf("StartedAt not recorded: %+v", run.StartedAt)
	}
}

func TestTransitionRunningToEachTerminalStateIsLegal(t *testing.T) {
	for _, terminal := range []RunState{RunPassed, RunDegraded, RunFailed, RunCancelled} {
		run := newRun(RunRunning)
		now := time.Date(2026, 7, 23, 9, 5, 0, 0, time.UTC)
		if err := Transition(&run, terminal, now, FailureReasonNone); err != nil {
			t.Fatalf("Transition running->%s: %v", terminal, err)
		}
		if run.State != terminal {
			t.Fatalf("state = %s, want %s", run.State, terminal)
		}
		if run.FinishedAt == nil || !run.FinishedAt.Equal(now) {
			t.Fatalf("FinishedAt not recorded for %s: %+v", terminal, run.FinishedAt)
		}
	}
}

func TestTransitionScheduledDirectlyToTerminalIsIllegal(t *testing.T) {
	for _, terminal := range []RunState{RunPassed, RunDegraded, RunFailed, RunCancelled} {
		run := newRun(RunScheduled)
		err := Transition(&run, terminal, time.Now(), FailureReasonNone)
		if !errors.Is(err, ErrIllegalTransition) {
			t.Fatalf("Transition scheduled->%s: got %v, want ErrIllegalTransition", terminal, err)
		}
		if run.State != RunScheduled {
			t.Fatalf("state mutated on illegal transition: %s", run.State)
		}
	}
}

func TestTransitionOutOfTerminalStateIsIllegal(t *testing.T) {
	for _, terminal := range []RunState{RunPassed, RunDegraded, RunFailed, RunCancelled} {
		run := newRun(terminal)
		err := Transition(&run, RunRunning, time.Now(), FailureReasonNone)
		if !errors.Is(err, ErrAlreadyTerminal) {
			t.Fatalf("Transition %s->running: got %v, want ErrAlreadyTerminal", terminal, err)
		}
		// A second transition to a different terminal state must also be
		// rejected: no_backward_transition applies to every terminal state,
		// not only re-entering "running".
		err = Transition(&run, RunCancelled, time.Now(), FailureReasonNone)
		if !errors.Is(err, ErrAlreadyTerminal) {
			t.Fatalf("Transition %s->cancelled: got %v, want ErrAlreadyTerminal", terminal, err)
		}
	}
}

func TestTransitionRunningToRunningIsIllegal(t *testing.T) {
	run := newRun(RunRunning)
	err := Transition(&run, RunRunning, time.Now(), FailureReasonNone)
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("Transition running->running: got %v, want ErrIllegalTransition", err)
	}
}

func TestEvaluateOutcomePassedWhenNoFailures(t *testing.T) {
	checks := []AuditCheck{
		{Status: CheckStatusPass},
		{Status: CheckStatusSkippedUnsupported},
	}
	if got := EvaluateOutcome(checks, nil); got != RunPassed {
		t.Fatalf("EvaluateOutcome = %s, want passed", got)
	}
}

func TestEvaluateOutcomeDegradedOnYellowTierFailure(t *testing.T) {
	checks := []AuditCheck{
		{Status: CheckStatusPass},
		{Status: CheckStatusFail, Category: "watermark_stall"},
	}
	redTier := map[string]bool{"db_integrity_violation": true}
	if got := EvaluateOutcome(checks, redTier); got != RunDegraded {
		t.Fatalf("EvaluateOutcome = %s, want degraded", got)
	}
}

func TestEvaluateOutcomeFailedOnRedTierFailure(t *testing.T) {
	checks := []AuditCheck{
		{Status: CheckStatusPass},
		{Status: CheckStatusFail, Category: "db_integrity_violation"},
	}
	redTier := map[string]bool{"db_integrity_violation": true}
	if got := EvaluateOutcome(checks, redTier); got != RunFailed {
		t.Fatalf("EvaluateOutcome = %s, want failed", got)
	}
}

func TestEvaluateOutcomeFailedOnStageTimeoutCategory(t *testing.T) {
	checks := []AuditCheck{
		{Status: CheckStatusFail, Category: string(FailureReasonStageTimeout)},
	}
	if got := EvaluateOutcome(checks, nil); got != RunFailed {
		t.Fatalf("EvaluateOutcome = %s, want failed for stage_timeout category", got)
	}
}

func TestStagesForRunFullModeIsEveryStageInOrdinalOrder(t *testing.T) {
	stages := StagesForRun(RunModeFull, TriggerScheduledDaily)
	if len(stages) != len(StageRegistry) {
		t.Fatalf("full mode stage count = %d, want %d", len(stages), len(StageRegistry))
	}
	for i, d := range StageRegistry {
		if stages[i] != d.StageID {
			t.Fatalf("stage[%d] = %s, want %s (ordinal order violated)", i, stages[i], d.StageID)
		}
	}
}

func TestStagesForRunReducedModeStartupIsThreeStages(t *testing.T) {
	stages := StagesForRun(RunModeReduced, TriggerStartup)
	want := []StageID{Stage1DiscoveryAndConfiguration, Stage2EndpointAndHookVerification, Stage3WatermarkVsInactivity}
	if len(stages) != len(want) {
		t.Fatalf("reduced/startup stage count = %d, want %d", len(stages), len(want))
	}
	for i := range want {
		if stages[i] != want[i] {
			t.Fatalf("stage[%d] = %s, want %s", i, stages[i], want[i])
		}
	}
}

func TestStagesForRunReducedModeVersionChangeAddsStage4And7(t *testing.T) {
	stages := StagesForRun(RunModeReduced, TriggerVersionChangeDetected)
	found := map[StageID]bool{}
	for _, s := range stages {
		found[s] = true
	}
	for _, want := range []StageID{Stage1DiscoveryAndConfiguration, Stage2EndpointAndHookVerification, Stage3WatermarkVsInactivity, Stage4ParserFixtureReplay, Stage7UnknownSchemaAndLag} {
		if !found[want] {
			t.Fatalf("version_change_detected reduced stages missing %s: %v", want, stages)
		}
	}
	if found[Stage10OptionalLiveCanary] || found[Stage9RetentionDiskAndBackup] {
		t.Fatalf("version_change_detected must never force stage_9/stage_10: %v", stages)
	}
	// Ordinal order must still hold even though this is a filtered subset.
	lastOrdinal := -1
	ordinal := map[StageID]int{}
	for _, d := range StageRegistry {
		ordinal[d.StageID] = d.Ordinal
	}
	for _, s := range stages {
		if ordinal[s] <= lastOrdinal {
			t.Fatalf("reduced-mode stages not in ascending ordinal order: %v", stages)
		}
		lastOrdinal = ordinal[s]
	}
}

func TestIsFresh(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	if IsFresh(time.Time{}, now, DefaultFreshnessWindow) {
		t.Fatalf("zero-value observedAt must never be fresh")
	}
	if !IsFresh(now.Add(-1*time.Hour), now, DefaultFreshnessWindow) {
		t.Fatalf("1h-old evidence should be fresh within a 36h window")
	}
	if IsFresh(now.Add(-48*time.Hour), now, DefaultFreshnessWindow) {
		t.Fatalf("48h-old evidence should not be fresh within a 36h window")
	}
}
