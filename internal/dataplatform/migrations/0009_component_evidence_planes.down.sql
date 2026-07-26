DROP INDEX IF EXISTS component_observation_windows_range_idx;
DROP INDEX IF EXISTS component_assertions_unresolved_idx;
DROP INDEX IF EXISTS component_assertions_installation_idx;
DROP INDEX IF EXISTS component_assertions_profile_idx;
DROP TABLE IF EXISTS component_file_tree_metadata;
DROP TABLE IF EXISTS component_observation_windows;
DROP TABLE IF EXISTS component_assertions;
DROP TABLE IF EXISTS component_terminal_contracts;

-- The shared Session 12 incident columns and occurrence relation may predate
-- this migration and are deliberately retained during a Session 14
-- downgrade. Removing them would destroy historical incident telemetry.
