DROP TABLE IF EXISTS integrity_audit_reports;
DROP TABLE IF EXISTS integrity_live_canary_state;
DROP TABLE IF EXISTS integrity_schema_compatibility;
DROP TABLE IF EXISTS integrity_fingerprints;
DROP TABLE IF EXISTS integrity_audit_attempts;
DROP INDEX IF EXISTS idx_integrity_audit_checks_source_key;
ALTER TABLE integrity_audit_checks DROP CONSTRAINT IF EXISTS integrity_audit_checks_pkey;
ALTER TABLE integrity_audit_checks
    ADD PRIMARY KEY (audit_run_id, check_id, capability_id, installation_id);
ALTER TABLE integrity_audit_checks DROP COLUMN IF EXISTS source_id;
