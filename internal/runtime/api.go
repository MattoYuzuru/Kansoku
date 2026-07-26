package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/integrity"
	"kansoku.local/kansoku/internal/localhttp"
)

const APIVersion = "kansoku.api/1"

var safeQueryID = regexp.MustCompile(`^[a-zA-Z0-9_.:@|-]{1,256}$`)

type APIEnvelope struct {
	APIVersion   string `json:"api_version"`
	RequestID    string `json:"request_id"`
	Data         any    `json:"data,omitempty"`
	Completeness any    `json:"completeness,omitempty"`
	Error        string `json:"error,omitempty"`
}

type RetentionPreviewRequest struct {
	HorizonDays int `json:"horizon_days"`
}

type RetentionApplyRequest struct {
	RequestID        string `json:"request_id"`
	HorizonDays      int    `json:"horizon_days"`
	ParametersSHA256 string `json:"parameters_sha256"`
	ApprovalNonce    string `json:"approval_nonce"`
}

type ExportRequest struct{}
type ImportRequest struct {
	ExportID       string `json:"export_id"`
	IdempotencyKey string `json:"idempotency_key"`
}
type BackupRequest struct{}
type RestoreVerifyRequest struct {
	BackupID string `json:"backup_id"`
}
type DiagnosticsRequest struct{}

type AdminOperations interface {
	PreviewRetention(RetentionPreviewRequest) (any, error)
	ApplyRetention(context.Context, RetentionApplyRequest) (any, error)
	Export(context.Context, ExportRequest) (any, error)
	Import(context.Context, ImportRequest) (any, error)
	Backup(context.Context, BackupRequest) (any, error)
	RestoreVerify(context.Context, RestoreVerifyRequest) (any, error)
	Diagnostics(context.Context, DiagnosticsRequest) (any, error)
}

type API struct {
	pool       *pgxpool.Pool
	queue      *DurableIngressQueue
	plans      *PlanManager
	jobs       *JobManager
	operations AdminOperations
	config     Config
}

func NewAPI(config Config, secrets Secrets, pool *pgxpool.Pool, queue *DurableIngressQueue, plans *PlanManager, jobs *JobManager, operations AdminOperations) (http.Handler, error) {
	if err := config.Validate(); err != nil || pool == nil || queue == nil || plans == nil || jobs == nil || operations == nil {
		return nil, errors.New("invalid_api_configuration")
	}
	readGuard, err := localhttp.NewApplianceGuard(secrets.ReadBearer, secrets.CSRF, 1<<20, 120, time.Minute, config.ContainerMode)
	if err != nil {
		return nil, err
	}
	mutationGuard, err := localhttp.NewApplianceGuard(secrets.MutationBearer, secrets.CSRF, 1<<20, 120, time.Minute, config.ContainerMode)
	if err != nil {
		return nil, err
	}
	api := &API{pool: pool, queue: queue, plans: plans, jobs: jobs, operations: operations, config: config}
	mux := http.NewServeMux()
	for route, handler := range map[string]http.HandlerFunc{
		"/api/v1/inventory":                     api.inventory,
		"/api/v1/analytics":                     api.analytics,
		"/api/v1/health":                        api.health,
		"/api/v1/incidents":                     api.incidents,
		"/api/v1/completeness":                  api.completeness,
		"/api/v1/operations/jobs":               api.jobRuns,
		"/api/v1/components/mcp/topology":       api.mcpTopology,
		"/api/v1/components/inventory":          api.componentInventory,
		"/api/v1/activity":                      api.activityTimeline,
		"/api/v1/prompts/shape":                 api.promptShape,
		"/api/v1/models/usage":                  api.modelUsage,
		"/api/v1/tools/analytics":               api.toolAnalytics,
		"/api/v1/components/mcp/uptime":         api.mcpUptime,
		"/api/v1/reliability/counts":            api.reliabilityCounts,
		"/api/v1/reliability/collection-health": api.collectionHealth,
		"/api/v1/system/snapshot":               api.systemSnapshot,
		"/api/v1/privacy/canary-history":        api.privacyCanaryHistory,
	} {
		mux.Handle(route, readGuard.Wrap(localhttp.RouteUIStream, handler))
	}
	for route, handler := range map[string]http.HandlerFunc{
		"/api/v1/plans/preview":           api.planPreview,
		"/api/v1/plans/apply":             api.planApply,
		"/api/v1/admin/retention/preview": api.retentionPreview,
		"/api/v1/admin/retention/apply":   api.retentionApply,
		"/api/v1/admin/export":            api.export,
		"/api/v1/admin/import":            api.importData,
		"/api/v1/admin/backup":            api.backup,
		"/api/v1/admin/restore-verify":    api.restoreVerify,
		"/api/v1/admin/diagnostics":       api.diagnostics,
	} {
		mux.Handle(route, mutationGuard.Wrap(localhttp.RouteUIMutation, handler))
	}
	return mux, nil
}

