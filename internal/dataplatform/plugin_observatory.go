package dataplatform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	FormulaVersionPluginActiveShare1 = "plugin.active_share/1"
	FormulaVersionPluginProfile1     = "plugin_profile/1"
)

type PluginObservatoryRow struct {
	ComponentInstallationID string     `json:"component_installation_id"`
	ComponentID             string     `json:"component_id"`
	DeclaredName            string     `json:"declared_name"`
	Version                 string     `json:"version,omitempty"`
	VersionState            string     `json:"version_state"`
	SourceScope             string     `json:"source_scope"`
	AgentID                 string     `json:"agent_id"`
	AgentInstallationID     string     `json:"agent_installation_id"`
	Installed               bool       `json:"installed"`
	Enabled                 bool       `json:"enabled"`
	LoadedCount             int64      `json:"loaded_count"`
	LoadedSessions          int64      `json:"loaded_sessions"`
	ChildActivityCount      int64      `json:"child_activity_count"`
	ChildCount              int64      `json:"child_count"`
	CollisionCount          int64      `json:"collision_count"`
	LastLoadedAt            *time.Time `json:"last_loaded_at,omitempty"`
	ActivityState           string     `json:"activity_state"`
	OutcomeState            string     `json:"outcome_state"`
	BundleCompleteness      string     `json:"bundle_completeness"`
}

type PluginPlaneCounts struct {
	Installed int64 `json:"installed"`
	Enabled   int64 `json:"enabled"`
	Loaded    int64 `json:"loaded"`
	Active    int64 `json:"active"`
	Cold      int64 `json:"cold"`
}

type PluginObservatoryResponse struct {
	Data           []PluginObservatoryRow `json:"data"`
	Counts         PluginPlaneCounts      `json:"counts"`
	FormulaVersion string                 `json:"formula_version"`
	Population     Population             `json:"population"`
	Exclusions     map[string]int64       `json:"exclusions"`
	Completeness   Completeness           `json:"completeness"`
	Freshness      Freshness              `json:"freshness"`
}

type PluginChildRow struct {
	ComponentID          string     `json:"component_id"`
	ComponentKind        string     `json:"component_kind"`
	DeclaredName         string     `json:"declared_name"`
	RelationKind         string     `json:"relation_kind"`
	Version              string     `json:"version,omitempty"`
	VersionState         string     `json:"version_state"`
	UsageCount           int64      `json:"usage_count"`
	LastActivityAt       *time.Time `json:"last_activity_at,omitempty"`
	RelationObservedAt   time.Time  `json:"relation_observed_at"`
	RelationCompleteness string     `json:"relation_completeness"`
}

