DROP INDEX IF EXISTS idx_integrity_incidents_workbench_page;
ALTER TABLE integrity_incidents DROP COLUMN IF EXISTS triage_note_category;
ALTER TABLE integrity_incidents DROP COLUMN IF EXISTS triage_state;
ALTER TABLE integrity_incidents DROP COLUMN IF EXISTS detector_state;
