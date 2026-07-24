package dataplatform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

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
	DeviceID            string
	AgentInstallationID string
	SurfaceID           string
	ProjectID           string
	SessionID           string
	TurnID              string
	ComponentID         string
	AdapterVersionID    string
	SourceInstanceID    string
	DimensionScope      string
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
	turnID := firstOrStable(event.Scope.TurnID, "turn", sessionID, event.EventID)
	componentID := firstOrStable(
		event.Subject.ComponentID, "component",
		event.Subject.Kind, event.EventType,
	)
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
	if err := EnsureDimensions(ctx, h.pool, DimensionRefs{
		DeviceID:            scope.DeviceID,
		AgentInstallationID: scope.AgentInstallationID,
		AgentID:             event.Source.AdapterID,
		SurfaceID:           scope.SurfaceID,
		ProjectID:           scope.ProjectID,
		SessionID:           scope.SessionID,
		TurnID:              scope.TurnID,
		ComponentID:         scope.ComponentID,
		AdapterVersionID:    scope.AdapterVersionID,
		AdapterID:           event.Source.AdapterID,
		AdapterVersion:      event.Source.AdapterVersion,
		SourceInstanceID:    scope.SourceInstanceID,
		SourceKind:          string(event.Source.Kind),
	}); err != nil {
		return err
	}
	duration := event.Measurements.DurationMS
	_, err := InsertFact(ctx, h.pool, FactRow{
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
		DurationMS:          &duration,
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
	return err
}
