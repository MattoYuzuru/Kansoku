import type {
  PluginChildRow,
  PluginObservatoryRow,
  PluginProfileResponse,
  PluginVersionRow,
  SkillAssertionRow,
  SkillFileTreeSummary,
  SkillObservatoryRow,
  SkillProfileResponse,
  SkillSourceRow,
} from "../api/types";

/*
 * The durable component model intentionally keeps source/profile/version
 * variants separate. The dashboard catalog is a presentation-only roll-up:
 * one human-facing row per normalized declared name inside one agent, with
 * every underlying installation retained as a visible variant.
 */

function normalizedName(value: string): string {
  return value.trim().normalize("NFKC").toLocaleLowerCase("en-US");
}

function stableCatalogID(prefix: "skill" | "plugin", agentID: string, name: string): string {
  const bytes = new TextEncoder().encode(`${prefix}\u0000${agentID}\u0000${normalizedName(name)}`);
  let hash = 0xcbf29ce484222325n;
  for (const byte of bytes) {
    hash ^= BigInt(byte);
    hash = BigInt.asUintN(64, hash * 0x100000001b3n);
  }
  return `${prefix === "skill" ? "skf" : "plf"}_${hash.toString(16).padStart(16, "0")}`;
}

function uniqueSorted(values: Iterable<string>): string[] {
  return [...new Set([...values].filter(Boolean))].sort((a, b) => a.localeCompare(b));
}

function latestTimestamp(values: Iterable<string | undefined>): string | undefined {
  let latest: string | undefined;
  for (const value of values) {
    if (value && (!latest || value > latest)) latest = value;
  }
  return latest;
}

const COMPLETENESS_RANK: Record<string, number> = {
  complete: 0,
  numeric_zero: 0,
  not_observed: 1,
  unknown: 2,
  partial: 3,
  degraded: 4,
};

function leastComplete(values: Iterable<string>): string {
  let selected = "complete";
  let selectedRank = 0;
  for (const value of values) {
    const rank = COMPLETENESS_RANK[value] ?? COMPLETENESS_RANK.unknown;
    if (rank > selectedRank) {
      selected = value;
      selectedRank = rank;
    }
  }
  return selected;
}

export interface CountBounds {
  lower: number;
  upper: number;
}

function distinctCountBounds(values: readonly number[]): CountBounds {
  return {
    lower: values.length === 0 ? 0 : Math.max(...values),
    upper: values.reduce((total, value) => total + value, 0),
  };
}

export interface SkillCatalogRow {
  catalog_id: string;
  declared_name: string;
  agent_id: string;
  variants: SkillObservatoryRow[];
  source_scopes: string[];
  versions: string[];
  enabled_variants: number;
  exposed_count: number;
  invoked_count: number;
  loaded_count: number;
  child_activity_count: number;
  session_bounds: CountBounds;
  last_invoked_at?: string;
  cold_state: SkillObservatoryRow["cold_state"];
  completeness: string;
}

export interface SkillCatalogStats {
  skill_families: number;
  installed_variants: number;
  enabled_skills: number;
  used_skills: number;
  total_invocations: number;
  total_loads: number;
  cold_skills: number;
}

