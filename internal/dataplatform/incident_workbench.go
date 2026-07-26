package dataplatform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	IncidentListFormulaVersion       = "reliability.incident_list/1"
	IncidentOccurrenceFormulaVersion = "reliability.incident_occurrences/1"
	QuarantineListFormulaVersion     = "reliability.quarantine_manifest/1"
)

type ExplicitValue struct {
	State string  `json:"state"`
	Value *string `json:"value"`
}

type IncidentRecord struct {
	IncidentID           string        `json:"incident_id"`
	DetectorState        string        `json:"detector_state"`
	TriageState          string        `json:"triage_state"`
	TriageNoteCategory   *string       `json:"triage_note_category"`
	Installation         ExplicitValue `json:"installation"`
	Source               ExplicitValue `json:"source"`
	CapabilityID         string        `json:"capability_id"`
	FailureClass         string        `json:"failure_class"`
	Severity             string        `json:"severity"`
	FirstSeenAt          time.Time     `json:"first_seen_at"`
	LastSeenAt           time.Time     `json:"last_seen_at"`
	ResolvedAt           *time.Time    `json:"resolved_at"`
	OccurrenceCount      int64         `json:"occurrence_count"`
	RetentionExclusions  int64         `json:"occurrence_retention_excluded_count"`
	AffectedIntervalFrom time.Time     `json:"affected_interval_from"`
	AffectedIntervalTo   time.Time     `json:"affected_interval_to"`
	AdapterVersion       *string       `json:"adapter_version"`
	SchemaFingerprint    *string       `json:"schema_fingerprint"`
	SourceSchemaVersion  *string       `json:"source_schema_version"`
	ParserVersion        *string       `json:"parser_version"`
	RecoveryCriteria     string        `json:"recovery_criteria"`
	RecoveryObservedAt   *time.Time    `json:"recovery_observed_at"`
	RecoveryAuditRunID   *string       `json:"recovery_audit_run_id"`
	RecoveryEvidenceRef  *string       `json:"recovery_evidence_ref"`
	EvidenceRef          string        `json:"evidence_ref"`
	Projection           string        `json:"projection"`
}

type IncidentFilter struct {
	DetectorState string
	TriageState   string
	Adapter       string
	Source        string
	Capability    string
	FailureClass  string
	From          *time.Time
	To            *time.Time
}

type IncidentPagePosition struct {
	LastSeenAt time.Time
	IncidentID string
}

type IncidentPage struct {
	Data            []IncidentRecord `json:"data"`
	HasMore         bool             `json:"has_more"`
	NextCursor      string           `json:"next_cursor,omitempty"`
	TotalState      string           `json:"total_state"`
	TotalLowerBound int              `json:"total_lower_bound"`
	FormulaVersion  string           `json:"formula_version"`
	Exclusions      []string         `json:"exclusions"`
	Completeness    string           `json:"completeness"`
}

const unifiedIncidentCTE = `
	WITH unified_incidents AS (
		SELECT
			i.incident_id, i.detector_state, i.triage_state, i.triage_note_category,
			NULLIF(d.installation_id, '') AS installation_id,
			CASE WHEN d.installation_id = '' THEN 'unknown' ELSE 'observed' END AS installation_value_state,
			NULLIF(d.source_id, '') AS source_id,
			CASE WHEN d.source_id = '' THEN 'unknown' ELSE 'observed' END AS source_value_state,
			d.capability_id, d.failure_class, 'error'::text AS severity,
			d.first_seen_at, i.last_observed_at AS last_seen_at, i.resolved_at,
			i.occurrence_count, 0::bigint AS occurrence_retention_excluded_count,
			d.affected_interval_from, d.affected_interval_to,
			NULLIF(d.agent_or_adapter_version, '') AS adapter_version,
			NULL::text AS schema_fingerprint, NULL::text AS source_schema_version,
			NULL::text AS parser_version, COALESCE(d.recovery_criteria, '') AS recovery_criteria,
			NULL::timestamptz AS recovery_observed_at, NULL::text AS recovery_audit_run_id,
			NULL::text AS recovery_evidence_ref,
			d.check_evidence_ref AS evidence_ref, 'integrity'::text AS projection
		FROM integrity_incidents i
		JOIN integrity_incident_details d ON d.incident_id = i.incident_id
		UNION ALL
		SELECT
			i.incident_id, i.detector_state, i.triage_state, i.triage_note_category,
			i.installation_id, i.installation_value_state, i.source_id,
			i.source_value_state, i.capability_id, i.category, i.severity,
			i.opened_at, i.last_seen_at, i.resolved_at, i.occurrence_count,
			i.occurrence_retention_excluded_count,
			i.opened_at, i.last_seen_at, i.adapter_version, i.schema_fingerprint,
			i.source_schema_version, i.parser_version, i.recovery_criteria,
			i.recovery_observed_at, i.recovery_audit_run_id, i.recovery_evidence_ref,
			'ingress:' || i.incident_id, 'ingress'::text
		FROM incidents i
	)`

