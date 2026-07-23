package claudeadapter

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"

	"kansoku.local/kansoku/internal/privacy"
)

const (
	sourceIDHook       = "claude.hook"
	hookSourceSchemaID = "claude.hook/1"

	// maxHookBodyBytes bounds the hook helper's stdin read. It matches
	// contracts/observability/ingress.yaml's limits.max_frame_bytes so a
	// Claude Code hook payload is never treated more permissively than any
	// other hook_http source.
	maxHookBodyBytes = 1 << 20
)

// HookEvent enumerates the Claude Code lifecycle hook events
// contracts/claude/hooks-and-otel.yaml declares. Exact per-version
// availability is manifested (see EventAvailability), never hardcoded as
// universally present: an installed Claude Code release may support a strict
// subset of this closed vocabulary.
type HookEvent string

const (
	HookSessionStart     HookEvent = "SessionStart"
	HookUserPromptSubmit HookEvent = "UserPromptSubmit"
	HookPreToolUse       HookEvent = "PreToolUse"
	HookPostToolUse      HookEvent = "PostToolUse"
	HookSubagentStart    HookEvent = "SubagentStart"
	HookSubagentStop     HookEvent = "SubagentStop"
	HookStop             HookEvent = "Stop"
)

// SupportedHookEvents is the closed, manifested vocabulary of Claude Code
// hook events this adapter recipe knows how to map. It mirrors
// contracts/claude/hooks-and-otel.yaml's hook_source.supported_events
// verbatim; a later validator stage checks the two stay identical.
func SupportedHookEvents() []HookEvent {
	return []HookEvent{
		HookSessionStart, HookUserPromptSubmit, HookPreToolUse, HookPostToolUse,
		HookSubagentStart, HookSubagentStop, HookStop,
	}
}

func validHookEvent(event HookEvent) bool {
	for _, candidate := range SupportedHookEvents() {
		if candidate == event {
			return true
		}
	}
	return false
}

// hookEventCanonical is the source_event_mapping table from
// contracts/claude/hooks-and-otel.yaml, restricted to the hook_http rows.
var hookEventCanonical = map[HookEvent]string{
	HookSessionStart:     "session.started",
	HookUserPromptSubmit: "prompt.submitted",
	HookPreToolUse:       "tool.called",
	HookPostToolUse:      "tool.called",
	HookSubagentStart:    "subagent.started",
	HookSubagentStop:     "subagent.completed",
	HookStop:             "session.stopped",
}

// CanonicalEventForHook returns the canonical event type a supported hook
// event maps to, and whether the event is known at all. An unrecognized
// event never silently maps to a plausible-looking default; the caller must
// treat ok=false as "this Claude Code release's manifest does not declare
// this event", which degrades only claude.hook's own capability.
func CanonicalEventForHook(event HookEvent) (string, bool) {
	canonical, ok := hookEventCanonical[event]
	return canonical, ok
}

// AllowlistedHookFields is the exact, closed set of fields the hook helper is
// permitted to send onward, reused verbatim from
// contracts/claude/hooks-and-otel.yaml's hook_source.helper_contract.allowlisted_fields.
// Prompt text, raw transcript path and raw cwd are never members of this set:
// prompt features are computed in memory, and transcript path/cwd are
// pseudonymized before leaving the helper process -- only their derived,
// already-safe shape is ever allowlisted.
func AllowlistedHookFields() map[string]struct{} {
	return map[string]struct{}{
		"session_id":      {},
		"turn_id":         {},
		"model_id":        {},
		"tool_id":         {},
		"tool_status":     {},
		"timing_ms":       {},
		"surface_id":      {},
		"installation_id": {},
	}
}

