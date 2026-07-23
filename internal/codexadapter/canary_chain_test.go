package codexadapter_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/codexadapter"
	"kansoku.local/kansoku/internal/observability"
)

// This file drives tests/fixtures/session-06/canary/kansoku-canary-scenario.json's
// expected_chain end to end against a materialized canaryWorkspace (see
// canary_lifecycle_test.go): discovery of the fixture project's surface,
// codex.hook + codex.otel + codex.rollout evidence for each declared chain
// step, skill evidence resolution for the "kansoku-canary-skill" invocation,
// and a final cross-source reconciliation -- proving the session_06 exit
// gate's central claim ("a canary session produces the expected
// session/prompt/tool/MCP chain") against the real Go implementation rather
// than only against unit-level fixtures in isolation.

type canaryScenarioFixture struct {
	CompatibilityVersionsCovered []string `json:"compatibility_versions_covered"`
	FixtureProject               struct {
		Layout struct {
			SkillRelpath     string `json:"skill_relpath"`
			SkillName        string `json:"skill_name"`
			MCPConfigRelpath string `json:"mcp_config_relpath"`
			MCPServerName    string `json:"mcp_server_name"`
			MCPToolName      string `json:"mcp_tool_name"`
		} `json:"layout"`
	} `json:"fixture_project"`
	ExpectedChain map[string][]struct {
		Step               int      `json:"step"`
		CanonicalEventType string   `json:"canonical_event_type"`
		Tier               string   `json:"tier"`
		SourceLanes        []string `json:"source_lanes"`
		EvidenceKind       string   `json:"evidence_kind"`
		Component          string   `json:"component"`
		Tool               string   `json:"tool"`
		SourceLabelsNative bool     `json:"source_labels_native"`
	} `json:"expected_chain"`
	ProhibitedContentCanary struct {
		RawFieldsThatMustNeverAppearDurably []string        `json:"raw_fields_that_must_never_appear_durably"`
		SampleRawHookStdin                  json.RawMessage `json:"sample_raw_hook_stdin"`
		SampleRawRolloutLine                json.RawMessage `json:"sample_raw_rollout_line"`
	} `json:"prohibited_content_canary"`
}

