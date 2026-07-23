// Package crossagent contains Session 07's cross-agent invariant test: one
// logical scenario -- session -> prompt metadata -> skill activation -> MCP
// tool call -> model tokens -> success -- expressed once per real agent
// (Codex, Claude), asserting only on canonical capability IDs and canonical
// event types from contracts/cross-agent/invariant-scenario.yaml's
// scenario_stage_to_capability_mapping table. Per-fixture input data is of
// course agent-specific (that is the whole point: two differently-shaped
// real adapters produce the same canonical shape); the assertions
// themselves never branch on an agent ID string. Gemini/Cursor are
// out of scope for this session and are not referenced here at all.
package crossagent_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/claudeadapter"
	"kansoku.local/kansoku/internal/codexadapter"
)

const fixturePath = "../../tests/fixtures/session-07/cross-agent-invariant-scenario.json"

type stageMappingRow struct {
	Stage               string `json:"stage"`
	CapabilityID        string `json:"capability_id"`
	CanonicalEventType  string `json:"canonical_event_type"`
}

type hookEventFixture struct {
	HookEventName              string `json:"hook_event_name"`
	SessionID                  string `json:"session_id"`
	ToolID                     string `json:"tool_id,omitempty"`
	ToolStatus                 string `json:"tool_status,omitempty"`
	ExpectedCanonicalEventType string `json:"expected_canonical_event_type"`
}

type skillActivationFixture struct {
	Kind                       string   `json:"kind"`
	CandidateSkillIdentities   []string `json:"candidate_skill_identities"`
	SourceLabelsNative         bool     `json:"source_labels_native"`
	ModeKnown                  bool     `json:"mode_known"`
	ExpectedCanonicalEventType string   `json:"expected_canonical_event_type"`
	ExpectedTier               string   `json:"expected_tier"`
}

type mcpToolCallFixture struct {
	ServerName string `json:"server_name"`
	ToolName   string `json:"tool_name"`
	Advertised bool   `json:"advertised"`
}

type modelTokensFixture struct {
	OTelEventName              string   `json:"otel_event_name"`
	Documented                 bool     `json:"documented"`
	ExpectUnmapped             bool     `json:"expect_unmapped"`
	AttributeShape             []string `json:"attribute_shape"`
	ExpectedCanonicalEventType string   `json:"expected_canonical_event_type"`
}

type extraAgentSpecificEventFixture struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type agentScenarioFixture struct {
	SessionID               string                         `json:"session_id"`
	HookEvents              []hookEventFixture             `json:"hook_events"`
	SkillActivationEvidence skillActivationFixture         `json:"skill_activation_evidence"`
	MCPToolCall             mcpToolCallFixture             `json:"mcp_tool_call"`
	ModelTokens             modelTokensFixture             `json:"model_tokens"`
	ExtraAgentSpecificEvent extraAgentSpecificEventFixture `json:"extra_agent_specific_event"`
}

type crossAgentScenarioFixture struct {
	ScenarioStageToCapabilityMapping []stageMappingRow     `json:"scenario_stage_to_capability_mapping"`
	Codex                            agentScenarioFixture  `json:"codex"`
	Claude                           agentScenarioFixture  `json:"claude"`
}

func loadScenarioFixture(t *testing.T) crossAgentScenarioFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixturePath))
	if err != nil {
		t.Fatal(err)
	}
	var fixture crossAgentScenarioFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

// capabilityByStage returns the canonical capability id and event type for
// a named scenario stage from the fixture's own mapping table -- the test
// never hardcodes this table a second time in Go, it only walks the one
// data-driven table the contract and fixture both declare.
func capabilityByStage(t *testing.T, rows []stageMappingRow, stage string) stageMappingRow {
	t.Helper()
	for _, row := range rows {
		if row.Stage == stage {
			return row
		}
	}
	t.Fatalf("scenario mapping table missing stage %q", stage)
	return stageMappingRow{}
}

