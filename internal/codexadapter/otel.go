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
// source_kind is otlp_log_span_metric. codex.api_request, codex.sse_event and
// codex.model_token_usage are documented but intentionally absent here: the
// registry does not yet map them onto any canonical event type, so an
// unmapped documented name must never fall through to a plausible-looking
// default -- it degrades only its own, still-unmapped capability.
var otelEventCanonical = map[OTelEventName]string{
	OTelConversationStarts: "session.started",
	OTelUserPrompt:         "prompt.submitted",
	OTelToolDecision:       "tool.called",
	OTelToolResult:         "tool.called",
}

// OTLPSafeAttributes is the exact, closed OTLP attribute allowlist reused
// verbatim from contracts/observability/ingress.yaml's otlp_safe_attributes;
// codex.otel declares no attribute of its own that bypasses it.
func OTLPSafeAttributes() []string {
	return []string{
		"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.outcome",
		"kansoku.value_state", "kansoku.model.id", "kansoku.tool.id", "kansoku.sequence",
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
// example codex.api_request, codex.sse_event, codex.model_token_usage today).
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
		OTelUserPrompt:         {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
		OTelToolDecision:       {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.tool.id"},
		OTelToolResult:         {"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.tool.id", "kansoku.outcome"},
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
	if safeObservedFingerprint(observed) != expected {
		return "", ErrOTelSchemaFingerprintMismatch
	}
	return canonical, nil
}

// OTelInstallerTargetID is the existing contracts/privacy/installer.yaml
// target this adapter reuses verbatim for the OTel configuration side.
// codex.otel declares no second OTel installer target: PlanConfiguration
// (added in a later stage) must build its ChangePlan against this exact
// target id, never a new one.
const OTelInstallerTargetID = "codex.user_otel"

// OTLPResourceServiceName is the real, documented OTel resource
// service.name value a locally-installed Codex CLI/app-server actually
// emits, per contracts/codex/hooks-and-otel.yaml's otel_source.
// resource_identity block and SOURCES.md's Codex OTel section (re-checked
// 2026-07-25): Codex's own built-in resource attributes are exactly
// service.name/service.version/env (and host.name for logs); service.name
// carries the originator string, which is "codex_cli_rs" for the
// interactive CLI (the only entry point contracts/codex/hooks-and-otel.yaml
// documents kansoku.otel targets today). This is never a Kansoku-invented
// literal: it is the real upstream value, distinct from the Session 03
// "fixture-agent" synthetic identity internal/observability's otlp.go
// already recognizes.
const OTLPResourceServiceName = "codex_cli_rs"

// MatchesOTLPResource reports whether an observed OTLP resource
// service.name value identifies a real, locally-installed Codex process.
// Only service.name is checked (never a version, since Codex's CLI version
// legitimately changes release to release and otel.go must not treat an
// upgrade as an unrecognized resource) -- matching
// internal/observability/otlp.go's dispatch, which tries the fixture-agent
// literal first and then each registered adapter's own matcher in turn. A
// service.name this function does not recognize must still fall through to
// the existing unknown()/IngestUnknown quarantine path unchanged.
func MatchesOTLPResource(serviceName string) bool {
	return serviceName == OTLPResourceServiceName
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
	default:
		return "", false
	}
}
