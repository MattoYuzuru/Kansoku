package claudeadapter_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/claudeadapter"
)

// --- claude.inventory ---------------------------------------------------------

func sampleInventoryInput() claudeadapter.InventoryInput {
	return claudeadapter.InventoryInput{
		InstallationID: "install-1",
		StandaloneSkills: []claudeadapter.SkillDescriptor{
			{Name: "code-reviewer", Scope: adaptersdk.ScopeUser, Enabled: true, PathPseudonym: "pseudo-1", Fingerprint: "fp-skill-1"},
		},
		StandaloneCommands: []claudeadapter.CommandDescriptor{
			{Name: "deploy", Scope: adaptersdk.ScopeUser, Enabled: true, PathPseudonym: "pseudo-2", Fingerprint: "fp-cmd-1"},
		},
		StandaloneSubagents: []claudeadapter.SubagentDescriptor{
			{Name: "researcher", Scope: adaptersdk.ScopeRepository, Enabled: true, PathPseudonym: "pseudo-3", Fingerprint: "fp-sub-1"},
		},
		StandaloneMCPServers: []claudeadapter.MCPServerDescriptor{
			{Name: "filesystem-mcp", Scope: adaptersdk.ScopeUser, Enabled: true, AdvertisedTools: []string{"read_file", "write_file"}, Fingerprint: "fp-mcp-1"},
		},
		Plugins: []claudeadapter.PluginDescriptor{
			{
				Name: "acme-toolkit", Version: "1.2.0", Scope: adaptersdk.ScopeUser,
				ActiveEnabledFor: "install-1", FromMarketplace: "acme-market", PathPseudonym: "pseudo-4", Fingerprint: "fp-plugin-1",
				BundledSkills: []claudeadapter.SkillDescriptor{
					{Name: "acme-linter", Scope: adaptersdk.ScopeUser, Enabled: true, Fingerprint: "fp-skill-2"},
				},
				BundledMCPServers: []claudeadapter.MCPServerDescriptor{
					{Name: "acme-mcp", Scope: adaptersdk.ScopeUser, Enabled: true, AdvertisedTools: []string{"acme_tool"}, Fingerprint: "fp-mcp-2"},
				},
			},
			{
				Name: "acme-cache-only", Version: "0.9.0", Scope: adaptersdk.ScopePluginCache,
				CachedOnly: true, PathPseudonym: "pseudo-5", Fingerprint: "fp-plugin-cache-1",
			},
		},
		Marketplaces: []claudeadapter.MarketplaceDescriptor{
			{Name: "acme-market", PathPseudonym: "pseudo-market-1", Fingerprint: "fp-market-1"},
		},
	}
}

func TestBuildInventorySnapshotIsIdempotentOnReplay(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	input := sampleInventoryInput()

	first, err := claudeadapter.BuildInventorySnapshot(input, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := claudeadapter.BuildInventorySnapshot(input, now)
	if err != nil {
		t.Fatal(err)
	}

	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("replaying the identical inventory input must yield a byte-identical snapshot (idempotent replay)")
	}
	if len(first.Nodes) == 0 || len(first.Edges) == 0 {
		t.Fatal("sample input must produce a non-empty inventory graph")
	}
}

