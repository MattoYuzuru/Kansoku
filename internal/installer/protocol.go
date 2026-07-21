package installer

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxConfigEntries                = 256
	maxConfigDepth                  = 8
	maxConfigString                 = 4096
	InstallerContractSemanticSHA256 = "219e7f4c72ffc67c2ea764ee85da56dda68c2d0ff25afeb99d3f57c4d37cf3d2"
)

type Operation struct {
	Action     string `json:"action"`
	Field      string `json:"field"`
	OldPreview any    `json:"old_preview"`
	NewPreview any    `json:"new_preview"`
	Disclosure string `json:"disclosure"`
}

type Plan struct {
	PlanID            string      `json:"plan_id"`
	PlanVersion       string      `json:"plan_version"`
	TargetID          string      `json:"target_id"`
	AgentID           string      `json:"agent_id"`
	ConfigLocator     string      `json:"config_locator"`
	ConfigLocatorKind string      `json:"config_locator_kind"`
	ConfigFormat      string      `json:"config_format"`
	Ownership         string      `json:"ownership"`
	DisclosedFields   []string    `json:"disclosed_fields"`
	ExactOperations   []Operation `json:"exact_operations"`
	OriginalSHA256    string      `json:"original_sha256"`
	PlannedSHA256     string      `json:"planned_sha256"`
	BackupLocator     string      `json:"backup_locator"`
	RollbackCommand   string      `json:"rollback_command"`
	PrivacyTradeoffs  []string    `json:"privacy_tradeoffs"`
	plannedCanonical  []byte
	originalCanonical []byte
}

type Approval struct {
	PlanSHA256     string `json:"plan_sha256"`
	TargetID       string `json:"target_id"`
	OriginalSHA256 string `json:"original_sha256"`
	PlannedSHA256  string `json:"planned_sha256"`
	ApprovalNonce  string `json:"approval_nonce"`
}

type EffectiveSettings struct {
	TargetID          string
	Values            map[string]any
	BlockingOverrides []string
}

type RuntimeCanaryReceipt struct {
	TargetID       string
	ConfigSHA256   string
	SourceRevision string
	Status         string
}

type AuditReceipt struct {
	PlanSHA256          string `json:"plan_sha256"`
	TargetID            string `json:"target_id"`
	AgentID             string `json:"agent_id"`
	ConfigPathPseudonym string `json:"config_path_pseudonym"`
	Operation           string `json:"operation"`
	Result              string `json:"result"`
	BeforeSHA256        string `json:"before_sha256"`
	AfterSHA256         string `json:"after_sha256"`
}

type targetSpec struct {
	agent, format, locatorKind, ownership string
	required                              map[string]any
	forbidden                             []string
}

var targetSpecs = map[string]targetSpec{
	"codex.user_otel": {
		agent: "codex", format: "toml", locatorKind: "codex_user_config", ownership: "plan_owned_otel_keys_only",
		required:  map[string]any{"otel.environment": "local", "otel.exporter": "otlp-http", "otel.endpoint": "http://127.0.0.1:4318", "otel.log_user_prompt": false},
		forbidden: []string{"authorization", "headers", "project_local_otel", "remote_endpoint"},
	},
	"claude.user_otel": {
		agent: "claude", format: "json", locatorKind: "claude_user_settings", ownership: "plan_owned_env_keys_only",
		required:  map[string]any{"env.CLAUDE_CODE_ENABLE_TELEMETRY": "1", "env.OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318", "env.OTEL_LOG_USER_PROMPTS": "0", "env.OTEL_LOG_ASSISTANT_RESPONSES": "0", "env.OTEL_LOG_TOOL_DETAILS": "0", "env.OTEL_LOG_TOOL_CONTENT": "0", "env.OTEL_LOG_RAW_API_BODIES": "0"},
		forbidden: []string{"env.OTEL_EXPORTER_OTLP_HEADERS", "env.OTEL_LOGS_EXPORTER_FILE", "remote_endpoint"},
	},
	"gemini.user_otel": {
		agent: "gemini", format: "json", locatorKind: "gemini_user_settings", ownership: "plan_owned_telemetry_keys_only",
		required:  map[string]any{"telemetry.enabled": true, "telemetry.target": "local", "telemetry.otlpEndpoint": "http://127.0.0.1:4318", "telemetry.logPrompts": false, "telemetry.useCliAuth": false},
		forbidden: []string{"telemetry.outfile", "telemetry.target=gcp", "telemetry.useCliAuth=true", "remote_endpoint"},
	},
	"cursor.user_hooks": {
		agent: "cursor", format: "json", locatorKind: "cursor_user_hooks", ownership: "one_exact_plan_owned_hook",
		required:  map[string]any{"hook.command": "kansoku hook --endpoint http://127.0.0.1:4318 --strict-privacy", "hook.role": "collection_only", "hook.privacy_boundary": "loopback_sanitizer", "hook.raw_persistence": false},
		forbidden: []string{"remote_command", "raw_payload_log", "hook_as_privacy_enforcement", "credential_forwarding"},
	},
}

