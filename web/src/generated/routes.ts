// AUTO-GENERATED from contracts/dashboard.yaml by web/scripts/gen-routes.mjs.
// Do not edit by hand. Regenerate: `npm run gen:routes` (runs on prebuild).
// contract_version: 1.2.0, schema_version: kansoku.dashboard/1

export interface RouteMeta {
  readonly path: string;
  readonly title: string;
  readonly wireframe: string;
  readonly panelIds: readonly string[];
}

export const ROUTES: readonly RouteMeta[] = [
  {
    "path": "/",
    "title": "Overview",
    "wireframe": "health strip; KPI row; activity/model trend; independent skill evidence planes; incident list",
    "panelIds": [
      "overview-collection-health",
      "overview-activity",
      "overview-skill-evidence",
      "overview-incidents"
    ]
  },
  {
    "path": "/activity",
    "title": "Activity",
    "wireframe": "timeline; active duration; session distribution; weekday/hour heatmap",
    "panelIds": [
      "activity-timeline"
    ]
  },
  {
    "path": "/prompts",
    "title": "Prompt metadata",
    "wireframe": "count timeline; byte percentile band; calendar and hour/weekday heatmaps",
    "panelIds": [
      "prompt-shape"
    ]
  },
  {
    "path": "/agents",
    "title": "Agents",
    "wireframe": "installation/version table; surface activity; capability support matrix",
    "panelIds": [
      "agent-fleet"
    ]
  },
  {
    "path": "/agents/:id",
    "title": "Agent detail",
    "wireframe": "version markers; activity/model mix; capability coverage; evidence sources",
    "panelIds": [
      "agent-detail-usage",
      "agent-detail-coverage"
    ]
  },
  {
    "path": "/models",
    "title": "Models",
    "wireframe": "request/token share; latency/errors; fallback markers; estimated cost",
    "panelIds": [
      "model-usage",
      "model-cost"
    ]
  },
  {
    "path": "/components/skills",
    "title": "Skills",
    "wireframe": "independent availability and runtime evidence planes; exact cold population; linked skill profiles",
    "panelIds": [
      "skill-evidence-planes"
    ]
  },
  {
    "path": "/components/skills/:id",
    "title": "Skill detail",
    "wireframe": "identity and provenance; availability/runtime assertion timeline; source matrix; attributed children; incidents; file-tree metadata only",
    "panelIds": [
      "skill-detail-evidence"
    ]
  },
  {
    "path": "/components/plugins",
    "title": "Plugins",
    "wireframe": "plugin tree; child lifecycle; version adoption; cold/stale reasons",
    "panelIds": [
      "plugin-evidence"
    ]
  },
  {
    "path": "/components/plugins/:id",
    "title": "Plugin detail",
    "wireframe": "identity, provenance and versions; snapshot-scoped bundle tree; load and exact child-activity assertions; incidents and completeness",
    "panelIds": [
      "plugin-detail-evidence"
    ]
  },
  {
    "path": "/components/mcp",
    "title": "MCP",
    "wireframe": "server tree; connection timeline; calls/errors/latency; support gaps",
    "panelIds": [
      "mcp-health"
    ]
  },
  {
    "path": "/tools",
    "title": "Tools",
    "wireframe": "call timeline; success/errors/latency; approvals; tool table",
    "panelIds": [
      "tool-analytics"
    ]
  },
  {
    "path": "/reliability",
    "title": "Reliability",
    "wireframe": "URL-addressable health, incidents and quarantine tabs; signed-keyset incident table; incident profile with lineage, occurrence history and safe debug bundle; metadata-only structural quarantine profiles",
    "panelIds": [
      "reliability-coverage",
      "reliability-drift",
      "reliability-incidents",
      "reliability-quarantine"
    ]
  },
  {
    "path": "/privacy",
    "title": "Privacy",
    "wireframe": "data classes; retention; redactions/canary; egress and host-access status",
    "panelIds": [
      "privacy-canary",
      "privacy-retention"
    ]
  },
  {
    "path": "/system",
    "title": "System",
    "wireframe": "CPU/RSS/disk; ingest/query latency; database growth; backup/restore state",
    "panelIds": [
      "system-overhead",
      "system-recovery"
    ]
  },
  {
    "path": "/glossary",
    "title": "Glossary",
    "wireframe": "searchable plain-language definitions linked from component, activity and operations metrics",
    "panelIds": [
      "glossary-reference"
    ]
  },
  {
    "path": "/settings",
    "title": "Settings",
    "wireframe": "read-only current policy; separate preview/apply flows for retention, exports, adapters, and backups",
    "panelIds": [
      "settings-impact-preview"
    ]
  }
] as const;

export const GLOBAL_QUERY = {
  "ranges": [
    "day",
    "week",
    "sprint",
    "month",
    "six_months",
    "year",
    "all_time",
    "custom"
  ],
  "range_semantics": "half-open [from,to) in selected timezone",
  "filters": [
    "agent",
    "project",
    "model",
    "component",
    "source",
    "evidence_tier"
  ],
  "comparison": "previous equivalent calendar or elapsed period, explicitly labeled",
  "safe_url_policy": "Only opaque IDs or user aliases; never raw paths, prompts, source, tool input/output, or credentials."
} as const;
