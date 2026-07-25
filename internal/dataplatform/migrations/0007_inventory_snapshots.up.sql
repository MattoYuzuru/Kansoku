-- Durable, privacy-safe adapter inventory snapshots and their current
-- component projection. Snapshot rows contain only the closed adaptersdk
-- graph: declared names, versions, scopes, pseudonyms and fingerprints.

ALTER TABLE components
    ADD COLUMN IF NOT EXISTS declared_name TEXT,
    ADD COLUMN IF NOT EXISTS source_scope TEXT,
    ADD COLUMN IF NOT EXISTS path_pseudonym TEXT,
    ADD COLUMN IF NOT EXISTS inventory_fingerprint TEXT,
    ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ;

ALTER TABLE component_versions
    ADD COLUMN IF NOT EXISTS version_state TEXT NOT NULL DEFAULT 'observed'
        CHECK (version_state IN ('not_observed', 'observed'));

CREATE TABLE IF NOT EXISTS inventory_snapshots (
    snapshot_id          TEXT PRIMARY KEY,
    adapter_id           TEXT NOT NULL,
    adapter_version      TEXT NOT NULL,
    agent_installation_id TEXT NOT NULL REFERENCES agent_installations(agent_installation_id),
    observed_at          TIMESTAMPTZ NOT NULL,
    fingerprint          TEXT NOT NULL,
    completeness         TEXT NOT NULL
        CHECK (completeness IN ('complete', 'partial', 'degraded', 'unknown'))
);

CREATE TABLE IF NOT EXISTS inventory_nodes (
    snapshot_id     TEXT NOT NULL REFERENCES inventory_snapshots(snapshot_id) ON DELETE CASCADE,
    node_id         TEXT NOT NULL,
    kind            TEXT NOT NULL,
    declared_name   TEXT NOT NULL,
    version         TEXT,
    source_scope    TEXT NOT NULL,
    path_pseudonym  TEXT,
    display_alias   TEXT,
    cached_only     BOOLEAN NOT NULL DEFAULT FALSE,
    fingerprint     TEXT NOT NULL,
    PRIMARY KEY (snapshot_id, node_id)
);

CREATE TABLE IF NOT EXISTS inventory_edges (
    snapshot_id   TEXT NOT NULL REFERENCES inventory_snapshots(snapshot_id) ON DELETE CASCADE,
    edge_id       TEXT NOT NULL,
    kind          TEXT NOT NULL,
    from_node_id  TEXT NOT NULL,
    to_node_id    TEXT NOT NULL,
    PRIMARY KEY (snapshot_id, edge_id),
    FOREIGN KEY (snapshot_id, from_node_id)
        REFERENCES inventory_nodes(snapshot_id, node_id) ON DELETE CASCADE,
    FOREIGN KEY (snapshot_id, to_node_id)
        REFERENCES inventory_nodes(snapshot_id, node_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS component_inventory_state (
    component_installation_id TEXT PRIMARY KEY
        REFERENCES component_installations(component_installation_id) ON DELETE CASCADE,
    inventory_node_id         TEXT NOT NULL,
    enabled                   BOOLEAN NOT NULL,
    first_seen_at             TIMESTAMPTZ NOT NULL,
    last_seen_at              TIMESTAMPTZ NOT NULL,
    last_snapshot_id          TEXT NOT NULL REFERENCES inventory_snapshots(snapshot_id),
    UNIQUE (inventory_node_id, component_installation_id)
);

CREATE TABLE IF NOT EXISTS inventory_collection_status (
    target_id              TEXT PRIMARY KEY,
    adapter_id             TEXT NOT NULL,
    agent_installation_id  TEXT,
    state                  TEXT NOT NULL
        CHECK (state IN ('complete', 'partial', 'degraded', 'not_observed')),
    error_class            TEXT,
    last_attempted_at      TIMESTAMPTZ NOT NULL,
    last_succeeded_at      TIMESTAMPTZ,
    snapshot_id            TEXT REFERENCES inventory_snapshots(snapshot_id),
    node_count             BIGINT NOT NULL DEFAULT 0 CHECK (node_count >= 0),
    edge_count             BIGINT NOT NULL DEFAULT 0 CHECK (edge_count >= 0)
);

CREATE INDEX IF NOT EXISTS inventory_snapshots_installation_observed_idx
    ON inventory_snapshots (agent_installation_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS inventory_nodes_kind_name_idx
    ON inventory_nodes (kind, declared_name);
CREATE INDEX IF NOT EXISTS component_inventory_state_last_seen_idx
    ON component_inventory_state (last_seen_at DESC, enabled);
