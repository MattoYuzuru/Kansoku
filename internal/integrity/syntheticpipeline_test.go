package integrity_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/integrity"
	"kansoku.local/kansoku/internal/localhttp"
	"kansoku.local/kansoku/internal/observability"
	"kansoku.local/kansoku/internal/privacy"
)

// syntheticTestBearer is a fixed >=32-byte bearer token, matching
// localhttp.NewGuard's own closed length validation.
var syntheticTestBearer = bytes.Repeat([]byte("b"), 32)

func newSyntheticPipelineHarness(t *testing.T) *integrity.SyntheticPipelineCheck {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := observability.OpenFileStore(path, 4<<20)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	ingestor, err := observability.NewIngestor(store, bytes.Repeat([]byte("k"), 32), privacy.DefaultLimits(), 4)
	if err != nil {
		t.Fatalf("NewIngestor: %v", err)
	}
	receiver, err := observability.NewOTLPReceiver(ingestor, 1<<20)
	if err != nil {
		t.Fatalf("NewOTLPReceiver: %v", err)
	}
	guard, err := localhttp.NewGuard(
		[]string{"127.0.0.1", "::1", "localhost"},
		[]string{"http://127.0.0.1:3000", "http://[::1]:3000", "http://localhost:3000"},
		syntheticTestBearer, bytes.Repeat([]byte("c"), 32), 1<<20, 120, time.Minute,
	)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	return integrity.NewSyntheticPipelineCheck(guard, ingestor, receiver, store, syntheticTestBearer)
}

// TestSyntheticPipelineCheckSendsThroughRealIngressAndVerifiesDurableAppearance
// proves Evaluate sends its tagged hook+OTLP records through the REAL public
// "/v1/hooks/{adapter}/{event}" and "/v1/logs" ingress (never a parallel
// test-only ingress), observes both land as durable Facts, and reports pass.
func TestSyntheticPipelineCheckSendsThroughRealIngressAndVerifiesDurableAppearance(t *testing.T) {
	check := newSyntheticPipelineHarness(t)
	in := integrity.CheckInput{AuditRunID: "run-synthetic-1", Now: time.Now()}
	targets, err := check.Targets(context.Background(), in)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("Targets = %+v, want exactly one shared-pipeline target", targets)
	}
	outcome, err := check.Evaluate(context.Background(), in, targets[0])
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusPass {
		t.Fatalf("outcome = %+v, want pass: both synthetic records must land durably through the real ingress", outcome)
	}
	if outcome.CheckID != integrity.SyntheticPipelineCheckID {
		t.Fatalf("CheckID = %s, want %s", outcome.CheckID, integrity.SyntheticPipelineCheckID)
	}
}

