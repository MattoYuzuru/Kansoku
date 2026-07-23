package wayfinder_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/adaptersdk/wayfinder"
	"kansoku.local/kansoku/internal/privacy"
)

func testPseudonymKey() []byte {
	return []byte("wayfinder-conformance-test-key-0123456789abcdef")
}

type wayfinderEventFixture struct {
	AdapterID string `json:"adapter_id"`
	Discovery struct {
		AllowedRoots            []string `json:"allowed_roots"`
		PathsSubpath            string   `json:"paths_subpath"`
		ExpectedCandidateCount  int      `json:"expected_candidate_count"`
		ExpectedDetectionMethod string   `json:"expected_detection_method"`
	} `json:"discovery"`
	SessionID string `json:"session_id"`
	Events    []struct {
		EventID    string `json:"event_id"`
		SessionID  string `json:"session_id"`
		Sequence   int    `json:"sequence"`
		EventType  string `json:"event_type"`
		Outcome    string `json:"outcome"`
		RecipeName string `json:"recipe_name,omitempty"`
	} `json:"events"`
	UnknownSchemaEventType string `json:"unknown_schema_event_type"`
	UnknownSchemaEventID   string `json:"unknown_schema_event_id"`
	UnsupportedCapability  string `json:"unsupported_capability"`
}

func loadFixture(t *testing.T) wayfinderEventFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "session-07", "wayfinder-eventfile.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture wayfinderEventFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

// --- Manifest / distinctness -------------------------------------------------------

func TestManifestSchemaValidationAndDistinctAdapterID(t *testing.T) {
	adapter := wayfinder.New()
	manifest := adapter.Manifest()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := adaptersdk.ParseManifest(raw)
	if err != nil {
		t.Fatalf("valid manifest must parse: %v", err)
	}
	if parsed.ID != wayfinder.AdapterID {
		t.Fatal("manifest id round-trip mismatch")
	}
	forbidden := []string{"codex", "claude", "gemini", "cursor", "loomwright", "fixture-agent"}
	for _, id := range forbidden {
		if parsed.ID == id {
			t.Fatalf("wayfinder adapter id must not collide with any real adapter or prior fixture-agent vocabulary, got %q", id)
		}
	}
}

func TestManifestDeclaresNoOTelSourceOnlyEventFile(t *testing.T) {
	manifest := wayfinder.New().Manifest()
	if len(manifest.Sources) != 1 {
		t.Fatalf("wayfinder must declare exactly one source (no OTel), got %+v", manifest.Sources)
	}
	if manifest.Sources[0].Kind == "otlp_log_span_metric" || manifest.Sources[0].Kind == "hook_http" {
		t.Fatalf("wayfinder's one source must not be shaped like an OTel/hook source, got kind %q", manifest.Sources[0].Kind)
	}
	if manifest.Sources[0].Kind != "transcript_jsonl" {
		t.Fatalf("expected a versioned local event file source (transcript_jsonl), got %q", manifest.Sources[0].Kind)
	}
}

func TestManifestDeclaresTokenModelCostUnsupportedNeverFakedZero(t *testing.T) {
	manifest := wayfinder.New().Manifest()
	state, declared := manifest.Capabilities[adaptersdk.CapabilityActivityTokenModelCost]
	if !declared {
		t.Fatal("wayfinder must explicitly declare activity.token_model_cost, even as unsupported")
	}
	if state != "unsupported" {
		t.Fatalf("wayfinder genuinely lacks token/cost tracking; expected state %q, got %q", "unsupported", state)
	}
}

// --- Discovery ----------------------------------------------------------------------

