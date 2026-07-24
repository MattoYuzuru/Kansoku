package integrity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Scheduler drives the daily-integrity workflow: single-writer advisory
// lock acquisition, AuditRun creation/state transitions, and stage
// execution against a CheckRegistry. It reuses internal/dataplatform's
// existing *pgxpool.Pool (passed in by the caller) rather than opening a
// second connection pool, per ADR 0011.
type Scheduler struct {
	pool        *pgxpool.Pool
	checks      *CheckRegistry
	newRunID    func() string
	reportKeyID string
	reportKey   []byte
}

// NewScheduler constructs a Scheduler over an already-connected pool (e.g.
// the result of internal/dataplatform.Connect) and a CheckRegistry (later
// stages populate this with real Check implementations; Stage 2 may pass
// an empty NewCheckRegistry()).
func NewScheduler(pool *pgxpool.Pool, checks *CheckRegistry) *Scheduler {
	if checks == nil {
		checks = NewCheckRegistry()
	}
	return &Scheduler{pool: pool, checks: checks, newRunID: newAuditRunID}
}

// ConfigureReportSigning enables the stage-11 final signed report. The key
// is copied and stays process-local; only key_id and HMAC output persist.
func (s *Scheduler) ConfigureReportSigning(keyID string, key []byte) error {
	if keyID == "" || len(key) < 32 {
		return errors.New("report signing requires a named key of at least 32 bytes")
	}
	s.reportKeyID = keyID
	s.reportKey = append([]byte(nil), key...)
	return nil
}

func newAuditRunID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand.Read failing is exceptionally unlikely (would indicate a
		// broken system RNG); fall back to a time-derived ID rather than
		// panicking, since an audit_run ID only needs to be unique, not
		// cryptographically unpredictable.
		return fmt.Sprintf("run_%d", time.Now().UnixNano())
	}
	return "run_" + hex.EncodeToString(buf)
}

// ErrAlreadyRunning is returned by StartRun when another session already
// holds the daily-integrity advisory lock. Callers must treat this as a
// clean, expected outcome ("already running"), never as a failure to
// retry aggressively.
var ErrAlreadyRunning = errors.New("daily_integrity_already_running")

// RunResult is the outcome of one full StartRun call: the finished
// AuditRun row and the checks it evaluated.
type RunResult struct {
	Run    AuditRun
	Checks []AuditCheck
}

type reusablePasses map[string]AuditCheck

// StartRun attempts to acquire the single daily-integrity advisory lock,
// and if successful, creates a new audit_run row, transitions it through
// scheduled -> running -> a terminal state, executing every registered
// Check for the stages this mode/trigger selects (see StagesForRun), and
// finally releases the lock. If the lock is already held, it returns
// ErrAlreadyRunning without creating any audit_run row or blocking --
// matching the non_overlap_guarantee: "a second scheduler instance or a
// manual trigger that fails to acquire the lock must record a skipped
// attempt and never start a concurrent audit_run row."
func (s *Scheduler) StartRun(ctx context.Context, mode RunMode, trigger Trigger, inputsVersionRef map[string]string, now time.Time) (RunResult, error) {
	return s.startRun(ctx, mode, trigger, StagesForRun(mode, trigger), nil, nil, inputsVersionRef, now)
}

func (s *Scheduler) startStartupRecoveryRun(ctx context.Context, inputsVersionRef map[string]string, prior reusablePasses, now time.Time) (RunResult, error) {
	return s.startRun(ctx, RunModeReduced, TriggerStartup, StagesForRun(RunModeReduced, TriggerStartup), nil, prior, inputsVersionRef, now)
}

// StartTargetedVersionRun executes only the union of stages named by the
// changed fingerprint kinds. It is always reduced/version_change_detected
// and rejects stage 9/10 expansion.
func (s *Scheduler) StartTargetedVersionRun(ctx context.Context, changes []FingerprintChange, inputsVersionRef map[string]string, now time.Time) (RunResult, error) {
	stages := TargetedStagesForChanges(changes)
	if len(stages) == 0 {
		return RunResult{}, errors.New("targeted version run requires at least one fingerprint change")
	}
	for _, stage := range stages {
		if stage == Stage9RetentionDiskAndBackup || stage == Stage10OptionalLiveCanary {
			return RunResult{}, errors.New("targeted version run cannot include retention or live canary")
		}
	}
	return s.startRun(ctx, RunModeReduced, TriggerVersionChangeDetected, stages, changes, nil, inputsVersionRef, now)
}

