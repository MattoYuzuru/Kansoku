package observability

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
)

var (
	hex64Pattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	fingerprintPattern  = regexp.MustCompile(`^(?:(?:sha256|hmac-sha256):)?[0-9a-f]{64}$`)
	hex32IDPattern      = regexp.MustCompile(`^(?:evt|evd|cor|inc|qua)_[0-9a-f]{32}$`)
	safeIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
	pseudonymPattern    = regexp.MustCompile(`^hmac-sha256:[0-9a-f]{64}$`)
	installationPattern = regexp.MustCompile(`^ain_(?:fixture|[0-9a-f]{32})$`)
	devicePattern       = regexp.MustCompile(`^dev_(?:fixture|[0-9a-f]{32})$`)
)

func validSourceKind(kind SourceKind) bool {
	switch kind {
	case SourceHook, SourceOTLPLog, SourceOTLPSpan, SourceOTLPMetric, SourceTranscript, SourceAdapterBatch, SourceEvidenceBridge:
		return true
	default:
		return false
	}
}

func validSourceLifecycle(value SourceLifecycle) bool {
	switch value {
	case SourceDiscovered, SourceConfigured, SourceConnected, SourceProducing, SourceReconciled, SourceDegraded, SourceDisabled, SourceError:
		return true
	default:
		return false
	}
}

func validCompleteness(value Completeness) bool {
	switch value {
	case Complete, Partial, Degraded, Unknown, Unsupported:
		return true
	default:
		return false
	}
}

func validCorrelation(value CorrelationStatus) bool {
	switch value {
	case CorrelationExact, CorrelationCandidate, CorrelationAmbiguous, CorrelationUnmatched:
		return true
	default:
		return false
	}
}

func validTier(value EvidenceTier) bool {
	switch value {
	case TierCorroborated, TierNative, TierReconstructed, TierInferred:
		return true
	default:
		return false
	}
}

func validOutcome(value string) bool {
	switch value {
	case "succeeded", "failed", "cancelled", "interrupted", "timed_out", "abandoned", "unknown":
		return true
	default:
		return false
	}
}

func validValueState(value string) bool {
	switch value {
	case "observed", "unsupported", "not_observed", "redacted", "unknown", "numeric_zero":
		return true
	default:
		return false
	}
}

func validEventType(value string) bool {
	switch value {
	case "session.started", "prompt.submitted", "component.executed", "session.stopped",
		"model.requested", "model.responded", "component.installed", "component.loaded", "component.invoked",
		"source.observed",
		// "tool.called" is appended (never replacing component.executed) for
		// Gap A: it is the real, already-tested canonical event type both
		// codexadapter.CanonicalEventForOTel and
		// claudeadapter.CanonicalEventForOTel resolve codex.tool_result and
		// Claude's tool_result OTel events onto
		// (internal/codexadapter/otel.go, internal/claudeadapter/otel.go).
		// The fixture-agent lane's own tool_finished -> component.executed
		// mapping (normalize.go's canonicalEventTypes) is untouched.
		"tool.called":
		return true
	default:
		return false
	}
}

func expectedSourceSchema(kind SourceKind) string {
	switch kind {
	case SourceHook:
		return "fixture.agent-hook/1"
	case SourceTranscript:
		return "fixture.agent-transcript/1"
	case SourceOTLPLog, SourceOTLPSpan, SourceOTLPMetric:
		return fixtureOTLPSchema
	default:
		return ""
	}
}

func validSource(source SourceRef) bool {
	if !safeIDPattern.MatchString(source.AdapterID) ||
		!safeIDPattern.MatchString(source.AdapterVersion) ||
		!safeIDPattern.MatchString(source.SchemaID) ||
		!validSourceKind(source.Kind) ||
		!fingerprintPattern.MatchString(source.SchemaFingerprint) ||
		!installationPattern.MatchString(source.InstallationID) ||
		!pseudonymPattern.MatchString(source.NativeEventID) {
		return false
	}
	expected := expectedSourceSchema(source.Kind)
	if source.Kind == SourceAdapterBatch || source.Kind == SourceEvidenceBridge || source.SchemaID == expected {
		return true
	}
	if source.Kind == SourceHook {
		return source.SchemaID == "codex.hook/1" || source.SchemaID == "claude.hook/1"
	}
	if source.Kind == SourceOTLPLog || source.Kind == SourceOTLPSpan || source.Kind == SourceOTLPMetric {
		return source.SchemaID == "codex.otel/1" || source.SchemaID == "claude.otel/1"
	}
	return false
}

func validSubjectKind(kind string) bool {
	switch kind {
	case "", "component", "tool", "skill", "plugin", "mcp", "hook", "command", "agent":
		return true
	default:
		return false
	}
}

