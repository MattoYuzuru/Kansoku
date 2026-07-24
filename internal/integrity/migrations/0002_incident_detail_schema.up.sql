-- Session 08 incident-detail extension, matching
-- contracts/integrity/incident-and-health.yaml's incident_field_extension:
-- a session-08-owned wrapper record keyed 1:1 by IncidentID, referencing
-- internal/observability's existing Incident by ID rather than forking it.

CREATE TABLE IF NOT EXISTS integrity_incident_details (
    incident_id           TEXT PRIMARY KEY,
    installation_id       TEXT NOT NULL,
    source_id             TEXT NOT NULL,
    capability_id         TEXT NOT NULL,
    failure_class         TEXT NOT NULL,
    first_seen_at         TIMESTAMPTZ NOT NULL,
    affected_interval_from TIMESTAMPTZ NOT NULL,
    affected_interval_to   TIMESTAMPTZ NOT NULL,
    check_evidence_ref    TEXT NOT NULL,
    agent_or_adapter_version TEXT,
    recovery_criteria     TEXT,
    user_notes            TEXT,
    resolved_at           TIMESTAMPTZ,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_integrity_incident_details_key
    ON integrity_incident_details (installation_id, source_id, capability_id, failure_class);
CREATE INDEX IF NOT EXISTS idx_integrity_incident_details_open
    ON integrity_incident_details (resolved_at);
