package privacy

import (
	"io"
	"time"
)

// PrivacyContractSemanticSHA256 is generated from the canonical JSON encoding
// of every contracts/privacy registry, ordered by repository-relative path.
// scripts/validate_privacy.py refuses a registry/runtime drift.
const PrivacyContractSemanticSHA256 = "1fdb47cd6865c4f2bdb3e6ff564740dc334ebb7b03a97423df57f1c605c9b34c"

type ValueState string

const (
	ValueObserved    ValueState = "observed"
	ValueUnsupported ValueState = "unsupported"
	ValueNotObserved ValueState = "not_observed"
	ValueRedacted    ValueState = "redacted"
	ValueUnknown     ValueState = "unknown"
	ValueNumericZero ValueState = "numeric_zero"
)

// TelemetryMeasurements is the closed, content-free numeric subset accepted
// from native agent telemetry. Pointers preserve not-observed separately
// from a genuine numeric zero.
type TelemetryMeasurements struct {
	DurationMS           *int64 `json:"duration_ms"`
	PromptCharacterCount *int64 `json:"prompt_character_count"`
	InputTokens          *int64 `json:"input_tokens"`
	CachedInputTokens    *int64 `json:"cached_input_tokens"`
	OutputTokens         *int64 `json:"output_tokens"`
	ProviderCostMicros   *int64 `json:"provider_cost_micros"`
	// CacheCreationTokens/CacheReadTokens are the two distinct cache
	// measurements agents report. CachedInputTokens keeps its existing
	// meaning -- whatever the source called "cached input" -- rather than
	// being redefined as a sum of these two, because collapsing three
	// separately reported numbers into one is exactly the kind of silent
	// loss this boundary exists to prevent.
	CacheCreationTokens *int64 `json:"cache_creation_tokens"`
	CacheReadTokens     *int64 `json:"cache_read_tokens"`
	// ResponseCharacterCount is a length, never the response itself; it is
	// the response-side counterpart of PromptCharacterCount.
	ResponseCharacterCount *int64 `json:"response_character_count"`
	// ToolInputBytes/ToolResultBytes are sizes the agent already computed.
	// The payloads they measure remain unconditionally dropped.
	ToolInputBytes  *int64 `json:"tool_input_bytes"`
	ToolResultBytes *int64 `json:"tool_result_bytes"`
}

// ActivityMetadata is the closed, content-free projection of how an activity
// happened: which decision a tool call received, which surface registered a
// hook, how a session was started. Every field is a short vocabulary token or
// an already-pseudonymized correlation handle -- never a payload, matcher,
// command, path or free-form label. Values outside a known vocabulary are
// carried through verbatim and classified downstream, exactly as
// ComponentEvidenceMetadata.SourceScope already is: this boundary records
// what the agent said, it does not coerce it into something it recognizes.
type ActivityMetadata struct {
	ToolDecision string `json:"tool_decision"`
	// ToolSource is where the tool came from ("builtin", "mcp", ...).
	// ToolDecisionSource is where the permission answer came from
	// ("config", ...). The agent reports both on the same record and they
	// answer different questions, so they are two fields, not one.
	ToolSource         string `json:"tool_source"`
	ToolDecisionSource string `json:"tool_decision_source"`
	// ToolUsePseudonym is the device-scoped HMAC of the agent's tool-use id.
	// It is what lets a decision be joined to the execution it authorized
	// without the raw upstream identifier ever becoming durable.
	ToolUsePseudonym string `json:"tool_use_pseudonym"`
	HookEvent        string `json:"hook_event"`
	HookType         string `json:"hook_type"`
	HookSource       string `json:"hook_source"`
	SessionStartType string `json:"session_start_type"`
	QuerySource      string `json:"query_source"`
	TerminalType     string `json:"terminal_type"`
	SafeMode         string `json:"safe_mode"`
}

type ObservationState string

const (
	ObservationObserved    ObservationState = "observed"
	ObservationUnsupported ObservationState = "unsupported"
	ObservationNotObserved ObservationState = "not_observed"
	ObservationRedacted    ObservationState = "redacted"
	ObservationUnknown     ObservationState = "unknown"
)

