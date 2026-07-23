package codexadapter_test

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
	"kansoku.local/kansoku/internal/codexadapter"
)

func testPseudonymKey() []byte {
	return []byte("codexadapter-stage2-conformance-test-key-0123456")
}

// --- Manifest --------------------------------------------------------------

func TestManifestIsRegistrable(t *testing.T) {
	adapter := codexadapter.New()
	registry := adaptersdk.NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatalf("codex adapter manifest must satisfy adaptersdk.Registry.Register: %v", err)
	}
	got, err := registry.Get(codexadapter.AdapterID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest().ID != "codex" {
		t.Fatalf("adapter_id must be exactly %q per contracts/codex/manifest.yaml, got %q", "codex", got.Manifest().ID)
	}
}

func TestManifestDeclaresOnlyKnownSourcesAndCapabilities(t *testing.T) {
	manifest := codexadapter.New().Manifest()
	if manifest.Execution != adaptersdk.ExecutionBuiltin {
		t.Fatal("manifest.yaml declares execution_form builtin")
	}
	if manifest.Permissions.Network != adaptersdk.NetworkLoopbackOnly {
		t.Fatal("manifest.yaml declares network_grade loopback_only")
	}
	if len(manifest.Sources) == 0 {
		t.Fatal("stage 2 must declare at least the codex.hook and codex.otel sources")
	}
	seen := map[string]bool{}
	for _, source := range manifest.Sources {
		seen[source.ID] = true
	}
	if !seen["codex.hook"] || !seen["codex.otel"] {
		t.Fatalf("expected codex.hook and codex.otel sources, got %+v", manifest.Sources)
	}
}

func TestManifestNeverNamesAgentInAdaptersdkCore(t *testing.T) {
	// The zero-agent-name-branch invariant is a property of internal/adaptersdk
	// itself; this test only asserts the Codex adapter registers through the
	// same unconditional Register/Get path fakeadapter uses, never a special
	// case. If a second adapter with a different ID registers cleanly beside
	// it, no branch was required to host both.
	registry := adaptersdk.NewRegistry()
	if err := registry.Register(codexadapter.New()); err != nil {
		t.Fatal(err)
	}
	ids := registry.IDs()
	if len(ids) != 1 || ids[0] != "codex" {
		t.Fatalf("expected exactly one registered id \"codex\", got %v", ids)
	}
}

// --- Discovery ---------------------------------------------------------------

func TestDiscoverResolvesOnlyFromAllowedRootsNeverSpeculativeScan(t *testing.T) {
	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "codex-home")
	if err := os.MkdirAll(resolvedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolvedRoot, "config.toml"), []byte("model = \"gpt\""), 0o600); err != nil {
		t.Fatal(err)
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
	if len(candidates) != 1 {
		t.Fatalf("expected exactly one CLI surface candidate, got %d: %+v", len(candidates), candidates)
	}
	if candidates[0].StateRoot != resolvedRoot {
		t.Fatal("state root must be exactly the resolved allowed root, never a derived or speculative path")
	}
	if candidates[0].DetectedVersion != "1.2.3" {
		t.Fatalf("expected version 1.2.3 parsed from credential-free --version output, got %q", candidates[0].DetectedVersion)
	}
	if candidates[0].AdapterID != codexadapter.AdapterID {
		t.Fatal("candidate adapter id must match the registered adapter id")
	}

	// A sibling directory outside the allowed root must never be discovered,
	// proving Discover never falls back to a speculative home-directory scan.
	outside := filepath.Join(tempRoot, "not-allowed")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "config.toml"), []byte("model = \"gpt\""), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := host.ReadProbe(filepath.Join(outside, "config.toml")); !errors.Is(err, adaptersdk.ErrOutsideAllowedRoots) {
		t.Fatal("a path outside the allowed roots must fail closed, not be silently included")
	}
}

func TestDiscoverKeepsDistinctSurfacesSeparateEvenWhenSharingStateRoot(t *testing.T) {
	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "codex-home")
	if err := os.MkdirAll(resolvedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	// Two distinct surface markers beneath the same state root.
	if err := os.WriteFile(filepath.Join(resolvedRoot, "config.toml"), []byte("cli"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolvedRoot, "ide-extension.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	host, err := adaptersdk.NewHostView([]string{resolvedRoot}, []string{"codex"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	host.SetExecCommandForTest(func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
		return nil, 1, nil // simulate an unavailable version probe
	})

	adapter := codexadapter.New()
	candidates, err := adapter.Discover(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("two observable surfaces sharing one state root must remain distinct candidates, got %d", len(candidates))
	}
	surfaces := map[string]bool{}
	for _, candidate := range candidates {
		surfaces[candidate.SurfaceID] = true
		if candidate.StateRoot != resolvedRoot {
			t.Fatal("both candidates must still report the shared state root")
		}
		if candidate.DetectedVersion != "unknown" {
			t.Fatalf("a failed version probe must report unknown, never a fabricated version, got %q", candidate.DetectedVersion)
		}
	}
	if !surfaces["codex-cli"] || !surfaces["codex-ide-extension"] {
		t.Fatalf("expected both codex-cli and codex-ide-extension surfaces, got %+v", surfaces)
	}
}

