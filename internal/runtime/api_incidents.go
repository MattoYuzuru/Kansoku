package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/integrity"
	"kansoku.local/kansoku/internal/privacy"
)

type workbenchCursor struct {
	Version string    `json:"version"`
	Kind    string    `json:"kind"`
	At      time.Time `json:"at"`
	ID      string    `json:"id"`
}

func (a *API) signWorkbenchCursor(kind string, at time.Time, id string) (string, error) {
	if len(a.cursorKey) < minSecretBytes || kind == "" || id == "" || at.IsZero() {
		return "", errors.New("cursor_unavailable")
	}
	payload, err := json.Marshal(workbenchCursor{
		Version: "kansoku.cursor/1", Kind: kind, At: at.UTC(), ID: id,
	})
	if err != nil {
		return "", errors.New("cursor_unavailable")
	}
	mac := hmac.New(sha256.New, a.cursorKey)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (a *API) verifyWorkbenchCursor(encoded, kind string) (workbenchCursor, error) {
	parts := strings.Split(encoded, ".")
	if len(parts) != 2 || len(encoded) > 1024 {
		return workbenchCursor{}, errors.New("invalid_cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return workbenchCursor{}, errors.New("invalid_cursor")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return workbenchCursor{}, errors.New("invalid_cursor")
	}
	mac := hmac.New(sha256.New, a.cursorKey)
	_, _ = mac.Write(payload)
	expected := mac.Sum(nil)
	if len(signature) != len(expected) ||
		subtle.ConstantTimeCompare(signature, expected) != 1 {
		return workbenchCursor{}, errors.New("invalid_cursor")
	}
	var cursor workbenchCursor
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil ||
		cursor.Version != "kansoku.cursor/1" || cursor.Kind != kind ||
		cursor.At.IsZero() || !safeQueryID.MatchString(cursor.ID) {
		return workbenchCursor{}, errors.New("invalid_cursor")
	}
	return cursor, nil
}

func incidentPageLimit(request *http.Request) (int, bool) {
	raw := request.URL.Query().Get("limit")
	if raw == "" {
		return 50, true
	}
	return boundedInt(raw, 1, 100)
}

func optionalRFC3339(value string) (*time.Time, bool) {
	if value == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, false
	}
	parsed = parsed.UTC()
	return &parsed, true
}

func validOptionalQueryID(value string) bool {
	return value == "" || safeQueryID.MatchString(value)
}

func (a *API) listIncidents(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		a.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	query := request.URL.Query()
	limit, ok := incidentPageLimit(request)
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_limit")
		return
	}
	filter := dataplatform.IncidentFilter{
		DetectorState: query.Get("state"), TriageState: query.Get("triage"),
		Adapter: query.Get("adapter"),
		Source:  query.Get("source"), Capability: query.Get("capability"),
		FailureClass: query.Get("failure"),
	}
	if filter.DetectorState != "" &&
		filter.DetectorState != "open" && filter.DetectorState != "recovering" &&
		filter.DetectorState != "resolved" {
		a.writeError(writer, http.StatusBadRequest, "invalid_incident_filter")
		return
	}
	if filter.TriageState != "" &&
		filter.TriageState != "new" && filter.TriageState != "acknowledged" &&
		filter.TriageState != "investigating" && filter.TriageState != "action_ready" {
		a.writeError(writer, http.StatusBadRequest, "invalid_incident_filter")
		return
	}
	if !validOptionalQueryID(filter.Adapter) || !validOptionalQueryID(filter.Source) ||
		!validOptionalQueryID(filter.Capability) ||
		!validOptionalQueryID(filter.FailureClass) {
		a.writeError(writer, http.StatusBadRequest, "invalid_incident_filter")
		return
	}
	filter.From, ok = optionalRFC3339(query.Get("from"))
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_incident_filter")
		return
	}
	filter.To, ok = optionalRFC3339(query.Get("to"))
	if !ok || filter.From != nil && filter.To != nil && !filter.To.After(*filter.From) {
		a.writeError(writer, http.StatusBadRequest, "invalid_incident_filter")
		return
	}
	var position *dataplatform.IncidentPagePosition
	if raw := query.Get("cursor"); raw != "" {
		cursor, err := a.verifyWorkbenchCursor(raw, "incidents")
		if err != nil {
			a.writeError(writer, http.StatusBadRequest, "invalid_cursor")
			return
		}
		position = &dataplatform.IncidentPagePosition{LastSeenAt: cursor.At, IncidentID: cursor.ID}
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	rows, hasMore, err := dataplatform.ListIncidents(ctx, a.pool, filter, position, limit)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "incidents_unavailable")
		return
	}
	page := dataplatform.IncidentPage{
		Data: rows, HasMore: hasMore, TotalState: "lower_bound",
		TotalLowerBound: len(rows), FormulaVersion: dataplatform.IncidentListFormulaVersion,
		Exclusions: []string{"uncategorized_user_notes"}, Completeness: "complete",
	}
	if hasMore && len(rows) != 0 {
		last := rows[len(rows)-1]
		page.NextCursor, err = a.signWorkbenchCursor("incidents", last.LastSeenAt, last.IncidentID)
		if err != nil {
			a.writeError(writer, http.StatusInternalServerError, "cursor_unavailable")
			return
		}
	}
	a.write(writer, http.StatusOK, page, map[string]any{
		"status": "complete", "numerator": len(rows), "denominator": len(rows),
		"exclusions": page.Exclusions, "page_complete": true,
	})
}

