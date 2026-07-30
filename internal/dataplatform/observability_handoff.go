package dataplatform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/observability"
)

const defaultObservabilityHandoffTimeout = 15 * time.Second

var ErrObservabilityProjectionPending = errors.New("observability_projection_pending")

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
	ComponentResolution     string
	ComponentCandidateCount int
	DeclaredComponentPseudo string
	ComponentKind           string
	QualifiedIdentity       string
	IdentitySource          string
	OwnerPluginIdentity     string
	InvocationMode          string
	UpstreamIdentityHash    string
	ComponentSourceScope    string
	ResolutionVersion       int64
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
	declaredComponentPseudo := ""
	if componentID != "" {
		declaredComponentPseudo = handoffID("declared-component", componentID)
	}
	adapterVersionID := handoffID(
		"adapter-version", event.Source.AdapterID, event.Source.AdapterVersion,
	)
	sourceInstanceID := handoffID(
		"source-instance", installationID, event.Source.AdapterID,
		event.Source.AdapterVersion, string(event.Source.Kind),
	)
	return ObservabilityFactScope{
		DeviceID:                deviceID,
		AgentInstallationID:     installationID,
		SurfaceID:               surfaceID,
		ProjectID:               projectID,
		SessionID:               sessionID,
		TurnID:                  turnID,
		ComponentID:             componentID,
		AdapterVersionID:        adapterVersionID,
		SourceInstanceID:        sourceInstanceID,
		ComponentResolution:     "unresolved",
		DeclaredComponentPseudo: declaredComponentPseudo,
		ComponentKind:           databaseComponentKind(event.Subject.Kind),
		QualifiedIdentity:       event.ComponentEvidence.QualifiedIdentity,
		IdentitySource:          event.ComponentEvidence.IdentitySource,
		OwnerPluginIdentity:     event.ComponentEvidence.OwnerPluginIdentity,
		InvocationMode:          event.ComponentEvidence.InvocationMode,
		UpstreamIdentityHash:    event.ComponentEvidence.UpstreamIdentityHash,
		ComponentSourceScope:    event.ComponentEvidence.SourceScope,
		ResolutionVersion:       1,
		DimensionScope: installationID + "|" + surfaceID + "|" +
			componentID + "|" + event.EventType,
	}
}

func eventCarriesTurn(eventType string) bool {
	switch eventType {
	case "prompt.submitted", "tool.called", "model.requested", "model.responded",
		"component.installed", "component.enabled", "component.exposed",
		"component.requested", "component.loaded", "component.invoked", "component.executed":
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
		// The adapter identity is the honest fallback provider dimension.
		// Rich adapter-owned display profiles may later supply a distinct
		// provider label; core never guesses one from a model or brand switch.
		ProviderID:       event.Source.AdapterID,
		AdapterVersionID: scope.AdapterVersionID,
		AdapterID:        event.Source.AdapterID,
		AdapterVersion:   event.Source.AdapterVersion,
		SourceInstanceID: scope.SourceInstanceID,
		SourceKind:       string(event.Source.Kind),
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
		ProjectionInput: &ObservabilityProjectionInput{
			SpecVersion: ProjectionInputSpecVersion,
			Event:       event,
			Evidence:    evidence,
		},
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
		h.markProjectionFailure(ctx, event, evidence, "source_watermark_failed", true)
		return ErrObservabilityProjectionPending
	}
	if err := h.persistProjections(ctx, event, evidence, scope); err != nil {
		h.markProjectionFailure(ctx, event, evidence, "derived_projection_failed", true)
		return ErrObservabilityProjectionPending
	}
	if _, err := h.pool.Exec(ctx, `
		DELETE FROM observability_projection_receipts
		WHERE evidence_id=$1 AND observed_at=$2
	`, evidence.EvidenceID, event.ObservedAt.UTC()); err != nil {
		h.markProjectionFailure(ctx, event, evidence, "projection_receipt_cleanup_failed", true)
		return ErrObservabilityProjectionPending
	}
	return nil
}