// TestSyntheticPipelineCheckExpiresRecordsAfterVerification proves that once
// Evaluate returns, the tagged synthetic facts it created are no longer
// present in the durable store -- the explicit test-namespace retention path
// (FileStore.PurgeFacts) actually ran and left no lingering synthetic rows
// behind, so they can never later be miscounted as real usage.
func TestSyntheticPipelineCheckExpiresRecordsAfterVerification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := observability.OpenFileStore(path, 4<<20)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	ingestor, err := observability.NewIngestor(store, bytes.Repeat([]byte("k"), 32), privacy.DefaultLimits(), 4)
	if err != nil {
		t.Fatalf("NewIngestor: %v", err)
	}
	receiver, err := observability.NewOTLPReceiver(ingestor, 1<<20)
	if err != nil {
		t.Fatalf("NewOTLPReceiver: %v", err)
	}
	guard, err := localhttp.NewGuard(
		[]string{"127.0.0.1", "::1", "localhost"},
		[]string{"http://127.0.0.1:3000", "http://[::1]:3000", "http://localhost:3000"},
		syntheticTestBearer, bytes.Repeat([]byte("c"), 32), 1<<20, 120, time.Minute,
	)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	check := integrity.NewSyntheticPipelineCheck(guard, ingestor, receiver, store, syntheticTestBearer)

	before := store.Snapshot()
	beforeFixtureCount := countFixtureAdapterFacts(before)

	outcome, err := check.Evaluate(context.Background(), integrity.CheckInput{AuditRunID: "run-synthetic-2", Now: time.Now()}, integrity.CheckTarget{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusPass {
		t.Fatalf("outcome = %+v, want pass", outcome)
	}

	after := store.Snapshot()
	afterFixtureCount := countFixtureAdapterFacts(after)
	if afterFixtureCount != beforeFixtureCount {
		t.Fatalf("fixture-adapter fact count changed from %d to %d after Evaluate returned -- synthetic probe records must be purged, never left lingering", beforeFixtureCount, afterFixtureCount)
	}
}

// TestSyntheticPipelineCheckRecordsAreExcludedFromRealReconciliationQueries
// proves ExcludeTestNamespace's filter (Source.AdapterID ==
// observability.FixtureAdapterID) removes every fact this check's own probe
// creates while retaining an installed-adapter fact of a different
// AdapterID -- exactly the discriminator a real reconciliation/usage-
// aggregate query is expected to apply.
func TestSyntheticPipelineCheckRecordsAreExcludedFromRealReconciliationQueries(t *testing.T) {
	check := newSyntheticPipelineHarness(t)

	outcome, err := check.Evaluate(context.Background(), integrity.CheckInput{AuditRunID: "run-synthetic-3", Now: time.Now()}, integrity.CheckTarget{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusPass {
		t.Fatalf("outcome = %+v, want pass", outcome)
	}
	// DetailRef reports how many synthetic facts Evaluate itself verified
	// durably before purging them; that count must be nonzero, otherwise
	// this test would be vacuously asserting exclusion over zero probe facts.
	if !strings.Contains(outcome.DetailRef, "verified_facts=4") {
		t.Fatalf("DetailRef = %q, want verified_facts=4 (hook plus OTLP log/span/metric)", outcome.DetailRef)
	}

	// Build a representative sample combining an installed-adapter fact
	// (standing in for genuine Codex/Claude usage already reconciled by an
	// earlier stage, tagged with a distinct AdapterID) and a fact shaped
	// exactly like the probe's own verified rows (Source.AdapterID ==
	// observability.FixtureAdapterID, the identity IngestHook/OTLP ingestion
	// for the fixture-agent lane always produces and which Evaluate itself
	// just purged from the live store above).
	sample := map[string]observability.Fact{
		"installed-fact-key": {
			Event: observability.Event{
				EventID: "evt_installed_adapter_real_fact",
				Source:  observability.SourceRef{AdapterID: "codex", InstallationID: "install-real-1"},
			},
		},
		"fixture-agent-probe-fact-key": {
			Event: observability.Event{
				EventID: "evt_synthetic_probe_fact",
				Source:  observability.SourceRef{AdapterID: observability.FixtureAdapterID, InstallationID: "kansoku-synthetic-probe"},
			},
		},
	}

	excluded := integrity.ExcludeTestNamespace(sample)
	if _, stillPresent := excluded["installed-fact-key"]; !stillPresent {
		t.Fatalf("ExcludeTestNamespace removed an installed-adapter fact that was never fixture-agent-tagged")
	}
	if _, stillPresent := excluded["fixture-agent-probe-fact-key"]; stillPresent {
		t.Fatalf("ExcludeTestNamespace still returned a fixture-agent (synthetic-namespace) fact")
	}
	for key, fact := range excluded {
		if fact.Event.Source.AdapterID == observability.FixtureAdapterID {
			t.Fatalf("ExcludeTestNamespace still returned a fixture-agent fact at key %s: %+v", key, fact)
		}
	}
}

func countFixtureAdapterFacts(state observability.DurableState) int {
	count := 0
	for _, fact := range state.Facts {
		if fact.Event.Source.AdapterID == observability.FixtureAdapterID {
			count++
		}
	}
	return count
}

// TestSyntheticPipelineCheckNotWiredFailsClosed proves a
// SyntheticPipelineCheck constructed without a fully wired guard/ingestor/
// receiver/store quadruple (e.g. before production wiring completes) is
// reported as a failed production dependency rather than pretending that a
// real probe ran.
func TestSyntheticPipelineCheckNotWiredFailsClosed(t *testing.T) {
	check := &integrity.SyntheticPipelineCheck{Now: time.Now}
	outcome, err := check.Evaluate(context.Background(), integrity.CheckInput{Now: time.Now()}, integrity.CheckTarget{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusFail {
		t.Fatalf("outcome = %+v, want fail when the probe is not wired", outcome)
	}
}
