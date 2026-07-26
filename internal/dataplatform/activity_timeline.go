package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FormulaVersionActivityTimeline1 is the registered formula version for the
// activity timeline query.
const FormulaVersionActivityTimeline1 = "activity_timeline/3"

// ActivityTimeline executes the "activity_timeline_range" budgeted query:
// one row per requested calendar bucket inside the half-open [from, to) range with
// distinct session/prompt counts and a reconstructed active-duration
// estimate. Serves the "/" overview-activity panel and the /activity
// activity-timeline panel (activity.sessions, activity.prompts,
// activity.active_duration_seconds).
//
// sessions/turns carry only a started_at timestamp -- there is no
// ended_at/finished_at column anywhere in the schema (see
// migrations/0001_core_schema.up.sql) -- so activity.active_duration_seconds
// cannot be read directly. contracts/metrics.yaml declares this metric's
// exactness as "exact or reconstructed as declared by evidence tier", which
// licenses a reconstruction: for each session, the span between its first
// and last observed events.observed_at is a real, non-fabricated active
// interval (never zero-duration for a single-event session, since that
// genuinely has no observed span). Sessions with zero events in range have
// a nil ActiveDurationSeconds (unknown span), never a fabricated zero.
func ActivityTimeline(ctx context.Context, pool *pgxpool.Pool, from, to time.Time, bucket TimeBucketSpec) (ActivityTimelineResponse, error) {
	budget := Budgets["activity_timeline_range"]
	conn, release, err := acquireBudgeted(ctx, pool, budget.MaxMS)
	if err != nil {
		return ActivityTimelineResponse{}, err
	}
	defer release()

	started := time.Now()

	sessionRows, err := conn.Query(ctx, `
		SELECT day, count(DISTINCT session_id) AS session_count,
			coalesce(sum(active_seconds), 0) AS active_seconds
		FROM (
			SELECT date_trunc($3, observed_at, $4) AS day, session_id,
				extract(epoch FROM (max(observed_at) - min(observed_at))) AS active_seconds
			FROM events
			WHERE observed_at >= $1 AND observed_at < $2 AND session_id IS NOT NULL
			GROUP BY date_trunc($3, observed_at, $4), session_id
		) per_session
		GROUP BY day
	`, from, to, bucket.SQLUnit(), bucket.Timezone)
	if err != nil {
		return ActivityTimelineResponse{}, budgetOrErr(budget, started, err)
	}
	sessionsByDay := make(map[time.Time]struct {
		count   int64
		seconds float64
	})
	for sessionRows.Next() {
		var day time.Time
		var count int64
		var seconds float64
		if err := sessionRows.Scan(&day, &count, &seconds); err != nil {
			sessionRows.Close()
			return ActivityTimelineResponse{}, err
		}
		sessionsByDay[day] = struct {
			count   int64
			seconds float64
		}{count: count, seconds: seconds}
	}
	sessionRows.Close()
	if err := sessionRows.Err(); err != nil {
		return ActivityTimelineResponse{}, err
	}

	promptRows, err := conn.Query(ctx, `
		SELECT date_trunc($3, pf.observed_at, $4) AS day, count(*) AS prompt_count
		FROM prompt_features pf
		WHERE pf.observed_at >= $1 AND pf.observed_at < $2
		GROUP BY day
	`, from, to, bucket.SQLUnit(), bucket.Timezone)
	if err != nil {
		return ActivityTimelineResponse{}, budgetOrErr(budget, started, err)
	}
	promptsByDay := make(map[time.Time]int64)
	for promptRows.Next() {
		var day time.Time
		var count int64
		if err := promptRows.Scan(&day, &count); err != nil {
			promptRows.Close()
			return ActivityTimelineResponse{}, err
		}
		promptsByDay[day] = count
	}
	promptRows.Close()
	if err := promptRows.Err(); err != nil {
		return ActivityTimelineResponse{}, err
	}
	if elapsed := time.Since(started).Milliseconds(); elapsed > budget.MaxMS {
		return ActivityTimelineResponse{}, &ErrBudgetExceeded{BudgetID: budget.ID, MaxMS: budget.MaxMS, ActualMS: elapsed}
	}

	days := make(map[time.Time]bool, len(sessionsByDay)+len(promptsByDay))
	for day := range sessionsByDay {
		days[day] = true
	}
	for day := range promptsByDay {
		days[day] = true
	}

	var response ActivityTimelineResponse
	var totalSessions, totalPrompts int64
	for day := range days {
		row := ActivityDayRow{Day: day}
		if s, ok := sessionsByDay[day]; ok {
			row.SessionCount = s.count
			seconds := s.seconds
			row.ActiveDurationSeconds = &seconds
			totalSessions += s.count
		}
		if p, ok := promptsByDay[day]; ok {
			row.PromptCount = p
			totalPrompts += p
		}
		response.Data = append(response.Data, row)
	}
	sortActivityDayRows(response.Data)

	response.FormulaVersion = FormulaVersionActivityTimeline1
	// Population reports session/prompt activity presence: numerator is
	// total observed sessions+prompts, denominator equals numerator (there
	// is no independent "expected" baseline for raw activity volume, only
	// a real "data present" vs "no data present" signal), matching
	// ModelBreakdown's precedent for the same honest limitation.
	total := totalSessions + totalPrompts
	response.Population = Population{Numerator: total, Denominator: total}
	response.Completeness = completenessFor(total, total)

	watermark, pending, err := aggregateSourceWatermarkFreshness(ctx, pool)
	if err != nil {
		return ActivityTimelineResponse{}, err
	}
	response.Freshness = Freshness{RollupWatermark: watermark, LateEventsPending: pending}
	return response, nil
}

func sortActivityDayRows(rows []ActivityDayRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j-1].Day.After(rows[j].Day); j-- {
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
}
