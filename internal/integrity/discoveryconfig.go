package integrity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// DiscoveryConfigCheckID is the check_id every DiscoveryConfigCheck outcome
// reports, matching contracts/integrity/audit-run-and-schedule.yaml's
// stage_1_discovery_and_configuration stage and
// incident-and-health.yaml's "configuration" health dimension
// (sourced_from_checks: ["stage_1_discovery_and_configuration"]).
const DiscoveryConfigCheckID = "stage_1_discovery_and_configuration"

// InstallationLister supplies the confirmed Installation targets a
// DiscoveryConfigCheck evaluates for one adapter_id. Session 08 does not
// invent a second installation-persistence mechanism: a real caller closes
// this over whatever already-durable installation registry a later session
// (09/10) exposes, and stage3's own tests close it over a fixed in-memory
// list. Returning an empty slice for an adapter_id with no discovered
// installations is valid and produces zero CheckTarget entries for that
// adapter, never a fabricated placeholder installation.
type InstallationLister func(ctx context.Context, adapterID string) ([]adaptersdk.Installation, error)

// AppliedPlanFingerprint looks up the last-approved installer.PlanSHA256 this
// installation/capability pair was actually applied under, matching
// drift-fingerprint-and-schema.yaml's config_recipe_fingerprint kind
// ("installer_target_id", "applied_plan_sha256"). The bool return reports
// whether an applied plan is known at all: an installation that has never
// been configured (no applied plan on record yet) is not config drift, it is
// simply unconfigured, and the check must not conflate the two.
type AppliedPlanFingerprint func(ctx context.Context, installationID string, capability adaptersdk.CapabilityID) (planSHA256 string, known bool, err error)

// DiscoveryConfigCheck implements stage_1_discovery_and_configuration: for
// every adapter registered in an adaptersdk.Registry (iterated via
// Registry.IDs()/Get(), never a hardcoded agent name), for every discovered
// installation InstallationLister reports, it verifies:
//
//   - the adapter's executable/version/surface is still resolvable (via
//     Discover, re-run against the same HostView a real caller would use --
//     this stage never mutates anything Discover touches, matching
//     no_mutation_rule);
//   - the installation's declared StateRoot is readable (via
//     HostView.ReadProbe, bounded, never a raw filesystem walk);
//   - the config fingerprint (installer.PlanSHA256 of the last-approved
//     plan, looked up via AppliedPlanFingerprint) either matches what was
//     actually applied or the drift is explained by a targeted
//     revalidation rather than silently ignored;
//   - the plugin/skill/MCP inventory snapshot (via the adapter's own
//     Inventory) completes within the stage's declared timeout bound.
//
// DiscoveryConfigCheck reuses each adapter's existing Discover/Inventory
// methods verbatim; it never re-derives installation-discovery or
// inventory-graph logic itself.
type DiscoveryConfigCheck struct {
	Registry            *adaptersdk.Registry
	Host                *adaptersdk.HostView
	Installations       InstallationLister
	AppliedPlan         AppliedPlanFingerprint
	ObservedPlan        AppliedPlanFingerprint
	InventoryCapability adaptersdk.CapabilityID
	Now                 func() time.Time
}

// SetObservedPlanFingerprint wires the independently observed current
// configuration fingerprint used to compare against the approved/applied
// plan. It is intentionally separate from AppliedPlan.
func (c *DiscoveryConfigCheck) SetObservedPlanFingerprint(observed AppliedPlanFingerprint) {
	c.ObservedPlan = observed
}

var _ Check = (*DiscoveryConfigCheck)(nil)