type PluginVersionRow struct {
	Version      string     `json:"version,omitempty"`
	VersionState string     `json:"version_state"`
	FirstSeenAt  *time.Time `json:"first_seen_at,omitempty"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	Current      bool       `json:"current"`
}

type PluginProfileResponse struct {
	Identity       PluginObservatoryRow `json:"identity"`
	Children       []PluginChildRow     `json:"children"`
	Versions       []PluginVersionRow   `json:"versions"`
	Assertions     []SkillAssertionRow  `json:"assertions"`
	Sources        []SkillSourceRow     `json:"sources"`
	IncidentCount  int64                `json:"incident_count"`
	FormulaVersion string               `json:"formula_version"`
	Population     Population           `json:"population"`
	Exclusions     map[string]int64     `json:"exclusions"`
	Completeness   Completeness         `json:"completeness"`
	Freshness      Freshness            `json:"freshness"`
}

var ErrPluginNotFound = errors.New("plugin_not_found")

func PluginObservatory(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (PluginObservatoryResponse, error) {
	budget := Budgets["plugin_observatory_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return PluginObservatoryResponse{}, err
	}
	defer release()
	started := time.Now()
	rows, err := conn.Query(ctx, `
		WITH assertion_counts AS (
			SELECT component_installation_id,
				count(*) FILTER (WHERE assertion_kind='loaded' AND identity_resolution='exact') loaded_count,
				count(DISTINCT session_id) FILTER (
					WHERE assertion_kind='loaded' AND identity_resolution='exact' AND session_id IS NOT NULL
				) loaded_sessions,
				count(*) FILTER (WHERE assertion_kind='child_activity' AND identity_resolution='exact') child_activity_count,
				max(observed_at) FILTER (WHERE assertion_kind='loaded' AND identity_resolution='exact') last_loaded_at
			FROM component_assertions
			WHERE observed_at >= $1 AND observed_at < $2
			GROUP BY component_installation_id
		),
		graph AS (
			SELECT ci.component_installation_id,
				count(*) FILTER (
					WHERE cr.relation_kind IN ('bundles','provides')
					  AND cro.relation_observation_id IS NOT NULL
				) child_count,
				count(*) FILTER (
					WHERE cr.relation_kind='collides_with'
					  AND cro.relation_observation_id IS NOT NULL
				) collision_count,
				bool_and(cro.completeness='complete') FILTER (
					WHERE cr.relation_kind IN ('bundles','provides')
					  AND cro.relation_observation_id IS NOT NULL
				) relations_complete
			FROM component_installations ci
			JOIN component_versions cv ON cv.component_version_id=ci.component_version_id
			JOIN components c ON c.component_id=cv.component_id AND c.kind='plugin'
			JOIN component_inventory_state cis ON cis.component_installation_id=ci.component_installation_id
			LEFT JOIN component_relations cr ON cr.parent_id=c.component_id
			LEFT JOIN component_relation_observations cro
				ON cro.relation_id=cr.relation_id AND cro.inventory_snapshot_id=cis.last_snapshot_id
			GROUP BY ci.component_installation_id
		)
		SELECT ci.component_installation_id,c.component_id,
			coalesce(c.declared_name,c.component_id),cv.version,cv.version_state,
			coalesce(c.source_scope,'unknown'),ai.agent_id,ai.agent_installation_id,
			cis.enabled,coalesce(ac.loaded_count,0),coalesce(ac.loaded_sessions,0),
			coalesce(ac.child_activity_count,0),coalesce(g.child_count,0),
			coalesce(g.collision_count,0),ac.last_loaded_at,
			s.completeness,coalesce(g.relations_complete,true)
		FROM component_inventory_state cis
		JOIN inventory_snapshots s ON s.snapshot_id=cis.last_snapshot_id
		JOIN component_installations ci ON ci.component_installation_id=cis.component_installation_id
		JOIN component_versions cv ON cv.component_version_id=ci.component_version_id
		JOIN components c ON c.component_id=cv.component_id
		JOIN agent_installations ai ON ai.agent_installation_id=ci.agent_installation_id
		LEFT JOIN assertion_counts ac ON ac.component_installation_id=ci.component_installation_id
		LEFT JOIN graph g ON g.component_installation_id=ci.component_installation_id
		WHERE c.kind='plugin'
		ORDER BY coalesce(ac.child_activity_count,0) DESC,
			coalesce(ac.loaded_count,0) DESC,coalesce(c.declared_name,c.component_id),
			ci.component_installation_id
	`, from, to)
	if err != nil {
		return PluginObservatoryResponse{}, budgetOrErr(budget, started, err)
	}
	var response PluginObservatoryResponse
	var eligible int64
	for rows.Next() {
		var row PluginObservatoryRow
		var snapshotCompleteness string
		var relationsComplete bool
		if err := rows.Scan(
			&row.ComponentInstallationID, &row.ComponentID, &row.DeclaredName,
			&row.Version, &row.VersionState, &row.SourceScope, &row.AgentID,
			&row.AgentInstallationID, &row.Enabled, &row.LoadedCount,
			&row.LoadedSessions, &row.ChildActivityCount, &row.ChildCount,
			&row.CollisionCount, &row.LastLoadedAt, &snapshotCompleteness,
			&relationsComplete,
		); err != nil {
			rows.Close()
			return PluginObservatoryResponse{}, err
		}
		row.Installed = true
		row.OutcomeState = "unsupported"
		row.BundleCompleteness = snapshotCompleteness
		if row.ChildCount == 0 && snapshotCompleteness == "complete" {
			// A configuration scan that finds a plugin but enumerates no
			// bundle edges is not proof of a complete empty bundle.
			row.BundleCompleteness = "unknown"
		}
		if row.BundleCompleteness == "complete" && relationsComplete {
			if row.Enabled {
				eligible++
				if row.LoadedCount > 0 || row.ChildActivityCount > 0 {
					row.ActivityState = "active"
					response.Counts.Active++
				} else {
					row.ActivityState = "cold"
					response.Counts.Cold++
				}
			} else {
				row.ActivityState = "disabled"
			}
		} else {
			row.ActivityState = "not_observed"
		}
		response.Counts.Installed++
		if row.Enabled {
			response.Counts.Enabled++
		}
		if row.LoadedCount > 0 {
			response.Counts.Loaded++
		}
		response.Data = append(response.Data, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PluginObservatoryResponse{}, err
	}
	var unresolved, ambiguous int64
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE identity_resolution='unresolved'),
			count(*) FILTER (WHERE identity_resolution='ambiguous')
		FROM component_assertions
		WHERE observed_at >= $1 AND observed_at < $2
		  AND assertion_kind IN ('loaded','child_activity')
	`, from, to).Scan(&unresolved, &ambiguous); err != nil {
		return PluginObservatoryResponse{}, err
	}
	response.FormulaVersion = FormulaVersionPluginActiveShare1
	response.Population = Population{Numerator: response.Counts.Active, Denominator: eligible}
	response.Exclusions = map[string]int64{
		"incomplete_enabled_or_child_graph": response.Counts.Enabled - eligible,
		"unresolved_identity":               unresolved,
		"ambiguous_identity":                ambiguous,
	}
	response.Completeness = completenessFor(eligible, response.Counts.Enabled)
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return PluginObservatoryResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}
	return response, nil
}

