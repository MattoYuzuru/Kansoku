/*
 * TypeScript mirrors of the /api/v1 response shapes from
 * internal/dataplatform/types.go and internal/runtime/api.go. Field names and
 * nullability match the Go JSON tags exactly (RFC3339 timestamps arrive as
 * strings on the wire). This module has no fetch logic — see api/queries.ts.
 */

export interface Percentiles {
  p50?: number | null;
  p90?: number | null;
  p95?: number | null;
  p99?: number | null;
}

export interface Population {
  numerator: number;
  denominator: number;
}

/** Matches internal/dataplatform.Completeness (distinct from the envelope's Completeness). */
export interface DataCompleteness {
  status: string;
  covered_ratio: number;
  intervals: string[];
}

export interface Freshness {
  rollup_watermark: string;
  late_events_pending: number;
}

/* ---- /api/v1/inventory ---- */
export interface InventoryCounts {
  agent_installations: number;
  agent_surfaces: number;
  projects: number;
  sessions: number;
  components: number;
  adapter_versions: number;
  source_instances: number;
}

/* ---- /api/v1/analytics?budget_id=hourly_rollup_range_30d|daily_rollup_range_1y ---- */
export interface RollupPoint {
  bucket_start: string;
  value: number | null;
  percentiles?: Percentiles;
  event_count: number;
}
export interface QueryResponse {
  data: RollupPoint[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  freshness: Freshness;
}

/* ---- /api/v1/analytics?budget_id=agent_breakdown_range|model_breakdown_range|component_breakdown_range ---- */
export interface EntityRow {
  entity_id: string;
  agent_id?: string;
  event_count: number;
  success_count: number;
  failure_count: number;
  costed_count?: number;
  estimated_cost_micros?: number;
  value?: number | null;
  percentiles?: Percentiles;
}
export interface EntityBreakdownResponse {
  data: EntityRow[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  freshness: Freshness;
}

/* ---- /api/v1/analytics?budget_id=component_lifecycle_funnel ---- */
export type LifecycleStage =
  | "opportunity_detected"
  | "installed"
  | "enabled"
  | "exposed"
  | "invoked"
  | "loaded"
  | "executed"
  | "succeeded"
  | string;
export interface FunnelStageRow {
  stage: LifecycleStage;
  component_count: number;
  event_count: number;
  value_state: "complete" | "numeric_zero" | "not_observed" | "unknown";
}
export interface FunnelResponse {
  data: FunnelStageRow[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  freshness: Freshness;
}

/* ---- /api/v1/analytics?budget_id=reliability_coverage_timeline ---- */
export interface ReliabilityDayRow {
  day: string;
  source_instance_id: string;
  status: string;
  interval_count: number;
}
export interface ReliabilityTimelineResponse {
  data: ReliabilityDayRow[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  freshness: Freshness;
}

/* ---- /api/v1/components/mcp/topology ---- */
export interface ComponentTreeNode {
  component_id: string;
  kind: string;
  child_component_ids: string[];
  latest_connection_state?: string;
  connection_observed_at?: string;
}
export interface ComponentTopologyResponse {
  data: ComponentTreeNode[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  // no freshness field on this endpoint
}

/* ---- /api/v1/activity ---- */
export interface ActivityDayRow {
  day: string;
  session_count: number;
  prompt_count: number;
  active_duration_seconds: number | null;
}
export interface ActivityTimelineResponse {
  data: ActivityDayRow[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  freshness: Freshness;
}

/* ---- /api/v1/prompts/shape ---- */
export interface PromptShapeDayRow {
  day: string;
  prompt_count: number;
  percentiles?: Percentiles;
  character_percentiles?: Percentiles;
}
export interface PromptShapeResponse {
  data: PromptShapeDayRow[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  freshness: Freshness;
}

/* ---- /api/v1/models/usage ---- */
export interface ModelUsageDayRow {
  day: string;
  request_count: number;
  total_tokens: number;
  estimated_cost_micros: number;
  costed_request_count: number;
  provider_cost_count: number;
  upper_bound_cost_count: number;
  percentiles?: Percentiles;
  error_ratio?: number | null;
  matched_event_count: number;
}
export interface ModelUsageResponse {
  data: ModelUsageDayRow[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  freshness: Freshness;
}

/* ---- /api/v1/tools/analytics ---- */
export interface ToolAnalyticsDayRow {
  day: string;
  call_count: number;
  success_count: number;
  failure_count: number;
  percentiles?: Percentiles;
}
export interface ToolAnalyticsResponse {
  data: ToolAnalyticsDayRow[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  freshness: Freshness;
}

/* ---- /api/v1/components/mcp/uptime ---- */
export interface MCPUptimeRow {
  component_id: string;
  connected_seconds: number;
  observable_seconds: number;
  uptime_ratio?: number | null;
}
export interface MCPUptimeResponse {
  data: MCPUptimeRow[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  // no freshness field on this endpoint
}

/* ---- /api/v1/reliability/counts ---- */
export interface ReliabilityCountsDayRow {
  day: string;
  unknown_schema_count: number;
  reconciliation_mismatch_count: number;
}
export interface ReliabilityCountsResponse {
  data: ReliabilityCountsDayRow[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  // no freshness field on this endpoint
}

export interface CollectionHealthSnapshot {
  accepted_event_count: number;
  quarantined_record_count: number;
  ingest_latency_p95_ms?: number | null;
  active_source_count: number;
  source_gap_count: number;
  oldest_source_age_seconds?: number | null;
  pending_rollup_count: number;
  rollup_age_seconds?: number | null;
  queue_depth: number;
  oldest_queue_age_seconds: number;
  formula_version: string;
}

/* ---- /api/v1/system/snapshot (flat, no `data` array, no from/to) ---- */
export interface SystemSnapshotResponse {
  database_size_bytes: number;
  backup_age_seconds: number | null;
  backup_checksum_ok?: boolean;
  restore_test_age_seconds: number | null;
  restore_test_passed?: boolean;
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  // no freshness field on this endpoint
}

/* ---- /api/v1/privacy/canary-history ---- */
export interface PrivacyCanaryDayRow {
  day: string;
  pass_count: number;
  fail_count: number;
}
export interface PrivacyCanaryHistoryResponse {
  data: PrivacyCanaryDayRow[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  // no freshness field on this endpoint
}

/* ---- /api/v1/incidents ---- */
export interface Incident {
  incident_id: string;
  installation_id: string;
  source_id: string;
  capability_id: string;
  failure_class: string;
  first_seen_at: string;
  recovery_criteria: string;
}

/* ---- /api/v1/completeness ---- */
export interface CompletenessSummary {
  numerator: number;
  denominator: number;
  exclusions: string[];
  completeness: string;
}
