package integrity

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const UnknownSchemaAndLagCheckID = "stage_7_unknown_schema_and_lag"

// SourceIntegritySnapshot is a metadata-only stage-7 view. It contains
// counts, timestamps and safe content-addressed identities, never event
// values or raw quarantine payloads.
type SourceIntegritySnapshot struct {
	AdapterID            string
	InstallationID       string
	CapabilityID         string
	SourceID             string
	SchemaFingerprint    string
	SchemaKnown          bool
	Quarantined          bool
	DuplicateReplayCount uint64
	InflatedFactCount    uint64
	LateEventsPending    uint64
	LatestReceivedAt     time.Time
	IngestLagBudget      time.Duration
}

type SourceIntegrityLister func(ctx context.Context) ([]SourceIntegritySnapshot, error)

// UnknownSchemaAndLagCheck makes unknown schemas, duplicate inflation and
// ingest lag visible. An unknown schema passes only when it is quarantined;
// it still returns fail/unknown_schema so health and incidents cannot mistake
// quarantine for compatibility.
type UnknownSchemaAndLagCheck struct {
	List SourceIntegrityLister
	Now  func() time.Time
	rows map[string]SourceIntegritySnapshot
}

var _ Check = (*UnknownSchemaAndLagCheck)(nil)

func NewUnknownSchemaAndLagCheck(list SourceIntegrityLister) *UnknownSchemaAndLagCheck {
	return &UnknownSchemaAndLagCheck{List: list, Now: time.Now}
}

func (c *UnknownSchemaAndLagCheck) StageID() StageID { return Stage7UnknownSchemaAndLag }
func (c *UnknownSchemaAndLagCheck) CheckID() string  { return UnknownSchemaAndLagCheckID }

func (c *UnknownSchemaAndLagCheck) Targets(ctx context.Context, _ CheckInput) ([]CheckTarget, error) {
	if c.List == nil {
		return nil, nil
	}
	rows, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].InstallationID == rows[j].InstallationID {
			return rows[i].CapabilityID < rows[j].CapabilityID
		}
		return rows[i].InstallationID < rows[j].InstallationID
	})
	c.rows = make(map[string]SourceIntegritySnapshot, len(rows))
	out := make([]CheckTarget, 0, len(rows))
	for _, row := range rows {
		key := sourceIntegrityKey(row.InstallationID, row.CapabilityID, row.SourceID)
		if _, exists := c.rows[key]; exists {
			return nil, fmt.Errorf("duplicate source integrity target %s", key)
		}
		c.rows[key] = row
		out = append(out, CheckTarget{InstallationID: row.InstallationID, CapabilityID: row.CapabilityID, SourceID: row.SourceID, AdapterID: row.AdapterID})
	}
	return out, nil
}

func (c *UnknownSchemaAndLagCheck) Evaluate(_ context.Context, in CheckInput, target CheckTarget) (CheckOutcome, error) {
	now := c.Now()
	if !in.Now.IsZero() {
		now = in.Now
	}
	row, ok := c.rows[sourceIntegrityKey(target.InstallationID, target.CapabilityID, target.SourceID)]
	if !ok {
		return sourceIntegrityFailure(now, FailureClassEligibilityUnknown, "source_integrity_target_not_enumerated"), nil
	}
	if !row.SchemaKnown {
		detail := "unknown_schema_metadata_only_quarantine"
		if !row.Quarantined {
			detail = "unknown_schema_not_quarantined"
		}
		return sourceIntegrityFailure(now, FailureClassUnknownSchema, detail), nil
	}
	if row.DuplicateReplayCount > 0 && row.InflatedFactCount > 0 {
		return sourceIntegrityFailure(now, FailureClassDuplicateEvidenceAnomaly, "duplicate_evidence_inflated_fact_count"), nil
	}
	budget := row.IngestLagBudget
	if budget <= 0 {
		budget = DefaultFreshnessWindow
	}
	if row.LateEventsPending > 0 {
		return sourceIntegrityFailure(now, FailureClassIngestLag, "late_events_pending"), nil
	}
	if !row.LatestReceivedAt.IsZero() && now.Sub(row.LatestReceivedAt) > budget {
		return sourceIntegrityFailure(now, FailureClassIngestLag, "latest_received_at_exceeds_ingest_lag_budget"), nil
	}
	return CheckOutcome{
		CheckID: UnknownSchemaAndLagCheckID, Status: CheckStatusPass,
		DetailRef:  fmt.Sprintf("schema_known duplicates_replayed=%d facts_inflated=0 late_events_pending=0", row.DuplicateReplayCount),
		ObservedAt: now,
	}, nil
}

func sourceIntegrityFailure(now time.Time, class FailureClass, detail string) CheckOutcome {
	return CheckOutcome{
		CheckID: UnknownSchemaAndLagCheckID, Status: CheckStatusFail,
		Category: string(class), DetailRef: detail, ObservedAt: now,
	}
}

func sourceIntegrityKey(installationID, capabilityID, sourceID string) string {
	return installationID + "\x00" + capabilityID + "\x00" + sourceID
}