type CompletenessState string

const (
	CompletenessComplete CompletenessState = "complete"
	CompletenessPartial  CompletenessState = "partial"
	CompletenessDegraded CompletenessState = "degraded"
	CompletenessUnknown  CompletenessState = "unknown"
)

// Limits bound untrusted work before any value can reach logging, tracing,
// retry, quarantine, or persistence code.
type Limits struct {
	MaxTotalBytes    int64
	MaxDepth         int
	MaxArrayItems    int
	MaxObjectFields  int
	MaxStringBytes   int
	MaxNumberBytes   int
	MaxRecords       int
	MaxProtobufFrame int64
}

func DefaultLimits() Limits {
	return Limits{
		MaxTotalBytes:    1 << 20,
		MaxDepth:         16,
		MaxArrayItems:    1024,
		MaxObjectFields:  1024,
		MaxStringBytes:   1 << 16,
		MaxNumberBytes:   128,
		MaxRecords:       128,
		MaxProtobufFrame: 1 << 20,
	}
}

type Fingerprint struct {
	SchemaFingerprint string `json:"schema_fingerprint"`
	TotalBytes        int64  `json:"total_bytes"`
	RecordCount       int    `json:"record_count"`
}

type SourceSchema struct {
	ID             string
	AdapterID      string
	AdapterVersion string
	EventTypes     map[string]struct{}
	Models         map[string]struct{}
	Tools          map[string]struct{}
	Components     map[string]struct{}
	InputFields    map[string]struct{}
}

func FixtureSourceSchema() SourceSchema {
	return SourceSchema{
		ID:             "fixture.agent-hook/1",
		AdapterID:      "fixture-agent",
		AdapterVersion: "1.0.0",
		EventTypes: stringSet(
			"session_started", "user_prompt", "tool_finished", "session_finished",
		),
		Models:     stringSet("catalog/model-safe"),
		Tools:      stringSet("inventory/tool-safe"),
		Components: stringSet("inventory/skill-safe"),
		InputFields: stringSet(
			"event_id", "session_id", "observed_at", "event_type", "outcome", "value_state",
			"model", "tool_name", "prompt", "attachments", "response", "source_code",
			"tool_input", "tool_output", "command", "path", "environment", "credentials",
			"exception",
		),
	}
}

func stringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

// Sanitizer is the only interface allowed to consume raw agent input.
type Sanitizer interface {
	InspectMetadata(reader io.Reader, limit Limits) (Fingerprint, error)
	DecodeAndExtract(reader io.Reader, schema SourceSchema) ([]SafeRecord, *SafeError)
}

type PromptFeatures struct {
	State              CompletenessState `json:"state"`
	ByteCount          int               `json:"byte_count"`
	CharacterCount     int               `json:"character_count"`
	WordCount          int               `json:"word_count"`
	LineCount          int               `json:"line_count"`
	CoarseScript       string            `json:"coarse_script"`
	CodeFenceCount     int               `json:"code_fence_count"`
	AttachmentCount    int               `json:"attachment_count"`
	URLReferenceCount  int               `json:"url_reference_count"`
	FileReferenceCount int               `json:"file_reference_count"`
}

// CatalogObservation represents absence with a typed state and JSON null. A
// magic string such as "not_observed" can therefore never be mistaken for an
// inventory/catalog ID.
type CatalogObservation struct {
	State ObservationState `json:"state"`
	ID    *string          `json:"id"`
}

type RedactionCounts struct {
	PromptFields              int `json:"prompt_fields"`
	AttachmentFields          int `json:"attachment_fields"`
	ResponseFields            int `json:"response_fields"`
	SourceFields              int `json:"source_fields"`
	ToolIOFields              int `json:"tool_io_fields"`
	CommandFields             int `json:"command_fields"`
	PathFields                int `json:"path_fields"`
	EnvironmentFields         int `json:"environment_fields"`
	CredentialFields          int `json:"credential_fields"`
	ExceptionFields           int `json:"exception_fields"`
	SensitiveIdentifierFields int `json:"sensitive_identifier_fields"`
}

