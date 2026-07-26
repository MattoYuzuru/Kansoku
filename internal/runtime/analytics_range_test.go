package runtime

import (
	"net/url"
	"testing"
	"time"
)

func TestParseAnalyticsRangeCarriesTimezoneAndAdaptiveGranularity(t *testing.T) {
	query := url.Values{
		"from":        {"2026-07-25T21:00:00Z"},
		"to":          {"2026-07-26T21:00:00Z"},
		"granularity": {"hourly"},
		"timezone":    {"Europe/Moscow"},
	}
	from, to, bucket, ok := parseAnalyticsRange(query)
	if !ok {
		t.Fatal("valid adaptive range was rejected")
	}
	if bucket.SQLUnit() != "hour" || bucket.Timezone != "Europe/Moscow" {
		t.Fatalf("bucket = %+v", bucket)
	}
	if to.Sub(from) != 24*time.Hour {
		t.Fatalf("span = %v, want 24h", to.Sub(from))
	}
}

func TestParseAnalyticsRangeRejectsInvalidZoneAndOverwideHourlyRange(t *testing.T) {
	base := url.Values{
		"from":        {"2026-01-01T00:00:00Z"},
		"to":          {"2026-03-01T00:00:00Z"},
		"granularity": {"hourly"},
		"timezone":    {"UTC"},
	}
	if _, _, _, ok := parseAnalyticsRange(base); ok {
		t.Fatal("overwide hourly range was accepted")
	}
	base.Set("to", "2026-01-02T00:00:00Z")
	base.Set("timezone", "not/a-zone")
	if _, _, _, ok := parseAnalyticsRange(base); ok {
		t.Fatal("invalid timezone was accepted")
	}
}