func BuildCodexPlan(planID, locator, backup, rollback string, original map[string]any) (Plan, error) {
	return buildTargetPlan(planID, "codex.user_otel", locator, backup, rollback, original)
}
func BuildClaudePlan(planID, locator, backup, rollback string, original map[string]any) (Plan, error) {
	return buildTargetPlan(planID, "claude.user_otel", locator, backup, rollback, original)
}
func BuildGeminiPlan(planID, locator, backup, rollback string, original map[string]any) (Plan, error) {
	return buildTargetPlan(planID, "gemini.user_otel", locator, backup, rollback, original)
}
func BuildCursorPlan(planID, locator, backup, rollback string, original map[string]any) (Plan, error) {
	return buildTargetPlan(planID, "cursor.user_hooks", locator, backup, rollback, original)
}

func buildTargetPlan(planID, targetID, locator, backupLocator, rollbackCommand string, original map[string]any) (Plan, error) {
	spec, ok := targetSpecs[targetID]
	if !ok || planID == "" || !validTransientLocator(locator) || !validTransientLocator(backupLocator) || rollbackCommand == "" {
		return Plan{}, errors.New("incomplete_preview")
	}
	if err := validateConfig(original, 1); err != nil {
		return Plan{}, errors.New("invalid_original_config")
	}
	planned := cloneMap(original)
	for _, forbidden := range spec.forbidden {
		if containsForbidden(planned, forbidden) {
			return Plan{}, errors.New("unsafe_existing_target_setting")
		}
	}
	fields := sortedKeys(spec.required)
	operations := make([]Operation, 0, len(fields))
	for _, field := range fields {
		old, present := planned[field]
		planned[field] = spec.required[field]
		action := "set"
		if !present {
			old = nil
		}
		operations = append(operations, Operation{Action: action, Field: field, OldPreview: old, NewPreview: spec.required[field], Disclosure: disclosureFor(field)})
	}
	originalBytes, err := canonicalMap(original)
	if err != nil {
		return Plan{}, errors.New("invalid_original_config")
	}
	plannedBytes, err := canonicalMap(planned)
	if err != nil {
		return Plan{}, errors.New("invalid_planned_config")
	}
	return Plan{
		PlanID: planID, PlanVersion: "kansoku.installer-plan/2", TargetID: targetID, AgentID: spec.agent,
		ConfigLocator: locator, ConfigLocatorKind: spec.locatorKind, ConfigFormat: spec.format, Ownership: spec.ownership,
		DisclosedFields: fields, ExactOperations: operations, OriginalSHA256: digest(originalBytes), PlannedSHA256: digest(plannedBytes),
		BackupLocator: backupLocator, RollbackCommand: rollbackCommand,
		PrivacyTradeoffs: []string{"agent payloads remain untrusted and are sanitized at loopback ingress", "managed or environment precedence can block application"},
		plannedCanonical: plannedBytes, originalCanonical: originalBytes,
	}, nil
}

func PlanSHA256(plan Plan) (string, error) {
	public := plan
	public.plannedCanonical, public.originalCanonical = nil, nil
	encoded, err := json.Marshal(public)
	if err != nil {
		return "", err
	}
	return digest(encoded), nil
}

func Approve(plan Plan, targetID, nonce string) (Approval, error) {
	if targetID != plan.TargetID || nonce == "" {
		return Approval{}, errors.New("target_specific_consent_required")
	}
	planHash, err := PlanSHA256(plan)
	if err != nil {
		return Approval{}, err
	}
	return Approval{PlanSHA256: planHash, TargetID: targetID, OriginalSHA256: plan.OriginalSHA256, PlannedSHA256: plan.PlannedSHA256, ApprovalNonce: nonce}, nil
}