func (s *Scheduler) startRun(ctx context.Context, mode RunMode, trigger Trigger, stages []StageID, changes []FingerprintChange, prior reusablePasses, inputsVersionRef map[string]string, now time.Time) (RunResult, error) {
	if s.pool == nil {
		return RunResult{}, errors.New("daily integrity scheduler requires PostgreSQL pool")
	}
	if err := ValidateModeTrigger(mode, trigger); err != nil {
		return RunResult{}, err
	}
	lock, err := AcquireLock(ctx, s.pool)
	if err != nil {
		if errors.Is(err, ErrLockNotAcquired) {
			attempt := AuditAttempt{
				AttemptID: s.newRunID(), Mode: mode, Trigger: trigger,
				AttemptedAt: now, Outcome: "already_running", AdvisoryLockKey: AdvisoryLockKey(),
			}
			if recordErr := RecordAlreadyRunningAttempt(ctx, s.pool, attempt); recordErr != nil {
				return RunResult{}, recordErr
			}
			return RunResult{}, ErrAlreadyRunning
		}
		return RunResult{}, err
	}
	defer func() { _ = lock.Release(context.Background()) }()

	run := AuditRun{
		AuditRunID:       s.newRunID(),
		Mode:             mode,
		Trigger:          trigger,
		State:            RunScheduled,
		ScheduledAt:      now,
		AdvisoryLockKey:  lock.key,
		RequestedStages:  stages,
		InputsVersionRef: inputsVersionRef,
	}
	if run.InputsVersionRef == nil {
		run.InputsVersionRef = map[string]string{}
	}
	if err := InsertScheduledRun(ctx, s.pool, run); err != nil {
		return RunResult{}, fmt.Errorf("insert scheduled run: %w", err)
	}

	if err := Transition(&run, RunRunning, now, FailureReasonNone); err != nil {
		return RunResult{}, err
	}
	if err := TransitionRun(ctx, s.pool, run); err != nil {
		return RunResult{}, err
	}

	checks, redTier, stageErr := s.runStages(ctx, run, stages, changes, prior, now)

	terminal := RunPassed
	failureReason := FailureReasonNone
	if errors.Is(stageErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		terminal = RunCancelled
		failureReason = FailureReasonOperatorCancelled
	} else if stageErr != nil {
		terminal = RunFailed
		failureReason = FailureReasonStageTimeout
	} else {
		terminal = EvaluateOutcome(checks, redTier)
		if terminal == RunFailed {
			failureReason = FailureReasonRedTierCheckFailed
		}
	}
	finishedAt := time.Now().UTC()
	finalCtx, cancelFinal := context.WithTimeout(context.Background(), stageTimeout(Stage11PersistReportAndIncidents))
	defer cancelFinal()
	reportCheck, finalErr := s.finalizeStage11(finalCtx, &run, checks, terminal, failureReason, finishedAt)
	checks = append(checks, reportCheck)
	if finalErr != nil && run.State == RunRunning {
		return RunResult{Run: run, Checks: checks}, finalErr
	}
	return RunResult{Run: run, Checks: checks}, nil
}

