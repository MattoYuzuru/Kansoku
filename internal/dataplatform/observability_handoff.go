package dataplatform

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

const defaultObservabilityHandoffTimeout = 15 * time.Second

// ObservabilityHandoff is the production normalized-ingress to PostgreSQL
// handoff. It consumes the same closed Event/Evidence pair committed by the
// public hook and OTLP ingress, rather than exposing a second audit-only
// insertion path.
type ObservabilityHandoff struct {
	pool    *pgxpool.Pool
	timeout time.Duration
}

var _ observability.DurableFactSink = (*ObservabilityHandoff)(nil)
var _ observability.DurableMetadataSink = (*ObservabilityHandoff)(nil)

func NewObservabilityHandoff(pool *pgxpool.Pool, timeout time.Duration) (*ObservabilityHandoff, error) {
	if pool == nil {
		return nil, errors.New("observability_handoff_pool_required")
	}
	if timeout <= 0 {
		timeout = defaultObservabilityHandoffTimeout
	}
	return &ObservabilityHandoff{pool: pool, timeout: timeout}, nil
}

func (h *ObservabilityHandoff) Pool() *pgxpool.Pool {
	if h == nil {
		return nil
	}
	return h.pool
}

// ObservabilityFactScope is the complete set of durable dimension
// identifiers derived from an already-sanitized event. Missing optional
// scopes are filled with stable, bounded identifiers based only on existing
// pseudonyms and closed catalog values.
type ObservabilityFactScope struct {
	DeviceID                string
	AgentInstallationID     string
	SurfaceID               string
	ProjectID               string
	SessionID               string
	TurnID                  string
	ComponentID             string
	ComponentInstallationID string
	AdapterVersionID        string
	SourceInstanceID        string
	DimensionScope          string
}

