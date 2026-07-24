package integrity

import (
	"context"
	"fmt"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/observability"
)

// SourceFreshnessCheckID is the check_id every SourceFreshnessCheck outcome
// reports, matching stage_3_watermark_vs_inactivity and
// incident-and-health.yaml's "event_freshness" health dimension.
const SourceFreshnessCheckID = "stage_3_watermark_vs_inactivity"

// WatermarkLookup returns the current internal/observability.Watermark for
// one source_id, plus whether a watermark row exists at all. Session 08
// never reimplements watermark math: this function is expected to close over
// an already-built internal/observability.FileStore's Snapshot().Watermarks
// map (or an equivalent durable read), so SourceFreshnessCheck only ever
// reads the SAME Watermark type/values ingest.go already maintains.
type WatermarkLookup func(ctx context.Context, sourceID string) (observability.Watermark, bool, error)

// SourceEnumerator returns every (installation_id, source_id) pair a
// SourceFreshnessCheck should evaluate for one adapter_id, sourced from that
// adapter's own Manifest().Sources -- never a second source registry. A real
// caller closes this over InstallationLister x Manifest().Sources; stage3's
// own tests supply a fixed list directly.
type SourceEnumerator func(ctx context.Context, adapterID string) ([]SourceTarget, error)

// SourceTarget names one source belonging to one installation.
type SourceTarget struct {
	InstallationID string
	SourceID       string
}

// SourceFreshnessCheck implements stage_3_watermark_vs_inactivity: for every
// source of every adapter's every installation, it classifies the source's
// current Watermark using EXACTLY the silence-classification rules already
// codified in contracts/observability/reconciliation.yaml and already
// enforced by internal/observability's own Watermark/Incident types (this
// Check reads that classification's OUTPUT state -- Watermark.Inactivity/
// GapCount/LastEligibleActivity -- rather than re-deriving gap/stall math
// from raw timestamps itself):
//
//   - no watermark row at all (never discovered) = eligibility_unknown,
//     matching "missing eligibility evidence = unknown after threshold";
//   - Watermark.Inactivity=true (no agent/session/process evidence and no
//     events) = true_inactivity_flagged, never a gap incident, matching
//     "no agent/session/process evidence + no events = inactive, not
//     failed";
//   - Watermark.Inactivity=false and evidence of a stall (GapCount grew, or
//     LastEligibleActivity is stale beyond ExpectedCadenceMS) =
//     watermark_stall, matching "eligible activity + stalled source = gap
//     incident";
//   - otherwise: pass, the source is fresh.
//
// A source declaring ExpectedCadenceMS follows that value as its heartbeat
// SLO bound; a source that declares no cadence (zero) is never treated as
// stalled purely by elapsed time, matching "sources with declared heartbeats
// follow their heartbeat SLO" (no declared heartbeat, no time-based stall
// claim).
type SourceFreshnessCheck struct {
	Registry  *adaptersdk.Registry
	Sources   SourceEnumerator
	Watermark WatermarkLookup
	Now       func() time.Time
}

var _ Check = (*SourceFreshnessCheck)(nil)

// NewSourceFreshnessCheck constructs a SourceFreshnessCheck. sources/
// watermark may be nil, producing zero targets / "no watermark row" for
// every lookup respectively, so a caller without a wired store yet still
// gets a Check that runs safely rather than panicking.
func NewSourceFreshnessCheck(registry *adaptersdk.Registry, sources SourceEnumerator, watermark WatermarkLookup) *SourceFreshnessCheck {
	if sources == nil {
		sources = func(context.Context, string) ([]SourceTarget, error) { return nil, nil }
	}
	if watermark == nil {
		watermark = func(context.Context, string) (observability.Watermark, bool, error) {
			return observability.Watermark{}, false, nil
		}
	}
	return &SourceFreshnessCheck{Registry: registry, Sources: sources, Watermark: watermark, Now: time.Now}
}

func (c *SourceFreshnessCheck) StageID() StageID { return Stage3WatermarkVsInactivity }
func (c *SourceFreshnessCheck) CheckID() string  { return SourceFreshnessCheckID }

// Targets enumerates one CheckTarget per (source_id, installation_id) pair
// across every registered adapter. CapabilityID remains the closed
// ingestion.live_stream capability; SourceID carries the finer-grained
// source identity explicitly.
func (c *SourceFreshnessCheck) Targets(ctx context.Context, in CheckInput) ([]CheckTarget, error) {
	if c.Registry == nil {
		return nil, nil
	}
	var targets []CheckTarget
	for _, adapterID := range c.Registry.IDs() {
		sources, err := c.Sources(ctx, adapterID)
		if err != nil {
			return nil, fmt.Errorf("list sources for %s: %w", adapterID, err)
		}
		for _, source := range sources {
			targets = append(targets, CheckTarget{
				CapabilityID:   string(adaptersdk.CapabilityIngestionLiveStream),
				InstallationID: source.InstallationID,
				SourceID:       source.SourceID,
				AdapterID:      adapterID,
			})
		}
	}
	return targets, nil
}