func ListIncidents(
	ctx context.Context,
	pool *pgxpool.Pool,
	filter IncidentFilter,
	position *IncidentPagePosition,
	limit int,
) ([]IncidentRecord, bool, error) {
	if pool == nil || limit < 1 || limit > 100 {
		return nil, false, errors.New("invalid_incident_query")
	}
	budget := Budgets["incident_list"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return nil, false, err
	}
	defer release()
	started := time.Now()
	var cursorTime *time.Time
	var cursorID string
	if position != nil {
		value := position.LastSeenAt.UTC()
		cursorTime = &value
		cursorID = position.IncidentID
	}
	rows, err := conn.Query(ctx, unifiedIncidentCTE+`
		SELECT incident_id, detector_state, triage_state, triage_note_category,
		       installation_id, installation_value_state, source_id, source_value_state,
		       capability_id, failure_class, severity, first_seen_at, last_seen_at,
		       resolved_at, occurrence_count, occurrence_retention_excluded_count,
		       affected_interval_from, affected_interval_to,
		       adapter_version, schema_fingerprint, source_schema_version, parser_version,
		       recovery_criteria, recovery_observed_at, recovery_audit_run_id,
		       recovery_evidence_ref,
		       evidence_ref, projection
		FROM unified_incidents
		WHERE ($1 = '' OR detector_state = $1)
		  AND ($2 = '' OR triage_state = $2)
		  AND ($3 = '' OR adapter_version = $3)
		  AND ($4 = '' OR source_id = $4)
		  AND ($5 = '' OR capability_id = $5)
		  AND ($6 = '' OR failure_class = $6)
		  AND ($7::timestamptz IS NULL OR last_seen_at >= $7)
		  AND ($8::timestamptz IS NULL OR last_seen_at < $8)
		  AND ($9::timestamptz IS NULL OR (last_seen_at, incident_id) < ($9, $10))
		ORDER BY last_seen_at DESC, incident_id DESC
		LIMIT $11
	`, filter.DetectorState, filter.TriageState, filter.Adapter, filter.Source,
		filter.Capability, filter.FailureClass, filter.From, filter.To,
		cursorTime, cursorID, limit+1)
	if err != nil {
		return nil, false, budgetOrErr(budget, started, err)
	}
	defer rows.Close()
	out := make([]IncidentRecord, 0, limit+1)
	for rows.Next() {
		record, err := scanIncident(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, false, budgetOrErr(budget, started, err)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, budgetOrErr(budget, started, nil)
}

func GetIncident(ctx context.Context, pool *pgxpool.Pool, incidentID string) (IncidentRecord, error) {
	if pool == nil || incidentID == "" {
		return IncidentRecord{}, errors.New("invalid_incident_query")
	}
	budget := Budgets["incident_detail"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return IncidentRecord{}, err
	}
	defer release()
	started := time.Now()
	row := conn.QueryRow(ctx, unifiedIncidentCTE+`
		SELECT incident_id, detector_state, triage_state, triage_note_category,
		       installation_id, installation_value_state, source_id, source_value_state,
		       capability_id, failure_class, severity, first_seen_at, last_seen_at,
		       resolved_at, occurrence_count, occurrence_retention_excluded_count,
		       affected_interval_from, affected_interval_to,
		       adapter_version, schema_fingerprint, source_schema_version, parser_version,
		       recovery_criteria, recovery_observed_at, recovery_audit_run_id,
		       recovery_evidence_ref,
		       evidence_ref, projection
		FROM unified_incidents WHERE incident_id = $1
	`, incidentID)
	record, err := scanIncident(row)
	if err != nil {
		return IncidentRecord{}, budgetOrErr(budget, started, err)
	}
	return record, budgetOrErr(budget, started, nil)
}

type incidentScanner interface {
	Scan(dest ...any) error
}

func scanIncident(scanner incidentScanner) (IncidentRecord, error) {
	var record IncidentRecord
	var installationID, sourceID *string
	if err := scanner.Scan(
		&record.IncidentID, &record.DetectorState, &record.TriageState,
		&record.TriageNoteCategory, &installationID, &record.Installation.State,
		&sourceID, &record.Source.State, &record.CapabilityID, &record.FailureClass,
		&record.Severity, &record.FirstSeenAt, &record.LastSeenAt, &record.ResolvedAt,
		&record.OccurrenceCount, &record.RetentionExclusions,
		&record.AffectedIntervalFrom, &record.AffectedIntervalTo,
		&record.AdapterVersion, &record.SchemaFingerprint, &record.SourceSchemaVersion,
		&record.ParserVersion, &record.RecoveryCriteria, &record.RecoveryObservedAt,
		&record.RecoveryAuditRunID, &record.RecoveryEvidenceRef,
		&record.EvidenceRef, &record.Projection,
	); err != nil {
		return IncidentRecord{}, err
	}
	record.Installation.Value = installationID
	record.Source.Value = sourceID
	return record, nil
}

type IncidentOccurrence struct {
	OccurrenceID      string    `json:"occurrence_id"`
	IncidentID        string    `json:"incident_id"`
	ObservedAt        time.Time `json:"observed_at"`
	EvidenceRef       string    `json:"evidence_ref"`
	SchemaFingerprint *string   `json:"schema_fingerprint"`
	SafeErrorClass    string    `json:"safe_error_class"`
	RecordCount       int64     `json:"record_count"`
	ByteCount         int64     `json:"byte_count"`
}

type OccurrencePagePosition struct {
	ObservedAt   time.Time
	OccurrenceID string
}

func ListIncidentOccurrences(
	ctx context.Context,
	pool *pgxpool.Pool,
	incidentID string,
	position *OccurrencePagePosition,
	limit int,
) ([]IncidentOccurrence, bool, error) {
	if pool == nil || incidentID == "" || limit < 1 || limit > 100 {
		return nil, false, errors.New("invalid_occurrence_query")
	}
	budget := Budgets["incident_occurrences"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return nil, false, err
	}
	defer release()
	started := time.Now()
	var cursorTime *time.Time
	var cursorID string
	if position != nil {
		value := position.ObservedAt.UTC()
		cursorTime, cursorID = &value, position.OccurrenceID
	}
	rows, err := conn.Query(ctx, `
		SELECT incident_occurrence_id, incident_id, observed_at, evidence_ref,
		       schema_fingerprint, safe_error_class, record_count, byte_count
		FROM incident_occurrences
		WHERE incident_id = $1
		  AND ($2::timestamptz IS NULL OR (observed_at, incident_occurrence_id) < ($2, $3))
		ORDER BY observed_at DESC, incident_occurrence_id DESC
		LIMIT $4
	`, incidentID, cursorTime, cursorID, limit+1)
	if err != nil {
		return nil, false, budgetOrErr(budget, started, err)
	}
	defer rows.Close()
	out := make([]IncidentOccurrence, 0, limit+1)
	for rows.Next() {
		var item IncidentOccurrence
		if err := rows.Scan(
			&item.OccurrenceID, &item.IncidentID, &item.ObservedAt, &item.EvidenceRef,
			&item.SchemaFingerprint, &item.SafeErrorClass, &item.RecordCount, &item.ByteCount,
		); err != nil {
			return nil, false, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, budgetOrErr(budget, started, err)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, budgetOrErr(budget, started, nil)
}

type QuarantineManifest struct {
	QuarantineID         string        `json:"quarantine_id"`
	IncidentID           string        `json:"incident_id"`
	SourceKind           string        `json:"source_kind"`
	SourceInstance       ExplicitValue `json:"source_instance"`
	SignalKind           string        `json:"signal_kind"`
	EventType            ExplicitValue `json:"event_type"`
	StructuralFieldPaths []string      `json:"structural_field_paths"`
	PrimitiveTypes       []string      `json:"primitive_types"`
	ShapeValueState      string        `json:"shape_value_state"`
	SchemaFingerprint    string        `json:"schema_fingerprint"`
	AdapterVersion       *string       `json:"adapter_version"`
	SourceSchemaVersion  *string       `json:"source_schema_version"`
	ParserVersion        *string       `json:"parser_version"`
	Classification       string        `json:"classification"`
	RejectionReason      string        `json:"rejection_reason"`
	FirstSeenAt          time.Time     `json:"first_seen_at"`
	LastSeenAt           time.Time     `json:"last_seen_at"`
	OccurrenceCount      int64         `json:"occurrence_count"`
	TotalRecordCount     int64         `json:"total_record_count"`
	TotalByteCount       int64         `json:"total_byte_count"`
	Disposition          string        `json:"disposition"`
}

type QuarantinePagePosition struct {
	LastSeenAt   time.Time
	QuarantineID string
}

func ListQuarantine(
	ctx context.Context,
	pool *pgxpool.Pool,
	fingerprint, source string,
	from, to *time.Time,
	position *QuarantinePagePosition,
	limit int,
) ([]QuarantineManifest, bool, error) {
	if pool == nil || limit < 1 || limit > 100 {
		return nil, false, errors.New("invalid_quarantine_query")
	}
	budget := Budgets["quarantine_list"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return nil, false, err
	}
	defer release()
	started := time.Now()
	var cursorTime *time.Time
	var cursorID string
	if position != nil {
		value := position.LastSeenAt.UTC()
		cursorTime, cursorID = &value, position.QuarantineID
	}
	rows, err := conn.Query(ctx, `
		SELECT quarantine_id, incident_id, source_kind, source_instance_pseudonym,
		       source_instance_value_state, signal_kind, safe_event_type,
		       event_type_value_state, structural_field_paths, primitive_types,
		       shape_value_state, schema_fingerprint, adapter_version,
		       source_schema_version, parser_version, classification, rejection_reason,
		       first_seen_at, last_seen_at, occurrence_count, total_record_count,
		       total_byte_count, disposition
		FROM quarantine_structural_manifests
		WHERE ($1 = '' OR schema_fingerprint = $1)
		  AND ($2 = '' OR source_kind = $2)
		  AND ($3::timestamptz IS NULL OR last_seen_at >= $3)
		  AND ($4::timestamptz IS NULL OR last_seen_at < $4)
		  AND ($5::timestamptz IS NULL OR (last_seen_at, quarantine_id) < ($5, $6))
		ORDER BY last_seen_at DESC, quarantine_id DESC
		LIMIT $7
	`, fingerprint, source, from, to, cursorTime, cursorID, limit+1)
	if err != nil {
		return nil, false, budgetOrErr(budget, started, err)
	}
	defer rows.Close()
	out := make([]QuarantineManifest, 0, limit+1)
	for rows.Next() {
		item, err := scanQuarantine(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, budgetOrErr(budget, started, err)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, budgetOrErr(budget, started, nil)
}

func GetQuarantine(ctx context.Context, pool *pgxpool.Pool, quarantineID string) (QuarantineManifest, error) {
	if pool == nil || quarantineID == "" {
		return QuarantineManifest{}, errors.New("invalid_quarantine_query")
	}
	budget := Budgets["quarantine_detail"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return QuarantineManifest{}, err
	}
	defer release()
	started := time.Now()
	row := conn.QueryRow(ctx, `
		SELECT quarantine_id, incident_id, source_kind, source_instance_pseudonym,
		       source_instance_value_state, signal_kind, safe_event_type,
		       event_type_value_state, structural_field_paths, primitive_types,
		       shape_value_state, schema_fingerprint, adapter_version,
		       source_schema_version, parser_version, classification, rejection_reason,
		       first_seen_at, last_seen_at, occurrence_count, total_record_count,
		       total_byte_count, disposition
		FROM quarantine_structural_manifests WHERE quarantine_id = $1
	`, quarantineID)
	manifest, err := scanQuarantine(row)
	if err != nil {
		return QuarantineManifest{}, budgetOrErr(budget, started, err)
	}
	return manifest, budgetOrErr(budget, started, nil)
}

func scanQuarantine(scanner incidentScanner) (QuarantineManifest, error) {
	var item QuarantineManifest
	var sourceInstance, eventType *string
	if err := scanner.Scan(
		&item.QuarantineID, &item.IncidentID, &item.SourceKind, &sourceInstance,
		&item.SourceInstance.State, &item.SignalKind, &eventType, &item.EventType.State,
		&item.StructuralFieldPaths, &item.PrimitiveTypes, &item.ShapeValueState,
		&item.SchemaFingerprint, &item.AdapterVersion, &item.SourceSchemaVersion,
		&item.ParserVersion, &item.Classification, &item.RejectionReason,
		&item.FirstSeenAt, &item.LastSeenAt, &item.OccurrenceCount,
		&item.TotalRecordCount, &item.TotalByteCount, &item.Disposition,
	); err != nil {
		return QuarantineManifest{}, err
	}
	item.SourceInstance.Value = sourceInstance
	item.EventType.Value = eventType
	return item, nil
}

func IsNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