func SimulateApply(current map[string]any, plan Plan, approval Approval, auditKey []byte) (map[string]any, AuditReceipt, error) {
	if len(auditKey) < 32 {
		return nil, AuditReceipt{}, errors.New("invalid_audit_key")
	}
	if err := validateApproval(plan, approval); err != nil {
		return nil, AuditReceipt{}, err
	}
	currentBytes, err := canonicalMap(current)
	if err != nil || digest(currentBytes) != plan.OriginalSHA256 {
		return nil, AuditReceipt{}, errors.New("config_race")
	}
	if len(plan.plannedCanonical) == 0 || digest(plan.plannedCanonical) != plan.PlannedSHA256 {
		return nil, AuditReceipt{}, errors.New("plan_payload_mismatch")
	}
	applied, err := decodeCanonicalMap(plan.plannedCanonical)
	if err != nil {
		return nil, AuditReceipt{}, errors.New("plan_payload_invalid")
	}
	return applied, auditReceipt(plan, auditKey, "apply", "pass", plan.OriginalSHA256, plan.PlannedSHA256), nil
}

func VerifyEffectiveSettings(plan Plan, effective EffectiveSettings, canary RuntimeCanaryReceipt) error {
	spec, ok := targetSpecs[plan.TargetID]
	if !ok || effective.TargetID != plan.TargetID || canary.TargetID != plan.TargetID || canary.ConfigSHA256 != plan.PlannedSHA256 || canary.SourceRevision == "" || canary.Status != "pass" {
		return errors.New("runtime_canary_required")
	}
	if len(effective.BlockingOverrides) != 0 {
		return errors.New("managed_or_environment_override")
	}
	if err := validateConfig(effective.Values, 1); err != nil {
		return errors.New("invalid_effective_settings")
	}
	for key, expected := range spec.required {
		if actual, exists := effective.Values[key]; !exists || !equalCanonical(actual, expected) {
			return errors.New("effective_settings_mismatch")
		}
	}
	for _, forbidden := range spec.forbidden {
		if containsForbidden(effective.Values, forbidden) {
			return errors.New("unsafe_effective_setting")
		}
	}
	return nil
}

func AuthorizeRealWrite(Plan, Approval, EffectiveSettings, RuntimeCanaryReceipt) error {
	return errors.New("real_agent_config_write_not_implemented_session_02")
}

func SimulateRollback(current, original map[string]any, plan Plan, approval Approval, auditKey []byte) (map[string]any, AuditReceipt, error) {
	return simulateRestore("rollback", current, original, plan, approval, auditKey)
}

func SimulateRemove(current, original map[string]any, plan Plan, approval Approval, auditKey []byte) (map[string]any, AuditReceipt, error) {
	return simulateRestore("remove", current, original, plan, approval, auditKey)
}

func simulateRestore(operation string, current, original map[string]any, plan Plan, approval Approval, auditKey []byte) (map[string]any, AuditReceipt, error) {
	if len(auditKey) < 32 {
		return nil, AuditReceipt{}, errors.New("invalid_audit_key")
	}
	if err := validateApproval(plan, approval); err != nil {
		return nil, AuditReceipt{}, err
	}
	currentBytes, err := canonicalMap(current)
	if err != nil || digest(currentBytes) != plan.PlannedSHA256 {
		return nil, AuditReceipt{}, errors.New("config_race")
	}
	originalBytes, err := canonicalMap(original)
	if err != nil || digest(originalBytes) != plan.OriginalSHA256 {
		return nil, AuditReceipt{}, errors.New("backup_revision_mismatch")
	}
	restored, err := decodeCanonicalMap(originalBytes)
	if err != nil {
		return nil, AuditReceipt{}, errors.New("backup_payload_invalid")
	}
	return restored, auditReceipt(plan, auditKey, operation, "pass", plan.PlannedSHA256, plan.OriginalSHA256), nil
}

func RenderPreview(plan Plan) ([]byte, error) {
	public := plan
	public.plannedCanonical, public.originalCanonical = nil, nil
	return json.MarshalIndent(public, "", "  ")
}

func validateApproval(plan Plan, approval Approval) error {
	planHash, err := PlanSHA256(plan)
	if err != nil {
		return err
	}
	if approval.TargetID != plan.TargetID || approval.PlanSHA256 != planHash || approval.OriginalSHA256 != plan.OriginalSHA256 || approval.PlannedSHA256 != plan.PlannedSHA256 || approval.ApprovalNonce == "" {
		return errors.New("approval_binding_mismatch")
	}
	return nil
}

