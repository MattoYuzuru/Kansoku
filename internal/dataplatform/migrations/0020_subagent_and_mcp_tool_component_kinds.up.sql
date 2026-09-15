-- Two component kinds the graph has always produced and the database has
-- never accepted.
--
-- 1. subagent. adaptersdk has declared NodeSubagentDefinition since the graph
--    contract was written, and both adapters emit those nodes, but
--    inventoryComponentKind had no case for it, so every subagent node was
--    dropped on the way to the database. Subagents could not be listed,
--    counted, or reported as unused -- on this host that is nine plugin
--    packages' worth of them.
--
-- 2. mcp_tool. An MCP tool and a user's custom slash command were both stored
--    as 'command'. That merged two unrelated populations into one count and
--    made "which of my slash commands are unused" unanswerable, because the
--    answer silently included every tool every MCP server advertises.
--
-- Existing rows are untouched: nothing is reclassified retroactively, because
-- a row written as 'command' was written from the evidence available then and
-- rewriting history to make a new query look tidy is exactly what the
-- reconciliation contract forbids. The next inventory snapshot classifies MCP
-- tools correctly going forward.
ALTER TABLE components
    DROP CONSTRAINT IF EXISTS components_kind_check;
ALTER TABLE components
    ADD CONSTRAINT components_kind_check
    CHECK (kind IN ('skill', 'plugin', 'mcp', 'hook', 'command', 'app', 'subagent', 'mcp_tool'));
