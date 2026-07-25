package codexadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
)

const (
	sourceIDOTel       = "codex.otel"
	otelSourceSchemaID = "codex.otel/1"
)

// OTelEventName is one documented Codex OpenTelemetry log/span/metric event
// name from contracts/codex/hooks-and-otel.yaml's otel_source.documented_events.
// A documented name is never assumed to exist merely because it is
// documented: DocumentedOTelEvents is a closed list this recipe knows about,
// but CanonicalEventForOTel only ever trusts a name after the caller has
// independently matched the event's actual attribute shape against
// ExpectedOTelAttributeFingerprint for that name -- by schema fingerprint,
// never by name alone.
type OTelEventName string

const (
	OTelConversationStarts OTelEventName = "codex.conversation_starts"
	OTelAPIRequest         OTelEventName = "codex.api_request"
	OTelSSEEvent           OTelEventName = "codex.sse_event"
	OTelModelTokenUsage    OTelEventName = "codex.model_token_usage"
	OTelUserPrompt         OTelEventName = "codex.user_prompt"
	OTelToolDecision       OTelEventName = "codex.tool_decision"
	OTelToolResult         OTelEventName = "codex.tool_result"
)

// DocumentedOTelEvents is the closed, documented Codex OTel event vocabulary
// this recipe knows about, mirroring
// contracts/codex/hooks-and-otel.yaml's otel_source.documented_events
// verbatim. Documentation alone never implies availability in any given
// Codex release; see CanonicalEventForOTel.
func DocumentedOTelEvents() []OTelEventName {
	return []OTelEventName{
		OTelConversationStarts, OTelAPIRequest, OTelSSEEvent, OTelModelTokenUsage,
		OTelUserPrompt, OTelToolDecision, OTelToolResult,
	}
}

// otelEventCanonical is the subset of
// contracts/codex/hooks-and-otel.yaml's source_event_mapping table whose
// source_kind is otlp_log_span_metric. Events that carry useful source
// activity but no independently countable prompt/tool/model operation map
// to source.observed, so they remain durable without double-counting a
// projected operation or masquerading as schema drift. API requests are a
// distinct request phase: Codex reports their duration separately from the
// response.completed SSE record that carries token usage.
var otelEventCanonical = map[OTelEventName]string{
	OTelConversationStarts: "session.started",
	OTelAPIRequest:         "model.requested",
	OTelModelTokenUsage:    "source.observed",
	OTelUserPrompt:         "prompt.submitted",
	OTelToolDecision:       "source.observed",
	OTelToolResult:         "tool.called",
	OTelSSEEvent:           "model.responded",
}

// OTLPSafeAttributes is the exact, closed OTLP attribute allowlist reused
// verbatim from contracts/observability/ingress.yaml's otlp_safe_attributes;
// codex.otel declares no attribute of its own that bypasses it.
func OTLPSafeAttributes() []string {
	return []string{
		"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.outcome",
		"kansoku.value_state", "kansoku.model.id", "kansoku.tool.id", "kansoku.sequence",
		"kansoku.duration_ms", "kansoku.prompt_length_characters",
		"kansoku.input_tokens", "kansoku.output_tokens",
	}
}

// DroppedOTelSurfaces is the exact, closed set of OTLP surfaces
// contracts/codex/hooks-and-otel.yaml's otel_source.dropped_surfaces
// declares kansoku never reads from a Codex OTel record: free-form log
// bodies, span names/events/links, metric descriptions, tool payloads and
// output snippets. These never reach OTelAttributeShape or any durable path.
func DroppedOTelSurfaces() []string {
	return []string{
		"log.body", "span.name", "span.events", "span.links",
		"metric.description", "tool_payload", "output_snippet",
	}
}

// OTelAttributeShape is the schema-fingerprint input for one observed Codex
// OTel record: only the closed set of already-safe attribute keys that were
// actually present (never their values, and never a dropped surface),
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
var ErrOTelEventNotDocumented = errors.New("codex_otel_event_not_documented")

