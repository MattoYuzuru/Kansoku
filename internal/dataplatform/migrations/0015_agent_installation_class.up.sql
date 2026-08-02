-- Installation class is explicit durable profile metadata. The one-time
-- mappings below are evidence-reviewed identities from the 2026-07-30 local
-- reconciliation; runtime code never infers class from an ID prefix/pattern.

ALTER TABLE agent_installation_profiles
    ADD COLUMN IF NOT EXISTS installation_class TEXT NOT NULL DEFAULT 'unknown'
        CHECK (installation_class IN ('real','canary','fixture','imported','unknown')),
    ADD COLUMN IF NOT EXISTS installation_class_provenance TEXT NOT NULL DEFAULT 'not_observed';

UPDATE agent_installation_profiles
SET installation_class = 'real',
    installation_class_provenance = 'research_reconciliation_2026_07_30'
WHERE agent_installation_id IN (
    'ain_9cd7c4fbf5d8df4694834d7769a3747b',
    'ain_c2e4a67af4327907cd7a66172c685713'
);

UPDATE agent_installation_profiles
SET installation_class = 'fixture',
    installation_class_provenance = 'research_reconciliation_2026_07_30'
WHERE agent_installation_id = 'ain_fixture';

UPDATE agent_installation_profiles
SET installation_class = 'canary',
    installation_class_provenance = 'research_reconciliation_2026_07_30'
WHERE agent_installation_id IN (
    'ain_codex_final_20260729',
    'ain_codex_supervised_live',
    'ain_mcp_live'
);

CREATE INDEX IF NOT EXISTS agent_installation_profiles_class_idx
    ON agent_installation_profiles (installation_class, agent_installation_id);