func auditReceipt(plan Plan, auditKey []byte, operation, result, before, after string) AuditReceipt {
	mac := hmac.New(sha256.New, auditKey)
	_, _ = mac.Write([]byte("installer-config-path/1\x00" + plan.ConfigLocator))
	planHash, _ := PlanSHA256(plan)
	return AuditReceipt{PlanSHA256: planHash, TargetID: plan.TargetID, AgentID: plan.AgentID, ConfigPathPseudonym: "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil)), Operation: operation, Result: result, BeforeSHA256: before, AfterSHA256: after}
}

func validTransientLocator(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsRune(value, '\x00')
}

func disclosureFor(field string) string {
	if strings.Contains(strings.ToLower(field), "prompt") || strings.Contains(strings.ToLower(field), "tool") || strings.Contains(strings.ToLower(field), "raw") {
		return "explicitly disables raw content exposure"
	}
	return "enables only loopback metadata collection"
}

func containsForbidden(values map[string]any, forbidden string) bool {
	if strings.Contains(forbidden, "=") {
		parts := strings.SplitN(forbidden, "=", 2)
		return strings.EqualFold(stringValue(values[parts[0]]), parts[1])
	}
	for key, value := range values {
		lower := strings.ToLower(key)
		if strings.Contains(lower, strings.ToLower(forbidden)) || strings.Contains(strings.ToLower(stringValue(value)), strings.ToLower(forbidden)) {
			return true
		}
		if strings.Contains(lower, "endpoint") {
			if endpoint, ok := value.(string); ok && !canonicalLoopbackEndpoint(endpoint) {
				return true
			}
		}
	}
	return false
}

func canonicalLoopbackEndpoint(value string) bool {
	return value == "http://127.0.0.1:4318" || value == "http://[::1]:4318"
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
}

func validateConfig(value any, depth int) error {
	if depth > maxConfigDepth {
		return errors.New("config_depth")
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > maxConfigEntries {
			return errors.New("config_entries")
		}
		for key, item := range typed {
			if key == "" || len(key) > maxConfigString || strings.ContainsAny(key, "\x00\r\n") {
				return errors.New("config_key")
			}
			if err := validateConfig(item, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if len(typed) > maxConfigEntries {
			return errors.New("config_entries")
		}
		for _, item := range typed {
			if err := validateConfig(item, depth+1); err != nil {
				return err
			}
		}
	case string:
		if len(typed) > maxConfigString {
			return errors.New("config_string")
		}
	case bool, nil, json.Number, float64, int, int64:
	default:
		return errors.New("config_type")
	}
	return nil
}

func cloneMap(value map[string]any) map[string]any {
	encoded, _ := canonicalMap(value)
	result, _ := decodeCanonicalMap(encoded)
	return result
}

func sortedKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func equalCanonical(left, right any) bool {
	a, errA := marshalCanonical(left)
	b, errB := marshalCanonical(right)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

func canonicalMap(value map[string]any) ([]byte, error) {
	if err := validateConfig(value, 1); err != nil {
		return nil, err
	}
	return marshalCanonical(value)
}

func marshalCanonical(value any) ([]byte, error) {
	switch typed := value.(type) {
	case map[string]any:
		keys := sortedKeys(typed)
		result := []byte{'{'}
		for index, key := range keys {
			if index > 0 {
				result = append(result, ',')
			}
			keyBytes, _ := json.Marshal(key)
			result = append(result, keyBytes...)
			result = append(result, ':')
			itemBytes, err := marshalCanonical(typed[key])
			if err != nil {
				return nil, err
			}
			result = append(result, itemBytes...)
		}
		return append(result, '}'), nil
	case []any:
		result := []byte{'['}
		for index, item := range typed {
			if index > 0 {
				result = append(result, ',')
			}
			itemBytes, err := marshalCanonical(item)
			if err != nil {
				return nil, err
			}
			result = append(result, itemBytes...)
		}
		return append(result, ']'), nil
	case string, bool, nil, float64, int, int64, json.Number:
		return json.Marshal(typed)
	default:
		return nil, errors.New("unsupported_config_value")
	}
}

func decodeCanonicalMap(encoded []byte) (map[string]any, error) {
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func digest(value []byte) string { hash := sha256.Sum256(value); return hex.EncodeToString(hash[:]) }
