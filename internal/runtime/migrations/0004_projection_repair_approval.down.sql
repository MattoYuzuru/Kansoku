-- Non-destructive rollback: an older binary ignores projection-repair
-- approvals. Narrowing the check would fail when historical approvals exist,
-- so the additive operation remains allowed.
SELECT 1;
