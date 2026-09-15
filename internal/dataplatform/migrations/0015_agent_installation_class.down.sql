DROP INDEX IF EXISTS agent_installation_profiles_class_idx;

ALTER TABLE agent_installation_profiles
    DROP COLUMN IF EXISTS installation_class_provenance,
    DROP COLUMN IF EXISTS installation_class;