// NewDiscoveryConfigCheck constructs a DiscoveryConfigCheck. installations
// and appliedPlan may be nil, in which case every installation lookup
// returns empty and every config-fingerprint lookup reports "not yet known"
// (unconfigured, not drifted) -- callers without a durable installation
// registry yet (e.g. this session's own unit tests) get a Check that runs
// safely against zero targets rather than panicking on a nil function value.
func NewDiscoveryConfigCheck(registry *adaptersdk.Registry, host *adaptersdk.HostView, installations InstallationLister, appliedPlan AppliedPlanFingerprint) *DiscoveryConfigCheck {
	if installations == nil {
		installations = func(context.Context, string) ([]adaptersdk.Installation, error) { return nil, nil }
	}
	if appliedPlan == nil {
		appliedPlan = func(context.Context, string, adaptersdk.CapabilityID) (string, bool, error) { return "", false, nil }
	}
	return &DiscoveryConfigCheck{
		Registry:            registry,
		Host:                host,
		Installations:       installations,
		AppliedPlan:         appliedPlan,
		InventoryCapability: adaptersdk.CapabilityInventoryComponents,
		Now:                 time.Now,
	}
}

func (c *DiscoveryConfigCheck) StageID() StageID { return Stage1DiscoveryAndConfiguration }
func (c *DiscoveryConfigCheck) CheckID() string  { return DiscoveryConfigCheckID }

// Targets enumerates one CheckTarget per (adapter_id's capabilities x
// discovered installation), scoped by CapabilityDiscoveryAgentAndSurface
// since that is the capability this stage's evidence is filed under
// (matching adapter-sdk's CapabilityID vocabulary the incident key reuses
// verbatim).
func (c *DiscoveryConfigCheck) Targets(ctx context.Context, in CheckInput) ([]CheckTarget, error) {
	if c.Registry == nil {
		return nil, nil
	}
	var targets []CheckTarget
	for _, adapterID := range c.Registry.IDs() {
		adapter, err := c.Registry.Get(adapterID)
		if err != nil {
			return nil, fmt.Errorf("registry get %s: %w", adapterID, err)
		}
		_ = adapter // resolved only to confirm the adapter_id is genuinely registered
		installations, err := c.Installations(ctx, adapterID)
		if err != nil {
			return nil, fmt.Errorf("list installations for %s: %w", adapterID, err)
		}
		for _, installation := range installations {
			targets = append(targets, CheckTarget{
				CapabilityID:   string(adaptersdk.CapabilityDiscoveryAgentAndSurface),
				InstallationID: installation.InstallationID,
				AdapterID:      adapterID,
			})
		}
	}
	return targets, nil
}

// discoveryFinding is the internal, structured result of one sub-check this
// Evaluate call performs, before it is folded into a single CheckOutcome.
// Keeping these as named findings (rather than a single boolean) is what
// lets a later stage 11 report which specific sub-check failed via
// DetailRef/Category without this Check inventing a second outcome type.
type discoveryFinding struct {
	category string
	failed   bool
	detail   string
}

// Evaluate runs every discovery/config sub-check for one installation and
// folds the results into one CheckOutcome. It never mutates target's agent
// configuration: Discover/Inventory/ReadProbe are exactly the read-only
// calls an already-permitted caller would make.
func (c *DiscoveryConfigCheck) Evaluate(ctx context.Context, in CheckInput, target CheckTarget) (CheckOutcome, error) {
	now := c.Now()
	if !in.Now.IsZero() {
		now = in.Now
	}
	adapter, installation, err := c.resolveInstallation(ctx, target.InstallationID)
	if err != nil {
		return CheckOutcome{
			CheckID: DiscoveryConfigCheckID, Status: CheckStatusFail,
			Category: string(FailureClassEndpointUnreachable), DetailRef: "installation_not_resolvable",
			ObservedAt: now,
		}, nil
	}

	var findings []discoveryFinding
	findings = append(findings, c.checkExecutableResolvable(ctx, adapter))
	findings = append(findings, c.checkStateRootReadable(installation))
	findings = append(findings, c.checkConfigFingerprint(ctx, installation))
	findings = append(findings, c.checkInventorySnapshot(ctx, adapter, installation))

	status := CheckStatusPass
	category := ""
	var details []string
	for _, f := range findings {
		details = append(details, f.detail)
		if f.failed {
			status = CheckStatusFail
			if category == "" {
				category = f.category
			}
		}
	}
	return CheckOutcome{
		CheckID:    DiscoveryConfigCheckID,
		Status:     status,
		Category:   category,
		DetailRef:  joinDetails(details),
		ObservedAt: now,
	}, nil
}

