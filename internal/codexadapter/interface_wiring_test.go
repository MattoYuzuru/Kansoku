package codexadapter_test

import (
	"context"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/codexadapter"
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
	})
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
	if _, err := adapter.Inventory(context.Background(), adaptersdk.Installation{}); err == nil {
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
