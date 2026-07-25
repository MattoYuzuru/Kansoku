DROP INDEX IF EXISTS component_inventory_state_last_seen_idx;
DROP INDEX IF EXISTS inventory_nodes_kind_name_idx;
DROP INDEX IF EXISTS inventory_snapshots_installation_observed_idx;
DROP TABLE IF EXISTS inventory_collection_status;
DROP TABLE IF EXISTS component_inventory_state;
DROP TABLE IF EXISTS inventory_edges;
DROP TABLE IF EXISTS inventory_nodes;
DROP TABLE IF EXISTS inventory_snapshots;
ALTER TABLE component_versions DROP COLUMN IF EXISTS version_state;
ALTER TABLE components
    DROP COLUMN IF EXISTS last_seen_at,
    DROP COLUMN IF EXISTS first_seen_at,
    DROP COLUMN IF EXISTS inventory_fingerprint,
    DROP COLUMN IF EXISTS path_pseudonym,
    DROP COLUMN IF EXISTS source_scope,
    DROP COLUMN IF EXISTS declared_name;
