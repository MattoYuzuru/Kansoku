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

// These are the real, short-form values Claude Code stamps on its "event.name"
// attribute (never a "claude_code."-prefixed literal): confirmed both by a
// live OTLP capture and by Anthropic's own monitoring-usage documentation
// (code.claude.com/docs/en/monitoring-usage, re-checked 2026-07-25), which
// documents user_prompt, api_request, api_error, tool_decision, tool_result,
// and feature/version-dependent lifecycle events. Claude Code never emits a session-start OTel log
// event at all (session start is hook-only, via the SessionStart hook); there
// is deliberately no OTelSessionStarted constant here.
const (
	OTelUserPrompt      OTelEventName = "user_prompt"
	OTelAPIRequest      OTelEventName = "api_request"
	OTelAPIError        OTelEventName = "api_error"
	OTelToolDecision    OTelEventName = "tool_decision"
	OTelToolResult      OTelEventName = "tool_result"
	OTelPluginInstalled OTelEventName = "plugin_installed"
	OTelPluginLoaded    OTelEventName = "plugin_loaded"
	OTelSkillActivated  OTelEventName = "skill_activated"
	// OTelHookRegistered and OTelAssistantResponse are emitted by Claude Code
	// 2.1.220 on every session start and on assistant turns respectively.
	// Both were observed on the wire while undeclared here, so each one
	// quarantined as an unsupported adapter event once per session -- standing
	// incident noise that said "this schema drifted" about a shape that had
	// simply never been written down. They are declared as metadata-only
	// source activity: their canonical mapping is source.observed, and no
	// measurement, component identity or content is read from either. In
	// particular assistant_response is *not* mapped onto model.responded --
	// api_request already counts that exact operation, and counting both would
	// double every model response.
	OTelHookRegistered    OTelEventName = "hook_registered"
	OTelAssistantResponse OTelEventName = "assistant_response"
)

// DocumentedOTelEvents is the closed, documented Claude Code OTel event
// vocabulary this recipe knows about, mirroring
// contracts/claude/hooks-and-otel.yaml's source_event_mapping
// otlp_log_span_metric rows verbatim.
func DocumentedOTelEvents() []OTelEventName {
	return []OTelEventName{
		OTelUserPrompt, OTelAPIRequest, OTelAPIError, OTelToolDecision, OTelToolResult,
		OTelPluginInstalled, OTelPluginLoaded, OTelSkillActivated,
		OTelHookRegistered, OTelAssistantResponse,
	}
}

// otelEventCanonical is the subset of
// contracts/claude/hooks-and-otel.yaml's source_event_mapping table whose
// source_kind is otlp_log_span_metric. Tool decisions are intentionally
// absent because tool_result is the single counted tool execution; counting
// both decision and result would double every call.
var otelEventCanonical = map[OTelEventName]string{
	OTelUserPrompt:        "prompt.submitted",
	OTelToolDecision:      "source.observed",
	OTelToolResult:        "tool.called",
	OTelAPIRequest:        "model.responded",
	OTelAPIError:          "model.responded",
	OTelPluginInstalled:   "component.installed",
	OTelPluginLoaded:      "component.loaded",
	OTelSkillActivated:    "component.invoked",
	OTelHookRegistered:    "source.observed",
	OTelAssistantResponse: "source.observed",
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
		"kansoku.component.kind", "kansoku.duration_ms", "kansoku.prompt_length_characters",
		"kansoku.input_tokens", "kansoku.cached_input_tokens", "kansoku.output_tokens", "kansoku.provider_cost_micros",
		"kansoku.turn.id", "kansoku.component.identity",
		"kansoku.component.identity_source", "kansoku.component.owner_plugin",
		"kansoku.component.invocation_mode", "kansoku.component.upstream_identity_hash",
		"kansoku.component.source_scope",
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
	AttributeSkillName         ClaudeComponentAttribute = "skill.name"
	AttributePluginName        ClaudeComponentAttribute = "plugin.name"
	AttributeAgentName         ClaudeComponentAttribute = "agent.name"
	AttributeInvocationTrigger ClaudeComponentAttribute = "invocation_trigger"
	AttributeSkillSource       ClaudeComponentAttribute = "skill.source"
	AttributePluginScope       ClaudeComponentAttribute = "plugin.scope"
	AttributeEnabledVia        ClaudeComponentAttribute = "enabled_via"
	AttributePluginIDHash      ClaudeComponentAttribute = "plugin_id_hash"
)

// DocumentedComponentAttributes is the closed, documented identity/component
// attribute vocabulary this recipe knows about, mirroring
// contracts/claude/hooks-and-otel.yaml's
// otel_source.documented_attributes.identity_and_component verbatim.
func DocumentedComponentAttributes() []ClaudeComponentAttribute {
	return []ClaudeComponentAttribute{
		AttributeSkillName, AttributePluginName, AttributeAgentName,
		AttributeInvocationTrigger, AttributeSkillSource, AttributePluginScope,
		AttributeEnabledVia, AttributePluginIDHash,
	}
}

