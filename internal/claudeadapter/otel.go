package claudeadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
)

const (
	sourceIDOTel       = "claude.otel"
	otelSourceSchemaID = "claude.otel/1"

	// OTelInstallerTargetID is the existing contracts/privacy/installer.yaml
	// target this adapter reuses verbatim for the OTel configuration side.
	// claude.otel declares no second OTel installer target:
	// PlanConfiguration (added in a later stage) must build its ChangePlan
	// against this exact target id, never a new one.
	OTelInstallerTargetID = "claude.user_otel"
)

// OTelEventName is one documented Claude Code OpenTelemetry log/span/metric
// event name from contracts/claude/hooks-and-otel.yaml's
// otel_source.documented_attributes-backed source_event_mapping rows. A
// documented name is never assumed to exist merely because it is documented:
// DocumentedOTelEvents is a closed list this recipe knows about, but
// CanonicalEventForOTel only ever trusts a name after the caller has
// independently matched the event's actual attribute shape against
// ExpectedOTelAttributeFingerprint for that name -- by schema fingerprint,
// never by name alone.
type OTelEventName string

const (
	OTelSessionStarted OTelEventName = "claude_code.session.started"
	OTelUserPrompt     OTelEventName = "claude_code.user_prompt"
	OTelToolResult     OTelEventName = "claude_code.tool_result"
	OTelAPIRequest     OTelEventName = "claude_code.api_request"
)

// DocumentedOTelEvents is the closed, documented Claude Code OTel event
// vocabulary this recipe knows about, mirroring
// contracts/claude/hooks-and-otel.yaml's source_event_mapping
// otlp_log_span_metric rows verbatim.
func DocumentedOTelEvents() []OTelEventName {
	return []OTelEventName{OTelSessionStarted, OTelUserPrompt, OTelToolResult, OTelAPIRequest}
}

// otelEventCanonical is the subset of
// contracts/claude/hooks-and-otel.yaml's source_event_mapping table whose
// source_kind is otlp_log_span_metric.
var otelEventCanonical = map[OTelEventName]string{
	OTelSessionStarted: "session.started",
	OTelUserPrompt:     "prompt.submitted",
	OTelToolResult:     "tool.called",
	OTelAPIRequest:     "model.responded",
}

// OTLPSafeAttributes is the exact, closed OTLP attribute allowlist reused
// verbatim from contracts/observability/ingress.yaml's otlp_safe_attributes;
// claude.otel declares no attribute of its own that bypasses it. Documented
// Claude-native identity/component attributes (skill.name, plugin.name,
// agent.name) are mapped onto the existing kansoku.tool.id-shaped allowlist
// slots by ClaudeComponentAttributeSlot rather than being added as new raw
// passthrough attribute names.
func OTLPSafeAttributes() []string {
	return []string{
		"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.outcome",
		"kansoku.value_state", "kansoku.model.id", "kansoku.tool.id", "kansoku.sequence",
	}
}

// DroppedOTelSurfaces is the exact, closed set of OTLP surfaces
// contracts/claude/hooks-and-otel.yaml's otel_source.dropped_surfaces
// declares kansoku never reads from a Claude Code OTel record: free-form log
// bodies, span names/events/links, metric descriptions, tool payloads,
// output snippets, prompt text, assistant response text and raw API bodies.
// These never reach OTelAttributeShape or any durable path, regardless of
// what env.OTEL_LOG_USER_PROMPTS/env.OTEL_LOG_ASSISTANT_RESPONSES/
// env.OTEL_LOG_TOOL_DETAILS/env.OTEL_LOG_TOOL_CONTENT/
// env.OTEL_LOG_RAW_API_BODIES report upstream -- this is the unconditional
// strip rule, not a flag-conditioned one.
func DroppedOTelSurfaces() []string {
	return []string{
		"log.body", "span.name", "span.events", "span.links",
		"metric.description", "tool_payload", "output_snippet",
		"prompt_text", "assistant_response_text", "raw_api_body",
	}
}

// ClaudeComponentAttribute names one documented Claude-native identity or
// component attribute (skill.name, plugin.name, agent.name) that
// contracts/claude/hooks-and-otel.yaml requires be mapped onto an existing
// OTLPSafeAttributes() slot rather than passed through as a new raw
// attribute name.
type ClaudeComponentAttribute string

const (
	AttributeSkillName  ClaudeComponentAttribute = "skill.name"
	AttributePluginName ClaudeComponentAttribute = "plugin.name"
	AttributeAgentName  ClaudeComponentAttribute = "agent.name"
)

// DocumentedComponentAttributes is the closed, documented identity/component
// attribute vocabulary this recipe knows about, mirroring
// contracts/claude/hooks-and-otel.yaml's
// otel_source.documented_attributes.identity_and_component verbatim.
func DocumentedComponentAttributes() []ClaudeComponentAttribute {
	return []ClaudeComponentAttribute{AttributeSkillName, AttributePluginName, AttributeAgentName}
}