func (h *ObservabilityHandoff) markProjectionFailure(
	ctx context.Context,
	event observability.Event,
	evidence observability.Evidence,
	errorClass string,
	retryable bool,
) {
	state := "permanent_error"
	if retryable {
		state = "retryable"
	}
	_, _ = h.pool.Exec(ctx, `
		UPDATE observability_projection_receipts
		SET state=$3, attempt_count=attempt_count+1,
		    last_error_class=$4, last_attempted_at=now()
		WHERE evidence_id=$1 AND observed_at=$2
	`, evidence.EvidenceID, event.ObservedAt.UTC(), state, errorClass)
	_, _ = h.pool.Exec(ctx, `
		INSERT INTO ingest_failures (ingest_failure_id, category, observed_at)
		VALUES ($1,$2,$3)
		ON CONFLICT (ingest_failure_id) DO NOTHING
	`, handoffID("projection-failure", event.EventID, errorClass), errorClass, event.ObservedAt.UTC())
}

// resolveInventoryLifecycleComponent correlates a native, identity-only
// component name against the current inventory for the same installation.
// Exactly one match is required. Zero or multiple matches remain durable
// assertions and incidents, without selecting a candidate or leaking the
// declared identity into the fact dimensions.
func (h *ObservabilityHandoff) resolveInventoryLifecycleComponent(
	ctx context.Context,
	event observability.Event,
	scope ObservabilityFactScope,
) (ObservabilityFactScope, error) {
	if !isComponentLifecycleEvent(event.EventType) {
		return scope, nil
	}
	declaredIdentity := scope.ComponentID
	scope.ComponentID = ""
	scope.ComponentInstallationID = ""
	scope.ComponentResolution = "unresolved"
	scope.ComponentCandidateCount = 0
	scope.DimensionScope = scope.AgentInstallationID + "|" + scope.SurfaceID + "|" +
		scope.DeclaredComponentPseudo + "|" + event.EventType
	if declaredIdentity == "" || event.Subject.Kind == "" {
		if scope.IdentitySource == "redacted" || scope.UpstreamIdentityHash != "" {
			scope.ComponentResolution = "redacted"
		}
		return scope, nil
	}
	var candidateCount int
	var componentID, componentInstallationID *string
	err := h.pool.QueryRow(ctx, `
		WITH candidates AS (
			SELECT DISTINCT c.component_id, ci.component_installation_id
			FROM component_inventory_state cis
			JOIN component_installations ci
			  ON ci.component_installation_id = cis.component_installation_id
			JOIN component_versions cv
			  ON cv.component_version_id = ci.component_version_id
			JOIN components c ON c.component_id = cv.component_id
			LEFT JOIN inventory_nodes node
			  ON node.snapshot_id = cis.last_snapshot_id
			 AND node.node_id = cis.inventory_node_id
			LEFT JOIN inventory_edges ownership
			  ON ownership.snapshot_id = cis.last_snapshot_id
			 AND ownership.to_node_id = cis.inventory_node_id
			 AND ownership.kind = 'bundles'
			LEFT JOIN inventory_nodes owner
			  ON owner.snapshot_id = ownership.snapshot_id
			 AND owner.node_id = ownership.from_node_id
			 AND owner.kind = 'plugin_package'
			WHERE ci.agent_installation_id = $1
			  AND c.kind = $2
			  AND (
				($4 <> '' AND
				 (
				  CASE WHEN owner.declared_name IS NULL
				       THEN c.declared_name
				       ELSE owner.declared_name || ':' || c.declared_name
				  END = $4
				  OR (
					c.kind='plugin' AND
					split_part(c.declared_name,'@',1) = $4
				  )
				  OR (
					c.kind='skill' AND owner.declared_name IS NOT NULL AND
					split_part(owner.declared_name,'@',1) || ':' ||
						c.declared_name = $4
				  )
				 ))
				OR ($4 = '' AND (
					c.declared_name = $3 OR
					(c.kind='plugin' AND
					 split_part(c.declared_name,'@',1) = $3)
				))
			  )
			  AND ($5 = '' OR node.source_scope = $5)
		)
		SELECT count(*)::integer, min(component_id), min(component_installation_id)
		FROM candidates
	`, scope.AgentInstallationID, databaseComponentKind(event.Subject.Kind), declaredIdentity,
		scope.QualifiedIdentity, scope.ComponentSourceScope).
		Scan(&candidateCount, &componentID, &componentInstallationID)
	if err != nil {
		return scope, err
	}
	scope.ComponentCandidateCount = candidateCount
	if candidateCount == 0 {
		return scope, nil
	}
	if candidateCount > 1 {
		scope.ComponentResolution = "ambiguous"
		return scope, nil
	}
	if componentID == nil || componentInstallationID == nil {
		return scope, errors.New("component_identity_exact_candidate_missing")
	}
	scope.ComponentResolution = "exact"
	scope.ComponentID = *componentID
	scope.ComponentInstallationID = *componentInstallationID
	scope.DimensionScope = scope.AgentInstallationID + "|" + scope.SurfaceID + "|" +
		scope.ComponentID + "|" + event.EventType
	return scope, nil
}

