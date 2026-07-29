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
)

// This file proves TDD 11 Section C's Gap C requirement for the Codex
// adapter: Inventory must perform a real, bounded host filesystem scan of
// config.toml's [mcp_servers.<name>] tables through adaptersdk.HostView --
// never just forward an installation-scoped empty InventoryInput -- and must
// report Completeness="unknown" (not a fabricated "complete") when a scan
// genuinely observed zero components.

func testInventoryScanPseudonymKey() []byte {
	return []byte("codexadapter-inventoryscan-test-key-0123456789")
}

func writeConfigToml(t *testing.T, stateRoot, content string) {
	t.Helper()
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateRoot, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryScansRealMCPServersFromConfigTomlThroughHostView(t *testing.T) {
	stateRoot := t.TempDir()
	writeConfigToml(t, stateRoot, `
[mcp_servers.alpha]
command = "alpha-server"
enabled = true

[mcp_servers.alpha.env]
API_KEY = "should-never-be-read"

[mcp_servers."beta-server"]
command = "beta"
enabled = false
`)
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	adapter := codexadapter.New()
	target := adaptersdk.Installation{InstallationID: "inst-scan-1", AdapterID: codexadapter.AdapterID, StateRoot: stateRoot}
	snapshot, err := adapter.Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}

	var sawAlphaEnabled, sawBetaDisabled bool
	for _, node := range snapshot.Nodes {
		if node.Kind != adaptersdk.NodeMCPServerInstance {
			continue
		}
		switch node.DeclaredName {
		case "alpha":
			sawAlphaEnabled = true
		case "beta-server":
			sawBetaDisabled = true
		}
		if node.PathPseudonym == "" {
			t.Fatal("mcp_server_instance node must carry a path pseudonym, never a raw path")
		}
		if strings.Contains(node.PathPseudonym, stateRoot) {
			t.Fatal("path pseudonym must never contain the raw scanned path")
		}
	}
	if !sawAlphaEnabled {
		t.Fatal("expected an mcp_server_instance node named alpha from the real config.toml scan")
	}
	if !sawBetaDisabled {
		t.Fatal("expected an mcp_server_instance node named beta-server from the real config.toml scan")
	}

	scope := adaptersdk.ReconcileScope{InstallationID: target.InstallationID}
	result := adapter.Reconcile(context.Background(), scope, adaptersdk.InventorySnapshot{}, snapshot)
	if result.Completeness != "complete" {
		t.Fatalf("a snapshot with real scanned MCP server nodes must reconcile as complete, got %q", result.Completeness)
	}
}

// TestInventoryReportsUnknownCompletenessWhenNoMCPServersConfigured proves
// finding #5's "unknown is not zero" requirement: a real scan of a
// config.toml with zero [mcp_servers.*] tables must not be silently
// reported as a "complete" empty inventory.
func TestInventoryReportsUnknownCompletenessWhenNoMCPServersConfigured(t *testing.T) {
	stateRoot := t.TempDir()
	writeConfigToml(t, stateRoot, "# no mcp servers configured\n")
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	adapter := codexadapter.New()
	target := adaptersdk.Installation{InstallationID: "inst-scan-2", AdapterID: codexadapter.AdapterID, StateRoot: stateRoot}
	snapshot, err := adapter.Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}
	scope := adaptersdk.ReconcileScope{InstallationID: target.InstallationID}
	result := adapter.Reconcile(context.Background(), scope, adaptersdk.InventorySnapshot{}, snapshot)
	if result.Completeness != "unknown" {
		t.Fatalf("genuinely zero configured MCP servers must report Completeness=unknown, not a fabricated complete, got %q", result.Completeness)
	}
}

