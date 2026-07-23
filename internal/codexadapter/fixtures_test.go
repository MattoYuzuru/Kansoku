package codexadapter_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/codexadapter"
)

// This file is Stage 4's fixture-driven, end-to-end exercise of the Codex
// adapter: it loads the checked-in tests/fixtures/session-06/*.json files
// (never hand-inlines a second copy of that data) and drives discovery ->
// normalize/evidence -> reconcile against the same canonical event-type
// vocabulary internal/observability declares, plus the canary chain itself.
// Every unit-level behavior these fixtures exercise is already covered in
// isolation by codexadapter_test.go/stage3_test.go; this file's job is only
// to prove the fixtures agree with that behavior and with each other.

const fixturesRoot = "../../tests/fixtures/session-06"

func readFixtureJSON(t *testing.T, relPath string, out interface{}) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixturesRoot, relPath))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", relPath, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decoding fixture %s: %v", relPath, err)
	}
}

// --- hook-otel-golden-map.json ----------------------------------------------

type hookGoldenCase struct {
	HookEventName              string `json:"hook_event_name"`
	ExpectedCanonicalEventType string `json:"expected_canonical_event_type"`
	ExpectedTier               string `json:"expected_tier"`
	ExpectUnsupported          bool   `json:"expect_unsupported"`
}

type hookSampleInput struct {
	Case                          string          `json:"case"`
	StdinJSON                     json.RawMessage `json:"stdin_json"`
	ExpectedEventType             string          `json:"expected_event_type"`
	ExpectedPromptFeaturesPresent bool            `json:"expected_prompt_features_present"`
}

type otelGoldenCase struct {
	OTelEventName              string   `json:"otel_event_name"`
	AttributeShape             []string `json:"attribute_shape"`
	ExpectedCanonicalEventType *string  `json:"expected_canonical_event_type"`
	ExpectError                *string  `json:"expect_error"`
}

type hookOTelGoldenMap struct {
	HookGoldenMap    []hookGoldenCase  `json:"hook_golden_map"`
	HookSampleInputs []hookSampleInput `json:"hook_sample_inputs"`
	OTelGoldenMap    []otelGoldenCase  `json:"otel_golden_map"`
}

func TestFixtureHookGoldenMapMatchesCanonicalEventForHook(t *testing.T) {
	var fixture hookOTelGoldenMap
	readFixtureJSON(t, "hook-otel-golden-map.json", &fixture)
	if len(fixture.HookGoldenMap) == 0 {
		t.Fatal("fixture must declare at least one hook golden case")
	}
	for _, c := range fixture.HookGoldenMap {
		event := codexadapter.HookEvent(c.HookEventName)
		canonical, ok := codexadapter.CanonicalEventForHook(event)
		if c.ExpectUnsupported {
			if ok {
				t.Fatalf("case %q: expected unsupported hook event, got canonical %q", c.HookEventName, canonical)
			}
			continue
		}
		if !ok {
			t.Fatalf("case %q: expected supported hook event, got unsupported", c.HookEventName)
		}
		if canonical != c.ExpectedCanonicalEventType {
			t.Fatalf("case %q: expected canonical %q, got %q", c.HookEventName, c.ExpectedCanonicalEventType, canonical)
		}
	}
}

