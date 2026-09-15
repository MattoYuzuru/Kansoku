ALTER TABLE components DROP CONSTRAINT IF EXISTS components_kind_check;
ALTER TABLE components ADD CONSTRAINT components_kind_check
    CHECK (kind IN ('skill', 'plugin', 'mcp', 'hook', 'command', 'app'));

ALTER TABLE component_relations DROP CONSTRAINT IF EXISTS component_relations_relation_kind_check;
ALTER TABLE component_relations ADD CONSTRAINT component_relations_relation_kind_check
    CHECK (relation_kind IN ('bundles', 'provides', 'collides_with', 'shadows'));

CREATE TABLE IF NOT EXISTS component_relation_observations (
    relation_observation_id TEXT PRIMARY KEY,
    relation_id             TEXT NOT NULL REFERENCES component_relations(relation_id),
    inventory_snapshot_id   TEXT NOT NULL REFERENCES inventory_snapshots(snapshot_id),
    source_instance_id      TEXT NOT NULL REFERENCES source_instances(source_instance_id),
    observed_at             TIMESTAMPTZ NOT NULL,
    completeness            TEXT NOT NULL CHECK (completeness IN (
        'complete','partial','degraded','unknown'
    )),
    adapter_version         TEXT NOT NULL,
    schema_version          TEXT NOT NULL,
    idempotency_key         TEXT NOT NULL,
    UNIQUE (source_instance_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS component_relation_observations_snapshot_idx
    ON component_relation_observations (inventory_snapshot_id, relation_id);
CREATE INDEX IF NOT EXISTS component_relation_observations_relation_time_idx
    ON component_relation_observations (relation_id, observed_at DESC);
