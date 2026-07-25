package dataplatform

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPercentilesJSONMatchesDashboardContract(t *testing.T) {
	value := 42.0
	encoded, err := json.Marshal(Percentiles{P50: &value, P90: &value, P95: &value, P99: &value})
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, key := range []string{`"p50"`, `"p90"`, `"p95"`, `"p99"`} {
		if !strings.Contains(text, key) {
			t.Fatalf("percentile JSON %s missing %s", text, key)
		}
	}
	if strings.Contains(text, `"P50"`) {
		t.Fatalf("percentile JSON leaked Go field casing: %s", text)
	}
}

func TestBucketStartHourlyDaily(t *testing.T) {
	at := time.Date(2026, 3, 15, 13, 47, 9, 0, time.UTC)
	hourly := BucketStart(at, GranularityHourly)
	if !hourly.Equal(time.Date(2026, 3, 15, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("hourly bucket start: %v", hourly)
	}
	daily := BucketStart(at, GranularityDaily)
	if !daily.Equal(time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily bucket start: %v", daily)
	}
	if end := bucketEnd(hourly, GranularityHourly); !end.Equal(hourly.Add(time.Hour)) {
		t.Fatalf("hourly bucket end: %v", end)
	}
	if end := bucketEnd(daily, GranularityDaily); !end.Equal(daily.AddDate(0, 0, 1)) {
		t.Fatalf("daily bucket end: %v", end)
	}
}

func TestBucketStartHandlesDSTSpringForwardBoundary(t *testing.T) {
	// 2026-03-08 is a US DST spring-forward date; buckets are computed in UTC
	// so a local wall-clock skip never drops or duplicates a UTC hour.
	before := time.Date(2026, 3, 8, 6, 59, 0, 0, time.UTC)
	after := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC)
	if BucketStart(before, GranularityHourly).Equal(BucketStart(after, GranularityHourly)) {
		t.Fatalf("distinct UTC hours must not collapse into the same bucket")
	}
}

func TestDimensionScopeIsBoundedFourTuple(t *testing.T) {
	fact := FactRow{AgentInstallationID: "ain_1", SurfaceID: "cli", ComponentID: "skill/x", EventType: "component.executed"}
	scope := dimensionScope(fact)
	if scope != "ain_1|cli|skill/x|component.executed" {
		t.Fatalf("unexpected scope: %s", scope)
	}
}

func TestCompletenessForPolicy(t *testing.T) {
	cases := []struct {
		numerator, denominator int64
		status                 string
	}{
		{0, 0, "unknown"},
		{10, 10, "complete"},
		{6, 10, "partial"},
		{2, 10, "degraded"},
	}
	for _, c := range cases {
		got := completenessFor(c.numerator, c.denominator)
		if got.Status != c.status {
			t.Fatalf("completenessFor(%d,%d) = %s, want %s", c.numerator, c.denominator, got.Status, c.status)
		}
	}
}

func TestLoadMigrationsOrderedAndPaired(t *testing.T) {
	migrations, err := LoadMigrations()
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	if len(migrations) < 2 {
		t.Fatalf("expected at least 2 migrations, got %d", len(migrations))
	}
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].Version >= migrations[i].Version {
			t.Fatalf("migrations not strictly ordered: %s >= %s", migrations[i-1].Version, migrations[i].Version)
		}
	}
	for _, m := range migrations {
		if m.Up == "" || m.Down == "" {
			t.Fatalf("migration %s missing up or down SQL", m.Version)
		}
		if m.UpSHA256 == "" || len(m.UpSHA256) != 64 {
			t.Fatalf("migration %s has invalid checksum", m.Version)
		}
	}
}

func TestPartitionNameDeterministic(t *testing.T) {
	month := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if got := partitionName("events", month); got != "events_p202607" {
		t.Fatalf("unexpected partition name: %s", got)
	}
	start, end := monthBounds(time.Date(2026, 7, 15, 5, 0, 0, 0, time.UTC))
	if !start.Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)) || !end.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected month bounds: %v %v", start, end)
	}
}

// TestParsePartitionUpperBound is a regression test: fmt.Sscanf's "%[...]"
// scanset verb previously failed on pg_get_expr's exact partition-bound
// rendering with "bad verb '%[' for string", breaking every
// ApplyRetention call. The parser must be a plain regexp match, verified
// here without needing a live Postgres connection.
func TestParsePartitionUpperBound(t *testing.T) {
	got, err := parsePartitionUpperBound("FOR VALUES FROM ('2026-04-01 00:00:00+00') TO ('2026-05-01 00:00:00+00')")
	if err != nil {
		t.Fatalf("parsePartitionUpperBound: %v", err)
	}
	want := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("parsePartitionUpperBound = %v, want %v", got, want)
	}

	if _, err := parsePartitionUpperBound("not a partition bound"); err == nil {
		t.Fatalf("expected an error for an unrecognized partition bound expression")
	}
}

func TestBudgetsMatchQueryContract(t *testing.T) {
	expected := map[string]int64{
		"hourly_rollup_range_30d":       50,
		"daily_rollup_range_1y":         50,
		"session_drilldown":             100,
		"percentile_recompute_bucket":   200,
		"agent_breakdown_range":         150,
		"model_breakdown_range":         150,
		"component_breakdown_range":     150,
		"component_lifecycle_funnel":    150,
		"reliability_coverage_timeline": 150,
		"mcp_topology":                  100,
		// Wave 1b (Session 10 continuation) budgets -- see query.go's
		// Budgets map doc comment: not yet mirrored into
		// contracts/data-platform/query-contract.yaml, tracked as a
		// follow-up contract-governance task.
		"activity_timeline_range":      150,
		"prompt_shape_range":           150,
		"model_usage_range":            150,
		"tool_analytics_range":         150,
		"mcp_uptime_range":             100,
		"reliability_counts_range":     100,
		"system_snapshot":              50,
		"privacy_canary_history_range": 100,
	}
	if len(Budgets) != len(expected) {
		t.Fatalf("expected %d budgets, got %d", len(expected), len(Budgets))
	}
	for id, maxMS := range expected {
		budget, ok := Budgets[id]
		if !ok || budget.MaxMS != maxMS || budget.ID != id {
			t.Fatalf("budget %s mismatch: %+v", id, budget)
		}
	}
}

func TestErrBudgetExceededMessage(t *testing.T) {
	err := &ErrBudgetExceeded{BudgetID: "session_drilldown", MaxMS: 100, ActualMS: 150}
	if err.Error() == "" {
		t.Fatalf("expected non-empty error message")
	}
}