func TestFixtureHookSampleInputsDecodeAndBuildExpectedOutput(t *testing.T) {
	var fixture hookOTelGoldenMap
	readFixtureJSON(t, "hook-otel-golden-map.json", &fixture)
	if len(fixture.HookSampleInputs) == 0 {
		t.Fatal("fixture must declare at least one hook sample input")
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	for _, sample := range fixture.HookSampleInputs {
		input, err := codexadapter.DecodeHookInput(strings.NewReader(string(sample.StdinJSON)))
		if err != nil {
			t.Fatalf("case %q: DecodeHookInput failed: %v", sample.Case, err)
		}
		output, err := codexadapter.BuildHookOutput(input, now)
		if err != nil {
			t.Fatalf("case %q: BuildHookOutput failed: %v", sample.Case, err)
		}
		if output.EventType != sample.ExpectedEventType {
			t.Fatalf("case %q: expected event type %q, got %q", sample.Case, sample.ExpectedEventType, output.EventType)
		}
		gotFeatures := output.PromptFeatures != nil
		if gotFeatures != sample.ExpectedPromptFeaturesPresent {
			t.Fatalf("case %q: expected prompt_features present=%v, got %v", sample.Case, sample.ExpectedPromptFeaturesPresent, gotFeatures)
		}
		if err := codexadapter.ValidateHookOutputAllowlist(output); err != nil {
			t.Fatalf("case %q: output allowlist validation failed: %v", sample.Case, err)
		}
		// The raw stdin never carries a "prompt" value that could leak
		// through to the encoded output verbatim.
		encoded, _ := json.Marshal(output)
		if input.Prompt != "" && strings.Contains(string(encoded), input.Prompt) {
			t.Fatalf("case %q: raw prompt text leaked into hook output", sample.Case)
		}
	}
}

func TestFixtureOTelGoldenMapMatchesCanonicalEventForOTel(t *testing.T) {
	var fixture hookOTelGoldenMap
	readFixtureJSON(t, "hook-otel-golden-map.json", &fixture)
	if len(fixture.OTelGoldenMap) == 0 {
		t.Fatal("fixture must declare at least one otel golden case")
	}
	for _, c := range fixture.OTelGoldenMap {
		shape := codexadapter.OTelAttributeShape{
			InstrumentationScope: c.OTelEventName,
			PresentAttributeKeys: c.AttributeShape,
		}
		canonical, err := codexadapter.CanonicalEventForOTel(codexadapter.OTelEventName(c.OTelEventName), shape)
		if c.ExpectError != nil {
			if err == nil {
				t.Fatalf("case %q: expected error %q, got none (canonical=%q)", c.OTelEventName, *c.ExpectError, canonical)
			}
			if err.Error() != *c.ExpectError {
				t.Fatalf("case %q: expected error %q, got %q", c.OTelEventName, *c.ExpectError, err.Error())
			}
			continue
		}
		if err != nil {
			t.Fatalf("case %q: unexpected error: %v", c.OTelEventName, err)
		}
		if c.ExpectedCanonicalEventType == nil || canonical != *c.ExpectedCanonicalEventType {
			t.Fatalf("case %q: expected canonical %v, got %q", c.OTelEventName, c.ExpectedCanonicalEventType, canonical)
		}
	}
}

// --- rollout-fixtures.json ---------------------------------------------------

type rolloutVersionFixture struct {
	CompatibilityVersion     string   `json:"compatibility_version"`
	CodexRelease             string   `json:"codex_release"`
	JSONLLines               []string `json:"jsonl_lines"`
	ExpectedKinds            []string `json:"expected_kinds"`
	ExpectedAcceptedCount    int      `json:"expected_accepted_count"`
	ExpectedQuarantinedCount int      `json:"expected_quarantined_count"`
}

type rolloutQuarantineScenario struct {
	JSONLLines               []string `json:"jsonl_lines"`
	ExpectedAcceptedCount    int      `json:"expected_accepted_count"`
	ExpectedQuarantinedCount int      `json:"expected_quarantined_count"`
}

type rolloutOffsetResumeScenario struct {
	InitialLines                 []string `json:"initial_lines"`
	AppendedLines                []string `json:"appended_lines"`
	ExpectedInitialAcceptedCount int      `json:"expected_initial_accepted_count"`
	ExpectedResumeAcceptedCount  int      `json:"expected_resume_accepted_count"`
	ExpectedResumeKind           string   `json:"expected_resume_kind"`
}

type rolloutReplayScenario struct {
	JSONLLines            []string `json:"jsonl_lines"`
	ExpectedAcceptedCount int      `json:"expected_accepted_count"`
}

type rolloutRotationScenario struct {
	JSONLLines            []string `json:"jsonl_lines"`
	TamperedPathPseudonym string   `json:"tampered_path_pseudonym"`
	ExpectError           string   `json:"expect_error"`
}

type rolloutTruncationScenario struct {
	JSONLLines               []string `json:"jsonl_lines"`
	TruncateToBytes          int64    `json:"truncate_to_bytes"`
	ExpectError              string   `json:"expect_error"`
	ExpectTruncationDetected bool     `json:"expect_truncation_detected"`
}

type rolloutCrashScenario struct {
	JSONLLines                       []string `json:"jsonl_lines"`
	SimulatedCrashAfterRecordIndex   int      `json:"simulated_crash_after_record_index"`
	ExpectedPreCrashAcceptedCount    int      `json:"expected_pre_crash_accepted_count"`
	ExpectedPostRestartAcceptedCount int      `json:"expected_post_restart_accepted_count"`
	ExpectedTotalAcceptedCount       int      `json:"expected_total_accepted_count"`
}

type rolloutProhibitedContentCanary struct {
	RawContentLine      string   `json:"raw_content_line"`
	ForbiddenSubstrings []string `json:"forbidden_substrings"`
}

type rolloutFixtures struct {
	Versions                  []rolloutVersionFixture        `json:"versions"`
	QuarantineScenario        rolloutQuarantineScenario      `json:"quarantine_scenario"`
	OffsetResumeScenario      rolloutOffsetResumeScenario    `json:"offset_resume_scenario"`
	ReplayIdempotencyScenario rolloutReplayScenario          `json:"replay_idempotency_scenario"`
	RotationScenario          rolloutRotationScenario        `json:"rotation_scenario"`
	TruncationScenario        rolloutTruncationScenario      `json:"truncation_scenario"`
	CrashMidImportScenario    rolloutCrashScenario           `json:"crash_mid_import_scenario"`
	ProhibitedContentCanary   rolloutProhibitedContentCanary `json:"prohibited_content_canary"`
}

func writeFixtureRolloutFile(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFixtureRolloutVersionsAcrossAtLeastTwoDeclaredCodexReleases(t *testing.T) {
	var fixture rolloutFixtures
	readFixtureJSON(t, "rollout-fixtures.json", &fixture)
	if len(fixture.Versions) < 2 {
		t.Fatalf("fixture must declare at least two Codex versions/event variants, got %d", len(fixture.Versions))
	}
	seenReleases := map[string]bool{}
	for _, v := range fixture.Versions {
		seenReleases[v.CodexRelease] = true
		dir := t.TempDir()
		path := writeFixtureRolloutFile(t, dir, "rollout.jsonl", v.JSONLLines)
		result, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
		if err != nil {
			t.Fatalf("release %s: unexpected error: %v", v.CodexRelease, err)
		}
		if len(result.Accepted) != v.ExpectedAcceptedCount {
			t.Fatalf("release %s: expected %d accepted, got %d", v.CodexRelease, v.ExpectedAcceptedCount, len(result.Accepted))
		}
		if result.QuarantinedCount != v.ExpectedQuarantinedCount {
			t.Fatalf("release %s: expected %d quarantined, got %d", v.CodexRelease, v.ExpectedQuarantinedCount, result.QuarantinedCount)
		}
		if len(v.ExpectedKinds) != len(result.Accepted) {
			t.Fatalf("release %s: fixture expected_kinds length %d does not match accepted length %d", v.CodexRelease, len(v.ExpectedKinds), len(result.Accepted))
		}
		for i, wantKind := range v.ExpectedKinds {
			if string(result.Accepted[i].Kind) != wantKind {
				t.Fatalf("release %s record %d: expected kind %q, got %q", v.CodexRelease, i, wantKind, result.Accepted[i].Kind)
			}
		}
	}
	if len(seenReleases) < 2 {
		t.Fatalf("fixture versions must cover at least two distinct codex_release values, got %v", seenReleases)
	}
}

func TestFixtureRolloutQuarantineScenarioNeverDropsSilently(t *testing.T) {
	var fixture rolloutFixtures
	readFixtureJSON(t, "rollout-fixtures.json", &fixture)
	dir := t.TempDir()
	path := writeFixtureRolloutFile(t, dir, "quarantine.jsonl", fixture.QuarantineScenario.JSONLLines)
	result, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Accepted) != fixture.QuarantineScenario.ExpectedAcceptedCount {
		t.Fatalf("expected %d accepted, got %d", fixture.QuarantineScenario.ExpectedAcceptedCount, len(result.Accepted))
	}
	if result.QuarantinedCount != fixture.QuarantineScenario.ExpectedQuarantinedCount {
		t.Fatalf("expected %d quarantined, got %d", fixture.QuarantineScenario.ExpectedQuarantinedCount, result.QuarantinedCount)
	}
}

func TestFixtureRolloutOffsetResumeAcceptsOnlyAppendedRecords(t *testing.T) {
	var fixture rolloutFixtures
	readFixtureJSON(t, "rollout-fixtures.json", &fixture)
	scenario := fixture.OffsetResumeScenario
	dir := t.TempDir()
	path := writeFixtureRolloutFile(t, dir, "offset.jsonl", scenario.InitialLines)

	first, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Accepted) != scenario.ExpectedInitialAcceptedCount {
		t.Fatalf("initial pass: expected %d accepted, got %d", scenario.ExpectedInitialAcceptedCount, len(first.Accepted))
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range scenario.AppendedLines {
		if _, err := file.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	resumed, err := codexadapter.ImportRolloutFile(path, first.Checkpoint, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.Accepted) != scenario.ExpectedResumeAcceptedCount {
		t.Fatalf("resume pass: expected %d accepted, got %d", scenario.ExpectedResumeAcceptedCount, len(resumed.Accepted))
	}
	if len(resumed.Accepted) > 0 && string(resumed.Accepted[0].Kind) != scenario.ExpectedResumeKind {
		t.Fatalf("resume pass: expected first accepted kind %q, got %q", scenario.ExpectedResumeKind, resumed.Accepted[0].Kind)
	}
}

func TestFixtureRolloutReplayIsIdempotent(t *testing.T) {
	var fixture rolloutFixtures
	readFixtureJSON(t, "rollout-fixtures.json", &fixture)
	scenario := fixture.ReplayIdempotencyScenario
	dir := t.TempDir()
	path := writeFixtureRolloutFile(t, dir, "replay.jsonl", scenario.JSONLLines)

	first, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Accepted) != scenario.ExpectedAcceptedCount || len(second.Accepted) != scenario.ExpectedAcceptedCount {
		t.Fatalf("expected %d accepted on each replay, got %d and %d", scenario.ExpectedAcceptedCount, len(first.Accepted), len(second.Accepted))
	}
	firstJSON, _ := json.Marshal(first.Accepted)
	secondJSON, _ := json.Marshal(second.Accepted)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("replaying the same byte range from a fresh checkpoint must yield byte-identical accepted records")
	}
	if first.Checkpoint.FirstRecordFingerprint != second.Checkpoint.FirstRecordFingerprint ||
		first.Checkpoint.LastRecordFingerprint != second.Checkpoint.LastRecordFingerprint {
		t.Fatal("replaying the same byte range must yield identical first/last record fingerprints")
	}
}

