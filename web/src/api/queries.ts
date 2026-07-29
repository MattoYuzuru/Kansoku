/*
 * TanStack Query hooks over the real /api/v1 surface (internal/runtime/api.go).
 * Every hook returns the raw ApiEnvelope<T> so pages can derive their own
 * ViewState via deriveViewState — this module does not hide completeness.
 *
 * Range-taking endpoints require RFC3339 `from`/`to` (half-open [from,to),
 * span <= 366 days); see hooks/useRange.ts for the shared UI control that
 * produces them.
 */
import { useQueries, useQuery } from "@tanstack/react-query";
import { apiGet, type ApiEnvelope } from "./client";
import type {
  ActivityTimelineResponse,
  CompletenessSummary,
  ComponentTopologyResponse,
  EntityBreakdownResponse,
  FunnelResponse,
  IncidentDebugBundle,
  Incident,
  IncidentOccurrence,
  IncidentPage,
  InventoryComponentResponse,
  InventoryCounts,
  MCPUptimeResponse,
  ModelUsageResponse,
  AgentProfile,
  CollectionHealthSnapshot,
  PrivacyCanaryHistoryResponse,
  PromptShapeResponse,
  QuarantineManifest,
  QuarantinePage,
  ReliabilityCountsResponse,
  ReliabilityTimelineResponse,
  RuntimeHealthResponse,
  SystemSnapshotResponse,
  ToolAnalyticsResponse,
  SkillObservatoryResponse,
  SkillProfileResponse,
  PluginObservatoryResponse,
  PluginProfileResponse,
  MCPObservatoryResponse,
  MCPServerProfileResponse,
  MCPPrimitiveListResponse,
  MCPToolProfileResponse,
} from "./types";

export interface RangeParams {
  from: string;
  to: string;
  granularity: "hourly" | "daily" | "weekly" | "monthly";
  timezone: string;
}

const rk = (...parts: unknown[]) => parts;

export function useInventory() {
  return useQuery({
    queryKey: rk("inventory"),
    queryFn: ({ signal }) => apiGet<InventoryCounts>("/api/v1/inventory", undefined, signal),
  });
}

export function useActivityTimeline(range: RangeParams) {
  return useQuery({
    queryKey: rk("activity", range),
    queryFn: ({ signal }) =>
      apiGet<ActivityTimelineResponse>("/api/v1/activity", { ...range }, signal),
  });
}

export function usePromptShape(range: RangeParams) {
  return useQuery({
    queryKey: rk("prompts-shape", range),
    queryFn: ({ signal }) =>
      apiGet<PromptShapeResponse>("/api/v1/prompts/shape", { ...range }, signal),
  });
}

export function useModelUsage(range: RangeParams) {
  return useQuery({
    queryKey: rk("models-usage", range),
    queryFn: ({ signal }) =>
      apiGet<ModelUsageResponse>("/api/v1/models/usage", { ...range }, signal),
  });
}

export function useToolAnalytics(range: RangeParams, componentId?: string) {
  return useQuery({
    queryKey: rk("tools-analytics", range, componentId),
    queryFn: ({ signal }) =>
      apiGet<ToolAnalyticsResponse>(
        "/api/v1/tools/analytics",
        { ...range, component_id: componentId },
        signal,
      ),
  });
}

export function useMCPTopology(range: RangeParams) {
  return useQuery({
    queryKey: rk("mcp-topology", range),
    queryFn: ({ signal }) =>
      apiGet<ComponentTopologyResponse>("/api/v1/components/mcp/topology", { ...range }, signal),
  });
}

export function useMCPUptime(range: RangeParams) {
  return useQuery({
    queryKey: rk("mcp-uptime", range),
    queryFn: ({ signal }) =>
      apiGet<MCPUptimeResponse>("/api/v1/components/mcp/uptime", { ...range }, signal),
  });
}

export function useReliabilityCounts(range: RangeParams) {
  return useQuery({
    queryKey: rk("reliability-counts", range),
    queryFn: ({ signal }) =>
      apiGet<ReliabilityCountsResponse>("/api/v1/reliability/counts", { ...range }, signal),
  });
}

export function useCollectionHealth(range: RangeParams) {
  return useQuery({
    queryKey: rk("collection-health", range),
    queryFn: ({ signal }) =>
      apiGet<CollectionHealthSnapshot>(
        "/api/v1/reliability/collection-health",
        { ...range },
        signal,
      ),
  });
}

export function useSystemSnapshot() {
  return useQuery({
    queryKey: rk("system-snapshot"),
    queryFn: ({ signal }) =>
      apiGet<SystemSnapshotResponse>("/api/v1/system/snapshot", undefined, signal),
  });
}

