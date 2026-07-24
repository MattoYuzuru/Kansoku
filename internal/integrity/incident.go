package integrity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kansoku.local/kansoku/internal/observability"
)

// ErrIncidentNotFound is returned when a caller asks for an incident row
// (base or detail) that does not exist.
var ErrIncidentNotFound = errors.New("integrity_incident_not_found")

// IncidentFinding is what one failing (or recovering) Check outcome reports
// to the incident lifecycle: everything OpenOrUpdateIncident/RecordRecovery
// need to dedup, extend, or close an incident, without this package
// re-deriving any of the per-check detection logic itself (a caller
// supplies Finding after a stage's own Check.Evaluate already ran).
type IncidentFinding struct {
	Key                   IncidentKey
	ObservedAt            time.Time
	AuditRunID            string
	CheckID               string
	AgentOrAdapterVersion string
	RecoveryCriteria      string
	// IntervalFrom/IntervalTo bound the metric/completeness interval this
	// finding's evidence implicates, matching
	// incident-and-health.yaml's affected_interval field. A caller typically
	// derives these via internal/dataplatform.BucketStart so the interval
	// aligns with rollup granularity; this package never computes bucket
	// boundaries itself.
	IntervalFrom time.Time
	IntervalTo   time.Time
}

// incidentKeyFingerprint derives a stable, deterministic incident_id from an
// IncidentKey's four components, matching incident-and-health.yaml's
// incident_key.components list exactly (installation_id, source_id,
// capability_id, failure_class). The same key always derives the same ID,
// which is what makes OpenOrUpdateIncident's "same key -> same row" dedup
// possible via a plain upsert rather than a read-then-decide race.
func incidentKeyFingerprint(key IncidentKey, salt string) string {
	hash := sha256.New()
	hash.Write([]byte("kansoku.integrity-incident/1"))
	for _, v := range []string{key.InstallationID, key.SourceID, key.CapabilityID, string(key.FailureClass), salt} {
		hash.Write([]byte{0})
		hash.Write([]byte(v))
	}
	return "inc_" + hex.EncodeToString(hash.Sum(nil))[:32]
}

