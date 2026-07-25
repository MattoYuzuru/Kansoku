DROP INDEX IF EXISTS model_operations_kind_observed_idx;

ALTER TABLE model_operations
    DROP COLUMN outcome,
    DROP COLUMN duration_ms,
    DROP COLUMN operation_kind;