func TestBuildInventorySnapshotLinksPluginBundledComponentsWithBundlesEdgeNeverStandalone(t *testing.T) {
	now := time.Now()
	input := sampleInventoryInput()
	snapshot, err := claudeadapter.BuildInventorySnapshot(input, now)
	if err != nil {
		t.Fatal(err)
	}

	var pluginNode *adaptersdk.Node
	var bundledSkillNode *adaptersdk.Node
	for i, node := range snapshot.Nodes {
		if node.Kind == adaptersdk.NodePluginPackage && node.DeclaredName == "acme-toolkit" {
			pluginNode = &snapshot.Nodes[i]
		}
		if node.Kind == adaptersdk.NodeSkillIdentity && node.DeclaredName == "acme-linter" {
			bundledSkillNode = &snapshot.Nodes[i]
		}
	}
	if pluginNode == nil {
		t.Fatal("expected an acme-toolkit plugin_package node")
	}
	if bundledSkillNode == nil {
		t.Fatal("expected an acme-linter skill_identity node for the plugin-bundled skill")
	}

	foundBundles := false
	foundDirectEnabledForBundledSkill := false
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeBundles && edge.FromNode == pluginNode.NodeID && edge.ToNode == bundledSkillNode.NodeID {
			foundBundles = true
		}
		if edge.Kind == adaptersdk.EdgeEnabledFor && edge.FromNode == bundledSkillNode.NodeID {
			foundDirectEnabledForBundledSkill = true
		}
	}
	if !foundBundles {
		t.Fatal("a plugin-bundled skill must be linked to its owning plugin package node by a bundles edge")
	}
	if foundDirectEnabledForBundledSkill {
		t.Fatal("a plugin-bundled component must never receive its own direct enabled_for edge to the installation (it is enabled transitively through the plugin, never reported as a standalone unowned component)")
	}
}

func TestBuildInventorySnapshotCacheEntryNeverReportedEnabled(t *testing.T) {
	now := time.Now()
	input := sampleInventoryInput()
	snapshot, err := claudeadapter.BuildInventorySnapshot(input, now)
	if err != nil {
		t.Fatal(err)
	}

	var cacheNode *adaptersdk.Node
	for i, node := range snapshot.Nodes {
		if node.Kind == adaptersdk.NodeCacheArtifact && node.DeclaredName == "acme-cache-only" {
			cacheNode = &snapshot.Nodes[i]
		}
	}
	if cacheNode == nil {
		t.Fatal("expected a cache_artifact node for the cache-only plugin")
	}
	if !cacheNode.CachedOnly {
		t.Fatal("cache-only plugin node must be flagged CachedOnly")
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeEnabledFor && edge.FromNode == cacheNode.NodeID {
			t.Fatal("a plugin/marketplace cache artifact must never receive an enabled_for edge")
		}
	}
}

func TestBuildInventorySnapshotDisambiguatesDuplicateComponentNamesAcrossOwners(t *testing.T) {
	// Two skills sharing the declared name "shared-skill": one standalone,
	// one bundled inside a different plugin. Neither may be silently
	// merged into a single node; both must remain distinct and linked by a
	// collides_with edge.
	now := time.Now()
	input := claudeadapter.InventoryInput{
		InstallationID: "install-2",
		StandaloneSkills: []claudeadapter.SkillDescriptor{
			{Name: "shared-skill", Scope: adaptersdk.ScopeUser, Enabled: true, Fingerprint: "fp-standalone"},
		},
		Plugins: []claudeadapter.PluginDescriptor{
			{
				Name: "plugin-a", Scope: adaptersdk.ScopeUser, ActiveEnabledFor: "install-2", Fingerprint: "fp-plugin-a",
				BundledSkills: []claudeadapter.SkillDescriptor{
					{Name: "shared-skill", Scope: adaptersdk.ScopeUser, Enabled: true, Fingerprint: "fp-bundled-a"},
				},
			},
			{
				Name: "plugin-b", Scope: adaptersdk.ScopeUser, ActiveEnabledFor: "install-2", Fingerprint: "fp-plugin-b",
				BundledSkills: []claudeadapter.SkillDescriptor{
					{Name: "shared-skill", Scope: adaptersdk.ScopeUser, Enabled: true, Fingerprint: "fp-bundled-b"},
				},
			},
		},
	}
	snapshot, err := claudeadapter.BuildInventorySnapshot(input, now)
	if err != nil {
		t.Fatal(err)
	}

	var skillNodes []adaptersdk.Node
	for _, node := range snapshot.Nodes {
		if node.Kind == adaptersdk.NodeSkillIdentity && node.DeclaredName == "shared-skill" {
			skillNodes = append(skillNodes, node)
		}
	}
	if len(skillNodes) != 3 {
		t.Fatalf("expected 3 distinct shared-skill nodes (standalone + two plugin-bundled), never merged, got %d", len(skillNodes))
	}
	// Every pairing among the 3 nodes must be linked by a collides_with edge:
	// 3 nodes => 3 pairs.
	collisionPairs := 0
	nodeIDs := map[string]bool{}
	for _, node := range skillNodes {
		nodeIDs[node.NodeID] = true
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeCollidesWith && nodeIDs[edge.FromNode] && nodeIDs[edge.ToNode] {
			collisionPairs++
		}
	}
	if collisionPairs != 3 {
		t.Fatalf("expected 3 collides_with edges linking every pair of the 3 duplicate-named skill nodes, got %d", collisionPairs)
	}
}

