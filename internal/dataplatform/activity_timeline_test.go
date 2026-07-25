//go:build postgres_integration

// See postgres_integration_test.go for why these tests carry the
// postgres_integration build tag and how testDSN/freshSchema work.
package dataplatform

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// insertActivityEvent/insertActivityPrompt are minimal fixture helpers for
// ActivityTimeline's two source tables (events, prompt_features), which
// postgres_integration_test.go's existing testDimensionRefs/EnsureDimensions
// path partially covers but does not itself insert rows for.
func insertActivityEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, sessionID string, observedAt time.Time, sourceInstanceID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO events (event_id, fact_key, event_type, observed_at, source_instance_id, source_native_event_id, sequence, session_id, value_state, outcome, correlation_status)
		VALUES ($1, $1, 'component.executed', $2, $3, $1, 0, $4, 'observed', 'succeeded', 'exact')
	`, id, observedAt, sourceInstanceID, sessionID); err != nil {
		t.Fatalf("insert event: %v", err)
	}
}

func insertActivityPrompt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, turnID string, observedAt time.Time, sizeBytes int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO prompt_features (prompt_feature_id, turn_id, observed_at, prompt_size_bytes, value_state)
		VALUES ($1, $2, $3, $4, 'observed')
	`, id, turnID, observedAt, sizeBytes); err != nil {
		t.Fatalf("insert prompt_feature: %v", err)
	}
}

// TestActivityTimelineAggregatesSessionsAndPromptsWithinRangeAndBudget proves
// ActivityTimeline groups distinct sessions and prompt counts by calendar
// day across multiple rows, respects the half-open [from, to) boundary, and
// returns within its registered budget.
func TestActivityTimelineAggregatesSessionsAndPromptsWithinRangeAndBudget(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()

	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartition(ctx, pool, "events", base); err != nil {
		t.Fatalf("ensure partition: %v", err)
	}
	// The "outside range" fixture row below is deliberately placed an hour
	// before base, which crosses into the prior month's partition.
	if err := EnsurePartition(ctx, pool, "events", base.AddDate(0, -1, 0)); err != nil {
		t.Fatalf("ensure partition (prior month): %v", err)
	}

	sourceInstanceID := "src_activity_timeline"
	refs := testDimensionRefs(sourceInstanceID)
	refs.ComponentID = ""
	if err := EnsureDimensions(ctx, pool, refs); err != nil {
		t.Fatalf("ensure dimensions: %v", err)
	}

	// Two events in the same session on day 1 (span = 2 minutes), one event
	// for a second session also on day 1.
	insertActivityEvent(t, ctx, pool, "evt_act_1", "ses_fixture", base.Add(time.Minute), sourceInstanceID)
	insertActivityEvent(t, ctx, pool, "evt_act_2", "ses_fixture", base.Add(3*time.Minute), sourceInstanceID)
	if _, err := pool.Exec(ctx, `INSERT INTO sessions (session_id, project_id, started_at) VALUES ('ses_second', 'proj_fixture', now()) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("insert second session: %v", err)
	}
	insertActivityEvent(t, ctx, pool, "evt_act_3", "ses_second", base.Add(5*time.Minute), sourceInstanceID)
	insertActivityPrompt(t, ctx, pool, "pf_act_1", "turn_fixture", base.Add(2*time.Minute), 100)
	insertActivityPrompt(t, ctx, pool, "pf_act_2", "turn_fixture", base.Add(4*time.Minute), 200)

	// Outside range: must never leak in (half-open boundary proof).
	insertActivityEvent(t, ctx, pool, "evt_act_out", "ses_fixture", base.Add(-time.Hour), sourceInstanceID)
	insertActivityPrompt(t, ctx, pool, "pf_act_out", "turn_fixture", base.AddDate(0, 0, 2), 999)

	to := base.AddDate(0, 0, 1)
	started := time.Now()
	response, err := ActivityTimeline(ctx, pool, base, to)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("ActivityTimeline: %v", err)
	}
	if elapsed > time.Duration(Budgets["activity_timeline_range"].MaxMS)*time.Millisecond {
		t.Fatalf("activity_timeline_range exceeded its budget: %v", elapsed)
	}
	if response.FormulaVersion != FormulaVersionActivityTimeline1 {
		t.Fatalf("formula_version = %q, want %q", response.FormulaVersion, FormulaVersionActivityTimeline1)
	}
	if len(response.Data) != 1 {
		t.Fatalf("expected exactly 1 day row (out-of-range rows must not leak in), got %d: %+v", len(response.Data), response.Data)
	}
	day := response.Data[0]
	if !day.Day.Equal(base) {
		t.Fatalf("day = %v, want %v", day.Day, base)
	}
	if day.SessionCount != 2 {
		t.Fatalf("session_count = %d, want 2", day.SessionCount)
	}
	if day.PromptCount != 2 {
		t.Fatalf("prompt_count = %d, want 2", day.PromptCount)
	}
	if day.ActiveDurationSeconds == nil {
		t.Fatalf("expected a non-nil active_duration_seconds when sessions are present")
	}
	if response.Population.Denominator == 0 {
		t.Fatalf("expected a nonzero denominator when activity is present: %+v", response.Population)
	}
	if response.Completeness.Status == "unknown" {
		t.Fatalf("completeness should not be unknown when data is present: %+v", response.Completeness)
	}
}

// TestActivityTimelineEmptyRangeReportsUnknownNotZero proves the "no silent
// zero" convention: an empty range must report completeness "unknown" via a
// zero denominator, not a fabricated "complete" with empty data.
func TestActivityTimelineEmptyRangeReportsUnknownNotZero(t *testing.T) {
	dsn := testDSN(t)
	pool := freshSchema(t, dsn)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartition(ctx, pool, "events", base); err != nil {
		t.Fatalf("ensure partition: %v", err)
	}
	response, err := ActivityTimeline(ctx, pool, base, base.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("ActivityTimeline: %v", err)
	}
	if len(response.Data) != 0 {
		t.Fatalf("expected zero rows, got %d: %+v", len(response.Data), response.Data)
	}
	if response.Completeness.Status != "unknown" {
		t.Fatalf("completeness = %+v, want unknown for empty range", response.Completeness)
	}
}
