package dataplatform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	FormulaVersionMCPObservatory1 = "mcp.observatory/1"
	FormulaVersionMCPUptime2      = "mcp.observable_uptime/2"
)

var (
	ErrMCPServerNotFound    = errors.New("mcp_server_not_found")
	ErrMCPPrimitiveNotFound = errors.New("mcp_primitive_not_found")
)

type MCPContourSupport struct {
	Status       string `json:"status"`
	Completeness string `json:"completeness"`
}

type MCPServerRow struct {
	ServerComponentID       string            `json:"server_component_id"`
	DeclaredName            string            `json:"declared_name"`
	Configured              bool              `json:"configured"`
	Enabled                 bool              `json:"enabled"`
	Transport               string            `json:"transport"`
	Locality                string            `json:"locality"`
	EnumerationCompleteness string            `json:"enumeration_completeness"`
	PrimitiveCount          int64             `json:"primitive_count"`
	ToolCount               int64             `json:"tool_count"`
	LatestConnectionState   string            `json:"latest_connection_state"`
	CallCount               int64             `json:"call_count"`
	TerminalCount           int64             `json:"terminal_count"`
	ObservableSeconds       float64           `json:"observable_seconds"`
	ConnectedSeconds        float64           `json:"connected_seconds"`
	UptimeRatio             *float64          `json:"uptime_ratio,omitempty"`
	Inventory               MCPContourSupport `json:"inventory"`
	Connection              MCPContourSupport `json:"connection"`
	Calls                   MCPContourSupport `json:"calls"`
}

type MCPObservatoryResponse struct {
	Data           []MCPServerRow   `json:"data"`
	FormulaVersion string           `json:"formula_version"`
	Population     Population       `json:"population"`
	Exclusions     map[string]int64 `json:"exclusions"`
	Completeness   Completeness     `json:"completeness"`
}

type MCPPrimitiveRow struct {
	ToolComponentID         string    `json:"tool_component_id"`
	DeclaredName            string    `json:"declared_name"`
	Kind                    string    `json:"kind"`
	SchemaFingerprint       *string   `json:"schema_fingerprint,omitempty"`
	DescriptionByteCount    *int64    `json:"description_byte_count,omitempty"`
	SchemaByteCount         *int64    `json:"schema_byte_count,omitempty"`
	EnumerationCompleteness string    `json:"enumeration_completeness"`
	LastAdvertisedAt        time.Time `json:"last_advertised_at"`
}

type MCPCallOutcomeCounts struct {
	Started        int64 `json:"started"`
	Completed      int64 `json:"completed"`
	ExecutionError int64 `json:"execution_error"`
	ProtocolError  int64 `json:"protocol_error"`
	Cancelled      int64 `json:"cancelled"`
	TimedOut       int64 `json:"timed_out"`
	Denied         int64 `json:"denied"`
	TransportLost  int64 `json:"transport_lost"`
	Incomplete     int64 `json:"incomplete"`
}

type MCPServerProfileResponse struct {
	Identity       MCPServerRow         `json:"identity"`
	Primitives     []MCPPrimitiveRow    `json:"primitives"`
	Outcomes       MCPCallOutcomeCounts `json:"outcomes"`
	CallP95MS      *float64             `json:"call_p95_ms,omitempty"`
	FormulaVersion string               `json:"formula_version"`
	Population     Population           `json:"population"`
	Exclusions     map[string]int64     `json:"exclusions"`
	Completeness   Completeness         `json:"completeness"`
}

type MCPPrimitiveListResponse struct {
	Data           []MCPPrimitiveRow `json:"data"`
	FormulaVersion string            `json:"formula_version"`
	Population     Population        `json:"population"`
	Exclusions     map[string]int64  `json:"exclusions"`
	Completeness   Completeness      `json:"completeness"`
}

type MCPToolProfileResponse struct {
	Identity       MCPPrimitiveRow      `json:"identity"`
	Parent         MCPServerRow         `json:"parent"`
	Outcomes       MCPCallOutcomeCounts `json:"outcomes"`
	CallP95MS      *float64             `json:"call_p95_ms,omitempty"`
	FormulaVersion string               `json:"formula_version"`
	Population     Population           `json:"population"`
	Exclusions     map[string]int64     `json:"exclusions"`
	Completeness   Completeness         `json:"completeness"`
	Inventory      MCPContourSupport    `json:"inventory"`
	Calls          MCPContourSupport    `json:"calls"`
}