func TestBuildInventorySnapshotRejectsTooManyRepositoryTargets(t *testing.T) {
	input := claudeadapter.InventoryInput{InstallationID: "install-3"}
	for i := 0; i < 65; i++ {
		input.RepositoryTargets = append(input.RepositoryTargets, "/repo")
	}
	if _, err := claudeadapter.BuildInventorySnapshot(input, time.Now()); !errors.Is(err, claudeadapter.ErrTooManyRepositoryTargets) {
		t.Fatalf("expected ErrTooManyRepositoryTargets, got %v", err)
	}
}

func TestBuildInventorySnapshotRejectsRelativeRepositoryTarget(t *testing.T) {
	input := claudeadapter.InventoryInput{InstallationID: "install-4", RepositoryTargets: []string{"relative/path"}}
	if _, err := claudeadapter.BuildInventorySnapshot(input, time.Now()); err == nil {
		t.Fatal("a relative repository target must be rejected, never silently accepted as a speculative scan root")
	}
}

func TestBuildInventorySnapshotMCPToolsLinkedByProvidesEdge(t *testing.T) {
	now := time.Now()
	input := sampleInventoryInput()
	snapshot, err := claudeadapter.BuildInventorySnapshot(input, now)
	if err != nil {
		t.Fatal(err)
	}
	var mcpServerNode, mcpToolNode *adaptersdk.Node
	for i, node := range snapshot.Nodes {
		if node.Kind == adaptersdk.NodeMCPServerInstance && node.DeclaredName == "filesystem-mcp" {
			mcpServerNode = &snapshot.Nodes[i]
		}
		if node.Kind == adaptersdk.NodeMCPTool && node.DeclaredName == "read_file" {
			mcpToolNode = &snapshot.Nodes[i]
		}
	}
	if mcpServerNode == nil || mcpToolNode == nil {
		t.Fatal("expected both an mcp_server_instance and an mcp_tool node")
	}
	found := false
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeProvides && edge.FromNode == mcpServerNode.NodeID && edge.ToNode == mcpToolNode.NodeID {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a provides edge from the MCP server instance to its advertised MCP tool node")
	}
}

// --- skill evidence -------------------------------------------------------------

func TestResolveSkillEvidenceCoversEveryDeclaredKind(t *testing.T) {
	for _, kind := range claudeadapter.AllSkillEvidenceKinds() {
		input := claudeadapter.SkillEvidenceInput{Kind: kind, CandidateSkillIdentities: []string{"skill-a"}}
		resolution, err := claudeadapter.ResolveSkillEvidence(input)
		if err != nil {
			t.Fatalf("kind %q: unexpected error %v", kind, err)
		}
		if resolution.CanonicalEventType == "" {
			t.Fatalf("kind %q must resolve to a non-empty canonical event type", kind)
		}
	}
}

