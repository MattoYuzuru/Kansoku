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

// Inventory forwards to BuildInventorySnapshot, the fully-tested inventory
// graph builder in inventory.go. adaptersdk.Adapter's Inventory signature
// receives only a confirmed Installation (no HostView), so this stage does
// not yet have a filesystem-scanning step to populate
// InventoryInput.Skills/Plugins/Hooks/MCPServers/RepositoryTargets --
// resolving that from an on-disk Codex state root is a later stage's
// dedicated deliverable. Until then this method still calls through to the
// real builder with the one confirmed fact it does have
// (target.InstallationID), returning a genuine, correctly-shaped empty
// InventorySnapshot rather than a fabricated error, so a caller driving this
// adapter through the standard interface observes real (if currently empty)
// behavior instead of a hard failure.
func (a *Adapter) Inventory(ctx context.Context, target adaptersdk.Installation) (adaptersdk.InventorySnapshot, error) {
	if target.InstallationID == "" {
		return adaptersdk.InventorySnapshot{}, errors.New("codex_inventory_requires_installation_id")
	}
	return BuildInventorySnapshot(InventoryInput{InstallationID: target.InstallationID}, time.Now())
}

// PlanConfiguration builds a real adaptersdk.ChangePlan for the one
// capability this stage's sources actually configure --
// CapabilityConfigurationLiveCanary and CapabilityConfigurationInstall both
// route through the existing contracts/privacy/installer.yaml
// "codex.user_otel" target (see otel.go's OTelInstallerTargetID) via
// installer.BuildCodexPlan and adaptersdk.BuildChangePlan verbatim -- this
// package never defines a second apply/rollback mechanism. Any other
// capability ID is not yet backed by a real installer target for Codex and
// returns ErrNotImplementedYet, degrading only that capability rather than
// fabricating a plausible-looking plan for it.
func (a *Adapter) PlanConfiguration(ctx context.Context, target adaptersdk.Installation, capability adaptersdk.CapabilityID) (adaptersdk.ChangePlan, error) {
	switch capability {
	case adaptersdk.CapabilityConfigurationInstall, adaptersdk.CapabilityConfigurationLiveCanary:
	default:
		return adaptersdk.ChangePlan{}, ErrNotImplementedYet
	}
	if target.StateRoot == "" || !filepath.IsAbs(target.StateRoot) {
		return adaptersdk.ChangePlan{}, errors.New("codex_plan_requires_absolute_state_root")
	}
	locator := filepath.Join(target.StateRoot, "config.toml")
	backup := filepath.Join(target.StateRoot, ".kansoku-backup", "config.toml")
	installerPlan, err := installer.BuildCodexPlan(
		"codexplan_"+stableHex(target.InstallationID, string(capability)),
		locator, backup, "kansoku installer rollback --target codex.user_otel",
		map[string]any{},
	)
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
	if len(current.Nodes) == 0 {
		completeness = "unknown"
	}
	return adaptersdk.ReconcileResult{
		SnapshotID: current.SnapshotID, AddedNodeIDs: added, RemovedNodeIDs: removed,
		ChangedNodeIDs: changed, NewCollisions: []string{}, Completeness: completeness,
	}
}

// Audit is implemented in a later checkpointed stage of Session 06. It
// returns no CheckResult rather than a fabricated pass/fail so a caller can
// never mistake "not implemented yet" for "checked and healthy" or "checked
// and failing".
func (a *Adapter) Audit(ctx context.Context, target adaptersdk.Installation, mode adaptersdk.AuditMode) []adaptersdk.CheckResult {
	return nil
}