func TestDiscoveryResolvesFromDocumentedEnvVarRootOnly(t *testing.T) {
	fixture := loadFixture(t)
	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "wayfinder-home")
	if err := os.MkdirAll(filepath.Join(resolvedRoot, fixture.Discovery.PathsSubpath), 0o700); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{resolvedRoot}, []string{"wayctl"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	adapter := wayfinder.New()
	candidates, err := adapter.Discover(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != fixture.Discovery.ExpectedCandidateCount {
		t.Fatalf("expected %d candidates, got %d", fixture.Discovery.ExpectedCandidateCount, len(candidates))
	}
	if string(candidates[0].DetectionMethod) != fixture.Discovery.ExpectedDetectionMethod {
		t.Fatal("detection method must be documented_env_var, never a speculative scan")
	}
	if candidates[0].StateRoot != resolvedRoot {
		t.Fatal("state root must be exactly the resolved allowed root")
	}
}

func TestDiscoveryNeverEscapesAllowedRoots(t *testing.T) {
	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "wayfinder-home")
	if err := os.MkdirAll(filepath.Join(resolvedRoot, "paths"), 0o700); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{resolvedRoot}, []string{"wayctl"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(tempRoot, "not-allowed")
	if err := os.MkdirAll(filepath.Join(outside, "paths"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := host.ReadProbe(filepath.Join(outside, "paths")); !errors.Is(err, adaptersdk.ErrOutsideAllowedRoots) {
		t.Fatal("read probe outside allowed roots must fail closed")
	}
}

// --- Inventory / vocabulary distinctness ---------------------------------------------

func TestInventoryUsesRecipeVocabularyNeverSkillOrThread(t *testing.T) {
	adapter := wayfinder.New()
	snapshot, err := adapter.Inventory(context.Background(), adaptersdk.Installation{InstallationID: "inst_1", AdapterID: wayfinder.AdapterID})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Nodes) == 0 || len(snapshot.Edges) == 0 {
		t.Fatal("inventory snapshot must contain nodes and edges")
	}
	raw, _ := json.Marshal(snapshot)
	serialized := string(raw)
	forbidden := []string{"codex", "claude", "gemini", "cursor", "loomwright", "thread", "spool", "skill.md", "SKILL.md"}
	for _, term := range forbidden {
		if contains(serialized, term) {
			t.Fatalf("wayfinder vocabulary leaked a term belonging to another agent/fixture: %s", term)
		}
	}
	var sawRecipe bool
	for _, node := range snapshot.Nodes {
		if node.Kind == adaptersdk.NodeSkillIdentity && node.DeclaredName == "brew-tea" {
			sawRecipe = true
		}
	}
	if !sawRecipe {
		t.Fatal("expected a recipe (skill-equivalent) node named brew-tea")
	}
}

func TestInventoryIsRegistrableAlongsideOtherAdaptersWithNoNewCoreBranch(t *testing.T) {
	registry := adaptersdk.NewRegistry()
	if err := registry.Register(wayfinder.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Get(wayfinder.AdapterID); err != nil {
		t.Fatal("wayfinder must be retrievable by its own adapter id via the unconditional Registry.Get path")
	}
	matrix := registry.CapabilityMatrix()
	if _, ok := matrix[wayfinder.AdapterID]; !ok {
		t.Fatal("registered wayfinder adapter must appear in the capability matrix")
	}
}

// --- Normalize: canonical mapping + graceful degradation on unknown schema -----------

func TestNormalizeMapsKnownWayfinderEventsOntoSharedCanonicalTypes(t *testing.T) {
	fixture := loadFixture(t)
	adapter := wayfinder.New()
	expected := map[string]string{
		"path.opened":      "session.started",
		"recipe.consulted": "component.invoked",
		"path.closed":      "session.stopped",
	}
	for _, event := range fixture.Events {
		if event.EventType == fixture.UnknownSchemaEventType {
			continue
		}
		record := privacy.SafeRecord{
			RecordID: event.EventID, IdempotencyKey: "idem_" + event.EventID, AdapterID: wayfinder.AdapterID,
			AdapterVersion: wayfinder.AdapterVersion, SourceSchemaID: "wayfinder.eventfile/1",
			ObservedAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(), Confidence: 0.9,
			EventType: event.EventType, Outcome: event.Outcome, ValueState: privacy.ValueUnsupported,
			Lineage: privacy.Lineage{SessionPseudonym: fixture.SessionID},
		}
		events, err := adapter.Normalize(context.Background(), record)
		if err != nil {
			t.Fatalf("event %q: unexpected error: %v", event.EventType, err)
		}
		if len(events) != 1 || events[0].EventType != expected[event.EventType] {
			t.Fatalf("event %q: expected canonical type %q, got %+v", event.EventType, expected[event.EventType], events)
		}
	}
}

func TestNormalizeQuarantinesTheOneUnknownSchemaEventWithoutError(t *testing.T) {
	fixture := loadFixture(t)
	adapter := wayfinder.New()
	record := privacy.SafeRecord{
		RecordID: fixture.UnknownSchemaEventID, IdempotencyKey: "idem_unknown", AdapterID: wayfinder.AdapterID,
		AdapterVersion: wayfinder.AdapterVersion, SourceSchemaID: "wayfinder.eventfile/1",
		ObservedAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(), Confidence: 0.9,
		EventType: fixture.UnknownSchemaEventType, Outcome: "succeeded", ValueState: privacy.ValueUnsupported,
		Lineage: privacy.Lineage{SessionPseudonym: fixture.SessionID},
	}
	if _, err := adapter.Normalize(context.Background(), record); !errors.Is(err, wayfinder.ErrUnknownEventSchema) {
		t.Fatalf("expected ErrUnknownEventSchema for %q, got %v", fixture.UnknownSchemaEventType, err)
	}
}

func TestUnknownSchemaEventAbsentFromDeclaredSourceSchema(t *testing.T) {
	fixture := loadFixture(t)
	schemas := wayfinder.New().SourceSchemas()
	if len(schemas) != 1 {
		t.Fatalf("expected exactly one declared source schema, got %d", len(schemas))
	}
	if _, present := schemas[0].EventTypes[fixture.UnknownSchemaEventType]; present {
		t.Fatalf("the deliberately unknown event type %q must be absent from the declared schema's EventTypes", fixture.UnknownSchemaEventType)
	}
}

// TestNormalizeBatchDegradesGracefullyNeverCrashesOrDropsWholeBatch is the
// concrete proof of property 5: a batch containing the fixture's four
// events (three recognized, one deliberately unknown) yields exactly three
// normalized canonical events and exactly one quarantined record id -- the
// call itself never panics/errors out for the whole batch, and no
// recognized sibling record is silently dropped alongside the quarantined
// one.
func TestNormalizeBatchDegradesGracefullyNeverCrashesOrDropsWholeBatch(t *testing.T) {
	fixture := loadFixture(t)
	adapter := wayfinder.New()
	var records []adaptersdk.SafeSourceRecord
	for _, event := range fixture.Events {
		records = append(records, privacy.SafeRecord{
			RecordID: event.EventID, IdempotencyKey: "idem_" + event.EventID, AdapterID: wayfinder.AdapterID,
			AdapterVersion: wayfinder.AdapterVersion, SourceSchemaID: "wayfinder.eventfile/1",
			ObservedAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(), Confidence: 0.9,
			EventType: event.EventType, Outcome: event.Outcome, ValueState: privacy.ValueUnsupported,
			Lineage: privacy.Lineage{SessionPseudonym: fixture.SessionID},
		})
	}
	result := adapter.NormalizeBatch(context.Background(), records)
	if len(result.Events) != len(fixture.Events)-1 {
		t.Fatalf("expected %d normalized events (all but the unknown-schema one), got %d", len(fixture.Events)-1, len(result.Events))
	}
	if len(result.QuarantinedRecordIDs) != 1 || result.QuarantinedRecordIDs[0] != fixture.UnknownSchemaEventID {
		t.Fatalf("expected exactly one quarantined record id %q, got %+v", fixture.UnknownSchemaEventID, result.QuarantinedRecordIDs)
	}
}

// --- Non-UUID session identifiers -----------------------------------------------------

func TestSessionIdentifiersAreNonUUIDShortSequenceTokens(t *testing.T) {
	fixture := loadFixture(t)
	if fixture.SessionID != "wf-session-1" {
		t.Fatalf("expected the fixture session id to be the documented non-UUID shape wf-session-1, got %q", fixture.SessionID)
	}
	if contains(fixture.SessionID, "-") && len(fixture.SessionID) == 36 {
		t.Fatal("session id must not accidentally be UUID-shaped")
	}
	next := wayfinder.NextSessionID(fixture.SessionID)
	if next != "wf-session-2" {
		t.Fatalf("expected monotonic next session id wf-session-2, got %q", next)
	}
	seed := wayfinder.NextSessionID("")
	if seed != "wf-session-1" {
		t.Fatalf("expected seed session id wf-session-1, got %q", seed)
	}
}

func TestCanonicalEventRoundTripPreservesNonUUIDSessionPseudonym(t *testing.T) {
	// The canonical SafeRecord/Lineage pipeline must not assume a UUID
	// session-id shape anywhere: round-tripping a non-UUID session
	// pseudonym through Normalize must leave it untouched.
	record := privacy.SafeRecord{
		RecordID: "rec_x", IdempotencyKey: "idem_x", AdapterID: wayfinder.AdapterID,
		AdapterVersion: wayfinder.AdapterVersion, SourceSchemaID: "wayfinder.eventfile/1",
		ObservedAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(), Confidence: 0.9,
		EventType: "path.opened", Outcome: "succeeded", ValueState: privacy.ValueUnsupported,
		Lineage: privacy.Lineage{SessionPseudonym: "wf-session-1"},
	}
	events, err := wayfinder.New().Normalize(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Lineage.SessionPseudonym != "wf-session-1" {
		t.Fatal("non-UUID session pseudonym must round-trip through Normalize unchanged")
	}
}

// --- Reconcile / Audit -----------------------------------------------------------------

func TestReconcileIsIdempotentAndDeterministic(t *testing.T) {
	adapter := wayfinder.New()
	previous := adaptersdk.InventorySnapshot{
		SnapshotID: "snap_prev",
		Nodes: []adaptersdk.Node{
			{NodeID: "node_a", Fingerprint: "fp1"},
			{NodeID: "node_b", Fingerprint: "fp2"},
		},
	}
	current := adaptersdk.InventorySnapshot{
		SnapshotID: "snap_curr",
		Nodes: []adaptersdk.Node{
			{NodeID: "node_a", Fingerprint: "fp1"},
			{NodeID: "node_b", Fingerprint: "fp2_changed"},
			{NodeID: "node_c", Fingerprint: "fp3"},
		},
	}
	scope := adaptersdk.ReconcileScope{InstallationID: "inst_1"}
	first := adapter.Reconcile(context.Background(), scope, previous, current)
	second := adapter.Reconcile(context.Background(), scope, previous, current)
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("reconcile must be idempotent")
	}
	if len(first.AddedNodeIDs) != 1 || first.AddedNodeIDs[0] != "node_c" {
		t.Fatalf("unexpected added set: %+v", first.AddedNodeIDs)
	}
	if len(first.ChangedNodeIDs) != 1 || first.ChangedNodeIDs[0] != "node_b" {
		t.Fatalf("unexpected changed set: %+v", first.ChangedNodeIDs)
	}
}

func TestAuditReportsTokenModelCostAsSkippedUnsupportedNeverFakedPass(t *testing.T) {
	adapter := wayfinder.New()
	results := adapter.Audit(context.Background(), adaptersdk.Installation{InstallationID: "inst_1"}, adaptersdk.AuditFixtureReplay)
	if len(results) == 0 {
		t.Fatal("audit must return at least one check result")
	}
	var sawTokenCheck bool
	for _, result := range results {
		if result.CapabilityID == adaptersdk.CapabilityActivityTokenModelCost {
			sawTokenCheck = true
			if result.Status != adaptersdk.CheckSkippedUnsupported {
				t.Fatalf("token/model/cost audit must be skipped_unsupported, never a fabricated pass/fail, got %q", result.Status)
			}
		}
	}
	if !sawTokenCheck {
		t.Fatal("expected an explicit skipped_unsupported audit result for activity.token_model_cost")
	}
}

// --- No database credential -------------------------------------------------------------

func TestManifestNeverExposesDatabaseCredentials(t *testing.T) {
	adapter := wayfinder.New()
	manifest := adapter.Manifest()
	raw, _ := json.Marshal(manifest)
	forbidden := []string{"password", "credential", "connection_string", "dsn", "secret"}
	serialized := string(raw)
	for _, term := range forbidden {
		if contains(serialized, term) {
			t.Fatalf("manifest must never mention a credential-shaped field: %s", term)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
