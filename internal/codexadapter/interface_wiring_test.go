package codexadapter_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/codexadapter"
	"kansoku.local/kansoku/internal/installer"
)

// This file proves the gap a Session 06 review previously found is closed:
// codexadapter.New() registered into a live adaptersdk.Registry and driven
// through the standard adaptersdk.Adapter interface (not the free functions
// directly) must produce real Inventory/PlanConfiguration/Normalize/
// Reconcile output, not a permanent ErrNotImplementedYet.

func TestAdapterInventoryForwardsToBuildInventorySnapshot(t *testing.T) {
	registry := adaptersdk.NewRegistry()
	if err := registry.Register(codexadapter.New()); err != nil {
		t.Fatal(err)
	}
	adapter, err := registry.Get(codexadapter.AdapterID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := adapter.Inventory(context.Background(), adaptersdk.Installation{
		InstallationID: "inst-wiring-1", AdapterID: codexadapter.AdapterID, StateRoot: "/tmp/does-not-matter",
	}, nil)
	if err != nil {
		t.Fatalf("Adapter.Inventory must forward to BuildInventorySnapshot and succeed, got: %v", err)
	}
	if snapshot.AdapterID != codexadapter.AdapterID {
		t.Fatalf("expected snapshot.AdapterID %q, got %q", codexadapter.AdapterID, snapshot.AdapterID)
	}
	if snapshot.InstallationID != "inst-wiring-1" {
		t.Fatal("snapshot must carry the requested installation id through to BuildInventorySnapshot")
	}
	if len(snapshot.Nodes) == 0 {
		t.Fatal("BuildInventorySnapshot always emits at least the installation node")
	}
}

func TestAdapterInventoryRejectsEmptyInstallationID(t *testing.T) {
	adapter := codexadapter.New()
	if _, err := adapter.Inventory(context.Background(), adaptersdk.Installation{}, nil); err == nil {
		t.Fatal("Inventory must fail closed on an empty InstallationID, never silently proceed")
	}
}

func TestAdapterPlanConfigurationReusesExistingCodexUserOTelTarget(t *testing.T) {
	adapter := codexadapter.New()
	plan, err := adapter.PlanConfiguration(context.Background(), adaptersdk.Installation{
		InstallationID: "inst-wiring-1", AdapterID: codexadapter.AdapterID, StateRoot: "/tmp/codex-home",
	}, adaptersdk.CapabilityConfigurationInstall)
	if err != nil {
		t.Fatalf("PlanConfiguration must forward to installer.BuildCodexPlan/adaptersdk.BuildChangePlan and succeed, got: %v", err)
	}
	if plan.CapabilityID != adaptersdk.CapabilityConfigurationInstall {
		t.Fatalf("expected capability %q, got %q", adaptersdk.CapabilityConfigurationInstall, plan.CapabilityID)
	}
	if plan.PlanID == "" || plan.RollbackCommand == "" {
		t.Fatal("a real ChangePlan must carry a non-empty PlanID and RollbackCommand")
	}
}

// TestAdapterPlanConfigurationHookInstallRoutesToCodexUserHookTarget proves
// ADR 0014/TDD 11.B step 4: CapabilityConfigurationHookInstall is no longer
// ErrNotImplementedYet -- it produces a real ChangePlan bound to the
// codex.user_hook installer target, distinct from and never colliding with
// the codex.user_otel plan the same capability's sibling produces for the
// same physical config.toml file.
func TestAdapterPlanConfigurationHookInstallRoutesToCodexUserHookTarget(t *testing.T) {
	adapter := codexadapter.New()
	installation := adaptersdk.Installation{InstallationID: "inst-hook-1", AdapterID: codexadapter.AdapterID, StateRoot: "/tmp/codex-home"}

	hookPlan, err := adapter.PlanConfiguration(context.Background(), installation, adaptersdk.CapabilityConfigurationHookInstall)
	if err != nil {
		t.Fatalf("PlanConfiguration must forward CapabilityConfigurationHookInstall to installer.BuildCodexHookPlan/adaptersdk.BuildChangePlan and succeed, got: %v", err)
	}
	if hookPlan.CapabilityID != adaptersdk.CapabilityConfigurationHookInstall {
		t.Fatalf("expected capability %q, got %q", adaptersdk.CapabilityConfigurationHookInstall, hookPlan.CapabilityID)
	}
	if hookPlan.PlanID == "" || hookPlan.RollbackCommand == "" {
		t.Fatal("a real hook ChangePlan must carry a non-empty PlanID and RollbackCommand")
	}

	otelPlan, err := adapter.PlanConfiguration(context.Background(), installation, adaptersdk.CapabilityConfigurationInstall)
	if err != nil {
		t.Fatal(err)
	}
	if hookPlan.PlanID == otelPlan.PlanID {
		t.Fatal("the hook and otel plans for the same config.toml must be distinct ChangePlans, never a shared or overwritten one")
	}
	if hookPlan.RollbackCommand == otelPlan.RollbackCommand {
		t.Fatal("the hook and otel rollback commands must target their own installer target, never the sibling's")
	}
}

// TestAdapterPlanConfigurationHookInstallFullSimulateApplyRollbackRoundTrip
// proves TDD 11.B step 6's "full ChangePlan build + SimulateApply +
// SimulateRollback round trip" requirement for the codex.user_hook target,
// reconstructing the exact installer.Plan PlanConfiguration builds (same
// target, locator, backup and rollback command it uses) so the round trip
// exercises the same simulate-only apply/rollback machinery every other
// installer target already uses -- never a second apply mechanism.
func TestAdapterPlanConfigurationHookInstallFullSimulateApplyRollbackRoundTrip(t *testing.T) {
	adapter := codexadapter.New()
	installation := adaptersdk.Installation{InstallationID: "inst-hook-roundtrip", AdapterID: codexadapter.AdapterID, StateRoot: "/tmp/codex-home-roundtrip"}

	changePlan, err := adapter.PlanConfiguration(context.Background(), installation, adaptersdk.CapabilityConfigurationHookInstall)
	if err != nil {
		t.Fatal(err)
	}

	// PlanConfiguration always previews from an empty original config.toml
	// (see stage2_stub.go's PlanConfiguration: map[string]any{}); reconstruct
	// the identical installer.Plan shape (same target/locator/backup/
	// rollback/original) so this round trip exercises exactly what
	// PlanConfiguration itself builds. Its PlanID string differs (derived
	// from an unexported stableHex(installationID, capability) this external
	// test package cannot reproduce), but OriginalSHA256/PreconditionHash
	// depend only on the original config content, so they must match.
	original := map[string]any{}
	installerPlan, err := installer.BuildCodexHookPlan(
		"codexhookplan_probe", "/tmp/codex-home-roundtrip/config.toml", "/tmp/codex-home-roundtrip/.kansoku-backup/config.toml.hook",
		"kansoku installer rollback --target codex.user_hook", original,
	)
	if err != nil {
		t.Fatal(err)
	}
	if changePlan.PreconditionHash != installerPlan.OriginalSHA256 {
		t.Fatalf("ChangePlan.PreconditionHash must equal the underlying installer.Plan.OriginalSHA256, got %s want %s", changePlan.PreconditionHash, installerPlan.OriginalSHA256)
	}

	approval, err := installer.Approve(installerPlan, "codex.user_hook", "roundtrip-nonce")
	if err != nil {
		t.Fatal(err)
	}
	auditKey := bytes.Repeat([]byte{9}, 32)
	applied, _, err := installer.SimulateApply(original, installerPlan, approval, auditKey)
	if err != nil {
		t.Fatal(err)
	}
	if applied["notify.command"] != "kansoku-codex-hook" || applied["notify.role"] != "collection_only" {
		t.Fatalf("SimulateApply must set the plan-owned notify.* keys, got %#v", applied)
	}
	restored, _, err := installer.SimulateRollback(applied, original, installerPlan, approval, auditKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, stillPresent := restored["notify.command"]; stillPresent {
		t.Fatal("SimulateRollback must remove the plan-owned notify.command key it introduced")
	}
}

func TestAdapterPlanConfigurationDegradesOnlyUnsupportedCapability(t *testing.T) {
	adapter := codexadapter.New()
	_, err := adapter.PlanConfiguration(context.Background(), adaptersdk.Installation{
		InstallationID: "inst-wiring-1", StateRoot: "/tmp/codex-home",
	}, adaptersdk.CapabilityComponentsMCPLifecycle)
	if err == nil {
		t.Fatal("a capability with no real Codex installer target yet must fail closed, never fabricate a plausible plan")
	}
}

func TestAdapterNormalizeForwardsKnownCanonicalEventTypesUnchanged(t *testing.T) {
	adapter := codexadapter.New()
	source := adaptersdk.SafeSourceRecord{RecordID: "rec-1", EventType: "session.started", ObservedAt: time.Now()}
	events, err := adapter.Normalize(context.Background(), source)
	if err != nil {
		t.Fatalf("Normalize must accept a known canonical event type, got: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "session.started" {
		t.Fatalf("expected exactly one passthrough event with EventType session.started, got %+v", events)
	}
	if events[0].AdapterID != codexadapter.AdapterID {
		t.Fatalf("Normalize must stamp the adapter id, got %q", events[0].AdapterID)
	}
}

func TestAdapterNormalizeRejectsUnknownEventType(t *testing.T) {
	adapter := codexadapter.New()
	_, err := adapter.Normalize(context.Background(), adaptersdk.SafeSourceRecord{EventType: "not_a_real_event_type"})
	if err == nil {
		t.Fatal("Normalize must reject an event_type outside the closed hook/otel mapping tables, never pass it through unclassified")
	}
}

func TestAdapterReconcileDiffsNodeMembershipAcrossTwoSnapshots(t *testing.T) {
	adapter := codexadapter.New()
	previous, err := codexadapter.BuildInventorySnapshot(codexadapter.InventoryInput{
		InstallationID: "inst-1",
		Skills:         []codexadapter.SkillDescriptor{{Name: "alpha", Scope: adaptersdk.ScopeUser, Enabled: true}},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	current, err := codexadapter.BuildInventorySnapshot(codexadapter.InventoryInput{
		InstallationID: "inst-1",
		Skills:         []codexadapter.SkillDescriptor{{Name: "beta", Scope: adaptersdk.ScopeUser, Enabled: true}},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	result := adapter.Reconcile(context.Background(), adaptersdk.ReconcileScope{InstallationID: "inst-1"}, previous, current)
	if len(result.AddedNodeIDs) == 0 {
		t.Fatal("switching from skill 'alpha' to 'beta' must report at least one added node id")
	}
	if len(result.RemovedNodeIDs) == 0 {
		t.Fatal("switching from skill 'alpha' to 'beta' must report at least one removed node id")
	}
	if result.Completeness != "complete" {
		t.Fatalf("two non-empty snapshots must reconcile as complete, got %q", result.Completeness)
	}
}

func TestAdapterReconcileNeverReportsCompleteForAnEmptyCurrentSnapshot(t *testing.T) {
	adapter := codexadapter.New()
	previous, err := codexadapter.BuildInventorySnapshot(codexadapter.InventoryInput{InstallationID: "inst-1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	result := adapter.Reconcile(context.Background(), adaptersdk.ReconcileScope{InstallationID: "inst-1"}, previous, adaptersdk.InventorySnapshot{})
	if result.Completeness != "unknown" {
		t.Fatalf("an empty current snapshot must never be silently reported complete, got %q", result.Completeness)
	}
}
