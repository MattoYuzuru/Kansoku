package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionPromptShape1 is the registered formula version for the
// prompt shape query.
const FormulaVersionPromptShape1 = "prompt_shape/1"

// PromptShape executes the "prompt_shape_range" budgeted query: one row per
// calendar day inside the half-open [from, to) range with the submitted
// prompt count and exact percentile_cont byte-length percentiles from
// prompt_features.prompt_size_bytes. Serves the /prompts "prompt-shape"
// panel (activity.prompts, prompt.utf8_bytes).
//
// prompt_size_bytes is nullable (see migrations/0001_core_schema.up.sql);
// rows with a null size are still counted toward prompt_count (a prompt was
// genuinely submitted) but excluded from the percentile computation via
// percentile_cont's FILTER clause, matching prompt.utf8_bytes's
// "null_policy": "explicit_exclusion_only" evaluator parameter.
func PromptShape(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (PromptShapeResponse, error) {
	budget := Budgets["prompt_shape_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return PromptShapeResponse{}, err
	}
	defer release()

	started := time.Now()
	rows, err := conn.Query(ctx, `
		SELECT date_trunc('day', pf.observed_at) AS day,
			count(*) AS prompt_count,
			percentile_cont(0.50) WITHIN GROUP (ORDER BY pf.prompt_size_bytes) FILTER (WHERE pf.prompt_size_bytes IS NOT NULL) AS p50,
			percentile_cont(0.90) WITHIN GROUP (ORDER BY pf.prompt_size_bytes) FILTER (WHERE pf.prompt_size_bytes IS NOT NULL) AS p90,
			percentile_cont(0.95) WITHIN GROUP (ORDER BY pf.prompt_size_bytes) FILTER (WHERE pf.prompt_size_bytes IS NOT NULL) AS p95,
			percentile_cont(0.99) WITHIN GROUP (ORDER BY pf.prompt_size_bytes) FILTER (WHERE pf.prompt_size_bytes IS NOT NULL) AS p99
		FROM prompt_features pf
		WHERE pf.observed_at >= $1 AND pf.observed_at < $2
		GROUP BY day
		ORDER BY day
	`, from, to)
	if err != nil {
		return PromptShapeResponse{}, budgetOrErr(budget, started, err)
	}
	var response PromptShapeResponse
	var totalPrompts int64
	for rows.Next() {
		var row PromptShapeDayRow
		var p Percentiles
		if err := rows.Scan(&row.Day, &row.PromptCount, &p.P50, &p.P90, &p.P95, &p.P99); err != nil {
			rows.Close()
			return PromptShapeResponse{}, err
		}
		if p.P50 != nil || p.P90 != nil || p.P95 != nil || p.P99 != nil {
			row.Percentiles = &p
		}
		response.Data = append(response.Data, row)
		totalPrompts += row.PromptCount
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PromptShapeResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return PromptShapeResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}

	response.FormulaVersion = FormulaVersionPromptShape1
	response.Population = Population{Numerator: totalPrompts, Denominator: totalPrompts}
	response.Completeness = completenessFor(totalPrompts, totalPrompts)

	watermark, pending, err := aggregateSourceWatermarkFreshness(ctx, pool)
	if err != nil {
		return PromptShapeResponse{}, err
	}
	response.Freshness = Freshness{RollupWatermark: watermark, LateEventsPending: pending}
	return response, nil
}
