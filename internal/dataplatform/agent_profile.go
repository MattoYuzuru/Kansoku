package dataplatform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const FormulaVersionAgentProfile1 = "agent_profile/1"

type AgentIdentityRow struct {
	AgentInstallationID string `json:"agent_installation_id"`
	AdapterID           string `json:"adapter_id"`
	ProviderID          string `json:"provider_id"`
	DisplayName         string `json:"display_name"`
	DisplayAlias        string `json:"display_alias,omitempty"`
	SurfaceKind         string `json:"surface_kind"`
	AgentVersion        string `json:"agent_version,omitempty"`
	AdapterVersion      string `json:"adapter_version,omitempty"`
	Completeness        string `json:"completeness"`
	SourceProvenance    string `json:"source_provenance"`
}

type AgentActivitySummary struct {
	EventCount        int64 `json:"event_count"`
	SessionCount      int64 `json:"session_count"`
	PromptCount       int64 `json:"prompt_count"`
	SuccessCount      int64 `json:"success_count"`
	FailureCount      int64 `json:"failure_count"`
	ToolCallCount     int64 `json:"tool_call_count"`
	ComponentCount    int64 `json:"component_count"`
	OpenIncidentCount int64 `json:"open_incident_count"`
}

type AgentModelRow struct {
	ModelID             string       `json:"model_id"`
	RequestCount        int64        `json:"request_count"`
	InputTokens         int64        `json:"input_tokens"`
	CachedInputTokens   int64        `json:"cached_input_tokens"`
	OutputTokens        int64        `json:"output_tokens"`
	CostedRequestCount  int64        `json:"costed_request_count"`
	EstimatedCostMicros int64        `json:"estimated_cost_micros"`
	SuccessCount        int64        `json:"success_count"`
	FailureCount        int64        `json:"failure_count"`
	Percentiles         *Percentiles `json:"percentiles,omitempty"`
}

type AgentSourceRow struct {
	SourceInstanceID string     `json:"source_instance_id"`
	SourceKind       string     `json:"source_kind"`
	AdapterVersion   string     `json:"adapter_version"`
	FactCount        int64      `json:"fact_count"`
	EvidenceCount    int64      `json:"evidence_count"`
	LastObservedAt   *time.Time `json:"last_observed_at,omitempty"`
	GapCount         int64      `json:"gap_count"`
	State            string     `json:"state"`
}

type AgentProfileResponse struct {
	Identity       AgentIdentityRow     `json:"identity"`
	Activity       AgentActivitySummary `json:"activity"`
	Models         []AgentModelRow      `json:"models"`
	Sources        []AgentSourceRow     `json:"sources"`
	FormulaVersion string               `json:"formula_version"`
	Population     Population           `json:"population"`
	Exclusions     map[string]int64     `json:"exclusions"`
	Completeness   Completeness         `json:"completeness"`
	Freshness      Freshness            `json:"freshness"`
}

var ErrAgentNotFound = errors.New("agent_not_found")