// runStages executes every registered Check for each selected stage, in
// ascending ordinal order, persisting a pending row before evaluation and
// the real outcome after, so an interrupted run leaves accurate partial
// evidence rather than silently-inferred passes. redTierCategories is
// returned so the caller's EvaluateOutcome call uses the same mapping this
// stage discovered; Stage 2 has no real Check bodies registered yet, so
// this is empty in practice until later stages populate CheckRegistry.
func (s *Scheduler) runStages(ctx context.Context, run AuditRun, stages []StageID, changes []FingerprintChange, prior reusablePasses, now time.Time) ([]AuditCheck, map[string]bool, error) {
	var results []AuditCheck
	redTier := map[string]bool{}
	stageOrdinal := map[StageID]int{}
	for _, d := range StageRegistry {
		stageOrdinal[d.StageID] = d.Ordinal
	}
	for _, stageID := range orderedByOrdinal(stages, stageOrdinal) {
		// Stage 11 is scheduler-owned finalization. It always runs once after
		// every selected evaluation stage so incident/signing evidence is
		// based on the complete check set.
		if stageID == Stage11PersistReportAndIncidents {
			continue
		}
		if err := ctx.Err(); err != nil {
			return results, redTier, err
		}
		stageChecks := s.checks.ForStage(stageID)
		if len(stageChecks) == 0 {
			unwired := AuditCheck{
				AuditCheckKey: AuditCheckKey{
					AuditRunID: run.AuditRunID, CheckID: string(stageID),
					CapabilityID: "ingestion.historical_import", InstallationID: "kansoku-integrity-engine",
				},
				StageID: stageID, Status: CheckStatusFail,
				Category:  string(FailureClassDBIntegrityViolation),
				DetailRef: "mandatory_stage_not_wired", ObservedAt: timePtr(now),
				StartedAt: timePtr(now), FinishedAt: timePtr(now),
			}
			if err := UpsertCheck(ctx, s.pool, unwired); err != nil {
				return results, redTier, err
			}
			results = append(results, unwired)
			continue
		}
		timeout := stageTimeout(stageID)
		stageCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		for _, check := range stageChecks {
			in := CheckInput{AuditRunID: run.AuditRunID, Mode: run.Mode, Trigger: run.Trigger, Now: now}
			targets, err := check.Targets(stageCtx, in)
			if err != nil {
				category := string(FailureClassDBIntegrityViolation)
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(stageCtx.Err(), context.DeadlineExceeded) {
					category = string(FailureReasonStageTimeout)
				}
				failed := AuditCheck{
					AuditCheckKey: AuditCheckKey{
						AuditRunID: run.AuditRunID, CheckID: string(stageID) + ".targets",
						CapabilityID: "ingestion.historical_import", InstallationID: "kansoku-integrity-engine",
					},
					StageID: stageID, Status: CheckStatusFail, Category: category,
					DetailRef: "stage_target_enumeration_failed", ObservedAt: timePtr(time.Now()),
					StartedAt: timePtr(now), FinishedAt: timePtr(time.Now()),
				}
				if err := UpsertCheck(ctx, s.pool, failed); err != nil {
					cancel()
					return results, redTier, err
				}
				results = append(results, failed)
				continue
			}
			for _, target := range targets {
				if len(changes) > 0 && !targetMatchesFingerprintChanges(target, changes) {
					continue
				}
				reuseKey := reusablePassKey(check.CheckID(), target.CapabilityID, target.InstallationID, target.SourceID)
				if previous, ok := prior[reuseKey]; ok && previous.Status == CheckStatusPass &&
					previous.ObservedAt != nil && IsFresh(*previous.ObservedAt, now, DefaultFreshnessWindow) {
					reused := AuditCheck{
						AuditCheckKey: AuditCheckKey{
							AuditRunID: run.AuditRunID, CheckID: check.CheckID(),
							CapabilityID: target.CapabilityID, InstallationID: target.InstallationID, SourceID: target.SourceID,
						},
						StageID: stageID, Status: CheckStatusPass,
						DetailRef:  "fresh_pass_reused_after_crash_recovery",
						ObservedAt: previous.ObservedAt, StartedAt: timePtr(now), FinishedAt: timePtr(now),
					}
					if err := UpsertCheck(ctx, s.pool, reused); err != nil {
						cancel()
						return results, redTier, err
					}
					results = append(results, reused)
					continue
				}
				in.CapabilityID = target.CapabilityID
				in.InstallationID = target.InstallationID
				in.SourceID = target.SourceID
				pending := AuditCheck{
					AuditCheckKey: AuditCheckKey{AuditRunID: run.AuditRunID, CheckID: check.CheckID(), CapabilityID: target.CapabilityID, InstallationID: target.InstallationID, SourceID: target.SourceID},
					StageID:       stageID,
					Status:        CheckStatusPending,
					StartedAt:     timePtr(now),
				}
				if err := UpsertCheck(ctx, s.pool, pending); err != nil {
					cancel()
					return results, redTier, err
				}
				outcome, err := check.Evaluate(stageCtx, in, target)
				if err != nil {
					pending.Status = CheckStatusFail
					pending.Category = string(FailureClassDBIntegrityViolation)
					pending.DetailRef = "check_evaluation_failed"
					if errors.Is(err, context.DeadlineExceeded) || errors.Is(stageCtx.Err(), context.DeadlineExceeded) {
						pending.Category = string(FailureReasonStageTimeout)
						pending.DetailRef = "stage_timeout"
					}
					pending.ObservedAt = timePtr(time.Now())
					pending.FinishedAt = timePtr(time.Now())
					if upsertErr := UpsertCheck(ctx, s.pool, pending); upsertErr != nil {
						return results, redTier, upsertErr
					}
					results = append(results, pending)
					continue
				}
				if outcome.CheckID != check.CheckID() {
					outcome = CheckOutcome{
						CheckID: check.CheckID(), Status: CheckStatusFail,
						Category:  string(FailureClassDBIntegrityViolation),
						DetailRef: "check_identity_mismatch", ObservedAt: time.Now().UTC(),
					}
				}
				pending.Status = outcome.Status
				pending.Category = outcome.Category
				pending.DetailRef = outcome.DetailRef
				pending.ObservedAt = timePtr(outcome.ObservedAt)
				pending.FinishedAt = timePtr(time.Now())
				if err := UpsertCheck(ctx, s.pool, pending); err != nil {
					return results, redTier, err
				}
				results = append(results, pending)
			}
		}
		cancel()
	}
	for category, red := range redFailureClasses {
		if red {
			redTier[category] = true
		}
	}
	return results, redTier, nil
}

