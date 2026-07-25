package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type JobID string
type JobState string

const (
	JobDailyIntegrity JobID = "daily_integrity"
	JobRollupRepair   JobID = "rollup_repair"
	JobRetention      JobID = "retention"
	JobBackup         JobID = "backup"
	JobRestoreVerify  JobID = "restore_verify"
	JobExport         JobID = "export"
	JobImport         JobID = "import"

	JobScheduled      JobState = "scheduled"
	JobRunning        JobState = "running"
	JobPassed         JobState = "passed"
	JobFailed         JobState = "failed"
	JobCancelled      JobState = "cancelled"
	JobInterrupted    JobState = "interrupted"
	JobAlreadyRunning JobState = "already_running"
)

var safeErrorClass = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

type JobRun struct {
	JobRunID     string           `json:"job_run_id"`
	JobID        JobID            `json:"job_id"`
	State        JobState         `json:"state"`
	Attempt      int              `json:"attempt"`
	ScheduledAt  time.Time        `json:"scheduled_at"`
	StartedAt    *time.Time       `json:"started_at,omitempty"`
	FinishedAt   *time.Time       `json:"finished_at,omitempty"`
	ErrorClass   string           `json:"error_class,omitempty"`
	DetailRef    string           `json:"detail_ref,omitempty"`
	ResultCounts map[string]int64 `json:"result_counts"`
}

type JobHandler func(context.Context) (map[string]int64, error)

type JobFailure struct {
	Class string
}

func (e *JobFailure) Error() string { return e.Class }

type JobManager struct {
	pool     *pgxpool.Pool
	ownerID  string
	lease    time.Duration
	handlers map[JobID]JobHandler
	now      func() time.Time
}

type JobStore = JobManager

func NewJobManager(pool *pgxpool.Pool, handlers map[JobID]JobHandler) (*JobManager, error) {
	if pool == nil {
		return nil, errors.New("job_manager_pool_required")
	}
	ownerID, err := newOpaqueID("worker")
	if err != nil {
		return nil, err
	}
	copied := make(map[JobID]JobHandler, len(handlers))
	for id, handler := range handlers {
		if !validJobID(id) || handler == nil {
			return nil, errors.New("invalid_job_handler")
		}
		copied[id] = handler
	}
	return &JobManager{pool: pool, ownerID: ownerID, lease: 2 * time.Minute, handlers: copied, now: time.Now}, nil
}

func validJobID(id JobID) bool {
	switch id {
	case JobDailyIntegrity, JobRollupRepair, JobRetention, JobBackup, JobRestoreVerify, JobExport, JobImport:
		return true
	default:
		return false
	}
}

func (m *JobManager) RecoverInterrupted(ctx context.Context) error {
	_, err := m.pool.Exec(ctx, `
		UPDATE runtime_job_runs
		SET state='interrupted', finished_at=$1, lease_owner_id=NULL,
		    lease_expires_at=NULL, error_class='lease_expired',
		    detail_ref='runtime.startup_recovery', updated_at=$1
		WHERE state='running' AND lease_expires_at < $1
	`, m.now().UTC())
	return err
}

func (m *JobManager) Run(ctx context.Context, id JobID) (JobRun, error) {
	handler := m.handlers[id]
	if !validJobID(id) || handler == nil {
		return JobRun{}, errors.New("job_not_configured")
	}
	var last JobRun
	for attempt := 1; attempt <= 3; attempt++ {
		run, err := m.runAttempt(ctx, id, handler, attempt)
		if err != nil {
			return JobRun{}, err
		}
		last = run
		if run.State != JobFailed || run.ErrorClass == "operator_input_required" ||
			errors.Is(ctx.Err(), context.Canceled) {
			return run, nil
		}
	}
	return last, nil
}

