-- Session 08 durable Incident table. This is a Postgres-native row shape
-- for the SAME internal/observability.Incident concept
-- (IncidentID, Capability, Category, Completeness, OpenedAt, LastObserved,
-- ResolvedAt, OccurrenceCount) -- contracts/integrity/incident-and-health.yaml's
-- one_incident_concept_rule requires this package to emit into that one
-- concept rather than invent a second, competing incident type. Because
-- internal/observability's own store (FileStore/DurableState) is a
-- pre-Session-04 file-based JSON store with no PostgreSQL table of its own,
-- this migration gives the identical struct shape a durable home in the
-- shared PostgreSQL instance the audit engine already uses, keyed
-- identically (incident_id) so the two representations never diverge in
-- meaning. integrity_incident_details (0002) extends this row 1:1 by
-- incident_id, exactly as incident-and-health.yaml's incident_field_extension
-- describes.

CREATE TABLE IF NOT EXISTS integrity_incidents (
    incident_id       TEXT PRIMARY KEY,
    capability        TEXT NOT NULL,
    category          TEXT NOT NULL,
    completeness      TEXT NOT NULL,
    opened_at         TIMESTAMPTZ NOT NULL,
    last_observed_at  TIMESTAMPTZ NOT NULL,
    resolved_at       TIMESTAMPTZ,
    occurrence_count  BIGINT NOT NULL DEFAULT 1,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_integrity_incidents_resolved_at ON integrity_incidents (resolved_at);

-- 0002 must remain independently reversible, so its extension table is
-- created before this base table. Enforce the promised 1:1 relationship as
-- soon as the base exists. NOT VALID followed by VALIDATE makes an upgrade
-- fail visibly if a pre-release database somehow contains orphan details;
-- no orphan is silently deleted or rewritten.
ALTER TABLE integrity_incident_details
    DROP CONSTRAINT IF EXISTS fk_integrity_incident_details_base;
ALTER TABLE integrity_incident_details
    ADD CONSTRAINT fk_integrity_incident_details_base
    FOREIGN KEY (incident_id) REFERENCES integrity_incidents (incident_id)
    ON DELETE CASCADE NOT VALID;
ALTER TABLE integrity_incident_details
    VALIDATE CONSTRAINT fk_integrity_incident_details_base;