// TestInventoryReportsUnknownCompletenessWhenHostViewIsNil proves the "no
// scan possible" branch (no HostView available at all) also never
// fabricates a "complete" empty inventory.
func TestInventoryReportsUnknownCompletenessWhenHostViewIsNil(t *testing.T) {
	adapter := codexadapter.New()
	target := adaptersdk.Installation{InstallationID: "inst-scan-3", AdapterID: codexadapter.AdapterID, StateRoot: "/tmp/does-not-matter"}
	snapshot, err := adapter.Inventory(context.Background(), target, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := adaptersdk.ReconcileScope{InstallationID: target.InstallationID}
	result := adapter.Reconcile(context.Background(), scope, adaptersdk.InventorySnapshot{}, snapshot)
	if result.Completeness != "unknown" {
		t.Fatalf("a nil HostView means no scan happened at all; must report unknown, got %q", result.Completeness)
	}
}

func TestInventoryScansMirroredSkillsPluginsAndMCPWithoutPersistingContent(t *testing.T) {
	stateRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stateRoot, "state", "personal"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateRoot, "skills", "user", "safe-canary"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `
[plugins."github@openai-curated"]
enabled = false

[plugins."canary@local"]
enabled = true

[mcp_servers.kansoku-do-nothing]
enabled = true
`
	if err := os.WriteFile(filepath.Join(stateRoot, "state", "personal", "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	skill := `---
name: kansoku-noop-canary
description: harmless secret-shaped text sk-live-value-must-not-persist
---
Never execute anything.
`
	if err := os.WriteFile(filepath.Join(stateRoot, "skills", "user", "safe-canary", "SKILL.md"), []byte(skill), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{
		InstallationID: "ain_fixture", AdapterID: codexadapter.AdapterID,
		SurfaceID: "cli", StateRoot: stateRoot,
	}
	snapshot, err := codexadapter.New().Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	if strings.Contains(serialized, "sk-live-value-must-not-persist") || strings.Contains(serialized, stateRoot) {
		t.Fatal("raw skill content or filesystem path reached the inventory snapshot")
	}
	nodeByName := map[string]adaptersdk.Node{}
	for _, node := range snapshot.Nodes {
		nodeByName[node.DeclaredName] = node
	}
	if nodeByName["kansoku-noop-canary"].Kind != adaptersdk.NodeSkillIdentity {
		t.Fatalf("missing skill node: %+v", snapshot.Nodes)
	}
	if nodeByName["github@openai-curated"].Kind != adaptersdk.NodePluginPackage ||
		nodeByName["canary@local"].Kind != adaptersdk.NodePluginPackage {
		t.Fatalf("missing plugin nodes: %+v", snapshot.Nodes)
	}
	if nodeByName["kansoku-do-nothing"].Kind != adaptersdk.NodeMCPServerInstance {
		t.Fatalf("missing MCP node: %+v", snapshot.Nodes)
	}
	enabled := map[string]bool{}
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeEnabledFor {
			enabled[edge.FromNode] = true
		}
	}
	if enabled[nodeByName["github@openai-curated"].NodeID] {
		t.Fatal("disabled plugin must remain installed but not enabled")
	}
	if !enabled[nodeByName["canary@local"].NodeID] ||
		!enabled[nodeByName["kansoku-noop-canary"].NodeID] ||
		!enabled[nodeByName["kansoku-do-nothing"].NodeID] {
		t.Fatal("enabled inventory components are missing enabled_for edges")
	}
}

// TestScanHostInventoryNeverReadsBeneathNestedEnvSubtable proves the parser
// deliberately does not descend into [mcp_servers.<name>.env] -- no
// credential-shaped value from that subtable is ever readable via the
// returned InventoryInput/InventorySnapshot.
func TestScanHostInventoryNeverReadsBeneathNestedEnvSubtable(t *testing.T) {
	stateRoot := t.TempDir()
	writeConfigToml(t, stateRoot, `
[mcp_servers.gamma]
enabled = true

[mcp_servers.gamma.env]
SECRET_TOKEN = "super-secret-value-must-never-leak"
`)
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{InstallationID: "inst-scan-4", StateRoot: stateRoot}
	input, scanned := codexadapter.ScanHostInventory(host, target)
	if !scanned {
		t.Fatal("expected scan to complete against a real, existing config.toml")
	}
	if len(input.MCPServers) != 1 || input.MCPServers[0].Name != "gamma" {
		t.Fatalf("expected exactly one mcp server named gamma, got %+v", input.MCPServers)
	}
	snapshot, err := codexadapter.BuildInventorySnapshot(input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "super-secret-value-must-never-leak") {
		t.Fatal("a credential value from a nested env subtable must never reach the inventory snapshot")
	}
}

// TestScanHostInventoryReturnsNotScannedWhenConfigFileAbsent proves the
// "we looked and the file simply doesn't exist" branch is distinguished
// from "we looked and found zero servers" only by scanned=false/true --
// InventoryInput itself never conflates the two.
func TestScanHostInventoryReturnsNotScannedWhenConfigFileAbsent(t *testing.T) {
	stateRoot := t.TempDir()
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{InstallationID: "inst-scan-5", StateRoot: stateRoot}
	input, scanned := codexadapter.ScanHostInventory(host, target)
	if scanned {
		t.Fatal("a missing config.toml must not be reported as a completed scan")
	}
	if len(input.MCPServers) != 0 {
		t.Fatal("a missing config.toml must never fabricate MCP server entries")
	}
}

func TestInventoryScansConfiguredPluginCacheSkillsWithQualifiedOwnership(t *testing.T) {
	stateRoot := t.TempDir()
	home := filepath.Join(stateRoot, "state", "personal")
	writeConfigToml(t, home, `
[plugins."sre-agent@yuzuru-engineering"]
enabled = true
`)
	activeSkillDir := filepath.Join(
		home, "plugins", "cache", "yuzuru-engineering", "sre-agent", "0.1.0",
		"skills", "sre-agent",
	)
	cacheOnlySkillDir := filepath.Join(
		home, "plugins", "cache", "marketplace-cache", "cached-plugin", "9.9.9",
		"skills", "cached-skill",
	)
	for _, directory := range []string{activeSkillDir, cacheOnlySkillDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(activeSkillDir, "SKILL.md"), []byte(`---
name: sre-agent
description: metadata-only KANSOKU_PLUGIN_SKILL_SECRET_MUST_NOT_PERSIST
---
KANSOKU_PLUGIN_SKILL_BODY_MUST_NOT_PERSIST
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheOnlySkillDir, "SKILL.md"), []byte(`---
name: cached-skill
description: cached metadata
---
`), 0o600); err != nil {
		t.Fatal(err)
	}

	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{
		InstallationID: "ain_plugin_catalog", AdapterID: codexadapter.AdapterID,
		SurfaceID: "cli", StateRoot: stateRoot,
	}
	snapshot, err := codexadapter.New().Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(encoded)
	for _, forbidden := range []string{
		stateRoot,
		"KANSOKU_PLUGIN_SKILL_SECRET_MUST_NOT_PERSIST",
		"KANSOKU_PLUGIN_SKILL_BODY_MUST_NOT_PERSIST",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("raw plugin catalog surface persisted: %q", forbidden)
		}
	}

	nodeByID := make(map[string]adaptersdk.Node, len(snapshot.Nodes))
	var pluginNode, skillNode, cacheNode, cacheSkillNode adaptersdk.Node
	for _, node := range snapshot.Nodes {
		nodeByID[node.NodeID] = node
		switch {
		case node.DeclaredName == "sre-agent@yuzuru-engineering" &&
			node.Kind == adaptersdk.NodePluginPackage:
			pluginNode = node
		case node.DeclaredName == "sre-agent" &&
			node.Kind == adaptersdk.NodeSkillIdentity:
			skillNode = node
		case node.DeclaredName == "cached-plugin@marketplace-cache":
			cacheNode = node
		case node.DeclaredName == "cached-skill":
			cacheSkillNode = node
		}
	}
	if pluginNode.NodeID == "" || pluginNode.Version != "0.1.0" ||
		pluginNode.SourceScope != adaptersdk.ScopeMarketplace || pluginNode.CachedOnly {
		t.Fatalf("configured plugin cache metadata was not promoted exactly: %+v", pluginNode)
	}
	if skillNode.NodeID == "" || skillNode.SourceScope != adaptersdk.ScopeMarketplace ||
		skillNode.CachedOnly {
		t.Fatalf("configured plugin skill metadata was not promoted exactly: %+v", skillNode)
	}
	if cacheNode.Kind != adaptersdk.NodeCacheArtifact || !cacheNode.CachedOnly ||
		!cacheSkillNode.CachedOnly {
		t.Fatalf("cache-only plugin/child must remain cache artifacts: plugin=%+v child=%+v", cacheNode, cacheSkillNode)
	}

	var bundled, pluginEnabled, skillEnabled, cacheEnabled bool
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeBundles &&
			edge.FromNode == pluginNode.NodeID && edge.ToNode == skillNode.NodeID {
			bundled = true
		}
		if edge.Kind == adaptersdk.EdgeEnabledFor {
			switch edge.FromNode {
			case pluginNode.NodeID:
				pluginEnabled = true
			case skillNode.NodeID:
				skillEnabled = true
			case cacheNode.NodeID, cacheSkillNode.NodeID:
				cacheEnabled = true
			}
		}
	}
	if !bundled || !pluginEnabled || !skillEnabled {
		t.Fatalf(
			"configured plugin ownership/enabled graph incomplete: bundled=%t plugin=%t skill=%t",
			bundled, pluginEnabled, skillEnabled,
		)
	}
	if cacheEnabled {
		t.Fatal("cache presence alone must never create enabled_for")
	}
}

