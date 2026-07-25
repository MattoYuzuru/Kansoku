package claudeadapter_test

import (
	"bytes"
	"context"
	"testing"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/claudeadapter"
	"kansoku.local/kansoku/internal/installer"
)

// This file proves, for claudeadapter, the exact same gap codexadapter's own
// interface_wiring_test.go closes: PlanConfiguration driven through the
// standard adaptersdk.Adapter interface must produce a real ChangePlan for
// every capability this stage actually backs -- including Session 11 (ADR
// 0014)'s new CapabilityConfigurationHookInstall -- never a permanent
// ErrNotImplementedYet.

func TestAdapterPlanConfigurationReusesExistingClaudeUserOTelTarget(t *testing.T) {
	adapter := claudeadapter.New()
	plan, err := adapter.PlanConfiguration(context.Background(), adaptersdk.Installation{
		InstallationID: "inst-wiring-1", AdapterID: claudeadapter.AdapterID, StateRoot: "/tmp/claude-home",
	}, adaptersdk.CapabilityConfigurationInstall)
	if err != nil {
		t.Fatalf("PlanConfiguration must forward to installer.BuildClaudePlan/adaptersdk.BuildChangePlan and succeed, got: %v", err)
	}
	if plan.CapabilityID != adaptersdk.CapabilityConfigurationInstall {
		t.Fatalf("expected capability %q, got %q", adaptersdk.CapabilityConfigurationInstall, plan.CapabilityID)
	}
	if plan.PlanID == "" || plan.RollbackCommand == "" {
		t.Fatal("a real ChangePlan must carry a non-empty PlanID and RollbackCommand")
	}
}

// TestAdapterPlanConfigurationHookInstallRoutesToClaudeUserHookTarget proves
// ADR 0014/TDD 11.B step 4: CapabilityConfigurationHookInstall is no longer
// ErrNotImplementedYet -- it produces a real ChangePlan bound to the
// claude.user_hook installer target, distinct from and never colliding with
// the claude.user_otel plan the same capability's sibling produces for the
// same physical settings.json file. This closes ADR 0010's recorded known
// gap ("claude.user_hook has no real filesystem writer yet").
func TestAdapterPlanConfigurationHookInstallRoutesToClaudeUserHookTarget(t *testing.T) {
	adapter := claudeadapter.New()
	installation := adaptersdk.Installation{InstallationID: "inst-hook-1", AdapterID: claudeadapter.AdapterID, StateRoot: "/tmp/claude-home"}

	hookPlan, err := adapter.PlanConfiguration(context.Background(), installation, adaptersdk.CapabilityConfigurationHookInstall)
	if err != nil {
		t.Fatalf("PlanConfiguration must forward CapabilityConfigurationHookInstall to installer.BuildClaudeHookPlan/adaptersdk.BuildChangePlan and succeed, got: %v", err)
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
		t.Fatal("the hook and otel plans for the same settings.json must be distinct ChangePlans, never a shared or overwritten one")
	}
	if hookPlan.RollbackCommand == otelPlan.RollbackCommand {
		t.Fatal("the hook and otel rollback commands must target their own installer target, never the sibling's")
	}
}

// TestAdapterPlanConfigurationHookInstallFullSimulateApplyRollbackRoundTrip
// proves TDD 11.B step 6's "full ChangePlan build + SimulateApply +
// SimulateRollback round trip" requirement for the claude.user_hook target,
// reconstructing the exact installer.Plan PlanConfiguration builds (same
// target, locator, backup and rollback command it uses) so the round trip
// exercises the same simulate-only apply/rollback machinery every other
// installer target already uses -- never a second apply mechanism.
func TestAdapterPlanConfigurationHookInstallFullSimulateApplyRollbackRoundTrip(t *testing.T) {
	adapter := claudeadapter.New()
	installation := adaptersdk.Installation{InstallationID: "inst-hook-roundtrip", AdapterID: claudeadapter.AdapterID, StateRoot: "/tmp/claude-home-roundtrip"}

	changePlan, err := adapter.PlanConfiguration(context.Background(), installation, adaptersdk.CapabilityConfigurationHookInstall)
	if err != nil {
		t.Fatal(err)
	}

	// PlanConfiguration always previews from an empty original settings.json
	// (see stage2_stub.go's PlanConfiguration: map[string]any{}); reconstruct
	// the identical installer.Plan shape so this round trip exercises exactly
	// what PlanConfiguration itself builds. Its PlanID string differs
	// (derived from an unexported stableHex(installationID, capability) this
	// external test package cannot reproduce), but OriginalSHA256/
	// PreconditionHash depend only on the original config content, so they
	// must match.
	original := map[string]any{}
	installerPlan, err := installer.BuildClaudeHookPlan(
		"claudehookplan_probe", "/tmp/claude-home-roundtrip/settings.json", "/tmp/claude-home-roundtrip/.kansoku-backup/settings.json.hook",
		"kansoku installer rollback --target claude.user_hook", original,
	)
	if err != nil {
		t.Fatal(err)
	}
	if changePlan.PreconditionHash != installerPlan.OriginalSHA256 {
		t.Fatalf("ChangePlan.PreconditionHash must equal the underlying installer.Plan.OriginalSHA256, got %s want %s", changePlan.PreconditionHash, installerPlan.OriginalSHA256)
	}

	approval, err := installer.Approve(installerPlan, "claude.user_hook", "roundtrip-nonce")
	if err != nil {
		t.Fatal(err)
	}
	auditKey := bytes.Repeat([]byte{9}, 32)
	applied, _, err := installer.SimulateApply(original, installerPlan, approval, auditKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "SubagentStart", "SubagentStop", "Stop"} {
		if applied["hooks."+event] != "kansoku-claude-hook" {
			t.Fatalf("SimulateApply must set the plan-owned hooks.%s key, got %#v", event, applied["hooks."+event])
		}
	}
	restored, _, err := installer.SimulateRollback(applied, original, installerPlan, approval, auditKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, stillPresent := restored["hooks.SessionStart"]; stillPresent {
		t.Fatal("SimulateRollback must remove the plan-owned hooks.* keys it introduced")
	}
}

func TestAdapterPlanConfigurationDegradesOnlyUnsupportedCapability(t *testing.T) {
	adapter := claudeadapter.New()
	_, err := adapter.PlanConfiguration(context.Background(), adaptersdk.Installation{
		InstallationID: "inst-wiring-1", StateRoot: "/tmp/claude-home",
	}, adaptersdk.CapabilityComponentsMCPLifecycle)
	if err == nil {
		t.Fatal("a capability with no real Claude installer target yet must fail closed, never fabricate a plausible plan")
	}
}
