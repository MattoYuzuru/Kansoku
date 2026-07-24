package integrity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type integrityExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// ErrRunNotFound is returned when a caller asks for an audit_run row that
// does not exist.
var ErrRunNotFound = errors.New("audit_run_not_found")

// RecordAlreadyRunningAttempt preserves non-overlap evidence without
// creating a second audit_run row.
func RecordAlreadyRunningAttempt(ctx context.Context, pool *pgxpool.Pool, attempt AuditAttempt) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO integrity_audit_attempts
		    (attempt_id, run_mode, trigger, attempted_at, outcome, advisory_lock_key)
		VALUES ($1,$2,$3,$4,'already_running',$5)
	`, attempt.AttemptID, string(attempt.Mode), string(attempt.Trigger), attempt.AttemptedAt.UTC(), attempt.AdvisoryLockKey)
	if err != nil {
		return fmt.Errorf("record already-running audit attempt: %w", err)
	}
	return nil
}

func ListAuditAttempts(ctx context.Context, pool *pgxpool.Pool) ([]AuditAttempt, error) {
	rows, err := pool.Query(ctx, `
		SELECT attempt_id, run_mode, trigger, attempted_at, outcome, advisory_lock_key
		FROM integrity_audit_attempts ORDER BY attempted_at, attempt_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditAttempt
	for rows.Next() {
		var attempt AuditAttempt
		var mode, trigger string
		if err := rows.Scan(&attempt.AttemptID, &mode, &trigger, &attempt.AttemptedAt, &attempt.Outcome, &attempt.AdvisoryLockKey); err != nil {
			return nil, err
		}
		attempt.Mode, attempt.Trigger = RunMode(mode), Trigger(trigger)
		out = append(out, attempt)
	}
	return out, rows.Err()
}

