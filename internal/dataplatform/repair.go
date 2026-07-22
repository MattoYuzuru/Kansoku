package dataplatform

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RepairWorkItem is one claimed, coalesced repair unit.
type RepairWorkItem struct {
	RepairID       int64
	MetricFamily   string
	Granularity    Granularity
	BucketStart    time.Time
	DimensionScope string
}

// ClaimRepairBatch claims up to `limit` pending repair rows (oldest first)
// using SELECT ... FOR UPDATE SKIP LOCKED so concurrent workers never
// recompute the same bucket twice, then deletes the coalesced rows so a
// duplicate late event enqueued during recompute produces a fresh row for
// the next pass instead of being silently dropped.
func ClaimRepairBatch(ctx context.Context, pool *pgxpool.Pool, limit int) ([]RepairWorkItem, error) {
	var items []RepairWorkItem
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT repair_id, metric_family, granularity, bucket_start, dimension_scope
			FROM rollup_repair_queue
			ORDER BY enqueued_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		`, limit)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var item RepairWorkItem
			var granularity string
			if err := rows.Scan(&item.RepairID, &item.MetricFamily, &granularity, &item.BucketStart, &item.DimensionScope); err != nil {
				rows.Close()
				return err
			}
			item.Granularity = Granularity(granularity)
			items = append(items, item)
			ids = append(ids, item.RepairID)
		}
		rows.Close()
		if len(ids) == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM rollup_repair_queue WHERE repair_id = ANY($1)`, ids); err != nil {
			return err
		}
		return nil
	})
	return items, err
}

// RecomputeBucket recomputes one metric-family/granularity/bucket/dimension
// rollup directly from normalized facts using exact percentile_cont (never
// averaging previously computed percentiles) and upserts the result,
// replacing whatever value previously existed for that exact bucket. This is
// the sole rollup write path: on-time and late data take the same route, so
// a late event naturally "repairs" a bucket by full recomputation.
func RecomputeBucket(ctx context.Context, pool *pgxpool.Pool, item RepairWorkItem) error {
	table := "metric_rollups_hourly"
	if item.Granularity == GranularityDaily {
		table = "metric_rollups_daily"
	}
	start := item.BucketStart
	end := bucketEnd(start, item.Granularity)
	parts := strings.SplitN(item.DimensionScope, "|", 4)
	if len(parts) != 4 {
		return fmt.Errorf("invalid dimension scope %q", item.DimensionScope)
	}
	agentInstallationID, surfaceID, componentID, eventType := parts[0], parts[1], parts[2], parts[3]

	row := pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE duration_ms IS NOT NULL) AS event_count,
			count(*) FILTER (WHERE duration_ms IS NULL) AS unknown_count,
			avg(duration_ms) FILTER (WHERE duration_ms IS NOT NULL),
			percentile_cont(0.5) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL),
			percentile_cont(0.9) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL),
			percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL),
			percentile_cont(0.99) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL)
		FROM events
		WHERE observed_at >= $1 AND observed_at < $2
			AND agent_installation_id = $3 AND surface_id = $4 AND component_id = $5 AND event_type = $6
	`, start, end, nullableString(agentInstallationID), nullableString(surfaceID), nullableString(componentID), eventType)

	var eventCount, unknownCount int64
	var avg, p50, p90, p95, p99 *float64
	if err := row.Scan(&eventCount, &unknownCount, &avg, &p50, &p90, &p95, &p99); err != nil {
		return fmt.Errorf("recompute bucket scan: %w", err)
	}

	_, err := pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			metric_family, bucket_start, dimension_scope, formula_version, event_count,
			unknown_count, completeness_duration_ms, value_numeric, value_p50, value_p90,
			value_p95, value_p99, computed_at
		) VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8,$9,$10,$11, now())
		ON CONFLICT (metric_family, bucket_start, dimension_scope) DO UPDATE SET
			formula_version = EXCLUDED.formula_version,
			event_count = EXCLUDED.event_count,
			unknown_count = EXCLUDED.unknown_count,
			value_numeric = EXCLUDED.value_numeric,
			value_p50 = EXCLUDED.value_p50,
			value_p90 = EXCLUDED.value_p90,
			value_p95 = EXCLUDED.value_p95,
			value_p99 = EXCLUDED.value_p99,
			computed_at = EXCLUDED.computed_at
	`, table), item.MetricFamily, start, item.DimensionScope, FormulaVersionLatencyMS1, eventCount, unknownCount, avg, p50, p90, p95, p99)
	if err != nil {
		return fmt.Errorf("recompute bucket upsert: %w", err)
	}

	// Advance the rollup watermark for this scope only after a successful
	// recompute commit, per contracts/data-platform/rollups.yaml
	// `late_data_algorithm.watermark_advance`.
	_, err = pool.Exec(ctx, `
		INSERT INTO rollup_status (metric_family, granularity, dimension_scope, rollup_watermark, late_events_pending)
		VALUES ($1, $2, $3, now(), 0)
		ON CONFLICT (metric_family, granularity, dimension_scope) DO UPDATE SET
			rollup_watermark = GREATEST(rollup_status.rollup_watermark, EXCLUDED.rollup_watermark)
	`, item.MetricFamily, string(item.Granularity), item.DimensionScope)
	return err
}

// RunRepairWorker drains the repair queue until empty, recomputing each
// claimed bucket. It returns the number of buckets recomputed.
func RunRepairWorker(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	total := 0
	for {
		batch, err := ClaimRepairBatch(ctx, pool, 64)
		if err != nil {
			return total, err
		}
		if len(batch) == 0 {
			return total, nil
		}
		for _, item := range batch {
			if err := RecomputeBucket(ctx, pool, item); err != nil {
				return total, err
			}
			total++
		}
	}
}
