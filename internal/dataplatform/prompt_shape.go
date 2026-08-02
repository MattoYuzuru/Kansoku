package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionPromptShape1 is the registered formula version for the
// prompt shape query.
//
// /4 changed two things a reader of the numbers would otherwise get wrong.
// Buckets with no prompt are now emitted with prompt_count = 0 instead of
// being absent, so "no prompts in this hour" stops looking identical to "this
// hour was never observed". And the population stopped being tautological:
// the numerator is now the prompts that actually carried a length and so
// contributed to the percentile band, the denominator is every prompt in
// range, and the difference is named in Exclusions. Before, both were the
// same total, which reported 100% coverage of a band that could have been
// computed from a single prompt out of hundreds.
const FormulaVersionPromptShape1 = "prompt_shape/4"

// PromptShape executes the "prompt_shape_range" budgeted query: one row per
// requested calendar bucket inside the half-open [from, to) range with the submitted
// prompt count and exact percentile_cont character-length percentiles from
// native OTel prompt metadata, with the older UTF-8 byte measurement kept as
// a fallback for hook/transcript sources.
//
// prompt_size_bytes is nullable (see migrations/0001_core_schema.up.sql);
// rows with a null size are still counted toward prompt_count (a prompt was
// genuinely submitted) but excluded from the percentile computation via
// percentile_cont's FILTER clause, matching prompt.utf8_bytes's
// "null_policy": "explicit_exclusion_only" evaluator parameter.
func PromptShape(ctx context.Context, pool *pgxpool.Pool, from, to time.Time, bucket TimeBucketSpec) (PromptShapeResponse, error) {
	budget := Budgets["prompt_shape_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return PromptShapeResponse{}, err
	}
	defer release()

	started := time.Now()
	// generate_series produces every bucket the requested range covers, and
	// the aggregate is LEFT JOINed onto it. A bucket the appliance observed
	// but in which nobody submitted a prompt is a numeric zero, and a numeric
	// zero is a different state from "not observed" -- emitting only the
	// buckets that happened to have rows collapsed the two.
	rows, err := conn.Query(ctx, `
		WITH buckets AS (
			SELECT generate_series(
				date_trunc($3, $1::timestamptz, $4),
				date_trunc($3, $2::timestamptz - interval '1 microsecond', $4),
				('1 ' || $3)::interval
			) AS day
		),
		measured AS (
		SELECT date_trunc($3, pf.observed_at, $4) AS day,
			count(*) AS prompt_count,
			count(*) FILTER (
				WHERE pf.prompt_size_bytes IS NOT NULL OR pf.prompt_character_count IS NOT NULL
			) AS measured_count,
			percentile_cont(0.50) WITHIN GROUP (ORDER BY pf.prompt_size_bytes) FILTER (WHERE pf.prompt_size_bytes IS NOT NULL) AS p50,
			percentile_cont(0.90) WITHIN GROUP (ORDER BY pf.prompt_size_bytes) FILTER (WHERE pf.prompt_size_bytes IS NOT NULL) AS p90,
			percentile_cont(0.95) WITHIN GROUP (ORDER BY pf.prompt_size_bytes) FILTER (WHERE pf.prompt_size_bytes IS NOT NULL) AS p95,
			percentile_cont(0.99) WITHIN GROUP (ORDER BY pf.prompt_size_bytes) FILTER (WHERE pf.prompt_size_bytes IS NOT NULL) AS p99,
			percentile_cont(0.50) WITHIN GROUP (ORDER BY pf.prompt_character_count) FILTER (WHERE pf.prompt_character_count IS NOT NULL) AS char_p50,
			percentile_cont(0.90) WITHIN GROUP (ORDER BY pf.prompt_character_count) FILTER (WHERE pf.prompt_character_count IS NOT NULL) AS char_p90,
			percentile_cont(0.95) WITHIN GROUP (ORDER BY pf.prompt_character_count) FILTER (WHERE pf.prompt_character_count IS NOT NULL) AS char_p95,
			percentile_cont(0.99) WITHIN GROUP (ORDER BY pf.prompt_character_count) FILTER (WHERE pf.prompt_character_count IS NOT NULL) AS char_p99
		FROM prompt_features pf
		WHERE pf.observed_at >= $1 AND pf.observed_at < $2
		GROUP BY day
		)
		SELECT b.day,
			coalesce(m.prompt_count, 0), coalesce(m.measured_count, 0),
			m.p50, m.p90, m.p95, m.p99,
			m.char_p50, m.char_p90, m.char_p95, m.char_p99
		FROM buckets b
		LEFT JOIN measured m ON m.day = b.day
		ORDER BY b.day
	`, from, to, bucket.SQLUnit(), bucket.Timezone)
	if err != nil {
		return PromptShapeResponse{}, budgetOrErr(budget, started, err)
	}
	var response PromptShapeResponse
	var totalPrompts, measuredPrompts int64
	for rows.Next() {
		var row PromptShapeDayRow
		var rowMeasured int64
		var p, characters Percentiles
		if err := rows.Scan(&row.Day, &row.PromptCount, &rowMeasured,
			&p.P50, &p.P90, &p.P95, &p.P99,
			&characters.P50, &characters.P90, &characters.P95, &characters.P99); err != nil {
			rows.Close()
			return PromptShapeResponse{}, err
		}
		measuredPrompts += rowMeasured
		if p.P50 != nil || p.P90 != nil || p.P95 != nil || p.P99 != nil {
			row.Percentiles = &p
		}
		if characters.P50 != nil || characters.P90 != nil || characters.P95 != nil || characters.P99 != nil {
			row.CharacterPercentiles = &characters
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

	// Zero-filling buckets and the query contract's unknown_denominator_policy
	// meet here, and they do not conflict: a bucket with no prompt inside a
	// range that did observe prompts is a numeric zero worth drawing, but a
	// range with no prompts at all has a zero denominator, and the contract
	// requires that case to return an empty series with completeness
	// "unknown" rather than a row of zeroes that looks like measured silence.
	if totalPrompts == 0 {
		response.Data = nil
	}

	response.FormulaVersion = FormulaVersionPromptShape1
	// The percentile band is computed from the prompts that carried a length.
	// Reporting the total as both numerator and denominator claimed the band
	// covered every prompt, which is exactly the silence AGENTS.md forbids:
	// a percentage must expose numerator, denominator and exclusions.
	response.Population = Population{Numerator: measuredPrompts, Denominator: totalPrompts}
	response.Exclusions = map[string]int64{
		"prompt_without_length_measurement": totalPrompts - measuredPrompts,
	}
	response.Completeness = completenessFor(measuredPrompts, totalPrompts)

	watermark, pending, err := aggregateSourceWatermarkFreshness(ctx, pool)
	if err != nil {
		return PromptShapeResponse{}, err
	}
	response.Freshness = Freshness{RollupWatermark: watermark, LateEventsPending: pending}
	return response, nil
}
