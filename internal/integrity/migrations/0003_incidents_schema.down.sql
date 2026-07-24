ALTER TABLE integrity_incident_details
    DROP CONSTRAINT IF EXISTS fk_integrity_incident_details_base;
DROP TABLE IF EXISTS integrity_incidents;