// OpenOrUpdateIncident implements dedup_and_lifecycle_rules.dedup and
// first_last_seen: a new failing finding for a key with no currently-OPEN
// incident opens one; a finding for a key that already has an open incident
// increments OccurrenceCount and advances LastObserved/affected_interval.to
// (affected_interval.from never moves once set). It never creates a second
// open incident row for the same key: if the most recent incident for this
// key was already resolved, reopen_rule applies -- a genuinely new incident
// (new IncidentID, new first_seen_at) is opened instead of reusing or
// clearing the old row's ResolvedAt, so the closed incident's history stays
// intact.
//
// This is the ONLY place Session 08 writes an internal/observability.Incident
// shape: the base fields (IncidentID/Capability/Category/Completeness/
// OpenedAt/LastObserved/ResolvedAt/OccurrenceCount) are persisted into
// integrity_incidents, structurally identical to that type, and the
// session-08 extension fields into integrity_incident_details, keyed 1:1 by
// IncidentID -- never a second, competing incident struct.
func OpenOrUpdateIncident(ctx context.Context, pool *pgxpool.Pool, finding IncidentFinding) (IntegrityIncidentDetail, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return IntegrityIncidentDetail{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	detail, err := openOrUpdateIncidentTx(ctx, tx, finding)
	if err != nil {
		return IntegrityIncidentDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return IntegrityIncidentDetail{}, fmt.Errorf("commit: %w", err)
	}
	return detail, nil
}

func openOrUpdateIncidentTx(ctx context.Context, tx pgx.Tx, finding IncidentFinding) (IntegrityIncidentDetail, error) {
	if finding.Key.InstallationID == "" || finding.Key.CapabilityID == "" || finding.Key.FailureClass == "" {
		return IntegrityIncidentDetail{}, fmt.Errorf("incident finding is missing a required key component")
	}

	existing, err := latestIncidentForKey(ctx, tx, finding.Key)
	if err != nil && !errors.Is(err, ErrIncidentNotFound) {
		return IntegrityIncidentDetail{}, fmt.Errorf("lookup latest incident: %w", err)
	}

	var detail IntegrityIncidentDetail
	if errors.Is(err, ErrIncidentNotFound) || existing.ResolvedAt != nil {
		// No incident on record for this key yet, or the most recent one was
		// already resolved: open a genuinely new incident (reopen_rule),
		// never reusing or clearing the prior row's ResolvedAt.
		salt := ""
		if existing.ResolvedAt != nil {
			// Distinguish the reopened incident's ID from the resolved one so
			// both remain independently addressable rows, since incident_id is
			// this table's primary key and a plain re-derivation from the same
			// four key components would otherwise collide with the still-present
			// resolved row.
			salt = finding.ObservedAt.UTC().Format(time.RFC3339Nano)
		}
		incidentID := incidentKeyFingerprint(finding.Key, salt)
		detail = IntegrityIncidentDetail{
			IncidentID:            incidentID,
			InstallationID:        finding.Key.InstallationID,
			SourceID:              finding.Key.SourceID,
			CapabilityID:          finding.Key.CapabilityID,
			FailureClass:          finding.Key.FailureClass,
			FirstSeenAt:           finding.ObservedAt.UTC(),
			AffectedInterval:      AffectedInterval{From: finding.IntervalFrom.UTC(), To: finding.IntervalTo.UTC()},
			CheckEvidenceRef:      checkEvidenceRef(finding.AuditRunID, finding.CheckID),
			AgentOrAdapterVersion: finding.AgentOrAdapterVersion,
			RecoveryCriteria:      finding.RecoveryCriteria,
		}
		if err := insertIncidentBase(ctx, tx, detail, finding.ObservedAt); err != nil {
			return IntegrityIncidentDetail{}, fmt.Errorf("insert incident base: %w", err)
		}
		if err := insertIncidentDetail(ctx, tx, detail); err != nil {
			return IntegrityIncidentDetail{}, fmt.Errorf("insert incident detail: %w", err)
		}
	} else {
		// An open incident for this exact key already exists: dedup onto it
		// (increment OccurrenceCount, advance LastObserved/affected_interval.to,
		// never move affected_interval.from, never touch FirstSeenAt).
		detail = existing
		if finding.IntervalTo.After(detail.AffectedInterval.To) {
			detail.AffectedInterval.To = finding.IntervalTo.UTC()
		}
		if finding.AgentOrAdapterVersion != "" {
			detail.AgentOrAdapterVersion = finding.AgentOrAdapterVersion
		}
		detail.CheckEvidenceRef = checkEvidenceRef(finding.AuditRunID, finding.CheckID)
		if finding.RecoveryCriteria != "" {
			detail.RecoveryCriteria = finding.RecoveryCriteria
		}
		if err := bumpIncidentOccurrence(ctx, tx, detail.IncidentID, finding.ObservedAt); err != nil {
			return IntegrityIncidentDetail{}, fmt.Errorf("bump incident occurrence: %w", err)
		}
		if err := updateIncidentDetailOnFinding(ctx, tx, detail); err != nil {
			return IntegrityIncidentDetail{}, fmt.Errorf("update incident detail: %w", err)
		}
	}
	return detail, nil
}

// RecordRecovery implements dedup_and_lifecycle_rules.
// recovery_requires_fresh_positive_evidence and no_deletion_on_recovery: it
// closes the currently-open incident for key (if any) by setting
// ResolvedAt=observedAt, and NEVER deletes the row or clears
// OccurrenceCount/FirstSeenAt/affected_interval history. Callers must only
// invoke this with the actual CheckOutcome for the SAME incident key.
// RecordRecovery verifies status=pass, a non-zero observation newer than the
// affected interval, and a later audit_run before it can close anything.
// If no open incident
// exists for key, RecordRecovery is a safe no-op (recovering something that
// was never broken is not an error).
func RecordRecovery(ctx context.Context, pool *pgxpool.Pool, key IncidentKey, auditRunID string, outcome CheckOutcome) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	recovered, err := recordRecoveryTx(ctx, tx, key, auditRunID, outcome)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return recovered, nil
}