// resolveInstallation looks up the registered Adapter for installation's
// adapter_id. It re-derives the adapter_id by re-running Discover across
// c.Host's allowed roots is deliberately NOT done here (Discover only
// returns candidates, not confirmed installations); instead this stage
// trusts the InstallationLister's own Installation.AdapterID field, matching
// its contract that InstallationLister returns already-confirmed
// installations, not raw candidates.
func (c *DiscoveryConfigCheck) resolveInstallation(ctx context.Context, installationID string) (adaptersdk.Adapter, adaptersdk.Installation, error) {
	if c.Registry == nil {
		return nil, adaptersdk.Installation{}, errors.New("no_adapter_registry_configured")
	}
	for _, adapterID := range c.Registry.IDs() {
		installations, err := c.Installations(ctx, adapterID)
		if err != nil {
			return nil, adaptersdk.Installation{}, err
		}
		for _, installation := range installations {
			if installation.InstallationID == installationID {
				adapter, err := c.Registry.Get(adapterID)
				if err != nil {
					return nil, adaptersdk.Installation{}, err
				}
				return adapter, installation, nil
			}
		}
	}
	return nil, adaptersdk.Installation{}, fmt.Errorf("installation %q not found among registered adapters", installationID)
}

// checkExecutableResolvable re-runs Discover against c.Host and confirms at
// least one InstallationCandidate is still reported for the adapter -- i.e.
// the executable/version/surface this installation depends on has not
// disappeared since it was originally discovered. Discover is read-only by
// contract (Session 05/06/07 precedent); this call performs no write.
func (c *DiscoveryConfigCheck) checkExecutableResolvable(ctx context.Context, adapter adaptersdk.Adapter) discoveryFinding {
	if c.Host == nil {
		return discoveryFinding{category: string(FailureClassEndpointUnreachable), failed: true, detail: "executable_check_skipped_no_host_view"}
	}
	candidates, err := adapter.Discover(ctx, c.Host)
	if err != nil {
		return discoveryFinding{category: string(FailureClassEndpointUnreachable), failed: true, detail: "discovery_probe_failed"}
	}
	if len(candidates) == 0 {
		return discoveryFinding{category: string(FailureClassEndpointUnreachable), failed: true, detail: "executable_or_surface_no_longer_resolvable"}
	}
	return discoveryFinding{detail: fmt.Sprintf("executable_resolvable candidates=%d", len(candidates))}
}

// checkStateRootReadable bounds-reads installation.StateRoot via
// HostView.ReadProbe (existence/size/mtime only, never content), matching
// "expected state roots are readable ... via HostView.ReadProbe, bounded,
// never raw filesystem access".
func (c *DiscoveryConfigCheck) checkStateRootReadable(installation adaptersdk.Installation) discoveryFinding {
	if installation.StateRoot == "" {
		return discoveryFinding{category: string(FailureClassPermissionDenied), failed: true, detail: "state_root_not_declared"}
	}
	if c.Host == nil {
		return discoveryFinding{category: string(FailureClassPermissionDenied), failed: true, detail: "state_root_check_skipped_no_host_view"}
	}
	result, err := c.Host.ReadProbe(installation.StateRoot)
	if err != nil {
		if errors.Is(err, adaptersdk.ErrOutsideAllowedRoots) {
			return discoveryFinding{category: string(FailureClassPermissionDenied), failed: true, detail: "state_root_outside_allowed_roots"}
		}
		return discoveryFinding{category: string(FailureClassPermissionDenied), failed: true, detail: "state_root_read_probe_failed"}
	}
	if !result.Exists {
		return discoveryFinding{category: string(FailureClassPermissionDenied), failed: true, detail: "state_root_not_readable_or_absent"}
	}
	return discoveryFinding{detail: "state_root_readable"}
}

