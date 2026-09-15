package installer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeTargetPoliciesMatchAuthoritativeInstallerContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "privacy", "installer.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Targets []struct {
			ID           string         `json:"id"`
			AgentID      string         `json:"agent_id"`
			Format       string         `json:"format"`
			LocatorKind  string         `json:"config_locator_kind"`
			Ownership    string         `json:"ownership"`
			Required     map[string]any `json:"required_settings"`
			Forbidden    []string       `json:"forbidden_keys"`
			NeverWritten []string       `json:"never_written_keys"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.Targets) != len(targetSpecs) {
		t.Fatal("target population drift")
	}
	for _, expected := range contract.Targets {
		actual, ok := targetSpecs[expected.ID]
		if !ok || actual.agent != expected.AgentID || actual.format != expected.Format || actual.locatorKind != expected.LocatorKind || actual.ownership != expected.Ownership || !reflect.DeepEqual(actual.required, expected.Required) || !reflect.DeepEqual(actual.forbidden, expected.Forbidden) || !reflect.DeepEqual(actual.neverWritten, expected.NeverWritten) {
			t.Fatalf("runtime target policy drift: %s", expected.ID)
		}
		// A key cannot be both required and never-written; the runtime rejects
		// such a spec, and the contract must not declare one either.
		for _, key := range expected.NeverWritten {
			if _, required := expected.Required[key]; required {
				t.Fatalf("%s declares %s as both required and never-written", expected.ID, key)
			}
		}
	}
}

type builder func(string, string, string, string, map[string]any) (Plan, error)

var builders = map[string]builder{
	"codex.user_otel": BuildCodexPlan, "claude.user_otel": BuildClaudePlan,
	"gemini.user_otel": BuildGeminiPlan, "cursor.user_hooks": BuildCursorPlan,
	"codex.user_hook": BuildCodexHookPlan, "claude.user_hook": BuildClaudeHookPlan,
}

func samplePlan(t *testing.T, target string) (Plan, map[string]any) {
	t.Helper()
	original := map[string]any{"unrelated": "preserve"}
	plan, err := builders[target]("plan-"+target, "/private/preview/config", "/private/preview/backup", "/usr/bin/kansoku rollback", original)
	if err != nil {
		t.Fatal(err)
	}
	return plan, original
}

func TestTypedAgentPlansDeriveExactOperationsAndBindConsent(t *testing.T) {
	for target := range builders {
		t.Run(target, func(t *testing.T) {
			plan, original := samplePlan(t, target)
			spec := targetSpecs[target]
			if plan.AgentID != spec.agent || plan.ConfigFormat != spec.format || plan.ConfigLocatorKind != spec.locatorKind || plan.Ownership != spec.ownership {
				t.Fatalf("target metadata=%#v", plan)
			}
			if len(plan.ExactOperations) != len(spec.required) || !reflect.DeepEqual(plan.DisclosedFields, sortedKeys(spec.required)) {
				t.Fatal("operations not derived from required settings")
			}
			preview, err := RenderPreview(plan)
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"config_locator_kind", "ownership", "exact_operations", "original_sha256", "planned_sha256"} {
				if !bytes.Contains(preview, []byte(field)) {
					t.Errorf("preview missing %s", field)
				}
			}
			if _, _, err := SimulateApply(original, plan, Approval{}, bytes.Repeat([]byte{1}, 32)); err == nil {
				t.Fatal("apply without consent")
			}
			approval, err := Approve(plan, target, "explicit-nonce")
			if err != nil {
				t.Fatal(err)
			}
			applied, receipt, err := SimulateApply(original, plan, approval, bytes.Repeat([]byte{1}, 32))
			if err != nil {
				t.Fatal(err)
			}
			for key, expected := range spec.required {
				if !equalCanonical(applied[key], expected) {
					t.Fatalf("%s=%v", key, applied[key])
				}
			}
			encoded, _ := json.Marshal(receipt)
			if bytes.Contains(encoded, []byte(plan.ConfigLocator)) || strings.Contains(string(encoded), "/private/") {
				t.Fatal("path leaked to audit")
			}
			restored, rollbackReceipt, err := SimulateRollback(applied, original, plan, approval, bytes.Repeat([]byte{1}, 32))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(normalizeMap(t, restored), normalizeMap(t, original)) || rollbackReceipt.Operation != "rollback" {
				t.Fatal("rollback mismatch")
			}
			removed, removeReceipt, err := SimulateRemove(applied, original, plan, approval, bytes.Repeat([]byte{1}, 32))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(normalizeMap(t, removed), normalizeMap(t, original)) || removeReceipt.Operation != "remove" {
				t.Fatal("remove semantics mismatch")
			}
		})
	}
}

func TestEffectiveSettingsAndRuntimeCanaryFailClosed(t *testing.T) {
	plan, original := samplePlan(t, "gemini.user_otel")
	approval, _ := Approve(plan, plan.TargetID, "nonce")
	applied, _, err := SimulateApply(original, plan, approval, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	valid := RuntimeCanaryReceipt{TargetID: plan.TargetID, ConfigSHA256: plan.PlannedSHA256, SourceRevision: "fixture-revision", Status: "pass"}
	if err := VerifyEffectiveSettings(plan, EffectiveSettings{TargetID: plan.TargetID, Values: applied}, valid); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		effective EffectiveSettings
		canary    RuntimeCanaryReceipt
		want      string
	}{
		{"missing_canary", EffectiveSettings{TargetID: plan.TargetID, Values: applied}, RuntimeCanaryReceipt{}, "runtime_canary_required"},
		{"managed_override", EffectiveSettings{TargetID: plan.TargetID, Values: applied, BlockingOverrides: []string{"managed policy"}}, valid, "managed_or_environment_override"},
		{"unsafe_gcp", EffectiveSettings{TargetID: plan.TargetID, Values: merge(applied, map[string]any{"telemetry.target": "gcp"})}, valid, "effective_settings_mismatch"},
		{"outfile", EffectiveSettings{TargetID: plan.TargetID, Values: merge(applied, map[string]any{"telemetry.outfile": "/tmp/raw"})}, valid, "unsafe_effective_setting"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if err := VerifyEffectiveSettings(plan, item.effective, item.canary); err == nil || err.Error() != item.want {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if err := AuthorizeRealWrite(plan, approval, EffectiveSettings{}, valid); err == nil || err.Error() != "real_agent_config_write_not_implemented_session_02" {
		t.Fatalf("real write err=%v", err)
	}
}

func TestUnsafeExistingSettingsAndUnboundedConfigsAreRejected(t *testing.T) {
	cases := []struct {
		target   string
		original map[string]any
	}{
		{"codex.user_otel", map[string]any{"otel.endpoint": "https://remote.invalid"}},
		// env.OTEL_EXPORTER_OTLP_HEADERS moved to never-written: see
		// TestPreExistingUserOwnedHeadersYieldADisclosedPlan. These two stay
		// hard-forbidden -- a file exporter writes telemetry outside the
		// loopback sanitizer, and a remote endpoint defeats the boundary.
		{"claude.user_otel", map[string]any{"env.OTEL_LOGS_EXPORTER_FILE": "/tmp/raw-logs"}},
		{"claude.user_otel", map[string]any{"env.OTEL_EXPORTER_OTLP_ENDPOINT": "https://remote.invalid"}},
		{"gemini.user_otel", map[string]any{"telemetry.target": "gcp"}},
		{"gemini.user_otel", map[string]any{"telemetry.outfile": "/tmp/raw"}},
		{"cursor.user_hooks", map[string]any{"hook_as_privacy_enforcement": true}},
	}
	for _, item := range cases {
		if _, err := builders[item.target]("plan", "/private/config", "/private/backup", "/bin/rollback", item.original); err == nil {
			t.Errorf("accepted unsafe %s", item.target)
		}
	}
	tooLarge := map[string]any{}
	for index := 0; index <= maxConfigEntries; index++ {
		tooLarge[string(rune('a'+index%26))+strings.Repeat("x", index/26)] = index
	}
	if _, err := BuildCodexPlan("plan", "/private/config", "/private/backup", "/bin/rollback", tooLarge); err == nil {
		t.Fatal("accepted unbounded config")
	}
}

func TestApplyRollbackAndApprovalRefuseEveryRevisionRace(t *testing.T) {
	plan, original := samplePlan(t, "codex.user_otel")
	approval, _ := Approve(plan, plan.TargetID, "nonce")
	mutated := cloneForTest(t, original)
	mutated["user_change"] = true
	if _, _, err := SimulateApply(mutated, plan, approval, bytes.Repeat([]byte{2}, 32)); err == nil || err.Error() != "config_race" {
		t.Fatalf("err=%v", err)
	}
	tamperedApproval := approval
	tamperedApproval.PlannedSHA256 = strings.Repeat("0", 64)
	if _, _, err := SimulateApply(original, plan, tamperedApproval, bytes.Repeat([]byte{2}, 32)); err == nil || err.Error() != "approval_binding_mismatch" {
		t.Fatalf("err=%v", err)
	}
	applied, _, _ := SimulateApply(original, plan, approval, bytes.Repeat([]byte{2}, 32))
	applied["later_user_change"] = true
	if _, _, err := SimulateRollback(applied, original, plan, approval, bytes.Repeat([]byte{2}, 32)); err == nil || err.Error() != "config_race" {
		t.Fatalf("err=%v", err)
	}
}

// TestHookPlanOwnershipIsolationRoundTrip proves ADR 0014 decision 4's hard
// requirement for both Codex (config.toml) and Claude (settings.json):
// apply the *.user_otel plan, apply the sibling *.user_hook plan into the
// same physical file, then roll back only the hook plan, and assert the
// otel target's already-applied keys and any unrelated pre-existing user
// content are byte-identical to their state immediately before the hook
// plan was ever applied -- not merely equal by assertion, but recomputed
// from the actual SimulateApply/SimulateRollback outputs.
func TestHookPlanOwnershipIsolationRoundTrip(t *testing.T) {
	cases := []struct {
		name       string
		otelTarget string
		hookTarget string
	}{
		{"codex", "codex.user_otel", "codex.user_hook"},
		{"claude", "claude.user_otel", "claude.user_hook"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			auditKey := bytes.Repeat([]byte{7}, 32)
			original := map[string]any{"unrelated_user_setting": "keep-me", "another_block": map[string]any{"nested": "value"}}

			otelPlan, err := builders[item.otelTarget]("otelplan", "/private/preview/otel-config", "/private/preview/otel-backup", "/usr/bin/kansoku rollback --target "+item.otelTarget, original)
			if err != nil {
				t.Fatal(err)
			}
			otelApproval, err := Approve(otelPlan, item.otelTarget, "otel-nonce")
			if err != nil {
				t.Fatal(err)
			}
			afterOtelApply, _, err := SimulateApply(original, otelPlan, otelApproval, auditKey)
			if err != nil {
				t.Fatal(err)
			}
			// Snapshot of the file state immediately before the hook plan is
			// ever applied -- this is the exact byte-identity baseline the
			// rollback must restore for the otel keys and unrelated content.
			beforeHookApply := cloneForTest(t, afterOtelApply)

			hookPlan, err := builders[item.hookTarget]("hookplan", "/private/preview/hook-config", "/private/preview/hook-backup", "/usr/bin/kansoku rollback --target "+item.hookTarget, afterOtelApply)
			if err != nil {
				t.Fatal(err)
			}
			hookApproval, err := Approve(hookPlan, item.hookTarget, "hook-nonce")
			if err != nil {
				t.Fatal(err)
			}
			afterHookApply, _, err := SimulateApply(afterOtelApply, hookPlan, hookApproval, auditKey)
			if err != nil {
				t.Fatal(err)
			}
			otelSpec := targetSpecs[item.otelTarget]
			for key, expected := range otelSpec.required {
				if !equalCanonical(afterHookApply[key], expected) {
					t.Fatalf("hook apply disturbed otel key %s: got %v want %v", key, afterHookApply[key], expected)
				}
			}
			for key, expected := range beforeHookApply {
				if _, isHookKey := targetSpecs[item.hookTarget].required[key]; isHookKey {
					continue
				}
				if !equalCanonical(afterHookApply[key], expected) {
					t.Fatalf("hook apply disturbed unrelated/otel key %s: got %v want %v", key, afterHookApply[key], expected)
				}
			}

			restoredAfterHookRollback, _, err := SimulateRollback(afterHookApply, afterOtelApply, hookPlan, hookApproval, auditKey)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(normalizeMap(t, restoredAfterHookRollback), normalizeMap(t, beforeHookApply)) {
				t.Fatalf("hook rollback did not byte-for-byte restore pre-hook state: got %#v want %#v", restoredAfterHookRollback, beforeHookApply)
			}
			for key, expected := range otelSpec.required {
				if !equalCanonical(restoredAfterHookRollback[key], expected) {
					t.Fatalf("hook rollback disturbed otel key %s: got %v want %v", key, restoredAfterHookRollback[key], expected)
				}
			}
			for key, expected := range original {
				if !equalCanonical(restoredAfterHookRollback[key], expected) {
					t.Fatalf("hook rollback disturbed original unrelated key %s: got %v want %v", key, restoredAfterHookRollback[key], expected)
				}
			}
		})
	}
}

func merge(base, additions map[string]any) map[string]any {
	result := cloneMap(base)
	for key, value := range additions {
		result[key] = value
	}
	return result
}
func cloneForTest(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	encoded, _ := json.Marshal(value)
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func normalizeMap(t *testing.T, value map[string]any) map[string]any { return cloneForTest(t, value) }

// TestPreExistingUserOwnedHeadersYieldADisclosedPlan covers the split between
// keys that must never happen and keys that are simply not Kansoku's to set.
//
// env.OTEL_EXPORTER_OTLP_HEADERS is Claude Code's only header mechanism and the
// loopback OTLP ingress requires a bearer, so the operator has to set it for
// telemetry to be accepted at all. While it was hard-forbidden, the one
// supported configuration could not be previewed: a correctly configured host
// failed the preview it needed to pass. It is now disclosed, never written, and
// never read.
func TestPreExistingUserOwnedHeadersYieldADisclosedPlan(t *testing.T) {
	const headerKey = "env.OTEL_EXPORTER_OTLP_HEADERS"
	plan, err := BuildClaudePlan("plan", "/private/config", "/private/backup", "/bin/rollback",
		map[string]any{headerKey: "Authorization=Bearer operator-owned"})
	if err != nil {
		t.Fatalf("a pre-existing user-owned header key must not fail the preview: %v", err)
	}
	disclosed := false
	for _, tradeoff := range plan.PrivacyTradeoffs {
		if strings.Contains(tradeoff, headerKey) && strings.Contains(tradeoff, "user-owned") {
			disclosed = true
		}
	}
	if !disclosed {
		t.Fatalf("no disclosure named %s: %v", headerKey, plan.PrivacyTradeoffs)
	}
	// The key must never appear as something this plan writes.
	for _, field := range plan.DisclosedFields {
		if field == headerKey {
			t.Fatalf("%s appeared in planned writes", headerKey)
		}
	}
	for _, operation := range plan.ExactOperations {
		if operation.Field == headerKey {
			t.Fatalf("%s appeared as a planned operation", headerKey)
		}
	}
	// The operator's secret value must never be echoed into the plan.
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("operator-owned")) {
		t.Fatal("the operator's header value was echoed into the plan")
	}
	// An absent key is disclosed too: the operator has to know Kansoku will not
	// set it for them.
	absent, err := BuildClaudePlan("plan", "/private/config", "/private/backup", "/bin/rollback", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	stated := false
	for _, tradeoff := range absent.PrivacyTradeoffs {
		if strings.Contains(tradeoff, headerKey) && strings.Contains(tradeoff, "must be set by the operator") {
			stated = true
		}
	}
	if !stated {
		t.Fatalf("absent never-written key not disclosed: %v", absent.PrivacyTradeoffs)
	}

	// Effective-settings verification must accept the operator's header too,
	// or the plan would pass preview and fail the gate immediately after.
	approval, err := Approve(absent, absent.TargetID, "nonce")
	if err != nil {
		t.Fatal(err)
	}
	_ = approval
	effective := map[string]any{headerKey: "Authorization=Bearer operator-owned"}
	for key, value := range targetSpecs["claude.user_otel"].required {
		effective[key] = value
	}
	if err := VerifyEffectiveSettings(absent, EffectiveSettings{
		TargetID: absent.TargetID, Values: effective,
	}, RuntimeCanaryReceipt{
		TargetID: absent.TargetID, ConfigSHA256: absent.PlannedSHA256,
		SourceRevision: "rev", Status: "pass",
	}); err != nil {
		t.Fatalf("effective settings carrying the operator's header were rejected: %v", err)
	}
}