func recordRecoveryTx(ctx context.Context, tx pgx.Tx, key IncidentKey, auditRunID string, outcome CheckOutcome) (bool, error) {
	if outcome.Status != CheckStatusPass || outcome.ObservedAt.IsZero() {
		return false, errors.New("recovery requires an actual passing check outcome")
	}
	observedAt := outcome.ObservedAt.UTC()

	existing, err := latestIncidentForKey(ctx, tx, key)
	if errors.Is(err, ErrIncidentNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lookup latest incident: %w", err)
	}
	if existing.ResolvedAt != nil {
		// Already resolved: nothing to do, and definitely not an error (a
		// recovery check may legitimately keep passing on every subsequent
		// run after the incident closed).
		return false, nil
	}
	if !observedAt.After(existing.AffectedInterval.To) {
		return false, errors.New("recovery evidence is not newer than the affected interval")
	}
	lastObservedRunRef, err := lastObservedAuditRun(ctx, tx, existing.IncidentID)
	if err != nil {
		return false, fmt.Errorf("lookup last-observed run: %w", err)
	}
	if lastObservedRunRef != "" && lastObservedRunRef == auditRunID {
		// The most recent evidence for this incident came from the SAME
		// audit_run as this recovery attempt: that is not a later, fresh run,
		// so refuse to close it here. This is the concrete guard behind
		// "recovery requires a check for the SAME incident_key to run again in
		// a later audit_run, not the same one that raised it".
		return false, fmt.Errorf("refusing to recover incident %s: recovery evidence must come from a later audit_run than %s", existing.IncidentID, auditRunID)
	}
	if err := resolveIncident(ctx, tx, existing.IncidentID, observedAt); err != nil {
		return false, fmt.Errorf("resolve incident: %w", err)
	}
	return true, nil
}

func checkEvidenceRef(auditRunID, checkID string) string {
	return auditRunID + ":" + checkID
}

func latestIncidentForKey(ctx context.Context, tx pgx.Tx, key IncidentKey) (IntegrityIncidentDetail, error) {
	row := tx.QueryRow(ctx, `
		SELECT d.incident_id, d.installation_id, d.source_id, d.capability_id, d.failure_class,
		       d.first_seen_at, d.affected_interval_from, d.affected_interval_to, d.check_evidence_ref,
		       COALESCE(d.agent_or_adapter_version, ''), COALESCE(d.recovery_criteria, ''), COALESCE(d.user_notes, ''),
		       i.resolved_at
		FROM integrity_incident_details d
		JOIN integrity_incidents i ON i.incident_id = d.incident_id
		WHERE d.installation_id = $1 AND d.source_id = $2 AND d.capability_id = $3 AND d.failure_class = $4
		ORDER BY d.first_seen_at DESC
		LIMIT 1
	`, key.InstallationID, key.SourceID, key.CapabilityID, string(key.FailureClass))
	var detail IntegrityIncidentDetail
	var failureClass string
	if err := row.Scan(&detail.IncidentID, &detail.InstallationID, &detail.SourceID, &detail.CapabilityID, &failureClass,
		&detail.FirstSeenAt, &detail.AffectedInterval.From, &detail.AffectedInterval.To, &detail.CheckEvidenceRef,
		&detail.AgentOrAdapterVersion, &detail.RecoveryCriteria, &detail.UserNotes, &detail.ResolvedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IntegrityIncidentDetail{}, ErrIncidentNotFound
		}
		return IntegrityIncidentDetail{}, err
	}
	detail.FailureClass = FailureClass(failureClass)
	return detail, nil
}

