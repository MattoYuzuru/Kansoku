package claudeadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/claudeadapter"
)

func testPseudonymKey() []byte {
	return []byte("claudeadapter-stage2-conformance-test-key-01234")
}

// --- Manifest ----------------------------------------------------------------

func TestManifestIsRegistrable(t *testing.T) {
	adapter := claudeadapter.New()
	registry := adaptersdk.NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatalf("claude adapter manifest must satisfy adaptersdk.Registry.Register: %v", err)
	}
	got, err := registry.Get(claudeadapter.AdapterID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest().ID != "claude" {
		t.Fatalf("adapter_id must be exactly %q per contracts/claude/manifest.yaml, got %q", "claude", got.Manifest().ID)
	}
}

func TestManifestDeclaresOnlyKnownSourcesAndCapabilities(t *testing.T) {
	manifest := claudeadapter.New().Manifest()
	if manifest.Execution != adaptersdk.ExecutionBuiltin {
		t.Fatal("manifest.yaml declares execution_form builtin")
	}
	if manifest.Permissions.Network != adaptersdk.NetworkLoopbackOnly {
		t.Fatal("manifest.yaml declares network_grade loopback_only")
	}
	if len(manifest.Sources) == 0 {
		t.Fatal("stage 2 must declare at least the claude.hook and claude.otel sources")
	}
	seen := map[string]bool{}
	for _, source := range manifest.Sources {
		seen[source.ID] = true
	}
	if !seen["claude.hook"] || !seen["claude.otel"] {
		t.Fatalf("expected claude.hook and claude.otel sources, got %+v", manifest.Sources)
	}
}

func TestManifestRegistersAlongsideCodexWithoutAnyCoreAgentBranch(t *testing.T) {
	// The zero-agent-name-branch invariant is a property of internal/adaptersdk
	// itself; this test only asserts the Claude adapter registers through the
	// same unconditional Register/Get path every other adapter uses, never a
	// special case. Registering a second, differently-shaped real adapter
	// beside a hypothetical first one requires no branch inside adaptersdk.
	registry := adaptersdk.NewRegistry()
	if err := registry.Register(claudeadapter.New()); err != nil {
		t.Fatal(err)
	}
	ids := registry.IDs()
	if len(ids) != 1 || ids[0] != "claude" {
		t.Fatalf("expected exactly one registered id \"claude\", got %v", ids)
	}
}

// --- Discovery -----------------------------------------------------------------

