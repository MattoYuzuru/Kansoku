package codexadapter

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/installer"
	"kansoku.local/kansoku/internal/privacy"
)

// ErrNotImplementedYet is returned by an adaptersdk.Adapter method call this
// package cannot service for the reason stated at the call site (never as a
// blanket "later stage" placeholder any more): PlanConfiguration returns it
// for any capability outside codex.otel's install target, and Normalize
// returns it for any event_type this recipe's own mapping tables do not
// recognize. Both cases degrade only that one call, never the whole adapter.
var ErrNotImplementedYet = errors.New("codex_adapter_method_not_implemented_in_this_stage")

// SourceSchemas returns the schema fingerprints this adapter's sources may
// emit. Both schemas are declared now because codex.hook/codex.otel are this
// stage's deliverables; Normalize itself (which would consume them) is a
// later stage's responsibility.
func (a *Adapter) SourceSchemas() []privacy.SourceSchema {
	return nil
}

// Inventory performs a real, bounded host filesystem scan (Session 11, ADR
// 0014, Gap C) through ScanHostInventory in inventoryscan.go, then forwards
// the resulting InventoryInput to BuildInventorySnapshot, the fully-tested
// inventory graph builder in inventory.go. host may be nil (e.g. a caller
// that has no permission-checked HostView, such as an older integration
// test) -- in that case ScanHostInventory reports scanned=false and this
// method still returns a genuine, correctly-shaped InventorySnapshot
// containing only the installation node, never a fabricated error. Whether
// a scan actually completed and observed real components is recorded via
// hasComponentNodes/Reconcile's Completeness field, never silently
// collapsed into "zero configured = complete".
func (a *Adapter) Inventory(ctx context.Context, target adaptersdk.Installation, host *adaptersdk.HostView) (adaptersdk.InventorySnapshot, error) {
	if target.InstallationID == "" {
		return adaptersdk.InventorySnapshot{}, errors.New("codex_inventory_requires_installation_id")
	}
	input, _ := ScanHostInventory(host, target)
	return BuildInventorySnapshot(input, time.Now())
}

// PlanConfiguration builds a real adaptersdk.ChangePlan for the capabilities
// this stage's sources actually configure. CapabilityConfigurationLiveCanary
// and CapabilityConfigurationInstall route through the existing
// contracts/privacy/installer.yaml "codex.user_otel" target (see otel.go's
// OTelInstallerTargetID) via installer.BuildCodexPlan and
// adaptersdk.BuildChangePlan verbatim. CapabilityConfigurationHookInstall
// (Session 11, ADR 0014) routes through the new "codex.user_hook" target via
// installer.BuildCodexHookPlan the same way -- this package never defines a
// second apply/rollback mechanism for either capability. Both targets share
// one physical file (config.toml, different tables); each is built from an
// independent installer.Plan with its own PlanID/backup locator/rollback
// command, so applying or rolling back one never references the other's
// plan-owned keys (see protocol.go's buildTargetPlan ownership model and
// protocol_test.go's round-trip proof). Any other capability ID is not yet
// backed by a real installer target for Codex and returns
// ErrNotImplementedYet, degrading only that capability rather than
// fabricating a plausible-looking plan for it.
func (a *Adapter) PlanConfiguration(ctx context.Context, target adaptersdk.Installation, capability adaptersdk.CapabilityID) (adaptersdk.ChangePlan, error) {
	if target.StateRoot == "" || !filepath.IsAbs(target.StateRoot) {
		return adaptersdk.ChangePlan{}, errors.New("codex_plan_requires_absolute_state_root")
	}
	locator := filepath.Join(target.StateRoot, "config.toml")

	var installerPlan installer.Plan
	var err error
	switch capability {
	case adaptersdk.CapabilityConfigurationInstall, adaptersdk.CapabilityConfigurationLiveCanary:
		backup := filepath.Join(target.StateRoot, ".kansoku-backup", "config.toml")
		installerPlan, err = installer.BuildCodexPlan(
			"codexplan_"+stableHex(target.InstallationID, string(capability)),
			locator, backup, "kansoku installer rollback --target codex.user_otel",
			map[string]any{},
		)
	case adaptersdk.CapabilityConfigurationHookInstall:
		backup := filepath.Join(target.StateRoot, ".kansoku-backup", "config.toml.hook")
		installerPlan, err = installer.BuildCodexHookPlan(
			"codexhookplan_"+stableHex(target.InstallationID, string(capability)),
			locator, backup, "kansoku installer rollback --target codex.user_hook",
			map[string]any{},
		)
	default:
		return adaptersdk.ChangePlan{}, ErrNotImplementedYet
	}
	if err != nil {
		return adaptersdk.ChangePlan{}, err
	}
	return adaptersdk.BuildChangePlan(installerPlan, target.InstallationID, capability)
}