func validateEvent(event Event, factKey string) error {
	if event.SpecVersion != EventSpecVersion || !hex64Pattern.MatchString(event.FactKey) || event.FactKey != factKey ||
		event.EventID != "evt_"+event.FactKey[:32] || !hex32IDPattern.MatchString(event.EventID) || !validEventType(event.EventType) ||
		event.EmittedAt.IsZero() || event.ObservedAt.IsZero() || event.IngestedAt.IsZero() ||
		(event.TimestampQuality != "source_rfc3339" && event.TimestampQuality != "source_clock_skewed") || !validSource(event.Source) ||
		!devicePattern.MatchString(event.Scope.DeviceID) || event.Scope.AgentInstallationID != event.Source.InstallationID || !safeIDPattern.MatchString(event.Scope.SessionID) ||
		!validSubjectKind(event.Subject.Kind) ||
		(event.Measurements.DurationMS != nil && *event.Measurements.DurationMS < 0) ||
		(event.Measurements.PromptCharacterCount != nil && *event.Measurements.PromptCharacterCount < 0) ||
		(event.Measurements.InputTokens != nil && *event.Measurements.InputTokens < 0) ||
		(event.Measurements.CachedInputTokens != nil && *event.Measurements.CachedInputTokens < 0) ||
		(event.Measurements.OutputTokens != nil && *event.Measurements.OutputTokens < 0) ||
		(event.Measurements.ProviderCostMicros != nil && *event.Measurements.ProviderCostMicros < 0) ||
		(event.Measurements.Count != nil && *event.Measurements.Count < 0) ||
		!validValueState(event.ValueState) || !validOutcome(event.Outcome) || !validCorrelation(event.CorrelationStatus) {
		return errors.New("invalid_event")
	}
	expectedLifecycle := []EventStage{StageReceived, StageSanitized, StageValidated, StageNormalized, StageDeduped, StageCorrelated, StageReconciled}
	if len(event.Lifecycle) != len(expectedLifecycle) {
		return errors.New("invalid_event_lifecycle")
	}
	for index := range expectedLifecycle {
		if event.Lifecycle[index] != expectedLifecycle[index] {
			return errors.New("invalid_event_lifecycle")
		}
	}
	return nil
}

func validateEvidence(evidence Evidence, key string) error {
	if evidence.EvidenceID != key || !hex32IDPattern.MatchString(key) || !hex32IDPattern.MatchString(evidence.EventID) ||
		!validSource(evidence.Source) || !validTier(evidence.Tier) || evidence.Confidence < 0 || evidence.Confidence > 1 ||
		!validCompleteness(evidence.Completeness) || evidence.FirstSeenAt.IsZero() || evidence.LastSeenAt.Before(evidence.FirstSeenAt) ||
		evidence.Sanitizer != "kansoku.ingress-sanitizer/1" || !hex64Pattern.MatchString(evidence.PrivacySHA256) ||
		!validEventType(evidence.Assertion.EventType) || !validOutcome(evidence.Assertion.Outcome) || !validValueState(evidence.Assertion.ValueState) {
		return errors.New("invalid_evidence")
	}
	if evidence.Tier == TierReconstructed && evidence.Confidence > 0.95 || evidence.Tier == TierInferred && evidence.Confidence >= 0.9 {
		return errors.New("invalid_evidence_confidence")
	}
	return nil
}