func TestInventoryDoesNotGuessConfiguredPluginVersionWhenCacheIsAmbiguous(t *testing.T) {
	stateRoot := t.TempDir()
	home := filepath.Join(stateRoot, "state", "personal")
	writeConfigToml(t, home, `
[plugins."ambiguous@yuzuru-engineering"]
enabled = true
`)
	for _, version := range []string{"1.0.0", "2.0.0"} {
		directory := filepath.Join(
			home, "plugins", "cache", "yuzuru-engineering", "ambiguous", version,
			"skills", "same-skill",
		)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(`---
name: same-skill
description: ambiguous cache version
---
`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{
		InstallationID: "ain_plugin_ambiguous", AdapterID: codexadapter.AdapterID,
		StateRoot: stateRoot,
	}
	snapshot, err := codexadapter.New().Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}
	var configured adaptersdk.Node
	var cacheVersions int
	var configuredBundles int
	for _, node := range snapshot.Nodes {
		if node.DeclaredName != "ambiguous@yuzuru-engineering" {
			continue
		}
		if node.Kind == adaptersdk.NodePluginPackage {
			configured = node
		}
		if node.Kind == adaptersdk.NodeCacheArtifact {
			cacheVersions++
		}
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeBundles && edge.FromNode == configured.NodeID {
			configuredBundles++
		}
	}
	if configured.NodeID == "" || configured.Version != "" ||
		configured.SourceScope != adaptersdk.ScopeUser {
		t.Fatalf("ambiguous cache must not be selected as configured version: %+v", configured)
	}
	if cacheVersions != 2 || configuredBundles != 0 {
		t.Fatalf(
			"ambiguous versions must remain two cache artifacts without guessed ownership: cache=%d bundles=%d",
			cacheVersions, configuredBundles,
		)
	}
}

func TestInventoryDeduplicatesOnlyExactPluginCatalogsAcrossProfiles(t *testing.T) {
	stateRoot := t.TempDir()
	writeProfile := func(profile, description string) {
		t.Helper()
		home := filepath.Join(stateRoot, "state", profile)
		writeConfigToml(t, home, `
[plugins."shared@yuzuru-engineering"]
enabled = true
`)
		skillDir := filepath.Join(
			home, "plugins", "cache", "yuzuru-engineering", "shared", "1.0.0",
			"skills", "shared-skill",
		)
		if err := os.MkdirAll(skillDir, 0o700); err != nil {
			t.Fatal(err)
		}
		manifest := "---\nname: shared-skill\ndescription: " + description + "\n---\n"
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeProfile("personal", "identical")
	writeProfile("corporate", "identical")
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{
		InstallationID: "ain_shared_profiles", AdapterID: codexadapter.AdapterID,
		StateRoot: stateRoot,
	}
	snapshot, err := codexadapter.New().Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}
	var plugins, skills, collisions int
	for _, node := range snapshot.Nodes {
		if node.DeclaredName == "shared@yuzuru-engineering" &&
			node.Kind == adaptersdk.NodePluginPackage {
			plugins++
		}
		if node.DeclaredName == "shared-skill" &&
			node.Kind == adaptersdk.NodeSkillIdentity {
			skills++
		}
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeCollidesWith {
			collisions++
		}
	}
	if plugins != 1 || skills != 1 || collisions != 0 {
		t.Fatalf(
			"identical profile catalogs must be one logical bundle: plugins=%d skills=%d collisions=%d",
			plugins, skills, collisions,
		)
	}

	writeProfile("corporate", "different-content")
	divergent, err := codexadapter.New().Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}
	plugins, skills, collisions = 0, 0, 0
	for _, node := range divergent.Nodes {
		if node.DeclaredName == "shared@yuzuru-engineering" &&
			node.Kind == adaptersdk.NodePluginPackage {
			plugins++
		}
		if node.DeclaredName == "shared-skill" &&
			node.Kind == adaptersdk.NodeSkillIdentity {
			skills++
		}
	}
	for _, edge := range divergent.Edges {
		if edge.Kind == adaptersdk.EdgeCollidesWith {
			collisions++
		}
	}
	if plugins != 2 || skills != 2 || collisions < 2 {
		t.Fatalf(
			"divergent profile catalogs must remain colliding candidates: plugins=%d skills=%d collisions=%d",
			plugins, skills, collisions,
		)
	}
}
