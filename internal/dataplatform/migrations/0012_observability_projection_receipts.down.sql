-- Non-destructive rollback: older binaries ignore pending projection
-- receipts. Dropping them could erase the only evidence of an incomplete
-- projection, so rollback preserves the table.
SELECT 1;
