package dataplatform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/adaptersdk"
)

const (
	FormulaVersionSkillObservatory1 = "skill.cold_count/1"
	FormulaVersionSkillProfile1     = "skill_profile/1"

	// Version 2 adds the second cold-eligibility path. Version 1 required an
	// exposure observation window, which silently assumed every agent has a
	// surface reporting the model-visible component set. Claude Code has
	// none, so every Claude skill reported not_observed forever -- "we looked
	// and saw nothing" where the truth was "there is nothing to look at".
	//
	// Under /2, an installation whose adapter declares the exposed plane
	// unsupported becomes eligible on inventory-snapshot completeness
	// instead. That mirrors plugin.active_share/2, which already gates
	// eligibility on inventory completeness with no exposure window at all;
	// skills were the outlier. The prohibition on treating a global enabled
	// list as exposure is unchanged: the enabled list still never becomes an
	// exposure assertion, it only becomes an eligibility precondition where
	// no exposure surface exists.
	FormulaVersionSkillObservatory2 = "skill.cold_count/2"
	FormulaVersionSkillProfile2     = "skill_profile/2"
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
	// ExposureState is observed, not_observed or unsupported. "unsupported"
	// means the agent publishes no model-visible component set at all; it is
	// never rendered as zero and never as "not enough evidence yet".
	ExposureState     string `json:"exposure_state"`
	ExposureReason    string `json:"exposure_reason,omitempty"`
	InventoryCoverage string `json:"inventory_coverage"`
	OutcomeState      string `json:"outcome_state"`
	Completeness      string `json:"completeness"`
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

func newSkillProfileResponse() SkillProfileResponse {
	return SkillProfileResponse{
		Assertions: make([]SkillAssertionRow, 0),
		Sources:    make([]SkillSourceRow, 0),
		FileTree:   make([]SkillFileTreeSummary, 0),
	}
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
			SELECT cr.component_installation_id,
				count(*) FILTER (WHERE ca.assertion_kind='exposed' AND cr.identity_resolution='exact') AS exposed_count,
				count(*) FILTER (WHERE ca.assertion_kind='invoked' AND cr.identity_resolution='exact') AS invoked_count,
				count(*) FILTER (WHERE ca.assertion_kind='loaded' AND cr.identity_resolution='exact') AS loaded_count,
				count(*) FILTER (WHERE ca.assertion_kind='child_activity' AND cr.identity_resolution='exact') AS child_count,
				count(DISTINCT ca.session_id) FILTER (WHERE ca.assertion_kind='invoked' AND cr.identity_resolution='exact') AS sessions,
				count(DISTINCT date_trunc('day', ca.observed_at)) FILTER (WHERE ca.assertion_kind='invoked' AND cr.identity_resolution='exact') AS active_days,
				max(ca.observed_at) FILTER (WHERE ca.assertion_kind='invoked' AND cr.identity_resolution='exact') AS last_invoked,
				count(*) FILTER (WHERE ca.assertion_kind='invoked' AND ca.mode='explicit' AND cr.identity_resolution='exact') AS explicit_count,
				count(*) FILTER (WHERE ca.assertion_kind='invoked' AND ca.mode='proactive' AND cr.identity_resolution='exact') AS proactive_count,
				count(*) FILTER (WHERE ca.assertion_kind='invoked' AND ca.mode='nested' AND cr.identity_resolution='exact') AS nested_count,
				count(*) FILTER (WHERE ca.assertion_kind='outcome' AND cr.identity_resolution='exact') AS outcome_count
			FROM component_assertions ca
			JOIN component_assertion_current_resolution cr
			  ON cr.assertion_id=ca.assertion_id
			WHERE ca.observed_at >= $1 AND ca.observed_at < $2
			  AND ca.component_kind='skill'
			GROUP BY cr.component_installation_id
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
			coalesce(w.complete_exposure,false),
			coalesce(ps.state,''), coalesce(ps.reason,''),
			coalesce(snap.completeness,'unknown')
		FROM component_inventory_state cis
		JOIN component_installations ci ON ci.component_installation_id=cis.component_installation_id
		JOIN component_versions cv ON cv.component_version_id=ci.component_version_id
		JOIN components c ON c.component_id=cv.component_id
		JOIN agent_installations ai ON ai.agent_installation_id=ci.agent_installation_id
		LEFT JOIN assertion_counts ac ON ac.component_installation_id=ci.component_installation_id
		LEFT JOIN windows w ON w.component_installation_id=ci.component_installation_id
		LEFT JOIN agent_component_plane_support ps
		  ON ps.agent_installation_id=ai.agent_installation_id
		 AND ps.component_kind='skill' AND ps.plane='exposed'
		LEFT JOIN inventory_snapshots snap ON snap.snapshot_id=cis.last_snapshot_id
		WHERE c.kind='skill'
		ORDER BY coalesce(ac.invoked_count,0) DESC, coalesce(c.declared_name,c.component_id), ci.component_installation_id
	`, from, to)
	if err != nil {
		return SkillObservatoryResponse{}, budgetOrErr(budget, started, err)
	}
	var response SkillObservatoryResponse
	var eligible, missingExposureWindow, unsupportedWithoutInventory int64
	for rows.Next() {
		var row SkillObservatoryRow
		var outcomeCount int64
		var completeExposure bool
		var planeState, planeReason, inventoryCompleteness string
		if err := rows.Scan(
			&row.ComponentInstallationID, &row.ComponentID, &row.DeclaredName,
			&row.Version, &row.VersionState, &row.SourceScope, &row.AgentID,
			&row.AgentInstallationID, &row.Enabled, &row.ExposedCount,
			&row.InvokedCount, &row.LoadedCount, &row.ChildActivityCount,
			&row.UniqueSessions, &row.ActiveDays, &row.LastInvokedAt,
			&row.Modes.Explicit, &row.Modes.Proactive, &row.Modes.Nested,
			&outcomeCount, &completeExposure,
			&planeState, &planeReason, &inventoryCompleteness,
		); err != nil {
			rows.Close()
			return SkillObservatoryResponse{}, err
		}
		row.Installed = true
		row.OutcomeState = "unsupported"
		if outcomeCount > 0 {
			row.OutcomeState = "observed"
		}
		// An undeclared plane is supported. That is what preserves today's
		// behaviour byte for byte for every adapter that has not declared
		// anything -- fakeadapter, wayfinder, and any future adapter -- and
		// it is why planeState is compared against the one value that changes
		// the outcome rather than switched over exhaustively.
		exposureUnsupported := planeState == string(adaptersdk.PlaneUnsupported)
		inventoryComplete := inventoryCompleteness == "complete"
		row.InventoryCoverage = inventoryCompleteness
		switch {
		case exposureUnsupported:
			row.ExposureState = "unsupported"
			row.ExposureReason = planeReason
		case row.ExposedCount > 0:
			row.ExposureState = "observed"
		default:
			row.ExposureState = "not_observed"
		}
		// Cold eligibility has two paths, and exactly one applies per row.
		//   supported plane: the agent told us what it exposed, inside a
		//     complete window. Unchanged from /1.
		//   unsupported plane: there is no exposure surface at all, so
		//     eligibility rests on the inventory snapshot being complete.
		//     This is deliberately coupled to coverage gaps: a mis-mounted
		//     host reports a partial snapshot and drops out of the
		//     denominator, instead of producing a confident cold count over a
		//     silently truncated inventory.
		rowEligible := row.Enabled &&
			((!exposureUnsupported && completeExposure && row.ExposedCount > 0) ||
				(exposureUnsupported && inventoryComplete))
		switch {
		case !rowEligible:
			row.ColdState = "not_observed"
			row.Completeness = "partial"
			if row.Enabled && exposureUnsupported {
				unsupportedWithoutInventory++
			} else if row.Enabled {
				missingExposureWindow++
			}
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
		SELECT count(*) FILTER (WHERE cr.identity_resolution='unresolved'),
			count(*) FILTER (WHERE cr.identity_resolution='ambiguous')
		FROM component_assertions ca
		JOIN component_assertion_current_resolution cr
		  ON cr.assertion_id=ca.assertion_id
		WHERE ca.observed_at >= $1 AND ca.observed_at < $2
		  AND ca.component_kind = 'skill'
	`, from, to).Scan(&unresolved, &ambiguous); err != nil {
		return SkillObservatoryResponse{}, err
	}
	response.FormulaVersion = FormulaVersionSkillObservatory2
	response.Population = Population{
		Numerator: response.Counts.Cold, Denominator: eligible,
	}
	// The two exposure exclusions partition the ineligible enabled rows, so
	// nothing is counted twice: partial_or_missing_exposure_window covers
	// supported planes only, and the new key covers the rest. Their sum is
	// still enabled - eligible.
	response.Exclusions = map[string]int64{
		"partial_or_missing_exposure_window":                    missingExposureWindow,
		"exposure_plane_unsupported_without_complete_inventory": unsupportedWithoutInventory,
		"unresolved_identity":                                   unresolved,
		"ambiguous_identity":                                    ambiguous,
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
	response := newSkillProfileResponse()
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
			cr.identity_resolution, cr.candidate_count, coalesce(ca.outcome,''),
			coalesce(ca.terminal_contract_id,'')
		FROM component_assertions ca
		JOIN component_assertion_current_resolution cr
		  ON cr.assertion_id=ca.assertion_id
		JOIN source_instances si ON si.source_instance_id=ca.source_instance_id
		WHERE cr.component_installation_id=$1
		  AND ca.component_kind='skill'
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
			count(*) FILTER (WHERE cr.identity_resolution='exact'), max(ca.observed_at),
			CASE WHEN bool_and(cr.identity_resolution='exact') THEN 'complete' ELSE 'partial' END
		FROM component_assertions ca
		JOIN component_assertion_current_resolution cr
		  ON cr.assertion_id=ca.assertion_id
		JOIN source_instances si ON si.source_instance_id=ca.source_instance_id
		WHERE cr.component_installation_id=$1
		  AND ca.component_kind='skill'
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
	response.FormulaVersion = FormulaVersionSkillProfile2
	response.Population = Population{Numerator: int64(len(response.Assertions)), Denominator: int64(len(response.Assertions))}
	response.Exclusions = list.Exclusions
	response.Completeness = list.Completeness
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return SkillProfileResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}
	return response, nil
}