export function groupSkillCatalog(rows: readonly SkillObservatoryRow[]): SkillCatalogRow[] {
  const groups = new Map<string, SkillObservatoryRow[]>();
  for (const row of rows) {
    const key = `${row.agent_id}\u0000${normalizedName(row.declared_name)}`;
    const group = groups.get(key);
    if (group) group.push(row);
    else groups.set(key, [row]);
  }

  const result = [...groups.values()].map((variants): SkillCatalogRow => {
    variants.sort(
      (a, b) =>
        b.invoked_count - a.invoked_count ||
        a.source_scope.localeCompare(b.source_scope) ||
        a.component_installation_id.localeCompare(b.component_installation_id),
    );
    const invokedCount = variants.reduce((total, row) => total + row.invoked_count, 0);
    const eligible = variants.filter((row) => row.cold_state !== "not_observed");
    const coldState: SkillObservatoryRow["cold_state"] =
      invokedCount > 0
        ? "used"
        : eligible.length > 0 && eligible.length === variants.length
          ? "cold"
          : "not_observed";
    const first = variants[0];
    return {
      catalog_id: stableCatalogID("skill", first.agent_id, first.declared_name),
      declared_name: first.declared_name,
      agent_id: first.agent_id,
      variants,
      source_scopes: uniqueSorted(variants.map((row) => row.source_scope)),
      versions: uniqueSorted(variants.map((row) => row.version ?? row.version_state)),
      enabled_variants: variants.filter((row) => row.enabled).length,
      exposed_count: variants.reduce((total, row) => total + row.exposed_count, 0),
      invoked_count: invokedCount,
      loaded_count: variants.reduce((total, row) => total + row.loaded_count, 0),
      child_activity_count: variants.reduce((total, row) => total + row.child_activity_count, 0),
      session_bounds: distinctCountBounds(variants.map((row) => row.unique_sessions)),
      last_invoked_at: latestTimestamp(variants.map((row) => row.last_invoked_at)),
      cold_state: coldState,
      completeness: leastComplete(variants.map((row) => row.completeness)),
    };
  });

  return result.sort(
    (a, b) =>
      b.invoked_count - a.invoked_count ||
      b.loaded_count - a.loaded_count ||
      a.declared_name.localeCompare(b.declared_name) ||
      a.agent_id.localeCompare(b.agent_id),
  );
}

export function skillCatalogStats(rows: readonly SkillCatalogRow[]): SkillCatalogStats {
  return {
    skill_families: rows.length,
    installed_variants: rows.reduce((total, row) => total + row.variants.length, 0),
    enabled_skills: rows.filter((row) => row.enabled_variants > 0).length,
    used_skills: rows.filter((row) => row.invoked_count > 0).length,
    total_invocations: rows.reduce((total, row) => total + row.invoked_count, 0),
    total_loads: rows.reduce((total, row) => total + row.loaded_count, 0),
    cold_skills: rows.filter((row) => row.cold_state === "cold").length,
  };
}

export interface PluginCatalogRow {
  catalog_id: string;
  declared_name: string;
  agent_id: string;
  variants: PluginObservatoryRow[];
  source_scopes: string[];
  versions: string[];
  enabled_variants: number;
  loaded_count: number;
  loaded_session_bounds: CountBounds;
  child_activity_count: number;
  child_count_bounds: CountBounds;
  collision_count: number;
  last_loaded_at?: string;
  activity_state: PluginObservatoryRow["activity_state"];
  bundle_completeness: string;
}

export interface PluginCatalogStats {
  plugin_families: number;
  installed_variants: number;
  enabled_plugins: number;
  active_plugins: number;
  total_loads: number;
  total_child_uses: number;
  cold_plugins: number;
}

export function groupPluginCatalog(rows: readonly PluginObservatoryRow[]): PluginCatalogRow[] {
  const groups = new Map<string, PluginObservatoryRow[]>();
  for (const row of rows) {
    const key = `${row.agent_id}\u0000${normalizedName(row.declared_name)}`;
    const group = groups.get(key);
    if (group) group.push(row);
    else groups.set(key, [row]);
  }

  const result = [...groups.values()].map((variants): PluginCatalogRow => {
    variants.sort(
      (a, b) =>
        b.child_activity_count - a.child_activity_count ||
        b.loaded_count - a.loaded_count ||
        a.source_scope.localeCompare(b.source_scope) ||
        a.component_installation_id.localeCompare(b.component_installation_id),
    );
    const enabled = variants.filter((row) => row.enabled);
    // Preserve the backend's complete-bundle eligibility rule. Raw load or
    // child counts may exist beside an incomplete graph, but that row remains
    // not_observed rather than being promoted to active in presentation code.
    const active = variants.some((row) => row.activity_state === "active");
    const activityState: PluginObservatoryRow["activity_state"] = active
      ? "active"
      : enabled.length === 0
        ? "disabled"
        : enabled.every((row) => row.activity_state === "cold")
          ? "cold"
          : "not_observed";
    const first = variants[0];
    return {
      catalog_id: stableCatalogID("plugin", first.agent_id, first.declared_name),
      declared_name: first.declared_name,
      agent_id: first.agent_id,
      variants,
      source_scopes: uniqueSorted(variants.map((row) => row.source_scope)),
      versions: uniqueSorted(variants.map((row) => row.version ?? row.version_state)),
      enabled_variants: enabled.length,
      loaded_count: variants.reduce((total, row) => total + row.loaded_count, 0),
      loaded_session_bounds: distinctCountBounds(variants.map((row) => row.loaded_sessions)),
      child_activity_count: variants.reduce((total, row) => total + row.child_activity_count, 0),
      child_count_bounds: distinctCountBounds(variants.map((row) => row.child_count)),
      collision_count: variants.reduce((total, row) => total + row.collision_count, 0),
      last_loaded_at: latestTimestamp(variants.map((row) => row.last_loaded_at)),
      activity_state: activityState,
      bundle_completeness: leastComplete(variants.map((row) => row.bundle_completeness)),
    };
  });

  return result.sort(
    (a, b) =>
      b.child_activity_count - a.child_activity_count ||
      b.loaded_count - a.loaded_count ||
      a.declared_name.localeCompare(b.declared_name) ||
      a.agent_id.localeCompare(b.agent_id),
  );
}