func isComponentLifecycleEvent(eventType string) bool {
	switch eventType {
	case "component.installed", "component.enabled", "component.exposed",
		"component.requested", "component.loaded", "component.invoked", "component.executed":
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
	case "skill", "plugin", "mcp", "hook", "command", "app":
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

func (h *ObservabilityHandoff) persistProjections(ctx context.Context, event observability.Event, evidence observability.Evidence, scope ObservabilityFactScope) error {
	switch event.EventType {
	case "component.installed", "component.enabled", "component.exposed",
		"component.requested", "component.loaded", "component.invoked", "component.executed":
		stage := map[string]string{
			"component.installed": "installed",
			"component.enabled":   "enabled",
			"component.exposed":   "exposed",
			"component.requested": "requested",
			"component.loaded":    "loaded",
			"component.invoked":   "invoked",
			"component.executed":  "executed",
		}[event.EventType]
		if stage == "executed" {
			if scope.ComponentInstallationID == "" {
				return nil
			}
			_, err := h.pool.Exec(ctx, `
				INSERT INTO component_lifecycle_events (
					component_lifecycle_event_id, component_installation_id,
					observed_at, lifecycle_stage
				) VALUES ($1,$2,$3,$4)
				ON CONFLICT (component_lifecycle_event_id) DO NOTHING
			`, event.EventID, scope.ComponentInstallationID, event.ObservedAt, stage)
			return err
		}
		tx, err := h.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if scope.ComponentInstallationID != "" && stage != "requested" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO component_lifecycle_events (
					component_lifecycle_event_id, component_installation_id,
					observed_at, lifecycle_stage
				) VALUES ($1,$2,$3,$4)
				ON CONFLICT (component_lifecycle_event_id) DO NOTHING
			`, event.EventID, scope.ComponentInstallationID, event.ObservedAt, stage); err != nil {
				return err
			}
		}
		mode := "not_observed"
		if scope.InvocationMode == "explicit" || scope.InvocationMode == "proactive" ||
			scope.InvocationMode == "nested" {
			mode = scope.InvocationMode
		} else if event.Source.Kind == observability.SourceEvidenceBridge && stage == "invoked" {
			mode = "explicit"
		}
		assertionID := handoffID("component-assertion", evidence.EvidenceID, stage)
		result, err := tx.Exec(ctx, `
			INSERT INTO component_assertions (
				assertion_id, component_installation_id, agent_installation_id,
				session_id, turn_id, event_id, evidence_id, assertion_kind, mode,
				evidence_tier, confidence, source_instance_id, adapter_version,
				schema_version, observed_at, idempotency_key, identity_resolution,
				declared_identity_pseudonym, candidate_count, component_kind,
				qualified_identity, identity_source, owner_plugin_identity,
				invocation_mode, upstream_identity_hash, resolution_version
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,
				$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)
			ON CONFLICT (source_instance_id, idempotency_key) DO NOTHING
		`, assertionID, nullableString(scope.ComponentInstallationID),
			scope.AgentInstallationID, nullableString(scope.SessionID),
			nullableString(scope.TurnID), event.EventID, evidence.EvidenceID, stage,
			mode, string(evidence.Tier), evidence.Confidence, scope.SourceInstanceID,
			event.Source.AdapterVersion, event.Source.SchemaID, event.ObservedAt,
			evidence.EvidenceID+":"+stage, scope.ComponentResolution,
			scope.DeclaredComponentPseudo, scope.ComponentCandidateCount,
			scope.ComponentKind, nullableString(scope.QualifiedIdentity),
			nullableString(scope.IdentitySource), nullableString(scope.OwnerPluginIdentity),
			nullableString(scope.InvocationMode), nullableString(scope.UpstreamIdentityHash),
			scope.ResolutionVersion)
		if err != nil {
			return err
		}
		if result.RowsAffected() > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO component_assertion_resolution_history (
					resolution_history_id, assertion_id, resolution_version,
					identity_resolution, component_installation_id, candidate_count,
					resolver_version, resolution_trigger, resolved_at
				) VALUES (
					$1,$2,$3,$4,$5,$6,'component-resolver/2','initial_ingest',$7
				)
				ON CONFLICT (assertion_id, resolution_version) DO NOTHING
			`, handoffID("component-resolution", assertionID, "1"), assertionID,
				scope.ResolutionVersion, scope.ComponentResolution,
				nullableString(scope.ComponentInstallationID),
				scope.ComponentCandidateCount, event.ObservedAt.UTC()); err != nil {
				return err
			}
		}
		if result.RowsAffected() == 0 || scope.ComponentResolution == "exact" {
			if result.RowsAffected() > 0 && scope.ComponentResolution == "exact" &&
				stage == "invoked" {
				if err := persistPluginChildActivity(ctx, tx, pluginChildEvidence{
					ChildComponentID: scope.ComponentID, AgentInstallationID: scope.AgentInstallationID,
					SessionID: scope.SessionID, TurnID: scope.TurnID, EventID: event.EventID,
					EvidenceID: evidence.EvidenceID, SourceInstanceID: scope.SourceInstanceID,
					AdapterVersion: event.Source.AdapterVersion, SchemaVersion: event.Source.SchemaID,
					EvidenceTier: string(evidence.Tier), Confidence: evidence.Confidence,
					ObservedAt: event.ObservedAt, IdempotencyKey: evidence.EvidenceID + ":" + stage,
				}); err != nil {
					return err
				}
			}
			if result.RowsAffected() > 0 && scope.ComponentResolution == "exact" &&
				stage == "exposed" {
				if _, err := tx.Exec(ctx, `
					INSERT INTO component_observation_windows (
						observation_window_id, component_installation_id,
						source_instance_id, plane, window_start, window_end,
						completeness, idempotency_key
					) VALUES (
						$1,$2,$3,'availability',$4::timestamptz,
						$4::timestamptz + interval '1 microsecond',
						$5,$6
					)
					ON CONFLICT (source_instance_id, idempotency_key) DO NOTHING
				`, handoffID("component-observation-window", assertionID),
					scope.ComponentInstallationID, scope.SourceInstanceID,
					event.ObservedAt.UTC(), string(evidence.Completeness),
					evidence.EvidenceID+":exposure-window"); err != nil {
					return err
				}
			}
			return tx.Commit(ctx)
		}
		if err := persistComponentIdentityIncident(
			ctx, tx, event, evidence, scope, assertionID,
		); err != nil {
			return err
		}
		return tx.Commit(ctx)
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
				duration_ms, outcome, agent_installation_id,
				installation_attribution_state
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'exact')
			ON CONFLICT (tool_call_id, observed_at) DO NOTHING
		`, handoffID("tool-call", event.EventID), event.ObservedAt, event.EventID,
			nullableString(scope.ComponentID), scope.SessionID, event.Measurements.DurationMS,
			event.Outcome, scope.AgentInstallationID)
		if err != nil {
			return err
		}
		if event.Subject.Kind == "mcp" {
			return h.persistMCPToolAssertion(ctx, event, evidence, scope)
		}
		return nil
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
				provider_cost_micros, operation_kind, duration_ms, outcome,
				agent_installation_id, installation_attribution_state
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'exact')
			ON CONFLICT (model_operation_id, observed_at) DO NOTHING
		`, operationID, event.ObservedAt, event.EventID, event.Subject.ModelID,
			scope.SessionID, event.Measurements.ProviderCostMicros, operationKind,
			event.Measurements.DurationMS, event.Outcome, scope.AgentInstallationID); err != nil {
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

func (h *ObservabilityHandoff) persistMCPToolAssertion(
	ctx context.Context,
	event observability.Event,
	evidence observability.Evidence,
	scope ObservabilityFactScope,
) error {
	identity := event.Subject.ComponentID
	if !strings.HasPrefix(identity, "mcp:") {
		return nil
	}
	parts := strings.SplitN(strings.TrimPrefix(identity, "mcp:"), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil
	}
	var serverID, toolID string
	err := h.pool.QueryRow(ctx, `
		SELECT min(s.component_id),min(t.component_id)
		FROM components s
		JOIN component_relations r ON r.parent_id=s.component_id AND r.relation_kind='bundles'
		JOIN components t ON t.component_id=r.child_id
		WHERE s.kind='mcp' AND s.declared_name=$1 AND t.declared_name=$2
		HAVING count(*)=1
	`, parts[0], parts[1]).Scan(&serverID, &toolID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	state, failure := "started", "none"
	switch event.Outcome {
	case "succeeded":
		state = "completed"
	case "failed":
		state, failure = "execution_error", "execution"
	case "cancelled":
		state, failure = "cancelled", "cancelled"
	case "timed_out":
		state, failure = "timed_out", "timeout"
	case "abandoned", "interrupted":
		state, failure = "transport_lost", "transport_loss"
	}
	logicalID := handoffID("mcp-logical-call", event.Source.NativeEventID)
	idempotency := evidence.EvidenceID + ":" + state
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		INSERT INTO mcp_call_assertions(
			call_assertion_id,logical_call_id,server_component_id,tool_component_id,
			agent_installation_id,session_id,source_instance_id,state,observed_at,
			duration_ms,safe_error_class,approval_decision,approval_source,
			evidence_tier,confidence,adapter_version,schema_version,idempotency_key
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'not_observed','not_observed',
			$12,$13,$14,$15,$16)
		ON CONFLICT(source_instance_id,idempotency_key) DO NOTHING
	`, handoffID("mcp-call-assertion", idempotency), logicalID, serverID, toolID,
		scope.AgentInstallationID, nullableString(scope.SessionID), scope.SourceInstanceID,
		state, event.ObservedAt, event.Measurements.DurationMS, failure,
		string(evidence.Tier), evidence.Confidence, event.Source.AdapterVersion,
		event.Source.SchemaID, idempotency)
	if err != nil {
		return err
	}
	if result.RowsAffected() > 0 {
		if err := persistPluginChildActivity(ctx, tx, pluginChildEvidence{
			ChildComponentID: toolID, AgentInstallationID: scope.AgentInstallationID,
			SessionID: scope.SessionID, TurnID: scope.TurnID, EventID: event.EventID,
			EvidenceID: evidence.EvidenceID, SourceInstanceID: scope.SourceInstanceID,
			AdapterVersion: event.Source.AdapterVersion, SchemaVersion: event.Source.SchemaID,
			EvidenceTier: string(evidence.Tier), Confidence: evidence.Confidence,
			ObservedAt: event.ObservedAt, IdempotencyKey: idempotency,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func persistComponentIdentityIncident(
	ctx context.Context,
	tx pgx.Tx,
	event observability.Event,
	evidence observability.Evidence,
	scope ObservabilityFactScope,
	assertionID string,
) error {
	category := "component_identity_" + scope.ComponentResolution
	incidentID := handoffID(
		"component-identity-incident", category, scope.AgentInstallationID,
		scope.DeclaredComponentPseudo,
	)
	idempotencyKey := handoffID(
		"component-identity-occurrence", evidence.EvidenceID,
		scope.ComponentResolution,
	)
	occurrenceID := handoffID("component-identity-occurrence-row", idempotencyKey)
	var inserted bool
	err := tx.QueryRow(ctx, `
		INSERT INTO incident_occurrences (
			incident_occurrence_id, incident_id, observed_at, evidence_ref,
			safe_error_class, record_count, byte_count, idempotency_key
		) VALUES ($1,$2,$3,$4,$5,1,0,$6)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING TRUE
	`, occurrenceID, incidentID, event.ObservedAt.UTC(),
		"component-assertion:"+assertionID, category, idempotencyKey).
		Scan(&inserted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if !inserted {
		return nil
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO incidents (
			incident_id, category, opened_at, resolved_at, last_seen_at,
			occurrence_count, detector_state, triage_state, capability_id,
			installation_id, installation_value_state, source_id,
			source_value_state, severity, adapter_version,
			source_schema_version, recovery_criteria, updated_at
		) VALUES (
			$1,$2,$3,NULL,$3,1,'open','new','skill_observatory',
			$4,'observed',$5,'observed','warning',$6,$7,
			'exact inventory identity followed by a passing targeted audit',now()
		)
		ON CONFLICT (incident_id) DO UPDATE SET
			last_seen_at=GREATEST(incidents.last_seen_at, EXCLUDED.last_seen_at),
			occurrence_count=incidents.occurrence_count+1,
			detector_state='open',
			resolved_at=NULL,
			adapter_version=EXCLUDED.adapter_version,
			source_schema_version=EXCLUDED.source_schema_version,
			updated_at=now()
	`, incidentID, category, event.ObservedAt.UTC(),
		scope.AgentInstallationID, scope.SourceInstanceID,
		event.Source.AdapterVersion, event.Source.SchemaID)
	return err
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
	if quarantine.OccurrenceKey != "" {
		idempotencyKey = quarantine.OccurrenceKey
	}
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