// InsertScheduledRun inserts a brand-new integrity_audit_runs row in the "scheduled"
// state, matching state_machine's scheduled_to_running precondition ("an
// audit_run row inserted with a started_at timestamp" happens on the next
// transition; the row itself is created here in "scheduled" first). The
// insert is a plain INSERT, never an upsert: a fresh AuditRunID is always
// a new row, matching no_backward_transition's "a subsequent audit is
// always a new audit_run row".
func InsertScheduledRun(ctx context.Context, pool *pgxpool.Pool, run AuditRun) error {
	requestedStagesJSON, err := json.Marshal(run.RequestedStages)
	if err != nil {
		return fmt.Errorf("marshal requested_stages: %w", err)
	}
	inputsJSON, err := json.Marshal(run.InputsVersionRef)
	if err != nil {
		return fmt.Errorf("marshal inputs_version_ref: %w", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO integrity_audit_runs (audit_run_id, run_mode, trigger, state, failure_reason, scheduled_at, started_at, finished_at, advisory_lock_key, requested_stages, inputs_version_ref)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11)
	`, run.AuditRunID, string(run.Mode), string(run.Trigger), string(run.State), string(run.FailureReason), run.ScheduledAt, run.StartedAt, run.FinishedAt, run.AdvisoryLockKey, requestedStagesJSON, inputsJSON)
	if err != nil {
		return fmt.Errorf("insert audit_run: %w", err)
	}
	return nil
}

// TransitionRun persists a state_machine transition already validated and
// applied in-memory by Transition: it writes run's current State/
// FailureReason/StartedAt/FinishedAt back to its integrity_audit_runs row. Callers
// must call Transition(run, ...) first; TransitionRun never itself decides
// legality, it only persists an already-legal in-memory transition.
func TransitionRun(ctx context.Context, pool *pgxpool.Pool, run AuditRun) error {
	return transitionRunWith(ctx, pool, run)
}

func transitionRunWith(ctx context.Context, executor integrityExecer, run AuditRun) error {
	tag, err := executor.Exec(ctx, `
		UPDATE integrity_audit_runs
		SET state = $2, failure_reason = NULLIF($3, ''), started_at = $4, finished_at = $5
		WHERE audit_run_id = $1
	`, run.AuditRunID, string(run.State), string(run.FailureReason), run.StartedAt, run.FinishedAt)
	if err != nil {
		return fmt.Errorf("transition audit_run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRunNotFound
	}
	return nil
}

// GetRun reads one integrity_audit_runs row by ID.
func GetRun(ctx context.Context, pool *pgxpool.Pool, auditRunID string) (AuditRun, error) {
	row := pool.QueryRow(ctx, `
		SELECT audit_run_id, run_mode, trigger, state, COALESCE(failure_reason, ''), scheduled_at, started_at, finished_at, advisory_lock_key, requested_stages, inputs_version_ref
		FROM integrity_audit_runs WHERE audit_run_id = $1
	`, auditRunID)
	run, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuditRun{}, ErrRunNotFound
	}
	return run, err
}

// ListRunsInState returns every integrity_audit_runs row currently in state, ordered
// by scheduled_at ascending.
func ListRunsInState(ctx context.Context, pool *pgxpool.Pool, state RunState) ([]AuditRun, error) {
	rows, err := pool.Query(ctx, `
		SELECT audit_run_id, run_mode, trigger, state, COALESCE(failure_reason, ''), scheduled_at, started_at, finished_at, advisory_lock_key, requested_stages, inputs_version_ref
		FROM integrity_audit_runs WHERE state = $1 ORDER BY scheduled_at ASC
	`, string(state))
	if err != nil {
		return nil, fmt.Errorf("list integrity audit runs: %w", err)
	}
	defer rows.Close()
	var runs []AuditRun
	for rows.Next() {
		run, err := scanRunFromRows(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRun(row rowScanner) (AuditRun, error) {
	return scanRunFromRows(row)
}

func scanRunFromRows(row rowScanner) (AuditRun, error) {
	var (
		run                             AuditRun
		mode, trigger, state, failure   string
		requestedStagesJSON, inputsJSON []byte
	)
	if err := row.Scan(&run.AuditRunID, &mode, &trigger, &state, &failure, &run.ScheduledAt, &run.StartedAt, &run.FinishedAt, &run.AdvisoryLockKey, &requestedStagesJSON, &inputsJSON); err != nil {
		return AuditRun{}, err
	}
	run.Mode = RunMode(mode)
	run.Trigger = Trigger(trigger)
	run.State = RunState(state)
	run.FailureReason = FailureReason(failure)
	var stages []StageID
	if err := json.Unmarshal(requestedStagesJSON, &stages); err != nil {
		return AuditRun{}, fmt.Errorf("unmarshal requested_stages: %w", err)
	}
	run.RequestedStages = stages
	inputs := map[string]string{}
	if err := json.Unmarshal(inputsJSON, &inputs); err != nil {
		return AuditRun{}, fmt.Errorf("unmarshal inputs_version_ref: %w", err)
	}
	run.InputsVersionRef = inputs
	return run, nil
}

// UpsertCheck idempotently inserts or updates one integrity_audit_checks row keyed by
// (audit_run_id, check_id, capability_id, installation_id), matching
// audit-run-and-schedule.yaml's idempotency_rule: "rerunning an interrupted
// stage recomputes the same key deterministically and upserts rather than
// duplicating a row". A row that already has a fresh (pass or
// skipped_unsupported) terminal status for this exact run is not
// overwritten with a pending placeholder; callers seed pending rows before
// evaluation and then call this again with the real outcome.
func UpsertCheck(ctx context.Context, pool *pgxpool.Pool, check AuditCheck) error {
	return upsertCheckWith(ctx, pool, check)
}

func upsertCheckWith(ctx context.Context, executor integrityExecer, check AuditCheck) error {
	_, err := executor.Exec(ctx, `
		INSERT INTO integrity_audit_checks (audit_run_id, check_id, capability_id, installation_id, source_id, stage_id, status, category, detail_ref, observed_at, started_at, finished_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), $10, $11, $12, now())
		ON CONFLICT (audit_run_id, check_id, capability_id, installation_id, source_id)
		DO UPDATE SET stage_id = EXCLUDED.stage_id, status = EXCLUDED.status, category = EXCLUDED.category, detail_ref = EXCLUDED.detail_ref, observed_at = EXCLUDED.observed_at, started_at = COALESCE(integrity_audit_checks.started_at, EXCLUDED.started_at), finished_at = EXCLUDED.finished_at, updated_at = now()
	`, check.AuditRunID, check.CheckID, check.CapabilityID, check.InstallationID, check.SourceID, string(check.StageID), string(check.Status), check.Category, check.DetailRef, check.ObservedAt, check.StartedAt, check.FinishedAt)
	if err != nil {
		return fmt.Errorf("upsert audit_check: %w", err)
	}
	return nil
}

// ListChecksForRun returns every integrity_audit_checks row for auditRunID.
func ListChecksForRun(ctx context.Context, pool *pgxpool.Pool, auditRunID string) ([]AuditCheck, error) {
	rows, err := pool.Query(ctx, `
		SELECT audit_run_id, check_id, capability_id, installation_id, source_id, stage_id, status, COALESCE(category, ''), COALESCE(detail_ref, ''), observed_at, started_at, finished_at
		FROM integrity_audit_checks WHERE audit_run_id = $1
	`, auditRunID)
	if err != nil {
		return nil, fmt.Errorf("list integrity audit checks: %w", err)
	}
	defer rows.Close()
	var checks []AuditCheck
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		checks = append(checks, c)
	}
	return checks, rows.Err()
}

// GetCheck returns one audit_checks row by its full key, or
// (AuditCheck{}, pgx.ErrNoRows) if absent.
func GetCheck(ctx context.Context, pool *pgxpool.Pool, key AuditCheckKey) (AuditCheck, error) {
	row := pool.QueryRow(ctx, `
		SELECT audit_run_id, check_id, capability_id, installation_id, source_id, stage_id, status, COALESCE(category, ''), COALESCE(detail_ref, ''), observed_at, started_at, finished_at
		FROM integrity_audit_checks WHERE audit_run_id = $1 AND check_id = $2 AND capability_id = $3 AND installation_id = $4 AND source_id = $5
	`, key.AuditRunID, key.CheckID, key.CapabilityID, key.InstallationID, key.SourceID)
	return scanCheck(row)
}

func scanCheck(row rowScanner) (AuditCheck, error) {
	var (
		c               AuditCheck
		stageID, status string
	)
	if err := row.Scan(&c.AuditRunID, &c.CheckID, &c.CapabilityID, &c.InstallationID, &c.SourceID, &stageID, &status, &c.Category, &c.DetailRef, &c.ObservedAt, &c.StartedAt, &c.FinishedAt); err != nil {
		return AuditCheck{}, err
	}
	c.StageID = StageID(stageID)
	c.Status = CheckStatus(status)
	return c, nil
}

// MarkStaleRunsInterrupted implements crash_recovery.stale_running_detection:
// "on process start, every audit_run row still in state running whose
// advisory lock is not currently held by a live session is marked
// interrupted, a terminal sub-state of failed with
// failure_reason=crash_recovery_stale_run". It never resumes a stale run in
// place (never_silent_resume): it only flips State to failed with that
// FailureReason so the run's incomplete checks stay exactly as they were
// (pending/gray), and returns the IDs it reclassified.
func MarkStaleRunsInterrupted(ctx context.Context, pool *pgxpool.Pool, now time.Time) ([]string, error) {
	lockHeld, err := IsHeldByAnySession(ctx, pool)
	if err != nil {
		return nil, err
	}
	if lockHeld {
		// A live session holds the lock, so any "running" row belongs to a
		// genuinely in-progress run, not a crashed one; nothing to
		// reclassify. This is the same principle audit-run-and-schedule.yaml
		// states for the lock's session-scoped release: a live holder means
		// the run is not abandoned.
		return nil, nil
	}
	running, err := ListRunsInState(ctx, pool, RunRunning)
	if err != nil {
		return nil, err
	}
	var interrupted []string
	for _, run := range running {
		if err := Transition(&run, RunFailed, now, FailureReasonCrashRecoveryStale); err != nil {
			return interrupted, fmt.Errorf("transition run %s to interrupted: %w", run.AuditRunID, err)
		}
		if err := TransitionRun(ctx, pool, run); err != nil {
			return interrupted, fmt.Errorf("mark run %s interrupted: %w", run.AuditRunID, err)
		}
		interrupted = append(interrupted, run.AuditRunID)
	}
	return interrupted, nil
}
