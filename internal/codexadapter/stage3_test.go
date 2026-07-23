package codexadapter_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/codexadapter"
)

// --- codex.rollout -----------------------------------------------------------

func writeRolloutFile(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportRolloutFileParsesEachDeclaredRecordKind(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"type":"session_meta","session_id":"s1"}`,
		`{"type":"user_message","session_id":"s1","turn_id":"t1"}`,
		`{"type":"tool_call","session_id":"s1","tool_id":"shell"}`,
		`{"type":"tool_result","session_id":"s1","tool_id":"shell"}`,
		`{"type":"subagent_event","session_id":"s1"}`,
	}
	path := writeRolloutFile(t, dir, "rollout-1.jsonl", lines)

	result, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accepted) != 5 {
		t.Fatalf("expected 5 accepted records, got %d (quarantined=%d)", len(result.Accepted), result.QuarantinedCount)
	}
	if result.QuarantinedCount != 0 {
		t.Fatalf("expected zero quarantined records for well-formed input, got %d", result.QuarantinedCount)
	}
	wantKinds := []codexadapter.RolloutRecordKind{
		codexadapter.RolloutRecordSessionMeta, codexadapter.RolloutRecordUserMessage,
		codexadapter.RolloutRecordToolCall, codexadapter.RolloutRecordToolResult,
		codexadapter.RolloutRecordSubagent,
	}
	for i, want := range wantKinds {
		if result.Accepted[i].Kind != want {
			t.Fatalf("record %d: expected kind %q, got %q", i, want, result.Accepted[i].Kind)
		}
	}
}

func TestImportRolloutFileQuarantinesCorruptAndUnknownSchemaLines(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"type":"session_meta","session_id":"s1"}`,
		`not even json`,
		`{"type":"totally_unknown_type","session_id":"s1"}`,
		`{"type":"user_message","session_id":"s1","unexpected_field":true}`,
		`{"type":"user_message"}`, // missing session_id
	}
	path := writeRolloutFile(t, dir, "rollout-2.jsonl", lines)

	result, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accepted) != 1 {
		t.Fatalf("expected exactly 1 accepted record, got %d", len(result.Accepted))
	}
	if result.QuarantinedCount != 4 {
		t.Fatalf("expected 4 quarantined records (corrupt/unknown-schema/unknown-field/missing-session-id), got %d", result.QuarantinedCount)
	}
}

func TestImportRolloutFileNeverWritesToTheSourceFile(t *testing.T) {
	dir := t.TempDir()
	lines := []string{`{"type":"session_meta","session_id":"s1"}`}
	path := writeRolloutFile(t, dir, "rollout-3.jsonl", lines)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{}); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("ImportRolloutFile must never modify the underlying Codex session tree file")
	}
}

