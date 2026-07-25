/*
 * TanStack Query hooks over the real /api/v1 surface (internal/runtime/api.go).
 * Every hook returns the raw ApiEnvelope<T> so pages can derive their own
 * ViewState via deriveViewState — this module does not hide completeness.
 *
 * Range-taking endpoints require RFC3339 `from`/`to` (half-open [from,to),
 * span <= 366 days); see hooks/useRange.ts for the shared UI control that
 * produces them.
 */
import { useQuery } from "@tanstack/react-query";
import { apiGet, type ApiEnvelope } from "./client";
import type {
  ActivityTimelineResponse,
  CompletenessSummary,
  ComponentTopologyResponse,
  EntityBreakdownResponse,
  FunnelResponse,
  Incident,
  InventoryCounts,
  MCPUptimeResponse,
  ModelUsageResponse,
  PrivacyCanaryHistoryResponse,
  PromptShapeResponse,
  ReliabilityCountsResponse,
  ReliabilityTimelineResponse,
  SystemSnapshotResponse,
  ToolAnalyticsResponse,
} from "./types";

export interface RangeParams {
  from: string;
  to: string;
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

export function useSystemSnapshot() {
  return useQuery({
    queryKey: rk("system-snapshot"),
    queryFn: ({ signal }) =>
      apiGet<SystemSnapshotResponse>("/api/v1/system/snapshot", undefined, signal),
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

export function useIncidents() {
  return useQuery({
    queryKey: rk("incidents"),
    queryFn: ({ signal }) => apiGet<Incident[]>("/api/v1/incidents", undefined, signal),
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
