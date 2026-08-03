package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionMetricReconciliation1 is the registered formula version for
// the metric-versus-fact reconciliation query.
const FormulaVersionMetricReconciliation1 = "metric.reconciliation/1"

// MetricReconciliationRow compares one measurement the agent reported twice:
// once as an OTLP metric, once as an attribute on an api_request log record.
//
// The two lanes are never merged. api_request remains the single counted
// operation surface and the metric stream remains an independent observation,
// which is the whole point: if they disagree, the disagreement is a number in
// Difference rather than a silence nobody can see. A disagreement is not
// automatically a bug -- the metric stream is cumulative per session and can
// legitimately be ahead of the log stream at a range boundary -- so this query
// reports the gap and its inputs, and never "corrects" either side.
type MetricReconciliationRow struct {
	Measurement string `json:"measurement"`
	// MetricName is the OTLP metric the left-hand value came from, and
	// MetricDimension the `type` attribute that split it, when there was one.
	MetricName      string `json:"metric_name"`
	MetricDimension string `json:"metric_dimension"`
	MetricSamples   int64  `json:"metric_samples"`
	// MetricValue and FactValue are the same unit. Cost is micros on both
	// sides; tokens are counts on both sides.
	MetricValue int64 `json:"metric_value"`
	FactValue   int64 `json:"fact_value"`
	FactRows    int64 `json:"fact_rows"`
	// Difference is metric minus fact. A positive number means the agent's
	// own counter is ahead of what Kansoku reconstructed from log records.
	Difference int64 `json:"difference"`
	// State is "observed" when both lanes reported, and "not_observed" when
	// one of them never did -- never a fabricated zero difference.
	State string `json:"state"`
}

// MetricReconciliationResponse is the completeness-aware envelope.
type MetricReconciliationResponse struct {
	Data           []MetricReconciliationRow `json:"data"`
	FormulaVersion string                    `json:"formula_version"`
	Population     Population                `json:"population"`
	Exclusions     map[string]int64          `json:"exclusions"`
	Completeness   Completeness              `json:"completeness"`
	Freshness      Freshness                 `json:"freshness"`
}

// MetricReconciliation executes the "metric_reconciliation_range" budgeted
// query over the half-open [from, to) range.
func MetricReconciliation(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (MetricReconciliationResponse, error) {
	budget := Budgets["metric_reconciliation_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return MetricReconciliationResponse{}, err
	}
	defer release()

	started := time.Now()
	// Each comparison is one row: the metric side aggregated from
	// metric_samples, the fact side aggregated from the lane api_request
	// already feeds. Cost is converted from USD to micros on the metric side
	// so both columns carry the same unit; the conversion is exact enough for
	// a comparison and is never written back anywhere.
	rows, err := conn.Query(ctx, `
		WITH metric AS (
			SELECT metric_name, coalesce(dimension, '') AS dimension,
			       count(*) AS samples,
			       sum(coalesce(value_int, 0))::bigint AS int_total,
			       sum(coalesce(value_double, 0)) AS double_total
			FROM metric_samples
			WHERE observed_at >= $1 AND observed_at < $2
			GROUP BY metric_name, coalesce(dimension, '')
		),
		facts AS (
			SELECT
				coalesce(sum(tu.input_tokens), 0)::bigint AS input_tokens,
				coalesce(sum(tu.output_tokens), 0)::bigint AS output_tokens,
				coalesce(sum(tu.cache_creation_tokens), 0)::bigint AS cache_creation_tokens,
				coalesce(sum(tu.cache_read_tokens), 0)::bigint AS cache_read_tokens,
				count(*)::bigint AS token_rows
			FROM token_usage tu
			WHERE tu.observed_at >= $1 AND tu.observed_at < $2
		),
		cost AS (
			SELECT coalesce(sum(mo.provider_cost_micros), 0)::bigint AS cost_micros,
			       count(*) FILTER (WHERE mo.provider_cost_micros IS NOT NULL)::bigint AS cost_rows
			FROM model_operations mo
			WHERE mo.observed_at >= $1 AND mo.observed_at < $2
		),
		comparisons(measurement, metric_name, metric_dimension, fact_value, fact_rows) AS (
			SELECT 'input_tokens', 'claude_code.token.usage', 'input', f.input_tokens, f.token_rows FROM facts f
			UNION ALL
			SELECT 'output_tokens', 'claude_code.token.usage', 'output', f.output_tokens, f.token_rows FROM facts f
			UNION ALL
			SELECT 'cache_creation_tokens', 'claude_code.token.usage', 'cacheCreation', f.cache_creation_tokens, f.token_rows FROM facts f
			UNION ALL
			SELECT 'cache_read_tokens', 'claude_code.token.usage', 'cacheRead', f.cache_read_tokens, f.token_rows FROM facts f
			UNION ALL
			SELECT 'provider_cost_micros', 'claude_code.cost.usage', '', c.cost_micros, c.cost_rows FROM cost c
		)
		SELECT c.measurement, c.metric_name, c.metric_dimension,
		       coalesce(m.samples, 0)::bigint AS metric_samples,
		       CASE
		           WHEN m.metric_name IS NULL THEN 0::bigint
		           WHEN c.measurement = 'provider_cost_micros'
		               THEN round(m.double_total * 1000000)::bigint + m.int_total * 1000000
		           ELSE m.int_total + round(m.double_total)::bigint
		       END AS metric_value,
		       c.fact_value, c.fact_rows
		FROM comparisons c
		LEFT JOIN metric m
		  ON m.metric_name = c.metric_name
		 AND m.dimension = c.metric_dimension
		ORDER BY c.measurement
	`, from, to)
	if err != nil {
		return MetricReconciliationResponse{}, budgetOrErr(budget, started, err)
	}
	var response MetricReconciliationResponse
	var observed, total int64
	for rows.Next() {
		var row MetricReconciliationRow
		if err := rows.Scan(&row.Measurement, &row.MetricName, &row.MetricDimension,
			&row.MetricSamples, &row.MetricValue, &row.FactValue, &row.FactRows); err != nil {
			rows.Close()
			return MetricReconciliationResponse{}, err
		}
		total++
		switch {
		case row.MetricSamples == 0 && row.FactRows == 0:
			// Neither lane reported. That is not agreement.
			row.State = "not_observed"
		case row.MetricSamples == 0 || row.FactRows == 0:
			row.State = "partial"
		default:
			row.State = "observed"
			observed++
		}
		if row.State == "observed" {
			row.Difference = row.MetricValue - row.FactValue
		}
		response.Data = append(response.Data, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return MetricReconciliationResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return MetricReconciliationResponse{}, &ErrBudgetExceeded{
			BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed,
		}
	}

	response.FormulaVersion = FormulaVersionMetricReconciliation1
	response.Population = Population{Numerator: observed, Denominator: total}
	response.Exclusions = map[string]int64{"measurement_reported_by_one_lane_only": total - observed}
	response.Completeness = completenessFor(observed, total)

	watermark, pending, err := aggregateSourceWatermarkFreshness(ctx, pool)
	if err != nil {
		return MetricReconciliationResponse{}, err
	}
	response.Freshness = Freshness{RollupWatermark: watermark, LateEventsPending: pending}
	return response, nil
}