// ComponentAttributeSafeSlot returns the existing OTLPSafeAttributes() slot a
// documented Claude-native component attribute is mapped onto. Every
// documented attribute maps onto kansoku.tool.id, the existing
// component-identity slot; no new raw attribute passthrough is declared for
// skill.name/plugin.name/agent.name.
func ComponentAttributeSafeSlot(attribute ClaudeComponentAttribute) (string, bool) {
	switch attribute {
	case AttributeSkillName, AttributePluginName, AttributeAgentName:
		return "kansoku.tool.id", true
	default:
		return "", false
	}
}

// NativeOTLPAttribute names one real, documented Claude Code OTel activity
// attribute this recipe knows how to translate onto an existing
// OTLPSafeAttributes() slot. These are the actual upstream attribute names
// Claude Code's OTel exporter sends (contracts/claude/hooks-and-otel.yaml's
// otel_source.documented_attributes.activity block, SOURCES.md's Claude Code
// monitoring section, re-checked 2026-07-25) -- never a Kansoku-invented
// name -- and every mapped slot below is already a member of
// OTLPSafeAttributes(); no new raw attribute passthrough is declared.
type NativeOTLPAttribute string

const (
	NativeAttributeSessionID NativeOTLPAttribute = "session.id"
	NativeAttributeModel     NativeOTLPAttribute = "model"
	NativeAttributeToolName  NativeOTLPAttribute = "tool_name"
	NativeAttributeToolState NativeOTLPAttribute = "tool_status"
)

// NativeOTLPAttributeSafeSlot returns the existing OTLPSafeAttributes() slot
// a real, documented Claude-native OTLP activity attribute name maps onto,
// mirroring codexadapter.NativeOTLPAttributeSafeSlot's identical role for
// Codex. tool_status maps onto kansoku.outcome (Claude's own hook helper
// already treats tool_status as the outcome-shaped signal for a completed
// tool call, per internal/claudeadapter/hook.go); it is never treated as a
// second, independent outcome source.
func NativeOTLPAttributeSafeSlot(attribute NativeOTLPAttribute) (string, bool) {
	switch attribute {
	case NativeAttributeSessionID:
		return "kansoku.session.id", true
	case NativeAttributeModel:
		return "kansoku.model.id", true
	case NativeAttributeToolName:
		return "kansoku.tool.id", true
	case NativeAttributeToolState:
		return "kansoku.outcome", true
	default:
		return "", false
	}
}

// OTelAttributeShape is the schema-fingerprint input for one observed Claude
// Code OTel record: only the closed set of already-safe attribute keys that
// were actually present (never their values, and never a dropped surface),
// alongside the record's declared instrumentation-scope name. Two records
// with the same declared event name but a different attribute shape produce
// a different fingerprint, so CanonicalEventForOTel never trusts a bare name
// alone.
type OTelAttributeShape struct {
	InstrumentationScope string
	PresentAttributeKeys []string
}

// ErrOTelEventNotDocumented is returned when the caller's declared event name
// is not even in this recipe's documented vocabulary.
var ErrOTelEventNotDocumented = errors.New("claude_otel_event_not_documented")

// ErrOTelEventNotMapped is returned when the event name is documented but the
// active mapping table declares no canonical event type for it yet. The
// caller must treat this as a degraded-capability signal scoped only to that
// event name, never as evidence the whole claude.otel source is silent.
var ErrOTelEventNotMapped = errors.New("claude_otel_event_not_mapped_in_active_version_manifest")

// ErrOTelSchemaFingerprintMismatch is returned when an event name resolves to
// a canonical mapping but the observed attribute shape does not match the
// expected fingerprint for that mapping. This is the "by schema fingerprint,
// never by name alone" guarantee: a same-named event whose shape drifted
// (for example across a Claude Code release) never silently normalizes as if
// nothing changed.
var ErrOTelSchemaFingerprintMismatch = errors.New("claude_otel_schema_fingerprint_mismatch")

func documentedOTelEvent(name OTelEventName) bool {
	for _, candidate := range DocumentedOTelEvents() {
		if candidate == name {
			return true
		}
	}
	return false
}

