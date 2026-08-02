-- Deliberately non-destructive rollback: an older binary ignores these
-- additive operational tables. Historical reconciliation and health evidence
-- must survive a code rollback.
SELECT 1;
