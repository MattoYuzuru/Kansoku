package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ApplyIncidentMetadataRetention expires only Session 12 detail rows. The
// aggregate incident identity/count/lifecycle remains durable, and every
// removed occurrence is added to an explicit exclusion counter in the same
// transaction. Partitioned fact retention remains drop-only.
func ApplyIncidentMetadataRetention(
	ctx context.Context,
	pool *pgxpool.Pool,
	now time.Time,
	horizonDays int,
) (map[string]int64, error) {
	if pool == nil || now.IsZero() || horizonDays < 30 || horizonDays > 3650 {
		return nil, errors.New("invalid_incident_retention")
	}
	cutoff := now.UTC().AddDate(0, 0, -horizonDays)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var occurrences int64
	if err := tx.QueryRow(ctx, `
		WITH deleted AS (
			DELETE FROM incident_occurrences o
			USING incidents i
			WHERE o.incident_id=i.incident_id
			  AND o.observed_at < $1
			  AND o.idempotency_key NOT LIKE 'legacy:%'
			RETURNING o.incident_id
		),
		grouped AS (
			SELECT incident_id, count(*)::bigint AS excluded
			FROM deleted GROUP BY incident_id
		),
		updated AS (
			UPDATE incidents i
			SET occurrence_retention_excluded_count =
			        i.occurrence_retention_excluded_count + g.excluded,
			    updated_at=now()
			FROM grouped g
			WHERE i.incident_id=g.incident_id
			RETURNING g.excluded
		)
		SELECT COALESCE(sum(excluded),0)::bigint FROM updated
	`, cutoff).Scan(&occurrences); err != nil {
		return nil, err
	}

	manifestTag, err := tx.Exec(ctx, `
		DELETE FROM quarantine_structural_manifests WHERE last_seen_at < $1
	`, cutoff)
	if err != nil {
		return nil, err
	}
	quarantineTag, err := tx.Exec(ctx, `
		DELETE FROM schema_quarantine_metadata q
		WHERE q.observed_at < $1
		  AND NOT EXISTS (
		      SELECT 1 FROM quarantine_structural_manifests m
		      WHERE m.quarantine_id=q.quarantine_id
		  )
	`, cutoff)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]int64{
		"incident_occurrences":            occurrences,
		"quarantine_structural_manifests": manifestTag.RowsAffected(),
		"schema_quarantine_metadata":      quarantineTag.RowsAffected(),
	}, nil
}
