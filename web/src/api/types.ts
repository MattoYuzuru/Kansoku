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
  status:
    | "complete"
    | "partial"
    | "degraded"
    | "unsupported"
    | "not_observed"
    | "redacted"
    | "unknown"
    | "numeric_zero";
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
  provider_id?: string;
  display_name?: string;
  display_alias?: string;
  surface_kind?: string;
  agent_version?: string;
  adapter_version?: string;
  installation_class?: "real" | "canary" | "fixture" | "imported" | "unknown";
  installation_class_provenance?: string;
  event_count: number;
  success_count: number;
  failure_count: number;
  costed_count?: number;
  estimated_cost_micros?: number;
  value?: number | null;
  percentiles?: Percentiles;
}

export interface AgentProfile {
  identity: {
    agent_installation_id: string;
    agent_id: string;
    adapter_id: string;
    provider_id: string;
    display_name: string;
    display_alias?: string;
    surface_kind: string;
    agent_version?: string;
    adapter_version?: string;
    completeness: DataCompleteness["status"];
    source_provenance: string;
    installation_class: "real" | "canary" | "fixture" | "imported" | "unknown";
    installation_class_provenance: string;
  };
  activity: {
    event_count: number;
    session_count: number;
    prompt_count: number;
    success_count: number;
    failure_count: number;
    tool_call_count: number;
    component_count: number;
    open_incident_count: number;
  };
  models: Array<{
    model_id: string;
    request_count: number;
    input_tokens: number;
    cached_input_tokens: number;
    output_tokens: number;
    costed_request_count: number;
    estimated_cost_micros: number;
    provider_costed_request_count: number;
    provider_cost_micros: number;
    api_estimated_request_count: number;
    api_equivalent_cost_micros: number;
    success_count: number;
    failure_count: number;
    percentiles?: Percentiles;
  }>;
  sources: Array<{
    source_instance_id: string;
    source_kind: string;
    adapter_version: string;
    fact_count: number;
    evidence_count: number;
    last_observed_at?: string;
    gap_count: number;
    state: string;
  }>;
  formula_version: string;
  population: Population;
  exclusions: Record<string, number>;
  completeness: DataCompleteness;
  freshness: Freshness;
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
  value_state: "complete" | "numeric_zero" | "not_observed" | "unsupported" | "unknown";
}
export interface FunnelResponse {
  data: FunnelStageRow[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  freshness: Freshness;
}

export interface InventoryComponentRow {
  component_id: string;
  declared_name: string;
  kind: string;
  source_scope: string;
  version?: string;
  version_state: "observed" | "not_observed";
  enabled: boolean;
  agent_id: string;
  agent_installation_id: string;
  first_seen_at: string;
  last_seen_at: string;
}
export interface InventoryComponentResponse {
  data: InventoryComponentRow[];
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
  error_numerator: number;
  error_denominator: number;
  error_excluded_count: number;
  matched_event_count: number;
}
export interface RatioMetric {
  value?: number | null;
  formula_version: string;
  population: Population;
  exclusions: Record<string, number>;
  completeness: DataCompleteness;
}
export interface ModelUsageResponse {
  data: ModelUsageDayRow[];
  formula_version: string;
  population: Population;
  completeness: DataCompleteness;
  freshness: Freshness;
  error_ratio_metric: RatioMetric;
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

/* ---- /api/v1/skills and /api/v1/skills/:id ---- */
export interface SkillModeCounts {
  explicit: number;
  proactive: number;
  nested: number;
}
export interface SkillObservatoryRow {
  component_installation_id: string;
  component_id: string;
  declared_name: string;
  version?: string;
  version_state: string;
  source_scope: string;
  agent_id: string;
  agent_installation_id: string;
  installed: boolean;
  enabled: boolean;
  exposed_count: number;
  invoked_count: number;
  loaded_count: number;
  child_activity_count: number;
  unique_sessions: number;
  active_days: number;
  last_invoked_at?: string;
  modes: SkillModeCounts;
  cold_state: "cold" | "used" | "not_observed";
  outcome_state: "observed" | "unsupported";
  completeness: string;
}
export interface SkillPlaneCounts {
  installed: number;
  enabled: number;
  exposed: number;
  invoked: number;
  loaded: number;
  cold: number;
}
export interface SkillObservatoryResponse {
  data: SkillObservatoryRow[];
  counts: SkillPlaneCounts;
  formula_version: string;
  population: Population;
  exclusions: Record<string, number>;
  completeness: DataCompleteness;
  freshness: Freshness;
}
export interface SkillAssertionRow {
  assertion_id: string;
  assertion_kind: string;
  mode: string;
  evidence_tier: string;
  confidence: number;
  source_kind: string;
  schema_version: string;
  observed_at: string;
  identity_resolution: string;
  candidate_count: number;
  outcome?: string;
  terminal_contract_id?: string;
}
export interface SkillSourceRow {
  source_instance_id: string;
  source_kind: string;
  assertion_count: number;
  exact_count: number;
  last_observed_at?: string;
  completeness: string;
}
export interface SkillFileTreeSummary {
  inventory_snapshot_id: string;
  file_count: number;
  directory_count: number;
  total_bytes: number;
  max_depth: number;
}
export interface SkillProfileResponse {
  identity: SkillObservatoryRow;
  assertions: SkillAssertionRow[];
  sources: SkillSourceRow[];
  file_tree: SkillFileTreeSummary[];
  incident_count: number;
  formula_version: string;
  population: Population;
  exclusions: Record<string, number>;
  completeness: DataCompleteness;
  freshness: Freshness;
}

/* ---- /api/v1/plugins and /api/v1/plugins/:id ---- */
export interface PluginObservatoryRow {
  component_installation_id: string;
  component_id: string;
  declared_name: string;
  version?: string;
  version_state: string;
  source_scope: string;
  agent_id: string;
  agent_installation_id: string;
  installed: boolean;
  enabled: boolean;
  loaded_count: number;
  loaded_sessions: number;
  child_activity_count: number;
  child_count: number;
  collision_count: number;
  last_loaded_at?: string;
  activity_state: "active" | "cold" | "disabled" | "not_observed";
  outcome_state: "unsupported";
  bundle_completeness: string;
}
export interface PluginPlaneCounts {
  installed: number;
  enabled: number;
  loaded: number;
  active: number;
  cold: number;
}
export interface PluginObservatoryResponse {
  data: PluginObservatoryRow[];
  counts: PluginPlaneCounts;
  formula_version: string;
  population: Population;
  exclusions: Record<string, number>;
  completeness: DataCompleteness;
  freshness: Freshness;
}
export interface PluginChildRow {
  component_id: string;
  component_kind: string;
  declared_name: string;
  relation_kind: string;
  version?: string;
  version_state: string;
  usage_count: number;
  last_activity_at?: string;
  relation_observed_at: string;
  relation_completeness: string;
}
export interface PluginVersionRow {
  version?: string;
  version_state: string;
  first_seen_at?: string;
  last_seen_at?: string;
  current: boolean;
}
export interface PluginProfileResponse {
  identity: PluginObservatoryRow;
  children: PluginChildRow[];
  versions: PluginVersionRow[];
  assertions: SkillAssertionRow[];
  sources: SkillSourceRow[];
  incident_count: number;
  formula_version: string;
  population: Population;
  exclusions: Record<string, number>;
  completeness: DataCompleteness;
  freshness: Freshness;
}

/* ---- /api/v1/components/mcp and server profiles ---- */
export interface MCPContourSupport {
  status: string;
  completeness: string;
}
export interface MCPServerRow {
  server_component_id: string;
  declared_name: string;
  configured: boolean;
  enabled: boolean;
  transport: string;
  locality: string;
  enumeration_completeness: string;
  primitive_count: number;
  tool_count: number;
  latest_connection_state: string;
  call_count: number;
  terminal_count: number;
  observable_seconds: number;
  connected_seconds: number;
  uptime_ratio?: number;
  inventory: MCPContourSupport;
  connection: MCPContourSupport;
  calls: MCPContourSupport;
}
export interface MCPObservatoryResponse {
  data: MCPServerRow[];
  formula_version: string;
  population: Population;
  exclusions: Record<string, number>;
  completeness: DataCompleteness;
}
export interface MCPPrimitiveRow {
  tool_component_id: string;
  declared_name: string;
  kind: string;
  schema_fingerprint?: string;
  description_byte_count?: number;
  schema_byte_count?: number;
  enumeration_completeness: string;
  last_advertised_at: string;
}
export interface MCPCallOutcomeCounts {
  started: number;
  completed: number;
  execution_error: number;
  protocol_error: number;
  cancelled: number;
  timed_out: number;
  denied: number;
  transport_lost: number;
  incomplete: number;
}
export interface MCPServerProfileResponse {
  identity: MCPServerRow;
  primitives: MCPPrimitiveRow[];
  outcomes: MCPCallOutcomeCounts;
  call_p95_ms?: number;
  formula_version: string;
  population: Population;
  exclusions: Record<string, number>;
  completeness: DataCompleteness;
}
export interface MCPPrimitiveListResponse {
  data: MCPPrimitiveRow[];
  formula_version: string;
  population: Population;
  exclusions: Record<string, number>;
  completeness: DataCompleteness;
}
export interface MCPToolProfileResponse {
  identity: MCPPrimitiveRow;
  parent: MCPServerRow;
  outcomes: MCPCallOutcomeCounts;
  call_p95_ms?: number;
  formula_version: string;
  population: Population;
  exclusions: Record<string, number>;
  completeness: DataCompleteness;
  inventory: MCPContourSupport;
  calls: MCPContourSupport;
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
  receive_to_commit_p95_ms?: number | null;
  observation_age_p95_seconds?: number | null;
  replay_count: number;
  late_backfill_candidate_count: number;
  clock_skew_event_count: number;
  active_source_count: number;
  source_gap_count: number;
  oldest_source_age_seconds?: number | null;
  pending_rollup_count: number;
  rollup_age_seconds?: number | null;
  queue_depth: number;
  oldest_queue_age_seconds: number;
  formula_version: string;
  population: Population;
  exclusions: Record<string, number>;
  completeness: DataCompleteness;
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

/* ---- /api/v1/health (runtime durability/capacity snapshot) ---- */
export type RuntimeHealthState = "pass" | "warning" | "degraded" | "critical" | "unknown";
export interface CapacityMeasure {
  current_bytes: number;
  budget_bytes: number;
  percentage: number;
  state: RuntimeHealthState;
  growth_bytes_per_day: number | null;
  estimated_exhaustion_at: string | null;
}
export interface StorageComponent {
  bytes: number | null;
  value_state: "observed" | "not_observed" | "unsupported" | "unknown";
  notes?: string;
}
export interface RuntimeSourceFreshness {
  source_kind?: string;
  source_id?: string;
  state?: string;
  value_state: "observed" | "not_observed" | "unsupported" | "unknown";
  last_observed_at?: string;
  last_committed_at?: string;
  last_attempted_at?: string;
  last_successful_at?: string | null;
  last_error_class?: string | null;
  gap_count?: number;
  inactivity?: boolean;
}
export interface RuntimeHealthResponse {
  status: RuntimeHealthState;
  database: string;
  workers: string;
  spool: string;
  migration_ledgers: string;
  database_budget: CapacityMeasure;
  checkpoint_usage: CapacityMeasure;
  legacy_mirror: { current_bytes: number; state: string };
  spool_bytes: Record<string, number>;
  spool_budget_bytes: Record<string, number>;
  queue_depth: Record<string, number>;
  source_freshness: RuntimeSourceFreshness[];
  storage_components: {
    backups: StorageComponent;
    indexes: StorageComponent;
    table_heap: StorageComponent;
    temporary_files: StorageComponent;
    wal_headroom: StorageComponent;
    filesystem: {
      available_bytes: number;
      total_bytes: number;
      free_percentage: number;
      minimum_recommended_free_bytes: number;
      state: RuntimeHealthState;
    };
  };
  last_successful_ingest_at: string | null;
  last_rejected_ingest_at: string | null;
  backpressure_rejected_total: number;
  durability_unavailable_total: number;
  pending_projection_count: number;
  oldest_pending_projection_at: string | null;
  counter_scope: string;
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
  detector_state: "open" | "recovering" | "resolved";
  triage_state: "new" | "acknowledged" | "investigating" | "action_ready";
  triage_note_category: string | null;
  installation: { state: ViewValueState; value: string | null };
  source: { state: ViewValueState; value: string | null };
  capability_id: string;
  failure_class: string;
  severity: string;
  first_seen_at: string;
  last_seen_at: string;
  resolved_at: string | null;
  occurrence_count: number;
  occurrence_retention_excluded_count: number;
  affected_interval_from: string;
  affected_interval_to: string;
  adapter_version: string | null;
  schema_fingerprint: string | null;
  source_schema_version: string | null;
  parser_version: string | null;
  recovery_criteria: string;
  recovery_observed_at: string | null;
  recovery_audit_run_id: string | null;
  recovery_evidence_ref: string | null;
  evidence_ref: string;
  projection: "ingress" | "integrity";
}
export type ViewValueState =
  | "observed"
  | "unsupported"
  | "not_observed"
  | "redacted"
  | "unknown";
export interface CursorPage<T> {
  data: T[];
  has_more: boolean;
  next_cursor?: string;
  total_state: "exact" | "lower_bound" | "unknown";
  total_lower_bound: number;
  formula_version: string;
  exclusions: string[];
  completeness: string;
}
export type IncidentPage = CursorPage<Incident>;
export interface IncidentOccurrence {
  occurrence_id: string;
  incident_id: string;
  observed_at: string;
  evidence_ref: string;
  schema_fingerprint: string | null;
  safe_error_class: string;
  record_count: number;
  byte_count: number;
}
export interface QuarantineManifest {
  quarantine_id: string;
  incident_id: string;
  source_kind: string;
  source_instance: { state: ViewValueState; value: string | null };
  signal_kind: string;
  event_type: { state: ViewValueState; value: string | null };
  structural_field_paths: string[];
  primitive_types: string[];
  shape_value_state: ViewValueState;
  schema_fingerprint: string;
  adapter_version: string | null;
  source_schema_version: string | null;
  parser_version: string | null;
  classification: string;
  rejection_reason: string;
  first_seen_at: string;
  last_seen_at: string;
  occurrence_count: number;
  total_record_count: number;
  total_byte_count: number;
  disposition: "unresolved" | "fixture_added" | "supported" | "unsupported";
}
export type QuarantinePage = CursorPage<QuarantineManifest>;
export interface IncidentDebugBundle {
  schema_version: string;
  incident: Incident;
  structural_manifest: QuarantineManifest | null;
  occurrence_count: number;
  contract_locators: string[];
  fixture_locators: string[];
  validation_commands: string[];
  agent_prompt: string;
  exclusions: string[];
}

/* ---- /api/v1/completeness ---- */
export interface CompletenessSummary {
  numerator: number;
  denominator: number;
  exclusions: string[];
  completeness: string;
}