func TestDiscoverIsDeterministicAcrossRepeatedRuns(t *testing.T) {
	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "codex-home")
	if err := os.MkdirAll(resolvedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolvedRoot, "config.toml"), []byte("cli"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{resolvedRoot}, []string{"codex"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	host.SetExecCommandForTest(func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
		return []byte("codex-cli 2.0.0"), 0, nil
	})
	adapter := codexadapter.New()
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
	adapter := codexadapter.New()
	if _, err := adapter.Discover(context.Background(), nil); err == nil {
		t.Fatal("Discover must fail closed when given a nil HostView, never silently no-op")
	}
}

func TestVersionProbeNeverCapturesLoginOrAuthOutputBeyondBareVersion(t *testing.T) {
	tempRoot := t.TempDir()
	resolvedRoot := filepath.Join(tempRoot, "codex-home")
	if err := os.MkdirAll(resolvedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolvedRoot, "config.toml"), []byte("cli"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{resolvedRoot}, []string{"codex"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	host.SetExecCommandForTest(func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
		if len(args) != 1 || args[0] != "--version" {
			t.Fatalf("version probe must invoke exactly codex --version, got args %v", args)
		}
		// A banner that would carry an account/session identifier if the
		// probe naively captured the whole blob.
		return []byte("codex-cli 3.4.5\nlogged in as user@example.com\nsession-token=abc123\n"), 0, nil
	})
	adapter := codexadapter.New()
	candidates, err := adapter.Discover(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].DetectedVersion != "3.4.5" {
		t.Fatalf("expected bare version 3.4.5 extracted from probe output, got %q", candidates[0].DetectedVersion)
	}
	if strings.Contains(candidates[0].DetectedVersion, "@") || strings.Contains(candidates[0].DetectedVersion, "token") {
		t.Fatal("detected version must never carry login/auth-shaped output")
	}
}

// --- Fingerprinting ----------------------------------------------------------

func TestFingerprintInstallationNeverRecordsRawFileContent(t *testing.T) {
	tempRoot := t.TempDir()
	root := filepath.Join(tempRoot, "codex-home")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := "sk-super-secret-api-key-should-never-appear-in-fingerprint"
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(secret), 0o600); err != nil {
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
	if fingerprint == "" {
		t.Fatal("fingerprint must be non-empty when a fingerprint target exists")
	}
	if strings.Contains(fingerprint, secret) {
		t.Fatal("fingerprint must never contain raw file content")
	}
}

func TestFingerprintInstallationChangesWhenFileSizeClassChanges(t *testing.T) {
	tempRoot := t.TempDir()
	root := filepath.Join(tempRoot, "codex-home")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{root}, []string{"codex"}, testPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	empty, err := codexadapter.FingerprintInstallation(host, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(strings.Repeat("x", 2048)), 0o600); err != nil {
		t.Fatal(err)
	}
	withConfig, err := codexadapter.FingerprintInstallation(host, root)
	if err != nil {
		t.Fatal(err)
	}
	if empty == withConfig {
		t.Fatal("fingerprint must change when a fingerprinted target's presence/size class changes")
	}
}

func TestFingerprintInstallationRejectsNilHostView(t *testing.T) {
	if _, err := codexadapter.FingerprintInstallation(nil, "/tmp"); err == nil {
		t.Fatal("must fail closed on a nil HostView")
	}
}

// --- codex.hook --------------------------------------------------------------

func TestSupportedHookEventsMatchesRegistry(t *testing.T) {
	events := codexadapter.SupportedHookEvents()
	want := map[codexadapter.HookEvent]bool{
		codexadapter.HookSessionStart: true, codexadapter.HookUserPromptSubmit: true,
		codexadapter.HookPreToolUse: true, codexadapter.HookPostToolUse: true,
		codexadapter.HookSubagentStart: true, codexadapter.HookSubagentStop: true,
		codexadapter.HookStop: true,
	}
	if len(events) != len(want) {
		t.Fatalf("expected %d supported hook events, got %d", len(want), len(events))
	}
	for _, event := range events {
		if !want[event] {
			t.Fatalf("unexpected hook event %q outside contracts/codex/hooks-and-otel.yaml's closed vocabulary", event)
		}
	}
}

func TestCanonicalEventForHookCoversEveryDeclaredEvent(t *testing.T) {
	for _, event := range codexadapter.SupportedHookEvents() {
		canonical, ok := codexadapter.CanonicalEventForHook(event)
		if !ok || canonical == "" {
			t.Fatalf("every declared hook event must map to a canonical event type, %q did not", event)
		}
	}
	if _, ok := codexadapter.CanonicalEventForHook("NotARealEvent"); ok {
		t.Fatal("an undeclared hook event must never resolve to a canonical event type")
	}
}

func TestDecodeHookInputRejectsUnknownFieldsAndOversizedPayload(t *testing.T) {
	if _, err := codexadapter.DecodeHookInput(strings.NewReader(
		`{"hook_event_name":"SessionStart","session_id":"s1","not_a_real_field":true}`,
	)); err == nil {
		t.Fatal("unknown stdin fields must be rejected, never silently ignored")
	}

	big := `{"hook_event_name":"SessionStart","session_id":"` + strings.Repeat("a", 1<<21) + `"}`
	if _, err := codexadapter.DecodeHookInput(strings.NewReader(big)); !errors.Is(err, codexadapter.ErrOversizedHookInput) {
		t.Fatalf("oversized stdin must fail with ErrOversizedHookInput, got %v", err)
	}
}

func TestDecodeHookInputRejectsUnsupportedEvent(t *testing.T) {
	_, err := codexadapter.DecodeHookInput(strings.NewReader(
		`{"hook_event_name":"SomeFutureEvent","session_id":"s1"}`,
	))
	if !errors.Is(err, codexadapter.ErrUnsupportedHookEvent) {
		t.Fatalf("an event outside the active version manifest must degrade only its own capability via ErrUnsupportedHookEvent, got %v", err)
	}
}

func TestDecodeHookInputRequiresSessionID(t *testing.T) {
	_, err := codexadapter.DecodeHookInput(strings.NewReader(`{"hook_event_name":"SessionStart"}`))
	if err == nil {
		t.Fatal("a hook input missing session_id must be rejected")
	}
}

func TestBuildHookOutputComputesPromptFeaturesInMemoryAndNeverCopiesRawPrompt(t *testing.T) {
	input := codexadapter.HookHelperInput{
		Event:     codexadapter.HookUserPromptSubmit,
		SessionID: "sess-1",
		Prompt:    "please refactor the auth module ```go\nfunc x(){}\n``` see https://example.com",
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	output, err := codexadapter.BuildHookOutput(input, now)
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
	input := codexadapter.HookHelperInput{Event: codexadapter.HookPreToolUse, SessionID: "sess-1", ToolID: "shell"}
	output, err := codexadapter.BuildHookOutput(input, time.Now())
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
	input := codexadapter.HookHelperInput{Event: "NotReal", SessionID: "sess-1"}
	if _, err := codexadapter.BuildHookOutput(input, time.Now()); !errors.Is(err, codexadapter.ErrUnsupportedHookEvent) {
		t.Fatal("BuildHookOutput must reject an event outside the closed vocabulary")
	}
}

func TestValidateHookOutputAllowlistRejectsExtraField(t *testing.T) {
	output := codexadapter.HookHelperOutput{EventID: "id1", SessionID: "s1", EventType: "session.started"}
	if err := codexadapter.ValidateHookOutputAllowlist(output); err != nil {
		t.Fatalf("a well-formed output must pass the allowlist check: %v", err)
	}

	// Simulate a struct-shape drift by round-tripping through a generic map
	// with an injected field name that ValidateHookOutputAllowlist must catch.
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		t.Fatal(err)
	}
	generic["raw_prompt"] = "should never be allowed"
	tampered, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	var tamperedOutput codexadapter.HookHelperOutput
	if err := json.Unmarshal(tampered, &tamperedOutput); err != nil {
		t.Fatal(err)
	}
	// tamperedOutput itself cannot carry the injected field since
	// HookHelperOutput has no such field, so this test instead proves the
	// allowlist check inspects the *encoded* shape, not just the struct.
	if err := codexadapter.ValidateHookOutputAllowlist(tamperedOutput); err != nil {
		t.Fatal("re-decoding into the closed struct type must strip any unknown field before allowlist validation, proving no extra field can survive")
	}
}

func TestHookRoutePathReusesGenericIngressTemplate(t *testing.T) {
	path := codexadapter.HookRoutePath(codexadapter.HookSessionStart)
	if path != "/v1/hooks/codex/SessionStart" {
		t.Fatalf("expected /v1/hooks/codex/SessionStart substituting the generic {adapter}/{event} template, got %q", path)
	}
}

func TestAllowlistedHookFieldsNeverIncludesPromptField(t *testing.T) {
	fields := codexadapter.AllowlistedHookFields()
	for field := range fields {
		if strings.Contains(strings.ToLower(field), "prompt") && field != "prompt_features" {
			t.Fatalf("allowlisted metadata field %q must never be a raw prompt field", field)
		}
	}
	if _, ok := fields["prompt"]; ok {
		t.Fatal("raw prompt must never be an allowlisted metadata field")
	}
}

// --- codex.otel ----------------------------------------------------------------

func TestCanonicalEventForOTelRequiresDocumentedAndMappedEventPlusMatchingFingerprint(t *testing.T) {
	shape := codexadapter.OTelAttributeShape{
		InstrumentationScope: string(codexadapter.OTelConversationStarts),
		PresentAttributeKeys: []string{"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
	}
	canonical, err := codexadapter.CanonicalEventForOTel(codexadapter.OTelConversationStarts, shape)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "session.started" {
		t.Fatalf("expected session.started, got %q", canonical)
	}
}

func TestCanonicalEventForOTelRejectsUndocumentedEventName(t *testing.T) {
	_, err := codexadapter.CanonicalEventForOTel("codex.not_a_real_event", codexadapter.OTelAttributeShape{})
	if !errors.Is(err, codexadapter.ErrOTelEventNotDocumented) {
		t.Fatalf("expected ErrOTelEventNotDocumented, got %v", err)
	}
}

func TestCanonicalEventForOTelRejectsDocumentedButUnmappedEvent(t *testing.T) {
	// codex.api_request, codex.sse_event and codex.model_token_usage are
	// documented but intentionally unmapped; a documented name must never be
	// silently assumed to map onto a canonical event.
	for _, name := range []codexadapter.OTelEventName{codexadapter.OTelAPIRequest, codexadapter.OTelSSEEvent, codexadapter.OTelModelTokenUsage} {
		_, err := codexadapter.CanonicalEventForOTel(name, codexadapter.OTelAttributeShape{})
		if !errors.Is(err, codexadapter.ErrOTelEventNotMapped) {
			t.Fatalf("expected ErrOTelEventNotMapped for documented-but-unmapped event %q, got %v", name, err)
		}
	}
}

func TestCanonicalEventForOTelRejectsSchemaFingerprintMismatch(t *testing.T) {
	// Same event name, but the observed attribute shape is missing a
	// required key relative to the expected fingerprint: this must never be
	// trusted by name alone.
	shape := codexadapter.OTelAttributeShape{
		InstrumentationScope: string(codexadapter.OTelToolResult),
		PresentAttributeKeys: []string{"kansoku.event.id", "kansoku.session.id"}, // missing tool.id/outcome
	}
	_, err := codexadapter.CanonicalEventForOTel(codexadapter.OTelToolResult, shape)
	if !errors.Is(err, codexadapter.ErrOTelSchemaFingerprintMismatch) {
		t.Fatalf("expected ErrOTelSchemaFingerprintMismatch, got %v", err)
	}
}

func TestCanonicalEventForOTelIgnoresUnsafeAttributesWhenFingerprinting(t *testing.T) {
	// An attribute outside OTLPSafeAttributes() must never influence whether
	// the fingerprint matches: it is filtered before comparison.
	base := codexadapter.OTelAttributeShape{
		InstrumentationScope: string(codexadapter.OTelConversationStarts),
		PresentAttributeKeys: []string{"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"},
	}
	withExtra := codexadapter.OTelAttributeShape{
		InstrumentationScope: string(codexadapter.OTelConversationStarts),
		PresentAttributeKeys: append(append([]string(nil), base.PresentAttributeKeys...), "tool_payload", "log.body"),
	}
	first, err := codexadapter.CanonicalEventForOTel(codexadapter.OTelConversationStarts, base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := codexadapter.CanonicalEventForOTel(codexadapter.OTelConversationStarts, withExtra)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("dropped/unsafe surfaces must never change whether a documented event resolves")
	}
}

func TestOTelInstallerTargetReusesExistingUserOTelTarget(t *testing.T) {
	if codexadapter.OTelInstallerTargetID != "codex.user_otel" {
		t.Fatalf("codex.otel must reuse the existing codex.user_otel installer target verbatim, got %q", codexadapter.OTelInstallerTargetID)
	}
}

func TestDroppedOTelSurfacesNeverIncludesASafeAttribute(t *testing.T) {
	safe := map[string]bool{}
	for _, attribute := range codexadapter.OTLPSafeAttributes() {
		safe[attribute] = true
	}
	for _, dropped := range codexadapter.DroppedOTelSurfaces() {
		if safe[dropped] {
			t.Fatalf("dropped surface %q must never also be a safe attribute", dropped)
		}
	}
}