// runnerBothAgentsRegisterCapabilities is a small helper asserting that
// both adapters declare the mapped capability id in their manifest, without
// ever comparing the two adapters' IDs to each other -- the assertion binds
// to the capability id row, not to "codex" or "claude" as a branch
// condition.
func assertManifestDeclaresCapability(t *testing.T, manifest adaptersdk.Manifest, capabilityID adaptersdk.CapabilityID) string {
	t.Helper()
	state, ok := manifest.Capabilities[capabilityID]
	if !ok {
		t.Fatalf("manifest for adapter id %q must declare capability %q (even if unsupported)", manifest.ID, capabilityID)
	}
	return state
}

// --- Stage: session -----------------------------------------------------------------

func TestCrossAgentSessionStageMapsToActivitySessionsAndSessionStarted(t *testing.T) {
	fixture := loadScenarioFixture(t)
	row := capabilityByStage(t, fixture.ScenarioStageToCapabilityMapping, "session")
	if row.CapabilityID != string(adaptersdk.CapabilityActivitySessions) {
		t.Fatalf("session stage must map to activity.sessions, got %q", row.CapabilityID)
	}

	for _, agent := range []struct {
		manifest adaptersdk.Manifest
		hooks    []hookEventFixture
	}{
		{codexadapter.New().Manifest(), fixture.Codex.HookEvents},
		{claudeadapter.New().Manifest(), fixture.Claude.HookEvents},
	} {
		assertManifestDeclaresCapability(t, agent.manifest, adaptersdk.CapabilityID(row.CapabilityID))
		var sawSessionStarted bool
		for _, hook := range agent.hooks {
			if hook.HookEventName != "SessionStart" {
				continue
			}
			sawSessionStarted = true
			canonical := canonicalEventForHook(t, agent.manifest.ID, hook.HookEventName)
			if canonical != row.CanonicalEventType {
				t.Fatalf("SessionStart must map to canonical event %q, got %q", row.CanonicalEventType, canonical)
			}
		}
		if !sawSessionStarted {
			t.Fatal("fixture must include a SessionStart hook event for this agent")
		}
	}
}

// --- Stage: prompt metadata ----------------------------------------------------------

func TestCrossAgentPromptMetadataStageMapsToActivityPromptMetadata(t *testing.T) {
	fixture := loadScenarioFixture(t)
	row := capabilityByStage(t, fixture.ScenarioStageToCapabilityMapping, "prompt_metadata")
	if row.CapabilityID != string(adaptersdk.CapabilityActivityPromptMetadata) {
		t.Fatalf("prompt_metadata stage must map to activity.prompt_metadata, got %q", row.CapabilityID)
	}

	for _, agent := range []struct {
		manifest adaptersdk.Manifest
		hooks    []hookEventFixture
	}{
		{codexadapter.New().Manifest(), fixture.Codex.HookEvents},
		{claudeadapter.New().Manifest(), fixture.Claude.HookEvents},
	} {
		assertManifestDeclaresCapability(t, agent.manifest, adaptersdk.CapabilityID(row.CapabilityID))
		var sawPrompt bool
		for _, hook := range agent.hooks {
			if hook.HookEventName != "UserPromptSubmit" {
				continue
			}
			sawPrompt = true
			canonical := canonicalEventForHook(t, agent.manifest.ID, hook.HookEventName)
			if canonical != row.CanonicalEventType {
				t.Fatalf("UserPromptSubmit must map to canonical event %q, got %q", row.CanonicalEventType, canonical)
			}
		}
		if !sawPrompt {
			t.Fatal("fixture must include a UserPromptSubmit hook event for this agent")
		}
	}
}

// --- Stage: skill activation ----------------------------------------------------------