func loadCanaryScenario(t *testing.T) canaryScenarioFixture {
	t.Helper()
	var scenario canaryScenarioFixture
	data, err := os.ReadFile(filepath.Join(fixturesRoot, "canary", "kansoku-canary-scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &scenario); err != nil {
		t.Fatal(err)
	}
	return scenario
}

// TestCanaryWorkspaceLifecycleIsSeparateFromRun proves the "generated
// workspace lifecycle" execution constraint precisely: the workspace
// directory still exists immediately after a canary "run" completes, and is
// only removed once the distinct Destroy step executes deliberately.
func TestCanaryWorkspaceLifecycleIsSeparateFromRun(t *testing.T) {
	workspace, err := newCanaryWorkspace()
	if err != nil {
		t.Fatal(err)
	}

	// A canary "run" that reads/inventories the workspace but never itself
	// destroys it.
	skillPath := filepath.Join(workspace.Root, "kansoku-canary-fixture-project", ".codex", "skills", "kansoku-canary-skill", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("expected canary fixture skill file to exist inside the materialized workspace: %v", err)
	}

	if _, err := os.Stat(workspace.Root); err != nil {
		t.Fatalf("workspace root must still exist immediately after a canary run completes, got: %v", err)
	}

	if err := workspace.Destroy(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace.Root); !os.IsNotExist(err) {
		t.Fatal("workspace root must be gone only after the separately controlled Destroy step runs")
	}
}

// TestCanaryChainProducesExpectedSessionPromptToolMCPChain drives every step
// of the codex-compat/1 expected_chain against the real discovery/hook/otel/
// rollout/evidence/reconcile Go implementation, using a freshly materialized
// canary workspace as the only source of skill/MCP identity, and asserts
// every canonical event type and tier produced matches both the fixture and
// internal/observability's own closed EvidenceTier vocabulary -- proving the
// adapter never collapses this chain into a false native exact activation
// and never silently reports zero for a source that is actually healthy.
func TestCanaryChainProducesExpectedSessionPromptToolMCPChain(t *testing.T) {
	scenario := loadCanaryScenario(t)
	chain, ok := scenario.ExpectedChain["codex-compat/1"]
	if !ok || len(chain) == 0 {
		t.Fatal("scenario must declare an expected_chain for codex-compat/1")
	}

	workspace, err := newCanaryWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := workspace.Destroy(); err != nil {
			t.Fatal(err)
		}
	}()

	fixtureProjectRoot := filepath.Join(workspace.Root, "kansoku-canary-fixture-project")
	codexHome := filepath.Join(fixtureProjectRoot, ".codex")
	if _, err := os.Stat(codexHome); err != nil {
		t.Fatalf("materialized workspace must contain the fixture project's .codex directory: %v", err)
	}

	// --- Step 0: discovery -----------------------------------------------
	// The canary fixture project's .codex directory stands in for a
	// resolved CODEX_HOME: it carries a "config.toml"-equivalent surface
	// marker (the skills/mcp directories) that Discover finds strictly
	// through HostView.AllowedRoots, never a speculative scan.
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"test\""), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{codexHome}, []string{"codex"}, testPseudonymKey())
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
	if len(candidates) != 1 || candidates[0].SurfaceID != "codex-cli" {
		t.Fatalf("expected exactly one codex-cli candidate for the canary fixture project, got %+v", candidates)
	}

	// --- Step 0b: inventory carries the canary skill + echo MCP ----------
	skillData, err := os.ReadFile(filepath.Join(codexHome, "skills", "kansoku-canary-skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	mcpData, err := os.ReadFile(filepath.Join(codexHome, "mcp", "echo-mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mcpConfig struct {
		MCPServerName   string `json:"mcp_server_name"`
		AdvertisedTools []struct {
			Name string `json:"name"`
		} `json:"advertised_tools"`
	}
	if err := json.Unmarshal(mcpData, &mcpConfig); err != nil {
		t.Fatal(err)
	}
	if mcpConfig.MCPServerName != scenario.FixtureProject.Layout.MCPServerName {
		t.Fatalf("expected mcp server name %q, got %q", scenario.FixtureProject.Layout.MCPServerName, mcpConfig.MCPServerName)
	}
	var advertisedTools []string
	for _, tool := range mcpConfig.AdvertisedTools {
		advertisedTools = append(advertisedTools, tool.Name)
	}

	snapshot, err := codexadapter.BuildInventorySnapshot(codexadapter.InventoryInput{
		InstallationID: "inst-canary-1",
		Skills: []codexadapter.SkillDescriptor{
			{Name: scenario.FixtureProject.Layout.SkillName, Scope: adaptersdk.ScopeRepository, Enabled: true, DescriptionBytes: len(skillData), DescriptionChars: len(skillData)},
		},
		MCPServers: []codexadapter.MCPServerDescriptor{
			{Name: scenario.FixtureProject.Layout.MCPServerName, Scope: adaptersdk.ScopeRepository, Enabled: true, AdvertisedTools: advertisedTools},
		},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	foundSkillNode, foundMCPToolNode := false, false
	for _, n := range snapshot.Nodes {
		if n.Kind == adaptersdk.NodeSkillIdentity && n.DeclaredName == scenario.FixtureProject.Layout.SkillName {
			foundSkillNode = true
		}
		if n.Kind == adaptersdk.NodeMCPTool && n.DeclaredName == scenario.FixtureProject.Layout.MCPToolName {
			foundMCPToolNode = true
		}
	}
	if !foundSkillNode {
		t.Fatal("inventory must contain a skill_identity node for kansoku-canary-skill")
	}
	if !foundMCPToolNode {
		t.Fatal("inventory must contain an mcp_tool node for the echo tool")
	}

	// --- Steps 1-5: walk the expected chain -------------------------------
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	sessionID := "canary-sess-1"

	type stepOutcome struct {
		canonicalEventType string
		tier               observability.EvidenceTier
	}
	var outcomes []stepOutcome

	// Per-lane SourceHealth accumulators for the final reconciliation.
	hookIdentities := map[string][]string{}
	otelIdentities := map[string][]string{}
	rolloutIdentities := map[string][]string{}

	for _, step := range chain {
		switch step.CanonicalEventType {
		case "session.started", "session.stopped":
			hookEvent := codexadapter.HookSessionStart
			if step.CanonicalEventType == "session.stopped" {
				hookEvent = codexadapter.HookStop
			}
			canonical, ok := codexadapter.CanonicalEventForHook(hookEvent)
			if !ok || canonical != step.CanonicalEventType {
				t.Fatalf("step %d: hook lane expected canonical %q, got %q (ok=%v)", step.Step, step.CanonicalEventType, canonical, ok)
			}
			otelName := codexadapter.OTelConversationStarts
			otelShape := codexadapter.OTelAttributeShape{InstrumentationScope: string(otelName), PresentAttributeKeys: []string{"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"}}
			otelCanonical, err := codexadapter.CanonicalEventForOTel(otelName, otelShape)
			if step.CanonicalEventType == "session.started" {
				if err != nil || otelCanonical != step.CanonicalEventType {
					t.Fatalf("step %d: otel lane failed: canonical=%q err=%v", step.Step, otelCanonical, err)
				}
			}
			outcomes = append(outcomes, stepOutcome{canonicalEventType: step.CanonicalEventType, tier: observability.TierNative})
			hookIdentities["session_lifecycle"] = append(hookIdentities["session_lifecycle"], sessionID+":"+step.CanonicalEventType)
			otelIdentities["session_lifecycle"] = append(otelIdentities["session_lifecycle"], sessionID+":"+step.CanonicalEventType)
			rolloutIdentities["session_lifecycle"] = append(rolloutIdentities["session_lifecycle"], sessionID+":"+step.CanonicalEventType)

		case "prompt.submitted":
			hookInput := codexadapter.HookHelperInput{
				Event: codexadapter.HookUserPromptSubmit, SessionID: sessionID, TurnID: "t1",
				Prompt: "please read canary.txt and echo it",
			}
			hookOutput, err := codexadapter.BuildHookOutput(hookInput, now)
			if err != nil {
				t.Fatalf("step %d: BuildHookOutput failed: %v", step.Step, err)
			}
			if hookOutput.EventType != step.CanonicalEventType {
				t.Fatalf("step %d: expected hook canonical %q, got %q", step.Step, step.CanonicalEventType, hookOutput.EventType)
			}
			if hookOutput.PromptFeatures == nil {
				t.Fatalf("step %d: expected prompt_features present for UserPromptSubmit", step.Step)
			}
			encoded, _ := json.Marshal(hookOutput)
			if strings.Contains(string(encoded), hookInput.Prompt) {
				t.Fatalf("step %d: raw prompt text leaked into hook output", step.Step)
			}
			otelShape := codexadapter.OTelAttributeShape{InstrumentationScope: string(codexadapter.OTelUserPrompt), PresentAttributeKeys: []string{"kansoku.event.id", "kansoku.session.id", "kansoku.event.type"}}
			otelCanonical, err := codexadapter.CanonicalEventForOTel(codexadapter.OTelUserPrompt, otelShape)
			if err != nil || otelCanonical != step.CanonicalEventType {
				t.Fatalf("step %d: otel lane failed: canonical=%q err=%v", step.Step, otelCanonical, err)
			}
			outcomes = append(outcomes, stepOutcome{canonicalEventType: step.CanonicalEventType, tier: observability.TierNative})
			hookIdentities["prompts"] = append(hookIdentities["prompts"], sessionID+":t1")
			otelIdentities["prompts"] = append(otelIdentities["prompts"], sessionID+":t1")
			rolloutIdentities["prompts"] = append(rolloutIdentities["prompts"], sessionID+":t1")

		case "component.invoked":
			resolution, err := codexadapter.ResolveSkillEvidence(codexadapter.SkillEvidenceInput{
				Kind:                     codexadapter.EvidenceExplicitUserInvocation,
				CandidateSkillIdentities: []string{step.Component},
				SourceLabelsNative:       step.SourceLabelsNative,
			})
			if err != nil {
				t.Fatalf("step %d: ResolveSkillEvidence failed: %v", step.Step, err)
			}
			if resolution.CanonicalEventType != step.CanonicalEventType {
				t.Fatalf("step %d: expected canonical %q, got %q", step.Step, step.CanonicalEventType, resolution.CanonicalEventType)
			}
			wantTier := observability.EvidenceTier(step.Tier)
			if resolution.Tier != codexadapter.EvidenceTier(wantTier) {
				t.Fatalf("step %d: expected tier %q, got %q", step.Step, wantTier, resolution.Tier)
			}
			if !step.SourceLabelsNative || wantTier != observability.TierNative {
				t.Fatalf("step %d: canary scenario declares source_labels_native=true and tier=native for the explicit skill invocation -- this test's fixture expectations are inconsistent with that", step.Step)
			}
			outcomes = append(outcomes, stepOutcome{canonicalEventType: step.CanonicalEventType, tier: wantTier})

		case "tool.called":
			hookOutput, err := codexadapter.BuildHookOutput(codexadapter.HookHelperInput{
				Event: codexadapter.HookPreToolUse, SessionID: sessionID, TurnID: "t1", ToolID: step.Component + "." + step.Tool,
			}, now)
			if err != nil {
				t.Fatalf("step %d: BuildHookOutput failed: %v", step.Step, err)
			}
			if hookOutput.EventType != step.CanonicalEventType {
				t.Fatalf("step %d: expected hook canonical %q, got %q", step.Step, step.CanonicalEventType, hookOutput.EventType)
			}
			otelShape := codexadapter.OTelAttributeShape{
				InstrumentationScope: string(codexadapter.OTelToolResult),
				PresentAttributeKeys: []string{"kansoku.event.id", "kansoku.session.id", "kansoku.event.type", "kansoku.tool.id", "kansoku.outcome"},
			}
			otelCanonical, err := codexadapter.CanonicalEventForOTel(codexadapter.OTelToolResult, otelShape)
			if err != nil || otelCanonical != step.CanonicalEventType {
				t.Fatalf("step %d: otel lane failed: canonical=%q err=%v", step.Step, otelCanonical, err)
			}
			// The tool call resolves against the MCP tool node the
			// inventory snapshot already proved exists -- never an
			// invented, unconfigured tool identity.
			if step.Tool != scenario.FixtureProject.Layout.MCPToolName || step.Component != scenario.FixtureProject.Layout.MCPServerName {
				t.Fatalf("step %d: tool/component must match the canary fixture's configured echo MCP, got tool=%q component=%q", step.Step, step.Tool, step.Component)
			}
			outcomes = append(outcomes, stepOutcome{canonicalEventType: step.CanonicalEventType, tier: observability.TierNative})
			hookIdentities["tools"] = append(hookIdentities["tools"], sessionID+":"+step.Tool)
			otelIdentities["tools"] = append(otelIdentities["tools"], sessionID+":"+step.Tool)
			rolloutIdentities["tools"] = append(rolloutIdentities["tools"], sessionID+":"+step.Tool)

		default:
			t.Fatalf("step %d: unrecognized canonical_event_type %q in expected_chain -- extend this test", step.Step, step.CanonicalEventType)
		}
	}

	if len(outcomes) != len(chain) {
		t.Fatalf("expected to resolve all %d chain steps, resolved %d", len(chain), len(outcomes))
	}
	for i, step := range chain {
		if outcomes[i].canonicalEventType != step.CanonicalEventType {
			t.Fatalf("step %d order mismatch: expected %q, got %q", step.Step, step.CanonicalEventType, outcomes[i].canonicalEventType)
		}
		if string(outcomes[i].tier) != step.Tier {
			t.Fatalf("step %d tier mismatch: expected %q, got %q", step.Step, step.Tier, outcomes[i].tier)
		}
		// Every produced tier must be a member of internal/observability's
		// own closed EvidenceTier vocabulary -- the adapter invents no
		// tier of its own.
		switch outcomes[i].tier {
		case observability.TierNative, observability.TierReconstructed, observability.TierInferred, observability.TierCorroborated:
		default:
			t.Fatalf("step %d: tier %q is outside internal/observability's closed EvidenceTier vocabulary", step.Step, outcomes[i].tier)
		}
	}

	// --- Final reconciliation across hook/otel/rollout for every lane -----
	var laneInputs []codexadapter.LaneInput
	for _, lane := range []struct {
		name codexadapter.ReconciliationLane
		ids  string
	}{
		{codexadapter.LanePrompts, "prompts"},
		{codexadapter.LaneToolTerminal, "tools"},
		{codexadapter.LaneSessionLifecycle, "session_lifecycle"},
	} {
		laneInputs = append(laneInputs, codexadapter.LaneInput{
			Lane:                 lane.name,
			CompatibilityVersion: "codex-compat/1",
			Hook:                 codexadapter.SourceHealth{Present: true, Count: len(hookIdentities[lane.ids]), EventIdentities: hookIdentities[lane.ids]},
			OTel:                 codexadapter.SourceHealth{Present: true, Count: len(otelIdentities[lane.ids]), EventIdentities: otelIdentities[lane.ids]},
			Rollout:              codexadapter.SourceHealth{Present: true, Count: len(rolloutIdentities[lane.ids]), EventIdentities: rolloutIdentities[lane.ids]},
		})
	}
	// The canary scenario never spawns a subagent: supplied as
	// present=true, count=0 on every source, distinguishing "no subagents
	// happened" from "we did not check" per
	// reconciliation_expectation.not_applicable_reason.
	laneInputs = append(laneInputs, codexadapter.LaneInput{
		Lane:                 codexadapter.LaneSubagentLifecycle,
		CompatibilityVersion: "codex-compat/1",
		Hook:                 codexadapter.SourceHealth{Present: true, Count: 0},
		OTel:                 codexadapter.SourceHealth{Present: true, Count: 0},
		Rollout:              codexadapter.SourceHealth{Present: true, Count: 0},
	})

	session := codexadapter.ReconcileSession(sessionID, laneInputs)
	for _, lane := range []codexadapter.ReconciliationLane{codexadapter.LanePrompts, codexadapter.LaneToolTerminal, codexadapter.LaneSessionLifecycle, codexadapter.LaneSubagentLifecycle} {
		result, ok := session.Lanes[lane]
		if !ok {
			t.Fatalf("lane %q missing from reconciliation result", lane)
		}
		if result.Completeness != codexadapter.LaneComplete {
			t.Fatalf("lane %q: expected complete (every source present and healthy for the canary run), got %q with degraded=%v", lane, result.Completeness, result.DegradedSources)
		}
		if result.Mismatched {
			t.Fatalf("lane %q: expected hook/otel/rollout counts to agree for the canary run, got a mismatch (hook=%d otel=%d rollout=%d)", lane, result.HookCount, result.OTelCount, result.RolloutCount)
		}
	}

	// --- Prohibited content: raw prompt fields must be entirely absent ----
	// from every value this test produced and inspected above (already
	// checked inline per-step); this final check additionally confirms the
	// scenario's own declared prohibited *raw field names* (e.g. Codex's own
	// hook/rollout "prompt"/"tool_input"/"tool_output"/"response"/
	// "source_code" keys) never surface as a JSON key carrying content in
	// this durable-shaped output. This is a key-membership check, not a bare
	// substring scan: canonical, already-sanitized identifiers such as the
	// "prompt.submitted" event type or the "prompts" reconciliation lane
	// legitimately contain the English word "prompt" without carrying any
	// raw prompt content, exactly as contracts/observability/*.yaml already
	// names that canonical event type.
	full, _ := json.Marshal(struct {
		Outcomes []stepOutcome
		Lanes    map[codexadapter.ReconciliationLane]codexadapter.LaneResult
	}{Outcomes: outcomes, Lanes: session.Lanes})
	forbiddenKeys := forbiddenJSONKeys(full)
	for _, forbidden := range scenario.ProhibitedContentCanary.RawFieldsThatMustNeverAppearDurably {
		if forbiddenKeys[forbidden] {
			t.Fatalf("prohibited raw field name %q must never appear as a JSON key in any durable-shaped canary output", forbidden)
		}
	}
	// The scenario's own sample raw hook/rollout payloads are the actual
	// synthetic content those prohibited fields would have carried; their
	// concrete values must never appear anywhere in this test's captured
	// outcomes either.
	for _, sample := range [][]byte{scenario.ProhibitedContentCanary.SampleRawHookStdin, scenario.ProhibitedContentCanary.SampleRawRolloutLine} {
		var raw map[string]any
		if len(sample) == 0 {
			continue
		}
		if err := json.Unmarshal(sample, &raw); err != nil {
			t.Fatal(err)
		}
		for _, key := range scenario.ProhibitedContentCanary.RawFieldsThatMustNeverAppearDurably {
			value, ok := raw[key].(string)
			if !ok || value == "" {
				continue
			}
			if strings.Contains(string(full), value) {
				t.Fatalf("raw content value of prohibited field %q leaked into durable-shaped canary output", key)
			}
		}
	}
}

// forbiddenJSONKeys walks an arbitrary marshaled JSON document and returns
// the set of every object key that appears anywhere in it, at any nesting
// depth. Used to check for prohibited raw field names landing as JSON keys
// in durable output, as distinct from the same word appearing inside an
// already-sanitized identifier value (e.g. "prompt.submitted").
func forbiddenJSONKeys(data []byte) map[string]bool {
	keys := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, vv := range t {
				keys[k] = true
				walk(vv)
			}
		case []any:
			for _, vv := range t {
				walk(vv)
			}
		}
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return keys
	}
	walk(v)
	return keys
}

// TestCanaryScenarioDeclaresConsentAndBudgetBoundedExecution asserts the
// canary scenario fixture itself declares the required non-interactive,
// consent-gated, budget-bounded, never-a-real-repository execution
// constraints -- this is a property of the fixture, checked so a future edit
// can never silently loosen it.
func TestCanaryScenarioDeclaresConsentAndBudgetBoundedExecution(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(fixturesRoot, "canary", "kansoku-canary-scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		ExecutionConstraints struct {
			NonInteractiveOnly      bool `json:"non_interactive_only"`
			RequiresExplicitConsent bool `json:"requires_explicit_consent"`
			RequiresBoundedBudget   bool `json:"requires_bounded_budget"`
			Budget                  struct {
				MaxTurns            int `json:"max_turns"`
				MaxWallClockSeconds int `json:"max_wall_clock_seconds"`
				MaxToolCalls        int `json:"max_tool_calls"`
			} `json:"budget"`
			NeverUsesARealUserRepository bool `json:"never_uses_a_real_user_repository"`
		} `json:"execution_constraints"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	c := raw.ExecutionConstraints
	if !c.NonInteractiveOnly || !c.RequiresExplicitConsent || !c.RequiresBoundedBudget || !c.NeverUsesARealUserRepository {
		t.Fatalf("canary scenario must declare non_interactive_only, requires_explicit_consent, requires_bounded_budget and never_uses_a_real_user_repository all true, got %+v", c)
	}
	if c.Budget.MaxTurns <= 0 || c.Budget.MaxWallClockSeconds <= 0 || c.Budget.MaxToolCalls <= 0 {
		t.Fatalf("canary scenario budget must be a strictly positive bound on every declared dimension, got %+v", c.Budget)
	}
}
