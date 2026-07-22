package dataplatform

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// bucketStart truncates `at` to the start of its hourly or daily bucket in
// UTC, matching the half-open bucket rule in
// contracts/data-platform/rollups.yaml.
func BucketStart(at time.Time, granularity Granularity) time.Time {
	at = at.UTC()
	switch granularity {
	case GranularityHourly:
		return time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), 0, 0, 0, time.UTC)
	case GranularityDaily:
		return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	default:
		return at
	}
}

func bucketEnd(start time.Time, granularity Granularity) time.Time {
	switch granularity {
	case GranularityHourly:
		return start.Add(time.Hour)
	case GranularityDaily:
		return start.AddDate(0, 0, 1)
	default:
		return start
	}
}

// MetricFamilyLatencyMS is the registered duration_ms rollup formula. Its
// percentile levels come from contracts/data-platform/rollups.yaml
// `percentile_policy.levels`.
const MetricFamilyLatencyMS = "component.duration_ms"

// FormulaVersionLatencyMS1 is formula_versions row (MetricFamilyLatencyMS, 1).
const FormulaVersionLatencyMS1 = "component.duration_ms/1"

// enqueueRepairForFact enqueues the affected (metric_family, bucket_start,
// dimension_scope) rollup repair keys for both granularities whenever a
// fact commits, whether on-time or late. Coalescing is provided by the
// queue's UNIQUE constraint plus ON CONFLICT DO NOTHING.
func enqueueRepairForFact(ctx context.Context, tx pgx.Tx, fact FactRow) error {
	scope := dimensionScope(fact)
	for _, granularity := range []Granularity{GranularityHourly, GranularityDaily} {
		bucket := BucketStart(fact.ObservedAt, granularity)
		if _, err := tx.Exec(ctx, `
			INSERT INTO rollup_repair_queue (metric_family, granularity, bucket_start, dimension_scope)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (metric_family, granularity, bucket_start, dimension_scope) DO NOTHING
		`, MetricFamilyLatencyMS, string(granularity), bucket, scope); err != nil {
			return fmt.Errorf("enqueue repair: %w", err)
		}
	}
	return nil
}

// dimensionScope is the bounded dimension key from
// contracts/data-platform/rollups.yaml `dimension_scope_fields`; it never
// grows unboundedly because it is a fixed 4-tuple, not a free-form label.
func dimensionScope(fact FactRow) string {
	return fmt.Sprintf("%s|%s|%s|%s", fact.AgentInstallationID, fact.SurfaceID, fact.ComponentID, fact.EventType)
}

// RepairQueueDepth returns the number of un-coalesced pending repair rows,
// used as `freshness.late_events_pending` in the query contract.
func RepairQueueDepth(ctx context.Context, tx pgxQuerier) (int64, error) {
	var count int64
	err := tx.QueryRow(ctx, `SELECT count(*) FROM rollup_repair_queue`).Scan(&count)
	return count, err
}

// pgxQuerier is satisfied by both *pgxpool.Pool and pgx.Tx, letting rollup
// helpers run either standalone or inside a caller's transaction.
type pgxQuerier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}