type Lineage struct {
	SourceRecordPseudonym string `json:"source_record_pseudonym"`
	SessionPseudonym      string `json:"session_pseudonym"`
	TurnPseudonym         string `json:"turn_pseudonym"`
	// MessagePseudonym and UserPseudonym are device-scoped HMACs of the
	// agent's own message and user identifiers. They make per-message and
	// per-operator correlation possible without either raw value ever
	// becoming durable, the same construction TurnPseudonym already uses.
	MessagePseudonym  string `json:"message_pseudonym"`
	UserPseudonym     string `json:"user_pseudonym"`
	AdapterID         string `json:"adapter_id"`
	AdapterVersion    string `json:"adapter_version"`
	SourceSchemaID    string `json:"source_schema_id"`
	SchemaFingerprint string `json:"schema_fingerprint"`
	SanitizerVersion  string `json:"sanitizer_version"`
	ContractSHA256    string `json:"contract_sha256"`
}

// ComponentEvidenceMetadata is the closed identity-only projection accepted
// from native component telemetry. It cannot carry a payload, command,
// environment value or filesystem location.
type ComponentEvidenceMetadata struct {
	QualifiedIdentity    string `json:"qualified_identity"`
	IdentitySource       string `json:"identity_source"`
	OwnerPluginIdentity  string `json:"owner_plugin_identity"`
	InvocationMode       string `json:"invocation_mode"`
	UpstreamIdentityHash string `json:"upstream_identity_hash"`
	SourceScope          string `json:"source_scope"`
	// Marketplace is the declared marketplace an owner plugin came from. The
	// resolver previously approximated it by splitting the owner declared
	// name on '@' while the exact value sat unread on the wire.
	Marketplace string `json:"marketplace"`
	// ComponentVersion is the owner's declared version as the agent reports
	// it: identity metadata, not a location and not a payload. There is no
	// separate scope field here -- plugin.scope already lands on SourceScope
	// above, and the wire carries that value exactly once.
	ComponentVersion string `json:"component_version"`
}

// SafeRecord is an explicit persistence allowlist. It deliberately has no
// generic payload/attributes map.
type SafeRecord struct {
	RecordID          string                    `json:"record_id"`
	IdempotencyKey    string                    `json:"idempotency_key"`
	AdapterID         string                    `json:"adapter_id"`
	AdapterVersion    string                    `json:"adapter_version"`
	SourceSchemaID    string                    `json:"source_schema_id"`
	SchemaFingerprint string                    `json:"schema_fingerprint"`
	ObservedAt        time.Time                 `json:"observed_at"`
	ReceivedAt        time.Time                 `json:"received_at"`
	Confidence        float64                   `json:"confidence"`
	EventType         string                    `json:"event_type"`
	Outcome           string                    `json:"outcome"`
	ValueState        ValueState                `json:"value_state"`
	Model             CatalogObservation        `json:"model"`
	Tool              CatalogObservation        `json:"tool"`
	ComponentKind     string                    `json:"component_kind"`
	ComponentMentions []string                  `json:"component_mentions"`
	ComponentEvidence ComponentEvidenceMetadata `json:"component_evidence"`
	Activity          ActivityMetadata          `json:"activity"`
	PromptFeatures    PromptFeatures            `json:"prompt_features"`
	Telemetry         TelemetryMeasurements     `json:"telemetry"`
	RedactionCounts   RedactionCounts           `json:"redaction_counts"`
	Lineage           Lineage                   `json:"lineage"`
}

// SafeError contains structural metadata only. Error intentionally returns
// the category, never a wrapped decoder/source error string.
type SafeError struct {
	IncidentID        string    `json:"incident_id"`
	SourceSchemaID    string    `json:"source_schema_id"`
	SchemaFingerprint string    `json:"schema_fingerprint"`
	FieldPath         string    `json:"field_path"`
	Category          string    `json:"category"`
	TotalBytes        int64     `json:"total_bytes"`
	RecordCount       int       `json:"record_count"`
	ObservedAt        time.Time `json:"observed_at"`
	ReceivedAt        time.Time `json:"received_at"`
}

func (e *SafeError) Error() string {
	if e == nil {
		return ""
	}
	return e.Category
}