// ComponentAttributeSafeSlot returns the existing OTLPSafeAttributes() slot a
// documented Claude-native component attribute is mapped onto. Every
// documented attribute maps onto kansoku.tool.id, the existing
// component-identity slot; no new raw attribute passthrough is declared for
// skill.name/plugin.name/agent.name.
func ComponentAttributeSafeSlot(attribute ClaudeComponentAttribute) (string, bool) {
	switch attribute {
	case AttributeSkillName, AttributeAgentName:
		return "kansoku.component.identity", true
	case AttributePluginName:
		return "kansoku.component.owner_plugin", true
	case AttributeInvocationTrigger:
		return "kansoku.component.invocation_mode", true
	case AttributeSkillSource, AttributePluginScope:
		return "kansoku.component.source_scope", true
	case AttributeEnabledVia:
		return "kansoku.component.identity_source", true
	case AttributePluginIDHash:
		return "kansoku.component.upstream_identity_hash", true
	default:
		return "", false
	}
}

// DocumentedSourceScopeValues records, per source-scope-shaped attribute, the
// raw values a locally-installed Claude Code has actually been observed to
// stamp -- taken from the 2.1.220 wire capture in
// reports/artifacts/2026-08-01-component-audit, not from a specification.
//
// This is advisory documentation only. Nothing resolves against it: the data
// platform classifies an observed value against the closed
// adaptersdk.SourceScope vocabulary itself, and a value outside that
// vocabulary widens rather than narrows resolution. The list exists so the
// divergence is written down where the adapter recipe lives -- none of these
// values is a vocabulary member, and every one of them would otherwise look
// like an unexplained mismatch to the next reader.
//
// Claude Code's own words are not Kansoku's: "plugin" describes where the
// skill came from, while inventory records the same plugin-bundled skills at
// "plugin_cache" (where they physically live). The two genuinely mean
// different things, which is exactly why neither is translated into the other.
func DocumentedSourceScopeValues() map[ClaudeComponentAttribute][]string {
	return map[ClaudeComponentAttribute][]string{
		AttributeSkillSource: {"plugin"},
		AttributePluginScope: {"user-local"},
	}
}

// NativeOTLPAttribute names one real, documented Claude Code OTel activity
// attribute this recipe knows how to translate onto an existing
// OTLPSafeAttributes() slot. These are the actual upstream attribute names
// Claude Code's OTel exporter sends -- confirmed by Anthropic's own
// monitoring-usage documentation (code.claude.com/docs/en/monitoring-usage,
// re-checked 2026-07-25), which documents the tool_result event's outcome
// attribute as "success": "true" or "false" (a string-valued attribute, never
// a native OTLP bool) -- never a Kansoku-invented name -- and every mapped
// slot below is already a member of OTLPSafeAttributes(); no new raw
// attribute passthrough is declared.
type NativeOTLPAttribute string

const (
	NativeAttributeSessionID    NativeOTLPAttribute = "session.id"
	NativeAttributeModel        NativeOTLPAttribute = "model"
	NativeAttributeToolName     NativeOTLPAttribute = "tool_name"
	NativeAttributeToolState    NativeOTLPAttribute = "success"
	NativeAttributeSequence     NativeOTLPAttribute = "event.sequence"
	NativeAttributePromptID     NativeOTLPAttribute = "prompt.id"
	NativeAttributeDuration     NativeOTLPAttribute = "duration_ms"
	NativeAttributePromptLength NativeOTLPAttribute = "prompt_length"
	NativeAttributeInputTokens  NativeOTLPAttribute = "input_tokens"
	NativeAttributeOutputTokens NativeOTLPAttribute = "output_tokens"
	NativeAttributeCostMicros   NativeOTLPAttribute = "cost_usd_micros"
)

