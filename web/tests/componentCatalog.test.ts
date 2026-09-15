import assert from "node:assert/strict";
import test from "node:test";
import {
  groupPluginCatalog,
  groupSkillCatalog,
  mergePluginProfiles,
  mergeSkillProfiles,
  skillEvidenceSourceHealth,
  skillCatalogStats,
} from "../src/lib/componentCatalog.ts";
import type {
  PluginObservatoryRow,
  PluginProfileResponse,
  SkillProfileResponse,
  SkillObservatoryRow,
  RuntimeSourceFreshness,
} from "../src/api/types.ts";

function skill(
  id: string,
  source: string,
  invoked: number,
  loaded: number,
): SkillObservatoryRow {
  return {
    component_installation_id: id,
    component_id: `component-${id}`,
    declared_name: "search-workflow",
    version_state: "not_observed",
    source_scope: source,
    agent_id: "codex",
    agent_installation_id: `installation-${id}`,
    installed: true,
    enabled: true,
    exposed_count: 1,
    invoked_count: invoked,
    loaded_count: loaded,
    child_activity_count: 0,
    unique_sessions: invoked > 0 ? 1 : 0,
    active_days: invoked > 0 ? 1 : 0,
    modes: { explicit: invoked, proactive: 0, nested: 0 },
    cold_state: invoked > 0 ? "used" : "cold",
    exposure_state: "observed",
    inventory_coverage: "complete",
    outcome_state: "unsupported",
    completeness: "complete",
  };
}

function plugin(id: string, source: string, childUses: number): PluginObservatoryRow {
  return {
    component_installation_id: id,
    component_id: `component-${id}`,
    declared_name: "architecture-agent",
    version: "0.1.0",
    version_state: "observed",
    source_scope: source,
    agent_id: "codex",
    agent_installation_id: `installation-${id}`,
    installed: true,
    enabled: true,
    loaded_count: 0,
    loaded_sessions: 0,
    child_activity_count: childUses,
    child_count: 2,
    collision_count: 1,
    activity_state: childUses > 0 ? "active" : "cold",
    outcome_state: "unsupported",
    bundle_completeness: "complete",
  };
}

test("skill catalog keeps variants but renders one family and exact totals", () => {
  const rows = [
    skill("marketplace", "marketplace", 3, 2),
    skill("cache", "plugin_cache", 2, 1),
    { ...skill("claude", "user", 7, 1), agent_id: "claude" },
  ];
  const catalog = groupSkillCatalog(rows);
  assert.equal(catalog.length, 2);
  const codex = catalog.find((row) => row.agent_id === "codex");
  assert.ok(codex);
  assert.equal(codex.variants.length, 2);
  assert.equal(codex.invoked_count, 5);
  assert.deepEqual(codex.source_scopes, ["marketplace", "plugin_cache"]);
  assert.deepEqual(skillCatalogStats(catalog), {
    skill_families: 2,
    installed_variants: 3,
    enabled_skills: 2,
    used_skills: 2,
    total_invocations: 12,
    total_loads: 4,
    cold_skills: 0,
  });
});

test("skill metric completeness and source health stay separate", () => {
  const sources: RuntimeSourceFreshness[] = [
    { source_id: "codex.rollout", state: "producing", value_state: "observed" },
    { source_id: "codex.app_server", state: "configured", value_state: "not_observed" },
    { source_id: "backup.scheduler", state: "producing", value_state: "observed" },
  ];
  assert.deepEqual(
    skillEvidenceSourceHealth(sources).map((source) => source.source_id),
    ["codex.app_server", "codex.rollout"],
  );
  assert.equal(skill("cold", "user", 0, 0).completeness, "complete");
});

test("catalog IDs are stable across inventory order", () => {
  const first = groupSkillCatalog([
    skill("marketplace", "marketplace", 3, 2),
    skill("cache", "plugin_cache", 2, 1),
  ])[0];
  const second = groupSkillCatalog([
    skill("cache", "plugin_cache", 2, 1),
    skill("marketplace", "marketplace", 3, 2),
  ])[0];
  assert.equal(first.catalog_id, second.catalog_id);
});

test("plugin catalog aggregates child activity without hiding variants", () => {
  const catalog = groupPluginCatalog([
    plugin("marketplace", "marketplace", 4),
    plugin("cache", "plugin_cache", 2),
  ]);
  assert.equal(catalog.length, 1);
  assert.equal(catalog[0].variants.length, 2);
  assert.equal(catalog[0].child_activity_count, 6);
  assert.equal(catalog[0].activity_state, "active");
});

