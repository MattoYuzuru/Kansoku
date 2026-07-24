package integrity

import (
	"errors"
	"fmt"
	"time"
)

// ErrIllegalTransition is returned whenever a caller attempts a transition
// audit-run-and-schedule.yaml's state_machine does not permit.
var ErrIllegalTransition = errors.New("illegal_audit_run_transition")

// ErrAlreadyTerminal is returned when a caller attempts any transition on a
// run already in a terminal state, matching `no_backward_transition`: "a run
// that reached passed, degraded, failed or cancelled never transitions
// again".
var ErrAlreadyTerminal = errors.New("audit_run_already_terminal")

// Transition validates and applies one state_machine edge from
// audit-run-and-schedule.yaml, mutating run in place and returning an error
// if the edge is not legal from run's current state. now is the observation
// time recorded on the relevant timestamp field.
func Transition(run *AuditRun, to RunState, now time.Time, failureReason FailureReason) error {
	if run.State.terminal() {
		return fmt.Errorf("%w: run %s is already %s", ErrAlreadyTerminal, run.AuditRunID, run.State)
	}
	if !legalTransition(run.State, to) {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, run.State, to)
	}
	switch to {
	case RunRunning:
		run.StartedAt = timePtr(now)
	case RunPassed, RunDegraded, RunFailed, RunCancelled:
		run.FinishedAt = timePtr(now)
		run.FailureReason = failureReason
	}
	run.State = to
	return nil
}

// legalTransition encodes exactly the edges audit-run-and-schedule.yaml's
// state_machine declares:
//
//	scheduled -> running
//	running   -> passed | degraded | failed | cancelled
//
// No other edge (including any transition out of a terminal state, or
// scheduled directly to a terminal state without ever running) is legal.
func legalTransition(from, to RunState) bool {
	switch from {
	case RunScheduled:
		return to == RunRunning
	case RunRunning:
		switch to {
		case RunPassed, RunDegraded, RunFailed, RunCancelled:
			return true
		}
		return false
	default:
		return false
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}

// EvaluateOutcome derives the terminal RunState a running audit_run should
// transition to, given its checks' statuses and each check's category ->
// health-tier mapping, matching audit-run-and-schedule.yaml's
// running_to_passed/running_to_degraded/running_to_failed rules:
//
//   - passed: every eligible check completed pass or skipped_unsupported,
//     no check failed.
//   - degraded: at least one check failed with a yellow-tier category (or a
//     benign precondition left evidence gray, e.g. a disabled-by-default
//     stage), but no red-tier failure was observed.
//   - failed: at least one check failed with a red-tier category, or a
//     stage exceeded its timeout bound and could not be safely retried.
//
// redTierCategories is the caller-supplied set of category values this run
// treats as red-tier (an "observed breakage" health tier); every other
// failing category is treated as yellow-tier. This stage does not yet wire
// the full category->health-tier mapping (that is a later stage's
// responsibility once real checks exist), so EvaluateOutcome accepts it as
// an explicit parameter rather than hardcoding an incomplete mapping here.
func EvaluateOutcome(checks []AuditCheck, redTierCategories map[string]bool) RunState {
	sawFailure := false
	sawRedTier := false
	for _, c := range checks {
		if c.Status != CheckStatusFail {
			continue
		}
		sawFailure = true
		if redTierCategories[c.Category] || c.Category == string(FailureReasonStageTimeout) {
			sawRedTier = true
		}
	}
	switch {
	case sawRedTier:
		return RunFailed
	case sawFailure:
		return RunDegraded
	default:
		return RunPassed
	}
}
