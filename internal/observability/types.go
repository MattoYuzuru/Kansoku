package observability

import "time"

const (
	EventSpecVersion                    = "kansoku.event/1"
	StoreSpecVersion                    = "kansoku.durable-state/1"
	ObservabilityContractSemanticSHA256 = "TO_BE_REPLACED"
)

type EventStage string

const (
	StageReceived   EventStage = "received"
	StageSanitized  EventStage = "sanitized"
	StageValidated  EventStage = "validated"
	StageNormalized EventStage = "normalized"
	StageDeduped    EventStage = "deduplicated"
	StageCorrelated EventStage = "correlated"
	StageReconciled EventStage = "reconciled"
)

type SourceKind string

const (
	SourceHook       SourceKind = "hook_http"
	SourceOTLPLog    SourceKind = "otlp_log"
	SourceOTLPSpan   SourceKind = "otlp_span"
	SourceOTLPMetric SourceKind = "otlp_metric"
	SourceTranscript SourceKind = "transcript_jsonl"
)

type SourceLifecycle string

const (
	SourceDiscovered SourceLifecycle = "discovered"
	SourceConfigured SourceLifecycle = "configured"
	SourceConnected  SourceLifecycle = "connected"
	SourceProducing  SourceLifecycle = "producing"
	SourceReconciled SourceLifecycle = "reconciled"
	SourceDegraded   SourceLifecycle = "degraded"
	SourceDisabled   SourceLifecycle = "disabled"
	SourceError      SourceLifecycle = "error"
)

type CorrelationStatus string

const (
	CorrelationExact     CorrelationStatus = "exact"
	CorrelationCandidate CorrelationStatus = "candidate"
	CorrelationAmbiguous CorrelationStatus = "ambiguous"
	CorrelationUnmatched CorrelationStatus = "unmatched"
)

type Completeness string

const (
	Complete    Completeness = "complete"
	Partial     Completeness = "partial"
	Degraded    Completeness = "degraded"
	Unknown     Completeness = "unknown"
	Unsupported Completeness = "unsupported"
)

type EvidenceTier string

const (
	TierCorroborated  EvidenceTier = "corroborated"
	TierNative        EvidenceTier = "native"
	TierReconstructed EvidenceTier = "reconstructed"
	TierInferred      EvidenceTier = "inferred"
)

type SourceRef struct {
	AdapterID         string     `json:"adapter_id"`
	AdapterVersion    string     `json:"adapter_version"`
	Kind              SourceKind `json:"source_kind"`
	SchemaID          string     `json:"source_schema"`
	SchemaFingerprint string     `json:"schema_fingerprint"`
	InstallationID    string     `json:"installation_id"`
	NativeEventID     string     `json:"native_event_id"`
	Sequence          uint64     `json:"sequence"`
}

type Scope struct {
	DeviceID            string `json:"device_id"`
	AgentInstallationID string `json:"agent_installation_id"`
	SurfaceID           string `json:"surface_id"`
	ProjectID           string `json:"project_id"`
	SessionID           string `json:"session_id"`
	TurnID              string `json:"turn_id"`
	ParentEventID       string `json:"parent_event_id"`
}

type Subject struct {
	Kind               string `json:"kind"`
	ComponentID        string `json:"component_id"`
	ComponentVersionID string `json:"component_version_id"`
}

type Measurements struct {
	DurationMS int64  `json:"duration_ms"`
	Success    *bool  `json:"success"`
	Count      *int64 `json:"count"`
}

// Event is a closed durable allowlist. Raw payloads, generic attribute maps,
// source paths and error strings deliberately have no representation here.
type Event struct {
	SpecVersion       string            `json:"spec_version"`
	EventID           string            `json:"event_id"`
	FactKey           string            `json:"fact_key"`
	EventType         string            `json:"event_type"`
	EmittedAt         time.Time         `json:"emitted_at"`
	ObservedAt        time.Time         `json:"observed_at"`
	IngestedAt        time.Time         `json:"ingested_at"`
	TimestampQuality  string            `json:"timestamp_quality"`
	Source            SourceRef         `json:"source"`
	Scope             Scope             `json:"scope"`
	Subject           Subject           `json:"subject"`
	Measurements      Measurements      `json:"measurements"`
	ValueState        string            `json:"value_state"`
	Outcome           string            `json:"outcome"`
	CorrelationStatus CorrelationStatus `json:"correlation_status"`
	Lifecycle         []EventStage      `json:"lifecycle"`
}

