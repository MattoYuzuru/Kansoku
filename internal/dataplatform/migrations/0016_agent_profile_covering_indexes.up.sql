-- Agent profiles read exact range-bounded counters from the raw normalized
-- facts. Cover the closed metadata columns used by activity/source contours
-- so the 200 ms profile budget does not require wide heap scans.

CREATE INDEX IF NOT EXISTS events_agent_profile_cover_idx
    ON events (agent_installation_id, observed_at)
    INCLUDE (session_id, event_type, outcome, component_id, source_instance_id);

CREATE INDEX IF NOT EXISTS event_evidence_source_observed_idx
    ON event_evidence (source_instance_id, observed_at);
