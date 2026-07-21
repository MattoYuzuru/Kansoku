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
			ID          string         `json:"id"`
			AgentID     string         `json:"agent_id"`
			Format      string         `json:"format"`
			LocatorKind string         `json:"config_locator_kind"`
			Ownership   string         `json:"ownership"`
			Required    map[string]any `json:"required_settings"`
			Forbidden   []string       `json:"forbidden_keys"`
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
		if !ok || actual.agent != expected.AgentID || actual.format != expected.Format || actual.locatorKind != expected.LocatorKind || actual.ownership != expected.Ownership || !reflect.DeepEqual(actual.required, expected.Required) || !reflect.DeepEqual(actual.forbidden, expected.Forbidden) {
			t.Fatalf("runtime target policy drift: %s", expected.ID)
		}
	}
}

type builder func(string, string, string, string, map[string]any) (Plan, error)

var builders = map[string]builder{
	"codex.user_otel": BuildCodexPlan, "claude.user_otel": BuildClaudePlan,
	"gemini.user_otel": BuildGeminiPlan, "cursor.user_hooks": BuildCursorPlan,
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
		{"claude.user_otel", map[string]any{"env.OTEL_EXPORTER_OTLP_HEADERS": "Authorization=secret"}},
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