export function useRuntimeHealth() {
  return useQuery({
    queryKey: rk("runtime-health"),
    queryFn: ({ signal }) =>
      apiGet<RuntimeHealthResponse>("/api/v1/health", undefined, signal),
    refetchInterval: 30_000,
  });
}

export function usePrivacyCanaryHistory(range: RangeParams) {
  return useQuery({
    queryKey: rk("privacy-canary-history", range),
    queryFn: ({ signal }) =>
      apiGet<PrivacyCanaryHistoryResponse>(
        "/api/v1/privacy/canary-history",
        { ...range },
        signal,
      ),
  });
}

export interface IncidentQueryParams {
  state?: string;
  triage?: string;
  adapter?: string;
  source?: string;
  capability?: string;
  failure?: string;
  from?: string;
  to?: string;
  cursor?: string;
  limit?: number;
}

export function useIncidents(params: IncidentQueryParams = {}) {
  return useQuery({
    queryKey: rk("incidents", params),
    queryFn: ({ signal }) => apiGet<IncidentPage>("/api/v1/incidents", { ...params }, signal),
  });
}

export function useIncident(id?: string) {
  return useQuery({
    queryKey: rk("incident", id),
    queryFn: ({ signal }) => apiGet<Incident>(`/api/v1/incidents/${id}`, undefined, signal),
    enabled: Boolean(id),
  });
}

export function useIncidentOccurrences(id?: string, cursor?: string) {
  return useQuery({
    queryKey: rk("incident-occurrences", id, cursor),
    queryFn: ({ signal }) =>
      apiGet<{
        data: IncidentOccurrence[];
        has_more: boolean;
        next_cursor?: string;
        total_state: string;
        total_lower_bound: number;
        formula_version: string;
        exclusions: string[];
        completeness: string;
      }>(`/api/v1/incidents/${id}/occurrences`, { cursor, limit: 25 }, signal),
    enabled: Boolean(id),
  });
}

export function useIncidentDebugBundle(id?: string) {
  return useQuery({
    queryKey: rk("incident-debug-bundle", id),
    queryFn: ({ signal }) =>
      apiGet<IncidentDebugBundle>(
        `/api/v1/incidents/${id}/debug-bundle`,
        { format: "json" },
        signal,
      ),
    enabled: Boolean(id),
  });
}

export function useQuarantine(
  params: {
    fingerprint?: string;
    source?: string;
    from?: string;
    to?: string;
    cursor?: string;
    limit?: number;
  } = {},
) {
  return useQuery({
    queryKey: rk("quarantine", params),
    queryFn: ({ signal }) =>
      apiGet<QuarantinePage>("/api/v1/quarantine", { ...params }, signal),
  });
}

export function useQuarantineManifest(id?: string) {
  return useQuery({
    queryKey: rk("quarantine-manifest", id),
    queryFn: ({ signal }) =>
      apiGet<QuarantineManifest>(`/api/v1/quarantine/${id}`, undefined, signal),
    enabled: Boolean(id),
  });
}

export function useCompletenessSummary() {
  return useQuery({
    queryKey: rk("completeness"),
    queryFn: ({ signal }) =>
      apiGet<CompletenessSummary>("/api/v1/completeness", undefined, signal),
  });
}

/* ---- /api/v1/analytics dispatch: entity-breakdown / funnel / timeline family ---- */

export function useAgentBreakdown(range: RangeParams) {
  return useQuery({
    queryKey: rk("agent-breakdown", range),
    queryFn: ({ signal }) =>
      apiGet<EntityBreakdownResponse>(
        "/api/v1/analytics",
        { budget_id: "agent_breakdown_range", ...range },
        signal,
      ),
  });
}

export function useAgentProfile(id: string, range: RangeParams) {
  return useQuery({
    queryKey: rk("agent-profile", { id, ...range }),
    queryFn: ({ signal }) =>
      apiGet<AgentProfile>(`/api/v1/agents/${encodeURIComponent(id)}`, { ...range }, signal),
    enabled: Boolean(id),
  });
}

export function useModelBreakdown(range: RangeParams) {
  return useQuery({
    queryKey: rk("model-breakdown", range),
    queryFn: ({ signal }) =>
      apiGet<EntityBreakdownResponse>(
        "/api/v1/analytics",
        { budget_id: "model_breakdown_range", ...range },
        signal,
      ),
  });
}

/** componentKind: "" (empty) means every kind. */
export function useComponentBreakdown(range: RangeParams, componentKind = "") {
  return useQuery({
    queryKey: rk("component-breakdown", range, componentKind),
    queryFn: ({ signal }) =>
      apiGet<EntityBreakdownResponse>(
        "/api/v1/analytics",
        { budget_id: "component_breakdown_range", metric_family: componentKind, ...range },
        signal,
      ),
  });
}

