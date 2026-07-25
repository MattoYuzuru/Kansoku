package privacy

import (
	"io"
	"time"
)

// PrivacyContractSemanticSHA256 is generated from the canonical JSON encoding
// of every contracts/privacy registry, ordered by repository-relative path.
// scripts/validate_privacy.py refuses a registry/runtime drift.
const PrivacyContractSemanticSHA256 = "e81482afd6005beb05eb3287397248367796adcbe2468132a960c5f3d608f974"

type ValueState string

const (
	ValueUnsupported ValueState = "unsupported"
	ValueNotObserved ValueState = "not_observed"
	ValueRedacted    ValueState = "redacted"
	ValueUnknown     ValueState = "unknown"
	ValueNumericZero ValueState = "numeric_zero"
)

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
	AdapterID             string `json:"adapter_id"`
	AdapterVersion        string `json:"adapter_version"`
	SourceSchemaID        string `json:"source_schema_id"`
	SchemaFingerprint     string `json:"schema_fingerprint"`
	SanitizerVersion      string `json:"sanitizer_version"`
	ContractSHA256        string `json:"contract_sha256"`
}

// SafeRecord is an explicit persistence allowlist. It deliberately has no
// generic payload/attributes map.
type SafeRecord struct {
	RecordID          string             `json:"record_id"`
	IdempotencyKey    string             `json:"idempotency_key"`
	AdapterID         string             `json:"adapter_id"`
	AdapterVersion    string             `json:"adapter_version"`
	SourceSchemaID    string             `json:"source_schema_id"`
	SchemaFingerprint string             `json:"schema_fingerprint"`
	ObservedAt        time.Time          `json:"observed_at"`
	ReceivedAt        time.Time          `json:"received_at"`
	Confidence        float64            `json:"confidence"`
	EventType         string             `json:"event_type"`
	Outcome           string             `json:"outcome"`
	ValueState        ValueState         `json:"value_state"`
	Model             CatalogObservation `json:"model"`
	Tool              CatalogObservation `json:"tool"`
	ComponentMentions []string           `json:"component_mentions"`
	PromptFeatures    PromptFeatures     `json:"prompt_features"`
	RedactionCounts   RedactionCounts    `json:"redaction_counts"`
	Lineage           Lineage            `json:"lineage"`
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