func insertIncidentBase(ctx context.Context, tx pgx.Tx, detail IntegrityIncidentDetail, observedAt time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO integrity_incidents (incident_id, capability, category, completeness, opened_at, last_observed_at, resolved_at, occurrence_count, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULL, 1, now())
	`, detail.IncidentID, detail.CapabilityID, string(detail.FailureClass), "degraded", detail.FirstSeenAt, observedAt.UTC())
	return err
}

func insertIncidentDetail(ctx context.Context, tx pgx.Tx, detail IntegrityIncidentDetail) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO integrity_incident_details (incident_id, installation_id, source_id, capability_id, failure_class, first_seen_at, affected_interval_from, affected_interval_to, check_evidence_ref, agent_or_adapter_version, recovery_criteria, user_notes, resolved_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''), NULL, now())
	`, detail.IncidentID, detail.InstallationID, detail.SourceID, detail.CapabilityID, string(detail.FailureClass),
		detail.FirstSeenAt, detail.AffectedInterval.From, detail.AffectedInterval.To, detail.CheckEvidenceRef,
		detail.AgentOrAdapterVersion, detail.RecoveryCriteria, detail.UserNotes)
	return err
}

func bumpIncidentOccurrence(ctx context.Context, tx pgx.Tx, incidentID string, observedAt time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE integrity_incidents
		SET occurrence_count = occurrence_count + 1, last_observed_at = $2, updated_at = now()
		WHERE incident_id = $1 AND resolved_at IS NULL
	`, incidentID, observedAt.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrIncidentNotFound
	}
	return nil
}

func updateIncidentDetailOnFinding(ctx context.Context, tx pgx.Tx, detail IntegrityIncidentDetail) error {
	_, err := tx.Exec(ctx, `
		UPDATE integrity_incident_details
		SET affected_interval_to = $2, check_evidence_ref = $3,
		    agent_or_adapter_version = NULLIF($4, ''), recovery_criteria = COALESCE(NULLIF($5, ''), recovery_criteria),
		    updated_at = now()
		WHERE incident_id = $1
	`, detail.IncidentID, detail.AffectedInterval.To, detail.CheckEvidenceRef, detail.AgentOrAdapterVersion, detail.RecoveryCriteria)
	return err
}

func resolveIncident(ctx context.Context, tx pgx.Tx, incidentID string, resolvedAt time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE integrity_incidents SET resolved_at = $2, updated_at = now() WHERE incident_id = $1`, incidentID, resolvedAt.UTC()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE integrity_incident_details SET resolved_at = $2, updated_at = now() WHERE incident_id = $1`, incidentID, resolvedAt.UTC()); err != nil {
		return err
	}
	return nil
}

// lastObservedAuditRun returns the audit_run_id embedded in the incident's
// current check_evidence_ref (format "audit_run_id:check_id", matching
// checkEvidenceRef), so RecordRecovery can refuse to close an incident whose
// most recent evidence came from the very same run as the recovery attempt.
func lastObservedAuditRun(ctx context.Context, tx pgx.Tx, incidentID string) (string, error) {
	row := tx.QueryRow(ctx, `SELECT check_evidence_ref FROM integrity_incident_details WHERE incident_id = $1`, incidentID)
	var ref string
	if err := row.Scan(&ref); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	for i := 0; i < len(ref); i++ {
		if ref[i] == ':' {
			return ref[:i], nil
		}
	}
	return ref, nil
}

// GetIncidentDetail returns the current IntegrityIncidentDetail for
// incidentID (open or resolved), or ErrIncidentNotFound.
func GetIncidentDetail(ctx context.Context, pool *pgxpool.Pool, incidentID string) (IntegrityIncidentDetail, error) {
	row := pool.QueryRow(ctx, `
		SELECT d.incident_id, d.installation_id, d.source_id, d.capability_id, d.failure_class,
		       d.first_seen_at, d.affected_interval_from, d.affected_interval_to, d.check_evidence_ref,
		       COALESCE(d.agent_or_adapter_version, ''), COALESCE(d.recovery_criteria, ''), COALESCE(d.user_notes, ''),
		       i.resolved_at
		FROM integrity_incident_details d
		JOIN integrity_incidents i ON i.incident_id = d.incident_id
		WHERE d.incident_id = $1
	`, incidentID)
	var detail IntegrityIncidentDetail
	var failureClass string
	if err := row.Scan(&detail.IncidentID, &detail.InstallationID, &detail.SourceID, &detail.CapabilityID, &failureClass,
		&detail.FirstSeenAt, &detail.AffectedInterval.From, &detail.AffectedInterval.To, &detail.CheckEvidenceRef,
		&detail.AgentOrAdapterVersion, &detail.RecoveryCriteria, &detail.UserNotes, &detail.ResolvedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IntegrityIncidentDetail{}, ErrIncidentNotFound
		}
		return IntegrityIncidentDetail{}, err
	}
	detail.FailureClass = FailureClass(failureClass)
	return detail, nil
}

// GetIncidentBase returns the base internal/observability.Incident-shaped
// row for incidentID (structurally identical fields, this package's own
// Postgres-backed copy), or ErrIncidentNotFound.
func GetIncidentBase(ctx context.Context, pool *pgxpool.Pool, incidentID string) (IncidentBase, error) {
	row := pool.QueryRow(ctx, `
		SELECT incident_id, capability, category, completeness, opened_at, last_observed_at, resolved_at, occurrence_count
		FROM integrity_incidents WHERE incident_id = $1
	`, incidentID)
	var base IncidentBase
	if err := row.Scan(&base.IncidentID, &base.Capability, &base.Category, &base.Completeness, &base.OpenedAt, &base.LastObserved, &base.ResolvedAt, &base.OccurrenceCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IncidentBase{}, ErrIncidentNotFound
		}
		return IncidentBase{}, err
	}
	return base, nil
}

// ListOpenIncidents returns every currently-unresolved incident's detail
// row, used by the Health API (a later stage) to derive red-tier health
// dimensions from actually-open incidents rather than re-scanning check
// history.
func ListOpenIncidents(ctx context.Context, pool *pgxpool.Pool) ([]IntegrityIncidentDetail, error) {
	return listOpenIncidentsWith(ctx, pool)
}

type incidentQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func listOpenIncidentsWith(ctx context.Context, querier incidentQuerier) ([]IntegrityIncidentDetail, error) {
	rows, err := querier.Query(ctx, `
		SELECT d.incident_id, d.installation_id, d.source_id, d.capability_id, d.failure_class,
		       d.first_seen_at, d.affected_interval_from, d.affected_interval_to, d.check_evidence_ref,
		       COALESCE(d.agent_or_adapter_version, ''), COALESCE(d.recovery_criteria, ''), COALESCE(d.user_notes, '')
		FROM integrity_incident_details d
		JOIN integrity_incidents i ON i.incident_id = d.incident_id
		WHERE i.resolved_at IS NULL
		ORDER BY d.first_seen_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list open incidents: %w", err)
	}
	defer rows.Close()
	var out []IntegrityIncidentDetail
	for rows.Next() {
		var detail IntegrityIncidentDetail
		var failureClass string
		if err := rows.Scan(&detail.IncidentID, &detail.InstallationID, &detail.SourceID, &detail.CapabilityID, &failureClass,
			&detail.FirstSeenAt, &detail.AffectedInterval.From, &detail.AffectedInterval.To, &detail.CheckEvidenceRef,
			&detail.AgentOrAdapterVersion, &detail.RecoveryCriteria, &detail.UserNotes); err != nil {
			return nil, err
		}
		detail.FailureClass = FailureClass(failureClass)
		out = append(out, detail)
	}
	return out, rows.Err()
}

// IncidentBase is an alias, not a second incident concept. PostgreSQL stores
// the audit projection while callers and the rest of the repository use the
// one canonical observability.Incident type.
type IncidentBase = observability.Incident
