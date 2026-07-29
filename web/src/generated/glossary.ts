// AUTO-GENERATED from contracts/glossary.yaml by web/scripts/gen-routes.mjs.
// Do not edit by hand. Regenerate: `npm run gen:routes` (runs on prebuild).
// contract_version: 1.1.0, schema_version: kansoku.glossary/1

export interface GlossaryTerm {
  readonly id: string;
  readonly definition: string;
  readonly plainDefinition: string;
}

export const GLOSSARY_TERMS: readonly GlossaryTerm[] = [
  {
    "id": "agent",
    "definition": "A developer-facing AI runtime or product whose local surfaces Kansoku may inventory or observe.",
    "plainDefinition": "A developer-facing AI runtime or product whose local surfaces Kansoku may inventory or observe."
  },
  {
    "id": "surface",
    "definition": "A distinct agent execution surface such as CLI, IDE, desktop app, web task, or cloud worker.",
    "plainDefinition": "A distinct agent execution surface such as CLI, IDE, desktop app, web task, or cloud worker."
  },
  {
    "id": "session",
    "definition": "A source-scoped resumable interaction container; it is not assumed to be one process or one task.",
    "plainDefinition": "A source-scoped resumable interaction container; it is not assumed to be one process or one task."
  },
  {
    "id": "turn",
    "definition": "One user-initiated or agent-initiated interaction unit inside a session when the source exposes that boundary.",
    "plainDefinition": "One user-initiated or agent-initiated interaction unit inside a session when the source exposes that boundary."
  },
  {
    "id": "component",
    "definition": "A versioned skill, plugin, MCP server/tool, built-in tool, custom command, hook, or agent extension.",
    "plainDefinition": "A versioned skill, plugin, MCP server/tool, built-in tool, custom command, hook, or agent extension."
  },
  {
    "id": "installed",
    "definition": "An inventory source confirms that an artifact and version exist in an installation scope.",
    "plainDefinition": "Kansoku found this component on disk or in an agent inventory. It does not mean the agent could use it in a session."
  },
  {
    "id": "enabled",
    "definition": "Effective agent configuration permits the component to participate.",
    "plainDefinition": "The agent configuration allows this component to be considered. Enabled does not prove that it was shown to a model or used."
  },
  {
    "id": "exposed",
    "definition": "Evidence shows the component was available to a specific session or model; inventory alone is insufficient.",
    "plainDefinition": "The component was actually available inside a particular session or model context. This is stronger than installed or enabled."
  },
  {
    "id": "invoked",
    "definition": "An explicit native, tool, user, or deterministic reconstructed invocation occurred.",
    "plainDefinition": "Kansoku has evidence that the skill was selected for a concrete interaction. The invocation count is the number of deduplicated invocation events in the selected time range."
  },
  {
    "id": "loaded",
    "definition": "Instructions or resources were actually loaded when that stage is observable.",
    "plainDefinition": "The agent read or loaded the component instructions or resources. A load is not automatically a successful execution."
  },
  {
    "id": "executed",
    "definition": "A uniquely attributed script, tool, MCP, command, or hook action occurred.",
    "plainDefinition": "A uniquely attributed script, tool, MCP, command, or hook action occurred."
  },
  {
    "id": "succeeded",
    "definition": "The component-specific terminal contract succeeded; this does not assert that the overall task succeeded.",
    "plainDefinition": "Success is shown only when that component has a registered finish condition and the finish condition was observed. A normal model response is not enough."
  },
  {
    "id": "opportunity_detected",
    "definition": "An ephemeral or stored metadata-only inference that a component might have applied; it never promotes a factual lifecycle state.",
    "plainDefinition": "An ephemeral or stored metadata-only inference that a component might have applied; it never promotes a factual lifecycle state."
  },
  {
    "id": "supported",
    "definition": "For a bounded agent/capability/version scope, contract, privacy tests, fixtures, passive audit, and end-to-end verification pass.",
    "plainDefinition": "For a bounded agent/capability/version scope, contract, privacy tests, fixtures, passive audit, and end-to-end verification pass."
  },
  {
    "id": "beta",
    "definition": "A deterministic data path and fixtures exist, but live or version coverage remains incomplete.",
    "plainDefinition": "A deterministic data path and fixtures exist, but live or version coverage remains incomplete."
  },
  {
    "id": "experimental",
    "definition": "A documentation, inventory, or source probe exists without a completeness promise.",
    "plainDefinition": "A documentation, inventory, or source probe exists without a completeness promise."
  },
  {
    "id": "unsupported",
    "definition": "No reliable source exists for the bounded capability/version scope, or the capability is explicitly out of scope.",
    "plainDefinition": "No reliable source exists for the bounded capability/version scope, or the capability is explicitly out of scope."
  },
  {
    "id": "not_observed",
    "definition": "The capability is observable and the source interval is healthy, but no qualifying event was seen.",
    "plainDefinition": "The capability is observable and the source interval is healthy, but no qualifying event was seen."
  },
  {
    "id": "observed",
    "definition": "A source supplied a qualifying non-content value or identity and Kansoku preserved it without inference.",
    "plainDefinition": "A source supplied a qualifying non-content value or identity and Kansoku preserved it without inference."
  },
  {
    "id": "redacted",
    "definition": "A value existed at the transient trust boundary and was intentionally removed by data policy.",
    "plainDefinition": "A value existed at the transient trust boundary and was intentionally removed by data policy."
  },
  {
    "id": "unknown",
    "definition": "The system cannot establish a value or denominator from available evidence.",
    "plainDefinition": "The system cannot establish a value or denominator from available evidence."
  },
  {
    "id": "numeric_zero",
    "definition": "A complete eligible population was measured and its numeric result is exactly zero.",
    "plainDefinition": "A complete eligible population was measured and its numeric result is exactly zero."
  },
  {
    "id": "complete",
    "definition": "All required sources for the declared capability and interval are healthy, reconciled, and past their watermarks.",
    "plainDefinition": "Kansoku has all evidence it expects for this measurement and time range."
  },
  {
    "id": "partial",
    "definition": "Some but not all eligible evidence is available and the missing portion is bounded and disclosed.",
    "plainDefinition": "Some but not all eligible evidence is available and the missing portion is bounded and disclosed."
  },
  {
    "id": "degraded",
    "definition": "A known source, schema, parser, reconciliation, or freshness failure affects the result.",
    "plainDefinition": "A known source, schema, parser, reconciliation, or freshness failure affects the result."
  },
  {
    "id": "evidence",
    "definition": "A source-scoped observation carrying adapter/schema versions, confidence, timestamps, and lineage.",
    "plainDefinition": "A metadata-only fact together with where it came from, when it was seen, and how confidently it was interpreted."
  },
  {
    "id": "coverage",
    "definition": "Observed normalized eligible events divided by expected eligible events for a declared source/capability/time population.",
    "plainDefinition": "Observed normalized eligible events divided by expected eligible events for a declared source/capability/time population."
  },
  {
    "id": "sprint",
    "definition": "A configurable half-open calendar range derived from a local anchor date and duration; the default duration is fourteen days.",
    "plainDefinition": "A configurable half-open calendar range derived from a local anchor date and duration; the default duration is fourteen days."
  },
  {
    "id": "skill_family",
    "definition": "A presentation-only catalog row grouping same-named skill variants inside one agent while preserving every underlying installation and identity.",
    "plainDefinition": "The one row you see in the Skills list. Different copies, versions or sources stay visible as variants inside it; Kansoku does not rewrite or delete them."
  },
  {
    "id": "component_variant",
    "definition": "One source-, profile-, version- or owner-specific component installation retained beneath a catalog family.",
    "plainDefinition": "A concrete copy of a skill or plugin. Marketplace, plugin cache and separate agent profiles can create several variants under one visible catalog row."
  },
  {
    "id": "cold",
    "definition": "An enabled and provably exposed skill or plugin had zero exact qualifying activity in a complete selected interval.",
    "plainDefinition": "Kansoku knows the component was available but saw no exact use in the selected time range. Installed but unobserved components are not called cold."
  },
  {
    "id": "call",
    "definition": "One observed request to a tool or MCP primitive. A call may later complete, fail, time out, be denied, or remain incomplete.",
    "plainDefinition": "An agent tried to run a tool. A call is not the same thing as a prompt, a skill invocation, or a successful result."
  },
  {
    "id": "bundle_activity",
    "definition": "Plugin-level summary of exact load evidence and activity attributed to current bundled children.",
    "plainDefinition": "What Kansoku can prove happened around a plugin: plugin loads plus exact uses of its child skills, tools, hooks or apps. It does not claim the plugin itself was invoked."
  },
  {
    "id": "child_activity",
    "definition": "A child component action attributed to exactly one current plugin owner without moving the original child fact.",
    "plainDefinition": "A skill or tool inside a plugin was used and Kansoku could identify one owning plugin. The original use still belongs to the child."
  },
  {
    "id": "active_plugin",
    "definition": "An enabled plugin with exact load or child-activity evidence and a sufficiently complete current bundle graph.",
    "plainDefinition": "The plugin was not merely installed: Kansoku saw it load or saw one of its children used."
  },
  {
    "id": "collision",
    "definition": "Two current component candidates share a declared name but differ in scope, owner, version, path pseudonym or fingerprint and therefore are not merged as one identity.",
    "plainDefinition": "Kansoku found same-named variants that may be different. It keeps them separate and shows the conflict instead of guessing."
  },
  {
    "id": "population",
    "definition": "The explicitly eligible set used as the denominator for a metric.",
    "plainDefinition": "The records that were allowed to participate in a calculation. A population is shown as numerator / denominator."
  },
  {
    "id": "exclusion",
    "definition": "An explicitly classified record or interval left out of a metric because required evidence was unavailable or ineligible.",
    "plainDefinition": "Data not counted in a metric, with a reason. Exclusions prevent missing evidence from silently becoming zero."
  },
  {
    "id": "database_budget",
    "definition": "A soft operational capacity threshold for PostgreSQL data, not a hard storage allocation or billing limit.",
    "plainDefinition": "The amount of database growth Kansoku considers safe before warning. PostgreSQL can be physically smaller or larger; crossing the budget triggers health warnings, not automatic deletion."
  },
  {
    "id": "checkpoint_budget",
    "definition": "The bounded local emergency-checkpoint allowance used when authoritative PostgreSQL persistence is temporarily unavailable.",
    "plainDefinition": "A small safety buffer for metadata waiting to reach PostgreSQL. It is not a second full database or long-term mirror."
  },
  {
    "id": "mirror",
    "definition": "A second continuously maintained copy of data intended to reproduce the authoritative store.",
    "plainDefinition": "Writing the same telemetry to two full databases. Kansoku does not use a full mirror now: PostgreSQL is authoritative and the local checkpoint is only a small emergency buffer."
  },
  {
    "id": "fsync",
    "definition": "An operating-system request that dirty file data and required filesystem metadata reach durable storage before success is acknowledged.",
    "plainDefinition": "The write waits for the disk durability boundary. This adds latency and I/O, but prevents an acknowledged record from existing only in volatile memory."
  },
  {
    "id": "docker_filesystem",
    "definition": "The disk space available to Docker's storage area, including images, layers, volumes and container writable data.",
    "plainDefinition": "Free disk space where the local Kansoku containers and PostgreSQL volume live. A large database soak consumes this space even when the host disk has room elsewhere."
  },
  {
    "id": "backpressure_rejection",
    "definition": "An ingest item rejected because the bounded queue or durability path could not safely accept more work.",
    "plainDefinition": "Kansoku refused new telemetry instead of pretending it was stored when its safe buffers were full or durable storage was unavailable."
  },
  {
    "id": "estimated_exhaustion",
    "definition": "A forecast timestamp derived from current free capacity and an observed database growth rate.",
    "plainDefinition": "When the database budget may be reached if the recent growth rate continues. It is an estimate, not a shutdown deadline."
  },
  {
    "id": "cost_estimate",
    "definition": "A non-billing monetary estimate derived from provider-reported tokens and a versioned price snapshot, with confidence and unknown states.",
    "plainDefinition": "A non-billing monetary estimate derived from provider-reported tokens and a versioned price snapshot, with confidence and unknown states."
  }
] as const;

export const GLOSSARY_BY_ID = new Map(GLOSSARY_TERMS.map((term) => [term.id, term]));
