package dataplatform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	FormulaVersionSkillObservatory1 = "skill.cold_count/1"
	FormulaVersionSkillProfile1     = "skill_profile/1"
)

type SkillModeCounts struct {
	Explicit  int64 `json:"explicit"`
	Proactive int64 `json:"proactive"`
	Nested    int64 `json:"nested"`
}

type SkillObservatoryRow struct {
	ComponentInstallationID string          `json:"component_installation_id"`
	ComponentID             string          `json:"component_id"`
	DeclaredName            string          `json:"declared_name"`
	Version                 string          `json:"version,omitempty"`
	VersionState            string          `json:"version_state"`
	SourceScope             string          `json:"source_scope"`
	AgentID                 string          `json:"agent_id"`
	AgentInstallationID     string          `json:"agent_installation_id"`
	Installed               bool            `json:"installed"`
	Enabled                 bool            `json:"enabled"`
	ExposedCount            int64           `json:"exposed_count"`
	InvokedCount            int64           `json:"invoked_count"`
	LoadedCount             int64           `json:"loaded_count"`
	ChildActivityCount      int64           `json:"child_activity_count"`
	UniqueSessions          int64           `json:"unique_sessions"`
	ActiveDays              int64           `json:"active_days"`
	LastInvokedAt           *time.Time      `json:"last_invoked_at,omitempty"`
	Modes                   SkillModeCounts `json:"modes"`
	ColdState               string          `json:"cold_state"`
	OutcomeState            string          `json:"outcome_state"`
	Completeness            string          `json:"completeness"`
}

type SkillPlaneCounts struct {
	Installed int64 `json:"installed"`
	Enabled   int64 `json:"enabled"`
	Exposed   int64 `json:"exposed"`
	Invoked   int64 `json:"invoked"`
	Loaded    int64 `json:"loaded"`
	Cold      int64 `json:"cold"`
}

type SkillObservatoryResponse struct {
	Data           []SkillObservatoryRow `json:"data"`
	Counts         SkillPlaneCounts      `json:"counts"`
	FormulaVersion string                `json:"formula_version"`
	Population     Population            `json:"population"`
	Exclusions     map[string]int64      `json:"exclusions"`
	Completeness   Completeness          `json:"completeness"`
	Freshness      Freshness             `json:"freshness"`
}

type SkillAssertionRow struct {
	AssertionID        string    `json:"assertion_id"`
	AssertionKind      string    `json:"assertion_kind"`
	Mode               string    `json:"mode"`
	EvidenceTier       string    `json:"evidence_tier"`
	Confidence         float64   `json:"confidence"`
	SourceKind         string    `json:"source_kind"`
	SchemaVersion      string    `json:"schema_version"`
	ObservedAt         time.Time `json:"observed_at"`
	IdentityResolution string    `json:"identity_resolution"`
	CandidateCount     int       `json:"candidate_count"`
	Outcome            string    `json:"outcome,omitempty"`
	TerminalContractID string    `json:"terminal_contract_id,omitempty"`
}

type SkillSourceRow struct {
	SourceInstanceID string     `json:"source_instance_id"`
	SourceKind       string     `json:"source_kind"`
	AssertionCount   int64      `json:"assertion_count"`
	ExactCount       int64      `json:"exact_count"`
	LastObservedAt   *time.Time `json:"last_observed_at,omitempty"`
	Completeness     string     `json:"completeness"`
}

type SkillFileTreeSummary struct {
	InventorySnapshotID string `json:"inventory_snapshot_id"`
	FileCount           int64  `json:"file_count"`
	DirectoryCount      int64  `json:"directory_count"`
	TotalBytes          int64  `json:"total_bytes"`
	MaxDepth            int    `json:"max_depth"`
}

