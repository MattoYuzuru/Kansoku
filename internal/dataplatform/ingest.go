package dataplatform

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureDimension upserts a minimal dimension row set required before a fact
// insert: device, agent installation, surface, project, session, turn,
// component, source instance and adapter version. Fixture/test callers pass
// already-known IDs; production adapters own the full dimension model from
// TDD 04.
type DimensionRefs struct {
	DeviceID            string
	AgentInstallationID string
	AgentID             string
	SurfaceID           string
	ProjectID           string
	SessionID           string
	TurnID              string
	ComponentID         string
	ComponentKind       string
	ModelID             string
	ProviderID          string
	AdapterVersionID    string
	AdapterID           string
	AdapterVersion      string
	SourceInstanceID    string
	SourceKind          string
}

// EnsureDimensions idempotently inserts the dimension rows a FactRow depends
// on via foreign keys (ON CONFLICT DO NOTHING keeps repeated fixture setup
// calls safe).
func EnsureDimensions(ctx context.Context, pool *pgxpool.Pool, refs DimensionRefs) error {
	componentKind := refs.ComponentKind
	if componentKind == "" {
		componentKind = "skill"
	}
	statements := []struct {
		optional bool
		sql      string
		args     []any
	}{
		{false, `INSERT INTO devices (device_id) VALUES ($1) ON CONFLICT DO NOTHING`, []any{refs.DeviceID}},
		{false, `INSERT INTO agent_installations (agent_installation_id, device_id, agent_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, []any{refs.AgentInstallationID, refs.DeviceID, refs.AgentID}},
		{false, `INSERT INTO agent_surfaces (surface_id, agent_installation_id, surface_kind) VALUES ($1, $2, 'cli') ON CONFLICT DO NOTHING`, []any{refs.SurfaceID, refs.AgentInstallationID}},
		{false, `INSERT INTO projects (project_id) VALUES ($1) ON CONFLICT DO NOTHING`, []any{refs.ProjectID}},
		{false, `INSERT INTO sessions (session_id, project_id, started_at) VALUES ($1, $2, now()) ON CONFLICT DO NOTHING`, []any{refs.SessionID, refs.ProjectID}},
		{refs.TurnID == "", `INSERT INTO turns (turn_id, session_id, started_at) VALUES ($1, $2, now()) ON CONFLICT DO NOTHING`, []any{refs.TurnID, refs.SessionID}},
		{refs.ComponentID == "", `INSERT INTO components (component_id, kind) VALUES ($1, $2) ON CONFLICT DO NOTHING`, []any{refs.ComponentID, componentKind}},
		{refs.ModelID == "", `INSERT INTO providers (provider_id) VALUES ($1) ON CONFLICT DO NOTHING`, []any{refs.ProviderID}},
		{refs.ModelID == "", `INSERT INTO models (model_id, provider_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, []any{refs.ModelID, refs.ProviderID}},
		{false, `INSERT INTO adapter_versions (adapter_version_id, adapter_id, version) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, []any{refs.AdapterVersionID, refs.AdapterID, refs.AdapterVersion}},
		{false, `INSERT INTO source_instances (source_instance_id, adapter_version_id, source_kind) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, []any{refs.SourceInstanceID, refs.AdapterVersionID, refs.SourceKind}},
	}
	for _, statement := range statements {
		if statement.optional {
			continue
		}
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			return fmt.Errorf("ensure dimension: %w", err)
		}
	}
	return nil
}

// InsertResult reports what InsertFact actually did, mirroring the
// idempotency semantics of internal/observability.CommitResult so that
// replay tests can assert zero duplicate-fact inflation.
type InsertResult struct {
	FactInserted     bool
	EvidenceInserted bool
	DuplicateReplay  bool
}

// InsertFact writes one normalized fact/evidence pair inside a single
// transaction. Idempotency is enforced by the unique index on
// (source_instance_id, source_native_event_id, observed_at): a replay of the
// same source event is detected via ON CONFLICT and increments replay_count
// on evidence without inserting a second fact row, exactly like the Session
// 03 FileStore contract.
func InsertFact(ctx context.Context, pool *pgxpool.Pool, fact FactRow, evidence EvidenceRow) (InsertResult, error) {
	if fact.EventID != evidence.EventID {
		return InsertResult{}, fmt.Errorf("fact/evidence event id mismatch")
	}
	var result InsertResult
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := EnsurePartition(ctx, pool, "events", fact.ObservedAt); err != nil {
			return err
		}
		if err := EnsurePartition(ctx, pool, "event_evidence", fact.ObservedAt); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO events (
				event_id, fact_key, event_type, observed_at, ingested_at, timestamp_quality,
				source_instance_id, source_native_event_id, sequence, agent_installation_id,
				surface_id, project_id, session_id, turn_id, component_id, duration_ms, success,
				count, value_state, outcome, correlation_status
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
			ON CONFLICT (source_instance_id, source_native_event_id, observed_at) DO NOTHING
		`, fact.EventID, fact.FactKey, fact.EventType, fact.ObservedAt, fact.IngestedAt, fact.TimestampQuality,
			fact.SourceInstanceID, fact.SourceNativeEventID, fact.Sequence, nullableString(fact.AgentInstallationID),
			nullableString(fact.SurfaceID), nullableString(fact.ProjectID), nullableString(fact.SessionID),
			nullableString(fact.TurnID), nullableString(fact.ComponentID), fact.DurationMS, fact.Success,
			fact.Count, fact.ValueState, fact.Outcome, fact.CorrelationStatus)
		if err != nil {
			return fmt.Errorf("insert event: %w", err)
		}
		result.FactInserted = tag.RowsAffected() == 1

		evidenceTag, err := tx.Exec(ctx, `
			INSERT INTO event_evidence (
				evidence_id, event_id, observed_at, source_instance_id, tier, confidence,
				completeness, replay_count, first_seen_at, last_seen_at, sanitizer_version,
				privacy_contract_sha256, assertion_event_type, assertion_outcome, assertion_value_state
			) VALUES ($1,$2,$3,$4,$5,$6,$7,0,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT (evidence_id, observed_at) DO UPDATE SET
				replay_count = event_evidence.replay_count + 1,
				last_seen_at = EXCLUDED.last_seen_at
		`, evidence.EvidenceID, evidence.EventID, evidence.ObservedAt, evidence.SourceInstanceID, evidence.Tier,
			evidence.Confidence, evidence.Completeness, evidence.FirstSeenAt, evidence.LastSeenAt,
			evidence.SanitizerVersion, evidence.PrivacyContractID, evidence.AssertEventType, evidence.AssertOutcome,
			evidence.AssertValueState)
		if err != nil {
			return fmt.Errorf("insert evidence: %w", err)
		}
		result.EvidenceInserted = evidenceTag.RowsAffected() == 1 && !result.DuplicateReplay
		if evidenceTag.RowsAffected() == 1 {
			var replay int64
			if err := tx.QueryRow(ctx, `SELECT replay_count FROM event_evidence WHERE evidence_id = $1 AND observed_at = $2`, evidence.EvidenceID, evidence.ObservedAt).Scan(&replay); err != nil {
				return err
			}
			result.DuplicateReplay = replay > 0
			result.EvidenceInserted = replay == 0
		}
		if err := enqueueRepairForFact(ctx, tx, fact); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return InsertResult{}, err
	}
	return result, nil
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