func (a *API) componentInventory(writer http.ResponseWriter, request *http.Request) {
	kind := request.URL.Query().Get("kind")
	switch kind {
	case "", "skill", "plugin", "mcp", "hook", "command":
	default:
		a.writeError(writer, http.StatusBadRequest, "invalid_component_kind")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.ComponentInventory(ctx, a.pool, kind)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "component_inventory_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
}

type CollectionHealthSnapshot struct {
	AcceptedEventCount     int64    `json:"accepted_event_count"`
	QuarantinedRecordCount int64    `json:"quarantined_record_count"`
	IngestLatencyP95MS     *float64 `json:"ingest_latency_p95_ms,omitempty"`
	ActiveSourceCount      int64    `json:"active_source_count"`
	SourceGapCount         int64    `json:"source_gap_count"`
	OldestSourceAgeSeconds *float64 `json:"oldest_source_age_seconds,omitempty"`
	PendingRollupCount     int64    `json:"pending_rollup_count"`
	RollupAgeSeconds       *float64 `json:"rollup_age_seconds,omitempty"`
	QueueDepth             int64    `json:"queue_depth"`
	OldestQueueAgeSeconds  float64  `json:"oldest_queue_age_seconds"`
	FormulaVersion         string   `json:"formula_version"`
}

func (a *API) collectionHealth(writer http.ResponseWriter, request *http.Request) {
	from, fromErr := time.Parse(time.RFC3339, request.URL.Query().Get("from"))
	to, toErr := time.Parse(time.RFC3339, request.URL.Query().Get("to"))
	if fromErr != nil || toErr != nil || !to.After(from) || to.Sub(from) > 366*24*time.Hour {
		a.writeError(writer, http.StatusBadRequest, "invalid_collection_health_range")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	var result CollectionHealthSnapshot
	if err := a.pool.QueryRow(ctx, `
		SELECT count(*),
			percentile_cont(0.95) WITHIN GROUP (
				ORDER BY extract(epoch FROM (ingested_at - observed_at)) * 1000
			)
		FROM events
		WHERE observed_at >= $1 AND observed_at < $2
	`, from, to).Scan(&result.AcceptedEventCount, &result.IngestLatencyP95MS); err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "collection_health_unavailable")
		return
	}
	if err := a.pool.QueryRow(ctx, `
		SELECT coalesce(sum(record_count), 0)
		FROM schema_quarantine_metadata
		WHERE observed_at >= $1 AND observed_at < $2
	`, from, to).Scan(&result.QuarantinedRecordCount); err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "collection_health_unavailable")
		return
	}
	if err := a.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE NOT inactivity), coalesce(sum(gap_count), 0),
			max(extract(epoch FROM (now() - last_committed_at)))
				FILTER (WHERE NOT inactivity AND last_committed_at IS NOT NULL)
		FROM source_watermarks
	`).Scan(&result.ActiveSourceCount, &result.SourceGapCount, &result.OldestSourceAgeSeconds); err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "collection_health_unavailable")
		return
	}
	if err := a.pool.QueryRow(ctx, `
		SELECT coalesce(sum(late_events_pending), 0),
			max(extract(epoch FROM (now() - rollup_watermark)))
				FILTER (WHERE rollup_watermark IS NOT NULL)
		FROM rollup_status
	`).Scan(&result.PendingRollupCount, &result.RollupAgeSeconds); err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "collection_health_unavailable")
		return
	}
	queueMetrics, err := a.queue.Metrics()
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "collection_health_unavailable")
		return
	}
	now := time.Now().UTC()
	for source, depth := range queueMetrics.Depth {
		result.QueueDepth += int64(depth)
		if oldest := queueMetrics.OldestSpoolRecord[source]; !oldest.IsZero() {
			if age := now.Sub(oldest).Seconds(); age > result.OldestQueueAgeSeconds {
				result.OldestQueueAgeSeconds = age
			}
		}
	}
	result.FormulaVersion = "collection_health_snapshot/1"
	denominator := result.AcceptedEventCount + result.QuarantinedRecordCount
	completeness := "unknown"
	if denominator > 0 {
		completeness = "complete"
	}
	a.write(writer, http.StatusOK, result, map[string]any{
		"numerator": result.AcceptedEventCount, "denominator": denominator,
		"exclusions": []string{}, "completeness": completeness,
	})
}

func (a *API) inventory(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	counts := map[string]int64{}
	for _, table := range []string{
		"agent_installations", "agent_surfaces", "projects", "sessions",
		"components", "component_installations", "inventory_snapshots",
		"adapter_versions", "source_instances",
	} {
		var count int64
		if err := a.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			a.writeError(writer, http.StatusServiceUnavailable, "inventory_unavailable")
			return
		}
		counts[table] = count
	}
	for _, state := range []string{"complete", "partial", "degraded", "not_observed"} {
		var count int64
		if err := a.pool.QueryRow(ctx, `
			SELECT count(*) FROM inventory_collection_status WHERE state = $1
		`, state).Scan(&count); err != nil {
			a.writeError(writer, http.StatusServiceUnavailable, "inventory_unavailable")
			return
		}
		counts["inventory_targets_"+state] = count
	}
	a.write(writer, http.StatusOK, counts, map[string]any{"status": "complete", "exclusions": []string{}})
}

// entityBreakdownBudgetIDs are the "group and rank across entities within a
// time range" budget_id values analytics() accepts in addition to the
// original single-dimension-scope rollup budgets. Each is backed by a
// dedicated internal/dataplatform aggregation function rather than
// metric_rollups_*, since dimension_scope there names exactly one
// already-known entity and cannot express a leaderboard/funnel/timeline.
var entityBreakdownBudgetIDs = map[string]bool{
	"agent_breakdown_range":         true,
	"model_breakdown_range":         true,
	"component_breakdown_range":     true,
	"component_lifecycle_funnel":    true,
	"reliability_coverage_timeline": true,
}

func (a *API) analytics(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	budgetID, metric, granularity, scope := query.Get("budget_id"), query.Get("metric_family"), query.Get("granularity"), query.Get("dimension_scope")
	from, to, bucket, ok := parseAnalyticsRange(query)
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_range")
		return
	}
	if entityBreakdownBudgetIDs[budgetID] {
		a.analyticsEntityBreakdown(writer, request, budgetID, metric, from, to, bucket)
		return
	}
	if !safeQueryID.MatchString(metric) || !safeQueryID.MatchString(scope) ||
		(budgetID != "hourly_rollup_range_30d" && budgetID != "daily_rollup_range_1y") {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_query")
		return
	}
	unit := dataplatform.GranularityHourly
	if granularity == "daily" {
		unit = dataplatform.GranularityDaily
	} else if granularity != "hourly" {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_query")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.RollupRange(ctx, a.pool, budgetID, metric, unit, scope, from, to)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
		return
	}
	coverage := map[string]any{
		"numerator": result.Population.Numerator, "denominator": result.Population.Denominator,
		"exclusions": []string{}, "completeness": result.Completeness.Status,
	}
	a.write(writer, http.StatusOK, result, coverage)
}

// analyticsEntityBreakdown dispatches the entity-breakdown/funnel/timeline
// family of budget_id values. metric_family selects which aggregation runs;
// for component_breakdown_range and component_lifecycle_funnel, metric_family
// doubles as an optional component_kind filter (empty string means every
// kind) since contracts/metrics.yaml's component.* metrics are already
// dimensioned by component_kind and this reuses that same query parameter
// rather than inventing a second one.
func (a *API) analyticsEntityBreakdown(writer http.ResponseWriter, request *http.Request, budgetID, metric string, from, to time.Time, bucket dataplatform.TimeBucketSpec) {
	if metric != "" && !safeQueryID.MatchString(metric) {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_query")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()

	switch budgetID {
	case "agent_breakdown_range":
		result, err := dataplatform.AgentBreakdown(ctx, a.pool, from, to)
		if err != nil {
			a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
			return
		}
		a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
	case "model_breakdown_range":
		result, err := dataplatform.ModelBreakdown(ctx, a.pool, from, to)
		if err != nil {
			a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
			return
		}
		a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
	case "component_breakdown_range":
		result, err := dataplatform.ComponentBreakdown(ctx, a.pool, metric, from, to)
		if err != nil {
			a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
			return
		}
		a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
	case "component_lifecycle_funnel":
		result, err := dataplatform.ComponentLifecycleFunnel(ctx, a.pool, metric, from, to)
		if err != nil {
			a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
			return
		}
		a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
	case "reliability_coverage_timeline":
		result, err := dataplatform.ReliabilityCoverageTimeline(ctx, a.pool, from, to, bucket)
		if err != nil {
			a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
			return
		}
		a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
	default:
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_query")
	}
}

func entityCoverage(population dataplatform.Population, completeness dataplatform.Completeness) map[string]any {
	return map[string]any{
		"numerator": population.Numerator, "denominator": population.Denominator,
		"exclusions": []string{}, "completeness": completeness.Status,
	}
}

// mcpTopology serves the one dedicated new route named by ADR 0013
// decision #12: an MCP server/tool relationship tree, which no existing
// metric_family/dimension_scope pair can represent since dimension_scope is
// a flat single-entity scope, never a parent/child graph. Accepts the same
// [from, to) range convention as analytics.
func (a *API) mcpTopology(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	from, to, _, ok := parseAnalyticsRange(query)
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_range")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.MCPTopology(ctx, a.pool, from, to)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
}

// parseAnalyticsRange applies one [from,to), timezone and bucket contract to
// every range-taking route. Empty granularity/timezone values retain the
// historical daily/UTC behavior for API compatibility.
func parseAnalyticsRange(query url.Values) (from, to time.Time, bucket dataplatform.TimeBucketSpec, ok bool) {
	from, fromErr := time.Parse(time.RFC3339, query.Get("from"))
	to, toErr := time.Parse(time.RFC3339, query.Get("to"))
	granularity := query.Get("granularity")
	if granularity == "" {
		granularity = string(dataplatform.GranularityDaily)
	}
	timezone := query.Get("timezone")
	if timezone == "" {
		timezone = "UTC"
	}
	bucket, bucketErr := dataplatform.NewTimeBucketSpec(granularity, timezone)
	if fromErr != nil || toErr != nil || bucketErr != nil || !bucket.ValidateRange(from, to) {
		return time.Time{}, time.Time{}, dataplatform.TimeBucketSpec{}, false
	}
	return from, to, bucket, true
}

// activityTimeline serves the "/" overview-activity panel and the /activity
// activity-timeline panel: distinct session/prompt counts and a
// reconstructed active-duration estimate per calendar day in range.
func (a *API) activityTimeline(writer http.ResponseWriter, request *http.Request) {
	from, to, bucket, ok := parseAnalyticsRange(request.URL.Query())
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_range")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.ActivityTimeline(ctx, a.pool, from, to, bucket)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
}

// promptShape serves the /prompts "prompt-shape" panel: per-day submitted
// prompt count and exact byte-length percentiles.
func (a *API) promptShape(writer http.ResponseWriter, request *http.Request) {
	from, to, bucket, ok := parseAnalyticsRange(request.URL.Query())
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_range")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.PromptShape(ctx, a.pool, from, to, bucket)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
}

// modelUsage serves the /models "model-usage" and "model-cost" panels: the
// per-day time-series companion to the model_breakdown_range leaderboard.
func (a *API) modelUsage(writer http.ResponseWriter, request *http.Request) {
	from, to, bucket, ok := parseAnalyticsRange(request.URL.Query())
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_range")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.ModelUsage(ctx, a.pool, from, to, bucket)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
}

// toolAnalytics serves the /tools "tool-analytics" panel and the
// /components/mcp "mcp-health" panel's calls/errors/latency series, optionally
// restricted to one component via the component_id query param (empty means
// every component, matching ComponentBreakdown's "" == all-kinds convention).
func (a *API) toolAnalytics(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	from, to, bucket, ok := parseAnalyticsRange(query)
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_range")
		return
	}
	componentID := query.Get("component_id")
	if componentID != "" && !safeQueryID.MatchString(componentID) {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_query")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.ToolAnalytics(ctx, a.pool, componentID, from, to, bucket)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
}

// mcpUptime serves /components/mcp "mcp-health"'s connection-uptime ratio:
// per MCP-server component, the fraction of its own observable window spent
// in the "connected" state.
func (a *API) mcpUptime(writer http.ResponseWriter, request *http.Request) {
	from, to, _, ok := parseAnalyticsRange(request.URL.Query())
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_range")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.MCPUptime(ctx, a.pool, from, to)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
}

// reliabilityCounts serves the "/" overview-incidents panel and the
// /reliability "reliability-drift" panel: per-day unknown-schema and
// reconciliation-mismatch counts.
func (a *API) reliabilityCounts(writer http.ResponseWriter, request *http.Request) {
	from, to, bucket, ok := parseAnalyticsRange(request.URL.Query())
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_range")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.ReliabilityCounts(ctx, a.pool, from, to, bucket)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
}

// systemSnapshot serves the /privacy "privacy-retention", /system
// "system-recovery" and /settings "settings-impact-preview" panels: a single
// non-time-series snapshot of durable database-size and backup/restore-test
// age facts. Unlike every other route in this file, it takes no from/to
// range -- it is a live snapshot, not a bucketed series.
func (a *API) systemSnapshot(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.SystemSnapshot(ctx, a.pool)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
}

// privacyCanaryHistory serves the /privacy "privacy-canary" panel: per-day
// pass/fail counts for the integrity privacy-canary check, an honest
// check-history timeline rather than a fabricated exact violation count.
func (a *API) privacyCanaryHistory(writer http.ResponseWriter, request *http.Request) {
	from, to, bucket, ok := parseAnalyticsRange(request.URL.Query())
	if !ok {
		a.writeError(writer, http.StatusBadRequest, "invalid_analytics_range")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	result, err := dataplatform.PrivacyCanaryHistory(ctx, a.pool, from, to, bucket)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "analytics_unavailable")
		return
	}
	a.write(writer, http.StatusOK, result, entityCoverage(result.Population, result.Completeness))
}

func (a *API) health(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	if err := a.pool.Ping(ctx); err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "database_unavailable")
		return
	}
	if err := verifyMigrationLedgers(ctx, a.pool); err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "migration_ledgers_unavailable")
		return
	}
	metrics, err := a.queue.Metrics()
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "queue_health_unavailable")
		return
	}
	if !a.queue.IsAccepting() {
		a.writeError(writer, http.StatusServiceUnavailable, "ingress_workers_draining")
		return
	}
	var openIncidents int64
	if err := a.pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT incident_id FROM integrity_incidents WHERE resolved_at IS NULL
			UNION
			SELECT incident_id FROM incidents WHERE resolved_at IS NULL
		) open_incidents
	`).Scan(&openIncidents); err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "integrity_health_unavailable")
		return
	}
	a.write(writer, http.StatusOK, map[string]any{
		"database": "pass", "migration_ledgers": "pass", "spool": "pass",
		"workers": "pass", "open_incident_count": openIncidents,
		"queue_depth": metrics.Depth, "oldest_spooled_at": metrics.OldestSpoolRecord,
	}, map[string]any{"status": "complete", "exclusions": []string{}})
}