test("incomplete plugin graph is not promoted to active by a raw load count", () => {
  const incomplete = {
    ...plugin("partial", "marketplace", 0),
    loaded_count: 2,
    activity_state: "not_observed" as const,
    bundle_completeness: "partial",
  };
  const catalog = groupPluginCatalog([incomplete]);
  assert.equal(catalog[0].activity_state, "not_observed");
});

test("plugin detail ranks merged child families by exact usage", () => {
  const profile = (identity: PluginObservatoryRow, componentID: string, usage: number): PluginProfileResponse => ({
    identity,
    children: [{
      component_id: componentID,
      component_kind: "skill",
      declared_name: "architecture-capacity",
      relation_kind: "bundles",
      version: "0.1.0",
      version_state: "observed",
      usage_count: usage,
      relation_observed_at: "2026-07-29T00:00:00Z",
      relation_completeness: "complete",
    }],
    versions: [],
    assertions: [],
    sources: [],
    incident_count: 0,
    formula_version: "plugin_profile/2",
    population: { numerator: 1, denominator: 1 },
    exclusions: {},
    completeness: { status: "complete", covered_ratio: 1, intervals: [] },
    freshness: { rollup_watermark: "2026-07-29T00:00:00Z", late_events_pending: 0 },
  });
  const marketplace = profile(plugin("marketplace", "marketplace", 4), "child-a", 4);
  marketplace.children.push({
    ...marketplace.children[0],
    relation_kind: "provides",
  });
  const merged = mergePluginProfiles([
    marketplace,
    profile(plugin("cache", "plugin_cache", 2), "child-b", 2),
  ]);
  assert.equal(merged.children.length, 1);
  assert.equal(merged.children[0].usage_count, 6);
  assert.equal(merged.children[0].variants, 2);
  assert.deepEqual(merged.children[0].relation_kinds, ["bundles", "provides"]);
});

test("detail merging contains malformed legacy null collections", () => {
  const skillProfile = {
    identity: skill("legacy-skill", "user", 0, 0),
    assertions: null,
    sources: null,
    file_tree: null,
    incident_count: 0,
    completeness: { status: "unknown" },
  } as unknown as SkillProfileResponse;
  const pluginProfile = {
    identity: plugin("legacy-plugin", "user", 0),
    children: null,
    versions: null,
    assertions: null,
    sources: null,
    incident_count: 0,
    completeness: { status: "unknown" },
  } as unknown as PluginProfileResponse;

  assert.doesNotThrow(() => mergeSkillProfiles([skillProfile]));
  assert.doesNotThrow(() => mergePluginProfiles([pluginProfile]));
});

test("an agent with no exposure surface never renders exposure as an observation", () => {
  // Every variant unsupported: the family inherits the claim, because the
  // absence is a property of the agent rather than of one installation.
  const unsupported = [skill("claude-a", "plugin_cache", 3, 3), skill("claude-b", "user", 0, 0)].map(
    (row) => ({
      ...row,
      agent_id: "claude",
      exposure_state: "unsupported" as const,
      exposure_reason: "claude_code_documents_no_model_visible_skill_set_event_or_snapshot",
      exposed_count: 0,
    }),
  );
  const [family] = groupSkillCatalog(unsupported);
  assert.equal(family.exposure_state, "unsupported");
  assert.equal(
    family.exposure_reason,
    "claude_code_documents_no_model_visible_skill_set_event_or_snapshot",
  );
  assert.equal(family.invoked_count, 3);

  // Two installations of the same agent can disagree -- one on an adapter
  // build that declares the plane unsupported, one that reports exposure. A
  // real observation must not be hidden behind the other's absent surface.
  const mixed = [
    { ...unsupported[0] },
    {
      ...skill("claude-c", "user", 0, 0),
      agent_id: "claude",
      exposure_state: "observed" as const,
      exposed_count: 2,
    },
  ];
  assert.equal(groupSkillCatalog(mixed)[0].exposure_state, "observed");

  // Neither observed nor unsupported: the surface exists and reported nothing.
  const silent = [
    { ...skill("codex-b", "user", 0, 0), exposure_state: "not_observed" as const, exposed_count: 0 },
  ];
  assert.equal(groupSkillCatalog(silent)[0].exposure_state, "not_observed");
});

test("family inventory coverage reports the least complete variant", () => {
  const rows = [
    { ...skill("a", "user", 1, 1), inventory_coverage: "complete" },
    { ...skill("b", "plugin_cache", 0, 0), inventory_coverage: "partial" },
  ];
  assert.equal(groupSkillCatalog(rows)[0].inventory_coverage, "partial");
});
