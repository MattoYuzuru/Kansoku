-- Add the explicit operator-approved projection retry operation without
-- rewriting or deleting any historical approval receipt.
ALTER TABLE runtime_operation_approvals
    DROP CONSTRAINT IF EXISTS runtime_operation_approvals_operation_check;

ALTER TABLE runtime_operation_approvals
    ADD CONSTRAINT runtime_operation_approvals_operation_check
    CHECK (operation IN (
        'plan_apply',
        'retention_apply',
        'import',
        'backup',
        'restore_verify',
        'projection_repair_retry'
    ));