func (a *API) incidentResource(writer http.ResponseWriter, request *http.Request) {
	relative := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/incidents/"), "/")
	parts := strings.Split(relative, "/")
	if relative == "" || len(parts) > 2 || !safeQueryID.MatchString(parts[0]) {
		a.writeError(writer, http.StatusNotFound, "incident_not_found")
		return
	}
	incidentID := parts[0]
	if len(parts) == 1 {
		if request.Method != http.MethodGet {
			a.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		a.incidentDetail(writer, request, incidentID)
		return
	}
	switch parts[1] {
	case "occurrences":
		a.incidentOccurrences(writer, request, incidentID)
	case "debug-bundle":
		a.incidentDebugBundle(writer, request, incidentID)
	case "triage", "acknowledge", "investigating", "action-ready":
		a.incidentTriage(writer, request, incidentID, parts[1])
	default:
		a.writeError(writer, http.StatusNotFound, "incident_resource_not_found")
	}
}

func (a *API) incidentDetail(writer http.ResponseWriter, request *http.Request, incidentID string) {
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	incident, err := dataplatform.GetIncident(ctx, a.pool, incidentID)
	if dataplatform.IsNotFound(err) {
		a.writeError(writer, http.StatusNotFound, "incident_not_found")
		return
	}
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "incidents_unavailable")
		return
	}
	a.write(writer, http.StatusOK, incident, map[string]any{
		"status": "complete", "numerator": 1, "denominator": 1,
		"exclusions": []string{"uncategorized_user_notes"},
	})
}

type cursorPage[T any] struct {
	Data            []T      `json:"data"`
	HasMore         bool     `json:"has_more"`
	NextCursor      string   `json:"next_cursor,omitempty"`
	TotalState      string   `json:"total_state"`
	TotalLowerBound int      `json:"total_lower_bound"`
	FormulaVersion  string   `json:"formula_version"`
	Exclusions      []string `json:"exclusions"`
	Completeness    string   `json:"completeness"`
}

func (a *API) incidentOccurrences(writer http.ResponseWriter, request *http.Request, incidentID string) {
	if request.Method != http.MethodGet {
		a.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	limit, ok := incidentPageLimit(request)
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_limit")
		return
	}
	var position *dataplatform.OccurrencePagePosition
	if raw := request.URL.Query().Get("cursor"); raw != "" {
		cursor, err := a.verifyWorkbenchCursor(raw, "occurrences:"+incidentID)
		if err != nil {
			a.writeError(writer, http.StatusBadRequest, "invalid_cursor")
			return
		}
		position = &dataplatform.OccurrencePagePosition{ObservedAt: cursor.At, OccurrenceID: cursor.ID}
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	rows, hasMore, err := dataplatform.ListIncidentOccurrences(ctx, a.pool, incidentID, position, limit)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "incident_occurrences_unavailable")
		return
	}
	page := cursorPage[dataplatform.IncidentOccurrence]{
		Data: rows, HasMore: hasMore, TotalState: "lower_bound",
		TotalLowerBound: len(rows), FormulaVersion: dataplatform.IncidentOccurrenceFormulaVersion,
		Exclusions: []string{}, Completeness: "complete",
	}
	if hasMore && len(rows) != 0 {
		last := rows[len(rows)-1]
		page.NextCursor, err = a.signWorkbenchCursor(
			"occurrences:"+incidentID, last.ObservedAt, last.OccurrenceID,
		)
		if err != nil {
			a.writeError(writer, http.StatusInternalServerError, "cursor_unavailable")
			return
		}
	}
	a.write(writer, http.StatusOK, page, map[string]any{
		"status": "complete", "numerator": len(rows), "denominator": len(rows),
		"exclusions": []string{}, "page_complete": true,
	})
}

type triageRequest struct {
	State        string `json:"state"`
	NoteCategory string `json:"note_category"`
}

