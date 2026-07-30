package observability

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/privacy"
)

func TestBridgeAssertionSinkUsesCanonicalFactAndMetadataQuarantineTransactions(t *testing.T) {
	store, err := OpenFileStore(filepath.Join(t.TempDir(), "state.json"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, err := NewIngestor(store, bytes.Repeat([]byte("b"), 32), privacy.DefaultLimits(), 2)
	if err != nil {
		t.Fatal(err)
	}
	sink, err := NewBridgeAssertionSink(ingestor)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 15, 0, 0, 0, time.UTC)
	pseudonym := "hmac-sha256:" + string(bytes.Repeat([]byte("a"), 64))
	record := privacy.SafeRecord{
		RecordID: pseudonym, IdempotencyKey: pseudonym,
		AdapterID: "loomwright", AdapterVersion: "9.4.1",
		SourceSchemaID: "loomwright.bridge/7", SchemaFingerprint: pseudonym,
		ObservedAt: now, ReceivedAt: now, Confidence: 1,
		EventType: "source.observed", Outcome: "unknown", ValueState: privacy.ValueObserved,
		Model: privacy.CatalogObservation{State: privacy.ObservationNotObserved},
		Tool:  privacy.CatalogObservation{State: privacy.ObservationNotObserved},
		Lineage: privacy.Lineage{
			SourceRecordPseudonym: pseudonym, SessionPseudonym: pseudonym,
			AdapterID: "loomwright", AdapterVersion: "9.4.1",
			SourceSchemaID: "loomwright.bridge/7", SchemaFingerprint: pseudonym,
			SanitizerVersion: "kansoku.ingress-sanitizer/1",
			ContractSHA256:   privacy.PrivacyContractSemanticSHA256,
		},
	}
	if err := sink.Accept(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if err := sink.Reject(context.Background(), adaptersdk.BridgeRejection{
		BridgeID: "star-pulse", SchemaVersion: "7",
		SchemaFingerprint: pseudonym, Category: "unknown_constellation_schema",
		ByteCount: 18, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 || len(state.Evidence) != 1 ||
		len(state.Quarantine) != 1 || len(state.Incidents) != 1 {
		t.Fatalf("facts=%d evidence=%d quarantine=%d incidents=%d",
			len(state.Facts), len(state.Evidence), len(state.Quarantine), len(state.Incidents))
	}
}

func TestSanitizedRolloutRecordUsesExplicitInventoryInstallationBinding(t *testing.T) {
	store, err := OpenFileStore(filepath.Join(t.TempDir(), "state.json"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, err := NewIngestor(
		store, bytes.Repeat([]byte("r"), 32), privacy.DefaultLimits(), 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	pseudonym := "hmac-sha256:" + string(bytes.Repeat([]byte("c"), 64))
	skill := "search-workflow"
	record := privacy.SafeRecord{
		RecordID: pseudonym, IdempotencyKey: pseudonym,
		AdapterID: "codex", AdapterVersion: "0.145.0",
		SourceSchemaID: "codex.rollout/2", SchemaFingerprint: pseudonym,
		ObservedAt: now, ReceivedAt: now, Confidence: .85,
		EventType: "component.requested", Outcome: "unknown",
		ValueState:    privacy.ValueObserved,
		Model:         privacy.CatalogObservation{State: privacy.ObservationNotObserved},
		Tool:          privacy.CatalogObservation{State: privacy.ObservationObserved, ID: &skill},
		ComponentKind: "skill",
		ComponentEvidence: privacy.ComponentEvidenceMetadata{
			QualifiedIdentity: skill, IdentitySource: "rollout_marker",
			InvocationMode: "requested",
		},
		Lineage: privacy.Lineage{
			SourceRecordPseudonym: pseudonym, SessionPseudonym: pseudonym,
			AdapterID: "codex", AdapterVersion: "0.145.0",
			SourceSchemaID: "codex.rollout/2", SchemaFingerprint: pseudonym,
			SanitizerVersion: "kansoku.ingress-sanitizer/1",
			ContractSHA256:   privacy.PrivacyContractSemanticSHA256,
		},
	}
	const installationID = "ain_codex_final_20260729"
	if _, err := ingestor.IngestSanitizedRolloutRecordForInstallation(
		record, 7, installationID,
	); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if len(state.Facts) != 1 {
		t.Fatalf("facts=%d", len(state.Facts))
	}
	for _, fact := range state.Facts {
		if fact.Event.Source.InstallationID != installationID ||
			fact.Event.Scope.AgentInstallationID != installationID {
			t.Fatalf("source=%#v scope=%#v", fact.Event.Source, fact.Event.Scope)
		}
	}
}

func TestExplicitInstallationIDUsesBoundedOpaqueAlphabet(t *testing.T) {
	for _, value := range []string{
		"ain_codex_final_20260729",
		"ain_fixture",
		"ain_0123456789abcdef0123456789abcdef",
	} {
		if !installationPattern.MatchString(value) {
			t.Fatalf("safe explicit installation %q rejected", value)
		}
	}
	for _, value := range []string{
		"ain_",
		"ain_/unsafe",
		"ain_unsafe@owner",
		"ain_unsafe|owner",
		"ain_unsafe..owner",
		"ain_" + string(bytes.Repeat([]byte("a"), 125)),
	} {
		if installationPattern.MatchString(value) {
			t.Fatalf("unsafe explicit installation %q accepted", value)
		}
	}
}