// AgentProfile builds the complete installation drill-down from exact event
// lineage. Fresh projections use direct dimensional columns; legacy rows
// remain queryable through their exact event foreign key without mutating
// historical telemetry.
func AgentProfile(ctx context.Context, pool *pgxpool.Pool, installationID string, from, to time.Time) (AgentProfileResponse, error) {
	budget := Budgets["agent_profile_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return AgentProfileResponse{}, err
	}
	defer release()
	started := time.Now()
	var response AgentProfileResponse
	err = conn.QueryRow(ctx, `
		SELECT ai.agent_installation_id, ai.agent_id,
			coalesce(p.provider_id, ai.agent_id),
			coalesce(p.display_name, ai.agent_id),
			coalesce(p.display_alias, ''),
			coalesce(p.surface_kind, s.surface_kind, 'unknown'),
			coalesce(p.observed_agent_version, ''),
			coalesce(p.adapter_version, av.version, ''),
			coalesce(p.completeness, 'partial'),
			coalesce(p.source_provenance, 'legacy_exact_event_lineage')
		FROM agent_installations ai
		LEFT JOIN agent_installation_profiles p
		  ON p.agent_installation_id = ai.agent_installation_id
		LEFT JOIN LATERAL (
			SELECT surface_kind FROM agent_surfaces
			WHERE agent_installation_id = ai.agent_installation_id
			ORDER BY surface_id LIMIT 1
		) s ON TRUE
		LEFT JOIN LATERAL (
			SELECT av.version
			FROM events e
			JOIN source_instances si ON si.source_instance_id = e.source_instance_id
			JOIN adapter_versions av ON av.adapter_version_id = si.adapter_version_id
			WHERE e.agent_installation_id = ai.agent_installation_id
			ORDER BY e.observed_at DESC LIMIT 1
		) av ON TRUE
		WHERE ai.agent_installation_id = $1
	`, installationID).Scan(
		&response.Identity.AgentInstallationID, &response.Identity.AdapterID,
		&response.Identity.ProviderID, &response.Identity.DisplayName,
		&response.Identity.DisplayAlias, &response.Identity.SurfaceKind,
		&response.Identity.AgentVersion, &response.Identity.AdapterVersion,
		&response.Identity.Completeness, &response.Identity.SourceProvenance,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentProfileResponse{}, ErrAgentNotFound
	}
	if err != nil {
		return AgentProfileResponse{}, err
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(*), count(DISTINCT session_id),
			count(*) FILTER (WHERE event_type = 'prompt.submitted'),
			count(*) FILTER (WHERE outcome = 'succeeded'),
			count(*) FILTER (WHERE outcome IN ('failed','timed_out','abandoned')),
			count(DISTINCT component_id) FILTER (WHERE component_id IS NOT NULL)
		FROM events
		WHERE agent_installation_id = $1 AND observed_at >= $2 AND observed_at < $3
	`, installationID, from, to).Scan(
		&response.Activity.EventCount, &response.Activity.SessionCount,
		&response.Activity.PromptCount, &response.Activity.SuccessCount,
		&response.Activity.FailureCount, &response.Activity.ComponentCount,
	); err != nil {
		return AgentProfileResponse{}, err
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(*)
		FROM tool_calls tc
		LEFT JOIN events e ON e.event_id = tc.event_id AND e.observed_at = tc.observed_at
		WHERE coalesce(tc.agent_installation_id, e.agent_installation_id) = $1
		  AND tc.observed_at >= $2 AND tc.observed_at < $3
	`, installationID, from, to).Scan(&response.Activity.ToolCallCount); err != nil {
		return AgentProfileResponse{}, err
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM incidents
		WHERE installation_id = $1 AND detector_state <> 'resolved'
	`, installationID).Scan(&response.Activity.OpenIncidentCount); err != nil {
		return AgentProfileResponse{}, err
	}
	modelRows, err := conn.Query(ctx, `
		WITH selected AS (
			SELECT mo.*
			FROM model_operations mo
			LEFT JOIN events e ON e.event_id = mo.event_id AND e.observed_at = mo.observed_at
			WHERE coalesce(mo.agent_installation_id, e.agent_installation_id) = $1
			  AND mo.observed_at >= $2 AND mo.observed_at < $3
			  AND mo.operation_kind = 'response'
		)
		SELECT mo.model_id, count(*),
			coalesce(sum(tu.input_tokens),0),
			coalesce(sum(tu.cached_input_tokens),0),
			coalesce(sum(tu.output_tokens),0),
			count(*) FILTER (WHERE mo.provider_cost_micros IS NOT NULL OR priced.cost_micros IS NOT NULL),
			coalesce(sum(coalesce(mo.provider_cost_micros, priced.cost_micros, 0)),0),
			count(*) FILTER (WHERE mo.outcome = 'succeeded'),
			count(*) FILTER (WHERE mo.outcome IN ('failed','timed_out','abandoned')),
			percentile_cont(0.50) WITHIN GROUP (ORDER BY mo.duration_ms)
				FILTER (WHERE mo.duration_ms IS NOT NULL),
			percentile_cont(0.90) WITHIN GROUP (ORDER BY mo.duration_ms)
				FILTER (WHERE mo.duration_ms IS NOT NULL),
			percentile_cont(0.95) WITHIN GROUP (ORDER BY mo.duration_ms)
				FILTER (WHERE mo.duration_ms IS NOT NULL),
			percentile_cont(0.99) WITHIN GROUP (ORDER BY mo.duration_ms)
				FILTER (WHERE mo.duration_ms IS NOT NULL)
		FROM selected mo
		LEFT JOIN token_usage tu
		  ON tu.model_operation_id = mo.model_operation_id AND tu.observed_at = mo.observed_at
		LEFT JOIN LATERAL (
			SELECT ce.cost_micros FROM cost_estimates ce
			JOIN price_catalog_versions pcv
			  ON pcv.price_catalog_version_id = ce.price_catalog_version_id
			WHERE ce.token_usage_id = tu.token_usage_id
			ORDER BY pcv.effective_at DESC LIMIT 1
		) priced ON TRUE
		GROUP BY mo.model_id
		ORDER BY count(*) DESC, mo.model_id
	`, installationID, from, to)
	if err != nil {
		return AgentProfileResponse{}, err
	}
	for modelRows.Next() {
		var row AgentModelRow
		var percentiles Percentiles
		if err := modelRows.Scan(
			&row.ModelID, &row.RequestCount, &row.InputTokens,
			&row.CachedInputTokens, &row.OutputTokens, &row.CostedRequestCount,
			&row.EstimatedCostMicros, &row.SuccessCount, &row.FailureCount,
			&percentiles.P50, &percentiles.P90, &percentiles.P95, &percentiles.P99,
		); err != nil {
			modelRows.Close()
			return AgentProfileResponse{}, err
		}
		if percentiles.P50 != nil || percentiles.P90 != nil ||
			percentiles.P95 != nil || percentiles.P99 != nil {
			row.Percentiles = &percentiles
		}
		response.Models = append(response.Models, row)
	}
	modelRows.Close()
	if err := modelRows.Err(); err != nil {
		return AgentProfileResponse{}, err
	}
	sourceRows, err := conn.Query(ctx, `
		WITH facts AS (
			SELECT source_instance_id, count(DISTINCT event_id) AS fact_count
			FROM events
			WHERE agent_installation_id = $1
			  AND observed_at >= $2 AND observed_at < $3
			GROUP BY source_instance_id
		),
		evidence AS (
			SELECT source_instance_id, count(DISTINCT evidence_id) AS evidence_count
			FROM event_evidence
			WHERE observed_at >= $2 AND observed_at < $3
			GROUP BY source_instance_id
		)
		SELECT si.source_instance_id, si.source_kind, av.version,
			f.fact_count, coalesce(ev.evidence_count, 0),
			sw.last_observed_at, coalesce(sw.gap_count,0),
			CASE
				WHEN coalesce(sw.inactivity, false) THEN 'disabled'
				WHEN coalesce(sw.gap_count,0) > 0 THEN 'degraded'
				WHEN sw.last_observed_at IS NULL THEN 'not_observed'
				ELSE 'producing'
			END
		FROM source_instances si
		JOIN adapter_versions av ON av.adapter_version_id = si.adapter_version_id
		JOIN facts f ON f.source_instance_id = si.source_instance_id
		LEFT JOIN evidence ev ON ev.source_instance_id = si.source_instance_id
		LEFT JOIN source_watermarks sw ON sw.source_instance_id = si.source_instance_id
		ORDER BY si.source_kind, si.source_instance_id
	`, installationID, from, to)
	if err != nil {
		return AgentProfileResponse{}, err
	}
	for sourceRows.Next() {
		var row AgentSourceRow
		if err := sourceRows.Scan(
			&row.SourceInstanceID, &row.SourceKind, &row.AdapterVersion,
			&row.FactCount, &row.EvidenceCount, &row.LastObservedAt,
			&row.GapCount, &row.State,
		); err != nil {
			sourceRows.Close()
			return AgentProfileResponse{}, err
		}
		response.Sources = append(response.Sources, row)
	}
	sourceRows.Close()
	if err := sourceRows.Err(); err != nil {
		return AgentProfileResponse{}, err
	}
	var exact, excluded int64
	if err := conn.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE
				(mo.agent_installation_id = $1 AND mo.installation_attribution_state = 'exact')
				OR e.agent_installation_id = $1),
			count(*) FILTER (WHERE coalesce(mo.installation_attribution_state,'not_observed')
				IN ('candidate','ambiguous','unmatched','not_observed')
				AND e.agent_installation_id IS NULL)
		FROM model_operations mo
		LEFT JOIN events e ON e.event_id = mo.event_id AND e.observed_at = mo.observed_at
		WHERE (mo.agent_installation_id = $1 OR e.agent_installation_id = $1)
		  AND mo.observed_at >= $2 AND mo.observed_at < $3
	`, installationID, from, to).Scan(&exact, &excluded); err != nil {
		return AgentProfileResponse{}, err
	}
	response.FormulaVersion = FormulaVersionAgentProfile1
	response.Population = Population{Numerator: exact, Denominator: exact + excluded}
	response.Exclusions = map[string]int64{"non_exact_installation_attribution": excluded}
	response.Completeness = completenessFor(exact, exact+excluded)
	watermark, pending, err := aggregateSourceWatermarkFreshness(ctx, pool)
	if err != nil {
		return AgentProfileResponse{}, err
	}
	response.Freshness = Freshness{RollupWatermark: watermark, LateEventsPending: pending}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return AgentProfileResponse{}, &ErrBudgetExceeded{
			BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed,
		}
	}
	return response, nil
}