func TestImportRolloutFileOffsetResumeIsIdempotentOnReplay(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"type":"session_meta","session_id":"s1"}`,
		`{"type":"user_message","session_id":"s1","turn_id":"t1"}`,
		`{"type":"tool_call","session_id":"s1","tool_id":"shell"}`,
	}
	path := writeRolloutFile(t, dir, "rollout-4.jsonl", lines)

	// First import from scratch.
	first, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Accepted) != 3 {
		t.Fatalf("expected 3 accepted records on first pass, got %d", len(first.Accepted))
	}

	// Resume from the committed checkpoint: no new records exist, so this
	// must accept nothing new -- proving offset resume doesn't reprocess.
	second, err := codexadapter.ImportRolloutFile(path, first.Checkpoint, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Accepted) != 0 {
		t.Fatalf("resuming from a checkpoint at EOF must yield zero new records, got %d", len(second.Accepted))
	}

	// Replaying the *same* checkpoint twice from a fresh checkpoint value
	// (byte_offset 0) must produce byte-identical results -- the idempotency
	// guarantee for a given byte range.
	replay, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first.Accepted)
	replayJSON, _ := json.Marshal(replay.Accepted)
	if string(firstJSON) != string(replayJSON) {
		t.Fatal("replaying the same byte range must yield identical accepted records")
	}
	if first.Checkpoint.FirstRecordFingerprint != replay.Checkpoint.FirstRecordFingerprint ||
		first.Checkpoint.LastRecordFingerprint != replay.Checkpoint.LastRecordFingerprint {
		t.Fatal("replaying the same byte range must yield identical first/last record fingerprints")
	}
}

func TestImportRolloutFileAppendOnlyResumeAcceptsOnlyNewRecords(t *testing.T) {
	dir := t.TempDir()
	path := writeRolloutFile(t, dir, "rollout-5.jsonl", []string{
		`{"type":"session_meta","session_id":"s1"}`,
	})
	first, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Append a new record after the checkpoint.
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"type":"user_message","session_id":"s1"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := codexadapter.ImportRolloutFile(path, first.Checkpoint, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Accepted) != 1 {
		t.Fatalf("expected exactly 1 newly appended record, got %d", len(second.Accepted))
	}
	if second.Accepted[0].Kind != codexadapter.RolloutRecordUserMessage {
		t.Fatalf("expected the new record to be user_message, got %q", second.Accepted[0].Kind)
	}
}

func TestImportRolloutFileDetectsRotationViaFileIdentityMismatch(t *testing.T) {
	dir := t.TempDir()
	path := writeRolloutFile(t, dir, "rollout-6.jsonl", []string{`{"type":"session_meta","session_id":"s1"}`})
	first, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate rotation: replace the file at the same path with different
	// content (a fresh file), and forge a checkpoint identity mismatch by
	// tampering with the recorded pseudonym.
	tamperedCheckpoint := first.Checkpoint
	tamperedCheckpoint.FileIdentity.PathPseudonym = "hmac-sha256:0000000000000000000000000000000000000000000000000000000000000000"

	_, err = codexadapter.ImportRolloutFile(path, tamperedCheckpoint, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if !errors.Is(err, codexadapter.ErrRolloutSourceChanged) {
		t.Fatalf("expected ErrRolloutSourceChanged for a file-identity mismatch, got %v", err)
	}
}

func TestImportRolloutFileDetectsTruncation(t *testing.T) {
	dir := t.TempDir()
	path := writeRolloutFile(t, dir, "rollout-7.jsonl", []string{
		`{"type":"session_meta","session_id":"s1"}`,
		`{"type":"user_message","session_id":"s1"}`,
	})
	first, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Checkpoint.ByteOffset == 0 {
		t.Fatal("checkpoint must have advanced past byte 0")
	}

	// Truncate the file to shorter than the checkpoint's recorded offset.
	if err := os.Truncate(path, 5); err != nil {
		t.Fatal(err)
	}

	result, err := codexadapter.ImportRolloutFile(path, first.Checkpoint, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if !errors.Is(err, codexadapter.ErrRolloutInvalidCheckpoint) {
		t.Fatalf("expected ErrRolloutInvalidCheckpoint on truncation, got %v", err)
	}
	if !result.TruncationDetected {
		t.Fatal("truncation must be reported via TruncationDetected, not silently rewound")
	}
}

func TestImportRolloutFileDefaultsToMetadataOnlyContentParsing(t *testing.T) {
	dir := t.TempDir()
	path := writeRolloutFile(t, dir, "rollout-8.jsonl", []string{
		`{"type":"user_message","session_id":"s1","content":"do not leak this raw text anywhere durable"}`,
	})

	result, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accepted) != 1 {
		t.Fatalf("expected 1 accepted record, got %d", len(result.Accepted))
	}
	if result.Accepted[0].PromptFeatures != nil {
		t.Fatal("PromptFeatures must be nil by default (AllowTransientContentParsing=false)")
	}
	encoded, _ := json.Marshal(result.Accepted[0])
	if strings.Contains(string(encoded), "do not leak this raw text anywhere durable") {
		t.Fatal("raw content must never appear in the default metadata-only record")
	}
}

func TestImportRolloutFileOptInContentParsingComputesFeaturesButNeverPersistsRawText(t *testing.T) {
	dir := t.TempDir()
	path := writeRolloutFile(t, dir, "rollout-9.jsonl", []string{
		`{"type":"user_message","session_id":"s1","content":"please refactor auth module"}`,
	})

	result, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{AllowTransientContentParsing: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted[0].PromptFeatures == nil {
		t.Fatal("opt-in transient content parsing must populate PromptFeatures")
	}
	encoded, _ := json.Marshal(result.Accepted[0])
	if strings.Contains(string(encoded), "refactor auth module") {
		t.Fatal("raw content must never be serialized even when transient parsing is opted into")
	}
}

func TestImportRolloutFileRejectsOversizedLine(t *testing.T) {
	dir := t.TempDir()
	huge := `{"type":"session_meta","session_id":"` + strings.Repeat("a", 1<<21) + `"}`
	path := writeRolloutFile(t, dir, "rollout-10.jsonl", []string{huge})
	_, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if !errors.Is(err, codexadapter.ErrRolloutLineOversized) {
		t.Fatalf("expected ErrRolloutLineOversized, got %v", err)
	}
}

// --- codex.inventory ----------------------------------------------------------

func TestBuildInventorySnapshotNeverMarksCacheOnlyPluginEnabled(t *testing.T) {
	input := codexadapter.InventoryInput{
		InstallationID: "inst-1",
		Plugins: []codexadapter.PluginDescriptor{
			{Name: "cached-plugin", Scope: adaptersdk.ScopePluginCache, CachedOnly: true, ActiveEnabledFor: "inst-1"},
		},
	}
	snapshot, err := codexadapter.BuildInventorySnapshot(input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var cacheNode adaptersdk.Node
	found := false
	for _, node := range snapshot.Nodes {
		if node.Kind == adaptersdk.NodeCacheArtifact {
			cacheNode = node
			found = true
		}
	}
	if !found {
		t.Fatal("expected a cache_artifact node for the cached-only plugin")
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeEnabledFor && edge.FromNode == cacheNode.NodeID {
			t.Fatal("a cache-only plugin must never receive an enabled_for edge, even when ActiveEnabledFor is set")
		}
	}
}

func TestBuildInventorySnapshotLinksCollidingSkillNamesRatherThanMerging(t *testing.T) {
	input := codexadapter.InventoryInput{
		InstallationID: "inst-1",
		Skills: []codexadapter.SkillDescriptor{
			{Name: "deploy", Scope: adaptersdk.ScopeUser, Enabled: true},
			{Name: "deploy", Scope: adaptersdk.ScopeRepository, Enabled: true},
		},
	}
	snapshot, err := codexadapter.BuildInventorySnapshot(input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var skillNodes []adaptersdk.Node
	for _, node := range snapshot.Nodes {
		if node.Kind == adaptersdk.NodeSkillIdentity && node.DeclaredName == "deploy" {
			skillNodes = append(skillNodes, node)
		}
	}
	if len(skillNodes) != 2 {
		t.Fatalf("two distinct skill declarations sharing a name must remain two distinct nodes, got %d", len(skillNodes))
	}
	if skillNodes[0].NodeID == skillNodes[1].NodeID {
		t.Fatal("colliding skill nodes must never share a node id")
	}
	collision := false
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeCollidesWith {
			collision = true
		}
	}
	if !collision {
		t.Fatal("expected a collides_with edge linking the two same-named skill nodes")
	}
}

func TestBuildInventorySnapshotIsDeterministic(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	input := codexadapter.InventoryInput{
		InstallationID: "inst-1",
		Skills:         []codexadapter.SkillDescriptor{{Name: "deploy", Scope: adaptersdk.ScopeUser, Enabled: true}},
		Hooks:          []codexadapter.HookDescriptor{{Name: "pre-commit", Scope: adaptersdk.ScopeUser, Enabled: true, Trusted: true}},
	}
	first, err := codexadapter.BuildInventorySnapshot(input, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := codexadapter.BuildInventorySnapshot(input, now)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("BuildInventorySnapshot must be deterministic given identical input and timestamp")
	}
}

func TestBuildInventorySnapshotRejectsTooManyRepositoryTargets(t *testing.T) {
	targets := make([]string, 65)
	for i := range targets {
		targets[i] = "/tmp/repo"
	}
	_, err := codexadapter.BuildInventorySnapshot(codexadapter.InventoryInput{InstallationID: "inst-1", RepositoryTargets: targets}, time.Now())
	if !errors.Is(err, codexadapter.ErrTooManyRepositoryTargets) {
		t.Fatalf("expected ErrTooManyRepositoryTargets, got %v", err)
	}
}

func TestBuildInventorySnapshotRejectsHookNotBothEnabledAndTrusted(t *testing.T) {
	input := codexadapter.InventoryInput{
		InstallationID: "inst-1",
		Hooks:          []codexadapter.HookDescriptor{{Name: "sketchy-hook", Scope: adaptersdk.ScopeUser, Enabled: true, Trusted: false}},
	}
	snapshot, err := codexadapter.BuildInventorySnapshot(input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeEnabledFor {
			t.Fatal("an enabled-but-untrusted hook must never receive an enabled_for edge")
		}
	}
}

// --- skill evidence tiers / mapping table --------------------------------------

func TestResolveSkillEvidenceDefaultsMatchContractTable(t *testing.T) {
	cases := []struct {
		kind      codexadapter.SkillEvidenceKind
		canonical string
		tier      codexadapter.EvidenceTier
	}{
		{codexadapter.EvidenceExplicitUserInvocation, codexadapter.CanonicalComponentInvoked, codexadapter.EvidenceTierReconstructed},
		{codexadapter.EvidenceSkillMDLoad, codexadapter.CanonicalComponentLoaded, codexadapter.EvidenceTierReconstructed},
		{codexadapter.EvidenceAgentDeclaredUse, codexadapter.CanonicalComponentInvoked, codexadapter.EvidenceTierReconstructed},
		{codexadapter.EvidenceUniquelyOwnedHelperCall, codexadapter.CanonicalComponentExecuted, codexadapter.EvidenceTierReconstructed},
		{codexadapter.EvidenceSemanticOpportunity, codexadapter.CanonicalComponentOpportunity, codexadapter.EvidenceTierInferred},
	}
	for _, testCase := range cases {
		input := codexadapter.SkillEvidenceInput{Kind: testCase.kind, CandidateSkillIdentities: []string{"skill-a"}}
		resolution, err := codexadapter.ResolveSkillEvidence(input)
		if err != nil {
			t.Fatalf("kind %q: unexpected error %v", testCase.kind, err)
		}
		if resolution.CanonicalEventType != testCase.canonical {
			t.Fatalf("kind %q: expected canonical %q, got %q", testCase.kind, testCase.canonical, resolution.CanonicalEventType)
		}
		if resolution.Tier != testCase.tier {
			t.Fatalf("kind %q: expected tier %q, got %q", testCase.kind, testCase.tier, resolution.Tier)
		}
	}
}

func TestResolveSkillEvidenceExplicitInvocationPromotesToNativeOnlyWhenSourceLabelsIt(t *testing.T) {
	unlabeled, err := codexadapter.ResolveSkillEvidence(codexadapter.SkillEvidenceInput{
		Kind: codexadapter.EvidenceExplicitUserInvocation, CandidateSkillIdentities: []string{"skill-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if unlabeled.Tier != codexadapter.EvidenceTierReconstructed {
		t.Fatalf("unlabeled explicit invocation must default to reconstructed, got %q", unlabeled.Tier)
	}

	labeled, err := codexadapter.ResolveSkillEvidence(codexadapter.SkillEvidenceInput{
		Kind: codexadapter.EvidenceExplicitUserInvocation, CandidateSkillIdentities: []string{"skill-a"}, SourceLabelsNative: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if labeled.Tier != codexadapter.EvidenceTierNative {
		t.Fatalf("source-labeled explicit invocation must resolve to native, got %q", labeled.Tier)
	}
}

func TestResolveSkillEvidenceNeverPromotesSemanticOpportunityBeyondInferred(t *testing.T) {
	resolution, err := codexadapter.ResolveSkillEvidence(codexadapter.SkillEvidenceInput{
		Kind: codexadapter.EvidenceSemanticOpportunity, CandidateSkillIdentities: []string{"skill-a"}, SourceLabelsNative: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Tier != codexadapter.EvidenceTierInferred {
		t.Fatal("semantic_opportunity_classifier must never resolve above inferred, regardless of SourceLabelsNative")
	}
	if resolution.CanonicalEventType == codexadapter.CanonicalComponentInvoked {
		t.Fatal("semantic_opportunity_classifier must never resolve to component.invoked")
	}
}

func TestResolveSkillEvidenceRefusesAmbiguousOwnershipPromotion(t *testing.T) {
	resolution, err := codexadapter.ResolveSkillEvidence(codexadapter.SkillEvidenceInput{
		Kind:                     codexadapter.EvidenceUniquelyOwnedHelperCall,
		CandidateSkillIdentities: []string{"skill-a", "skill-b"},
	})
	if !errors.Is(err, codexadapter.ErrAmbiguousOwnershipPromotion) {
		t.Fatalf("expected ErrAmbiguousOwnershipPromotion for a multi-candidate helper call, got %v", err)
	}
	if resolution.CanonicalEventType != codexadapter.CanonicalComponentExecuted {
		t.Fatalf("ambiguous ownership must remain component.executed, got %q", resolution.CanonicalEventType)
	}
	if resolution.CanonicalEventType == codexadapter.CanonicalComponentInvoked {
		t.Fatal("ambiguous ownership must never be promoted to component.invoked")
	}
	if len(resolution.CandidateSkillIdentities) != 2 {
		t.Fatal("ambiguous resolution must retain every plural candidate")
	}
}

func TestResolveSkillEvidenceUniqueOwnershipResolvesCleanly(t *testing.T) {
	resolution, err := codexadapter.ResolveSkillEvidence(codexadapter.SkillEvidenceInput{
		Kind: codexadapter.EvidenceUniquelyOwnedHelperCall, CandidateSkillIdentities: []string{"skill-a"},
	})
	if err != nil {
		t.Fatalf("a single-candidate helper call must resolve without error, got %v", err)
	}
	if resolution.CanonicalEventType != codexadapter.CanonicalComponentExecuted {
		t.Fatal("expected component.executed for a uniquely owned helper call")
	}
}

func TestResolveSkillEvidenceRejectsUnknownKind(t *testing.T) {
	_, err := codexadapter.ResolveSkillEvidence(codexadapter.SkillEvidenceInput{Kind: "not_a_real_kind"})
	if !errors.Is(err, codexadapter.ErrUnknownSkillEvidenceKind) {
		t.Fatalf("expected ErrUnknownSkillEvidenceKind, got %v", err)
	}
}

func TestSourceToCanonicalTableMatchesContractRowCount(t *testing.T) {
	rows := codexadapter.SourceToCanonicalTable()
	if len(rows) != 8 {
		t.Fatalf("expected 8 rows matching contracts/codex/skill-evidence-and-reconciliation.yaml's source_to_canonical_mapping, got %d", len(rows))
	}
	for _, row := range rows {
		if row.SourceEvidence == "" || row.CanonicalEventType == "" || row.Tier == "" {
			t.Fatalf("every mapping row must be fully populated, got %+v", row)
		}
	}
}

// --- cross-source reconciliation ------------------------------------------------

func TestReconcileLaneDegradesOnlyTheMissingSourceNeverFabricatesZeroForWholeSession(t *testing.T) {
	input := codexadapter.LaneInput{
		Lane:                 codexadapter.LanePrompts,
		CompatibilityVersion: "codex-compat/1",
		Hook:                 codexadapter.SourceHealth{Present: true, Count: 3},
		OTel:                 codexadapter.SourceHealth{Present: false},
		Rollout:              codexadapter.SourceHealth{Present: true, Count: 3},
	}
	result := codexadapter.ReconcileLane(input)
	if result.Completeness != codexadapter.LanePartial {
		t.Fatalf("a missing source must mark the lane partial, got %q", result.Completeness)
	}
	if len(result.DegradedSources) != 1 || result.DegradedSources[0] != codexadapter.ReconSourceOTel {
		t.Fatalf("expected exactly codex.otel degraded, got %v", result.DegradedSources)
	}
	if result.Mismatched {
		t.Fatal("hook and rollout agree; missing otel must not itself cause a mismatch")
	}
	if result.HookCount != 3 || result.RolloutCount != 3 {
		t.Fatal("present sources' counts must be reported even when another source is missing")
	}
}

func TestReconcileLaneDetectsMismatchOnlyAmongPresentSources(t *testing.T) {
	input := codexadapter.LaneInput{
		Lane:                 codexadapter.LaneToolTerminal,
		CompatibilityVersion: "codex-compat/1",
		Hook:                 codexadapter.SourceHealth{Present: true, Count: 5},
		OTel:                 codexadapter.SourceHealth{Present: true, Count: 4},
		Rollout:              codexadapter.SourceHealth{Present: true, Count: 5},
	}
	result := codexadapter.ReconcileLane(input)
	if !result.Mismatched {
		t.Fatal("differing present-source counts must be reported as a mismatch")
	}
	if result.Completeness != codexadapter.LaneComplete {
		t.Fatal("completeness reflects source presence, not count agreement; all three sources are present here")
	}
}

func TestReconcileLaneWithFewerThanTwoPresentSourcesNeverFabricatesMismatch(t *testing.T) {
	input := codexadapter.LaneInput{
		Lane:                 codexadapter.LaneMCPCalls,
		CompatibilityVersion: "codex-compat/1",
		Hook:                 codexadapter.SourceHealth{Present: true, Count: 2},
		OTel:                 codexadapter.SourceHealth{Present: false},
		Rollout:              codexadapter.SourceHealth{Present: false},
	}
	result := codexadapter.ReconcileLane(input)
	if result.Mismatched {
		t.Fatal("with only one present source there is nothing to contradict; Mismatched must stay false")
	}
	if len(result.DegradedSources) != 2 {
		t.Fatalf("expected exactly 2 degraded sources, got %d", len(result.DegradedSources))
	}
}

func TestReconcileSessionCoversEveryDeclaredLaneIndependently(t *testing.T) {
	var inputs []codexadapter.LaneInput
	for _, lane := range codexadapter.AllReconciliationLanes() {
		inputs = append(inputs, codexadapter.LaneInput{
			Lane: lane, CompatibilityVersion: "codex-compat/1",
			Hook: codexadapter.SourceHealth{Present: true, Count: 1}, OTel: codexadapter.SourceHealth{Present: true, Count: 1}, Rollout: codexadapter.SourceHealth{Present: true, Count: 1},
		})
	}
	session := codexadapter.ReconcileSession("sess-1", inputs)
	if len(session.Lanes) != len(codexadapter.AllReconciliationLanes()) {
		t.Fatalf("expected %d reconciled lanes, got %d", len(codexadapter.AllReconciliationLanes()), len(session.Lanes))
	}
	for _, lane := range codexadapter.AllReconciliationLanes() {
		result, ok := session.Lanes[lane]
		if !ok {
			t.Fatalf("missing lane result for %q", lane)
		}
		if result.Completeness != codexadapter.LaneComplete {
			t.Fatalf("lane %q: expected complete, got %q", lane, result.Completeness)
		}
	}
}

func TestResolveToleranceIsVersionedNeverHardcodedAcrossVersions(t *testing.T) {
	entry, ok := codexadapter.ResolveTolerance("codex-compat/1")
	if !ok {
		t.Fatal("expected a registered tolerance entry for codex-compat/1")
	}
	if entry.BatchingDelayMS <= 0 || entry.TerminalDelayMS <= 0 {
		t.Fatal("expected positive, meaningful tolerance values")
	}
	if _, ok := codexadapter.ResolveTolerance("codex-compat/unknown-future-version"); ok {
		t.Fatal("an unknown compatibility version must never silently resolve to a fabricated tolerance entry")
	}
}

func TestOneFactRuleLaneIdentityIsIndependentAcrossSources(t *testing.T) {
	// One lane where every source is present but the identity sets differ in
	// size: this is still surfaced via Mismatched, proving each source keeps
	// its own distinct evidence identity rather than being coalesced.
	input := codexadapter.LaneInput{
		Lane: codexadapter.LaneSubagentLifecycle, CompatibilityVersion: "codex-compat/1",
		Hook:    codexadapter.SourceHealth{Present: true, Count: 2, EventIdentities: []string{"a", "b"}},
		OTel:    codexadapter.SourceHealth{Present: true, Count: 2, EventIdentities: []string{"a", "b"}},
		Rollout: codexadapter.SourceHealth{Present: true, Count: 1, EventIdentities: []string{"a"}},
	}
	result := codexadapter.ReconcileLane(input)
	if !result.Mismatched {
		t.Fatal("differing counts across present sources must be surfaced, never silently reconciled away")
	}
}

// --- discoverability pressure -----------------------------------------------------

func TestComputeDiscoverabilityPressureFlagsDuplicateNamesAndDisabled(t *testing.T) {
	inputs := []codexadapter.SkillDiscoverabilityInput{
		{Skill: codexadapter.SkillDescriptor{Name: "deploy", Scope: adaptersdk.ScopeUser, Enabled: true, DescriptionBytes: 100, DescriptionChars: 100}},
		{Skill: codexadapter.SkillDescriptor{Name: "deploy", Scope: adaptersdk.ScopeRepository, Enabled: true, DescriptionBytes: 50, DescriptionChars: 50}},
		{Skill: codexadapter.SkillDescriptor{Name: "unique-skill", Scope: adaptersdk.ScopeUser, Enabled: false, Disabled: true, DescriptionBytes: 200, DescriptionChars: 200}},
	}
	reports := codexadapter.ComputeDiscoverabilityPressure(inputs, codexadapter.DefaultCatalogBudget())
	if len(reports) != 3 {
		t.Fatalf("expected 3 reports, got %d", len(reports))
	}
	duplicateCount := 0
	for _, report := range reports {
		if report.Name == "deploy" {
			if !report.DuplicateNameFlag {
				t.Fatal("both deploy declarations must be flagged as duplicate names")
			}
			duplicateCount++
		}
		if report.Name == "unique-skill" && !report.DisabledFlag {
			t.Fatal("a disabled skill must be flagged DisabledFlag=true")
		}
		if report.Name == "unique-skill" && report.DuplicateNameFlag {
			t.Fatal("a uniquely named skill must never be flagged as duplicate")
		}
	}
	if duplicateCount != 2 {
		t.Fatalf("expected both deploy entries flagged, got %d", duplicateCount)
	}
}

func TestComputeDiscoverabilityPressureNeverEstimatesRiskForExposedSkills(t *testing.T) {
	inputs := []codexadapter.SkillDiscoverabilityInput{
		{Skill: codexadapter.SkillDescriptor{Name: "exposed-skill", Scope: adaptersdk.ScopeUser, Enabled: true, DescriptionBytes: 20000}, ExposureEvidence: true},
	}
	reports := codexadapter.ComputeDiscoverabilityPressure(inputs, codexadapter.DefaultCatalogBudget())
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if !reports[0].Exposed {
		t.Fatal("expected Exposed=true for a skill with direct exposure evidence")
	}
	if reports[0].RiskEstimate != "" {
		t.Fatalf("an exposed skill must carry no risk estimate at all, got %q", reports[0].RiskEstimate)
	}
	if reports[0].RiskEvidenceTier != "" {
		t.Fatal("an exposed skill must carry no risk evidence tier at all")
	}
}

func TestComputeDiscoverabilityPressureLabelsRiskEstimateInferredNeverNative(t *testing.T) {
	inputs := []codexadapter.SkillDiscoverabilityInput{
		{Skill: codexadapter.SkillDescriptor{Name: "heavy-skill", Scope: adaptersdk.ScopeUser, Enabled: true, DescriptionBytes: 20000}},
	}
	reports := codexadapter.ComputeDiscoverabilityPressure(inputs, codexadapter.DefaultCatalogBudget())
	if reports[0].RiskEstimate != codexadapter.CatalogPressureHigh {
		t.Fatalf("expected high risk estimate for a 20000-byte description against the default budget, got %q", reports[0].RiskEstimate)
	}
	if reports[0].RiskEvidenceTier != codexadapter.EvidenceTierInferred {
		t.Fatalf("catalog pressure risk must always be labeled inferred, got %q", reports[0].RiskEvidenceTier)
	}
}

func TestScopePrecedenceRankCoversEveryDeclaredScope(t *testing.T) {
	scopes := []adaptersdk.SourceScope{
		adaptersdk.ScopeSystem, adaptersdk.ScopeUser, adaptersdk.ScopeRepository, adaptersdk.ScopeAdmin,
		adaptersdk.ScopeMarketplace, adaptersdk.ScopePluginCache, adaptersdk.ScopeTransientSession,
	}
	seen := map[int]bool{}
	for _, scope := range scopes {
		rank, ok := codexadapter.ScopePrecedenceRank(scope)
		if !ok {
			t.Fatalf("expected a ranked precedence for declared scope %q", scope)
		}
		if seen[rank] {
			t.Fatalf("scope %q reused an already-assigned rank %d", scope, rank)
		}
		seen[rank] = true
	}
	if _, ok := codexadapter.ScopePrecedenceRank("not_a_real_scope"); ok {
		t.Fatal("an undeclared scope must never resolve to a fabricated precedence rank")
	}
}

func TestTotalCatalogDescriptionBytesExcludesDisabledSkills(t *testing.T) {
	inputs := []codexadapter.SkillDiscoverabilityInput{
		{Skill: codexadapter.SkillDescriptor{Name: "a", Scope: adaptersdk.ScopeUser, Enabled: true, DescriptionBytes: 100}},
		{Skill: codexadapter.SkillDescriptor{Name: "b", Scope: adaptersdk.ScopeUser, Enabled: false, Disabled: true, DescriptionBytes: 500}},
	}
	reports := codexadapter.ComputeDiscoverabilityPressure(inputs, codexadapter.DefaultCatalogBudget())
	total := codexadapter.TotalCatalogDescriptionBytes(reports)
	if total != 100 {
		t.Fatalf("expected disabled skill's bytes excluded from the total, got %d", total)
	}
}
