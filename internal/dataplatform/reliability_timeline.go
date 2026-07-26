package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionReliabilityTimeline1 is the registered formula version for
// the reliability coverage timeline query.
const FormulaVersionReliabilityTimeline1 = "reliability_coverage_timeline/2"

// ReliabilityCoverageTimeline executes the "reliability_coverage_timeline"
// budgeted query: one row per (source, requested calendar bucket, status) combination
// recorded in completeness_intervals whose interval overlaps the half-open
// [from, to) range. Serves the /reliability "coverage timeline; source
// gaps/watermarks" panel.
//
// completeness_intervals.dimension_scope is a generic JSONB blob (see
// internal/runtime/export.go portableCompleteness) with no DB-enforced key
// set; this query reads the conventional "source_instance_id" key used
// everywhere else in the schema. An interval whose dimension_scope omits
// that key is still counted (grouped under an empty source id) rather than
// silently dropped, since the fact itself remains real and observed.
func ReliabilityCoverageTimeline(ctx context.Context, pool *pgxpool.Pool, from, to time.Time, bucket TimeBucketSpec) (ReliabilityTimelineResponse, error) {
	budget := Budgets["reliability_coverage_timeline"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return ReliabilityTimelineResponse{}, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		SELECT date_trunc($3, interval_start, $4) AS day,
			coalesce(dimension_scope->>'source_instance_id', '') AS source_instance_id,
			status,
			count(*) AS interval_count
		FROM completeness_intervals
		WHERE interval_start < $2 AND interval_end > $1
		GROUP BY day, source_instance_id, status
		ORDER BY day, source_instance_id, status
	`, from, to, bucket.SQLUnit(), bucket.Timezone)
	if err != nil {
		return ReliabilityTimelineResponse{}, budgetOrErr(budget, started, err)
	}
	var response ReliabilityTimelineResponse
	var numerator, denominator int64
	for rows.Next() {
		var row ReliabilityDayRow
		if err := rows.Scan(&row.Day, &row.SourceInstanceID, &row.Status, &row.IntervalCount); err != nil {
			rows.Close()
			return ReliabilityTimelineResponse{}, err
		}
		response.Data = append(response.Data, row)
		denominator += row.IntervalCount
		if row.Status == "complete" {
			numerator += row.IntervalCount
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ReliabilityTimelineResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return ReliabilityTimelineResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}
	response.FormulaVersion = FormulaVersionReliabilityTimeline1
	response.Population = Population{Numerator: numerator, Denominator: denominator}
	response.Completeness = completenessFor(numerator, denominator)

	watermark, pending, err := aggregateSourceWatermarkFreshness(ctx, pool)
	if err != nil {
		return ReliabilityTimelineResponse{}, err
	}
	response.Freshness = Freshness{RollupWatermark: watermark, LateEventsPending: pending}
	return response, nil
}

// aggregateSourceWatermarkFreshness reports the earliest last_committed_at
// across all known sources as a conservative overall watermark (the
// timeline is only as fresh as its least-fresh source), and the total gap
// count as the pending-repair signal. Returns the zero time and a nil-safe
// zero count when no source_watermarks rows exist yet, which
// completenessFor-style callers must treat as "unknown", never "complete".
func aggregateSourceWatermarkFreshness(ctx context.Context, pool *pgxpool.Pool) (time.Time, int64, error) {
	var watermark *time.Time
	var gapTotal int64
	err := pool.QueryRow(ctx, `
		SELECT min(last_committed_at), coalesce(sum(gap_count), 0)
		FROM source_watermarks
	`).Scan(&watermark, &gapTotal)
	if err != nil {
		return time.Time{}, 0, err
	}
	if watermark == nil {
		return time.Time{}, gapTotal, nil
	}
	return *watermark, gapTotal, nil
}
