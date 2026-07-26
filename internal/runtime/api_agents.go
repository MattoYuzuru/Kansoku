package runtime

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"kansoku.local/kansoku/internal/dataplatform"
)

func (a *API) agentResource(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		a.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	installationID := strings.TrimPrefix(request.URL.Path, "/api/v1/agents/")
	if installationID == "" || strings.Contains(installationID, "/") ||
		!safeQueryID.MatchString(installationID) {
		a.writeError(writer, http.StatusBadRequest, "invalid_agent_id")
		return
	}
	from, to, _, ok := parseAnalyticsRange(request.URL.Query())
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_range")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.AgentProfile(ctx, a.pool, installationID, from, to)
	if errors.Is(err, dataplatform.ErrAgentNotFound) {
		a.writeError(writer, http.StatusNotFound, "agent_not_found")
		return
	}
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "agent_profile_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, map[string]any{
		"numerator":    result.Population.Numerator,
		"denominator":  result.Population.Denominator,
		"exclusions":   result.Exclusions,
		"completeness": result.Completeness.Status,
	})
}