func ValidateState(state DurableState) error {
	if state.SpecVersion != StoreSpecVersion || state.Facts == nil || state.Evidence == nil || state.Correlations == nil ||
		state.Quarantine == nil || state.Incidents == nil || state.Watermarks == nil || state.Checkpoints == nil {
		return errors.New("invalid_store_schema")
	}
	evidenceOwners := map[string]string{}
	eventIDs := map[string]struct{}{}
	contradiction := false
	for key, fact := range state.Facts {
		if err := validateEvent(fact.Event, key); err != nil || !validCompleteness(fact.Completeness) || len(fact.EvidenceIDs) == 0 {
			return fmt.Errorf("invalid_fact:%s", key)
		}
		eventIDs[fact.Event.EventID] = struct{}{}
		ordered := append([]string(nil), fact.EvidenceIDs...)
		sort.Strings(ordered)
		for index, evidenceID := range fact.EvidenceIDs {
			if index > 0 && evidenceID == fact.EvidenceIDs[index-1] || ordered[index] != evidenceID {
				return fmt.Errorf("invalid_fact_evidence_order:%s", key)
			}
			evidence, ok := state.Evidence[evidenceID]
			if !ok || evidence.EventID != fact.Event.EventID {
				return fmt.Errorf("invalid_evidence_reference:%s", evidenceID)
			}
			if _, owned := evidenceOwners[evidenceID]; owned {
				return fmt.Errorf("duplicate_evidence_reference:%s", evidenceID)
			}
			evidenceOwners[evidenceID] = key
			if evidence.Assertion.EventType != fact.Event.EventType || evidence.Assertion.Outcome != fact.Event.Outcome || evidence.Assertion.ValueState != fact.Event.ValueState {
				contradiction = true
			}
		}
		if expected := completenessForEvidence(fact.EvidenceIDs, state.Evidence, state.Watermarks); fact.Completeness != expected {
			return fmt.Errorf("invalid_fact_completeness:%s", key)
		}
	}
	for key, evidence := range state.Evidence {
		if err := validateEvidence(evidence, key); err != nil {
			return fmt.Errorf("invalid_evidence:%s", key)
		}
		if _, owned := evidenceOwners[key]; !owned {
			return fmt.Errorf("orphan_evidence:%s", key)
		}
	}
	contradictionIncident := false
	for key, incident := range state.Incidents {
		if key != incident.IncidentID || !hex32IDPattern.MatchString(key) || incident.Capability != "core_ingestion" ||
			!safeIDPattern.MatchString(incident.Category) || incident.Completeness != Degraded || incident.OpenedAt.IsZero() ||
			incident.LastObserved.Before(incident.OpenedAt) || incident.OccurrenceCount == 0 ||
			(incident.ResolvedAt != nil && incident.ResolvedAt.Before(incident.OpenedAt)) {
			return fmt.Errorf("invalid_incident:%s", key)
		}
		if incident.Category == "evidence_contradiction" {
			contradictionIncident = true
		}
	}
	if contradiction != contradictionIncident {
		return errors.New("contradiction_assertion_incident_mismatch")
	}
	for key, correlation := range state.Correlations {
		if key != correlation.CorrelationID || correlation.CorrelationID != "cor_"+stableID("correlation/1", correlation.EventID)[:32] ||
			!validCorrelation(correlation.Status) {
			return fmt.Errorf("invalid_correlation:%s", key)
		}
		if _, exists := eventIDs[correlation.EventID]; !exists {
			return fmt.Errorf("orphan_correlation:%s", key)
		}
		seen := map[string]struct{}{}
		for index, candidate := range correlation.Candidates {
			if !hex32IDPattern.MatchString(candidate.EventID) || candidate.Confidence < 0 || candidate.Confidence > 1 {
				return fmt.Errorf("invalid_correlation_candidate:%s", key)
			}
			if _, exists := eventIDs[candidate.EventID]; !exists {
				return fmt.Errorf("orphan_correlation_candidate:%s", key)
			}
			if _, duplicate := seen[candidate.EventID]; duplicate {
				return fmt.Errorf("duplicate_correlation_candidate:%s", key)
			}
			seen[candidate.EventID] = struct{}{}
			if index > 0 {
				previous := correlation.Candidates[index-1]
				if previous.Confidence < candidate.Confidence || previous.Confidence == candidate.Confidence && previous.EventID > candidate.EventID {
					return fmt.Errorf("unordered_correlation_candidates:%s", key)
				}
			}
		}
		if correlation.Status == CorrelationExact && len(correlation.Candidates) != 0 || correlation.Status == CorrelationCandidate && len(correlation.Candidates) != 1 || correlation.Status == CorrelationAmbiguous && len(correlation.Candidates) < 2 || correlation.Status == CorrelationUnmatched && len(correlation.Candidates) != 0 {
			return fmt.Errorf("invalid_correlation_cardinality:%s", key)
		}
	}
	for key, watermark := range state.Watermarks {
		if key != watermark.SourceID || !validSourceKind(SourceKind(key)) || !validSourceLifecycle(watermark.Lifecycle) ||
			watermark.LastDiscovered.IsZero() || watermark.ExpectedCadenceMS <= 0 || watermark.LastEmittedSequence > watermark.LastReadSequence || watermark.LastCommitted.IsZero() {
			return fmt.Errorf("invalid_watermark:%s", key)
		}
	}
	for key, checkpoint := range state.Checkpoints {
		if key != checkpoint.ImporterID || !safeIDPattern.MatchString(key) || checkpoint.Offset < 0 || checkpoint.Sequence == 0 || !hex64Pattern.MatchString(checkpoint.FileID) {
			return fmt.Errorf("invalid_checkpoint:%s", key)
		}
	}
	for index, quarantine := range state.Quarantine {
		if !hex32IDPattern.MatchString(quarantine.QuarantineID) || !validSourceKind(quarantine.SourceKind) || !fingerprintPattern.MatchString(quarantine.SchemaFingerprint) ||
			!safeIDPattern.MatchString(quarantine.Category) || quarantine.ByteCount < 0 || quarantine.RecordCount < 0 || quarantine.ObservedAt.IsZero() {
			return fmt.Errorf("invalid_quarantine:%d", index)
		}
	}
	return nil
}