// NativeOTLPAttributeSafeSlot returns the existing OTLPSafeAttributes() slot
// a real, documented Claude-native OTLP activity attribute name maps onto,
// mirroring codexadapter.NativeOTLPAttributeSafeSlot's identical role for
// Codex. success maps onto kansoku.outcome; it is never treated as a second,
// independent outcome source.
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
	case NativeAttributeSequence:
		return "kansoku.sequence", true
	case NativeAttributePromptID:
		return "kansoku.turn.id", true
	case NativeAttributeDuration:
		return "kansoku.duration_ms", true
	case NativeAttributePromptLength:
		return "kansoku.prompt_length_characters", true
	case NativeAttributeInputTokens:
		return "kansoku.input_tokens", true
	case NativeAttributeOutputTokens:
		return "kansoku.output_tokens", true
	case NativeAttributeCostMicros:
		return "kansoku.provider_cost_micros", true
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
		OTelUserPrompt:      {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.prompt_length_characters"},
		OTelToolDecision:    {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelToolResult:      {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.tool.id", "kansoku.outcome", "kansoku.duration_ms"},
		OTelAPIRequest:      {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.model.id", "kansoku.duration_ms", "kansoku.input_tokens", "kansoku.output_tokens"},
		OTelAPIError:        {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.model.id", "kansoku.duration_ms"},
		OTelPluginInstalled: {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.component.identity", "kansoku.component.kind"},
		OTelPluginLoaded:    {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.component.identity", "kansoku.component.kind"},
		OTelSkillActivated:  {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.component.identity", "kansoku.component.kind"},
		// Metadata-only: identity and event type, never a measurement or a
		// component. Requiring more would quarantine a record that is
		// genuinely shaped this way upstream.
		OTelHookRegistered:    {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelAssistantResponse: {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
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
	required := requiredOTelKeys(name)
	if expected == "" || observed.InstrumentationScope != string(name) || !containsEveryKey(observed.PresentAttributeKeys, required) {
		return "", ErrOTelSchemaFingerprintMismatch
	}
	return canonical, nil
}

func requiredOTelKeys(name OTelEventName) []string {
	requiredByEvent := map[OTelEventName][]string{
		OTelUserPrompt:      {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.prompt_length_characters"},
		OTelToolDecision:    {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelToolResult:      {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.tool.id", "kansoku.outcome", "kansoku.duration_ms"},
		OTelAPIRequest:      {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.model.id", "kansoku.duration_ms", "kansoku.input_tokens", "kansoku.output_tokens"},
		OTelAPIError:        {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.model.id", "kansoku.duration_ms"},
		OTelPluginInstalled: {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.component.identity", "kansoku.component.kind"},
		OTelPluginLoaded:    {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.component.identity", "kansoku.component.kind"},
		OTelSkillActivated:  {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.component.identity", "kansoku.component.kind"},
		// Metadata-only: identity and event type, never a measurement or a
		// component. Requiring more would quarantine a record that is
		// genuinely shaped this way upstream.
		OTelHookRegistered:    {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelAssistantResponse: {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
	}
	return requiredByEvent[name]
}

func containsEveryKey(observed, required []string) bool {
	present := map[string]bool{}
	for _, key := range observed {
		present[key] = true
	}
	for _, key := range required {
		if !present[key] {
			return false
		}
	}
	return true
}

// OTLPResourceServiceName is the real, documented OTel resource
// service.name value a locally-installed Claude Code CLI actually emits,
// per contracts/claude/hooks-and-otel.yaml's otel_source.resource_identity
// block and SOURCES.md's Claude Code OTel section (re-checked 2026-07-25):
// Claude Code's own OpenTelemetry exporter stamps its resource
// service.name as "claude-code" for every log/metric a terminal session
// emits. This is never a Kansoku-invented literal: it is the real upstream
// value, distinct from the Session 03 "fixture-agent" synthetic identity
// internal/observability's otlp.go already recognizes, and distinct from
// codexadapter.OTLPResourceServiceName. Kept as the exported primary
// constant for backward compatibility; use MatchesOTLPResource (or
// OTLPResourceServiceNames) rather than comparing against this single
// literal, since real Claude Code has more than one surface with its own
// service.name -- see OTLPResourceServiceNames below.
const OTLPResourceServiceName = "claude-code"

// OTLPResourceServiceNames is every real, upstream-confirmed OTel resource
// service.name value a locally-installed Claude Code product can emit, per
// Anthropic's own monitoring-usage documentation
// (code.claude.com/docs/en/monitoring-usage, re-checked 2026-07-25):
//   - "claude-code"         -- terminal/CLI sessions.
//   - "claude-code-desktop" -- Claude Desktop's Code tab sessions.
var OTLPResourceServiceNames = []string{
	OTLPResourceServiceName,
	"claude-code-desktop",
}

// MatchesOTLPResource reports whether an observed OTLP resource
// service.name value identifies a real, locally-installed Claude Code
// process, from any recognized surface (see OTLPResourceServiceNames). Only
// service.name is checked (never a version, since Claude Code's version
// legitimately changes release to release and otel.go must not treat an
// upgrade as an unrecognized resource) -- matching
// internal/observability/otlp.go's dispatch, which tries the fixture-agent
// literal first and then each registered adapter's own matcher in turn. A
// service.name this function does not recognize must still fall through to
// the existing unknown()/IngestUnknown quarantine path unchanged.
func MatchesOTLPResource(serviceName string) bool {
	for _, known := range OTLPResourceServiceNames {
		if serviceName == known {
			return true
		}
	}
	return false
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
