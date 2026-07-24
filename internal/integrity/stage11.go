package integrity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const stage11CapabilityID = "ingestion.historical_import"
const stage11InstallationID = "kansoku-integrity-engine"

func stage11Check(runID string, now time.Time) AuditCheck {
	return AuditCheck{
		AuditCheckKey: AuditCheckKey{
			AuditRunID:     runID,
			CheckID:        string(Stage11PersistReportAndIncidents),
			CapabilityID:   stage11CapabilityID,
			InstallationID: stage11InstallationID,
			SourceID:       "integrity-finalization",
		},
		StageID: Stage11PersistReportAndIncidents, Status: CheckStatusPass,
		DetailRef:  "incidents_reconciled_and_signed_report_persisted",
		ObservedAt: timePtr(now), StartedAt: timePtr(now), FinishedAt: timePtr(now),
	}
}

// finalizeStage11 commits incident reconciliation, the strict signed report,
// the Stage-11 check and the audit_run terminal transition in one PostgreSQL
// transaction. No signed terminal report can exist while its run remains
// running or carries a different terminal state.
func (s *Scheduler) finalizeStage11(
	ctx context.Context,
	run *AuditRun,
	checks []AuditCheck,
	terminal RunState,
	failureReason FailureReason,
	now time.Time,
) (AuditCheck, error) {
	result := stage11Check(run.AuditRunID, now)
	if len(s.reportKey) < 32 || s.reportKeyID == "" {
		return s.finalizeStage11Failure(ctx, run, checks, result, now,
			"report_signing_not_configured", errors.New("report_signing_not_configured"))
	}

	candidate := *run
	if err := Transition(&candidate, terminal, now, failureReason); err != nil {
		return s.finalizeStage11Failure(ctx, run, checks, result, now,
			"terminal_transition_validation_failed", err)
	}
	reportChecks := append(append([]AuditCheck(nil), checks...), result)
	signed, err := BuildSignedAuditReport(candidate, reportChecks, now, s.reportKeyID, s.reportKey)
	if err != nil {
		return s.finalizeStage11Failure(ctx, run, checks, result, now,
			"signed_report_build_failed", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, fmt.Errorf("begin stage11 transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := reconcileRunIncidentsTx(ctx, tx, run.AuditRunID, checks, now); err != nil {
		_ = tx.Rollback(ctx)
		return s.finalizeStage11Failure(ctx, run, checks, result, now,
			"incident_lifecycle_persistence_failed", err)
	}
	if err := persistSignedAuditReportWith(ctx, tx, signed); err != nil {
		_ = tx.Rollback(ctx)
		return s.finalizeStage11Failure(ctx, run, checks, result, now,
			"signed_report_persistence_failed", err)
	}
	if err := upsertCheckWith(ctx, tx, result); err != nil {
		_ = tx.Rollback(ctx)
		return s.finalizeStage11Failure(ctx, run, checks, result, now,
			"stage11_check_persistence_failed", err)
	}
	if err := transitionRunWith(ctx, tx, candidate); err != nil {
		_ = tx.Rollback(ctx)
		return s.finalizeStage11Failure(ctx, run, checks, result, now,
			"terminal_transition_persistence_failed", err)
	}
	if err := tx.Commit(ctx); err != nil {
		_ = tx.Rollback(ctx)
		return s.finalizeStage11Failure(ctx, run, checks, result, now,
			"stage11_atomic_commit_failed", err)
	}
	*run = candidate
	return result, nil
}

// finalizeStage11Failure rolls back any caller transaction through its
// deferred rollback, then atomically persists a Stage-11 failure, its own
// incident, and a failed terminal transition. A signing/persistence failure
// never leaves a misleading successful report.
func (s *Scheduler) finalizeStage11Failure(
	ctx context.Context,
	run *AuditRun,
	checks []AuditCheck,
	result AuditCheck,
	now time.Time,
	detail string,
	cause error,
) (AuditCheck, error) {
	result.Status = CheckStatusFail
	result.Category = string(FailureClassDBIntegrityViolation)
	result.DetailRef = detail
	candidate := *run
	if err := Transition(&candidate, RunFailed, now, FailureReasonRedTierCheckFailed); err != nil {
		return result, errors.Join(cause, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, errors.Join(cause, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	allChecks := append(append([]AuditCheck(nil), checks...), result)
	if err := reconcileRunIncidentsTx(ctx, tx, run.AuditRunID, allChecks, now); err != nil {
		return result, errors.Join(cause, err)
	}
	if err := upsertCheckWith(ctx, tx, result); err != nil {
		return result, errors.Join(cause, err)
	}
	if err := transitionRunWith(ctx, tx, candidate); err != nil {
		return result, errors.Join(cause, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return result, errors.Join(cause, err)
	}
	*run = candidate
	if cause == nil {
		cause = errors.New(detail)
	}
	return result, cause
}

// reconcileRunIncidentsTx opens/updates one incident per failing check and
// closes one only from later fresh passing evidence for the exact identity.
// It deliberately accepts the caller transaction so Stage 11 is atomic.
func reconcileRunIncidentsTx(ctx context.Context, tx pgx.Tx, auditRunID string, checks []AuditCheck, now time.Time) error {
	open, err := listOpenIncidentsWith(ctx, tx)
	if err != nil {
		return err
	}
	for _, check := range checks {
		if check.Status == CheckStatusFail {
			failureClass := FailureClass(check.Category)
			if _, known := failureClassDimensions[failureClass]; !known {
				failureClass = FailureClassDBIntegrityViolation
			}
			observedAt := now
			if check.ObservedAt != nil {
				observedAt = check.ObservedAt.UTC()
			}
			from := observedAt.Add(-stageTimeout(check.StageID))
			if check.StartedAt != nil {
				from = check.StartedAt.UTC()
			}
			if _, err := openOrUpdateIncidentTx(ctx, tx, IncidentFinding{
				Key: IncidentKey{
					InstallationID: check.InstallationID,
					SourceID:       check.SourceID,
					CapabilityID:   check.CapabilityID,
					FailureClass:   failureClass,
				},
				ObservedAt: observedAt, AuditRunID: auditRunID, CheckID: check.CheckID,
				IntervalFrom: from, IntervalTo: observedAt,
				RecoveryCriteria: "later fresh pass for the same check identity",
			}); err != nil {
				return err
			}
			continue
		}
		if check.Status != CheckStatusPass || check.ObservedAt == nil {
			continue
		}
		for _, incident := range open {
			if incident.InstallationID != check.InstallationID ||
				incident.SourceID != check.SourceID ||
				incident.CapabilityID != check.CapabilityID ||
				evidenceCheckID(incident.CheckEvidenceRef) != check.CheckID {
				continue
			}
			if _, err := recordRecoveryTx(ctx, tx, IncidentKey{
				InstallationID: incident.InstallationID,
				SourceID:       incident.SourceID,
				CapabilityID:   incident.CapabilityID,
				FailureClass:   incident.FailureClass,
			}, auditRunID, CheckOutcome{
				CheckID: check.CheckID, Status: CheckStatusPass,
				ObservedAt: check.ObservedAt.UTC(),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func evidenceCheckID(ref string) string {
	if index := strings.IndexByte(ref, ':'); index >= 0 {
		return ref[index+1:]
	}
	return ref
}