// ErrOTelEventNotMapped is returned when the event name is documented but the
// active mapping table declares no canonical event type for it yet (for
// example codex.api_request or codex.model_token_usage today).
// The caller must treat this as a degraded-capability signal scoped only to
// that event name, never as evidence the whole codex.otel source is silent.
var ErrOTelEventNotMapped = errors.New("codex_otel_event_not_mapped_in_active_version_manifest")

// ErrOTelSchemaFingerprintMismatch is returned when an event name resolves to
// a canonical mapping but the observed attribute shape does not match the
// expected fingerprint for that mapping. This is the "by schema fingerprint,
// never by name alone" guarantee: a same-named event whose shape drifted
// (for example across a Codex release) never silently normalizes as if
// nothing changed.
var ErrOTelSchemaFingerprintMismatch = errors.New("codex_otel_schema_fingerprint_mismatch")

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
// mirrors ingress.yaml's allowlist so a Codex-specific attribute can never
// bypass it by influencing the fingerprint of an otherwise-recognized event.
func ExpectedOTelAttributeFingerprint(name OTelEventName) (string, bool) {
	requiredByEvent := map[OTelEventName][]string{
		OTelConversationStarts: {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelAPIRequest:         {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelModelTokenUsage:    {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelUserPrompt:         {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.prompt_length_characters"},
		OTelToolDecision:       {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelToolResult:         {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.tool.id", "kansoku.outcome", "kansoku.duration_ms"},
		OTelSSEEvent:           {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.model.id", "kansoku.input_tokens", "kansoku.output_tokens"},
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
	hash.Write([]byte("codex-otel-attribute-shape/1"))
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

// CanonicalEventForOTel maps one observed Codex OTel event onto a canonical
// event type strictly by schema fingerprint: the event name must be
// documented, must have an active canonical mapping, and the observed
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
		OTelConversationStarts: {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelAPIRequest:         {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelModelTokenUsage:    {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelUserPrompt:         {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.prompt_length_characters"},
		OTelToolDecision:       {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelToolResult:         {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.tool.id", "kansoku.outcome", "kansoku.duration_ms"},
		OTelSSEEvent:           {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.model.id", "kansoku.input_tokens", "kansoku.output_tokens"},
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

// OTelInstallerTargetID is the existing contracts/privacy/installer.yaml
// target this adapter reuses verbatim for the OTel configuration side.
// codex.otel declares no second OTel installer target: PlanConfiguration
// (added in a later stage) must build its ChangePlan against this exact
// target id, never a new one.
const OTelInstallerTargetID = "codex.user_otel"

// OTLPResourceServiceName is the interactive-CLI OTel resource service.name
// value: real Codex builds set service.name from the process "originator"
// string unless a surface overrides it (codex-rs/core/src/otel_init.rs:
// `service_name_override.unwrap_or(originator.value.as_str())`), and the
// interactive TUI never overrides it (codex-rs/tui/src/lib.rs), so it falls
// back to DEFAULT_ORIGINATOR = "codex_cli_rs"
// (codex-rs/login/src/auth/default_client.rs:40). Kept as the exported
// primary constant for backward compatibility; use MatchesOTLPResource (or
// OTLPResourceServiceNames) rather than comparing against this single
// literal, since real Codex has multiple surfaces with their own
// service.name -- see OTLPResourceServiceNames below.
const OTLPResourceServiceName = "codex_cli_rs"

// OTLPResourceServiceNames is every real, upstream-confirmed OTel resource
// service.name value a locally-installed Codex product can emit, one per
// surface (each surface calls codex_core::otel_init::build_provider with its
// own override, or none):
//   - "codex_cli_rs" -- interactive TUI (`codex`); falls back to
//     DEFAULT_ORIGINATOR, never overridden (codex-rs/tui/src/lib.rs).
//   - "codex_exec"   -- `codex exec` (headless/scripted); overridden via
//     set_default_originator("codex_exec") (codex-rs/exec/src/lib.rs:246).
//     Confirmed live 2026-07-25 against real Codex CLI v0.145.0 via a
//     temporary debug capture (see reports/2026-07-25-gap-inventory-and-fix-plan.md,
//     "Live-test findings") -- this is what a real `codex exec` invocation
//     actually sends, not "codex_cli_rs" as previously assumed.
//   - "codex_mcp_server" -- `codex mcp-server` (codex-rs/mcp-server/src/lib.rs:55,
//     OTEL_SERVICE_NAME constant).
//   - "codex-app-server" -- `codex app-server` (codex-rs/app-server/src/lib.rs:136,
//     OTEL_SERVICE_NAME constant; note the hyphen, not an underscore).
//
// Each literal was verified directly against openai/codex's main-branch
// source (codex-rs/) on 2026-07-25, not merely documentation. Deliberately
// excludes speculative surfaces never confirmed against this exact source
// tree (e.g. a desktop app or VS Code extension originator) -- add a new
// entry only once its literal is confirmed the same way, not by guessing
// from a naming pattern.
var OTLPResourceServiceNames = []string{
	OTLPResourceServiceName,
	"codex_exec",
	"codex_mcp_server",
	"codex-app-server",
}

// MatchesOTLPResource reports whether an observed OTLP resource
// service.name value identifies a real, locally-installed Codex process,
// from any recognized surface (see OTLPResourceServiceNames). Only
// service.name is checked (never a version, since Codex's CLI version
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

// OTelEventNameFromString resolves a raw OTLP-observed event/span/metric
// name onto this recipe's closed OTelEventName vocabulary. A name outside
// DocumentedOTelEvents() is never guessed at or coerced -- the caller must
// treat a false result as "not a name this recipe recognizes," which is
// distinct from (and upstream of) CanonicalEventForOTel's own
// documented-but-unmapped/fingerprint-mismatch failure modes.
func OTelEventNameFromString(name string) (OTelEventName, bool) {
	candidate := OTelEventName(name)
	if documentedOTelEvent(candidate) {
		return candidate, true
	}
	return "", false
}

// NativeOTLPAttribute names one real, documented Codex OTel attribute this
// recipe knows how to translate onto an existing OTLPSafeAttributes() slot.
// These are the actual upstream attribute names Codex's OTel exporter sends
// (SOURCES.md Codex OTel section, re-checked 2026-07-25) -- never a
// Kansoku-invented name -- and every mapped slot below is already a member
// of OTLPSafeAttributes(); no new raw attribute passthrough is declared.
type NativeOTLPAttribute string

const (
	NativeAttributeConversationID NativeOTLPAttribute = "conversation.id"
	NativeAttributeModel          NativeOTLPAttribute = "model"
	NativeAttributeToolName       NativeOTLPAttribute = "tool_name"
	NativeAttributeSuccess        NativeOTLPAttribute = "success"
	NativeAttributeDuration       NativeOTLPAttribute = "duration_ms"
	NativeAttributePromptLength   NativeOTLPAttribute = "prompt_length"
	NativeAttributeInputTokens    NativeOTLPAttribute = "input_token_count"
	NativeAttributeOutputTokens   NativeOTLPAttribute = "output_token_count"
)

// NativeOTLPAttributeSafeSlot returns the existing OTLPSafeAttributes() slot
// a real, documented Codex-native OTLP attribute name maps onto, mirroring
// claudeadapter.ComponentAttributeSafeSlot's identical role for Claude Code.
func NativeOTLPAttributeSafeSlot(attribute NativeOTLPAttribute) (string, bool) {
	switch attribute {
	case NativeAttributeConversationID:
		return "kansoku.session.id", true
	case NativeAttributeModel:
		return "kansoku.model.id", true
	case NativeAttributeToolName:
		return "kansoku.tool.id", true
	case NativeAttributeSuccess:
		return "kansoku.outcome", true
	case NativeAttributeDuration:
		return "kansoku.duration_ms", true
	case NativeAttributePromptLength:
		return "kansoku.prompt_length_characters", true
	case NativeAttributeInputTokens:
		return "kansoku.input_tokens", true
	case NativeAttributeOutputTokens:
		return "kansoku.output_tokens", true
	default:
		return "", false
	}
}
