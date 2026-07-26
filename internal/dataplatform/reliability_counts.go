package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionReliabilityCounts1 is the registered formula version for the
// reliability counts query.
const FormulaVersionReliabilityCounts1 = "reliability_counts/2"

// ReliabilityCounts executes the "reliability_counts_range" budgeted query:
// one row per requested calendar bucket inside the half-open [from, to) range with
// reliability.unknown_schema_count (from schema_quarantine_metadata,
// bucketed by its own observed_at) and reliability.reconciliation_mismatch_count
// (from reconciliation_mismatches, which has no timestamp column of its
// own -- see migrations/0001_core_schema.up.sql -- so it is bucketed by the
// period its parent reconciliation_runs.started_at falls in). Serves the "/"
// overview-incidents panel and /reliability "reliability-drift" panel.
//
// Neither internal/runtime/api.go's health() nor completeness() handler
// exposes either of these counts today (health() only reports open
// integrity_incidents; completeness() only reports events.value_state
// distribution), so this is new, non-duplicative backend surface.
func ReliabilityCounts(ctx context.Context, pool *pgxpool.Pool, from, to time.Time, bucket TimeBucketSpec) (ReliabilityCountsResponse, error) {
	budget := Budgets["reliability_counts_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return ReliabilityCountsResponse{}, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		WITH accepted_days AS (
			SELECT date_trunc($3, observed_at, $4) AS day, count(*) AS accepted_count
			FROM events
			WHERE observed_at >= $1 AND observed_at < $2
			GROUP BY day
		),
		schema_days AS (
			SELECT date_trunc($3, observed_at, $4) AS day, count(*) AS unknown_schema_count
			FROM schema_quarantine_metadata
			WHERE observed_at >= $1 AND observed_at < $2
			GROUP BY day
		),
		mismatch_days AS (
			SELECT date_trunc($3, rr.started_at, $4) AS day, count(*) AS reconciliation_mismatch_count
			FROM reconciliation_mismatches rm
			JOIN reconciliation_runs rr ON rr.reconciliation_run_id = rm.reconciliation_run_id
			WHERE rr.started_at >= $1 AND rr.started_at < $2
			GROUP BY day
		)
		SELECT coalesce(a.day, s.day, m.day) AS day,
			coalesce(a.accepted_count, 0) AS accepted_count,
			coalesce(s.unknown_schema_count, 0) AS unknown_schema_count,
			coalesce(m.reconciliation_mismatch_count, 0) AS reconciliation_mismatch_count
		FROM accepted_days a
		FULL OUTER JOIN schema_days s ON s.day = a.day
		FULL OUTER JOIN mismatch_days m ON m.day = coalesce(a.day, s.day)
		ORDER BY day
	`, from, to, bucket.SQLUnit(), bucket.Timezone)
	if err != nil {
		return ReliabilityCountsResponse{}, budgetOrErr(budget, started, err)
	}
	var response ReliabilityCountsResponse
	var totalAccepted, totalUnknownSchema, totalMismatches int64
	for rows.Next() {
		var row ReliabilityCountsDayRow
		var accepted int64
		if err := rows.Scan(&row.Day, &accepted, &row.UnknownSchemaCount, &row.ReconciliationMismatchCount); err != nil {
			rows.Close()
			return ReliabilityCountsResponse{}, err
		}
		response.Data = append(response.Data, row)
		totalAccepted += accepted
		totalUnknownSchema += row.UnknownSchemaCount
		totalMismatches += row.ReconciliationMismatchCount
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ReliabilityCountsResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return ReliabilityCountsResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}

	response.FormulaVersion = FormulaVersionReliabilityCounts1
	// These are exact metadata counts of adverse events (contracts/metrics.yaml
	// exactness: "exact metadata count" / "exact"), not a ratio against an
	// eligible population; both counts equal their own denominator, matching
	// the same "data present" completeness convention used by
	// ReliabilityCoverageTimeline for genuinely denominator-less counters.
	total := totalAccepted + totalUnknownSchema + totalMismatches
	response.Population = Population{Numerator: total, Denominator: total}
	response.Completeness = completenessFor(total, total)
	return response, nil
}