func handoffID(kind string, values ...string) string {
	hash := sha256.New()
	hash.Write([]byte("kansoku-observability-handoff/1"))
	hash.Write([]byte{0})
	hash.Write([]byte(kind))
	for _, value := range values {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	return "oh_" + hex.EncodeToString(hash.Sum(nil)[:16])
}

func firstOrStable(value, kind string, seeds ...string) string {
	if value != "" {
		return value
	}
	return handoffID(kind, seeds...)
}

func ObservabilityScope(event observability.Event) ObservabilityFactScope {
	installationID := firstOrStable(
		event.Scope.AgentInstallationID, "installation",
		event.Source.InstallationID, event.Source.AdapterID,
	)
	deviceID := firstOrStable(event.Scope.DeviceID, "device", installationID)
	surfaceID := firstOrStable(event.Scope.SurfaceID, "surface", installationID)
	sessionID := firstOrStable(
		event.Scope.SessionID, "session",
		installationID, event.Source.NativeEventID,
	)
	projectID := firstOrStable(event.Scope.ProjectID, "project", sessionID)
	turnID := event.Scope.TurnID
	if turnID == "" && eventCarriesTurn(event.EventType) {
		turnID = handoffID("turn", sessionID, event.EventID)
	}
	componentID := event.Subject.ComponentID
	adapterVersionID := handoffID(
		"adapter-version", event.Source.AdapterID, event.Source.AdapterVersion,
	)
	sourceInstanceID := handoffID(
		"source-instance", installationID, event.Source.AdapterID,
		event.Source.AdapterVersion, string(event.Source.Kind),
	)
	return ObservabilityFactScope{
		DeviceID:            deviceID,
		AgentInstallationID: installationID,
		SurfaceID:           surfaceID,
		ProjectID:           projectID,
		SessionID:           sessionID,
		TurnID:              turnID,
		ComponentID:         componentID,
		AdapterVersionID:    adapterVersionID,
		SourceInstanceID:    sourceInstanceID,
		DimensionScope: installationID + "|" + surfaceID + "|" +
			componentID + "|" + event.EventType,
	}
}

func eventCarriesTurn(eventType string) bool {
	switch eventType {
	case "prompt.submitted", "tool.called", "model.requested", "model.responded",
		"component.installed", "component.loaded", "component.invoked", "component.executed":
		return true
	default:
		return false
	}
}

func (h *ObservabilityHandoff) PersistNormalizedFact(event observability.Event, evidence observability.Evidence) error {
	if h == nil || h.pool == nil {
		return errors.New("observability_handoff_not_configured")
	}
	if evidence.EventID != event.EventID {
		return errors.New("observability_handoff_evidence_mismatch")
	}
	scope := ObservabilityScope(event)
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()
	var err error
	scope, err = h.resolveInventoryLifecycleComponent(ctx, event, scope)
	if err != nil {
		return err
	}
	if err := EnsureDimensions(ctx, h.pool, DimensionRefs{
		DeviceID:            scope.DeviceID,
		AgentInstallationID: scope.AgentInstallationID,
		AgentID:             event.Source.AdapterID,
		SurfaceID:           scope.SurfaceID,
		ProjectID:           scope.ProjectID,
		SessionID:           scope.SessionID,
		TurnID:              scope.TurnID,
		ComponentID:         scope.ComponentID,
		ComponentKind:       databaseComponentKind(event.Subject.Kind),
		ModelID:             event.Subject.ModelID,
		ProviderID:          providerForAdapter(event.Source.AdapterID),
		AdapterVersionID:    scope.AdapterVersionID,
		AdapterID:           event.Source.AdapterID,
		AdapterVersion:      event.Source.AdapterVersion,
		SourceInstanceID:    scope.SourceInstanceID,
		SourceKind:          string(event.Source.Kind),
	}); err != nil {
		return err
	}
	result, err := InsertFact(ctx, h.pool, FactRow{
		EventID:             event.EventID,
		FactKey:             event.FactKey,
		EventType:           event.EventType,
		ObservedAt:          event.ObservedAt.UTC(),
		IngestedAt:          event.IngestedAt.UTC(),
		TimestampQuality:    event.TimestampQuality,
		SourceInstanceID:    scope.SourceInstanceID,
		SourceNativeEventID: event.Source.NativeEventID,
		Sequence:            int64(event.Source.Sequence),
		AgentInstallationID: scope.AgentInstallationID,
		SurfaceID:           scope.SurfaceID,
		ProjectID:           scope.ProjectID,
		SessionID:           scope.SessionID,
		TurnID:              scope.TurnID,
		ComponentID:         scope.ComponentID,
		DurationMS:          event.Measurements.DurationMS,
		Success:             event.Measurements.Success,
		Count:               event.Measurements.Count,
		ValueState:          event.ValueState,
		Outcome:             event.Outcome,
		CorrelationStatus:   string(event.CorrelationStatus),
	}, EvidenceRow{
		EvidenceID:        evidence.EvidenceID,
		EventID:           evidence.EventID,
		ObservedAt:        event.ObservedAt.UTC(),
		SourceInstanceID:  scope.SourceInstanceID,
		Tier:              string(evidence.Tier),
		Confidence:        evidence.Confidence,
		Completeness:      string(evidence.Completeness),
		ReplayCount:       int64(evidence.ReplayCount),
		FirstSeenAt:       evidence.FirstSeenAt.UTC(),
		LastSeenAt:        evidence.LastSeenAt.UTC(),
		SanitizerVersion:  evidence.Sanitizer,
		PrivacyContractID: evidence.PrivacySHA256,
		AssertEventType:   evidence.Assertion.EventType,
		AssertOutcome:     evidence.Assertion.Outcome,
		AssertValueState:  evidence.Assertion.ValueState,
	})
	if err != nil {
		return err
	}
	if !result.FactInserted && !result.DuplicateReplay {
		return nil
	}
	if err := h.persistSourceWatermark(ctx, event, scope); err != nil {
		return err
	}
	return h.persistProjections(ctx, event, scope)
}

// resolveInventoryLifecycleComponent correlates a native, identity-only
// component name against the current inventory for the same installation.
// Exactly one match is required. Zero or multiple matches remain durable
// unmatched facts and are never promoted into the inventory-backed funnel.
func (h *ObservabilityHandoff) resolveInventoryLifecycleComponent(
	ctx context.Context,
	event observability.Event,
	scope ObservabilityFactScope,
) (ObservabilityFactScope, error) {
	if !isComponentLifecycleEvent(event.EventType) ||
		scope.ComponentID == "" || event.Subject.Kind == "" {
		return scope, nil
	}
	rows, err := h.pool.Query(ctx, `
		SELECT c.component_id, ci.component_installation_id
		FROM component_inventory_state cis
		JOIN component_installations ci
		  ON ci.component_installation_id = cis.component_installation_id
		JOIN component_versions cv
		  ON cv.component_version_id = ci.component_version_id
		JOIN components c ON c.component_id = cv.component_id
		WHERE ci.agent_installation_id = $1
		  AND c.kind = $2
		  AND c.declared_name = $3
		ORDER BY c.component_id
		LIMIT 2
	`, scope.AgentInstallationID, databaseComponentKind(event.Subject.Kind), scope.ComponentID)
	if err != nil {
		return scope, err
	}
	defer rows.Close()
	type match struct {
		componentID             string
		componentInstallationID string
	}
	var matches []match
	for rows.Next() {
		var candidate match
		if err := rows.Scan(&candidate.componentID, &candidate.componentInstallationID); err != nil {
			return scope, err
		}
		matches = append(matches, candidate)
	}
	if err := rows.Err(); err != nil {
		return scope, err
	}
	if len(matches) != 1 {
		return scope, nil
	}
	scope.ComponentID = matches[0].componentID
	scope.ComponentInstallationID = matches[0].componentInstallationID
	scope.DimensionScope = scope.AgentInstallationID + "|" + scope.SurfaceID + "|" +
		scope.ComponentID + "|" + event.EventType
	return scope, nil
}

func isComponentLifecycleEvent(eventType string) bool {
	switch eventType {
	case "component.installed", "component.loaded", "component.invoked", "component.executed":
		return true
	default:
		return false
	}
}

func (h *ObservabilityHandoff) persistSourceWatermark(ctx context.Context, event observability.Event, scope ObservabilityFactScope) error {
	_, err := h.pool.Exec(ctx, `
		INSERT INTO source_watermarks (
			source_instance_id, last_read_sequence, last_emitted_sequence,
			last_observed_at, last_committed_at, gap_count, inactivity
		) VALUES ($1,$2,$2,$3,$4,0,FALSE)
		ON CONFLICT (source_instance_id) DO UPDATE SET
			last_read_sequence = GREATEST(source_watermarks.last_read_sequence, EXCLUDED.last_read_sequence),
			last_emitted_sequence = GREATEST(source_watermarks.last_emitted_sequence, EXCLUDED.last_emitted_sequence),
			last_observed_at = GREATEST(source_watermarks.last_observed_at, EXCLUDED.last_observed_at),
			last_committed_at = GREATEST(source_watermarks.last_committed_at, EXCLUDED.last_committed_at),
			inactivity = FALSE
	`, scope.SourceInstanceID, int64(event.Source.Sequence), event.ObservedAt.UTC(), event.IngestedAt.UTC())
	return err
}

func databaseComponentKind(kind string) string {
	switch kind {
	case "skill", "plugin", "mcp", "hook", "command":
		return kind
	case "tool", "agent":
		// The v1 component catalog has no generic tool/subagent kind.
		// tool_calls still carries the exact safe tool identity; "command"
		// is the closest declared executable-component class.
		return "command"
	default:
		return "command"
	}
}

func providerForAdapter(adapterID string) string {
	switch adapterID {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	default:
		return adapterID
	}
}

func (h *ObservabilityHandoff) persistProjections(ctx context.Context, event observability.Event, scope ObservabilityFactScope) error {
	switch event.EventType {
	case "component.installed", "component.loaded", "component.invoked", "component.executed":
		if scope.ComponentInstallationID == "" {
			return nil
		}
		stage := map[string]string{
			"component.installed": "installed",
			"component.loaded":    "loaded",
			"component.invoked":   "invoked",
			"component.executed":  "executed",
		}[event.EventType]
		_, err := h.pool.Exec(ctx, `
			INSERT INTO component_lifecycle_events (
				component_lifecycle_event_id, component_installation_id,
				observed_at, lifecycle_stage
			) VALUES ($1,$2,$3,$4)
			ON CONFLICT (component_lifecycle_event_id) DO NOTHING
		`, event.EventID, scope.ComponentInstallationID, event.ObservedAt, stage)
		return err
	case "prompt.submitted":
		_, err := h.pool.Exec(ctx, `
			INSERT INTO prompt_features (
				prompt_feature_id, turn_id, observed_at, prompt_size_bytes,
				prompt_character_count, value_state
			) VALUES ($1,$2,$3,NULL,$4,$5)
			ON CONFLICT (prompt_feature_id) DO NOTHING
		`, handoffID("prompt-feature", event.EventID), scope.TurnID, event.ObservedAt,
			event.Measurements.PromptCharacterCount, event.ValueState)
		return err
	case "tool.called":
		if err := EnsurePartition(ctx, h.pool, "tool_calls", event.ObservedAt); err != nil {
			return err
		}
		_, err := h.pool.Exec(ctx, `
			INSERT INTO tool_calls (
				tool_call_id, observed_at, event_id, component_id, session_id,
				duration_ms, outcome
			) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (tool_call_id, observed_at) DO NOTHING
		`, handoffID("tool-call", event.EventID), event.ObservedAt, event.EventID,
			nullableString(scope.ComponentID), scope.SessionID, event.Measurements.DurationMS, event.Outcome)
		return err
	case "model.requested", "model.responded":
		if event.Subject.ModelID == "" {
			return nil
		}
		if err := EnsurePartition(ctx, h.pool, "model_operations", event.ObservedAt); err != nil {
			return err
		}
		operationID := handoffID("model-operation", event.EventID)
		operationKind := "response"
		if event.EventType == "model.requested" {
			operationKind = "request"
		}
		if _, err := h.pool.Exec(ctx, `
			INSERT INTO model_operations (
				model_operation_id, observed_at, event_id, model_id, session_id,
				provider_cost_micros, operation_kind, duration_ms, outcome
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (model_operation_id, observed_at) DO NOTHING
		`, operationID, event.ObservedAt, event.EventID, event.Subject.ModelID,
			scope.SessionID, event.Measurements.ProviderCostMicros, operationKind,
			event.Measurements.DurationMS, event.Outcome); err != nil {
			return err
		}
		if operationKind == "request" {
			return nil
		}
		if event.Measurements.InputTokens == nil || event.Measurements.OutputTokens == nil {
			return nil
		}
		price, err := ensurePublicAPIPrice(ctx, h.pool, event.Subject.ModelID)
		if err != nil {
			return err
		}
		if err := EnsurePartition(ctx, h.pool, "token_usage", event.ObservedAt); err != nil {
			return err
		}
		tokenUsageID := handoffID("token-usage", event.EventID)
		_, err = h.pool.Exec(ctx, `
			INSERT INTO token_usage (
				token_usage_id, observed_at, model_operation_id, input_tokens,
				cached_input_tokens, output_tokens
			) VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (token_usage_id, observed_at) DO NOTHING
		`, tokenUsageID, event.ObservedAt, operationID,
			*event.Measurements.InputTokens, event.Measurements.CachedInputTokens,
			*event.Measurements.OutputTokens)
		if err != nil {
			return err
		}
		if event.Measurements.ProviderCostMicros == nil && price != nil {
			return persistPublicAPICostEstimate(
				ctx, h.pool, tokenUsageID, *price,
				*event.Measurements.InputTokens, *event.Measurements.OutputTokens,
				event.Measurements.CachedInputTokens,
			)
		}
		return nil
	default:
		return nil
	}
}

func (h *ObservabilityHandoff) PersistQuarantineMetadata(quarantine observability.Quarantine, incident observability.Incident) error {
	if h == nil || h.pool == nil {
		return errors.New("observability_handoff_not_configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO schema_quarantine_metadata (
			quarantine_id,source_kind,schema_fingerprint,category,
			byte_count,record_count,observed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (quarantine_id) DO NOTHING
	`, quarantine.QuarantineID, quarantine.SourceKind, quarantine.SchemaFingerprint,
		quarantine.Category, quarantine.ByteCount, quarantine.RecordCount,
		quarantine.ObservedAt); err != nil {
		return err
	}
	idempotencyKey := handoffID(
		"incident-occurrence", quarantine.QuarantineID,
		quarantine.ObservedAt.UTC().Format(time.RFC3339Nano),
		string(quarantine.SourceKind), quarantine.SchemaFingerprint,
		fmt.Sprintf("%d", quarantine.ByteCount), fmt.Sprintf("%d", quarantine.RecordCount),
	)
	occurrenceID := handoffID("incident-occurrence-row", idempotencyKey)
	var inserted bool
	if err := tx.QueryRow(ctx, `
		INSERT INTO incident_occurrences (
			incident_occurrence_id, incident_id, observed_at, evidence_ref,
			schema_fingerprint, safe_error_class, record_count, byte_count,
			idempotency_key
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING TRUE
	`, occurrenceID, incident.IncidentID, quarantine.ObservedAt.UTC(),
		"quarantine:"+quarantine.QuarantineID, quarantine.SchemaFingerprint,
		quarantine.Category, quarantine.RecordCount, quarantine.ByteCount,
		idempotencyKey).Scan(&inserted); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if !inserted {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO incidents (
			incident_id, category, opened_at, resolved_at, last_seen_at,
			occurrence_count, schema_fingerprint, detector_state, triage_state,
			capability_id, installation_value_state, source_value_state,
			severity, recovery_criteria, updated_at
		) VALUES ($1,$2,$3,$4,$5,1,$6,'open','new','core_ingestion',
		          'not_observed','not_observed','warning',
		          'fresh supported evidence followed by a passing targeted audit',now())
		ON CONFLICT (incident_id) DO UPDATE SET
			category=EXCLUDED.category,
			last_seen_at=GREATEST(incidents.last_seen_at, EXCLUDED.last_seen_at),
			occurrence_count=incidents.occurrence_count+1,
			schema_fingerprint=COALESCE(incidents.schema_fingerprint, EXCLUDED.schema_fingerprint),
			detector_state=CASE WHEN incidents.resolved_at IS NULL THEN 'open' ELSE incidents.detector_state END,
			updated_at=now()
	`, incident.IncidentID, incident.Category, incident.OpenedAt, incident.ResolvedAt,
		quarantine.ObservedAt.UTC(), quarantine.SchemaFingerprint); err != nil {
		return err
	}
	pathsJSON, primitiveTypesJSON, shapeState := safeQuarantineShape(quarantine.SourceKind)
	if _, err := tx.Exec(ctx, `
		INSERT INTO quarantine_structural_manifests (
			quarantine_id, incident_id, source_kind, source_instance_pseudonym,
			source_instance_value_state, signal_kind, safe_event_type,
			event_type_value_state, structural_field_paths, primitive_types,
			shape_value_state, schema_fingerprint, adapter_version,
			source_schema_version, parser_version, classification,
			rejection_reason, first_seen_at, last_seen_at, occurrence_count,
			total_record_count, total_byte_count, disposition, updated_at
		) VALUES (
			$1,$2,$3,NULL,'not_observed',$3,NULL,'unknown',$4::jsonb,$5::jsonb,
			$6,$7,NULL,NULL,NULL,'metadata_only_unknown_schema',$8,$9,$9,1,$10,$11,
			'unresolved',now()
		)
		ON CONFLICT (quarantine_id) DO UPDATE SET
			incident_id=CASE
				WHEN quarantine_structural_manifests.incident_id LIKE 'inc_unlinked_%'
				  AND quarantine_structural_manifests.source_kind = EXCLUDED.source_kind
				  AND quarantine_structural_manifests.schema_fingerprint = EXCLUDED.schema_fingerprint
				THEN EXCLUDED.incident_id
				ELSE quarantine_structural_manifests.incident_id
			END,
			structural_field_paths=CASE
				WHEN quarantine_structural_manifests.shape_value_state='not_observed'
				  AND EXCLUDED.shape_value_state='observed'
				THEN EXCLUDED.structural_field_paths
				ELSE quarantine_structural_manifests.structural_field_paths
			END,
			primitive_types=CASE
				WHEN quarantine_structural_manifests.shape_value_state='not_observed'
				  AND EXCLUDED.shape_value_state='observed'
				THEN EXCLUDED.primitive_types
				ELSE quarantine_structural_manifests.primitive_types
			END,
			shape_value_state=CASE
				WHEN quarantine_structural_manifests.shape_value_state='not_observed'
				  AND EXCLUDED.shape_value_state='observed'
				THEN 'observed'
				ELSE quarantine_structural_manifests.shape_value_state
			END,
			last_seen_at=GREATEST(quarantine_structural_manifests.last_seen_at, EXCLUDED.last_seen_at),
			occurrence_count=quarantine_structural_manifests.occurrence_count+1,
			total_record_count=quarantine_structural_manifests.total_record_count+EXCLUDED.total_record_count,
			total_byte_count=quarantine_structural_manifests.total_byte_count+EXCLUDED.total_byte_count,
			updated_at=now()
	`, quarantine.QuarantineID, incident.IncidentID, string(quarantine.SourceKind),
		pathsJSON, primitiveTypesJSON, shapeState, quarantine.SchemaFingerprint,
		quarantine.Category, quarantine.ObservedAt.UTC(), quarantine.RecordCount,
		quarantine.ByteCount); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// safeQuarantineShape returns only protocol field names fixed in Kansoku
// source code. It never derives a durable key or value from the unknown
// payload. JSON hooks do not expose a value-free field-path manifest at this
// boundary, so their shape stays honestly not_observed.
func safeQuarantineShape(kind observability.SourceKind) (pathsJSON, primitiveTypesJSON, state string) {
	switch kind {
	case observability.SourceOTLPLog:
		return `["$.resource_logs","$.resource_logs[].scope_logs","$.resource_logs[].scope_logs[].log_records"]`, `["array","object"]`, "observed"
	case observability.SourceOTLPMetric:
		return `["$.resource_metrics","$.resource_metrics[].scope_metrics","$.resource_metrics[].scope_metrics[].metrics"]`, `["array","object"]`, "observed"
	case observability.SourceOTLPSpan:
		return `["$.resource_spans","$.resource_spans[].scope_spans","$.resource_spans[].scope_spans[].spans"]`, `["array","object"]`, "observed"
	default:
		return `[]`, `["object"]`, "not_observed"
	}
}
