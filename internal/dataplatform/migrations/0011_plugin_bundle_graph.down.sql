DROP INDEX IF EXISTS component_relation_observations_relation_time_idx;
DROP INDEX IF EXISTS component_relation_observations_snapshot_idx;
DROP TABLE IF EXISTS component_relation_observations;

-- The additive `app` component kind and `provides` relation kind remain
-- accepted on downgrade. Narrowing either CHECK would require deleting or
-- rewriting user inventory, which is prohibited; the old runtime safely
-- ignores kinds it does not query.
