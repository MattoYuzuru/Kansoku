-- Session 12 incident-workbench reconciliation.
-- Read-only, aggregate-only, and safe to run through psql with ON_ERROR_STOP.
BEGIN TRANSACTION READ ONLY;

SELECT 'legacy_ingress_incidents' AS measure, count(*)::bigint AS value
FROM incidents
UNION ALL
SELECT 'rich_integrity_incidents', count(*)::bigint
FROM integrity_incidents
UNION ALL
SELECT 'occurrence_rows', count(*)::bigint
FROM incident_occurrences
UNION ALL
SELECT 'structural_manifests', count(*)::bigint
FROM quarantine_structural_manifests
UNION ALL
SELECT 'legacy_quarantine_rows', count(*)::bigint
FROM schema_quarantine_metadata
ORDER BY measure;

SELECT
    count(*) FILTER (
        WHERE NOT EXISTS (
            SELECT 1 FROM incidents i WHERE i.incident_id=o.incident_id
        )
        AND NOT EXISTS (
            SELECT 1 FROM integrity_incidents i WHERE i.incident_id=o.incident_id
        )
    ) AS orphan_occurrences,
    count(*) FILTER (
        WHERE o.idempotency_key LIKE 'legacy:%'
    ) AS migration_backfill_occurrences
FROM incident_occurrences o;

SELECT
    count(*) AS nonlegacy_occurrence_count_mismatches
FROM incidents i
WHERE NOT EXISTS (
    SELECT 1
    FROM incident_occurrences o
    WHERE o.incident_id=i.incident_id
      AND o.idempotency_key LIKE 'legacy:%'
)
AND i.occurrence_count <> (
    SELECT count(*)
    FROM incident_occurrences o
    WHERE o.incident_id=i.incident_id
);

SELECT
    count(*) FILTER (
        WHERE incident_id LIKE 'inc_unlinked_%'
    ) AS legacy_manifests_with_explicit_unlinked_lineage,
    count(*) FILTER (
        WHERE shape_value_state='not_observed'
    ) AS manifests_without_observable_safe_shape,
    count(*) FILTER (
        WHERE disposition='unresolved'
    ) AS unresolved_manifests
FROM quarantine_structural_manifests;

SELECT
    count(*) FILTER (
        WHERE detector_state='resolved'
          AND recovery_audit_run_id IS NOT NULL
          AND recovery_evidence_ref IS NOT NULL
    ) AS resolved_with_session12_recovery_lineage,
    count(*) FILTER (
        WHERE detector_state='resolved'
          AND recovery_audit_run_id IS NULL
    ) AS resolved_legacy_lineage_exclusions
FROM incidents;

ROLLBACK;