// checkConfigFingerprint compares the last-approved plan fingerprint
// (AppliedPlanFingerprint) against itself across the run: this stage does
// not have a second, independently-recomputed fingerprint source to compare
// against yet (that would require re-deriving a fresh installer.Plan, which
// is a write-adjacent planning operation out of scope for a passive audit
// check); instead it verifies the fingerprint is genuinely on record
// (known=true) for any installation this stage's own PlanConfiguration
// capability applies to, and treats "never configured" (known=false) as an
// explained, non-drift state -- exactly the
// "config fingerprint matches the last-applied installer plan (or drift is
// explained via a targeted revalidation, not silently ignored)" requirement,
// since an installation with no applied plan on record has nothing to drift
// from.
func (c *DiscoveryConfigCheck) checkConfigFingerprint(ctx context.Context, installation adaptersdk.Installation) discoveryFinding {
	planSHA256, known, err := c.AppliedPlan(ctx, installation.InstallationID, adaptersdk.CapabilityConfigurationInstall)
	if err != nil {
		return discoveryFinding{category: string(FailureClassHookRemovedDisabledOrUntrusted), failed: true, detail: "applied_plan_lookup_failed"}
	}
	if !known {
		return discoveryFinding{detail: "config_fingerprint_not_yet_configured_no_drift"}
	}
	if planSHA256 == "" {
		return discoveryFinding{category: string(FailureClassHookRemovedDisabledOrUntrusted), failed: true, detail: "applied_plan_fingerprint_empty"}
	}
	if c.ObservedPlan == nil {
		return discoveryFinding{category: string(FailureClassHookRemovedDisabledOrUntrusted), failed: true, detail: "observed_config_fingerprint_not_wired"}
	}
	observed, observedKnown, err := c.ObservedPlan(ctx, installation.InstallationID, adaptersdk.CapabilityConfigurationInstall)
	if err != nil {
		return discoveryFinding{category: string(FailureClassHookRemovedDisabledOrUntrusted), failed: true, detail: "observed_config_fingerprint_lookup_failed"}
	}
	if !observedKnown || observed == "" || observed != planSHA256 {
		return discoveryFinding{category: string(FailureClassHookRemovedDisabledOrUntrusted), failed: true, detail: "observed_config_fingerprint_drift"}
	}
	return discoveryFinding{detail: "config_fingerprint_matches_approved_plan"}
}

// checkInventorySnapshot calls the adapter's own Inventory (never a
// re-derived inventory graph) and confirms it completes without error,
// matching "the plugin/skill/MCP inventory snapshot completes within
// bounds". The stage's own declared timeout_seconds bounds this call at the
// Scheduler/stage-execution layer (ctx deadline), not inside this method.
func (c *DiscoveryConfigCheck) checkInventorySnapshot(ctx context.Context, adapter adaptersdk.Adapter, installation adaptersdk.Installation) discoveryFinding {
	snapshot, err := adapter.Inventory(ctx, installation, c.Host)
	if err != nil {
		return discoveryFinding{category: string(FailureClassEndpointUnreachable), failed: true, detail: "inventory_snapshot_failed"}
	}
	if snapshot.SnapshotID == "" {
		return discoveryFinding{category: string(FailureClassEndpointUnreachable), failed: true, detail: "inventory_snapshot_empty_id"}
	}
	return classifyInventorySnapshot(snapshot)
}

func classifyInventorySnapshot(snapshot adaptersdk.InventorySnapshot) discoveryFinding {
	nodes := make(map[string]adaptersdk.Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes[node.NodeID] = node
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeEnabledFor && nodes[edge.FromNode].CachedOnly {
			return discoveryFinding{category: string(FailureClassPermissionDenied), failed: true, detail: "cached_component_miscounted_as_enabled"}
		}
	}
	return discoveryFinding{detail: fmt.Sprintf("inventory_snapshot_ok nodes=%d edges=%d", len(snapshot.Nodes), len(snapshot.Edges))}
}

func joinDetails(details []string) string {
	out := ""
	for i, d := range details {
		if i > 0 {
			out += "; "
		}
		out += d
	}
	return out
}