type SkillProfileResponse struct {
	Identity       SkillObservatoryRow    `json:"identity"`
	Assertions     []SkillAssertionRow    `json:"assertions"`
	Sources        []SkillSourceRow       `json:"sources"`
	FileTree       []SkillFileTreeSummary `json:"file_tree"`
	IncidentCount  int64                  `json:"incident_count"`
	FormulaVersion string                 `json:"formula_version"`
	Population     Population             `json:"population"`
	Exclusions     map[string]int64       `json:"exclusions"`
	Completeness   Completeness           `json:"completeness"`
	Freshness      Freshness              `json:"freshness"`
}

var ErrSkillNotFound = errors.New("skill_not_found")

func SkillObservatory(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (SkillObservatoryResponse, error) {
	budget := Budgets["skill_observatory_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return SkillObservatoryResponse{}, err
	}
	defer release()
	started := time.Now()
	rows, err := conn.Query(ctx, `
		WITH assertion_counts AS (
			SELECT component_installation_id,
				count(*) FILTER (WHERE assertion_kind='exposed' AND identity_resolution='exact') AS exposed_count,
				count(*) FILTER (WHERE assertion_kind='invoked' AND identity_resolution='exact') AS invoked_count,
				count(*) FILTER (WHERE assertion_kind='loaded' AND identity_resolution='exact') AS loaded_count,
				count(*) FILTER (WHERE assertion_kind='child_activity' AND identity_resolution='exact') AS child_count,
				count(DISTINCT session_id) FILTER (WHERE assertion_kind='invoked' AND identity_resolution='exact') AS sessions,
				count(DISTINCT date_trunc('day', observed_at)) FILTER (WHERE assertion_kind='invoked' AND identity_resolution='exact') AS active_days,
				max(observed_at) FILTER (WHERE assertion_kind='invoked' AND identity_resolution='exact') AS last_invoked,
				count(*) FILTER (WHERE assertion_kind='invoked' AND mode='explicit' AND identity_resolution='exact') AS explicit_count,
				count(*) FILTER (WHERE assertion_kind='invoked' AND mode='proactive' AND identity_resolution='exact') AS proactive_count,
				count(*) FILTER (WHERE assertion_kind='invoked' AND mode='nested' AND identity_resolution='exact') AS nested_count,
				count(*) FILTER (WHERE assertion_kind='outcome' AND identity_resolution='exact') AS outcome_count
			FROM component_assertions
			WHERE observed_at >= $1 AND observed_at < $2
			GROUP BY component_installation_id
		),
		windows AS (
			SELECT component_installation_id,
				bool_or(completeness='complete' AND plane='availability'
					AND window_start < $2 AND window_end > $1) AS complete_exposure
			FROM component_observation_windows
			WHERE window_start < $2 AND window_end > $1
			GROUP BY component_installation_id
		)
		SELECT ci.component_installation_id, c.component_id,
			coalesce(c.declared_name,c.component_id), cv.version, cv.version_state,
			coalesce(c.source_scope,'unknown'), ai.agent_id, ai.agent_installation_id,
			cis.enabled, coalesce(ac.exposed_count,0), coalesce(ac.invoked_count,0),
			coalesce(ac.loaded_count,0), coalesce(ac.child_count,0),
			coalesce(ac.sessions,0), coalesce(ac.active_days,0), ac.last_invoked,
			coalesce(ac.explicit_count,0), coalesce(ac.proactive_count,0),
			coalesce(ac.nested_count,0), coalesce(ac.outcome_count,0),
			coalesce(w.complete_exposure,false)
		FROM component_inventory_state cis
		JOIN component_installations ci ON ci.component_installation_id=cis.component_installation_id
		JOIN component_versions cv ON cv.component_version_id=ci.component_version_id
		JOIN components c ON c.component_id=cv.component_id
		JOIN agent_installations ai ON ai.agent_installation_id=ci.agent_installation_id
		LEFT JOIN assertion_counts ac ON ac.component_installation_id=ci.component_installation_id
		LEFT JOIN windows w ON w.component_installation_id=ci.component_installation_id
		WHERE c.kind='skill'
		ORDER BY coalesce(ac.invoked_count,0) DESC, coalesce(c.declared_name,c.component_id), ci.component_installation_id
	`, from, to)
	if err != nil {
		return SkillObservatoryResponse{}, budgetOrErr(budget, started, err)
	}
	var response SkillObservatoryResponse
	var eligible int64
	for rows.Next() {
		var row SkillObservatoryRow
		var outcomeCount int64
		var completeExposure bool
		if err := rows.Scan(
			&row.ComponentInstallationID, &row.ComponentID, &row.DeclaredName,
			&row.Version, &row.VersionState, &row.SourceScope, &row.AgentID,
			&row.AgentInstallationID, &row.Enabled, &row.ExposedCount,
			&row.InvokedCount, &row.LoadedCount, &row.ChildActivityCount,
			&row.UniqueSessions, &row.ActiveDays, &row.LastInvokedAt,
			&row.Modes.Explicit, &row.Modes.Proactive, &row.Modes.Nested,
			&outcomeCount, &completeExposure,
		); err != nil {
			rows.Close()
			return SkillObservatoryResponse{}, err
		}
		row.Installed = true
		row.OutcomeState = "unsupported"
		if outcomeCount > 0 {
			row.OutcomeState = "observed"
		}
		switch {
		case !row.Enabled || !completeExposure || row.ExposedCount == 0:
			row.ColdState = "not_observed"
			row.Completeness = "partial"
		case row.InvokedCount == 0:
			row.ColdState = "cold"
			row.Completeness = "complete"
			response.Counts.Cold++
			eligible++
		default:
			row.ColdState = "used"
			row.Completeness = "complete"
			eligible++
		}
		response.Counts.Installed++
		if row.Enabled {
			response.Counts.Enabled++
		}
		if row.ExposedCount > 0 {
			response.Counts.Exposed++
		}
		if row.InvokedCount > 0 {
			response.Counts.Invoked++
		}
		if row.LoadedCount > 0 {
			response.Counts.Loaded++
		}
		response.Data = append(response.Data, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return SkillObservatoryResponse{}, err
	}
	var unresolved, ambiguous int64
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE identity_resolution='unresolved'),
			count(*) FILTER (WHERE identity_resolution='ambiguous')
		FROM component_assertions
		WHERE observed_at >= $1 AND observed_at < $2
	`, from, to).Scan(&unresolved, &ambiguous); err != nil {
		return SkillObservatoryResponse{}, err
	}
	response.FormulaVersion = FormulaVersionSkillObservatory1
	response.Population = Population{
		Numerator: response.Counts.Cold, Denominator: eligible,
	}
	response.Exclusions = map[string]int64{
		"partial_or_missing_exposure_window": response.Counts.Enabled - eligible,
		"unresolved_identity":                unresolved,
		"ambiguous_identity":                 ambiguous,
	}
	response.Completeness = completenessFor(eligible, response.Counts.Enabled)
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return SkillObservatoryResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}
	return response, nil
}

func SkillProfile(ctx context.Context, pool *pgxpool.Pool, id string, from, to time.Time) (SkillProfileResponse, error) {
	list, err := SkillObservatory(ctx, pool, from, to)
	if err != nil {
		return SkillProfileResponse{}, err
	}
	var response SkillProfileResponse
	found := false
	for _, row := range list.Data {
		if row.ComponentInstallationID == id {
			response.Identity = row
			found = true
			break
		}
	}
	if !found {
		return SkillProfileResponse{}, ErrSkillNotFound
	}
	budget := Budgets["skill_profile_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return SkillProfileResponse{}, err
	}
	defer release()
	started := time.Now()
	rows, err := conn.Query(ctx, `
		SELECT ca.assertion_id, ca.assertion_kind, ca.mode, ca.evidence_tier,
			ca.confidence, si.source_kind, ca.schema_version, ca.observed_at,
			ca.identity_resolution, ca.candidate_count, coalesce(ca.outcome,''),
			coalesce(ca.terminal_contract_id,'')
		FROM component_assertions ca
		JOIN source_instances si ON si.source_instance_id=ca.source_instance_id
		WHERE ca.component_installation_id=$1
		  AND ca.observed_at >= $2 AND ca.observed_at < $3
		ORDER BY ca.observed_at DESC, ca.assertion_id
		LIMIT 500
	`, id, from, to)
	if err != nil {
		return SkillProfileResponse{}, budgetOrErr(budget, started, err)
	}
	for rows.Next() {
		var row SkillAssertionRow
		if err := rows.Scan(
			&row.AssertionID, &row.AssertionKind, &row.Mode, &row.EvidenceTier,
			&row.Confidence, &row.SourceKind, &row.SchemaVersion, &row.ObservedAt,
			&row.IdentityResolution, &row.CandidateCount, &row.Outcome,
			&row.TerminalContractID,
		); err != nil {
			rows.Close()
			return SkillProfileResponse{}, err
		}
		response.Assertions = append(response.Assertions, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return SkillProfileResponse{}, err
	}
	sourceRows, err := conn.Query(ctx, `
		SELECT ca.source_instance_id, si.source_kind, count(*),
			count(*) FILTER (WHERE ca.identity_resolution='exact'), max(ca.observed_at),
			CASE WHEN bool_and(ca.identity_resolution='exact') THEN 'complete' ELSE 'partial' END
		FROM component_assertions ca
		JOIN source_instances si ON si.source_instance_id=ca.source_instance_id
		WHERE ca.component_installation_id=$1
		  AND ca.observed_at >= $2 AND ca.observed_at < $3
		GROUP BY ca.source_instance_id, si.source_kind
		ORDER BY si.source_kind, ca.source_instance_id
	`, id, from, to)
	if err != nil {
		return SkillProfileResponse{}, err
	}
	for sourceRows.Next() {
		var row SkillSourceRow
		if err := sourceRows.Scan(
			&row.SourceInstanceID, &row.SourceKind, &row.AssertionCount,
			&row.ExactCount, &row.LastObservedAt, &row.Completeness,
		); err != nil {
			sourceRows.Close()
			return SkillProfileResponse{}, err
		}
		response.Sources = append(response.Sources, row)
	}
	sourceRows.Close()
	if err := sourceRows.Err(); err != nil {
		return SkillProfileResponse{}, err
	}
	treeRows, err := conn.Query(ctx, `
		SELECT inventory_snapshot_id,
			count(*) FILTER (WHERE entry_kind='file'),
			count(*) FILTER (WHERE entry_kind='directory'),
			coalesce(sum(byte_count),0), coalesce(max(depth),0)
		FROM component_file_tree_metadata
		WHERE component_installation_id=$1
		GROUP BY inventory_snapshot_id
		ORDER BY inventory_snapshot_id
	`, id)
	if err != nil {
		return SkillProfileResponse{}, err
	}
	for treeRows.Next() {
		var row SkillFileTreeSummary
		if err := treeRows.Scan(
			&row.InventorySnapshotID, &row.FileCount, &row.DirectoryCount,
			&row.TotalBytes, &row.MaxDepth,
		); err != nil {
			treeRows.Close()
			return SkillProfileResponse{}, err
		}
		response.FileTree = append(response.FileTree, row)
	}
	treeRows.Close()
	if err := treeRows.Err(); err != nil {
		return SkillProfileResponse{}, err
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM incidents
		WHERE installation_id=$1 AND detector_state <> 'resolved'
	`, response.Identity.AgentInstallationID).Scan(&response.IncidentCount); err != nil {
		return SkillProfileResponse{}, err
	}
	response.FormulaVersion = FormulaVersionSkillProfile1
	response.Population = Population{Numerator: int64(len(response.Assertions)), Denominator: int64(len(response.Assertions))}
	response.Exclusions = list.Exclusions
	response.Completeness = list.Completeness
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return SkillProfileResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}
	return response, nil
}
