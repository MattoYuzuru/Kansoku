package integrity

import (
	"context"
	"fmt"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
)

const AdapterFixtureAuditCheckID = "stage_4_adapter_fixture_audit"

// AdapterFixtureAuditCheck dispatches through the shared adaptersdk.Adapter
// interface. No concrete adapter type or agent ID appears in integrity core.
type AdapterFixtureAuditCheck struct {
	Registry      *adaptersdk.Registry
	Installations InstallationLister
	Now           func() time.Time
	targets       map[string]adaptersdk.Installation
}

var _ Check = (*AdapterFixtureAuditCheck)(nil)

func NewAdapterFixtureAuditCheck(registry *adaptersdk.Registry, installations InstallationLister) *AdapterFixtureAuditCheck {
	if installations == nil {
		installations = func(context.Context, string) ([]adaptersdk.Installation, error) { return nil, nil }
	}
	return &AdapterFixtureAuditCheck{Registry: registry, Installations: installations, Now: time.Now}
}

func (c *AdapterFixtureAuditCheck) StageID() StageID { return Stage4ParserFixtureReplay }
func (c *AdapterFixtureAuditCheck) CheckID() string  { return AdapterFixtureAuditCheckID }

func (c *AdapterFixtureAuditCheck) Targets(ctx context.Context, _ CheckInput) ([]CheckTarget, error) {
	if c.Registry == nil {
		return nil, nil
	}
	c.targets = map[string]adaptersdk.Installation{}
	var out []CheckTarget
	for _, adapterID := range c.Registry.IDs() {
		installations, err := c.Installations(ctx, adapterID)
		if err != nil {
			return nil, fmt.Errorf("list adapter audit installations: %w", err)
		}
		for _, installation := range installations {
			target := CheckTarget{
				InstallationID: installation.InstallationID,
				CapabilityID:   string(adaptersdk.CapabilityIngestionHistoricalImport),
				SourceID:       adapterID,
				AdapterID:      adapterID,
			}
			c.targets[endpointTargetKey(target.InstallationID, target.CapabilityID, target.SourceID)] = installation
			out = append(out, target)
		}
	}
	return out, nil
}

func (c *AdapterFixtureAuditCheck) Evaluate(ctx context.Context, in CheckInput, target CheckTarget) (CheckOutcome, error) {
	now := c.Now()
	if !in.Now.IsZero() {
		now = in.Now
	}
	installation, ok := c.targets[endpointTargetKey(target.InstallationID, target.CapabilityID, target.SourceID)]
	if !ok {
		return CheckOutcome{CheckID: AdapterFixtureAuditCheckID, Status: CheckStatusFail, Category: string(FailureClassParserIncompatibility), DetailRef: "adapter_audit_target_not_enumerated", ObservedAt: now}, nil
	}
	adapter, err := c.Registry.Get(installation.AdapterID)
	if err != nil {
		return CheckOutcome{CheckID: AdapterFixtureAuditCheckID, Status: CheckStatusFail, Category: string(FailureClassParserIncompatibility), DetailRef: "adapter_audit_registry_lookup_failed", ObservedAt: now}, nil
	}
	results := adapter.Audit(ctx, installation, adaptersdk.AuditFixtureReplay)
	if len(results) == 0 {
		return CheckOutcome{CheckID: AdapterFixtureAuditCheckID, Status: CheckStatusSkippedUnsupported, DetailRef: "adapter_fixture_audit_no_results", ObservedAt: now}, nil
	}
	passed, skipped := 0, 0
	for _, result := range results {
		switch result.Status {
		case adaptersdk.CheckPass:
			passed++
		case adaptersdk.CheckSkippedUnsupported:
			skipped++
		default:
			return CheckOutcome{
				CheckID: AdapterFixtureAuditCheckID, Status: CheckStatusFail,
				Category:  string(FailureClassParserIncompatibility),
				DetailRef: "adapter_fixture_audit_failed", ObservedAt: now,
			}, nil
		}
	}
	if passed == 0 && skipped == len(results) {
		return CheckOutcome{CheckID: AdapterFixtureAuditCheckID, Status: CheckStatusSkippedUnsupported, DetailRef: "adapter_fixture_audit_unsupported", ObservedAt: now}, nil
	}
	return CheckOutcome{
		CheckID: AdapterFixtureAuditCheckID, Status: CheckStatusPass,
		DetailRef:  fmt.Sprintf("adapter_fixture_audit_passed=%d skipped=%d", passed, skipped),
		ObservedAt: now,
	}, nil
}