type Evidence struct {
	EvidenceID    string            `json:"evidence_id"`
	EventID       string            `json:"event_id"`
	Source        SourceRef         `json:"source"`
	Tier          EvidenceTier      `json:"tier"`
	Confidence    float64           `json:"confidence"`
	Completeness  Completeness      `json:"completeness"`
	ReplayCount   uint64            `json:"replay_count"`
	FirstSeenAt   time.Time         `json:"first_seen_at"`
	LastSeenAt    time.Time         `json:"last_seen_at"`
	Sanitizer     string            `json:"sanitizer_version"`
	PrivacySHA256 string            `json:"privacy_contract_sha256"`
	Assertion     EvidenceAssertion `json:"assertion"`
}

// EvidenceAssertion preserves what each source asserted even when the first
// canonical fact remains unchanged after a contradiction.
type EvidenceAssertion struct {
	EventType  string `json:"event_type"`
	Outcome    string `json:"outcome"`
	ValueState string `json:"value_state"`
}

type Candidate struct {
	EventID    string  `json:"event_id"`
	Confidence float64 `json:"confidence"`
}

type Correlation struct {
	CorrelationID string            `json:"correlation_id"`
	EventID       string            `json:"event_id"`
	Status        CorrelationStatus `json:"status"`
	Candidates    []Candidate       `json:"candidates"`
}

type Quarantine struct {
	QuarantineID      string     `json:"quarantine_id"`
	SourceKind        SourceKind `json:"source_kind"`
	SchemaFingerprint string     `json:"schema_fingerprint"`
	Category          string     `json:"category"`
	ByteCount         int64      `json:"byte_count"`
	RecordCount       int        `json:"record_count"`
	ObservedAt        time.Time  `json:"observed_at"`
}

type Incident struct {
	IncidentID      string       `json:"incident_id"`
	Capability      string       `json:"capability"`
	Category        string       `json:"category"`
	Completeness    Completeness `json:"completeness"`
	OpenedAt        time.Time    `json:"opened_at"`
	LastObserved    time.Time    `json:"last_observed_at"`
	ResolvedAt      *time.Time   `json:"resolved_at"`
	OccurrenceCount uint64       `json:"occurrence_count"`
}

type Watermark struct {
	SourceID             string          `json:"source_id"`
	Lifecycle            SourceLifecycle `json:"lifecycle"`
	LastDiscovered       time.Time       `json:"last_discovered_at"`
	LastReadSequence     uint64          `json:"last_read_sequence"`
	LastEmittedSequence  uint64          `json:"last_emitted_sequence"`
	LastObserved         time.Time       `json:"last_observed_at"`
	LastCommitted        time.Time       `json:"last_committed_at"`
	LastEligibleActivity time.Time       `json:"last_eligible_activity_at"`
	ExpectedCadenceMS    int64           `json:"expected_cadence_ms"`
	GapCount             uint64          `json:"gap_count"`
	Inactivity           bool            `json:"inactivity"`
}

type Checkpoint struct {
	ImporterID string `json:"importer_id"`
	Offset     int64  `json:"offset"`
	Sequence   uint64 `json:"sequence"`
	FileID     string `json:"file_id"`
}

type Fact struct {
	Event        Event        `json:"event"`
	EvidenceIDs  []string     `json:"evidence_ids"`
	Completeness Completeness `json:"completeness"`
}

type DurableState struct {
	SpecVersion  string                 `json:"spec_version"`
	Revision     uint64                 `json:"revision"`
	Facts        map[string]Fact        `json:"facts"`
	Evidence     map[string]Evidence    `json:"evidence"`
	Correlations map[string]Correlation `json:"correlations"`
	Quarantine   []Quarantine           `json:"quarantine"`
	Incidents    map[string]Incident    `json:"incidents"`
	Watermarks   map[string]Watermark   `json:"watermarks"`
	Checkpoints  map[string]Checkpoint  `json:"checkpoints"`
}

func emptyState() DurableState {
	return DurableState{SpecVersion: StoreSpecVersion, Facts: map[string]Fact{}, Evidence: map[string]Evidence{}, Correlations: map[string]Correlation{}, Quarantine: []Quarantine{}, Incidents: map[string]Incident{}, Watermarks: map[string]Watermark{}, Checkpoints: map[string]Checkpoint{}}
}