func (a *API) incidents(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	incidents, err := integrity.ListOpenIncidents(ctx, a.pool)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "incidents_unavailable")
		return
	}
	if len(incidents) > 500 {
		incidents = incidents[:500]
	}
	type safeIncident struct {
		IncidentID       string    `json:"incident_id"`
		InstallationID   string    `json:"installation_id"`
		SourceID         string    `json:"source_id"`
		CapabilityID     string    `json:"capability_id"`
		FailureClass     string    `json:"failure_class"`
		FirstSeenAt      time.Time `json:"first_seen_at"`
		RecoveryCriteria string    `json:"recovery_criteria"`
	}
	safe := make([]safeIncident, 0, len(incidents))
	for _, incident := range incidents {
		safe = append(safe, safeIncident{
			IncidentID: incident.IncidentID, InstallationID: incident.InstallationID,
			SourceID: incident.SourceID, CapabilityID: incident.CapabilityID,
			FailureClass: string(incident.FailureClass), FirstSeenAt: incident.FirstSeenAt,
			RecoveryCriteria: incident.RecoveryCriteria,
		})
	}
	// Unknown-schema records are durably quarantined by the observability
	// ingress before the integrity audit runs. They therefore live in the
	// original incidents table, while audit findings live in
	// integrity_incidents + integrity_incident_details. Both are real open
	// incidents and must be visible through the one public incident API.
	rows, err := a.pool.Query(ctx, `
		SELECT incident_id, category, opened_at
		FROM incidents
		WHERE resolved_at IS NULL
		ORDER BY opened_at
		LIMIT $1
	`, 500-len(safe))
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "incidents_unavailable")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var incident safeIncident
		if err := rows.Scan(&incident.IncidentID, &incident.FailureClass, &incident.FirstSeenAt); err != nil {
			a.writeError(writer, http.StatusServiceUnavailable, "incidents_unavailable")
			return
		}
		incident.InstallationID = "not_observed"
		incident.SourceID = "not_observed"
		incident.CapabilityID = "core_ingestion"
		incident.RecoveryCriteria = "ingest a supported schema and complete a successful replay"
		safe = append(safe, incident)
	}
	if err := rows.Err(); err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "incidents_unavailable")
		return
	}
	a.write(writer, http.StatusOK, safe, map[string]any{"status": "complete", "exclusions": []string{"user_notes"}})
}