export function pluginCatalogStats(rows: readonly PluginCatalogRow[]): PluginCatalogStats {
  return {
    plugin_families: rows.length,
    installed_variants: rows.reduce((total, row) => total + row.variants.length, 0),
    enabled_plugins: rows.filter((row) => row.enabled_variants > 0).length,
    active_plugins: rows.filter((row) => row.activity_state === "active").length,
    total_loads: rows.reduce((total, row) => total + row.loaded_count, 0),
    total_child_uses: rows.reduce((total, row) => total + row.child_activity_count, 0),
    cold_plugins: rows.filter((row) => row.activity_state === "cold").length,
  };
}

function mergeAssertions(profiles: readonly { assertions: SkillAssertionRow[] }[]): SkillAssertionRow[] {
  const assertions = new Map<string, SkillAssertionRow>();
  for (const profile of profiles) {
    for (const assertion of profile.assertions ?? []) {
      assertions.set(assertion.assertion_id, assertion);
    }
  }
  return [...assertions.values()].sort(
    (a, b) => b.observed_at.localeCompare(a.observed_at) || a.assertion_id.localeCompare(b.assertion_id),
  );
}

function mergeSources(profiles: readonly { sources: SkillSourceRow[] }[]): SkillSourceRow[] {
  const sources = new Map<string, SkillSourceRow>();
  for (const profile of profiles) {
    for (const row of profile.sources ?? []) {
      const current = sources.get(row.source_instance_id);
      if (!current) {
        sources.set(row.source_instance_id, { ...row });
        continue;
      }
      current.assertion_count += row.assertion_count;
      current.exact_count += row.exact_count;
      current.last_observed_at = latestTimestamp([current.last_observed_at, row.last_observed_at]);
      current.completeness = leastComplete([current.completeness, row.completeness]);
    }
  }
  return [...sources.values()].sort(
    (a, b) =>
      b.assertion_count - a.assertion_count ||
      a.source_kind.localeCompare(b.source_kind) ||
      a.source_instance_id.localeCompare(b.source_instance_id),
  );
}

function mergedIncidentCount(
  profiles: readonly { identity: { agent_installation_id: string }; incident_count: number }[],
): number {
  const byInstallation = new Map<string, number>();
  for (const profile of profiles) {
    byInstallation.set(
      profile.identity.agent_installation_id,
      Math.max(byInstallation.get(profile.identity.agent_installation_id) ?? 0, profile.incident_count),
    );
  }
  return [...byInstallation.values()].reduce((total, value) => total + value, 0);
}

export interface MergedSkillProfile {
  variants: SkillObservatoryRow[];
  assertions: SkillAssertionRow[];
  sources: SkillSourceRow[];
  file_tree: Array<SkillFileTreeSummary & { component_installation_id: string }>;
  incident_count: number;
  completeness: string;
}