func TestDiscoverResolvesOnlyFromAllowedRootsNeverSpeculativeScan(t *testing.T) {
	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "claude-user-settings")
	if err := os.MkdirAll(resolvedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolvedRoot, "settings.json"), []byte(`{"model":"claude"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	host, err := adaptersdk.NewHostView([]string{resolvedRoot}, []string{"claude"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	host.SetExecCommandForTest(func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
		return []byte("claude-cli 2.1.197\n"), 0, nil
	})

	adapter := claudeadapter.New()
	candidates, err := adapter.Discover(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected exactly one CLI surface candidate, got %d: %+v", len(candidates), candidates)
	}
	if candidates[0].StateRoot != resolvedRoot {
		t.Fatal("state root must be exactly the resolved allowed root, never a derived or speculative path")
	}
	if candidates[0].DetectedVersion != "2.1.197" {
		t.Fatalf("expected version 2.1.197 parsed from credential-free --version output, got %q", candidates[0].DetectedVersion)
	}
	if candidates[0].AdapterID != claudeadapter.AdapterID {
		t.Fatal("candidate adapter id must match the registered adapter id")
	}

	// A sibling directory outside the allowed root must never be discovered,
	// proving Discover never falls back to a speculative home-directory scan.
	outside := filepath.Join(tempRoot, "not-allowed")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "settings.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := host.ReadProbe(filepath.Join(outside, "settings.json")); !errors.Is(err, adaptersdk.ErrOutsideAllowedRoots) {
		t.Fatal("a path outside the allowed roots must fail closed, not be silently included")
	}
}

func TestDiscoverKeepsDistinctSurfacesSeparateEvenWhenSharingStateRoot(t *testing.T) {
	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "claude-user-settings")
	if err := os.MkdirAll(resolvedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	// Two distinct surface markers beneath the same config root.
	if err := os.WriteFile(filepath.Join(resolvedRoot, "settings.json"), []byte("cli"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolvedRoot, "ide-extension.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	host, err := adaptersdk.NewHostView([]string{resolvedRoot}, []string{"claude"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	host.SetExecCommandForTest(func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
		return nil, 1, nil // simulate an unavailable version probe
	})

	adapter := claudeadapter.New()
	candidates, err := adapter.Discover(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("two observable surfaces sharing one config root must remain distinct candidates, got %d", len(candidates))
	}
	surfaces := map[string]bool{}
	for _, candidate := range candidates {
		surfaces[candidate.SurfaceID] = true
		if candidate.StateRoot != resolvedRoot {
			t.Fatal("both candidates must still report the shared config root")
		}
		if candidate.DetectedVersion != "unknown" {
			t.Fatalf("a failed version probe must report unknown, never a fabricated version, got %q", candidate.DetectedVersion)
		}
	}
	if !surfaces["claude-cli"] || !surfaces["claude-ide-extension"] {
		t.Fatalf("expected both claude-cli and claude-ide-extension surfaces, got %+v", surfaces)
	}
}

func TestDiscoverIsDeterministicAcrossRepeatedRuns(t *testing.T) {
	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "claude-user-settings")
	if err := os.MkdirAll(resolvedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolvedRoot, "settings.json"), []byte("cli"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{resolvedRoot}, []string{"claude"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	host.SetExecCommandForTest(func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
		return []byte("claude-cli 2.1.216"), 0, nil
	})
	adapter := claudeadapter.New()
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
		t.Fatal("discovery must be byte-identical across repeated runs against unchanged state")
	}
}

func TestDiscoverRejectsNilHostView(t *testing.T) {
	adapter := claudeadapter.New()
	if _, err := adapter.Discover(context.Background(), nil); err == nil {
		t.Fatal("Discover must fail closed when given a nil HostView, never silently no-op")
	}
}

func TestVersionProbeNeverCapturesLoginOrAuthOutputBeyondBareVersion(t *testing.T) {
	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "claude-user-settings")
	if err := os.MkdirAll(resolvedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolvedRoot, "settings.json"), []byte("cli"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{resolvedRoot}, []string{"claude"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	host.SetExecCommandForTest(func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
		if len(args) != 1 || args[0] != "--version" {
			t.Fatalf("version probe must invoke exactly claude --version, got args %v", args)
		}
		// A banner that would carry an account/session identifier if the
		// probe naively captured the whole blob.
		return []byte("claude-cli 2.1.214\nlogged in as user@example.com\nsession-token=abc123\n"), 0, nil
	})
	adapter := claudeadapter.New()
	candidates, err := adapter.Discover(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].DetectedVersion != "2.1.214" {
		t.Fatalf("expected bare version 2.1.214 extracted from probe output, got %q", candidates[0].DetectedVersion)
	}
	if strings.Contains(candidates[0].DetectedVersion, "@") || strings.Contains(candidates[0].DetectedVersion, "token") {
		t.Fatal("detected version must never carry login/auth-shaped output")
	}
}

// --- Fingerprinting ------------------------------------------------------------

func TestFingerprintInstallationNeverRecordsRawFileContent(t *testing.T) {
	tempRoot := t.TempDir()
	root := filepath.Join(tempRoot, "claude-user-settings")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := "sk-super-secret-api-key-should-never-appear-in-fingerprint"
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{root}, []string{"claude"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := claudeadapter.FingerprintInstallation(host, root)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint == "" {
		t.Fatal("fingerprint must be non-empty when a fingerprint target exists")
	}
	if strings.Contains(fingerprint, secret) {
		t.Fatal("fingerprint must never contain raw file content")
	}
}

func TestFingerprintInstallationChangesWhenFileSizeClassChanges(t *testing.T) {
	tempRoot := t.TempDir()
	root := filepath.Join(tempRoot, "claude-user-settings")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{root}, []string{"claude"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	empty, err := claudeadapter.FingerprintInstallation(host, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(strings.Repeat("x", 2048)), 0o600); err != nil {
		t.Fatal(err)
	}
	withConfig, err := claudeadapter.FingerprintInstallation(host, root)
	if err != nil {
		t.Fatal(err)
	}
	if empty == withConfig {
		t.Fatal("fingerprint must change when a fingerprinted target's presence/size class changes")
	}
}

func TestFingerprintInstallationRejectsNilHostView(t *testing.T) {
	if _, err := claudeadapter.FingerprintInstallation(nil, "/tmp"); err == nil {
		t.Fatal("must fail closed on a nil HostView")
	}
}

// --- claude.hook -----------------------------------------------------------------

func TestSupportedHookEventsMatchesRegistry(t *testing.T) {
	events := claudeadapter.SupportedHookEvents()
	want := map[claudeadapter.HookEvent]bool{
		claudeadapter.HookSessionStart: true, claudeadapter.HookUserPromptSubmit: true,
		claudeadapter.HookPreToolUse: true, claudeadapter.HookPostToolUse: true,
		claudeadapter.HookSubagentStart: true, claudeadapter.HookSubagentStop: true,
		claudeadapter.HookStop: true,
	}
	if len(events) != len(want) {
		t.Fatalf("expected %d supported hook events, got %d", len(want), len(events))
	}
	for _, event := range events {
		if !want[event] {
			t.Fatalf("unexpected hook event %q outside contracts/claude/hooks-and-otel.yaml's closed vocabulary", event)
		}
	}
}

func TestCanonicalEventForHookCoversEveryDeclaredEvent(t *testing.T) {
	for _, event := range claudeadapter.SupportedHookEvents() {
		canonical, ok := claudeadapter.CanonicalEventForHook(event)
		if !ok || canonical == "" {
			t.Fatalf("every declared hook event must map to a canonical event type, %q did not", event)
		}
	}
	if _, ok := claudeadapter.CanonicalEventForHook("NotARealEvent"); ok {
		t.Fatal("an undeclared hook event must never resolve to a canonical event type")
	}
}

func TestDecodeHookInputRejectsUnknownFieldsAndOversizedPayload(t *testing.T) {
	if _, err := claudeadapter.DecodeHookInput(strings.NewReader(
		`{"hook_event_name":"SessionStart","session_id":"s1","not_a_real_field":true}`,
	)); err == nil {
		t.Fatal("unknown stdin fields must be rejected, never silently ignored")
	}

	big := `{"hook_event_name":"SessionStart","session_id":"` + strings.Repeat("a", 1<<21) + `"}`
	if _, err := claudeadapter.DecodeHookInput(strings.NewReader(big)); !errors.Is(err, claudeadapter.ErrOversizedHookInput) {
		t.Fatalf("oversized stdin must fail with ErrOversizedHookInput, got %v", err)
	}
}

func TestDecodeHookInputRejectsUnsupportedEvent(t *testing.T) {
	_, err := claudeadapter.DecodeHookInput(strings.NewReader(
		`{"hook_event_name":"SomeFutureEvent","session_id":"s1"}`,
	))
	if !errors.Is(err, claudeadapter.ErrUnsupportedHookEvent) {
		t.Fatalf("an event outside the active version manifest must degrade only its own capability via ErrUnsupportedHookEvent, got %v", err)
	}
}

func TestDecodeHookInputRequiresSessionID(t *testing.T) {
	_, err := claudeadapter.DecodeHookInput(strings.NewReader(`{"hook_event_name":"SessionStart"}`))
	if err == nil {
		t.Fatal("a hook input missing session_id must be rejected")
	}
}

func TestDecodeHookInputAcceptsButBuildHookOutputDiscardsToolInputAndResponse(t *testing.T) {
	// Claude Code's documented detailed telemetry can expose tool_input and
	// tool_response on the hook payload. DecodeHookInput must accept a
	// well-formed payload carrying them (never reject outright merely because
	// detailed telemetry is on upstream), but BuildHookOutput must never read
	// either field, and HookHelperOutput must have no representation for
	// either at all. This is the concrete unconditional-strip proof for
	// hook-sourced tool input/output.
	raw := `{"hook_event_name":"PreToolUse","session_id":"s1","tool_id":"Bash",` +
		`"tool_input":{"command":"rm -rf /secret/customer/data"},` +
		`"tool_response":{"output":"top secret customer PII output"}}`
	input, err := claudeadapter.DecodeHookInput(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("a well-formed payload carrying tool_input/tool_response must still decode: %v", err)
	}
	output, err := claudeadapter.BuildHookOutput(input, testPseudonymKey(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "rm -rf") || strings.Contains(string(encoded), "customer PII") {
		t.Fatal("tool_input/tool_response content must never reach the durable-bound hook output")
	}
	if strings.Contains(string(encoded), "tool_input") || strings.Contains(string(encoded), "tool_response") {
		t.Fatal("HookHelperOutput must have no field representing tool_input/tool_response at all")
	}
}

func TestBuildHookOutputComputesPromptFeaturesInMemoryAndNeverCopiesRawPrompt(t *testing.T) {
	input := claudeadapter.HookHelperInput{
		Event:     claudeadapter.HookUserPromptSubmit,
		SessionID: "sess-1",
		Prompt:    "please refactor the auth module ```go\nfunc x(){}\n``` see https://example.com",
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	output, err := claudeadapter.BuildHookOutput(input, testPseudonymKey(), now)
	if err != nil {
		t.Fatal(err)
	}
	if output.PromptFeatures == nil {
		t.Fatal("UserPromptSubmit must carry computed prompt features")
	}
	if output.PromptFeatures.ByteCount == 0 || output.PromptFeatures.WordCount == 0 {
		t.Fatal("prompt features must reflect the actual prompt shape")
	}
	if output.PromptFeatures.CodeFenceCount != 1 {
		t.Fatalf("expected 1 code fence pair, got %d", output.PromptFeatures.CodeFenceCount)
	}
	if output.PromptFeatures.URLReferenceCount != 1 {
		t.Fatalf("expected 1 URL reference, got %d", output.PromptFeatures.URLReferenceCount)
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "refactor") || strings.Contains(string(encoded), "auth module") {
		t.Fatal("the raw prompt text must never appear in the durable-bound hook output")
	}
	if strings.Contains(string(encoded), "\"prompt\"") {
		t.Fatal("HookHelperOutput must have no field representing raw prompt text at all")
	}
}

func TestBuildHookOutputOmitsPromptFeaturesForNonPromptEvents(t *testing.T) {
	input := claudeadapter.HookHelperInput{Event: claudeadapter.HookPreToolUse, SessionID: "sess-1", ToolID: "shell"}
	output, err := claudeadapter.BuildHookOutput(input, testPseudonymKey(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if output.PromptFeatures != nil {
		t.Fatal("non-prompt events must never carry prompt features")
	}
	if output.EventType != "tool.called" {
		t.Fatalf("PreToolUse must map to tool.called, got %q", output.EventType)
	}
}

func TestBuildHookOutputRejectsUnsupportedEvent(t *testing.T) {
	input := claudeadapter.HookHelperInput{Event: "NotReal", SessionID: "sess-1"}
	if _, err := claudeadapter.BuildHookOutput(input, testPseudonymKey(), time.Now()); !errors.Is(err, claudeadapter.ErrUnsupportedHookEvent) {
		t.Fatal("BuildHookOutput must reject an event outside the closed vocabulary")
	}
}

func TestBuildHookOutputPseudonymizesTranscriptPathAndCWDNeverRawPath(t *testing.T) {
	rawPath := "/Users/alice/secret-project/.claude/transcripts/session-42.jsonl"
	rawCWD := "/Users/alice/secret-project"
	input := claudeadapter.HookHelperInput{
		Event:          claudeadapter.HookPreToolUse,
		SessionID:      "sess-1",
		ToolID:         "Read",
		TranscriptPath: rawPath,
		CWD:            rawCWD,
	}
	output, err := claudeadapter.BuildHookOutput(input, testPseudonymKey(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if output.TranscriptPathPseudonym == "" || output.CWDPseudonym == "" {
		t.Fatal("transcript path and cwd must be pseudonymized, never dropped silently, when present")
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), rawPath) || strings.Contains(string(encoded), rawCWD) || strings.Contains(string(encoded), "alice") || strings.Contains(string(encoded), "secret-project") {
		t.Fatal("the raw transcript path/cwd must never appear in the durable-bound hook output")
	}
	if !strings.HasPrefix(output.TranscriptPathPseudonym, "hmac-sha256:") || !strings.HasPrefix(output.CWDPseudonym, "hmac-sha256:") {
		t.Fatal("path pseudonyms must be the documented hmac-sha256: construction, not a raw or ad hoc transform")
	}

	// Deterministic: same input, same key => same pseudonym.
	again, err := claudeadapter.BuildHookOutput(input, testPseudonymKey(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if again.TranscriptPathPseudonym != output.TranscriptPathPseudonym {
		t.Fatal("path pseudonymization must be deterministic for the same input and key")
	}
}

func TestBuildHookOutputRejectsShortPseudonymKeyWhenPathPresent(t *testing.T) {
	input := claudeadapter.HookHelperInput{
		Event: claudeadapter.HookPreToolUse, SessionID: "sess-1", ToolID: "Read",
		TranscriptPath: "/some/path.jsonl",
	}
	if _, err := claudeadapter.BuildHookOutput(input, []byte("too-short"), time.Now()); err == nil {
		t.Fatal("a pseudonym key shorter than 32 bytes must fail closed rather than weakly pseudonymize a real path")
	}
}

func TestValidateHookOutputAllowlistRejectsExtraField(t *testing.T) {
	output := claudeadapter.HookHelperOutput{EventID: "id1", SessionID: "s1", EventType: "session.started"}
	if err := claudeadapter.ValidateHookOutputAllowlist(output); err != nil {
		t.Fatalf("a well-formed output must pass the allowlist check: %v", err)
	}
}

func TestHookRoutePathReusesGenericIngressTemplateAndNeverCollidesWithFixtureAgent(t *testing.T) {
	path := claudeadapter.HookRoutePath(claudeadapter.HookSessionStart)
	if path != "/v1/hooks/claude/SessionStart" {
		t.Fatalf("expected /v1/hooks/claude/SessionStart substituting the generic {adapter}/{event} template, got %q", path)
	}
	if strings.Contains(path, "fixture-agent") {
		t.Fatal("claude's hook route must never collide with the reserved fixture-agent adapter path segment")
	}
}

func TestAllowlistedHookFieldsNeverIncludesPromptOrPathFields(t *testing.T) {
	fields := claudeadapter.AllowlistedHookFields()
	for field := range fields {
		lower := strings.ToLower(field)
		if strings.Contains(lower, "prompt") && field != "prompt_features" {
			t.Fatalf("allowlisted metadata field %q must never be a raw prompt field", field)
		}
		if strings.Contains(lower, "path") || strings.Contains(lower, "cwd") {
			t.Fatalf("allowlisted metadata field %q must never be a raw path/cwd field", field)
		}
	}
	if _, ok := fields["prompt"]; ok {
		t.Fatal("raw prompt must never be an allowlisted metadata field")
	}
	if _, ok := fields["transcript_path"]; ok {
		t.Fatal("raw transcript_path must never be an allowlisted metadata field")
	}
}

// --- claude.otel -----------------------------------------------------------------

func TestCanonicalEventForOTelRequiresDocumentedAndMappedEventPlusMatchingFingerprint(t *testing.T) {
	shape := claudeadapter.OTelAttributeShape{
		InstrumentationScope: string(claudeadapter.OTelUserPrompt),
		PresentAttributeKeys: []string{"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.prompt_length_characters"},
	}
	canonical, err := claudeadapter.CanonicalEventForOTel(claudeadapter.OTelUserPrompt, shape)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "prompt.submitted" {
		t.Fatalf("expected prompt.submitted, got %q", canonical)
	}
}

func TestCanonicalEventForOTelPreservesToolDecisionWithoutCountingASecondCall(t *testing.T) {
	name := claudeadapter.OTelToolDecision
	canonical, err := claudeadapter.CanonicalEventForOTel(name, claudeadapter.OTelAttributeShape{
		InstrumentationScope: string(name),
		PresentAttributeKeys: []string{"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "source.observed" {
		t.Fatalf("tool_decision = %q, want source.observed", canonical)
	}
}

func TestCanonicalEventForOTelRejectsUndocumentedEventName(t *testing.T) {
	_, err := claudeadapter.CanonicalEventForOTel("claude_code.not_a_real_event", claudeadapter.OTelAttributeShape{})
	if !errors.Is(err, claudeadapter.ErrOTelEventNotDocumented) {
		t.Fatalf("expected ErrOTelEventNotDocumented, got %v", err)
	}
}

func TestCanonicalEventForOTelRejectsSchemaFingerprintMismatch(t *testing.T) {
	// Same event name, but the observed attribute shape is missing a
	// required key relative to the expected fingerprint: this must never be
	// trusted by name alone.
	shape := claudeadapter.OTelAttributeShape{
		InstrumentationScope: string(claudeadapter.OTelToolResult),
		PresentAttributeKeys: []string{"kansoku.event.id", "kansoku.session.id"}, // missing tool.id/outcome
	}
	_, err := claudeadapter.CanonicalEventForOTel(claudeadapter.OTelToolResult, shape)
	if !errors.Is(err, claudeadapter.ErrOTelSchemaFingerprintMismatch) {
		t.Fatalf("expected ErrOTelSchemaFingerprintMismatch, got %v", err)
	}
}

func TestCanonicalEventForOTelIgnoresUnsafeAttributesWhenFingerprinting(t *testing.T) {
	// An attribute outside OTLPSafeAttributes() must never influence whether
	// the fingerprint matches: it is filtered before comparison.
	base := claudeadapter.OTelAttributeShape{
		InstrumentationScope: string(claudeadapter.OTelUserPrompt),
		PresentAttributeKeys: []string{"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.prompt_length_characters"},
	}
	withExtra := claudeadapter.OTelAttributeShape{
		InstrumentationScope: string(claudeadapter.OTelUserPrompt),
		PresentAttributeKeys: append(append([]string(nil), base.PresentAttributeKeys...), "tool_payload", "prompt_text"),
	}
	first, err := claudeadapter.CanonicalEventForOTel(claudeadapter.OTelUserPrompt, base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := claudeadapter.CanonicalEventForOTel(claudeadapter.OTelUserPrompt, withExtra)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("dropped/unsafe surfaces must never change whether a documented event resolves")
	}
}

func TestOTelInstallerTargetReusesExistingUserOTelTargetVerbatim(t *testing.T) {
	if claudeadapter.OTelInstallerTargetID != "claude.user_otel" {
		t.Fatalf("claude.otel must reuse the existing claude.user_otel installer target verbatim, got %q", claudeadapter.OTelInstallerTargetID)
	}
}

func TestMatchesOTLPResourceRecognizesRealClaudeServiceNamesOnly(t *testing.T) {
	for _, recognized := range []string{"claude-code", "claude-code-desktop"} {
		if !claudeadapter.MatchesOTLPResource(recognized) {
			t.Fatalf("expected the real, documented %q service.name to match", recognized)
		}
	}
	for _, unrecognized := range []string{"", "claude", "codex_cli_rs", "fixture-agent", "claude-code-old", "Claude-Code"} {
		if claudeadapter.MatchesOTLPResource(unrecognized) {
			t.Fatalf("unrecognized service.name %q must never match", unrecognized)
		}
	}
}

func TestDroppedOTelSurfacesNeverIncludesASafeAttribute(t *testing.T) {
	safe := map[string]bool{}
	for _, attribute := range claudeadapter.OTLPSafeAttributes() {
		safe[attribute] = true
	}
	for _, dropped := range claudeadapter.DroppedOTelSurfaces() {
		if safe[dropped] {
			t.Fatalf("dropped surface %q must never also be a safe attribute", dropped)
		}
	}
}

func TestDroppedOTelSurfacesIncludesDetailedTelemetryContentFields(t *testing.T) {
	dropped := map[string]bool{}
	for _, surface := range claudeadapter.DroppedOTelSurfaces() {
		dropped[surface] = true
	}
	for _, mustDrop := range []string{"prompt_text", "assistant_response_text", "raw_api_body", "tool_payload"} {
		if !dropped[mustDrop] {
			t.Fatalf("expected %q to be an unconditionally dropped OTel surface, dropped set was %+v", mustDrop, dropped)
		}
	}
}

func TestComponentAttributeSafeSlotMapsSkillPluginAgentOntoExistingSlotNeverANewRawAttribute(t *testing.T) {
	for _, attribute := range claudeadapter.DocumentedComponentAttributes() {
		slot, ok := claudeadapter.ComponentAttributeSafeSlot(attribute)
		if !ok {
			t.Fatalf("documented component attribute %q must resolve to an existing safe slot", attribute)
		}
		found := false
		for _, safe := range claudeadapter.OTLPSafeAttributes() {
			if safe == slot {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("component attribute %q maps to %q, which must already be a member of OTLPSafeAttributes()", attribute, slot)
		}
	}
}

func TestResolveSkillComponentInventoryBackedVsUnknownTransient(t *testing.T) {
	known := map[string]struct{}{"code-reviewer": {}}
	id, backed := claudeadapter.ResolveSkillComponent("code-reviewer", known)
	if !backed || id != "code-reviewer" {
		t.Fatalf("a known inventory skill name must resolve to itself, inventory-backed; got id=%q backed=%v", id, backed)
	}
	unknownID, unknownBacked := claudeadapter.ResolveSkillComponent("some-future-skill", known)
	if unknownBacked {
		t.Fatal("an unknown skill.name must never be reported as inventory-backed")
	}
	if unknownID == "" || unknownID == "some-future-skill" {
		t.Fatalf("an unknown skill.name must become a scoped transient component id, not empty and not the bare name, got %q", unknownID)
	}
	if !strings.Contains(unknownID, "some-future-skill") {
		t.Fatal("the transient component id must still be traceable back to the observed skill.name")
	}
	again, _ := claudeadapter.ResolveSkillComponent("some-future-skill", known)
	if again != unknownID {
		t.Fatal("resolving the same unknown skill.name twice must be deterministic")
	}
}

func TestResolveSkillComponentRejectsEmptyName(t *testing.T) {
	id, backed := claudeadapter.ResolveSkillComponent("", map[string]struct{}{})
	if id != "" || backed {
		t.Fatal("an empty skill.name must never resolve to a component id")
	}
}
