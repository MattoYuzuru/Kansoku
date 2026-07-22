-- BRIN indexes for time-range scans on large partitioned fact tables; these
-- are created on the partitioned parent so every partition inherits one.
CREATE INDEX IF NOT EXISTS events_observed_at_brin ON events USING brin (observed_at);
CREATE INDEX IF NOT EXISTS event_evidence_observed_at_brin ON event_evidence USING brin (observed_at);
CREATE INDEX IF NOT EXISTS model_operations_observed_at_brin ON model_operations USING brin (observed_at);
CREATE INDEX IF NOT EXISTS token_usage_observed_at_brin ON token_usage USING brin (observed_at);

-- B-tree lookup indexes on session/component/source foreign keys.
CREATE INDEX IF NOT EXISTS events_session_idx ON events USING btree (session_id, observed_at);
CREATE INDEX IF NOT EXISTS events_component_idx ON events USING btree (component_id, observed_at);
CREATE INDEX IF NOT EXISTS events_source_instance_idx ON events USING btree (source_instance_id, observed_at);
CREATE INDEX IF NOT EXISTS tool_calls_session_idx ON tool_calls USING btree (session_id, observed_at);
CREATE INDEX IF NOT EXISTS tool_calls_component_idx ON tool_calls USING btree (component_id, observed_at);
CREATE INDEX IF NOT EXISTS component_lifecycle_events_installation_idx ON component_lifecycle_events USING btree (component_installation_id, observed_at);

-- Rollup lookup indexes for range queries within one metric family/dimension scope.
CREATE INDEX IF NOT EXISTS metric_rollups_hourly_lookup ON metric_rollups_hourly USING btree (metric_family, dimension_scope, bucket_start);
CREATE INDEX IF NOT EXISTS metric_rollups_daily_lookup ON metric_rollups_daily USING btree (metric_family, dimension_scope, bucket_start);