export function mergeSkillProfiles(profiles: readonly SkillProfileResponse[]): MergedSkillProfile {
  return {
    variants: profiles.map((profile) => profile.identity),
    assertions: mergeAssertions(profiles),
    sources: mergeSources(profiles),
    file_tree: profiles.flatMap((profile) =>
      (profile.file_tree ?? []).map((row) => ({
        ...row,
        component_installation_id: profile.identity.component_installation_id,
      })),
    ),
    incident_count: mergedIncidentCount(profiles),
    completeness: leastComplete(profiles.map((profile) => profile.completeness.status)),
  };
}

export interface MergedPluginChildRow extends PluginChildRow {
  variants: number;
  versions: string[];
  relation_kinds: string[];
}

export interface MergedPluginProfile {
  variants: PluginObservatoryRow[];
  children: MergedPluginChildRow[];
  versions: PluginVersionRow[];
  assertions: SkillAssertionRow[];
  sources: SkillSourceRow[];
  incident_count: number;
  completeness: string;
}

export function mergePluginProfiles(profiles: readonly PluginProfileResponse[]): MergedPluginProfile {
  const children = new Map<string, MergedPluginChildRow>();
  const childComponentIDs = new Map<string, Set<string>>();
  const childUsageByComponent = new Map<string, Map<string, number>>();
  for (const profile of profiles) {
    for (const row of profile.children ?? []) {
      const key = `${row.component_kind}\u0000${normalizedName(row.declared_name)}`;
      let current = children.get(key);
      if (!current) {
        current = {
          ...row,
          usage_count: 0,
          variants: 0,
          versions: uniqueSorted([row.version ?? row.version_state]),
          relation_kinds: [row.relation_kind],
        };
        children.set(key, current);
        childComponentIDs.set(key, new Set());
        childUsageByComponent.set(key, new Map());
      } else {
        current.versions = uniqueSorted([...current.versions, row.version ?? row.version_state]);
        current.relation_kinds = uniqueSorted([...current.relation_kinds, row.relation_kind]);
        current.last_activity_at = latestTimestamp([current.last_activity_at, row.last_activity_at]);
        current.relation_observed_at =
          latestTimestamp([current.relation_observed_at, row.relation_observed_at]) ??
          current.relation_observed_at;
        current.relation_completeness = leastComplete([
          current.relation_completeness,
          row.relation_completeness,
        ]);
      }
      const componentIDs = childComponentIDs.get(key);
      const usageByComponent = childUsageByComponent.get(key);
      if (!componentIDs || !usageByComponent) continue;
      componentIDs.add(row.component_id);
      usageByComponent.set(
        row.component_id,
        Math.max(usageByComponent.get(row.component_id) ?? 0, row.usage_count),
      );
      current.variants = componentIDs.size;
      current.usage_count = [...usageByComponent.values()].reduce(
        (total, usage) => total + usage,
        0,
      );
    }
  }

  const versions = new Map<string, PluginVersionRow>();
  for (const profile of profiles) {
    for (const row of profile.versions ?? []) {
      const key = `${row.version_state}\u0000${row.version ?? ""}`;
      const current = versions.get(key);
      if (!current) {
        versions.set(key, { ...row });
        continue;
      }
      current.current ||= row.current;
      current.first_seen_at =
        !current.first_seen_at || (row.first_seen_at && row.first_seen_at < current.first_seen_at)
          ? row.first_seen_at
          : current.first_seen_at;
      current.last_seen_at = latestTimestamp([current.last_seen_at, row.last_seen_at]);
    }
  }

  return {
    variants: profiles.map((profile) => profile.identity),
    children: [...children.values()].sort(
      (a, b) =>
        b.usage_count - a.usage_count ||
        a.component_kind.localeCompare(b.component_kind) ||
        a.declared_name.localeCompare(b.declared_name),
    ),
    versions: [...versions.values()].sort(
      (a, b) => Number(b.current) - Number(a.current) || (a.version ?? "").localeCompare(b.version ?? ""),
    ),
    assertions: mergeAssertions(profiles),
    sources: mergeSources(profiles),
    incident_count: mergedIncidentCount(profiles),
    completeness: leastComplete(profiles.map((profile) => profile.completeness.status)),
  };
}
