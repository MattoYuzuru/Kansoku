-- Narrowing the vocabulary again would reject rows this migration made
-- legitimate, so the constraint is only restored when no such row exists.
-- Failing loudly is correct here: silently deleting inventoried subagents to
-- make a downgrade succeed would destroy observations.
ALTER TABLE components
    DROP CONSTRAINT IF EXISTS components_kind_check;
ALTER TABLE components
    ADD CONSTRAINT components_kind_check
    CHECK (kind IN ('skill', 'plugin', 'mcp', 'hook', 'command', 'app'));
