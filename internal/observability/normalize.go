package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"kansoku.local/kansoku/internal/privacy"
)

var canonicalEventTypes = map[string]string{
	"session_started":  "session.started",
	"user_prompt":      "prompt.submitted",
	"tool_finished":    "component.executed",
	"session_finished": "session.stopped",
}

func stableID(namespace string, values ...string) string {
	hash := sha256.New()
	hash.Write([]byte(namespace))
	for _, value := range values {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// resolveCanonicalEventType maps one privacy.SafeRecord.EventType onto the
// closed canonical (dotted) event type vocabulary validEventType enforces.
// The fixture-agent lane's wire records always carry an underscore-shaped
// name (session_started/user_prompt/tool_finished/session_finished) that
// canonicalEventTypes translates; a real adapter's OTLP-sourced safe record
// (built by IngestSafeFields, never routed through the fixture sanitizer's
// DecodeAndExtract) instead already carries the final canonical type
// (codexadapter/claudeadapter's CanonicalEventForOTel already resolved it),
// so it is accepted as-is once validEventType confirms it is a real member
// of the closed vocabulary -- never a second, looser acceptance path.
func resolveCanonicalEventType(recordEventType string) (string, bool) {
	if eventType, ok := canonicalEventTypes[recordEventType]; ok {
		return eventType, true
	}
	if validEventType(recordEventType) {
		return recordEventType, true
	}
	return "", false
}

func NormalizedFromSafe(record privacy.SafeRecord, kind SourceKind, sequence uint64, now time.Time) (Event, Evidence, error) {
	eventType, ok := resolveCanonicalEventType(record.EventType)
	if !ok {
		return Event{}, Evidence{}, errors.New("unsupported_normalized_event_type")
	}
	if record.Confidence < 0 || record.Confidence > 1 || record.RecordID == "" || record.IdempotencyKey == "" || len(record.Lineage.SessionPseudonym) < 24 {
		return Event{}, Evidence{}, errors.New("invalid_safe_record")
	}
	tier := TierNative
	confidence := record.Confidence
	if kind == SourceTranscript {
		tier = TierReconstructed
		if confidence > 0.95 {
			confidence = 0.95
		}
	}
	success := record.Outcome == "succeeded"
	componentID := ""
	if record.Tool.ID != nil {
		componentID = *record.Tool.ID
	} else if len(record.ComponentMentions) > 0 {
		mentions := append([]string(nil), record.ComponentMentions...)
		sort.Strings(mentions)
		componentID = mentions[0]
	}
	factKey := stableID("fact/1", record.RecordID, eventType)
	eventID := "evt_" + factKey[:32]
	evidenceKey := stableID("evidence/1", factKey, string(kind), record.IdempotencyKey)
	schemaID := record.SourceSchemaID
	switch kind {
	case SourceTranscript:
		schemaID = "fixture.agent-transcript/1"
	case SourceOTLPLog, SourceOTLPSpan, SourceOTLPMetric:
		schemaID = fixtureOTLPSchema
	case SourceAdapterBatch:
		schemaID = record.SourceSchemaID
	}
	schemaFingerprint := stableID("lane-schema/1", schemaID, record.SchemaFingerprint)
	now = now.UTC()
	timestampQuality := "source_rfc3339"
	if delta := record.ObservedAt.UTC().Sub(now); delta > 5*time.Minute || delta < -5*time.Minute {
		timestampQuality = "source_clock_skewed"
	}
	event := Event{
		SpecVersion: EventSpecVersion, EventID: eventID, FactKey: factKey, EventType: eventType,
		EmittedAt: record.ObservedAt.UTC(), ObservedAt: record.ObservedAt.UTC(), IngestedAt: now,
		TimestampQuality: timestampQuality, Source: SourceRef{
			AdapterID: record.AdapterID, AdapterVersion: record.AdapterVersion, Kind: kind,
			SchemaID: schemaID, SchemaFingerprint: schemaFingerprint,
			InstallationID: "ain_fixture", NativeEventID: record.Lineage.SourceRecordPseudonym, Sequence: sequence,
		}, Scope: Scope{DeviceID: "dev_fixture", AgentInstallationID: "ain_fixture", SessionID: "ses_" + record.Lineage.SessionPseudonym[:24]},
		Subject: Subject{Kind: "component", ComponentID: componentID}, Measurements: Measurements{Success: &success},
		ValueState: string(record.ValueState), Outcome: record.Outcome, CorrelationStatus: CorrelationExact,
		Lifecycle: []EventStage{StageReceived, StageSanitized, StageValidated, StageNormalized},
	}
	evidence := Evidence{
		EvidenceID: "evd_" + evidenceKey[:32], EventID: eventID, Source: event.Source, Tier: tier,
		Confidence: confidence, Completeness: Complete, ReplayCount: 0, FirstSeenAt: now, LastSeenAt: now,
		Sanitizer: record.Lineage.SanitizerVersion, PrivacySHA256: record.Lineage.ContractSHA256,
		Assertion: EvidenceAssertion{EventType: eventType, Outcome: record.Outcome, ValueState: string(record.ValueState)},
	}
	return event, evidence, nil
}

func Correlate(event Event, candidates []Candidate) Correlation {
	ordered := append([]Candidate(nil), candidates...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Confidence == ordered[j].Confidence {
			return ordered[i].EventID < ordered[j].EventID
		}
		return ordered[i].Confidence > ordered[j].Confidence
	})
	status := CorrelationUnmatched
	switch {
	case event.Source.NativeEventID != "":
		status = CorrelationExact
	case len(ordered) == 1:
		status = CorrelationCandidate
	case len(ordered) > 1:
		status = CorrelationAmbiguous
	}
	return Correlation{CorrelationID: "cor_" + stableID("correlation/1", event.EventID)[:32], EventID: event.EventID, Status: status, Candidates: ordered}
}
