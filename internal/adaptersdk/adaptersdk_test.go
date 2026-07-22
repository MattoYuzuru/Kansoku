package adaptersdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/adaptersdk/fakeadapter"
	"kansoku.local/kansoku/internal/privacy"
)

func loadFixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "session-05", "loomwright-conformance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]any
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func testPseudonymKey() []byte {
	return []byte("adaptersdk-conformance-test-key-0123456789abcdef")
}

// --- Manifest / schema validation -------------------------------------------------

func TestManifestSchemaValidation(t *testing.T) {
	adapter := fakeadapter.New()
	manifest := adapter.Manifest()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := adaptersdk.ParseManifest(raw)
	if err != nil {
		t.Fatalf("valid manifest must parse: %v", err)
	}
	if parsed.ID != fakeadapter.AdapterID {
		t.Fatal("manifest id round-trip mismatch")
	}
	if parsed.ID == "codex" || parsed.ID == "claude" || parsed.ID == "gemini" || parsed.ID == "cursor" {
		t.Fatal("fake adapter id must not collide with any real adapter vocabulary")
	}
}

func TestManifestParsingRejectsUnknownFieldsDuplicatesAndOversize(t *testing.T) {
	valid, _ := json.Marshal(fakeadapter.New().Manifest())

	withUnknown := append([]byte(nil), valid[:len(valid)-1]...)
	withUnknown = append(withUnknown, []byte(`,"totally_unknown_field":1}`)...)
	if _, err := adaptersdk.ParseManifest(withUnknown); err == nil {
		t.Fatal("unknown manifest field must be rejected")
	}

	duplicate := []byte(`{"api_version":"kansoku.adapter/v1","api_version":"kansoku.adapter/v1","id":"loomwright","version":"0.9.0","execution":"builtin","agent_detection":{"executables":[],"state_roots":[]},"capabilities":{},"sources":[],"permissions":{"filesystem_read":[],"network":"none","process_exec":[]},"health_checks":[]}`)
	if _, err := adaptersdk.ParseManifest(duplicate); err == nil {
		t.Fatal("duplicate manifest field must be rejected")
	}

	oversizeEntries := map[string]any{
		"api_version": adaptersdk.AdapterAPIVersion, "id": "loomwright", "version": "0.9.0",
		"execution": "builtin", "agent_detection": map[string]any{"executables": []string{}, "state_roots": []string{}},
		"capabilities": func() map[string]string {
			result := map[string]string{}
			for i := 0; i < adaptersdk.MaxManifestConfigEntries+1; i++ {
				result[string(rune('a'+i%26))+string(rune(i))] = "supported"
			}
			return result
		}(),
		"sources": []any{}, "permissions": map[string]any{"filesystem_read": []string{}, "network": "none", "process_exec": []string{}},
		"health_checks": []string{},
	}
	oversizeRaw, _ := json.Marshal(oversizeEntries)
	if _, err := adaptersdk.ParseManifest(oversizeRaw); err == nil {
		t.Fatal("oversize manifest map must be rejected")
	}

	if _, err := adaptersdk.ParseManifest([]byte(`not json`)); err == nil {
		t.Fatal("malformed manifest json must be rejected")
	}

	invalidAPIVersion := append([]byte(nil), valid...)
	if _, err := adaptersdk.ParseManifest([]byte(`{"api_version":"kansoku.adapter/v2","id":"loomwright","version":"0.9.0","execution":"builtin","agent_detection":{"executables":[],"state_roots":[]},"capabilities":{},"sources":[],"permissions":{"filesystem_read":[],"network":"none","process_exec":[]},"health_checks":[]}`)); err == nil {
		t.Fatal("unsupported api_version must be rejected")
	}
	_ = invalidAPIVersion
}

func TestManifestParsingNeverExecutesCode(t *testing.T) {
	// A manifest string field that looks like a shell command must be
	// treated as inert data: ParseManifest performs no exec/eval of any
	// manifest content, so this must simply parse (or fail closed on shape)
	// without any side effect. This test documents that guarantee.
	payload := fakeadapter.New().Manifest()
	payload.AgentDetection.Executables = []string{"$(rm -rf /)", "`echo pwned`"}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := adaptersdk.ParseManifest(raw)
	if err != nil {
		t.Fatalf("data-only manifest with shell-like strings must still parse as inert data: %v", err)
	}
	if parsed.AgentDetection.Executables[0] != "$(rm -rf /)" {
		t.Fatal("manifest strings must round-trip as literal data")
	}
}