func TestResolveSkillEvidenceSemanticOpportunityAlwaysInferredNeverPromoted(t *testing.T) {
	resolution, err := claudeadapter.ResolveSkillEvidence(claudeadapter.SkillEvidenceInput{
		Kind: claudeadapter.EvidenceSemanticOpportunity, CandidateSkillIdentities: []string{"skill-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Tier != claudeadapter.EvidenceTierInferred {
		t.Fatalf("semantic_opportunity_classifier must always resolve to inferred tier, got %q", resolution.Tier)
	}
	if resolution.CanonicalEventType == claudeadapter.CanonicalComponentInvoked {
		t.Fatal("an inferred opportunity must never be represented as component.invoked")
	}
}

func TestResolveSkillEvidenceExplicitVsImplicitModeOnlyWhenNativelyLabeled(t *testing.T) {
	explicit, err := claudeadapter.ResolveSkillEvidence(claudeadapter.SkillEvidenceInput{
		Kind: claudeadapter.EvidenceSkillToolCallExplicit, CandidateSkillIdentities: []string{"skill-a"}, ModeKnown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Mode != "explicit" {
		t.Fatalf("expected mode explicit, got %q", explicit.Mode)
	}

	implicit, err := claudeadapter.ResolveSkillEvidence(claudeadapter.SkillEvidenceInput{
		Kind: claudeadapter.EvidenceSkillToolCallImplicit, CandidateSkillIdentities: []string{"skill-a"}, ModeKnown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if implicit.Mode != "implicit" {
		t.Fatalf("expected mode implicit, got %q", implicit.Mode)
	}

	// ModeKnown=false must never guess a mode, even for a kind that could
	// otherwise carry one.
	unknown, err := claudeadapter.ResolveSkillEvidence(claudeadapter.SkillEvidenceInput{
		Kind: claudeadapter.EvidenceSkillToolCallExplicit, CandidateSkillIdentities: []string{"skill-a"}, ModeKnown: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Mode != "unknown" {
		t.Fatalf("an unlabeled native call must resolve to mode unknown, never a guessed mode, got %q", unknown.Mode)
	}
}

func TestResolveSkillEvidenceAmbiguousOwnershipNeverPromotedToInvoked(t *testing.T) {
	resolution, err := claudeadapter.ResolveSkillEvidence(claudeadapter.SkillEvidenceInput{
		Kind:                     claudeadapter.EvidenceUniquelyOwnedHelperCall,
		CandidateSkillIdentities: []string{"skill-a", "skill-b"},
	})
	if !errors.Is(err, claudeadapter.ErrAmbiguousOwnershipPromotion) {
		t.Fatalf("expected ErrAmbiguousOwnershipPromotion, got %v", err)
	}
	if resolution.CanonicalEventType != claudeadapter.CanonicalComponentExecuted {
		t.Fatalf("ambiguous ownership must remain component.executed, got %q", resolution.CanonicalEventType)
	}
	if len(resolution.CandidateSkillIdentities) != 2 {
		t.Fatal("ambiguous ownership resolution must preserve every candidate skill identity")
	}
}

func TestResolveSkillEvidenceUniquelyOwnedRequiresAtLeastOneCandidate(t *testing.T) {
	_, err := claudeadapter.ResolveSkillEvidence(claudeadapter.SkillEvidenceInput{Kind: claudeadapter.EvidenceUniquelyOwnedHelperCall})
	if err == nil {
		t.Fatal("uniquely_owned_helper_execution with zero candidates must be rejected")
	}
}

func TestResolveSkillEvidenceRejectsUnknownKind(t *testing.T) {
	_, err := claudeadapter.ResolveSkillEvidence(claudeadapter.SkillEvidenceInput{Kind: "not_a_real_kind"})
	if !errors.Is(err, claudeadapter.ErrUnknownSkillEvidenceKind) {
		t.Fatalf("expected ErrUnknownSkillEvidenceKind, got %v", err)
	}
}

func TestSourceToCanonicalTableCoversNineDocumentedRows(t *testing.T) {
	rows := claudeadapter.SourceToCanonicalTable()
	if len(rows) != 9 {
		t.Fatalf("expected 9 source-to-canonical rows matching contracts/claude/skill-evidence-and-reconciliation.yaml, got %d", len(rows))
	}
	for _, row := range rows {
		if row.SourceEvidence == "" || row.CanonicalEventType == "" || row.Tier == "" {
			t.Fatalf("every source-to-canonical row must be fully populated, got %+v", row)
		}
	}
}

// --- reconciliation ---------------------------------------------------------------

func TestAllReconciliationLanesHasEightDeclaredLanes(t *testing.T) {
	lanes := claudeadapter.AllReconciliationLanes()
	if len(lanes) != 8 {
		t.Fatalf("expected 8 declared reconciliation lanes, got %d", len(lanes))
	}
}

func TestReconcileLaneMissingSourceDegradesOnlyThatSourceNeverFabricatesWholeSessionZero(t *testing.T) {
	// hook is missing entirely; otel and transcript both genuinely observed
	// 3 events. The lane must report hook as degraded and otel/transcript
	// counts as-is -- it must never report a session-wide zero merely
	// because one source was absent.
	result := claudeadapter.ReconcileLane(claudeadapter.LaneInput{
		Lane:                 claudeadapter.LanePrompts,
		CompatibilityVersion: "claude-compat/1",
		Hook:                 claudeadapter.SourceHealth{Present: false},
		OTel:                 claudeadapter.SourceHealth{Present: true, Count: 3},
		Transcript:           claudeadapter.SourceHealth{Present: true, Count: 3},
	})
	if result.Completeness != claudeadapter.LanePartial {
		t.Fatalf("a lane missing one source must be partial, got %q", result.Completeness)
	}
	if len(result.DegradedSources) != 1 || result.DegradedSources[0] != claudeadapter.ReconSourceHook {
		t.Fatalf("expected exactly claude.hook degraded, got %+v", result.DegradedSources)
	}
	if result.OTelCount != 3 || result.TranscriptCount != 3 {
		t.Fatal("present sources' real counts must be reported as observed, never zeroed out because a sibling source was missing")
	}
	if result.Mismatched {
		t.Fatal("otel and transcript agree; a missing third source alone must never itself flag a mismatch")
	}
}

func TestReconcileLaneAllSourcesMissingReportsEveryOneDegradedNeverFabricatedZero(t *testing.T) {
	result := claudeadapter.ReconcileLane(claudeadapter.LaneInput{Lane: claudeadapter.LaneSubagentLifecycle})
	if result.Completeness != claudeadapter.LanePartial {
		t.Fatal("a lane with every source missing must be partial, never falsely complete")
	}
	if len(result.DegradedSources) != 3 {
		t.Fatalf("expected all 3 sources named as degraded, got %+v", result.DegradedSources)
	}
	if result.Mismatched {
		t.Fatal("with zero live sources there is nothing to compare, so Mismatched must stay false")
	}
}

func TestReconcileLaneDetectsMismatchWhenAllSourcesPresentButCountsDisagree(t *testing.T) {
	result := claudeadapter.ReconcileLane(claudeadapter.LaneInput{
		Lane:       claudeadapter.LaneToolTerminal,
		Hook:       claudeadapter.SourceHealth{Present: true, Count: 5},
		OTel:       claudeadapter.SourceHealth{Present: true, Count: 5},
		Transcript: claudeadapter.SourceHealth{Present: true, Count: 4},
	})
	if result.Completeness != claudeadapter.LaneComplete {
		t.Fatal("every source present means the lane is complete even if counts disagree")
	}
	if !result.Mismatched {
		t.Fatal("differing counts across all-present sources must be flagged as mismatched")
	}
}

func TestReconcileSessionIsDeterministicAcrossRepeatedCalls(t *testing.T) {
	inputs := []claudeadapter.LaneInput{
		{Lane: claudeadapter.LanePrompts, Hook: claudeadapter.SourceHealth{Present: true, Count: 2}, OTel: claudeadapter.SourceHealth{Present: true, Count: 2}, Transcript: claudeadapter.SourceHealth{Present: true, Count: 2}},
		{Lane: claudeadapter.LaneMCPCalls, Hook: claudeadapter.SourceHealth{Present: false}, OTel: claudeadapter.SourceHealth{Present: true, Count: 1}, Transcript: claudeadapter.SourceHealth{Present: true, Count: 1}},
	}
	first := claudeadapter.ReconcileSession("sess-1", inputs)
	second := claudeadapter.ReconcileSession("sess-1", inputs)
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("reconciling the same session inputs twice must be byte-identical (idempotent replay)")
	}
}

func TestResolveToleranceNeverFallsBackToAnotherVersionsNumbers(t *testing.T) {
	if _, ok := claudeadapter.ResolveTolerance("claude-compat/1"); !ok {
		t.Fatal("expected claude-compat/1 to resolve")
	}
	if _, ok := claudeadapter.ResolveTolerance("claude-compat/999-does-not-exist"); ok {
		t.Fatal("an unknown compatibility version must report ok=false, never silently reuse another version's tolerance")
	}
}

// --- double-attribution guard -----------------------------------------------------

func TestAttributeSkillCostNeverDoubleCountsSameEventIdentityAcrossSources(t *testing.T) {
	// The same underlying Skill tool call is visible through both
	// claude.otel (which carries native token/cost semantics) and
	// claude.hook (which does not carry cost/token data at all). This must
	// contribute to the skill's total exactly once.
	observations := []claudeadapter.SkillCallObservation{
		{EventIdentity: "sess-1:turn-4:tool-Skill", Source: claudeadapter.ReconSourceOTel, SkillID: "code-reviewer", TokenCount: 1500, CostUSD: 0.045, NativeSemanticsSupport: true},
		{EventIdentity: "sess-1:turn-4:tool-Skill", Source: claudeadapter.ReconSourceHook, SkillID: "code-reviewer"},
	}
	attribution := claudeadapter.AttributeSkillCost(observations)
	entry, ok := attribution["code-reviewer"]
	if !ok {
		t.Fatal("expected an attribution entry for code-reviewer")
	}
	if entry.ObservationCount != 1 {
		t.Fatalf("a call visible in both otel and hooks must be counted once, got ObservationCount=%d", entry.ObservationCount)
	}
	if entry.DuplicateObservationsExcluded != 1 {
		t.Fatalf("expected exactly 1 duplicate observation excluded, got %d", entry.DuplicateObservationsExcluded)
	}
	if entry.TotalTokens != 1500 {
		t.Fatalf("expected total tokens 1500 (counted once via the native otel observation), got %d", entry.TotalTokens)
	}
	if entry.TotalCostUSD != 0.045 {
		t.Fatalf("expected total cost 0.045 (counted once), got %v", entry.TotalCostUSD)
	}
}

func TestAttributeSkillCostDistinctCallsAreNotTreatedAsDuplicates(t *testing.T) {
	observations := []claudeadapter.SkillCallObservation{
		{EventIdentity: "sess-1:turn-1:tool-Skill", Source: claudeadapter.ReconSourceOTel, SkillID: "code-reviewer", TokenCount: 100, NativeSemanticsSupport: true},
		{EventIdentity: "sess-1:turn-2:tool-Skill", Source: claudeadapter.ReconSourceOTel, SkillID: "code-reviewer", TokenCount: 200, NativeSemanticsSupport: true},
	}
	attribution := claudeadapter.AttributeSkillCost(observations)
	entry := attribution["code-reviewer"]
	if entry.ObservationCount != 2 {
		t.Fatalf("two genuinely distinct calls must both be counted, got %d", entry.ObservationCount)
	}
	if entry.TotalTokens != 300 {
		t.Fatalf("expected summed tokens 300 across 2 distinct calls, got %d", entry.TotalTokens)
	}
	if entry.DuplicateObservationsExcluded != 0 {
		t.Fatal("distinct event identities must never be reported as duplicates")
	}
}

func TestAttributeSkillCostRetainsConcurrentSubagentShareRatherThanDividingOrSumming(t *testing.T) {
	observations := []claudeadapter.SkillCallObservation{
		{EventIdentity: "sess-1:turn-1:tool-Skill:subagent-a", Source: claudeadapter.ReconSourceOTel, SkillID: "researcher", TokenCount: 1000, NativeSemanticsSupport: true, ConcurrentSubagentShare: true},
		{EventIdentity: "sess-1:turn-1:tool-Skill:subagent-b", Source: claudeadapter.ReconSourceOTel, SkillID: "researcher", TokenCount: 1000, NativeSemanticsSupport: true, ConcurrentSubagentShare: true},
	}
	attribution := claudeadapter.AttributeSkillCost(observations)
	entry := attribution["researcher"]
	if !entry.ConcurrentSubagentShareNoted {
		t.Fatal("a documented concurrent-subagent-shared-call case must be surfaced, not silently absorbed")
	}
	if entry.ObservationCount != 2 {
		t.Fatal("two distinct concurrent subagent calls sharing one billed call are still two distinct facts, never merged into one")
	}
	if entry.TotalTokens != 2000 {
		t.Fatalf("retained-and-surfaced means summed as observed (never divided), expected 2000, got %d", entry.TotalTokens)
	}
}

func TestAttributeSkillCostOnlyAttributesWithNativeSemanticsSupport(t *testing.T) {
	observations := []claudeadapter.SkillCallObservation{
		{EventIdentity: "sess-1:turn-1:tool-Skill", Source: claudeadapter.ReconSourceHook, SkillID: "code-reviewer", TokenCount: 9999, NativeSemanticsSupport: false},
	}
	attribution := claudeadapter.AttributeSkillCost(observations)
	entry, ok := attribution["code-reviewer"]
	if !ok {
		t.Fatal("a call without native cost semantics is still counted as an observation")
	}
	if entry.TotalTokens != 0 {
		t.Fatalf("token/cost must never be attributed without native source semantics support, got %d", entry.TotalTokens)
	}
	if entry.ObservationCount != 1 {
		t.Fatal("the observation itself is still recorded even though it contributes no cost/token figures")
	}
}

func TestAttributeSkillCostIsDeterministicRegardlessOfInputOrder(t *testing.T) {
	a := []claudeadapter.SkillCallObservation{
		{EventIdentity: "id-1", Source: claudeadapter.ReconSourceOTel, SkillID: "skill-a", TokenCount: 10, NativeSemanticsSupport: true},
		{EventIdentity: "id-1", Source: claudeadapter.ReconSourceHook, SkillID: "skill-a"},
		{EventIdentity: "id-2", Source: claudeadapter.ReconSourceOTel, SkillID: "skill-a", TokenCount: 20, NativeSemanticsSupport: true},
	}
	b := []claudeadapter.SkillCallObservation{a[2], a[1], a[0]}

	first := claudeadapter.AttributeSkillCost(a)
	second := claudeadapter.AttributeSkillCost(b)
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("AttributeSkillCost must be deterministic regardless of input observation order")
	}
}

func TestAttributeSkillCostIgnoresObservationsMissingSkillOrEventIdentity(t *testing.T) {
	observations := []claudeadapter.SkillCallObservation{
		{EventIdentity: "", Source: claudeadapter.ReconSourceOTel, SkillID: "skill-a", TokenCount: 100, NativeSemanticsSupport: true},
		{EventIdentity: "id-1", Source: claudeadapter.ReconSourceOTel, SkillID: "", TokenCount: 100, NativeSemanticsSupport: true},
	}
	attribution := claudeadapter.AttributeSkillCost(observations)
	if len(attribution) != 0 {
		t.Fatalf("observations missing SkillID or EventIdentity must never contribute an attribution entry, got %+v", attribution)
	}
}
