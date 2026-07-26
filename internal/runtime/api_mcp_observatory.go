package runtime

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"kansoku.local/kansoku/internal/dataplatform"
)

func (a *API) mcpObservatory(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		a.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	from, to, _, ok := parseAnalyticsRange(request.URL.Query())
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_range")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.MCPObservatory(ctx, a.pool, from, to)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "mcp_observatory_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, map[string]any{
		"numerator": result.Population.Numerator, "denominator": result.Population.Denominator,
		"exclusions": result.Exclusions, "completeness": result.Completeness.Status,
	})
}

func (a *API) mcpResource(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		a.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	resource := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/components/mcp/"), "/")
	parts := strings.Split(resource, "/")
	if len(parts) == 0 || parts[0] == "" || !safeQueryID.MatchString(parts[0]) {
		a.writeError(writer, http.StatusBadRequest, "invalid_mcp_server_id")
		return
	}
	id := parts[0]
	if len(parts) != 1 && (len(parts) < 2 || parts[1] != "tools") {
		a.writeError(writer, http.StatusBadRequest, "invalid_mcp_resource")
		return
	}
	if len(parts) > 3 || (len(parts) == 3 && !safeQueryID.MatchString(parts[2])) {
		a.writeError(writer, http.StatusBadRequest, "invalid_mcp_tool_id")
		return
	}
	from, to, _, ok := parseAnalyticsRange(request.URL.Query())
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_range")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	if len(parts) == 2 {
		result, err := dataplatform.MCPPrimitiveList(ctx, a.pool, id, from, to)
		if errors.Is(err, dataplatform.ErrMCPServerNotFound) {
			a.writeError(writer, http.StatusNotFound, "mcp_server_not_found")
			return
		}
		if err != nil {
			a.writeError(writer, http.StatusServiceUnavailable, "mcp_tool_list_unavailable")
			return
		}
		a.write(writer, http.StatusOK, result, map[string]any{
			"numerator": result.Population.Numerator, "denominator": result.Population.Denominator,
			"exclusions": result.Exclusions, "completeness": result.Completeness.Status,
		})
		return
	}
	if len(parts) == 3 {
		result, err := dataplatform.MCPToolProfile(ctx, a.pool, id, parts[2], from, to)
		if errors.Is(err, dataplatform.ErrMCPServerNotFound) || errors.Is(err, dataplatform.ErrMCPPrimitiveNotFound) {
			a.writeError(writer, http.StatusNotFound, "mcp_tool_not_found")
			return
		}
		if err != nil {
			a.writeError(writer, http.StatusServiceUnavailable, "mcp_tool_profile_unavailable")
			return
		}
		a.write(writer, http.StatusOK, result, map[string]any{
			"numerator": result.Population.Numerator, "denominator": result.Population.Denominator,
			"exclusions": result.Exclusions, "completeness": result.Completeness.Status,
		})
		return
	}
	result, err := dataplatform.MCPServerProfile(ctx, a.pool, id, from, to)
	if errors.Is(err, dataplatform.ErrMCPServerNotFound) {
		a.writeError(writer, http.StatusNotFound, "mcp_server_not_found")
		return
	}
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "mcp_profile_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, map[string]any{
		"numerator": result.Population.Numerator, "denominator": result.Population.Denominator,
		"exclusions": result.Exclusions, "completeness": result.Completeness.Status,
	})
}
