package dataplatform

import (
	"testing"
	"time"
)

func TestNewTimeBucketSpecValidatesClosedGranularityAndTimezone(t *testing.T) {
	for _, tc := range []struct {
		granularity string
		unit        string
	}{
		{"hourly", "hour"},
		{"daily", "day"},
		{"weekly", "week"},
		{"monthly", "month"},
	} {
		spec, err := NewTimeBucketSpec(tc.granularity, "Europe/Moscow")
		if err != nil {
			t.Fatalf("NewTimeBucketSpec(%q): %v", tc.granularity, err)
		}
		if got := spec.SQLUnit(); got != tc.unit {
			t.Fatalf("SQLUnit(%q) = %q, want %q", tc.granularity, got, tc.unit)
		}
	}
	if _, err := NewTimeBucketSpec("quarterly", "UTC"); err == nil {
		t.Fatal("unsupported granularity was accepted")
	}
	if _, err := NewTimeBucketSpec("daily", "Mars/Olympus"); err == nil {
		t.Fatal("invalid IANA timezone was accepted")
	}
}

func TestTimeBucketSpecRangeBudgetsMatchResolution(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		granularity string
		span        time.Duration
		want        bool
	}{
		{"hourly", 24 * time.Hour, true},
		{"hourly", 32 * 24 * time.Hour, false},
		{"daily", 365 * 24 * time.Hour, true},
		{"daily", 367 * 24 * time.Hour, false},
		{"weekly", 700 * 24 * time.Hour, true},
		{"monthly", 5 * 366 * 24 * time.Hour, true},
	}
	for _, tc := range cases {
		spec, err := NewTimeBucketSpec(tc.granularity, "America/New_York")
		if err != nil {
			t.Fatalf("NewTimeBucketSpec(%q): %v", tc.granularity, err)
		}
		if got := spec.ValidateRange(from, from.Add(tc.span)); got != tc.want {
			t.Fatalf("ValidateRange(%q, %v) = %v, want %v", tc.granularity, tc.span, got, tc.want)
		}
	}
}