func (a *API) incidentTriage(
	writer http.ResponseWriter,
	request *http.Request,
	incidentID, action string,
) {
	if request.Method != http.MethodPatch && request.Method != http.MethodPost {
		a.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	payload := triageRequest{}
	if action == "triage" {
		if !a.decode(writer, request, &payload) {
			return
		}
	} else {
		actionState := map[string]string{
			"acknowledge": "acknowledged", "investigating": "investigating",
			"action-ready": "action_ready",
		}[action]
		if request.ContentLength > 0 && !a.decode(writer, request, &payload) {
			return
		}
		payload.State = actionState
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	if err := integrity.SetIncidentTriage(
		ctx, a.pool, incidentID, payload.State, payload.NoteCategory,
	); errors.Is(err, integrity.ErrIncidentNotFound) {
		a.writeError(writer, http.StatusNotFound, "incident_not_found")
		return
	} else if err != nil {
		a.writeError(writer, http.StatusBadRequest, SafeErrorClass(err))
		return
	}
	a.incidentDetail(writer, request, incidentID)
}

func (a *API) quarantine(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		a.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	query := request.URL.Query()
	limit, ok := incidentPageLimit(request)
	if !ok || !validOptionalQueryID(query.Get("fingerprint")) ||
		!validOptionalQueryID(query.Get("source")) {
		a.writeError(writer, http.StatusBadRequest, "invalid_quarantine_filter")
		return
	}
	from, ok := optionalRFC3339(query.Get("from"))
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_quarantine_filter")
		return
	}
	to, ok := optionalRFC3339(query.Get("to"))
	if !ok || from != nil && to != nil && !to.After(*from) {
		a.writeError(writer, http.StatusBadRequest, "invalid_quarantine_filter")
		return
	}
	var position *dataplatform.QuarantinePagePosition
	if raw := query.Get("cursor"); raw != "" {
		cursor, err := a.verifyWorkbenchCursor(raw, "quarantine")
		if err != nil {
			a.writeError(writer, http.StatusBadRequest, "invalid_cursor")
			return
		}
		position = &dataplatform.QuarantinePagePosition{LastSeenAt: cursor.At, QuarantineID: cursor.ID}
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	rows, hasMore, err := dataplatform.ListQuarantine(
		ctx, a.pool, query.Get("fingerprint"), query.Get("source"),
		from, to, position, limit,
	)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "quarantine_unavailable")
		return
	}
	page := cursorPage[dataplatform.QuarantineManifest]{
		Data: rows, HasMore: hasMore, TotalState: "lower_bound",
		TotalLowerBound: len(rows), FormulaVersion: dataplatform.QuarantineListFormulaVersion,
		Exclusions: []string{"unknown_values", "raw_payload"}, Completeness: "complete",
	}
	if hasMore && len(rows) != 0 {
		last := rows[len(rows)-1]
		page.NextCursor, err = a.signWorkbenchCursor(
			"quarantine", last.LastSeenAt, last.QuarantineID,
		)
		if err != nil {
			a.writeError(writer, http.StatusInternalServerError, "cursor_unavailable")
			return
		}
	}
	a.write(writer, http.StatusOK, page, map[string]any{
		"status": "complete", "numerator": len(rows), "denominator": len(rows),
		"exclusions": page.Exclusions, "page_complete": true,
	})
}

func (a *API) quarantineResource(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		a.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	id := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/quarantine/"), "/")
	if !safeQueryID.MatchString(id) || strings.Contains(id, "/") {
		a.writeError(writer, http.StatusNotFound, "quarantine_not_found")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	manifest, err := dataplatform.GetQuarantine(ctx, a.pool, id)
	if dataplatform.IsNotFound(err) {
		a.writeError(writer, http.StatusNotFound, "quarantine_not_found")
		return
	}
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "quarantine_unavailable")
		return
	}
	a.write(writer, http.StatusOK, manifest, map[string]any{
		"status": "complete", "numerator": 1, "denominator": 1,
		"exclusions": []string{"unknown_values", "raw_payload"},
	})
}

type incidentDebugBundle struct {
	SchemaVersion      string                           `json:"schema_version"`
	Incident           dataplatform.IncidentRecord      `json:"incident"`
	Manifest           *dataplatform.QuarantineManifest `json:"structural_manifest"`
	OccurrenceCount    int64                            `json:"occurrence_count"`
	ContractLocators   []string                         `json:"contract_locators"`
	FixtureLocators    []string                         `json:"fixture_locators"`
	ValidationCommands []string                         `json:"validation_commands"`
	AgentPrompt        string                           `json:"agent_prompt"`
	Exclusions         []string                         `json:"exclusions"`
}