func (m *JobManager) runAttempt(ctx context.Context, id JobID, handler JobHandler, attempt int) (JobRun, error) {
	runID, err := newOpaqueID("job")
	if err != nil {
		return JobRun{}, err
	}
	now := m.now().UTC()
	if _, err := m.pool.Exec(ctx, `
		INSERT INTO runtime_job_runs
			(job_run_id, job_id, state, attempt, scheduled_at, result_counts)
		VALUES ($1,$2,'scheduled',$3,$4,'{}'::jsonb)
	`, runID, id, attempt, now); err != nil {
		return JobRun{}, err
	}
	leaseUntil := now.Add(m.lease)
	if _, err := m.pool.Exec(ctx, `
		UPDATE runtime_job_runs SET state='running', started_at=$2,
			lease_owner_id=$3, lease_expires_at=$4, updated_at=$2
		WHERE job_run_id=$1
	`, runID, now, m.ownerID, leaseUntil); err != nil {
		if isUniqueViolation(err) {
			if _, updateErr := m.pool.Exec(ctx, `
				UPDATE runtime_job_runs SET state='already_running',
					finished_at=$2, error_class='already_running',
					detail_ref='runtime.job_lease', updated_at=$2
				WHERE job_run_id=$1
			`, runID, now); updateErr != nil {
				return JobRun{}, updateErr
			}
			return m.get(ctx, runID)
		}
		return JobRun{}, err
	}

	counts, runErr := m.runWithLeaseRenewal(ctx, runID, handler)
	state := JobPassed
	errorClass := ""
	detailRef := ""
	if runErr != nil {
		state = JobFailed
		errorClass = "job_failed"
		if classified := SafeErrorClass(runErr); classified != "operation_failed" {
			errorClass = classified
		}
		var classified *JobFailure
		if errors.As(runErr, &classified) && safeErrorClass.MatchString(classified.Class) {
			errorClass = classified.Class
		}
		detailRef = "runtime.job_handler"
		if errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			state = JobCancelled
			errorClass = "cancelled"
		}
	}
	if counts == nil {
		counts = map[string]int64{}
	}
	encoded, err := json.Marshal(counts)
	if err != nil {
		encoded = []byte(`{}`)
		state, errorClass, detailRef = JobFailed, "invalid_result_counts", "runtime.job_handler"
	}
	finished := m.now().UTC()
	if _, err := m.pool.Exec(context.WithoutCancel(ctx), `
		UPDATE runtime_job_runs
		SET state=$2, finished_at=$3, lease_owner_id=NULL, lease_expires_at=NULL,
		    error_class=NULLIF($4,''), detail_ref=NULLIF($5,''),
		    result_counts=$6::jsonb, updated_at=$3
		WHERE job_run_id=$1 AND lease_owner_id=$7
	`, runID, state, finished, errorClass, detailRef, encoded, m.ownerID); err != nil {
		return JobRun{}, err
	}
	return m.get(context.WithoutCancel(ctx), runID)
}

type jobHandlerResult struct {
	counts map[string]int64
	err    error
}

func (m *JobManager) runWithLeaseRenewal(ctx context.Context, runID string, handler JobHandler) (map[string]int64, error) {
	handlerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan jobHandlerResult, 1)
	go func() {
		counts, err := handler(handlerCtx)
		result <- jobHandlerResult{counts: counts, err: err}
	}()
	interval := m.lease / 2
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case completed := <-result:
			return completed.counts, completed.err
		case <-ctx.Done():
			cancel()
			completed := <-result
			if completed.err != nil {
				return completed.counts, completed.err
			}
			return completed.counts, ctx.Err()
		case renewedAt := <-ticker.C:
			expiresAt := renewedAt.UTC().Add(m.lease)
			tag, err := m.pool.Exec(ctx, `
				UPDATE runtime_job_runs
				SET lease_expires_at=$3, updated_at=$2
				WHERE job_run_id=$1 AND state='running' AND lease_owner_id=$4
			`, runID, renewedAt.UTC(), expiresAt, m.ownerID)
			if err != nil || tag.RowsAffected() != 1 {
				cancel()
				<-result
				return nil, &JobFailure{Class: "lease_renewal_failed"}
			}
		}
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (m *JobManager) List(ctx context.Context) ([]JobRun, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT job_run_id, job_id, state, attempt, scheduled_at, started_at,
		       finished_at, COALESCE(error_class,''), COALESCE(detail_ref,''),
		       result_counts
		FROM runtime_job_runs ORDER BY scheduled_at DESC LIMIT 500
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []JobRun
	for rows.Next() {
		run, err := scanJobRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func (m *JobManager) get(ctx context.Context, runID string) (JobRun, error) {
	return scanJobRun(m.pool.QueryRow(ctx, `
		SELECT job_run_id, job_id, state, attempt, scheduled_at, started_at,
		       finished_at, COALESCE(error_class,''), COALESCE(detail_ref,''),
		       result_counts
		FROM runtime_job_runs WHERE job_run_id=$1
	`, runID))
}

type jobRowScanner interface {
	Scan(...any) error
}

func scanJobRun(row jobRowScanner) (JobRun, error) {
	var run JobRun
	var encoded []byte
	if err := row.Scan(&run.JobRunID, &run.JobID, &run.State, &run.Attempt, &run.ScheduledAt,
		&run.StartedAt, &run.FinishedAt, &run.ErrorClass, &run.DetailRef, &encoded); err != nil {
		return JobRun{}, err
	}
	if err := json.Unmarshal(encoded, &run.ResultCounts); err != nil {
		return JobRun{}, errors.New("invalid_job_result_counts")
	}
	return run, nil
}