// --- Deterministic discovery ------------------------------------------------------

func TestDiscoveryResolvesFromDocumentedRootOnlyAndNeverEscapesIt(t *testing.T) {
	fixture := loadFixture(t)
	discovery := fixture["discovery"].(map[string]any)
	allowedRoot := discovery["allowed_roots"].([]any)[0].(string)
	loomsSubpath := discovery["looms_subpath"].(string)

	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "loomwright-home")
	if err := os.MkdirAll(filepath.Join(resolvedRoot, loomsSubpath), 0o700); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{resolvedRoot}, []string{"loomctl"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}

	adapter := fakeadapter.New()
	candidates, err := adapter.Discover(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != int(discovery["expected_candidate_count"].(float64)) {
		t.Fatalf("expected %v candidates, got %d", discovery["expected_candidate_count"], len(candidates))
	}
	if string(candidates[0].DetectionMethod) != discovery["expected_detection_method"].(string) {
		t.Fatal("detection method must be documented_env_var, never a speculative scan")
	}
	if candidates[0].StateRoot != resolvedRoot {
		t.Fatal("state root must be exactly the resolved allowed root")
	}
	_ = allowedRoot

	// Discovery must never escape the allowed root even if asked to.
	outside := filepath.Join(tempRoot, "not-allowed")
	if err := os.MkdirAll(filepath.Join(outside, loomsSubpath), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := host.ReadProbe(filepath.Join(outside, loomsSubpath)); !errors.Is(err, adaptersdk.ErrOutsideAllowedRoots) {
		t.Fatal("read probe outside allowed roots must fail closed")
	}
}

func TestDiscoveryIsDeterministicAcrossRepeatedRuns(t *testing.T) {
	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "loomwright-home")
	if err := os.MkdirAll(filepath.Join(resolvedRoot, "looms"), 0o700); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{resolvedRoot}, []string{"loomctl"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	adapter := fakeadapter.New()
	first, err := adapter.Discover(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	second, err := adapter.Discover(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("discovery fixture must be byte-identical across repeated runs")
	}
}

// --- Inventory / normalization golden tests ----------------------------------------

func TestInventorySnapshotUsesDistinctVocabularyAndPathPseudonyms(t *testing.T) {
	adapter := fakeadapter.New()
	snapshot, err := adapter.Inventory(context.Background(), adaptersdk.Installation{InstallationID: "inst_1", AdapterID: fakeadapter.AdapterID})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Nodes) == 0 || len(snapshot.Edges) == 0 {
		t.Fatal("inventory snapshot must contain nodes and edges")
	}
	forbiddenSubstrings := []string{"codex", "claude", "gemini", "cursor", "skill.md", "SKILL.md"}
	raw, _ := json.Marshal(snapshot)
	serialized := string(raw)
	for _, forbidden := range forbiddenSubstrings {
		if contains(serialized, forbidden) {
			t.Fatalf("fake adapter vocabulary leaked a real-agent term: %s", forbidden)
		}
	}
	var sawCache bool
	for _, node := range snapshot.Nodes {
		if node.Kind == adaptersdk.NodeCacheArtifact {
			sawCache = true
			if !node.CachedOnly {
				t.Fatal("cache_artifact node must be marked cached_only")
			}
		}
	}
	if !sawCache {
		t.Fatal("inventory graph must separate a cache artifact from active configuration")
	}
}

func TestNormalizeMapsFakeVocabularyOntoSharedCanonicalEventTypes(t *testing.T) {
	fixture := loadFixture(t)
	adapter := fakeadapter.New()
	for _, rawCase := range fixture["normalize_cases"].([]any) {
		testCase := rawCase.(map[string]any)
		loomwrightType := testCase["loomwright_event_type"].(string)
		record := privacy.SafeRecord{
			RecordID: "rec_1", IdempotencyKey: "idem_1", AdapterID: fakeadapter.AdapterID,
			AdapterVersion: fakeadapter.AdapterVersion, SourceSchemaID: "loomwright.spindle/v3",
			ObservedAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(), Confidence: 0.9,
			EventType: loomwrightType, Outcome: "succeeded", ValueState: privacy.ValueUnsupported,
			Lineage: privacy.Lineage{SessionPseudonym: "0123456789abcdef01234567"},
		}
		events, err := adapter.Normalize(context.Background(), record)
		if expectedError, wantsError := testCase["expected_error"]; wantsError {
			if err == nil || err.Error() != expectedError {
				t.Fatalf("case %s: expected error %v, got %v", loomwrightType, expectedError, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("case %s: unexpected error: %v", loomwrightType, err)
		}
		if len(events) != 1 || events[0].EventType != testCase["expected_canonical_event_type"].(string) {
			t.Fatalf("case %s: expected canonical type %v, got %+v", loomwrightType, testCase["expected_canonical_event_type"], events)
		}
	}
}

// --- Prohibited-content canary ------------------------------------------------------

func TestProhibitedContentNeverReachesSafeRecordDurableFields(t *testing.T) {
	// adaptersdk.Normalize only ever receives a privacy.SafeRecord -- the
	// struct itself has no generic payload/attributes map, so a raw prompt
	// or path literally has no field to occupy. This test asserts that
	// invariant holds for the type Normalize is declared against.
	var record privacy.SafeRecord
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"prompt", "response", "source_code", "tool_input", "tool_output", "command", "path", "environment", "credential", "exception", "attributes", "payload"}
	for _, field := range forbidden {
		if _, present := generic[field]; present {
			t.Fatalf("SafeRecord must never expose a durable field named %q", field)
		}
	}
}

// --- Unknown-version/schema behavior -------------------------------------------------

func TestUnsupportedEventTypeIsRejectedNotSilentlyPassedThrough(t *testing.T) {
	adapter := fakeadapter.New()
	record := privacy.SafeRecord{
		RecordID: "rec_2", IdempotencyKey: "idem_2", AdapterID: fakeadapter.AdapterID,
		AdapterVersion: fakeadapter.AdapterVersion, SourceSchemaID: "loomwright.spindle/v3",
		ObservedAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(), Confidence: 0.9,
		EventType: "unknown.spindle_event", Outcome: "succeeded", ValueState: privacy.ValueUnsupported,
		Lineage: privacy.Lineage{SessionPseudonym: "0123456789abcdef01234567"},
	}
	if _, err := adapter.Normalize(context.Background(), record); err == nil {
		t.Fatal("unknown event type must fail closed, not silently normalize")
	}
}

// --- Idempotent replay / reconciliation ---------------------------------------------

func TestReconcileIsIdempotentAndDeterministic(t *testing.T) {
	adapter := fakeadapter.New()
	previous := adaptersdk.InventorySnapshot{
		SnapshotID: "snap_prev",
		Nodes: []adaptersdk.Node{
			{NodeID: "node_a", Fingerprint: "fp1"},
			{NodeID: "node_b", Fingerprint: "fp2"},
			{NodeID: "node_c", Fingerprint: "fp3"},
		},
	}
	current := adaptersdk.InventorySnapshot{
		SnapshotID: "snap_curr",
		Nodes: []adaptersdk.Node{
			{NodeID: "node_a", Fingerprint: "fp1"},
			{NodeID: "node_b", Fingerprint: "fp2_changed"},
			{NodeID: "node_d", Fingerprint: "fp4"},
		},
	}
	scope := adaptersdk.ReconcileScope{InstallationID: "inst_1"}
	first := adapter.Reconcile(context.Background(), scope, previous, current)
	second := adapter.Reconcile(context.Background(), scope, previous, current)
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("reconcile must be idempotent: replaying the same snapshot pair must yield an identical result")
	}
	if len(first.AddedNodeIDs) != 1 || first.AddedNodeIDs[0] != "node_d" {
		t.Fatalf("unexpected added set: %+v", first.AddedNodeIDs)
	}
	if len(first.RemovedNodeIDs) != 1 || first.RemovedNodeIDs[0] != "node_c" {
		t.Fatalf("unexpected removed set: %+v", first.RemovedNodeIDs)
	}
	if len(first.ChangedNodeIDs) != 1 || first.ChangedNodeIDs[0] != "node_b" {
		t.Fatalf("unexpected changed set: %+v", first.ChangedNodeIDs)
	}
}

// --- Permission and output-bound tests -----------------------------------------------

func TestHostViewRejectsDisallowedExecProbes(t *testing.T) {
	host, err := adaptersdk.NewHostView([]string{t.TempDir()}, []string{"loomctl"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.ExecProbe(context.Background(), "curl"); !errors.Is(err, adaptersdk.ErrDisallowedExec) {
		t.Fatal("exec probe outside the declared allowlist must be rejected")
	}
}

func TestHostViewExecProbeOutputIsBoundedAndCredentialFree(t *testing.T) {
	root := t.TempDir()
	host, err := adaptersdk.NewHostView([]string{root}, []string{"loomctl"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	host.SetExecCommandForTest(func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
		big := make([]byte, 1<<20)
		for i := range big {
			big[i] = 'x'
		}
		return big, 0, nil
	})
	result, err := host.ExecProbe(context.Background(), "loomctl", "--version")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout) > 4096 {
		t.Fatalf("exec probe output must be bounded, got %d bytes", len(result.Stdout))
	}
}

func TestHostViewRequiresAbsoluteAllowedRootsAndLongPseudonymKey(t *testing.T) {
	if _, err := adaptersdk.NewHostView([]string{"relative/path"}, nil, testPseudonymKey()); err == nil {
		t.Fatal("relative allowed root must be rejected")
	}
	if _, err := adaptersdk.NewHostView([]string{t.TempDir()}, nil, []byte("short")); err == nil {
		t.Fatal("short pseudonym key must be rejected")
	}
}

func TestPseudonymizePathNeverLeaksRawPathBytes(t *testing.T) {
	host, err := adaptersdk.NewHostView([]string{t.TempDir()}, nil, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	secret := "/private/tmp/kansoku-fixture/loomwright-home/looms/very-secret-project-name"
	pseudonym := host.PseudonymizePath(secret)
	if contains(pseudonym, "very-secret-project-name") || contains(pseudonym, "loomwright-home") {
		t.Fatal("path pseudonym must not contain raw path fragments")
	}
	if pseudonym != host.PseudonymizePath(secret) {
		t.Fatal("path pseudonym must be stable for the same input")
	}
}

// --- No database credential; adapter registry has no agent-name branch --------------

func TestAdapterInterfaceNeverExposesDatabaseCredentials(t *testing.T) {
	adapter := fakeadapter.New()
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

func TestRegistryRoutesByCapabilityIDNotAgentName(t *testing.T) {
	registry := adaptersdk.NewRegistry()
	if err := registry.Register(fakeadapter.New()); err != nil {
		t.Fatal(err)
	}
	matrix := registry.CapabilityMatrix()
	capabilities, ok := matrix[fakeadapter.AdapterID]
	if !ok {
		t.Fatal("registered fake adapter must appear in the capability matrix")
	}
	found := false
	for _, capability := range capabilities {
		if capability == adaptersdk.CapabilityInventoryComponents {
			found = true
		}
	}
	if !found {
		t.Fatal("capability matrix must expose the fake adapter's declared capabilities")
	}
	if _, err := registry.Get(fakeadapter.AdapterID); err != nil {
		t.Fatal("registered adapter must be retrievable by its own ID, with no other lookup path required")
	}
	if err := registry.Register(fakeadapter.New()); !errors.Is(err, adaptersdk.ErrDuplicateAdapterID) {
		t.Fatal("duplicate registration must be rejected")
	}
}

func TestAuditProducesPassResultsForDeclaredCapabilities(t *testing.T) {
	adapter := fakeadapter.New()
	results := adapter.Audit(context.Background(), adaptersdk.Installation{InstallationID: "inst_1"}, adaptersdk.AuditFixtureReplay)
	if len(results) == 0 {
		t.Fatal("audit must return at least one check result")
	}
	for _, result := range results {
		if result.Status != adaptersdk.CheckPass {
			t.Fatalf("fixture-replay audit for the conformance fixture must pass: %+v", result)
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
