package dataplatform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type pluginChildEvidence struct {
	ChildComponentID    string
	AgentInstallationID string
	SessionID           string
	TurnID              string
	EventID             string
	EvidenceID          string
	SourceInstanceID    string
	AdapterVersion      string
	SchemaVersion       string
	EvidenceTier        string
	Confidence          float64
	ObservedAt          time.Time
	IdempotencyKey      string
}

// persistPluginChildActivity attributes one child fact only when the current
// snapshot graph resolves exactly one plugin owner in the same agent
// installation. Zero or multiple owners produce no plugin aggregate.
func persistPluginChildActivity(
	ctx context.Context,
	tx pgx.Tx,
	e pluginChildEvidence,
) error {
	if e.ChildComponentID == "" || e.AgentInstallationID == "" ||
		e.SourceInstanceID == "" || e.AdapterVersion == "" ||
		e.SchemaVersion == "" || e.IdempotencyKey == "" ||
		e.ObservedAt.IsZero() {
		return nil
	}
	var pluginInstallationID, pluginName string
	err := tx.QueryRow(ctx, `
		WITH current_edges AS (
			SELECT cr.parent_id,cr.child_id
			FROM component_relations cr
			JOIN component_relation_observations cro ON cro.relation_id=cr.relation_id
			JOIN component_versions parent_cv ON parent_cv.component_id=cr.parent_id
			JOIN component_installations parent_ci
				ON parent_ci.component_version_id=parent_cv.component_version_id
				AND parent_ci.agent_installation_id=$2
			JOIN component_inventory_state parent_state
				ON parent_state.component_installation_id=parent_ci.component_installation_id
				AND parent_state.last_snapshot_id=cro.inventory_snapshot_id
			WHERE cr.relation_kind IN ('bundles','provides')
		),
		candidate_plugins AS (
			SELECT DISTINCT plugin.component_id
			FROM components plugin
			JOIN current_edges direct ON direct.parent_id=plugin.component_id
			WHERE plugin.kind='plugin' AND direct.child_id=$1
			UNION
			SELECT DISTINCT plugin.component_id
			FROM components plugin
			JOIN current_edges first_edge ON first_edge.parent_id=plugin.component_id
			JOIN current_edges second_edge ON second_edge.parent_id=first_edge.child_id
			WHERE plugin.kind='plugin' AND second_edge.child_id=$1
		)
		SELECT min(plugin_ci.component_installation_id),
			min(coalesce(plugin.declared_name,plugin.component_id))
		FROM candidate_plugins candidates
		JOIN components plugin ON plugin.component_id=candidates.component_id
		JOIN component_versions plugin_cv ON plugin_cv.component_id=plugin.component_id
		JOIN component_installations plugin_ci
			ON plugin_ci.component_version_id=plugin_cv.component_version_id
			AND plugin_ci.agent_installation_id=$2
		JOIN component_inventory_state plugin_state
			ON plugin_state.component_installation_id=plugin_ci.component_installation_id
		HAVING count(DISTINCT plugin_ci.component_installation_id)=1
	`, e.ChildComponentID, e.AgentInstallationID).Scan(
		&pluginInstallationID, &pluginName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	idempotency := e.IdempotencyKey + ":plugin-child:" + pluginInstallationID
	assertionID := handoffID("plugin-child-assertion", idempotency)
	inserted, err := tx.Exec(ctx, `
		INSERT INTO component_assertions (
			assertion_id,component_installation_id,agent_installation_id,
			session_id,turn_id,event_id,evidence_id,assertion_kind,mode,
			evidence_tier,confidence,source_instance_id,adapter_version,
			schema_version,observed_at,idempotency_key,identity_resolution,
			declared_identity_pseudonym,candidate_count
			,component_kind,qualified_identity,identity_source,
			owner_plugin_identity,invocation_mode,resolution_version
		) VALUES ($1,$2,$3,$4,$5,$6,$7,'child_activity','not_observed',
			$8,$9,$10,$11,$12,$13,$14,'exact',$15,1,
			'plugin',$16,'plugin_child_activity',$16,'not_observed',1)
		ON CONFLICT (source_instance_id,idempotency_key) DO NOTHING
	`, assertionID,
		pluginInstallationID, e.AgentInstallationID, nullableString(e.SessionID),
		nullableString(e.TurnID), nullableString(e.EventID), nullableString(e.EvidenceID),
		e.EvidenceTier, e.Confidence, e.SourceInstanceID, e.AdapterVersion,
		e.SchemaVersion, e.ObservedAt.UTC(), idempotency,
		inventoryID("declared-component", pluginName), pluginName)
	if err != nil {
		return err
	}
	if inserted.RowsAffected() == 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO component_assertion_resolution_history (
			resolution_history_id, assertion_id, resolution_version,
			identity_resolution, component_installation_id, candidate_count,
			resolver_version, resolution_trigger, resolved_at
		) VALUES (
			$1,$2,1,'exact',$3,1,'component-resolver/2','initial_ingest',$4
		)
		ON CONFLICT (assertion_id,resolution_version) DO NOTHING
	`, handoffID("component-resolution", assertionID, "1"), assertionID,
		pluginInstallationID, e.ObservedAt.UTC())
	return err
}
