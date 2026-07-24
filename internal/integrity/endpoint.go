package integrity

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// EndpointKind is the closed set of configured ingestion surfaces the
// stage-2 passive verifier understands. It is deliberately capability-shaped:
// no adapter name is part of the verifier or its branching.
type EndpointKind string

const (
	EndpointKindHook EndpointKind = "hook"
	EndpointKindOTLP EndpointKind = "otlp"
)

const EndpointAndHookCheckID = "stage_2_endpoint_and_hook_verification"

// EndpointTarget is structural configuration metadata only. AddressRef is a
// safe opaque identity (for example, a loopback endpoint ID), never a URL
// containing credentials or a raw config value.
type EndpointTarget struct {
	AdapterID      string
	InstallationID string
	CapabilityID   string
	SourceID       string
	Kind           EndpointKind
	AddressRef     string
	Configured     bool
	Enabled        bool
	Trusted        bool
}

// PassiveEndpointEvidence is produced by a caller-owned passive probe. The
// probe may inspect listener/config state, but it must not emit agent traffic.
type PassiveEndpointEvidence struct {
	Reachable       bool
	ProtocolMatches bool
	PortMatches     bool
	AuthMatches     bool
	ObservedAt      time.Time
}

type EndpointTargetLister func(ctx context.Context) ([]EndpointTarget, error)
type PassiveEndpointProbe func(ctx context.Context, target EndpointTarget) (PassiveEndpointEvidence, error)

// EndpointAndHookCheck implements the second daily-audit stage without
// mutating configuration and without sending live agent events.
type EndpointAndHookCheck struct {
	ListTargets EndpointTargetLister
	Probe       PassiveEndpointProbe
	Now         func() time.Time
	targets     map[string]EndpointTarget
}

var _ Check = (*EndpointAndHookCheck)(nil)

func NewEndpointAndHookCheck(list EndpointTargetLister, probe PassiveEndpointProbe) *EndpointAndHookCheck {
	return &EndpointAndHookCheck{ListTargets: list, Probe: probe, Now: time.Now}
}

func (c *EndpointAndHookCheck) StageID() StageID { return Stage2EndpointAndHookVerification }
func (c *EndpointAndHookCheck) CheckID() string  { return EndpointAndHookCheckID }

func (c *EndpointAndHookCheck) Targets(ctx context.Context, _ CheckInput) ([]CheckTarget, error) {
	if c.ListTargets == nil {
		return nil, nil
	}
	records, err := c.ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].InstallationID == records[j].InstallationID {
			return records[i].CapabilityID < records[j].CapabilityID
		}
		return records[i].InstallationID < records[j].InstallationID
	})
	c.targets = make(map[string]EndpointTarget, len(records))
	out := make([]CheckTarget, 0, len(records))
	for _, record := range records {
		key := endpointTargetKey(record.InstallationID, record.CapabilityID, record.SourceID)
		if _, exists := c.targets[key]; exists {
			return nil, fmt.Errorf("duplicate endpoint target %s", key)
		}
		c.targets[key] = record
		out = append(out, CheckTarget{InstallationID: record.InstallationID, CapabilityID: record.CapabilityID, SourceID: record.SourceID, AdapterID: record.AdapterID})
	}
	return out, nil
}

func (c *EndpointAndHookCheck) Evaluate(ctx context.Context, in CheckInput, target CheckTarget) (CheckOutcome, error) {
	now := c.Now()
	if !in.Now.IsZero() {
		now = in.Now
	}
	record, ok := c.targets[endpointTargetKey(target.InstallationID, target.CapabilityID, target.SourceID)]
	if !ok {
		return endpointFailure(now, FailureClassEndpointUnreachable, "endpoint_target_not_enumerated"), nil
	}
	if record.Kind != EndpointKindHook && record.Kind != EndpointKindOTLP {
		return endpointFailure(now, FailureClassEndpointUnreachable, "endpoint_kind_unknown"), nil
	}
	if !record.Configured || !record.Enabled || !record.Trusted {
		if record.Kind == EndpointKindHook {
			return endpointFailure(now, FailureClassHookRemovedDisabledOrUntrusted, "hook_missing_disabled_or_untrusted"), nil
		}
		return endpointFailure(now, FailureClassOTLPMisconfigured, "otlp_missing_disabled_or_untrusted"), nil
	}
	if c.Probe == nil {
		return endpointFailure(now, FailureClassEndpointUnreachable, "passive_probe_not_wired"), nil
	}
	evidence, err := c.Probe(ctx, record)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return endpointFailure(now, FailureClassEndpointUnreachable, "passive_probe_unreachable"), nil
		}
		return endpointFailure(now, FailureClassEndpointUnreachable, "passive_probe_error"), nil
	}
	if !evidence.Reachable {
		return endpointFailure(now, FailureClassEndpointUnreachable, "passive_probe_unreachable"), nil
	}
	if record.Kind == EndpointKindOTLP && (!evidence.ProtocolMatches || !evidence.PortMatches || !evidence.AuthMatches) {
		return endpointFailure(now, FailureClassOTLPMisconfigured, "otlp_protocol_port_or_auth_mismatch"), nil
	}
	if evidence.ObservedAt.IsZero() {
		evidence.ObservedAt = now
	}
	return CheckOutcome{
		CheckID: EndpointAndHookCheckID, Status: CheckStatusPass,
		DetailRef: "passive_endpoint_verification_passed", ObservedAt: evidence.ObservedAt,
	}, nil
}

func endpointFailure(now time.Time, class FailureClass, detail string) CheckOutcome {
	return CheckOutcome{
		CheckID: EndpointAndHookCheckID, Status: CheckStatusFail,
		Category: string(class), DetailRef: detail, ObservedAt: now,
	}
}

func endpointTargetKey(installationID, capabilityID, sourceID string) string {
	return installationID + "\x00" + capabilityID + "\x00" + sourceID
}