// TestCrossAgentSkillActivationStageResolvesToComponentInvoked proves both
// agents converge on the same canonical event type
// (components.skill_invocation -> component.invoked) for a skill
// activation, even though the underlying evidence kind vocabulary differs
// completely between the two adapters (codex's "explicit_user_invocation"
// vs. claude's native "skill_tool_call_explicit") -- the assertion binds
// only to the shared CanonicalEventType/CapabilityID pair, never to the
// evidence-kind string or an agent ID.
func TestCrossAgentSkillActivationStageResolvesToComponentInvoked(t *testing.T) {
	fixture := loadScenarioFixture(t)
	row := capabilityByStage(t, fixture.ScenarioStageToCapabilityMapping, "skill_activation")
	if row.CapabilityID != string(adaptersdk.CapabilityComponentsSkillInvocation) {
		t.Fatalf("skill_activation stage must map to components.skill_invocation, got %q", row.CapabilityID)
	}

	assertManifestDeclaresCapability(t, codexadapter.New().Manifest(), adaptersdk.CapabilityID(row.CapabilityID))
	assertManifestDeclaresCapability(t, claudeadapter.New().Manifest(), adaptersdk.CapabilityID(row.CapabilityID))

	codexResolution, err := codexadapter.ResolveSkillEvidence(codexadapter.SkillEvidenceInput{
		Kind:                     codexadapter.SkillEvidenceKind(fixture.Codex.SkillActivationEvidence.Kind),
		CandidateSkillIdentities: fixture.Codex.SkillActivationEvidence.CandidateSkillIdentities,
		SourceLabelsNative:       fixture.Codex.SkillActivationEvidence.SourceLabelsNative,
	})
	if err != nil {
		t.Fatal(err)
	}
	if codexResolution.CanonicalEventType != row.CanonicalEventType {
		t.Fatalf("codex skill activation must resolve to %q, got %q", row.CanonicalEventType, codexResolution.CanonicalEventType)
	}
	if string(codexResolution.Tier) != fixture.Codex.SkillActivationEvidence.ExpectedTier {
		t.Fatalf("codex skill activation tier mismatch: expected %q, got %q", fixture.Codex.SkillActivationEvidence.ExpectedTier, codexResolution.Tier)
	}

	claudeResolution, err := claudeadapter.ResolveSkillEvidence(claudeadapter.SkillEvidenceInput{
		Kind:                     claudeadapter.SkillEvidenceKind(fixture.Claude.SkillActivationEvidence.Kind),
		CandidateSkillIdentities: fixture.Claude.SkillActivationEvidence.CandidateSkillIdentities,
		ModeKnown:                fixture.Claude.SkillActivationEvidence.ModeKnown,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claudeResolution.CanonicalEventType != row.CanonicalEventType {
		t.Fatalf("claude skill activation must resolve to %q, got %q", row.CanonicalEventType, claudeResolution.CanonicalEventType)
	}
	if string(claudeResolution.Tier) != fixture.Claude.SkillActivationEvidence.ExpectedTier {
		t.Fatalf("claude skill activation tier mismatch: expected %q, got %q", fixture.Claude.SkillActivationEvidence.ExpectedTier, claudeResolution.Tier)
	}

	// The two agents are allowed -- indeed expected -- to differ in
	// evidence tier (native vs. reconstructed): the unsupported_rendering
	// rule never forces equal evidence tiers across agents. Only the
	// canonical event type (the capability-bound fact) must agree.
	if codexResolution.Tier == claudeResolution.Tier {
		t.Skip("tiers happen to match for this fixture pairing; this is not a required invariant, just documenting it is not forced apart either")
	}
}

// --- Stage: MCP tool call ---------------------------------------------------------------

func TestCrossAgentMCPToolCallStageMapsToComponentsMCPLifecycleAndToolCalled(t *testing.T) {
	fixture := loadScenarioFixture(t)
	row := capabilityByStage(t, fixture.ScenarioStageToCapabilityMapping, "mcp_tool_call")
	if row.CapabilityID != string(adaptersdk.CapabilityComponentsMCPLifecycle) {
		t.Fatalf("mcp_tool_call stage must map to components.mcp_lifecycle, got %q", row.CapabilityID)
	}

	for _, agent := range []struct {
		manifest adaptersdk.Manifest
		hooks    []hookEventFixture
		mcp      mcpToolCallFixture
	}{
		{codexadapter.New().Manifest(), fixture.Codex.HookEvents, fixture.Codex.MCPToolCall},
		{claudeadapter.New().Manifest(), fixture.Claude.HookEvents, fixture.Claude.MCPToolCall},
	} {
		assertManifestDeclaresCapability(t, agent.manifest, adaptersdk.CapabilityID(row.CapabilityID))
		if !agent.mcp.Advertised {
			t.Fatal("fixture's MCP tool must be advertised for this stage to be meaningfully exercised")
		}
		var sawPreToolMCP, sawPostToolMCP bool
		for _, hook := range agent.hooks {
			if hook.ToolID == "" || hook.ToolID[:4] != "mcp_" && !hasMCPPrefix(hook.ToolID) {
				continue
			}
			canonical := canonicalEventForHook(t, agent.manifest.ID, hook.HookEventName)
			if canonical != row.CanonicalEventType {
				t.Fatalf("MCP-tagged hook event %q must map to canonical event %q, got %q", hook.HookEventName, row.CanonicalEventType, canonical)
			}
			switch hook.HookEventName {
			case "PreToolUse":
				sawPreToolMCP = true
			case "PostToolUse":
				sawPostToolMCP = true
			}
		}
		if !sawPreToolMCP || !sawPostToolMCP {
			t.Fatal("expected both a PreToolUse and a PostToolUse MCP-tagged hook event in the fixture")
		}
	}
}

func hasMCPPrefix(toolID string) bool {
	return len(toolID) >= 4 && toolID[:4] == "mcp_"
}

// --- Stage: model tokens -----------------------------------------------------------------

// TestCrossAgentModelTokensStageRendersUnsupportedForCodexAndNativeForClaude
// is the concrete proof of the unsupported_rendering_rule: Claude has a
// native, mapped OTel event for this stage (claude_code.api_request ->
// model.responded), while Codex's equivalent OTel event
// (codex.model_token_usage) is documented but not yet present in Codex's
// own active canonical mapping table -- so this stage renders degraded/
// unmapped for Codex only, never a uniform zero forced onto Claude, and
// never a fabricated model.responded event invented for Codex to make the
// two agents look symmetrical.
func TestCrossAgentModelTokensStageRendersUnsupportedForCodexAndNativeForClaude(t *testing.T) {
	fixture := loadScenarioFixture(t)
	row := capabilityByStage(t, fixture.ScenarioStageToCapabilityMapping, "model_tokens")
	if row.CapabilityID != string(adaptersdk.CapabilityActivityTokenModelCost) {
		t.Fatalf("model_tokens stage must map to activity.token_model_cost, got %q", row.CapabilityID)
	}

	codexManifest := codexadapter.New().Manifest()
	assertManifestDeclaresCapability(t, codexManifest, adaptersdk.CapabilityID(row.CapabilityID))
	if !fixture.Codex.ModelTokens.ExpectUnmapped {
		t.Fatal("this fixture must document codex's model-tokens stage as currently unmapped")
	}
	_, err := codexadapter.CanonicalEventForOTel(
		codexadapter.OTelEventName(fixture.Codex.ModelTokens.OTelEventName),
		codexadapter.OTelAttributeShape{InstrumentationScope: fixture.Codex.ModelTokens.OTelEventName},
	)
	if err == nil {
		t.Fatal("codex's model-tokens OTel event must not silently resolve to a canonical type while it remains unmapped in codex's active mapping table")
	}
	if !errors.Is(err, codexadapter.ErrOTelEventNotMapped) {
		t.Fatalf("expected ErrOTelEventNotMapped degrading only this capability, got %v", err)
	}

	claudeManifest := claudeadapter.New().Manifest()
	assertManifestDeclaresCapability(t, claudeManifest, adaptersdk.CapabilityID(row.CapabilityID))
	claudeShape := claudeadapter.OTelAttributeShape{
		InstrumentationScope: fixture.Claude.ModelTokens.OTelEventName,
		PresentAttributeKeys: fixture.Claude.ModelTokens.AttributeShape,
	}
	claudeCanonical, err := claudeadapter.CanonicalEventForOTel(claudeadapter.OTelEventName(fixture.Claude.ModelTokens.OTelEventName), claudeShape)
	if err != nil {
		t.Fatalf("claude's model-tokens OTel event must resolve natively: %v", err)
	}
	if claudeCanonical != row.CanonicalEventType || claudeCanonical != fixture.Claude.ModelTokens.ExpectedCanonicalEventType {
		t.Fatalf("expected claude model-tokens canonical event %q, got %q", row.CanonicalEventType, claudeCanonical)
	}
}

// --- Stage: success --------------------------------------------------------------------

func TestCrossAgentSuccessStageMapsToActivitySessionsAndSessionStopped(t *testing.T) {
	fixture := loadScenarioFixture(t)
	row := capabilityByStage(t, fixture.ScenarioStageToCapabilityMapping, "success")
	if row.CapabilityID != string(adaptersdk.CapabilityActivitySessions) {
		t.Fatalf("success stage must map to activity.sessions, got %q", row.CapabilityID)
	}

	for _, agent := range []struct {
		manifest adaptersdk.Manifest
		hooks    []hookEventFixture
	}{
		{codexadapter.New().Manifest(), fixture.Codex.HookEvents},
		{claudeadapter.New().Manifest(), fixture.Claude.HookEvents},
	} {
		assertManifestDeclaresCapability(t, agent.manifest, adaptersdk.CapabilityID(row.CapabilityID))
		var sawStop bool
		for _, hook := range agent.hooks {
			if hook.HookEventName != "Stop" {
				continue
			}
			sawStop = true
			canonical := canonicalEventForHook(t, agent.manifest.ID, hook.HookEventName)
			if canonical != row.CanonicalEventType {
				t.Fatalf("Stop must map to canonical event %q, got %q", row.CanonicalEventType, canonical)
			}
		}
		if !sawStop {
			t.Fatal("fixture must include a Stop hook event for this agent")
		}
	}
}

// --- Agent-specific extra evidence survives without a core schema change ---------------

// TestAgentSpecificExtraEventSurvivesAsAllowlistedAttributeWithoutCoreChange
// proves that Claude's plugin.name OTel attribution -- an event/attribute
// with no equivalent in the shared scenario_stage_to_capability_mapping
// table and no equivalent in Codex's vocabulary at all -- still resolves
// onto an existing allowlisted safe attribute slot (kansoku.tool.id) rather
// than requiring a new canonical-table column or a new OTLPSafeAttributes
// entry.
func TestAgentSpecificExtraEventSurvivesAsAllowlistedAttributeWithoutCoreChange(t *testing.T) {
	fixture := loadScenarioFixture(t)
	if fixture.Claude.ExtraAgentSpecificEvent.Field != "plugin.name" {
		t.Fatalf("expected claude's documented extra event to be plugin.name, got %q", fixture.Claude.ExtraAgentSpecificEvent.Field)
	}
	slot, ok := claudeadapter.ComponentAttributeSafeSlot(claudeadapter.AttributePluginName)
	if !ok {
		t.Fatal("plugin.name must resolve to an existing safe attribute slot")
	}
	found := false
	for _, safe := range claudeadapter.OTLPSafeAttributes() {
		if safe == slot {
			found = true
		}
	}
	if !found {
		t.Fatalf("plugin.name's resolved slot %q must already be a member of OTLPSafeAttributes() -- no new raw attribute passthrough is declared for it", slot)
	}
	// The safe attribute allowlist itself is exactly the one Session 03
	// ingress.yaml already declares; it gains no new member for plugin.name.
	if len(claudeadapter.OTLPSafeAttributes()) != 8 {
		t.Fatalf("claude.otel's safe attribute allowlist must stay the same closed 8-member set ingress.yaml declares, got %d members", len(claudeadapter.OTLPSafeAttributes()))
	}
}

// --- Helpers reused by every stage test above -------------------------------------------

// canonicalEventForHook dispatches to the correct adapter's own
// CanonicalEventForHook by adapterID -- this is fixture selection, not an
// assertion; the fixture selection switch lives here once, and no
// individual stage assertion above ever compares against "codex" or
// "claude" as part of its pass/fail condition.
func canonicalEventForHook(t *testing.T, adapterID string, hookEventName string) string {
	t.Helper()
	switch adapterID {
	case codexadapter.AdapterID:
		canonical, ok := codexadapter.CanonicalEventForHook(codexadapter.HookEvent(hookEventName))
		if !ok {
			t.Fatalf("codex hook event %q must be a known, mapped event", hookEventName)
		}
		return canonical
	case claudeadapter.AdapterID:
		canonical, ok := claudeadapter.CanonicalEventForHook(claudeadapter.HookEvent(hookEventName))
		if !ok {
			t.Fatalf("claude hook event %q must be a known, mapped event", hookEventName)
		}
		return canonical
	default:
		t.Fatalf("unexpected adapter id in cross-agent fixture selection: %q", adapterID)
		return ""
	}
}