// ExpectedOTelAttributeFingerprint returns the closed, expected attribute
// shape fingerprint for one mapped OTel event name. Only attribute keys
// already on OTLPSafeAttributes() ever contribute to the fingerprint; this
// mirrors ingress.yaml's allowlist so a Claude-specific attribute can never
// bypass it by influencing the fingerprint of an otherwise-recognized event.
func ExpectedOTelAttributeFingerprint(name OTelEventName) (string, bool) {
	requiredByEvent := map[OTelEventName][]string{
		OTelSessionStarted: {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelUserPrompt:     {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelToolResult:     {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.tool.id", "kansoku.outcome"},
		OTelAPIRequest:     {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.model.id"},
	}
	required, ok := requiredByEvent[name]
	if !ok {
		return "", false
	}
	return otelShapeFingerprint(string(name), required), true
}

func otelShapeFingerprint(scope string, keys []string) string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	hash := sha256.New()
	hash.Write([]byte("claude-otel-attribute-shape/1"))
	hash.Write([]byte{0})
	hash.Write([]byte(scope))
	for _, key := range sorted {
		hash.Write([]byte{0})
		hash.Write([]byte(key))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func safeObservedFingerprint(shape OTelAttributeShape) string {
	safe := map[string]struct{}{}
	for _, key := range OTLPSafeAttributes() {
		safe[key] = struct{}{}
	}
	filtered := make([]string, 0, len(shape.PresentAttributeKeys))
	for _, key := range shape.PresentAttributeKeys {
		if _, ok := safe[key]; ok {
			filtered = append(filtered, key)
		}
	}
	return otelShapeFingerprint(shape.InstrumentationScope, filtered)
}

// CanonicalEventForOTel maps one observed Claude Code OTel event onto a
// canonical event type strictly by schema fingerprint: the event name must
// be documented, must have an active canonical mapping, and the observed
// attribute shape's fingerprint must match the mapping's expected
// fingerprint exactly. Any failure returns a specific, non-silent error
// scoped to this one event/capability rather than a plausible-looking
// canonical event.
func CanonicalEventForOTel(name OTelEventName, observed OTelAttributeShape) (string, error) {
	if !documentedOTelEvent(name) {
		return "", ErrOTelEventNotDocumented
	}
	canonical, ok := otelEventCanonical[name]
	if !ok {
		return "", ErrOTelEventNotMapped
	}
	expected, ok := ExpectedOTelAttributeFingerprint(name)
	if !ok {
		return "", ErrOTelEventNotMapped
	}
	if safeObservedFingerprint(observed) != expected {
		return "", ErrOTelSchemaFingerprintMismatch
	}
	return canonical, nil
}

// OTLPResourceServiceName is the real, documented OTel resource
// service.name value a locally-installed Claude Code CLI actually emits,
// per contracts/claude/hooks-and-otel.yaml's otel_source.resource_identity
// block and SOURCES.md's Claude Code OTel section (re-checked 2026-07-25):
// Claude Code's own OpenTelemetry exporter stamps its resource
// service.name as "claude-code" for every log/metric it emits. This is
// never a Kansoku-invented literal: it is the real upstream value, distinct
// from the Session 03 "fixture-agent" synthetic identity
// internal/observability's otlp.go already recognizes, and distinct from
// codexadapter.OTLPResourceServiceName.
const OTLPResourceServiceName = "claude-code"

// MatchesOTLPResource reports whether an observed OTLP resource
// service.name value identifies a real, locally-installed Claude Code
// process. Only service.name is checked (never a version, since Claude
// Code's CLI version legitimately changes release to release and otel.go
// must not treat an upgrade as an unrecognized resource) -- matching
// internal/observability/otlp.go's dispatch, which tries the fixture-agent
// literal first and then each registered adapter's own matcher in turn. A
// service.name this function does not recognize must still fall through to
// the existing unknown()/IngestUnknown quarantine path unchanged.
func MatchesOTLPResource(serviceName string) bool {
	return serviceName == OTLPResourceServiceName
}

// ResolveSkillComponent resolves an observed skill.name attribute value
// against a set of known inventory skill identities. A name that matches a
// known inventory entry resolves to that identity's component id; an unknown
// name never becomes arbitrary stored prompt text -- it becomes a scoped
// transient component id instead (still traceable back to the literal
// skill.name value via ScopedTransientComponentID, but explicitly marked as
// not inventory-backed), matching
// contracts/claude/skill-evidence-and-reconciliation.yaml's evidence model:
// an unresolved skill.name is still native OTel evidence, just not yet
// corroborated against inventory.
func ResolveSkillComponent(skillName string, knownInventorySkillIDs map[string]struct{}) (componentID string, inventoryBacked bool) {
	if skillName == "" {
		return "", false
	}
	if _, known := knownInventorySkillIDs[skillName]; known {
		return skillName, true
	}
	return ScopedTransientComponentID(skillName), false
}

// ScopedTransientComponentID derives a stable, scoped identifier for a
// skill.name value that does not match any known inventory entry. It is
// still derived from the literal (already-safe, identity-only) skill.name
// string -- never from prompt or tool content -- so the same unresolved name
// always maps to the same transient component id, but the id itself is
// namespaced so it can never be confused with an inventory-backed identity.
func ScopedTransientComponentID(skillName string) string {
	return "transient-skill:" + skillName
}