func MCPObservatory(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (MCPObservatoryResponse, error) {
	budget := Budgets["mcp_observatory_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return MCPObservatoryResponse{}, err
	}
	defer release()
	started := time.Now()
	rows, err := conn.Query(ctx, `
		WITH servers AS (
			SELECT DISTINCT ON (o.server_component_id)
				o.server_component_id,c.declared_name,o.configured,o.enabled,o.transport,o.locality,o.enumeration_completeness
			FROM mcp_server_observations o JOIN components c ON c.component_id=o.server_component_id
			ORDER BY o.server_component_id,o.observed_at DESC
		), primitives AS (
			SELECT server_component_id,count(DISTINCT primitive_component_id) primitive_count,
				count(DISTINCT primitive_component_id) FILTER (WHERE primitive_kind='tool') tool_count
			FROM mcp_primitive_observations WHERE last_advertised_at >= $1 AND last_advertised_at < $2 GROUP BY 1
		), states AS (
			SELECT DISTINCT ON (server_component_id) server_component_id,state
			FROM mcp_connection_assertions WHERE observed_at >= $1 AND observed_at < $2
			ORDER BY server_component_id,observed_at DESC
		), intervals AS (
			SELECT server_component_id,state,observed_at,
				lead(observed_at) OVER (PARTITION BY server_component_id ORDER BY observed_at) next_at
			FROM mcp_connection_assertions WHERE observed_at >= $1 AND observed_at < $2
		), uptime AS (
			SELECT server_component_id,
				coalesce(sum(extract(epoch FROM next_at-observed_at)) FILTER (WHERE next_at IS NOT NULL),0) observable_seconds,
				coalesce(sum(extract(epoch FROM next_at-observed_at)) FILTER (WHERE next_at IS NOT NULL AND state='connected'),0) connected_seconds
			FROM intervals GROUP BY 1
		), calls AS (
			SELECT a.server_component_id,
				count(DISTINCT a.logical_call_id) FILTER (WHERE a.state='started') call_count,
				count(DISTINCT a.logical_call_id) FILTER (
					WHERE a.state IN ('completed','execution_error','protocol_error','cancelled','timed_out','transport_lost','incomplete')
					AND EXISTS (
						SELECT 1 FROM mcp_call_assertions s
						WHERE s.server_component_id=a.server_component_id
						  AND s.logical_call_id=a.logical_call_id AND s.state='started'
						  AND s.observed_at >= $1 AND s.observed_at < $2
					)
				) terminal_count
			FROM mcp_call_assertions a WHERE a.observed_at >= $1 AND a.observed_at < $2 GROUP BY 1
		)
		SELECT s.server_component_id,s.declared_name,s.configured,s.enabled,s.transport,s.locality,s.enumeration_completeness,
			coalesce(p.primitive_count,0),coalesce(p.tool_count,0),coalesce(st.state,'not_observed'),
			coalesce(ca.call_count,0),coalesce(ca.terminal_count,0),coalesce(u.observable_seconds,0),coalesce(u.connected_seconds,0)
		FROM servers s LEFT JOIN primitives p USING(server_component_id) LEFT JOIN states st USING(server_component_id)
		LEFT JOIN uptime u USING(server_component_id) LEFT JOIN calls ca USING(server_component_id)
		ORDER BY s.declared_name,s.server_component_id
	`, from, to)
	if err != nil {
		return MCPObservatoryResponse{}, budgetOrErr(budget, started, err)
	}
	var response MCPObservatoryResponse
	var complete int64
	for rows.Next() {
		var row MCPServerRow
		if err := rows.Scan(&row.ServerComponentID, &row.DeclaredName, &row.Configured, &row.Enabled, &row.Transport, &row.Locality,
			&row.EnumerationCompleteness, &row.PrimitiveCount, &row.ToolCount, &row.LatestConnectionState, &row.CallCount, &row.TerminalCount,
			&row.ObservableSeconds, &row.ConnectedSeconds); err != nil {
			rows.Close()
			return response, err
		}
		if row.ObservableSeconds > 0 {
			ratio := row.ConnectedSeconds / row.ObservableSeconds
			row.UptimeRatio = &ratio
		}
		row.Inventory = MCPContourSupport{Status: "supported", Completeness: row.EnumerationCompleteness}
		if row.LatestConnectionState == "not_observed" {
			row.Connection = MCPContourSupport{Status: "not_observed", Completeness: "unknown"}
		} else {
			row.Connection = MCPContourSupport{Status: "supported", Completeness: "complete"}
		}
		if row.EnumerationCompleteness == "complete" {
			row.Calls = MCPContourSupport{Status: "supported", Completeness: "complete"}
			complete++
		} else {
			row.Calls = MCPContourSupport{Status: "not_observed", Completeness: "unknown"}
		}
		response.Data = append(response.Data, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return response, err
	}
	response.FormulaVersion = FormulaVersionMCPObservatory1
	response.Population = Population{Numerator: complete, Denominator: int64(len(response.Data))}
	response.Exclusions = map[string]int64{"incomplete_pagination": int64(len(response.Data)) - complete}
	response.Completeness = completenessFor(complete, int64(len(response.Data)))
	return response, nil
}

func MCPServerProfile(ctx context.Context, pool *pgxpool.Pool, id string, from, to time.Time) (MCPServerProfileResponse, error) {
	list, err := MCPObservatory(ctx, pool, from, to)
	if err != nil {
		return MCPServerProfileResponse{}, err
	}
	var identity *MCPServerRow
	for i := range list.Data {
		if list.Data[i].ServerComponentID == id {
			identity = &list.Data[i]
			break
		}
	}
	if identity == nil {
		return MCPServerProfileResponse{}, ErrMCPServerNotFound
	}
	budget := Budgets["mcp_server_profile_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return MCPServerProfileResponse{}, err
	}
	defer release()
	rows, err := conn.Query(ctx, `
		SELECT DISTINCT ON (p.primitive_component_id) p.primitive_component_id,c.declared_name,p.primitive_kind,
			p.structural_schema_fingerprint,p.description_byte_count,p.schema_byte_count,p.enumeration_completeness,p.last_advertised_at
		FROM mcp_primitive_observations p JOIN components c ON c.component_id=p.primitive_component_id
		WHERE p.server_component_id=$1 AND p.last_advertised_at >= $2 AND p.last_advertised_at < $3
		ORDER BY p.primitive_component_id,p.last_advertised_at DESC
	`, id, from, to)
	if err != nil {
		return MCPServerProfileResponse{}, err
	}
	response := MCPServerProfileResponse{Identity: *identity, FormulaVersion: "mcp.server_profile/1", Exclusions: list.Exclusions}
	for rows.Next() {
		var r MCPPrimitiveRow
		if err := rows.Scan(&r.ToolComponentID, &r.DeclaredName, &r.Kind, &r.SchemaFingerprint, &r.DescriptionByteCount, &r.SchemaByteCount, &r.EnumerationCompleteness, &r.LastAdvertisedAt); err != nil {
			rows.Close()
			return response, err
		}
		response.Primitives = append(response.Primitives, r)
	}
	rows.Close()
	err = conn.QueryRow(ctx, `
		SELECT count(DISTINCT logical_call_id) FILTER(WHERE state='started'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='completed'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='execution_error'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='protocol_error'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='cancelled'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='timed_out'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='denied'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='transport_lost'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='incomplete'),
			percentile_cont(0.95) WITHIN GROUP(ORDER BY duration_ms) FILTER(WHERE duration_ms IS NOT NULL AND state IN ('completed','execution_error','protocol_error','cancelled','timed_out','transport_lost'))
		FROM mcp_call_assertions WHERE server_component_id=$1 AND observed_at >= $2 AND observed_at < $3
	`, id, from, to).Scan(&response.Outcomes.Started, &response.Outcomes.Completed, &response.Outcomes.ExecutionError, &response.Outcomes.ProtocolError, &response.Outcomes.Cancelled, &response.Outcomes.TimedOut, &response.Outcomes.Denied, &response.Outcomes.TransportLost, &response.Outcomes.Incomplete, &response.CallP95MS)
	if err != nil {
		return response, err
	}
	response.Population = Population{Numerator: identity.TerminalCount, Denominator: identity.CallCount}
	response.Completeness = completenessFor(identity.TerminalCount, identity.CallCount)
	return response, nil
}

func MCPPrimitiveList(ctx context.Context, pool *pgxpool.Pool, serverID string, from, to time.Time) (MCPPrimitiveListResponse, error) {
	profile, err := MCPServerProfile(ctx, pool, serverID, from, to)
	if err != nil {
		return MCPPrimitiveListResponse{}, err
	}
	complete := int64(0)
	for _, primitive := range profile.Primitives {
		if primitive.EnumerationCompleteness == "complete" {
			complete++
		}
	}
	denominator := int64(len(profile.Primitives))
	return MCPPrimitiveListResponse{
		Data:           profile.Primitives,
		FormulaVersion: "mcp.primitive_list/1",
		Population:     Population{Numerator: complete, Denominator: denominator},
		Exclusions:     map[string]int64{"incomplete_pagination": denominator - complete},
		Completeness:   completenessFor(complete, denominator),
	}, nil
}

func MCPToolProfile(ctx context.Context, pool *pgxpool.Pool, serverID, toolID string, from, to time.Time) (MCPToolProfileResponse, error) {
	server, err := MCPServerProfile(ctx, pool, serverID, from, to)
	if err != nil {
		return MCPToolProfileResponse{}, err
	}
	var identity *MCPPrimitiveRow
	for i := range server.Primitives {
		if server.Primitives[i].ToolComponentID == toolID && server.Primitives[i].Kind == "tool" {
			identity = &server.Primitives[i]
			break
		}
	}
	if identity == nil {
		return MCPToolProfileResponse{}, ErrMCPPrimitiveNotFound
	}
	budget := Budgets["mcp_server_profile_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return MCPToolProfileResponse{}, err
	}
	defer release()
	response := MCPToolProfileResponse{
		Identity:       *identity,
		Parent:         server.Identity,
		FormulaVersion: "mcp.tool_profile/1",
		Exclusions:     map[string]int64{},
		Inventory:      MCPContourSupport{Status: "supported", Completeness: identity.EnumerationCompleteness},
	}
	var terminals int64
	err = conn.QueryRow(ctx, `
		SELECT count(DISTINCT logical_call_id) FILTER(WHERE state='started'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='completed'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='execution_error'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='protocol_error'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='cancelled'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='timed_out'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='denied'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='transport_lost'),
			count(DISTINCT logical_call_id) FILTER(WHERE state='incomplete'),
			count(DISTINCT logical_call_id) FILTER(
				WHERE state IN ('completed','execution_error','protocol_error','cancelled','timed_out','transport_lost','incomplete')
				AND EXISTS (
					SELECT 1 FROM mcp_call_assertions s
					WHERE s.server_component_id=$1 AND s.tool_component_id=$2
					  AND s.logical_call_id=mcp_call_assertions.logical_call_id AND s.state='started'
					  AND s.observed_at >= $3 AND s.observed_at < $4
				)
			),
			percentile_cont(0.95) WITHIN GROUP(ORDER BY duration_ms) FILTER(
				WHERE duration_ms IS NOT NULL AND state IN ('completed','execution_error','protocol_error','cancelled','timed_out','transport_lost')
			)
		FROM mcp_call_assertions
		WHERE server_component_id=$1 AND tool_component_id=$2 AND observed_at >= $3 AND observed_at < $4
	`, serverID, toolID, from, to).Scan(
		&response.Outcomes.Started, &response.Outcomes.Completed, &response.Outcomes.ExecutionError,
		&response.Outcomes.ProtocolError, &response.Outcomes.Cancelled, &response.Outcomes.TimedOut,
		&response.Outcomes.Denied, &response.Outcomes.TransportLost, &response.Outcomes.Incomplete,
		&terminals, &response.CallP95MS,
	)
	if err != nil {
		return MCPToolProfileResponse{}, err
	}
	response.Population = Population{Numerator: terminals, Denominator: response.Outcomes.Started}
	response.Completeness = completenessFor(terminals, response.Outcomes.Started)
	if identity.EnumerationCompleteness == "complete" {
		response.Calls = MCPContourSupport{Status: "supported", Completeness: response.Completeness.Status}
	} else {
		response.Calls = MCPContourSupport{Status: "not_observed", Completeness: "unknown"}
		response.Exclusions["incomplete_pagination"] = 1
	}
	return response, nil
}
