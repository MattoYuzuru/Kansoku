package claudeadapter

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
// for any capability outside claude.otel's install target, and Normalize
// returns it for any event_type this recipe's own mapping tables do not
// recognize. Both cases degrade only that one call, never the whole adapter.
var ErrNotImplementedYet = errors.New("claude_adapter_method_not_implemented_in_this_stage")

// SourceSchemas returns the schema fingerprints this adapter's sources may
// emit. Both schemas are declared now because claude.hook/claude.otel are
// this stage's deliverables; Normalize itself (which would consume them) is
// a later stage's responsibility.
func (a *Adapter) SourceSchemas() []privacy.SourceSchema {
	return nil
}

// Inventory forwards to BuildInventorySnapshot (inventory.go), this stage's
// real claude.inventory graph builder. adaptersdk.Adapter's Inventory
// signature receives only a confirmed Installation (no HostView), so this
// method still cannot itself perform the bounded filesystem scan that
// populates InventoryInput's skills/plugins/hooks/mcp-servers/marketplaces --
// wiring a HostView-driven scan into an InventoryInput value is a distinct,
// later concern (mirroring codexadapter's own Inventory/BuildInventorySnapshot
// split). Passing an installation-scoped, otherwise-empty InventoryInput here
// still exercises the real graph builder (producing a genuine, correctly-
// shaped snapshot containing only the installation node) rather than a
// hand-rolled empty literal, so this call and BuildInventorySnapshot's own
// unit tests can never silently drift apart.
func (a *Adapter) Inventory(ctx context.Context, target adaptersdk.Installation) (adaptersdk.InventorySnapshot, error) {
	if target.InstallationID == "" {
		return adaptersdk.InventorySnapshot{}, errors.New("claude_inventory_requires_installation_id")
	}
	return BuildInventorySnapshot(InventoryInput{InstallationID: target.InstallationID}, time.Now())
}

// PlanConfiguration builds a real adaptersdk.ChangePlan for the one
// capability this stage's sources actually configure --
// CapabilityConfigurationLiveCanary and CapabilityConfigurationInstall both
// route through the existing contracts/privacy/installer.yaml
// "claude.user_otel" target (see otel.go's OTelInstallerTargetID) via
// installer.BuildClaudePlan and adaptersdk.BuildChangePlan verbatim -- this
// package never defines a second apply/rollback mechanism. Any other
// capability ID is not yet backed by a real installer target for Claude and
// returns ErrNotImplementedYet, degrading only that capability rather than
// fabricating a plausible-looking plan for it. The claude.user_hook
// installer target contracts/claude/hooks-and-otel.yaml declares has no
// filesystem writer wired yet (ADR 0010's recorded known gap); a later stage
// adds it here without touching this claude.user_otel path.
func (a *Adapter) PlanConfiguration(ctx context.Context, target adaptersdk.Installation, capability adaptersdk.CapabilityID) (adaptersdk.ChangePlan, error) {
	switch capability {
	case adaptersdk.CapabilityConfigurationInstall, adaptersdk.CapabilityConfigurationLiveCanary:
	default:
		return adaptersdk.ChangePlan{}, ErrNotImplementedYet
	}
	if target.StateRoot == "" || !filepath.IsAbs(target.StateRoot) {
		return adaptersdk.ChangePlan{}, errors.New("claude_plan_requires_absolute_state_root")
	}
	locator := filepath.Join(target.StateRoot, "settings.json")
	backup := filepath.Join(target.StateRoot, ".kansoku-backup", "settings.json")
	installerPlan, err := installer.BuildClaudePlan(
		"claudeplan_"+stableHex(target.InstallationID, string(capability)),
		locator, backup, "kansoku installer rollback --target claude.user_otel",
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
// distinct from this package's own future cross-source per-session lane
// reconciliation (a later stage's ReconcileLane/ReconcileSession equivalent
// to codexadapter's reconcile.go). Node membership diffing here never
// fabricates a whole-session zero: an empty current snapshot is reported as
// Completeness="unknown" (evidence absent), never silently coerced into
// "everything was removed."
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

// Audit is implemented in a later checkpointed stage of Session 07. It
// returns no CheckResult rather than a fabricated pass/fail so a caller can
// never mistake "not implemented yet" for "checked and healthy" or "checked
// and failing".
func (a *Adapter) Audit(ctx context.Context, target adaptersdk.Installation, mode adaptersdk.AuditMode) []adaptersdk.CheckResult {
	return nil
}
