-- Session 10 dashboard aggregation indexes. These back the new
-- group-by/aggregate queries in internal/dataplatform (agent/model/component
-- breakdowns, component lifecycle funnel, reliability coverage timeline, MCP
-- topology) that group across entities within an explicit observed_at range,
-- a shape the Session 04-09 rollup/lookup indexes did not anticipate because
-- metric_rollups_* is keyed to exactly one already-known dimension_scope.
--
-- This migration only adds indexes; it does not alter any existing table,
-- column or constraint from 0001/0002.

-- AgentBreakdown groups events by agent_installation_id within an
-- observed_at range; 0002 only indexed session_id/component_id/
-- source_instance_id on events, not agent_installation_id.
CREATE INDEX IF NOT EXISTS events_agent_installation_idx ON events USING btree (agent_installation_id, observed_at);

-- ModelBreakdown groups model_operations by model_id within an observed_at
-- range; model_operations had no index at all beyond its own primary key.
CREATE INDEX IF NOT EXISTS model_operations_model_idx ON model_operations USING btree (model_id, observed_at);

-- token_usage is looked up by model_operation_id to join back to
-- model_operations for ModelBreakdown's token sum.
CREATE INDEX IF NOT EXISTS token_usage_model_operation_idx ON token_usage USING btree (model_operation_id, observed_at);

-- MCPTopology's latest-connection-state lookup groups mcp_connections by
-- component_id within an observed_at range; mcp_connections had no
-- component index at all despite being partitioned and FK'd to components.
CREATE INDEX IF NOT EXISTS mcp_connections_component_idx ON mcp_connections USING btree (component_id, observed_at);

-- ComponentLifecycleFunnel and MCPTopology join component_installations ->
-- component_versions -> components; component_installations had no index on
-- component_version_id beyond the implicit FK, and is queried joined from
-- component_lifecycle_events at funnel-query volume.
CREATE INDEX IF NOT EXISTS component_installations_version_idx ON component_installations USING btree (component_version_id);

-- MCPTopology's tree expansion looks up component_relations by parent_id
-- for relation_kind = 'bundles'.
CREATE INDEX IF NOT EXISTS component_relations_parent_idx ON component_relations USING btree (parent_id, relation_kind);

-- ReliabilityCoverageTimeline filters completeness_intervals by interval
-- overlap (interval_start < to AND interval_end > from); completeness_intervals
-- had no index at all beyond its primary key.
CREATE INDEX IF NOT EXISTS completeness_intervals_range_idx ON completeness_intervals USING btree (interval_start, interval_end);