func (a *API) buildIncidentDebugBundle(
	ctx context.Context,
	incidentID string,
) (incidentDebugBundle, error) {
	started := time.Now()
	incident, err := dataplatform.GetIncident(ctx, a.pool, incidentID)
	if err != nil {
		return incidentDebugBundle{}, err
	}
	bundle := incidentDebugBundle{
		SchemaVersion: "kansoku.incident-debug-bundle/1",
		Incident:      incident, OccurrenceCount: incident.OccurrenceCount,
		ContractLocators: []string{
			"contracts/incidents/model.yaml", "contracts/incidents/quarantine.yaml",
		},
		FixtureLocators: []string{"tests/fixtures/session-12/unknown-schema-canary.json"},
		ValidationCommands: []string{
			"python3 scripts/validate_incidents.py",
			"go test ./internal/dataplatform ./internal/integrity ./internal/runtime",
		},
		AgentPrompt: "Investigate this metadata-only incident read-only. Create a sanitized fixture, update the version-bounded parser, run privacy and replay tests, then require fresh supported evidence and a passing targeted audit. Do not request or persist the original payload.",
		Exclusions: []string{
			"raw_payload", "raw_error", "user_notes", "host_paths", "configuration_values",
		},
	}
	if incident.SchemaFingerprint != nil {
		manifests, _, listErr := dataplatform.ListQuarantine(
			ctx, a.pool, *incident.SchemaFingerprint, "", nil, nil, nil, 100,
		)
		if listErr != nil {
			return incidentDebugBundle{}, listErr
		}
		for index := range manifests {
			if manifests[index].IncidentID == incidentID {
				value := manifests[index]
				bundle.Manifest = &value
				break
			}
		}
	}
	budget := dataplatform.Budgets["incident_debug_bundle"]
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return incidentDebugBundle{}, &dataplatform.ErrBudgetExceeded{
			BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed,
		}
	}
	return bundle, nil
}

func (a *API) incidentDebugBundle(writer http.ResponseWriter, request *http.Request, incidentID string) {
	if request.Method != http.MethodGet {
		a.writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	format := request.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "markdown" {
		a.writeError(writer, http.StatusBadRequest, "invalid_debug_bundle_format")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	bundle, err := a.buildIncidentDebugBundle(ctx, incidentID)
	if dataplatform.IsNotFound(err) {
		a.writeError(writer, http.StatusNotFound, "incident_not_found")
		return
	}
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "debug_bundle_unavailable")
		return
	}
	if format == "json" {
		a.write(writer, http.StatusOK, bundle, map[string]any{
			"status": "complete", "numerator": 1, "denominator": 1,
			"exclusions": bundle.Exclusions,
		})
		return
	}
	body := renderIncidentDebugMarkdown(bundle)
	if int64(len(body)) > a.config.ResponseMaxBytes ||
		len(privacy.ScanSecretFormats(privacy.SinkSnapshot{"incident_debug_markdown": []byte(body)})) != 0 {
		a.writeError(writer, http.StatusInternalServerError, "debug_bundle_too_large")
		return
	}
	writer.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(body))
}

func renderIncidentDebugMarkdown(bundle incidentDebugBundle) string {
	incident := bundle.Incident
	var builder strings.Builder
	builder.WriteString("# Kansoku incident debug bundle\n\n")
	builder.WriteString("- Incident: `" + markdownSafe(incident.IncidentID) + "`\n")
	builder.WriteString("- Detector: `" + markdownSafe(incident.DetectorState) + "`\n")
	builder.WriteString("- Triage: `" + markdownSafe(incident.TriageState) + "`\n")
	builder.WriteString("- Capability: `" + markdownSafe(incident.CapabilityID) + "`\n")
	builder.WriteString("- Failure class: `" + markdownSafe(incident.FailureClass) + "`\n")
	builder.WriteString("- Occurrences: " + strconv.FormatInt(incident.OccurrenceCount, 10) + "\n")
	builder.WriteString("- First seen: `" + incident.FirstSeenAt.UTC().Format(time.RFC3339Nano) + "`\n")
	builder.WriteString("- Last seen: `" + incident.LastSeenAt.UTC().Format(time.RFC3339Nano) + "`\n")
	if incident.SchemaFingerprint != nil {
		builder.WriteString("- Schema fingerprint: `" + markdownSafe(*incident.SchemaFingerprint) + "`\n")
	}
	builder.WriteString("\n## Read-only investigation prompt\n\n")
	builder.WriteString(markdownSafe(bundle.AgentPrompt) + "\n\n")
	builder.WriteString("## Commands\n\n")
	for _, command := range bundle.ValidationCommands {
		builder.WriteString("- `" + markdownSafe(command) + "`\n")
	}
	builder.WriteString("\nExcluded: original payload, raw errors, user notes, host paths, and configuration values.\n")
	return builder.String()
}

func markdownSafe(value string) string {
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z',
			char >= '0' && char <= '9', strings.ContainsRune(" ._:@|/+-(),", char):
			builder.WriteRune(char)
		default:
			builder.WriteByte('?')
		}
		if builder.Len() >= 4096 {
			break
		}
	}
	return builder.String()
}
