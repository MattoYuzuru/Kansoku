package integrity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const IncidentWorkbenchAuditCheckID = "stage_7_incident_workbench"

// IncidentWorkbenchAuditCheck makes the Session 12 storage invariants part
// of the ordinary daily audit. It reads only bounded aggregate counts and
// never loads occurrence evidence or quarantine values.
type IncidentWorkbenchAuditCheck struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

var _ Check = (*IncidentWorkbenchAuditCheck)(nil)

func NewIncidentWorkbenchAuditCheck(pool *pgxpool.Pool) *IncidentWorkbenchAuditCheck {
	return &IncidentWorkbenchAuditCheck{pool: pool, now: time.Now}
}

func (c *IncidentWorkbenchAuditCheck) StageID() StageID {
	return Stage7UnknownSchemaAndLag
}

func (c *IncidentWorkbenchAuditCheck) CheckID() string {
	return IncidentWorkbenchAuditCheckID
}

func (c *IncidentWorkbenchAuditCheck) Targets(
	_ context.Context,
	_ CheckInput,
) ([]CheckTarget, error) {
	if c == nil || c.pool == nil {
		return nil, errors.New("incident_workbench_audit_pool_required")
	}
	return []CheckTarget{{
		CapabilityID:   "core_ingestion",
		InstallationID: "not_observed",
		SourceID:       "incident_workbench",
	}}, nil
}

func (c *IncidentWorkbenchAuditCheck) Evaluate(
	ctx context.Context,
	in CheckInput,
	_ CheckTarget,
) (CheckOutcome, error) {
	now := c.now().UTC()
	if !in.Now.IsZero() {
		now = in.Now.UTC()
	}
	var orphans, countMismatches, invalidManifests, staleOpen, invalidRecovery int64
	err := c.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM incident_occurrences o
			 WHERE NOT EXISTS (SELECT 1 FROM incidents i WHERE i.incident_id=o.incident_id)
			   AND NOT EXISTS (SELECT 1 FROM integrity_incidents i WHERE i.incident_id=o.incident_id)),
			(SELECT count(*) FROM incidents i
			 WHERE NOT EXISTS (
			    SELECT 1 FROM incident_occurrences o
			    WHERE o.incident_id=i.incident_id AND o.idempotency_key LIKE 'legacy:%'
			 )
			   AND i.occurrence_count <> i.occurrence_retention_excluded_count + (
			       SELECT count(*) FROM incident_occurrences o WHERE o.incident_id=i.incident_id
			   )),
			(SELECT count(*) FROM quarantine_structural_manifests
			 WHERE schema_fingerprint='' OR classification='' OR rejection_reason=''),
			(SELECT count(*) FROM incidents
			 WHERE detector_state IN ('open','recovering') AND last_seen_at < $1::timestamptz - interval '30 days'),
			(SELECT count(*) FROM incidents i
			 WHERE i.detector_state='resolved' AND i.recovery_audit_run_id IS NOT NULL
			   AND i.recovery_observed_at > $1::timestamptz - interval '30 days'
			   AND (
			       i.recovery_observed_at IS NULL OR i.recovery_evidence_ref IS NULL OR
			       NOT EXISTS (
			           SELECT 1 FROM events e
			           JOIN event_evidence ev ON ev.event_id=e.event_id
			           WHERE e.event_id=i.recovery_evidence_ref
			             AND e.observed_at=i.recovery_observed_at
			             AND e.event_type='source.observed'
			             AND e.value_state='observed'
			       ) OR NOT EXISTS (
			           SELECT 1 FROM integrity_audit_checks c
			           WHERE c.audit_run_id=i.recovery_audit_run_id
			             AND c.capability_id=i.capability_id
			             AND c.status='pass'
			             AND c.observed_at > i.recovery_observed_at
			       )
			   ))
	`, now).Scan(
		&orphans, &countMismatches, &invalidManifests, &staleOpen, &invalidRecovery,
	)
	if err != nil {
		return CheckOutcome{}, err
	}
	detail := fmt.Sprintf(
		"orphans=%d count_mismatches=%d invalid_manifests=%d stale_open=%d invalid_recovery=%d legacy_occurrence_reconstruction=excluded",
		orphans, countMismatches, invalidManifests, staleOpen, invalidRecovery,
	)
	if orphans+countMismatches+invalidManifests+invalidRecovery > 0 {
		return CheckOutcome{
			CheckID: IncidentWorkbenchAuditCheckID, Status: CheckStatusFail,
			Category:  string(FailureClassDBIntegrityViolation),
			DetailRef: detail, ObservedAt: now,
		}, nil
	}
	if staleOpen > 0 {
		return CheckOutcome{
			CheckID: IncidentWorkbenchAuditCheckID, Status: CheckStatusFail,
			Category:  string(FailureClassUnknownSchema),
			DetailRef: detail, ObservedAt: now,
		}, nil
	}
	return CheckOutcome{
		CheckID: IncidentWorkbenchAuditCheckID, Status: CheckStatusPass,
		DetailRef: detail, ObservedAt: now,
	}, nil
}

var validTriageStates = map[string]bool{
	"new": true, "acknowledged": true, "investigating": true, "action_ready": true,
}

var validTriageNoteCategories = map[string]bool{
	"": true, "fixture_needed": true, "parser_fix_prepared": true,
	"source_owner_contacted": true, "recovery_pending": true,
}

// SetIncidentTriage changes only operator workflow metadata. Detector state,
// resolution timestamps and occurrence evidence are deliberately absent from
// its arguments and UPDATE sets.
func SetIncidentTriage(
	ctx context.Context,
	pool *pgxpool.Pool,
	incidentID, state, noteCategory string,
) error {
	if pool == nil || incidentID == "" || !validTriageStates[state] ||
		!validTriageNoteCategories[noteCategory] {
		return errors.New("invalid_incident_triage")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE integrity_incidents
		SET triage_state=$2, triage_note_category=NULLIF($3,''), updated_at=now()
		WHERE incident_id=$1
	`, incidentID, state, noteCategory)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		tag, err = tx.Exec(ctx, `
			UPDATE incidents
			SET triage_state=$2, triage_note_category=NULLIF($3,''), updated_at=now()
			WHERE incident_id=$1
		`, incidentID, state, noteCategory)
		if err != nil {
			return err
		}
	}
	if tag.RowsAffected() != 1 {
		return ErrIncidentNotFound
	}
	return tx.Commit(ctx)
}