func TestFixtureRolloutRotationDetectedViaTamperedIdentity(t *testing.T) {
	var fixture rolloutFixtures
	readFixtureJSON(t, "rollout-fixtures.json", &fixture)
	scenario := fixture.RotationScenario
	dir := t.TempDir()
	path := writeFixtureRolloutFile(t, dir, "rotate.jsonl", scenario.JSONLLines)

	checkpoint := codexadapter.RolloutCheckpoint{
		FileIdentity: codexadapter.RolloutFileIdentity{PathPseudonym: scenario.TamperedPathPseudonym},
	}
	_, err := codexadapter.ImportRolloutFile(path, checkpoint, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err == nil {
		t.Fatal("expected rotation to be detected as an error, got none")
	}
	if err.Error() != scenario.ExpectError {
		t.Fatalf("expected error %q, got %q", scenario.ExpectError, err.Error())
	}
}

func TestFixtureRolloutTruncationNeverSilentlyRewinds(t *testing.T) {
	var fixture rolloutFixtures
	readFixtureJSON(t, "rollout-fixtures.json", &fixture)
	scenario := fixture.TruncationScenario
	dir := t.TempDir()
	path := writeFixtureRolloutFile(t, dir, "trunc.jsonl", scenario.JSONLLines)

	// First pass establishes a checkpoint at the file's full size.
	initial, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Truncate(path, scenario.TruncateToBytes); err != nil {
		t.Fatal(err)
	}

	result, err := codexadapter.ImportRolloutFile(path, initial.Checkpoint, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err == nil {
		t.Fatal("expected truncation to be detected as an error, got none")
	}
	if err.Error() != scenario.ExpectError {
		t.Fatalf("expected error %q, got %q", scenario.ExpectError, err.Error())
	}
	if result.TruncationDetected != scenario.ExpectTruncationDetected {
		t.Fatalf("expected TruncationDetected=%v, got %v", scenario.ExpectTruncationDetected, result.TruncationDetected)
	}
}

func TestFixtureRolloutCrashMidImportResumesExactlyAtNextRecord(t *testing.T) {
	var fixture rolloutFixtures
	readFixtureJSON(t, "rollout-fixtures.json", &fixture)
	scenario := fixture.CrashMidImportScenario
	dir := t.TempDir()

	preCrashLines := scenario.JSONLLines[:scenario.SimulatedCrashAfterRecordIndex+1]
	path := writeFixtureRolloutFile(t, dir, "crash.jsonl", preCrashLines)

	preCrash, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(preCrash.Accepted) != scenario.ExpectedPreCrashAcceptedCount {
		t.Fatalf("pre-crash: expected %d accepted, got %d", scenario.ExpectedPreCrashAcceptedCount, len(preCrash.Accepted))
	}

	// Simulate the crash: the checkpoint committed after the pre-crash pass
	// is the only durable state carried across the "restart". The rest of
	// the file (as it would exist on a real Codex session tree that kept
	// appending) is written now, mirroring a process that resumes after
	// being killed with no partially-applied side effects.
	remaining := scenario.JSONLLines[scenario.SimulatedCrashAfterRecordIndex+1:]
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range remaining {
		if _, err := file.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	postRestart, err := codexadapter.ImportRolloutFile(path, preCrash.Checkpoint, testPseudonymKey(), codexadapter.RolloutImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(postRestart.Accepted) != scenario.ExpectedPostRestartAcceptedCount {
		t.Fatalf("post-restart: expected %d accepted, got %d", scenario.ExpectedPostRestartAcceptedCount, len(postRestart.Accepted))
	}
	total := len(preCrash.Accepted) + len(postRestart.Accepted)
	if total != scenario.ExpectedTotalAcceptedCount {
		t.Fatalf("expected total accepted across both passes %d, got %d (never reprocessing or skipping the crash-boundary record)", scenario.ExpectedTotalAcceptedCount, total)
	}
}

func TestFixtureRolloutProhibitedContentNeverLeaksRawText(t *testing.T) {
	var fixture rolloutFixtures
	readFixtureJSON(t, "rollout-fixtures.json", &fixture)
	scenario := fixture.ProhibitedContentCanary
	dir := t.TempDir()
	path := writeFixtureRolloutFile(t, dir, "prohibited.jsonl", []string{scenario.RawContentLine})

	for _, allowTransient := range []bool{false, true} {
		result, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{AllowTransientContentParsing: allowTransient})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result.Accepted)
		for _, forbidden := range scenario.ForbiddenSubstrings {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("allow_transient_content_parsing=%v: forbidden substring %q leaked into accepted records", allowTransient, forbidden)
			}
		}
	}
}

// --- inventory-layouts.json ---------------------------------------------------

type inventorySkillFixture struct {
	Name             string `json:"name"`
	Scope            string `json:"scope"`
	Enabled          bool   `json:"enabled"`
	DescriptionBytes int    `json:"description_bytes"`
	DescriptionChars int    `json:"description_chars"`
}

type inventoryPluginFixture struct {
	Name             string `json:"name"`
	Scope            string `json:"scope"`
	CachedOnly       bool   `json:"cached_only"`
	ActiveEnabledFor string `json:"active_enabled_for"`
}

type inventoryHookFixture struct {
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	Enabled bool   `json:"enabled"`
	Trusted bool   `json:"trusted"`
}

type inventoryMCPFixture struct {
	Name            string   `json:"name"`
	Scope           string   `json:"scope"`
	Enabled         bool     `json:"enabled"`
	AdvertisedTools []string `json:"advertised_tools"`
}

type inventoryLayoutFixture struct {
	Name                                   string                   `json:"name"`
	CodexHomeEnvSet                        bool                     `json:"codex_home_env_set"`
	CodexHomeValueRelpath                  string                   `json:"codex_home_value_relpath"`
	SurfaceMarkersPresent                  []string                 `json:"surface_markers_present"`
	ExpectedSurfacesDetected               []string                 `json:"expected_surfaces_detected"`
	ExpectedCandidateCount                 int                      `json:"expected_candidate_count"`
	DocumentedDefaultStateRoot             string                   `json:"documented_default_state_root"`
	ExpectedResolutionOrder                []string                 `json:"expected_resolution_order"`
	ExpectedSharedStateRoot                bool                     `json:"expected_shared_state_root"`
	InstallationID                         string                   `json:"installation_id"`
	RepositoryTargets                      []string                 `json:"repository_targets"`
	Skills                                 []inventorySkillFixture  `json:"skills"`
	Plugins                                []inventoryPluginFixture `json:"plugins"`
	Hooks                                  []inventoryHookFixture   `json:"hooks"`
	MCPServers                             []inventoryMCPFixture    `json:"mcp_servers"`
	ExpectedRepositoryTargetCount          int                      `json:"expected_repository_target_count"`
	ExpectedRepositoryTargetMustBeAbsolute bool                     `json:"expected_repository_target_must_be_absolute"`
	ExpectError                            string                   `json:"expect_error"`
	ExpectedNodeKind                       string                   `json:"expected_node_kind"`
	ExpectedEnabledForEdge                 bool                     `json:"expected_enabled_for_edge"`
	ExpectedDistinctServerNodeCount        int                      `json:"expected_distinct_server_node_count"`
	ExpectedCollisionEdge                  bool                     `json:"expected_collision_edge"`
}

type inventoryLayoutsFixture struct {
	Layouts []inventoryLayoutFixture `json:"layouts"`
}

func TestFixtureInventoryLayoutsCodexHomeResolutionAndSurfaces(t *testing.T) {
	var fixture inventoryLayoutsFixture
	readFixtureJSON(t, "inventory-layouts.json", &fixture)
	if len(fixture.Layouts) == 0 {
		t.Fatal("fixture must declare at least one inventory layout")
	}

	for _, layout := range fixture.Layouts {
		layout := layout
		t.Run(layout.Name, func(t *testing.T) {
			switch layout.Name {
			case "codex_home_env_var_resolved_single_cli_surface", "multi_surface_shared_state_root_cli_and_ide_extension", "multi_surface_all_three_surfaces_present":
				runDiscoverySurfaceLayout(t, layout)
			case "codex_home_absent_falls_back_to_documented_default":
				if len(layout.ExpectedResolutionOrder) != 2 || layout.ExpectedResolutionOrder[0] != "CODEX_HOME" || layout.ExpectedResolutionOrder[1] != "documented_default_state_root" {
					t.Fatalf("resolution order must check CODEX_HOME before the documented default, got %v", layout.ExpectedResolutionOrder)
				}
				if layout.DocumentedDefaultStateRoot != codexadapter.DocumentedDefaultStateRoot {
					t.Fatalf("fixture documented default %q must match codexadapter.DocumentedDefaultStateRoot %q", layout.DocumentedDefaultStateRoot, codexadapter.DocumentedDefaultStateRoot)
				}
			case "project_scope_explicit_repository_target":
				runRepositoryTargetLayout(t, layout, false)
			case "project_scope_relative_target_rejected":
				runRepositoryTargetLayout(t, layout, true)
			case "cache_only_plugin_never_enabled_even_with_active_installation_target":
				runCacheOnlyPluginLayout(t, layout)
			case "hook_enabled_but_untrusted_never_gets_enabled_edge", "hook_enabled_and_trusted_gets_enabled_edge":
				runHookTrustLayout(t, layout)
			case "mcp_server_and_tool_nodes_with_collision":
				runMCPCollisionLayout(t, layout)
			default:
				t.Fatalf("unrecognized inventory layout fixture case %q -- add handling for it", layout.Name)
			}
		})
	}
}

func runDiscoverySurfaceLayout(t *testing.T, layout inventoryLayoutFixture) {
	t.Helper()
	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "codex-home")
	if err := os.MkdirAll(resolvedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, marker := range layout.SurfaceMarkersPresent {
		content := "{}"
		if marker == "config.toml" {
			content = "model = \"gpt\""
		}
		if err := os.WriteFile(filepath.Join(resolvedRoot, marker), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	host, err := adaptersdk.NewHostView([]string{resolvedRoot}, []string{"codex"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	host.SetExecCommandForTest(func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
		return []byte("codex-cli 1.2.3\n"), 0, nil
	})
	adapter := codexadapter.New()
	candidates, err := adapter.Discover(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != layout.ExpectedCandidateCount {
		t.Fatalf("expected %d candidates, got %d", layout.ExpectedCandidateCount, len(candidates))
	}
	gotSurfaces := map[string]bool{}
	for _, c := range candidates {
		gotSurfaces[c.SurfaceID] = true
		if layout.ExpectedSharedStateRoot && c.StateRoot != resolvedRoot {
			t.Fatalf("all candidates must share the same state root, got %q", c.StateRoot)
		}
	}
	for _, want := range layout.ExpectedSurfacesDetected {
		if !gotSurfaces[want] {
			t.Fatalf("expected surface %q detected, got %+v", want, gotSurfaces)
		}
	}
}

func runRepositoryTargetLayout(t *testing.T, layout inventoryLayoutFixture, expectErr bool) {
	t.Helper()
	var skills []codexadapter.SkillDescriptor
	for _, s := range layout.Skills {
		skills = append(skills, codexadapter.SkillDescriptor{
			Name: s.Name, Scope: adaptersdk.SourceScope(s.Scope), Enabled: s.Enabled,
			DescriptionBytes: s.DescriptionBytes, DescriptionChars: s.DescriptionChars,
		})
	}
	input := codexadapter.InventoryInput{
		InstallationID:    layout.InstallationID,
		Skills:            skills,
		RepositoryTargets: layout.RepositoryTargets,
	}
	snapshot, err := codexadapter.BuildInventorySnapshot(input, time.Now())
	if expectErr {
		if err == nil {
			t.Fatal("expected repository target rejection error, got none")
		}
		if err.Error() != layout.ExpectError {
			t.Fatalf("expected error %q, got %q", layout.ExpectError, err.Error())
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layout.RepositoryTargets) != layout.ExpectedRepositoryTargetCount {
		t.Fatalf("fixture repository target count mismatch: declared %d, expected %d", len(layout.RepositoryTargets), layout.ExpectedRepositoryTargetCount)
	}
	if layout.ExpectedRepositoryTargetMustBeAbsolute {
		for _, target := range layout.RepositoryTargets {
			if !filepath.IsAbs(target) {
				t.Fatalf("fixture declares an absolute-required target that is not absolute: %q", target)
			}
		}
	}
	if len(snapshot.Nodes) == 0 {
		t.Fatal("expected at least one node in the built snapshot")
	}
}

func runCacheOnlyPluginLayout(t *testing.T, layout inventoryLayoutFixture) {
	t.Helper()
	var plugins []codexadapter.PluginDescriptor
	for _, p := range layout.Plugins {
		plugins = append(plugins, codexadapter.PluginDescriptor{
			Name: p.Name, Scope: adaptersdk.SourceScope(p.Scope), CachedOnly: p.CachedOnly, ActiveEnabledFor: p.ActiveEnabledFor,
		})
	}
	snapshot, err := codexadapter.BuildInventorySnapshot(codexadapter.InventoryInput{
		InstallationID: layout.InstallationID, Plugins: plugins,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var pluginNode *adaptersdk.Node
	for i := range snapshot.Nodes {
		if snapshot.Nodes[i].Kind == adaptersdk.NodeKind(layout.ExpectedNodeKind) {
			pluginNode = &snapshot.Nodes[i]
		}
	}
	if pluginNode == nil {
		t.Fatalf("expected a node of kind %q in snapshot", layout.ExpectedNodeKind)
	}
	hasEnabledEdge := false
	for _, e := range snapshot.Edges {
		if e.Kind == adaptersdk.EdgeEnabledFor && e.FromNode == pluginNode.NodeID {
			hasEnabledEdge = true
		}
	}
	if hasEnabledEdge != layout.ExpectedEnabledForEdge {
		t.Fatalf("expected enabled_for edge=%v, got %v", layout.ExpectedEnabledForEdge, hasEnabledEdge)
	}
}

func runHookTrustLayout(t *testing.T, layout inventoryLayoutFixture) {
	t.Helper()
	var hooks []codexadapter.HookDescriptor
	for _, h := range layout.Hooks {
		hooks = append(hooks, codexadapter.HookDescriptor{
			Name: h.Name, Scope: adaptersdk.SourceScope(h.Scope), Enabled: h.Enabled, Trusted: h.Trusted,
		})
	}
	snapshot, err := codexadapter.BuildInventorySnapshot(codexadapter.InventoryInput{
		InstallationID: layout.InstallationID, Hooks: hooks,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var hookNode *adaptersdk.Node
	for i := range snapshot.Nodes {
		if snapshot.Nodes[i].Kind == adaptersdk.NodeHookDefinition {
			hookNode = &snapshot.Nodes[i]
		}
	}
	if hookNode == nil {
		t.Fatal("expected a hook_definition node in snapshot")
	}
	hasEnabledEdge := false
	for _, e := range snapshot.Edges {
		if e.Kind == adaptersdk.EdgeEnabledFor && e.FromNode == hookNode.NodeID {
			hasEnabledEdge = true
		}
	}
	if hasEnabledEdge != layout.ExpectedEnabledForEdge {
		t.Fatalf("expected enabled_for edge=%v (kansoku never bypasses hook trust), got %v", layout.ExpectedEnabledForEdge, hasEnabledEdge)
	}
}

func runMCPCollisionLayout(t *testing.T, layout inventoryLayoutFixture) {
	t.Helper()
	var servers []codexadapter.MCPServerDescriptor
	for _, m := range layout.MCPServers {
		servers = append(servers, codexadapter.MCPServerDescriptor{
			Name: m.Name, Scope: adaptersdk.SourceScope(m.Scope), Enabled: m.Enabled, AdvertisedTools: m.AdvertisedTools,
		})
	}
	snapshot, err := codexadapter.BuildInventorySnapshot(codexadapter.InventoryInput{
		InstallationID: layout.InstallationID, MCPServers: servers,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	serverNodeCount := 0
	var serverNodeIDs []string
	for _, n := range snapshot.Nodes {
		if n.Kind == adaptersdk.NodeMCPServerInstance {
			serverNodeCount++
			serverNodeIDs = append(serverNodeIDs, n.NodeID)
		}
	}
	if serverNodeCount != layout.ExpectedDistinctServerNodeCount {
		t.Fatalf("expected %d distinct MCP server nodes (never merged), got %d", layout.ExpectedDistinctServerNodeCount, serverNodeCount)
	}
	hasCollision := false
	for _, e := range snapshot.Edges {
		if e.Kind == adaptersdk.EdgeCollidesWith {
			for _, id := range serverNodeIDs {
				if e.FromNode == id || e.ToNode == id {
					hasCollision = true
				}
			}
		}
	}
	if hasCollision != layout.ExpectedCollisionEdge {
		t.Fatalf("expected collision edge=%v among colliding MCP server nodes, got %v", layout.ExpectedCollisionEdge, hasCollision)
	}
}

// --- skill-collision-and-ambiguous-ownership.json ---------------------------

type skillFixtureDescriptor struct {
	Name             string `json:"name"`
	Scope            string `json:"scope"`
	Enabled          bool   `json:"enabled"`
	DescriptionBytes int    `json:"description_bytes"`
	DescriptionChars int    `json:"description_chars"`
}

type collisionScenarioFixture struct {
	InstallationID             string                   `json:"installation_id"`
	Skills                     []skillFixtureDescriptor `json:"skills"`
	ExpectedDistinctNodeCount  int                      `json:"expected_distinct_node_count"`
	ExpectedCollisionEdgeCount int                      `json:"expected_collision_edge_count"`
}

type ambiguousOwnershipScenario struct {
	Case                       string   `json:"case"`
	EvidenceKind               string   `json:"evidence_kind"`
	CandidateSkillIdentities   []string `json:"candidate_skill_identities"`
	SourceLabelsNative         bool     `json:"source_labels_native"`
	ExpectError                *string  `json:"expect_error"`
	ExpectedCanonicalEventType string   `json:"expected_canonical_event_type"`
	ExpectedTier               string   `json:"expected_tier"`
	MustNeverResolveTo         string   `json:"must_never_resolve_to"`
}

type skillCollisionFixture struct {
	CollisionScenario        collisionScenarioFixture `json:"collision_scenario"`
	ShadowPrecedenceScenario struct {
		ExpectedPrecedenceOrderMostToLeastAuthoritative []string `json:"expected_precedence_order_most_to_least_authoritative"`
	} `json:"shadow_precedence_scenario"`
	AmbiguousOwnershipScenarios []ambiguousOwnershipScenario `json:"ambiguous_ownership_scenarios"`
}

func TestFixtureSkillCollisionNeverMergesDistinctScopeNodes(t *testing.T) {
	var fixture skillCollisionFixture
	readFixtureJSON(t, "skill-collision-and-ambiguous-ownership.json", &fixture)
	scenario := fixture.CollisionScenario

	var skills []codexadapter.SkillDescriptor
	for _, s := range scenario.Skills {
		skills = append(skills, codexadapter.SkillDescriptor{
			Name: s.Name, Scope: adaptersdk.SourceScope(s.Scope), Enabled: s.Enabled,
			DescriptionBytes: s.DescriptionBytes, DescriptionChars: s.DescriptionChars,
		})
	}
	snapshot, err := codexadapter.BuildInventorySnapshot(codexadapter.InventoryInput{
		InstallationID: scenario.InstallationID, Skills: skills,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	skillNodeCount := 0
	nodeIDs := map[string]bool{}
	for _, n := range snapshot.Nodes {
		if n.Kind == adaptersdk.NodeSkillIdentity {
			skillNodeCount++
			nodeIDs[n.NodeID] = true
		}
	}
	if skillNodeCount != scenario.ExpectedDistinctNodeCount {
		t.Fatalf("expected %d distinct skill nodes (never merged across scopes), got %d", scenario.ExpectedDistinctNodeCount, skillNodeCount)
	}
	collisionCount := 0
	for _, e := range snapshot.Edges {
		if e.Kind == adaptersdk.EdgeCollidesWith && nodeIDs[e.FromNode] && nodeIDs[e.ToNode] {
			collisionCount++
		}
	}
	if collisionCount != scenario.ExpectedCollisionEdgeCount {
		t.Fatalf("expected %d pairwise collides_with edges, got %d", scenario.ExpectedCollisionEdgeCount, collisionCount)
	}
}

func TestFixtureShadowPrecedenceNeverInfluencesIdentityOnlyDisplayOrder(t *testing.T) {
	var fixture skillCollisionFixture
	readFixtureJSON(t, "skill-collision-and-ambiguous-ownership.json", &fixture)
	order := fixture.ShadowPrecedenceScenario.ExpectedPrecedenceOrderMostToLeastAuthoritative
	if len(order) == 0 {
		t.Fatal("fixture must declare a precedence order")
	}
	var ranks []int
	for _, scopeName := range order {
		rank, ok := codexadapter.ScopePrecedenceRank(adaptersdk.SourceScope(scopeName))
		if !ok {
			t.Fatalf("scope %q must be ranked", scopeName)
		}
		ranks = append(ranks, rank)
	}
	if !sort.IntsAreSorted(ranks) {
		t.Fatalf("expected ranks in strictly increasing (most to least authoritative) order, got %v for %v", ranks, order)
	}
}

func TestFixtureAmbiguousOwnershipScenariosNeverPromoteToInvoked(t *testing.T) {
	var fixture skillCollisionFixture
	readFixtureJSON(t, "skill-collision-and-ambiguous-ownership.json", &fixture)
	if len(fixture.AmbiguousOwnershipScenarios) == 0 {
		t.Fatal("fixture must declare at least one ambiguous ownership scenario")
	}
	for _, scenario := range fixture.AmbiguousOwnershipScenarios {
		scenario := scenario
		t.Run(scenario.Case, func(t *testing.T) {
			resolution, err := codexadapter.ResolveSkillEvidence(codexadapter.SkillEvidenceInput{
				Kind:                     codexadapter.SkillEvidenceKind(scenario.EvidenceKind),
				CandidateSkillIdentities: scenario.CandidateSkillIdentities,
				SourceLabelsNative:       scenario.SourceLabelsNative,
			})
			if scenario.ExpectError != nil {
				if err == nil {
					t.Fatalf("expected error %q, got none", *scenario.ExpectError)
				}
				if err.Error() != *scenario.ExpectError {
					t.Fatalf("expected error %q, got %q", *scenario.ExpectError, err.Error())
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if scenario.ExpectedCanonicalEventType != "" && resolution.CanonicalEventType != scenario.ExpectedCanonicalEventType {
				t.Fatalf("expected canonical event type %q, got %q", scenario.ExpectedCanonicalEventType, resolution.CanonicalEventType)
			}
			if scenario.ExpectedTier != "" && string(resolution.Tier) != scenario.ExpectedTier {
				t.Fatalf("expected tier %q, got %q", scenario.ExpectedTier, resolution.Tier)
			}
			if scenario.MustNeverResolveTo != "" && resolution.CanonicalEventType == scenario.MustNeverResolveTo {
				t.Fatalf("must never resolve to %q, but it did", scenario.MustNeverResolveTo)
			}
		})
	}
}

// --- prohibited-content-canaries.json ----------------------------------------

type prohibitedContentCanaryCase struct {
	Case                         string          `json:"case"`
	Surface                      string          `json:"surface"`
	RawInput                     json.RawMessage `json:"raw_input"`
	RawLine                      string          `json:"raw_line"`
	AllowTransientContentParsing bool            `json:"allow_transient_content_parsing"`
	RawConfigContent             string          `json:"raw_config_content"`
	ForbiddenSubstringsInOutput  []string        `json:"forbidden_substrings_in_output"`
	AllowedFieldsInOutput        []string        `json:"allowed_fields_in_output"`
	ExpectPromptFeaturesPresent  bool            `json:"expect_prompt_features_present"`
	ForbiddenDroppedSurfaces     []string        `json:"forbidden_dropped_surfaces"`
}

type prohibitedContentCanariesFixture struct {
	Canaries []prohibitedContentCanaryCase `json:"canaries"`
}

func TestFixtureProhibitedContentCanariesNeverLeakRawText(t *testing.T) {
	var fixture prohibitedContentCanariesFixture
	readFixtureJSON(t, "prohibited-content-canaries.json", &fixture)
	if len(fixture.Canaries) == 0 {
		t.Fatal("fixture must declare at least one prohibited-content canary")
	}
	for _, c := range fixture.Canaries {
		c := c
		t.Run(c.Case, func(t *testing.T) {
			switch c.Surface {
			case "codex.hook":
				input, err := codexadapter.DecodeHookInput(strings.NewReader(string(c.RawInput)))
				if err != nil {
					t.Fatal(err)
				}
				output, err := codexadapter.BuildHookOutput(input, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(output)
				for _, forbidden := range c.ForbiddenSubstringsInOutput {
					if strings.Contains(string(encoded), forbidden) {
						t.Fatalf("forbidden substring %q leaked into hook output", forbidden)
					}
				}
				var generic map[string]json.RawMessage
				if err := json.Unmarshal(encoded, &generic); err != nil {
					t.Fatal(err)
				}
				allowed := map[string]bool{}
				for _, f := range c.AllowedFieldsInOutput {
					allowed[f] = true
				}
				for field := range generic {
					if !allowed[field] {
						t.Fatalf("field %q in hook output is not in the fixture's allowed field list", field)
					}
				}
			case "codex.rollout":
				dir := t.TempDir()
				path := writeFixtureRolloutFile(t, dir, "prohibited.jsonl", []string{c.RawLine})
				result, err := codexadapter.ImportRolloutFile(path, codexadapter.RolloutCheckpoint{}, testPseudonymKey(), codexadapter.RolloutImportOptions{AllowTransientContentParsing: c.AllowTransientContentParsing})
				if err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(result.Accepted)
				for _, forbidden := range c.ForbiddenSubstringsInOutput {
					if strings.Contains(string(encoded), forbidden) {
						t.Fatalf("forbidden substring %q leaked into rollout output", forbidden)
					}
				}
				if c.ExpectPromptFeaturesPresent {
					if len(result.Accepted) == 0 || result.Accepted[0].PromptFeatures == nil {
						t.Fatal("expected prompt_features to be present when opted into transient content parsing")
					}
				}
			case "codex.manifest_fingerprint":
				tempRoot := t.TempDir()
				root := filepath.Join(tempRoot, "codex-home")
				if err := os.MkdirAll(root, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(c.RawConfigContent), 0o600); err != nil {
					t.Fatal(err)
				}
				host, err := adaptersdk.NewHostView([]string{root}, []string{"codex"}, testPseudonymKey())
				if err != nil {
					t.Fatal(err)
				}
				fingerprint, err := codexadapter.FingerprintInstallation(host, root)
				if err != nil {
					t.Fatal(err)
				}
				for _, forbidden := range c.ForbiddenSubstringsInOutput {
					if strings.Contains(fingerprint, forbidden) {
						t.Fatalf("forbidden substring %q leaked into installation fingerprint", forbidden)
					}
				}
			case "codex.otel":
				safe := map[string]bool{}
				for _, key := range codexadapter.OTLPSafeAttributes() {
					safe[key] = true
				}
				dropped := codexadapter.DroppedOTelSurfaces()
				gotDropped := map[string]bool{}
				for _, d := range dropped {
					gotDropped[d] = true
					if safe[d] {
						t.Fatalf("dropped surface %q must never overlap with the safe attribute allowlist", d)
					}
				}
				for _, want := range c.ForbiddenDroppedSurfaces {
					if !gotDropped[want] {
						t.Fatalf("expected dropped surface %q to be in the closed dropped list", want)
					}
				}
			default:
				t.Fatalf("unrecognized prohibited-content canary surface %q", c.Surface)
			}
		})
	}
}