func PluginProfile(ctx context.Context, pool *pgxpool.Pool, id string, from, to time.Time) (PluginProfileResponse, error) {
	list, err := PluginObservatory(ctx, pool, from, to)
	if err != nil {
		return PluginProfileResponse{}, err
	}
	var response PluginProfileResponse
	found := false
	for _, row := range list.Data {
		if row.ComponentInstallationID == id {
			response.Identity = row
			found = true
			break
		}
	}
	if !found {
		return PluginProfileResponse{}, ErrPluginNotFound
	}
	budget := Budgets["plugin_profile_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return PluginProfileResponse{}, err
	}
	defer release()
	started := time.Now()
	rows, err := conn.Query(ctx, `
		SELECT child.component_id,child.kind,coalesce(child.declared_name,child.component_id),
			cr.relation_kind,coalesce(child_cv.version,''),coalesce(child_cv.version_state,'not_observed'),
			count(ca.assertion_id) FILTER (
				WHERE ca.assertion_kind IN ('invoked','loaded') AND ca.identity_resolution='exact'
			),max(ca.observed_at) FILTER (
				WHERE ca.assertion_kind IN ('invoked','loaded') AND ca.identity_resolution='exact'
			),cro.observed_at,cro.completeness
		FROM component_inventory_state plugin_state
		JOIN component_installations plugin_ci
			ON plugin_ci.component_installation_id=plugin_state.component_installation_id
		JOIN component_versions plugin_cv
			ON plugin_cv.component_version_id=plugin_ci.component_version_id
		JOIN component_relations cr ON cr.parent_id=plugin_cv.component_id
		JOIN component_relation_observations cro
			ON cro.relation_id=cr.relation_id
			AND cro.inventory_snapshot_id=plugin_state.last_snapshot_id
		JOIN components child ON child.component_id=cr.child_id
		LEFT JOIN component_installations child_ci
			ON child_ci.agent_installation_id=plugin_ci.agent_installation_id
			AND EXISTS (
				SELECT 1 FROM component_versions candidate_cv
				WHERE candidate_cv.component_version_id=child_ci.component_version_id
				  AND candidate_cv.component_id=child.component_id
			)
		LEFT JOIN component_versions child_cv
			ON child_cv.component_version_id=child_ci.component_version_id
		LEFT JOIN component_assertions ca
			ON ca.component_installation_id=child_ci.component_installation_id
			AND ca.observed_at >= $2 AND ca.observed_at < $3
		WHERE plugin_ci.component_installation_id=$1
		  AND cr.relation_kind IN ('bundles','provides')
		GROUP BY child.component_id,child.kind,child.declared_name,cr.relation_kind,
			child_cv.version,child_cv.version_state,cro.observed_at,cro.completeness
		ORDER BY child.kind,coalesce(child.declared_name,child.component_id),child.component_id
	`, id, from, to)
	if err != nil {
		return PluginProfileResponse{}, budgetOrErr(budget, started, err)
	}
	for rows.Next() {
		var row PluginChildRow
		if err := rows.Scan(
			&row.ComponentID, &row.ComponentKind, &row.DeclaredName,
			&row.RelationKind, &row.Version, &row.VersionState, &row.UsageCount,
			&row.LastActivityAt, &row.RelationObservedAt, &row.RelationCompleteness,
		); err != nil {
			rows.Close()
			return PluginProfileResponse{}, err
		}
		response.Children = append(response.Children, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PluginProfileResponse{}, err
	}
	versionRows, err := conn.Query(ctx, `
		SELECT cv.version,cv.version_state,c.first_seen_at,c.last_seen_at,
			cv.component_version_id=current_ci.component_version_id
		FROM component_versions cv
		JOIN components c ON c.component_id=cv.component_id
		JOIN component_installations current_ci
			ON current_ci.component_installation_id=$1
		WHERE cv.component_id=$2
		ORDER BY (cv.component_version_id=current_ci.component_version_id) DESC,cv.version
	`, id, response.Identity.ComponentID)
	if err != nil {
		return PluginProfileResponse{}, err
	}
	for versionRows.Next() {
		var row PluginVersionRow
		if err := versionRows.Scan(
			&row.Version, &row.VersionState, &row.FirstSeenAt, &row.LastSeenAt, &row.Current,
		); err != nil {
			versionRows.Close()
			return PluginProfileResponse{}, err
		}
		response.Versions = append(response.Versions, row)
	}
	versionRows.Close()
	assertionRows, err := conn.Query(ctx, `
		SELECT ca.assertion_id,ca.assertion_kind,ca.mode,ca.evidence_tier,
			ca.confidence,si.source_kind,ca.schema_version,ca.observed_at,
			ca.identity_resolution,ca.candidate_count,coalesce(ca.outcome,''),
			coalesce(ca.terminal_contract_id,'')
		FROM component_assertions ca
		JOIN source_instances si ON si.source_instance_id=ca.source_instance_id
		WHERE ca.component_installation_id=$1
		  AND ca.observed_at >= $2 AND ca.observed_at < $3
		ORDER BY ca.observed_at DESC,ca.assertion_id
		LIMIT 500
	`, id, from, to)
	if err != nil {
		return PluginProfileResponse{}, err
	}
	for assertionRows.Next() {
		var row SkillAssertionRow
		if err := assertionRows.Scan(
			&row.AssertionID, &row.AssertionKind, &row.Mode, &row.EvidenceTier,
			&row.Confidence, &row.SourceKind, &row.SchemaVersion, &row.ObservedAt,
			&row.IdentityResolution, &row.CandidateCount, &row.Outcome,
			&row.TerminalContractID,
		); err != nil {
			assertionRows.Close()
			return PluginProfileResponse{}, err
		}
		response.Assertions = append(response.Assertions, row)
	}
	assertionRows.Close()
	sourceRows, err := conn.Query(ctx, `
		SELECT ca.source_instance_id,si.source_kind,count(*),
			count(*) FILTER (WHERE ca.identity_resolution='exact'),max(ca.observed_at),
			CASE WHEN bool_and(ca.identity_resolution='exact') THEN 'complete' ELSE 'partial' END
		FROM component_assertions ca
		JOIN source_instances si ON si.source_instance_id=ca.source_instance_id
		WHERE ca.component_installation_id=$1
		  AND ca.observed_at >= $2 AND ca.observed_at < $3
		GROUP BY ca.source_instance_id,si.source_kind
		ORDER BY si.source_kind,ca.source_instance_id
	`, id, from, to)
	if err != nil {
		return PluginProfileResponse{}, err
	}
	for sourceRows.Next() {
		var row SkillSourceRow
		if err := sourceRows.Scan(
			&row.SourceInstanceID, &row.SourceKind, &row.AssertionCount,
			&row.ExactCount, &row.LastObservedAt, &row.Completeness,
		); err != nil {
			sourceRows.Close()
			return PluginProfileResponse{}, err
		}
		response.Sources = append(response.Sources, row)
	}
	sourceRows.Close()
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM incidents
		WHERE installation_id=$1 AND detector_state <> 'resolved'
	`, response.Identity.AgentInstallationID).Scan(&response.IncidentCount); err != nil {
		return PluginProfileResponse{}, err
	}
	response.FormulaVersion = FormulaVersionPluginProfile1
	response.Population = Population{
		Numerator:   int64(len(response.Children)),
		Denominator: response.Identity.ChildCount,
	}
	response.Exclusions = list.Exclusions
	response.Completeness = completenessFor(
		int64(len(response.Children)), response.Identity.ChildCount,
	)
	if response.Identity.BundleCompleteness != "complete" {
		response.Completeness.Status = response.Identity.BundleCompleteness
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return PluginProfileResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}
	return response, nil
}