// RecordIngressRecovery resolves an ingress unknown-schema incident only
// after a later supported observation and a later passing targeted audit.
// Parser deployment, triage, or the absence of another failure cannot call
// this successfully.
func RecordIngressRecovery(
	ctx context.Context,
	pool *pgxpool.Pool,
	incidentID, auditRunID, supportedEventID string,
	supportedObservedAt time.Time,
) (bool, error) {
	if pool == nil || incidentID == "" || auditRunID == "" ||
		supportedEventID == "" || supportedObservedAt.IsZero() {
		return false, errors.New("invalid_ingress_recovery")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var lastSeen time.Time
	var resolvedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT last_seen_at, resolved_at FROM incidents
		WHERE incident_id=$1 FOR UPDATE
	`, incidentID).Scan(&lastSeen, &resolvedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrIncidentNotFound
		}
		return false, err
	}
	if resolvedAt != nil {
		return false, nil
	}
	supportedObservedAt = supportedObservedAt.UTC()
	if !supportedObservedAt.After(lastSeen) {
		return false, errors.New("recovery_evidence_not_fresh")
	}
	var supported bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM events e
			JOIN event_evidence ev ON ev.event_id=e.event_id
			WHERE e.event_id=$1 AND e.observed_at=$2
			  AND e.event_type='source.observed' AND e.value_state='observed'
		)
	`, supportedEventID, supportedObservedAt).Scan(&supported); err != nil {
		return false, err
	}
	if !supported {
		return false, errors.New("fresh_supported_evidence_required")
	}
	var passed bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM integrity_audit_runs r
			JOIN integrity_audit_checks c ON c.audit_run_id=r.audit_run_id
			WHERE r.audit_run_id=$1
			  AND r.state IN ('passed','degraded')
			  AND c.status='pass'
			  AND c.capability_id='core_ingestion'
			  AND c.observed_at > $2
		)
	`, auditRunID, supportedObservedAt).Scan(&passed); err != nil {
		return false, err
	}
	if !passed {
		return false, errors.New("fresh_targeted_audit_required")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE incidents
		SET detector_state='resolved', resolved_at=$2, recovery_observed_at=$2,
		    recovery_audit_run_id=$3, recovery_evidence_ref=$4, updated_at=now()
		WHERE incident_id=$1 AND resolved_at IS NULL
	`, incidentID, supportedObservedAt, auditRunID, supportedEventID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE quarantine_structural_manifests
		SET disposition='supported', updated_at=now()
		WHERE incident_id=$1
	`, incidentID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
