ALTER TABLE inventory_snapshots
    DROP CONSTRAINT IF EXISTS inventory_snapshots_coverage_gap_count_check;
ALTER TABLE inventory_snapshots
    DROP COLUMN IF EXISTS coverage_gap_classes,
    DROP COLUMN IF EXISTS coverage_gap_count;

DROP INDEX IF EXISTS agent_component_plane_support_lookup_idx;
DROP TABLE IF EXISTS agent_component_plane_support;

ALTER TABLE component_assertions
    DROP CONSTRAINT IF EXISTS component_assertions_mode_check;
ALTER TABLE component_assertions
    DROP CONSTRAINT IF EXISTS component_assertions_invocation_mode_check;
ALTER TABLE component_assertions
    ADD CONSTRAINT component_assertions_mode_check
    CHECK (mode IN ('explicit', 'proactive', 'nested', 'not_observed')),
    ADD CONSTRAINT component_assertions_invocation_mode_check
    CHECK (
        invocation_mode IS NULL OR invocation_mode IN (
            'explicit', 'proactive', 'nested', 'requested', 'not_observed'
        )
    );

DROP INDEX IF EXISTS component_assertions_source_scope_state_idx;
ALTER TABLE component_assertions
    DROP CONSTRAINT IF EXISTS component_assertions_source_scope_state_check;
ALTER TABLE component_assertions
    DROP COLUMN IF EXISTS source_scope_state,
    DROP COLUMN IF EXISTS source_scope;