func reusablePassKey(checkID, capabilityID, installationID, sourceID string) string {
	return checkID + "\x00" + capabilityID + "\x00" + installationID + "\x00" + sourceID
}

func targetMatchesFingerprintChanges(target CheckTarget, changes []FingerprintChange) bool {
	for _, change := range changes {
		row := change.Current
		if row == nil {
			row = change.Previous
		}
		if row == nil {
			continue
		}
		// Executable/formula changes are deliberately global within the
		// already-reduced stage set. All other changes remain scoped by every
		// source/capability/adapter identity the provider supplied.
		if row.Kind == FingerprintExecutableVersion || row.Kind == FingerprintFormulaRegistry {
			return true
		}
		if row.SourceID != "" && row.SourceID != target.SourceID {
			continue
		}
		if row.CapabilityID != "" && row.CapabilityID != target.CapabilityID {
			continue
		}
		if row.SubjectID != "" &&
			row.SubjectID != target.AdapterID &&
			row.SubjectID != target.InstallationID &&
			row.SubjectID != target.SourceID {
			continue
		}
		return true
	}
	return false
}

func stageTimeout(stageID StageID) time.Duration {
	for _, descriptor := range StageRegistry {
		if descriptor.StageID == stageID {
			return time.Duration(descriptor.TimeoutSeconds) * time.Second
		}
	}
	return 30 * time.Second
}

func containsStage(stages []StageID, target StageID) bool {
	for _, stage := range stages {
		if stage == target {
			return true
		}
	}
	return false
}

func findStageCheck(checks []AuditCheck, stage StageID) int {
	for i := range checks {
		if checks[i].StageID == stage {
			return i
		}
	}
	return -1
}

func orderedByOrdinal(stages []StageID, ordinal map[StageID]int) []StageID {
	ordered := append([]StageID{}, stages...)
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && ordinal[ordered[j-1]] > ordinal[ordered[j]]; j-- {
			ordered[j-1], ordered[j] = ordered[j], ordered[j-1]
		}
	}
	return ordered
}

// randomJitter returns a pseudo-random duration in [0, max), used by the
// scheduled_daily trigger to add bounded jitter to a configured local
// time-of-day, matching audit-run-and-schedule.yaml's "configurable local
// time of day plus bounded random jitter". It uses crypto/rand rather than
// math/rand so this package introduces no seeded, predictable-schedule
// weakness and needs no explicit seeding.
func randomJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}
