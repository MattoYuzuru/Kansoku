package dataplatform

import (
	"fmt"
	"time"
)

// TimeBucketSpec is the closed, validated calendar-bucket contract shared by
// every bespoke timeline query. PostgreSQL keeps timestamps in UTC; timezone
// only determines calendar boundaries for day/week/month buckets.
type TimeBucketSpec struct {
	Granularity Granularity
	Timezone    string
}

// NewTimeBucketSpec validates untrusted API parameters before they can reach
// date_trunc. The returned SQLUnit is always from a closed enum.
func NewTimeBucketSpec(granularity, timezone string) (TimeBucketSpec, error) {
	spec := TimeBucketSpec{Granularity: Granularity(granularity), Timezone: timezone}
	switch spec.Granularity {
	case GranularityHourly, GranularityDaily, GranularityWeekly, GranularityMonthly:
	default:
		return TimeBucketSpec{}, fmt.Errorf("unsupported granularity %q", granularity)
	}
	if timezone == "" {
		return TimeBucketSpec{}, fmt.Errorf("timezone is required")
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return TimeBucketSpec{}, fmt.Errorf("invalid timezone %q: %w", timezone, err)
	}
	return spec, nil
}

// DefaultTimeBucketSpec preserves the historical query behavior for internal
// callers that do not have a user timezone.
func DefaultTimeBucketSpec() TimeBucketSpec {
	return TimeBucketSpec{Granularity: GranularityDaily, Timezone: "UTC"}
}

// SQLUnit returns PostgreSQL's date_trunc unit for a validated granularity.
func (s TimeBucketSpec) SQLUnit() string {
	switch s.Granularity {
	case GranularityHourly:
		return "hour"
	case GranularityWeekly:
		return "week"
	case GranularityMonthly:
		return "month"
	default:
		return "day"
	}
}

// ValidateRange applies an explicit cost bound per bucket size. Hourly
// requests stay small; coarse all-time views can cover the appliance's
// bounded retention horizon without pretending they are daily queries.
func (s TimeBucketSpec) ValidateRange(from, to time.Time) bool {
	if !to.After(from) {
		return false
	}
	span := to.Sub(from)
	switch s.Granularity {
	case GranularityHourly:
		return span <= 31*24*time.Hour
	case GranularityDaily:
		return span <= 366*24*time.Hour
	case GranularityWeekly:
		return span <= 2*366*24*time.Hour
	case GranularityMonthly:
		return span <= 6*366*24*time.Hour
	default:
		return false
	}
}