func (a *API) completeness(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	var known, total int64
	if err := a.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE value_state NOT IN ('unknown','not_observed')),
		       count(*) FROM events
	`).Scan(&known, &total); err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "completeness_unavailable")
		return
	}
	status := "unknown"
	if total > 0 && known == total {
		status = "complete"
	} else if total > 0 {
		status = "partial"
	}
	a.write(writer, http.StatusOK, map[string]any{
		"numerator": known, "denominator": total, "exclusions": []string{},
		"completeness": status,
	}, map[string]any{"status": status, "exclusions": []string{}})
}

func (a *API) jobRuns(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), a.config.QueryTimeout())
	defer cancel()
	runs, err := a.jobs.List(ctx)
	if err != nil {
		a.writeError(writer, http.StatusServiceUnavailable, "jobs_unavailable")
		return
	}
	a.write(writer, http.StatusOK, runs, map[string]any{"status": "complete", "exclusions": []string{}})
}

func (a *API) planPreview(writer http.ResponseWriter, request *http.Request) {
	var input PlanPreviewRequest
	if !a.decode(writer, request, &input) {
		return
	}
	result, err := a.plans.Preview(input)
	if err != nil {
		a.writeError(writer, http.StatusBadRequest, safeOperationError(err))
		return
	}
	a.write(writer, http.StatusOK, result, map[string]any{"status": "complete", "exclusions": []string{}})
}

func (a *API) planApply(writer http.ResponseWriter, request *http.Request) {
	var input PlanApplyRequest
	if !a.decode(writer, request, &input) {
		return
	}
	result, err := a.plans.Apply(request.Context(), input)
	if err != nil {
		a.writeError(writer, http.StatusConflict, safeOperationError(err))
		return
	}
	a.write(writer, http.StatusOK, result, map[string]any{"status": "complete", "exclusions": []string{}})
}

func (a *API) retentionPreview(writer http.ResponseWriter, request *http.Request) {
	var input RetentionPreviewRequest
	if !a.decode(writer, request, &input) {
		return
	}
	result, err := a.operations.PreviewRetention(input)
	a.adminResult(writer, result, err)
}

func (a *API) retentionApply(writer http.ResponseWriter, request *http.Request) {
	var input RetentionApplyRequest
	if !a.decode(writer, request, &input) {
		return
	}
	result, err := a.operations.ApplyRetention(request.Context(), input)
	a.adminResult(writer, result, err)
}

func (a *API) export(writer http.ResponseWriter, request *http.Request) {
	var input ExportRequest
	if !a.decode(writer, request, &input) {
		return
	}
	result, err := a.operations.Export(request.Context(), input)
	a.adminResult(writer, result, err)
}

func (a *API) importData(writer http.ResponseWriter, request *http.Request) {
	var input ImportRequest
	if !a.decode(writer, request, &input) {
		return
	}
	result, err := a.operations.Import(request.Context(), input)
	a.adminResult(writer, result, err)
}

func (a *API) backup(writer http.ResponseWriter, request *http.Request) {
	var input BackupRequest
	if !a.decode(writer, request, &input) {
		return
	}
	result, err := a.operations.Backup(request.Context(), input)
	a.adminResult(writer, result, err)
}

func (a *API) restoreVerify(writer http.ResponseWriter, request *http.Request) {
	var input RestoreVerifyRequest
	if !a.decode(writer, request, &input) {
		return
	}
	result, err := a.operations.RestoreVerify(request.Context(), input)
	a.adminResult(writer, result, err)
}

func (a *API) diagnostics(writer http.ResponseWriter, request *http.Request) {
	var input DiagnosticsRequest
	if !a.decode(writer, request, &input) {
		return
	}
	result, err := a.operations.Diagnostics(request.Context(), input)
	a.adminResult(writer, result, err)
}

func (a *API) adminResult(writer http.ResponseWriter, result any, err error) {
	if err != nil {
		a.writeError(writer, http.StatusConflict, safeOperationError(err))
		return
	}
	a.write(writer, http.StatusOK, result, map[string]any{"status": "complete", "exclusions": []string{}})
}

func (a *API) decode(writer http.ResponseWriter, request *http.Request, destination any) bool {
	decoder := json.NewDecoder(io.LimitReader(request.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		a.writeError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		a.writeError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func (a *API) write(writer http.ResponseWriter, status int, data, completeness any) {
	requestID, err := newOpaqueID("req")
	if err != nil {
		http.Error(writer, "response_unavailable", http.StatusInternalServerError)
		return
	}
	envelope := APIEnvelope{APIVersion: APIVersion, RequestID: requestID, Data: data, Completeness: completeness}
	encoded, err := json.Marshal(envelope)
	if err != nil || int64(len(encoded)) > a.config.ResponseMaxBytes || containsForbiddenResponseKey(encoded) {
		http.Error(writer, "response_policy_violation", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(append(encoded, '\n'))
}

func (a *API) writeError(writer http.ResponseWriter, status int, category string) {
	requestID, _ := newOpaqueID("req")
	envelope := APIEnvelope{APIVersion: APIVersion, RequestID: requestID, Error: category}
	encoded, _ := json.Marshal(envelope)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(append(encoded, '\n'))
}

var forbiddenResponseFields = map[string]bool{
	"prompt": true, "response": true, "content": true, "source_code": true,
	"tool_input": true, "tool_output": true, "environment": true,
	"credential": true, "raw_path": true, "sql_parameters": true,
}

func containsForbiddenResponseKey(encoded []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return true
	}
	var walk func(any) bool
	walk = func(item any) bool {
		switch typed := item.(type) {
		case map[string]any:
			for key, child := range typed {
				if forbiddenResponseFields[strings.ToLower(key)] || walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(value)
}

func safeOperationError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) <= 64 && safeErrorClass.MatchString(value) {
		return value
	}
	return "operation_failed"
}

func SafeErrorClass(err error) string {
	for current := err; current != nil; current = errors.Unwrap(current) {
		if value := current.Error(); len(value) <= 64 && safeErrorClass.MatchString(value) {
			return value
		}
	}
	return "operation_failed"
}

func boundedInt(value string, minimum, maximum int) (int, bool) {
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil && parsed >= minimum && parsed <= maximum
}
