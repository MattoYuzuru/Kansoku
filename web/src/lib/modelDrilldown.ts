export const MODEL_DRILLDOWN_PARAM = "model";

export function modelDrilldownHref(modelID: string): string {
  return `/models?${MODEL_DRILLDOWN_PARAM}=${encodeURIComponent(modelID)}`;
}

export function modelFilterFromSearch(search: string): string | null {
  const value = new URLSearchParams(search).get(MODEL_DRILLDOWN_PARAM);
  if (value == null || value.length === 0) {
    return null;
  }
  return value;
}

export function selectedModelRow<T extends { entity_id: string }>(
  search: string,
  rows: readonly T[],
): T | null {
  const modelID = modelFilterFromSearch(search);
  if (modelID == null) return null;
  return rows.find((row) => row.entity_id === modelID) ?? null;
}

export type ModelDrilldownState =
  | "inactive"
  | "selected"
  | "error"
  | "loading"
  | "invalid"
  | "not_observed";

export function modelDrilldownState(input: {
  requested: boolean;
  modelID: string | null;
  selected: boolean;
  isLoading: boolean;
  isError: boolean;
}): ModelDrilldownState {
  if (!input.requested) return "inactive";
  // Cached rows remain useful during a failed background refresh.
  if (input.selected) return "selected";
  // A failed query must never be presented as absence of telemetry.
  if (input.isError) return "error";
  if (input.isLoading) return "loading";
  if (input.modelID == null) return "invalid";
  return "not_observed";
}