// HookHelperInput is the bounded, closed shape the hook helper reads from
// stdin. It intentionally has no generic map field: any key outside this
// struct's json tags is rejected by the strict decoder in DecodeHookInput,
// so a future Claude Code release adding a new stdin field never silently
// starts flowing untrusted data through unnoticed. Prompt, TranscriptPath and
// CWD are read only to compute in-memory features / pseudonyms; none of the
// three is ever copied into HookHelperOutput or any other durable-bound
// value in raw form.
type HookHelperInput struct {
	Event          HookEvent `json:"hook_event_name"`
	SessionID      string    `json:"session_id"`
	TurnID         string    `json:"turn_id,omitempty"`
	ModelID        string    `json:"model_id,omitempty"`
	ToolID         string    `json:"tool_id,omitempty"`
	ToolStatus     string    `json:"tool_status,omitempty"`
	TimingMS       int64     `json:"timing_ms,omitempty"`
	SurfaceID      string    `json:"surface_id,omitempty"`
	InstallationID string    `json:"installation_id,omitempty"`
	// Prompt is read only to compute PromptFeatures in memory; it is never
	// copied into HookHelperOutput or any other durable-bound value.
	Prompt string `json:"prompt,omitempty"`
	// TranscriptPath and CWD are documented content-bearing fields upstream
	// (contracts/claude/hooks-and-otel.yaml's
	// documented_payload_surfaces.content_bearing_fields_present_upstream).
	// They are read only to derive a device-scoped HMAC pseudonym; the raw
	// path string is discarded the instant BuildHookOutput returns.
	TranscriptPath string `json:"transcript_path,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	// ToolInput and ToolResponse are documented content-bearing fields Claude
	// Code's detailed telemetry may expose. They exist on this struct only so
	// DecodeHookInput can accept (and then unconditionally discard) an
	// upstream payload that includes them; BuildHookOutput never reads
	// either field, and HookHelperOutput has no representation for either at
	// all. This is the concrete unconditional-strip enforcement point for
	// hook-sourced tool input/output.
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`
}

// HookHelperOutput is the already-sanitized, allowlisted-only event the hook
// helper sends to /v1/hooks/claude/<event>. Its JSON shape is the exact
// allowlist from contracts/claude/hooks-and-otel.yaml; raw prompt text, raw
// transcript path/cwd and raw tool input/output have no representation here
// at all.
type HookHelperOutput struct {
	EventID                 string                  `json:"event_id"`
	SessionID               string                  `json:"session_id"`
	ObservedAt              string                  `json:"observed_at"`
	EventType               string                  `json:"event_type"`
	TurnID                  string                  `json:"turn_id,omitempty"`
	ModelID                 string                  `json:"model_id,omitempty"`
	ToolID                  string                  `json:"tool_id,omitempty"`
	ToolStatus              string                  `json:"tool_status,omitempty"`
	TimingMS                int64                   `json:"timing_ms,omitempty"`
	SurfaceID               string                  `json:"surface_id,omitempty"`
	InstallationID          string                  `json:"installation_id,omitempty"`
	PromptFeatures          *privacy.PromptFeatures `json:"prompt_features,omitempty"`
	TranscriptPathPseudonym string                  `json:"transcript_path_pseudonym,omitempty"`
	CWDPseudonym            string                  `json:"cwd_pseudonym,omitempty"`
}

// ErrUnsupportedHookEvent is returned when a hook helper posts an event this
// adapter recipe's manifest does not declare. The caller must treat this as
// a degraded-capability incident scoped to claude.hook, never as evidence
// that the session had zero activity.
var ErrUnsupportedHookEvent = errors.New("claude_hook_event_not_in_active_version_manifest")

// ErrOversizedHookInput is returned when stdin exceeds the bounded read
// ceiling. The helper must never buffer more than this before rejecting.
var ErrOversizedHookInput = errors.New("claude_hook_input_oversized")

// DecodeHookInput performs the hook helper's bounded stdin read: exactly
// maxHookBodyBytes+1 bytes are read so an oversized payload is detected
// without ever fully materializing an unbounded buffer, the JSON is decoded
// with unknown-field rejection so a stray raw field can never smuggle
// through silently, and the declared hook_event_name is validated against
// the closed, version-manifested vocabulary. tool_input/tool_response are
// accepted here (as raw, opaque json.RawMessage) purely so a well-formed
// upstream payload is not rejected outright; BuildHookOutput never reads
// either field.
func DecodeHookInput(reader io.Reader) (HookHelperInput, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxHookBodyBytes+1))
	if err != nil {
		return HookHelperInput{}, errors.New("claude_hook_read_failure")
	}
	if len(raw) > maxHookBodyBytes {
		return HookHelperInput{}, ErrOversizedHookInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input HookHelperInput
	if err := decoder.Decode(&input); err != nil {
		return HookHelperInput{}, errors.New("claude_hook_input_malformed")
	}
	if err := decoder.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		return HookHelperInput{}, errors.New("claude_hook_trailing_input")
	}
	if input.SessionID == "" {
		return HookHelperInput{}, errors.New("claude_hook_missing_session_id")
	}
	if !validHookEvent(input.Event) {
		return HookHelperInput{}, ErrUnsupportedHookEvent
	}
	return input, nil
}