/** componentKind filters skill/plugin/mcp/hook/command; "" means every kind. */
export function useComponentLifecycleFunnel(range: RangeParams, componentKind = "") {
  return useQuery({
    queryKey: rk("component-funnel", range, componentKind),
    queryFn: ({ signal }) =>
      apiGet<FunnelResponse>(
        "/api/v1/analytics",
        { budget_id: "component_lifecycle_funnel", metric_family: componentKind, ...range },
        signal,
      ),
  });
}

export function useComponentInventory(componentKind = "") {
  return useQuery({
    queryKey: rk("component-inventory", componentKind),
    queryFn: ({ signal }) =>
      apiGet<InventoryComponentResponse>(
        "/api/v1/components/inventory",
        { kind: componentKind },
        signal,
      ),
  });
}

export function useSkills(range: RangeParams) {
  return useQuery({
    queryKey: rk("skills", range),
    queryFn: ({ signal }) =>
      apiGet<SkillObservatoryResponse>("/api/v1/skills", { ...range }, signal),
  });
}

export function useSkillProfile(id: string, range: RangeParams) {
  return useQuery({
    queryKey: rk("skill-profile", { id, ...range }),
    queryFn: ({ signal }) =>
      apiGet<SkillProfileResponse>(`/api/v1/skills/${encodeURIComponent(id)}`, { ...range }, signal),
    enabled: Boolean(id),
  });
}

export function useSkillProfiles(ids: readonly string[], range: RangeParams) {
  return useQueries({
    queries: ids.map((id) => ({
      queryKey: rk("skill-profile", { id, ...range }),
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        apiGet<SkillProfileResponse>(
          `/api/v1/skills/${encodeURIComponent(id)}`,
          { ...range },
          signal,
        ),
      enabled: Boolean(id),
    })),
  });
}

export function usePlugins(range: RangeParams) {
  return useQuery({
    queryKey: rk("plugins", range),
    queryFn: ({ signal }) =>
      apiGet<PluginObservatoryResponse>("/api/v1/plugins", { ...range }, signal),
  });
}

export function usePluginProfile(id: string, range: RangeParams) {
  return useQuery({
    queryKey: rk("plugin-profile", { id, ...range }),
    queryFn: ({ signal }) =>
      apiGet<PluginProfileResponse>(`/api/v1/plugins/${encodeURIComponent(id)}`, { ...range }, signal),
    enabled: Boolean(id),
  });
}

export function usePluginProfiles(ids: readonly string[], range: RangeParams) {
  return useQueries({
    queries: ids.map((id) => ({
      queryKey: rk("plugin-profile", { id, ...range }),
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        apiGet<PluginProfileResponse>(
          `/api/v1/plugins/${encodeURIComponent(id)}`,
          { ...range },
          signal,
        ),
      enabled: Boolean(id),
    })),
  });
}

export function useMCPObservatory(range: RangeParams) {
  return useQuery({
    queryKey: rk("mcp-observatory", range),
    queryFn: ({ signal }) =>
      apiGet<MCPObservatoryResponse>("/api/v1/components/mcp", { ...range }, signal),
  });
}

export function useMCPServerProfile(id: string, range: RangeParams) {
  return useQuery({
    queryKey: rk("mcp-server-profile", { id, ...range }),
    queryFn: ({ signal }) =>
      apiGet<MCPServerProfileResponse>(`/api/v1/components/mcp/${encodeURIComponent(id)}`, { ...range }, signal),
    enabled: Boolean(id),
  });
}

export function useMCPPrimitiveList(serverID: string, range: RangeParams) {
  return useQuery({
    queryKey: rk("mcp-primitive-list", { serverID, ...range }),
    queryFn: ({ signal }) =>
      apiGet<MCPPrimitiveListResponse>(`/api/v1/components/mcp/${encodeURIComponent(serverID)}/tools`, { ...range }, signal),
    enabled: Boolean(serverID),
  });
}

export function useMCPToolProfile(serverID: string, toolID: string, range: RangeParams) {
  return useQuery({
    queryKey: rk("mcp-tool-profile", { serverID, toolID, ...range }),
    queryFn: ({ signal }) =>
      apiGet<MCPToolProfileResponse>(
        `/api/v1/components/mcp/${encodeURIComponent(serverID)}/tools/${encodeURIComponent(toolID)}`,
        { ...range },
        signal,
      ),
    enabled: Boolean(serverID && toolID),
  });
}

export function useReliabilityCoverageTimeline(range: RangeParams) {
  return useQuery({
    queryKey: rk("reliability-coverage-timeline", range),
    queryFn: ({ signal }) =>
      apiGet<ReliabilityTimelineResponse>(
        "/api/v1/analytics",
        { budget_id: "reliability_coverage_timeline", ...range },
        signal,
      ),
  });
}

/** Re-exported for pages that need the envelope type directly. */
export type { ApiEnvelope };
