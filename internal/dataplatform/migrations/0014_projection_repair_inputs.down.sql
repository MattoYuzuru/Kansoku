-- Non-destructive compatibility rollback.  Older binaries ignore the
-- additive input columns.  Removing them or collapsing the evidence-scoped
-- primary key could destroy a pending retry or merge independent evidence,
-- so the durable repair metadata remains until normal projection cleanup.
SELECT 1;