// Evaluate classifies one source's current Watermark per the rules
// documented on SourceFreshnessCheck.
func (c *SourceFreshnessCheck) Evaluate(ctx context.Context, in CheckInput, target CheckTarget) (CheckOutcome, error) {
	now := c.Now()
	if !in.Now.IsZero() {
		now = in.Now
	}
	sourceID := target.SourceID
	watermark, exists, err := c.Watermark(ctx, sourceID)
	if err != nil {
		return CheckOutcome{}, fmt.Errorf("watermark lookup for %s: %w", sourceID, err)
	}
	if !exists {
		return CheckOutcome{
			CheckID: SourceFreshnessCheckID, Status: CheckStatusFail,
			Category: string(FailureClassEligibilityUnknown), DetailRef: "no_watermark_row_missing_eligibility_evidence",
			ObservedAt: now,
		}, nil
	}
	return classifyWatermark(watermark, now), nil
}

// classifyWatermark applies the silence-classification decision documented
// on SourceFreshnessCheck to one already-loaded Watermark. It is a free
// function (not a method) so stage3's unit tests can exercise the
// classification directly against hand-built Watermark fixtures without
// needing a Registry/SourceEnumerator/WatermarkLookup at all.
func classifyWatermark(watermark observability.Watermark, now time.Time) CheckOutcome {
	if watermark.Inactivity {
		// "no agent/session/process evidence + no events = inactive, not
		// failed": true_inactivity_flagged is a flag, never a gap incident,
		// matching contracts/observability/reconciliation.yaml's
		// silence.true_inactivity ("sets inactivity and opens no gap
		// incident").
		return CheckOutcome{
			CheckID: SourceFreshnessCheckID, Status: CheckStatusPass,
			Category: string(FailureClassTrueInactivityFlagged), DetailRef: "true_inactivity_no_gap_incident",
			ObservedAt: now,
		}
	}
	if stalled, detail := isEligibleActivityStalled(watermark, now); stalled {
		// "eligible activity + stalled source = gap incident", matching
		// silence.eligible_activity ("stalled cadence opens watermark_stall
		// incident and degrades interval").
		return CheckOutcome{
			CheckID: SourceFreshnessCheckID, Status: CheckStatusFail,
			Category: string(FailureClassWatermarkStall), DetailRef: detail,
			ObservedAt: now,
		}
	}
	return CheckOutcome{
		CheckID: SourceFreshnessCheckID, Status: CheckStatusPass,
		Category: "", DetailRef: "source_fresh",
		ObservedAt: now,
	}
}

// isEligibleActivityStalled reports whether an eligible (non-inactive)
// source shows a genuine stall: either its GapCount already reflects a
// detected sequence gap, or -- only for a source that DECLARES a heartbeat
// (ExpectedCadenceMS > 0) -- its LastEligibleActivity is older than that
// declared cadence as of now. A source with no declared cadence (0) is never
// judged stalled purely by elapsed time, matching "sources with declared
// heartbeats follow their heartbeat SLO" (silence about cadence is not
// itself a stall signal).
func isEligibleActivityStalled(watermark observability.Watermark, now time.Time) (bool, string) {
	if watermark.GapCount > 0 {
		return true, fmt.Sprintf("gap_count=%d", watermark.GapCount)
	}
	if watermark.ExpectedCadenceMS <= 0 {
		return false, ""
	}
	if watermark.LastEligibleActivity.IsZero() {
		// Eligible per Inactivity=false but never actually observed active:
		// treat as stalled only once a full cadence window has elapsed since
		// discovery, never immediately on the first audit tick after
		// discovery.
		if watermark.LastDiscovered.IsZero() {
			return false, ""
		}
		if now.Sub(watermark.LastDiscovered) > time.Duration(watermark.ExpectedCadenceMS)*time.Millisecond {
			return true, "no_eligible_activity_observed_since_discovery_beyond_cadence"
		}
		return false, ""
	}
	if now.Sub(watermark.LastEligibleActivity) > time.Duration(watermark.ExpectedCadenceMS)*time.Millisecond {
		return true, fmt.Sprintf("stalled_since=%s", watermark.LastEligibleActivity.UTC().Format(time.RFC3339))
	}
	return false, ""
}