// BuildHookOutput computes the allowlisted, already-sanitized output event
// for one decoded hook input. Prompt features are computed here, in memory,
// from input.Prompt; transcript_path and cwd are pseudonymized here via the
// same device-scoped HMAC construction internal/privacy.Lineage uses. The
// raw prompt string, raw transcript path and raw cwd are never copied into
// the returned value, matching
// contracts/claude/hooks-and-otel.yaml's prompt_feature_computation and
// path_pseudonymization guarantees. tool_input/tool_response are never read
// at all -- this is the unconditional-strip enforcement point for those two
// fields regardless of what upstream detailed telemetry settings report. now
// is injected so callers get a deterministic, testable observed_at.
func BuildHookOutput(input HookHelperInput, pseudonymKey []byte, now time.Time) (HookHelperOutput, error) {
	canonical, ok := CanonicalEventForHook(input.Event)
	if !ok {
		return HookHelperOutput{}, ErrUnsupportedHookEvent
	}
	output := HookHelperOutput{
		EventID:        hookEventID(input, now),
		SessionID:      input.SessionID,
		ObservedAt:     now.UTC().Format(time.RFC3339Nano),
		EventType:      canonical,
		TurnID:         input.TurnID,
		ModelID:        input.ModelID,
		ToolID:         input.ToolID,
		ToolStatus:     input.ToolStatus,
		TimingMS:       input.TimingMS,
		SurfaceID:      input.SurfaceID,
		InstallationID: input.InstallationID,
	}
	if input.Event == HookUserPromptSubmit {
		// Reuse internal/privacy's own audited feature-extraction routine
		// (the same one DecodeAndExtract uses) rather than a second,
		// hand-rolled computation; the raw prompt string is discarded the
		// instant this call returns and is never copied into output.
		features := privacy.ExtractPromptFeatures(input.Prompt, 0)
		output.PromptFeatures = &features
	}
	if input.TranscriptPath != "" {
		if len(pseudonymKey) < 32 {
			return HookHelperOutput{}, errors.New("claude_hook_pseudonym_key_too_short")
		}
		output.TranscriptPathPseudonym = pseudonymizePath(pseudonymKey, input.TranscriptPath)
	}
	if input.CWD != "" {
		if len(pseudonymKey) < 32 {
			return HookHelperOutput{}, errors.New("claude_hook_pseudonym_key_too_short")
		}
		output.CWDPseudonym = pseudonymizePath(pseudonymKey, input.CWD)
	}
	return output, nil
}

// pseudonymizePath derives a stable, non-reversible pseudonym for a
// filesystem path using the same class of device-scoped HMAC construction as
// internal/privacy.Lineage and adaptersdk.HostView.PseudonymizePath. It is
// the only durable representation of transcript_path/cwd this package ever
// produces; the raw path is never sent or stored.
func pseudonymizePath(key []byte, path string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("claudeadapter-path-pseudonym/1\x00" + path))
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
}

func hookEventID(input HookHelperInput, now time.Time) string {
	hash := sha256.New()
	hash.Write([]byte("claude-hook-event/1"))
	for _, part := range []string{input.SessionID, string(input.Event), input.ToolID, input.TurnID, now.UTC().Format(time.RFC3339Nano)} {
		hash.Write([]byte{0})
		hash.Write([]byte(part))
	}
	return "clh_" + hex.EncodeToString(hash.Sum(nil))[:32]
}

// ValidateHookOutputAllowlist re-checks a HookHelperOutput's JSON encoding
// contains only allowlisted field names before it is ever sent onward. This
// is a defense-in-depth check the hook helper runs on its own output right
// before transport, independent of Go's struct-tag closure, so a future
// struct-field addition that forgets to update AllowlistedHookFields fails
// loudly instead of silently widening what leaves the process.
func ValidateHookOutputAllowlist(output HookHelperOutput) error {
	encoded, err := json.Marshal(output)
	if err != nil {
		return errors.New("claude_hook_output_encode_failure")
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return errors.New("claude_hook_output_decode_failure")
	}
	allowed := AllowlistedHookFields()
	allowed["event_id"] = struct{}{}
	allowed["observed_at"] = struct{}{}
	allowed["event_type"] = struct{}{}
	allowed["prompt_features"] = struct{}{}
	allowed["transcript_path_pseudonym"] = struct{}{}
	allowed["cwd_pseudonym"] = struct{}{}
	fields := make([]string, 0, len(generic))
	for field := range generic {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		if _, ok := allowed[field]; !ok {
			return errors.New("claude_hook_output_field_not_allowlisted")
		}
	}
	return nil
}

// HookRoutePath returns the exact /v1/hooks/claude/<event> path this hook
// event is served on, reusing contracts/observability/ingress.yaml's generic
// hook_http route template verbatim with adapter="claude" substituted in --
// never a parallel ingress mechanism, and never colliding with the reserved
// "fixture-agent" adapter path segment.
func HookRoutePath(event HookEvent) string {
	return "/v1/hooks/claude/" + string(event)
}
