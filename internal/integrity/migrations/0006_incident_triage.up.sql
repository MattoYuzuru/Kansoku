-- Session 12 extends the existing integrity incident projection with
-- detector/triage separation. Cross-projection workbench tables are applied
-- later by runtime migration 0002, after both data-platform and integrity
-- migrations have completed.
ALTER TABLE integrity_incidents ADD COLUMN IF NOT EXISTS detector_state TEXT NOT NULL DEFAULT 'open'
    CHECK (detector_state IN ('open', 'recovering', 'resolved'));
ALTER TABLE integrity_incidents ADD COLUMN IF NOT EXISTS triage_state TEXT NOT NULL DEFAULT 'new'
    CHECK (triage_state IN ('new', 'acknowledged', 'investigating', 'action_ready'));
ALTER TABLE integrity_incidents ADD COLUMN IF NOT EXISTS triage_note_category TEXT
    CHECK (triage_note_category IS NULL OR triage_note_category IN (
        'fixture_needed', 'parser_fix_prepared', 'source_owner_contacted', 'recovery_pending'
    ));
UPDATE integrity_incidents
SET detector_state = CASE WHEN resolved_at IS NULL THEN 'open' ELSE 'resolved' END;
CREATE INDEX IF NOT EXISTS idx_integrity_incidents_workbench_page
    ON integrity_incidents (last_observed_at DESC, incident_id DESC);