// Normalize maps one already-sanitized SafeSourceRecord onto its canonical
// event type using exactly the closed hook/otel event-name mapping tables
// hook.go/otel.go already declare (CanonicalEventForHook/
// otelEventCanonical) -- no second, parallel mapping is invented here. An
// event_type this recipe's active mapping tables do not recognize returns
// ErrNotImplementedYet rather than silently passing the record through
// unclassified or fabricating a canonical type for it.
func (a *Adapter) Normalize(ctx context.Context, source adaptersdk.SafeSourceRecord) ([]adaptersdk.CanonicalEvent, error) {
	if !knownCanonicalEventType(source.EventType) {
		return nil, ErrNotImplementedYet
	}
	normalized := source
	normalized.AdapterID = AdapterID
	normalized.AdapterVersion = AdapterVersion
	return []adaptersdk.CanonicalEvent{normalized}, nil
}

// knownCanonicalEventType reports whether eventType is a canonical event
// type this recipe's own hook/otel mapping tables (hook.go's
// hookEventCanonical, otel.go's otelEventCanonical) actually produce.
// Normalize never trusts an arbitrary caller-supplied event_type beyond this
// closed set.
func knownCanonicalEventType(eventType string) bool {
	if eventType == "" {
		return false
	}
	known := map[string]struct{}{}
	for _, canonical := range hookEventCanonical {
		known[canonical] = struct{}{}
	}
	for _, canonical := range otelEventCanonical {
		known[canonical] = struct{}{}
	}
	_, ok := known[eventType]
	return ok
}

// Reconcile diffs two InventorySnapshot values by NodeID membership -- the
// exact contract adaptersdk.Adapter.Reconcile declares (compare two already-
// built snapshots and report added/removed/changed node IDs), which is
// distinct from this package's own cross-source per-session lane
// reconciliation in reconcile.go (ReconcileLane/ReconcileSession compare
// codex.hook/codex.otel/codex.rollout activity evidence for one session, not
// two inventory snapshots). Both are real: a caller that already has two
// InventorySnapshot values calls this method; a caller reconciling one
// session's hook/otel/rollout activity evidence calls ReconcileSession
// directly, exactly as canary_chain_test.go does. Node membership diffing
// here never fabricates a whole-session zero: an empty current snapshot is
// reported as Completeness="unknown" (evidence absent), never silently
// coerced into "everything was removed."
func (a *Adapter) Reconcile(ctx context.Context, scope adaptersdk.ReconcileScope, previous, current adaptersdk.InventorySnapshot) adaptersdk.ReconcileResult {
	previousNodes := make(map[string]adaptersdk.Node, len(previous.Nodes))
	for _, node := range previous.Nodes {
		previousNodes[node.NodeID] = node
	}
	currentNodes := make(map[string]adaptersdk.Node, len(current.Nodes))
	for _, node := range current.Nodes {
		currentNodes[node.NodeID] = node
	}
	var added, removed, changed []string
	for id, node := range currentNodes {
		if priorNode, existed := previousNodes[id]; !existed {
			added = append(added, id)
		} else if priorNode.Fingerprint != node.Fingerprint {
			changed = append(changed, id)
		}
	}
	for id := range previousNodes {
		if _, stillPresent := currentNodes[id]; !stillPresent {
			removed = append(removed, id)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	completeness := "complete"
	if !hasComponentNodes(current) {
		completeness = "unknown"
	} else if current.CoverageGapCount > 0 {
		// A scan that skipped entries it could not read is partial, never
		// complete, mirroring claudeadapter's identical rule.
		completeness = "partial"
	}
	return adaptersdk.ReconcileResult{
		SnapshotID: current.SnapshotID, AddedNodeIDs: added, RemovedNodeIDs: removed,
		ChangedNodeIDs: changed, NewCollisions: []string{}, Completeness: completeness,
	}
}

// hasComponentNodes reports whether snapshot contains at least one node
// beyond the always-present agent_installation node itself. BuildInventorySnapshot
// unconditionally emits the installation node even when a host scan found
// zero skills/plugins/hooks/MCP servers, so a snapshot's Nodes slice being
// non-empty is never on its own evidence of a genuinely populated scan --
// Reconcile must look past that one guaranteed node before it reports
// Completeness="complete", or an installation with zero configured
// components would be silently misreported as a complete, healthy, empty
// inventory instead of "unknown" (evidence absent).
func hasComponentNodes(snapshot adaptersdk.InventorySnapshot) bool {
	for _, node := range snapshot.Nodes {
		if node.Kind != adaptersdk.NodeAgentInstallation {
			return true
		}
	}
	return false
}

// Audit is implemented in a later checkpointed stage of Session 06. It
// returns no CheckResult rather than a fabricated pass/fail so a caller can
// never mistake "not implemented yet" for "checked and healthy" or "checked
// and failing".
func (a *Adapter) Audit(ctx context.Context, target adaptersdk.Installation, mode adaptersdk.AuditMode) []adaptersdk.CheckResult {
	return nil
}
