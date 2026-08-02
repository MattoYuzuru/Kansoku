package claudeadapter_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/claudeadapter"
)

// This file proves TDD 11 Section C's Gap C requirement for the Claude
// adapter: Inventory must perform a real, bounded host filesystem scan of
// settings.json's enabledPlugins/mcpServers keys through adaptersdk.HostView
// -- never just forward an installation-scoped empty InventoryInput -- and
// must report Completeness="unknown" (not a fabricated "complete") when a
// scan genuinely observed zero components.

func testInventoryScanPseudonymKey() []byte {
	return []byte("claudeadapter-inventoryscan-test-key-0123456789")
}

func writeSettingsJSON(t *testing.T, stateRoot, content string) {
	t.Helper()
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateRoot, "settings.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSkillManifest(t *testing.T, dir, name, description string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\nNever execute anything.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writePluginCacheVersion writes one version-hash directory's
// .claude-plugin/plugin.json under <stateRoot>/plugins/cache/<marketplace>/
// <pluginFolder>/<versionHash>/, mirroring Claude Code's real on-disk plugin
// cache layout. Pass declaredName == "" to omit plugin.json entirely, so a
// test can exercise the folder-name fallback.
func writePluginCacheVersion(t *testing.T, stateRoot, marketplace, pluginFolder, versionHash, declaredName string) string {
	t.Helper()
	versionDir := filepath.Join(stateRoot, "plugins", "cache", marketplace, pluginFolder, versionHash)
	if err := os.MkdirAll(versionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if declaredName != "" {
		manifestDir := filepath.Join(versionDir, ".claude-plugin")
		if err := os.MkdirAll(manifestDir, 0o700); err != nil {
			t.Fatal(err)
		}
		content := `{"name": "` + declaredName + `", "description": "irrelevant"}`
		if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return versionDir
}

func writeInstalledPlugins(t *testing.T, stateRoot string, activeVersions map[string]string) {
	t.Helper()
	pluginsDir := filepath.Join(stateRoot, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	entries := make(map[string][]map[string]string, len(activeVersions))
	for name, version := range activeVersions {
		entries[name] = []map[string]string{{"version": version}}
	}
	payload := map[string]any{"version": 2, "plugins": entries}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryScansRealPluginsAndMCPServersFromSettingsJSONThroughHostView(t *testing.T) {
	stateRoot := t.TempDir()
	writeSettingsJSON(t, stateRoot, `{
		"enabledPlugins": {
			"formatter-plugin@example-marketplace": true,
			"disabled-plugin@example-marketplace": false
		},
		"mcpServers": {
			"alpha": {"command": "alpha-server", "env": {"API_KEY": "should-never-be-read"}},
			"beta": {"command": "beta-server", "disabled": true}
		}
	}`)
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	adapter := claudeadapter.New()
	target := adaptersdk.Installation{InstallationID: "inst-scan-1", AdapterID: claudeadapter.AdapterID, StateRoot: stateRoot}
	snapshot, err := adapter.Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}

	var sawEnabledPlugin, sawDisabledPlugin, sawAlphaMCP, sawBetaMCP bool
	for _, node := range snapshot.Nodes {
		if node.PathPseudonym != "" && strings.Contains(node.PathPseudonym, stateRoot) {
			t.Fatal("path pseudonym must never contain the raw scanned path")
		}
		switch node.Kind {
		case adaptersdk.NodePluginPackage:
			switch node.DeclaredName {
			case "formatter-plugin@example-marketplace":
				sawEnabledPlugin = true
			case "disabled-plugin@example-marketplace":
				sawDisabledPlugin = true
			}
		case adaptersdk.NodeMCPServerInstance:
			switch node.DeclaredName {
			case "alpha":
				sawAlphaMCP = true
			case "beta":
				sawBetaMCP = true
			}
		}
	}
	if !sawEnabledPlugin {
		t.Fatal("expected a plugin_package node for the enabled plugin from the real settings.json scan")
	}
	if !sawDisabledPlugin {
		t.Fatal("expected a plugin_package node for the disabled plugin too (presence, not just enabled ones)")
	}
	if !sawAlphaMCP || !sawBetaMCP {
		t.Fatal("expected mcp_server_instance nodes for both alpha and beta from the real settings.json scan")
	}

	scope := adaptersdk.ReconcileScope{InstallationID: target.InstallationID}
	result := adapter.Reconcile(context.Background(), scope, adaptersdk.InventorySnapshot{}, snapshot)
	if result.Completeness != "complete" {
		t.Fatalf("a snapshot with real scanned plugin/MCP nodes must reconcile as complete, got %q", result.Completeness)
	}
}

// TestInventoryReportsUnknownCompletenessWhenNothingConfigured proves
// finding #5's "unknown is not zero" requirement: a real scan of a
// settings.json with empty enabledPlugins/mcpServers maps must not be
// silently reported as a "complete" empty inventory.
func TestInventoryReportsUnknownCompletenessWhenNothingConfigured(t *testing.T) {
	stateRoot := t.TempDir()
	writeSettingsJSON(t, stateRoot, `{"enabledPlugins": {}, "mcpServers": {}}`)
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	adapter := claudeadapter.New()
	target := adaptersdk.Installation{InstallationID: "inst-scan-2", AdapterID: claudeadapter.AdapterID, StateRoot: stateRoot}
	snapshot, err := adapter.Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}
	scope := adaptersdk.ReconcileScope{InstallationID: target.InstallationID}
	result := adapter.Reconcile(context.Background(), scope, adaptersdk.InventorySnapshot{}, snapshot)
	if result.Completeness != "unknown" {
		t.Fatalf("genuinely zero configured plugins/MCP servers must report Completeness=unknown, not a fabricated complete, got %q", result.Completeness)
	}
}

// TestInventoryReportsUnknownCompletenessWhenHostViewIsNil proves the "no
// scan possible" branch (no HostView available at all) also never
// fabricates a "complete" empty inventory.
func TestInventoryReportsUnknownCompletenessWhenHostViewIsNil(t *testing.T) {
	adapter := claudeadapter.New()
	target := adaptersdk.Installation{InstallationID: "inst-scan-3", AdapterID: claudeadapter.AdapterID, StateRoot: "/tmp/does-not-matter"}
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

// TestScanHostInventoryNeverReadsMCPServerCommandOrEnv proves the scan
// deliberately decodes only presence/disabled from each mcpServers entry --
// no command/args/env value (which may carry a credential) is ever readable
// via the returned InventoryInput/InventorySnapshot.
func TestScanHostInventoryNeverReadsMCPServerCommandOrEnv(t *testing.T) {
	stateRoot := t.TempDir()
	writeSettingsJSON(t, stateRoot, `{
		"mcpServers": {
			"gamma": {"command": "gamma-server", "args": ["--token", "abc"], "env": {"SECRET_TOKEN": "super-secret-value-must-never-leak"}}
		}
	}`)
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{InstallationID: "inst-scan-4", StateRoot: stateRoot}
	input, scanned := claudeadapter.ScanHostInventory(host, target)
	if !scanned {
		t.Fatal("expected scan to complete against a real, existing settings.json")
	}
	if len(input.StandaloneMCPServers) != 1 || input.StandaloneMCPServers[0].Name != "gamma" {
		t.Fatalf("expected exactly one mcp server named gamma, got %+v", input.StandaloneMCPServers)
	}
	snapshot, err := claudeadapter.BuildInventorySnapshot(input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "super-secret-value-must-never-leak") {
		t.Fatal("a credential value from mcpServers.env must never reach the inventory snapshot")
	}
}

// TestScanHostInventoryReturnsNotScannedWhenSettingsFileAbsent proves the
// "we looked and the file simply doesn't exist" branch is distinguished
// from "we looked and found zero entries" only by scanned=false/true --
// InventoryInput itself never conflates the two.
func TestScanHostInventoryReturnsNotScannedWhenSettingsFileAbsent(t *testing.T) {
	stateRoot := t.TempDir()
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{InstallationID: "inst-scan-5", StateRoot: stateRoot}
	input, scanned := claudeadapter.ScanHostInventory(host, target)
	if scanned {
		t.Fatal("a missing settings.json must not be reported as a completed scan")
	}
	if len(input.Plugins) != 0 || len(input.StandaloneMCPServers) != 0 {
		t.Fatal("a missing settings.json must never fabricate plugin or MCP server entries")
	}
}

// TestInventoryScansSkillsAcrossAllDocumentedScopesFromHostView proves the
// scanner walks each of Claude Code's documented skill roots (user,
// repository, system) and assigns each skill the corresponding source scope,
// mirroring codexadapter's identical scope set.
func TestInventoryScansSkillsAcrossAllDocumentedScopesFromHostView(t *testing.T) {
	stateRoot := t.TempDir()
	writeSkillManifest(t, filepath.Join(stateRoot, "skills", "user", "user-skill"), "user-skill", "a user-scoped skill")
	writeSkillManifest(t, filepath.Join(stateRoot, "skills", "repository", "repo-skill"), "repo-skill", "a repository-scoped skill")
	writeSkillManifest(t, filepath.Join(stateRoot, "skills", "system", "system-skill"), "system-skill", "a system-scoped skill")
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	adapter := claudeadapter.New()
	target := adaptersdk.Installation{InstallationID: "inst-scan-skills-1", AdapterID: claudeadapter.AdapterID, StateRoot: stateRoot}
	snapshot, err := adapter.Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}
	nodeByName := map[string]adaptersdk.Node{}
	for _, node := range snapshot.Nodes {
		if node.Kind == adaptersdk.NodeSkillIdentity {
			nodeByName[node.DeclaredName] = node
		}
	}
	if nodeByName["user-skill"].SourceScope != adaptersdk.ScopeUser {
		t.Fatalf("expected user-skill scoped as user, got %+v", nodeByName["user-skill"])
	}
	if nodeByName["repo-skill"].SourceScope != adaptersdk.ScopeRepository {
		t.Fatalf("expected repo-skill scoped as repository, got %+v", nodeByName["repo-skill"])
	}
	if nodeByName["system-skill"].SourceScope != adaptersdk.ScopeSystem {
		t.Fatalf("expected system-skill scoped as system, got %+v", nodeByName["system-skill"])
	}
	enabled := map[string]bool{}
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeEnabledFor {
			enabled[edge.FromNode] = true
		}
	}
	for _, name := range []string{"user-skill", "repo-skill", "system-skill"} {
		if !enabled[nodeByName[name].NodeID] {
			t.Fatalf("expected %s to have an enabled_for edge", name)
		}
	}
}

// TestInventoryScansSkillsPluginsAndMCPWithoutPersistingContent mirrors
// codexadapter's identical test: real settings.json plugins/MCP servers and
// a real skill manifest with a secret-shaped canary description must all
// surface as inventory nodes, but neither the secret text nor the raw scanned
// path may ever reach the serialized snapshot.
func TestInventoryScansSkillsPluginsAndMCPWithoutPersistingContent(t *testing.T) {
	stateRoot := t.TempDir()
	writeSettingsJSON(t, stateRoot, `{
		"enabledPlugins": {"formatter-plugin@example-marketplace": true},
		"mcpServers": {"alpha": {"command": "alpha-server"}}
	}`)
	writeSkillManifest(t, filepath.Join(stateRoot, "skills", "user", "safe-canary"),
		"kansoku-noop-canary", "harmless secret-shaped text sk-live-value-must-not-persist")
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{
		InstallationID: "inst-scan-skills-2", AdapterID: claudeadapter.AdapterID,
		SurfaceID: "cli", StateRoot: stateRoot,
	}
	snapshot, err := claudeadapter.New().Inventory(context.Background(), target, host)
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
	if nodeByName["formatter-plugin@example-marketplace"].Kind != adaptersdk.NodePluginPackage {
		t.Fatalf("missing plugin node: %+v", snapshot.Nodes)
	}
	if nodeByName["alpha"].Kind != adaptersdk.NodeMCPServerInstance {
		t.Fatalf("missing MCP node: %+v", snapshot.Nodes)
	}
}

// TestScanHostInventoryScansSkillsEvenWhenSettingsJSONAbsent proves the
// restructured ScanHostInventory: a missing/unreadable settings.json must
// not hide skills that live entirely in their own directories.
func TestScanHostInventoryScansSkillsEvenWhenSettingsJSONAbsent(t *testing.T) {
	stateRoot := t.TempDir()
	writeSkillManifest(t, filepath.Join(stateRoot, "skills", "user", "lonely-skill"),
		"lonely-skill", "exists without any settings.json")
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{InstallationID: "inst-scan-6", StateRoot: stateRoot}
	input, scanned := claudeadapter.ScanHostInventory(host, target)
	if !scanned {
		t.Fatal("expected scan to be reported as completed because the skill root scan observed real content, even though settings.json is absent")
	}
	if len(input.StandaloneSkills) != 1 || input.StandaloneSkills[0].Name != "lonely-skill" {
		t.Fatalf("expected exactly one skill named lonely-skill, got %+v", input.StandaloneSkills)
	}
}

// TestScanPluginCacheDiscoversArbitraryMarketplacesAndPluginsGenerically
// proves the scan performs genuine dynamic discovery -- made-up marketplace/
// plugin/skill names never referenced by any literal string in the scanner
// itself -- rather than hardcoding any specific structure (the user's
// explicit requirement: a real host may have many such structures).
func TestScanPluginCacheDiscoversArbitraryMarketplacesAndPluginsGenerically(t *testing.T) {
	stateRoot := t.TempDir()
	firstDir := writePluginCacheVersion(t, stateRoot, "quasar-bazaar", "widget-forge", "v1-hash", "widget-forge")
	writeSkillManifest(t, filepath.Join(firstDir, "skills", "widget-skill"), "widget-skill", "bundled by widget-forge")
	writePluginCacheVersion(t, stateRoot, "nebula-exchange", "gizmo-kit", "v2-hash", "gizmo-kit")
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{InstallationID: "inst-cache-1", StateRoot: stateRoot}
	input, scanned := claudeadapter.ScanHostInventory(host, target)
	if !scanned {
		t.Fatal("expected the plugin cache scan to be reported as completed")
	}
	pluginNames := map[string]claudeadapter.PluginDescriptor{}
	for _, plugin := range input.Plugins {
		pluginNames[plugin.Name] = plugin
	}
	widget, ok := pluginNames["widget-forge@quasar-bazaar"]
	if !ok {
		t.Fatalf("expected a discovered plugin named widget-forge@quasar-bazaar, got %+v", input.Plugins)
	}
	if len(widget.BundledSkills) != 1 || widget.BundledSkills[0].Name != "widget-skill" {
		t.Fatalf("expected widget-forge's bundled skill to be discovered, got %+v", widget.BundledSkills)
	}
	if _, ok := pluginNames["gizmo-kit@nebula-exchange"]; !ok {
		t.Fatalf("expected a discovered plugin named gizmo-kit@nebula-exchange, got %+v", input.Plugins)
	}
	marketNames := map[string]bool{}
	for _, marketplace := range input.Marketplaces {
		marketNames[marketplace.Name] = true
	}
	if !marketNames["quasar-bazaar"] || !marketNames["nebula-exchange"] {
		t.Fatalf("expected both discovered marketplaces present, got %+v", input.Marketplaces)
	}
}

// TestScanPluginCacheMergesIntoConfiguredPluginWithoutCollision proves the
// "merge, don't duplicate" design: a plugin already named in settings.json's
// enabledPlugins gets enriched by its matching cache entry (BundledSkills,
// Version, FromMarketplace) as a single node, never a second, colliding one.
func TestScanPluginCacheMergesIntoConfiguredPluginWithoutCollision(t *testing.T) {
	stateRoot := t.TempDir()
	writeSettingsJSON(t, stateRoot, `{"enabledPlugins": {"formatter-plugin@acme-market": true}}`)
	versionDir := writePluginCacheVersion(t, stateRoot, "acme-market", "formatter-plugin", "hash-1", "formatter-plugin")
	writeSkillManifest(t, filepath.Join(versionDir, "skills", "format-skill"), "format-skill", "bundled formatter skill")
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	adapter := claudeadapter.New()
	target := adaptersdk.Installation{InstallationID: "inst-cache-2", AdapterID: claudeadapter.AdapterID, StateRoot: stateRoot}
	snapshot, err := adapter.Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}

	var pluginNodes []adaptersdk.Node
	var bundledSkill *adaptersdk.Node
	for i, node := range snapshot.Nodes {
		if node.DeclaredName == "formatter-plugin@acme-market" {
			pluginNodes = append(pluginNodes, node)
		}
		if node.Kind == adaptersdk.NodeSkillIdentity && node.DeclaredName == "format-skill" {
			bundledSkill = &snapshot.Nodes[i]
		}
	}
	if len(pluginNodes) != 1 {
		t.Fatalf("expected exactly one merged plugin node, got %d: %+v", len(pluginNodes), pluginNodes)
	}
	if pluginNodes[0].Kind != adaptersdk.NodePluginPackage || pluginNodes[0].CachedOnly {
		t.Fatalf("merged plugin must remain a configured plugin_package, not CachedOnly, got %+v", pluginNodes[0])
	}
	if bundledSkill == nil {
		t.Fatal("expected the cache-discovered bundled skill to appear in the merged plugin's inventory")
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeCollidesWith && (edge.FromNode == pluginNodes[0].NodeID || edge.ToNode == pluginNodes[0].NodeID) {
			t.Fatal("a plugin enriched by its own matching cache entry must never collide with itself")
		}
		if edge.Kind == adaptersdk.EdgeEnabledFor && edge.FromNode == pluginNodes[0].NodeID {
			// expected: settings.json marked it enabled
		}
	}
}

// TestScanPluginCachePluginAbsentFromSettingsBecomesCacheOnly proves a
// plugin discovered only in the cache (never named in settings.json's
// enabledPlugins) surfaces as a CachedOnly cache_artifact, per the
// cache_rule -- presence in cache alone is never treated as "installed".
func TestScanPluginCachePluginAbsentFromSettingsBecomesCacheOnly(t *testing.T) {
	stateRoot := t.TempDir()
	writePluginCacheVersion(t, stateRoot, "acme-market", "unconfigured-plugin", "hash-1", "unconfigured-plugin")
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	adapter := claudeadapter.New()
	target := adaptersdk.Installation{InstallationID: "inst-cache-3", AdapterID: claudeadapter.AdapterID, StateRoot: stateRoot}
	snapshot, err := adapter.Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}
	var found *adaptersdk.Node
	for i, node := range snapshot.Nodes {
		if node.DeclaredName == "unconfigured-plugin@acme-market" {
			found = &snapshot.Nodes[i]
		}
	}
	if found == nil {
		t.Fatal("expected a cache-only plugin node to appear")
	}
	if found.Kind != adaptersdk.NodeCacheArtifact || !found.CachedOnly {
		t.Fatalf("expected a CachedOnly cache_artifact node, got %+v", found)
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeEnabledFor && edge.FromNode == found.NodeID {
			t.Fatal("a cache-only plugin must never receive an enabled_for edge")
		}
	}
}

// TestScanPluginCacheMultipleVersionsResolvesActiveViaInstalledPluginsJSON
// proves the multi-version disambiguation rule: when installed_plugins.json
// names one of two on-disk version-hash directories as active, exactly that
// one merges into the configured plugin entry, and the other stale version
// remains a separate CachedOnly node colliding with it by name.
func TestScanPluginCacheMultipleVersionsResolvesActiveViaInstalledPluginsJSON(t *testing.T) {
	stateRoot := t.TempDir()
	writeSettingsJSON(t, stateRoot, `{"enabledPlugins": {"multi-version-plugin@acme-market": true}}`)
	writePluginCacheVersion(t, stateRoot, "acme-market", "multi-version-plugin", "hash-old", "multi-version-plugin")
	writePluginCacheVersion(t, stateRoot, "acme-market", "multi-version-plugin", "hash-new", "multi-version-plugin")
	writeInstalledPlugins(t, stateRoot, map[string]string{"multi-version-plugin@acme-market": "hash-new"})
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	adapter := claudeadapter.New()
	target := adaptersdk.Installation{InstallationID: "inst-cache-4", AdapterID: claudeadapter.AdapterID, StateRoot: stateRoot}
	snapshot, err := adapter.Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}
	var matches []adaptersdk.Node
	for _, node := range snapshot.Nodes {
		if node.DeclaredName == "multi-version-plugin@acme-market" {
			matches = append(matches, node)
		}
	}
	if len(matches) != 2 {
		t.Fatalf("expected two distinct nodes (merged-active + stale cache-only), got %d: %+v", len(matches), matches)
	}
	var active, stale *adaptersdk.Node
	for i := range matches {
		if matches[i].Version == "hash-new" {
			active = &matches[i]
		}
		if matches[i].Version == "hash-old" {
			stale = &matches[i]
		}
	}
	if active == nil || stale == nil {
		t.Fatalf("expected one hash-new and one hash-old node, got %+v", matches)
	}
	if active.CachedOnly {
		t.Fatal("the version named active by installed_plugins.json must merge into the configured (non-cache-only) entry")
	}
	if !stale.CachedOnly {
		t.Fatal("the stale, non-active version must remain a separate cache-only entry")
	}
	collided := false
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeCollidesWith &&
			((edge.FromNode == active.NodeID && edge.ToNode == stale.NodeID) ||
				(edge.FromNode == stale.NodeID && edge.ToNode == active.NodeID)) {
			collided = true
		}
	}
	if !collided {
		t.Fatal("expected the active and stale same-named plugin versions to be linked by a collides_with edge")
	}
}

// TestScanPluginCacheMultipleVersionsWithoutInstalledPluginsJSONStaySafe
// proves the safe-degradation rule: with no installed_plugins.json to
// disambiguate, an ambiguous multi-version plugin never guesses which
// version is active -- every version becomes CachedOnly instead of a wrong
// "exact" merge.
func TestScanPluginCacheMultipleVersionsWithoutInstalledPluginsJSONStaySafe(t *testing.T) {
	stateRoot := t.TempDir()
	writeSettingsJSON(t, stateRoot, `{"enabledPlugins": {"ambiguous-plugin@acme-market": true}}`)
	writePluginCacheVersion(t, stateRoot, "acme-market", "ambiguous-plugin", "hash-a", "ambiguous-plugin")
	writePluginCacheVersion(t, stateRoot, "acme-market", "ambiguous-plugin", "hash-b", "ambiguous-plugin")
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{InstallationID: "inst-cache-5", StateRoot: stateRoot}
	input, scanned := claudeadapter.ScanHostInventory(host, target)
	if !scanned {
		t.Fatal("expected the scan to complete")
	}
	var configured *claudeadapter.PluginDescriptor
	cacheOnlyCount := 0
	for i, plugin := range input.Plugins {
		if plugin.Name != "ambiguous-plugin@acme-market" {
			continue
		}
		if plugin.CachedOnly {
			cacheOnlyCount++
			continue
		}
		configured = &input.Plugins[i]
	}
	// With no installed_plugins.json to disambiguate two candidate versions,
	// no cache candidate may merge into the settings.json-configured entry --
	// it must remain exactly as settings.json seeded it (no Version, not
	// CachedOnly), while both ambiguous cache versions surface separately as
	// CachedOnly entries. Fabricating a merge here would be a wrong guess.
	if configured == nil || configured.Version != "" {
		t.Fatalf("expected the configured entry to remain unmerged (no Version), got %+v", configured)
	}
	if cacheOnlyCount != 2 {
		t.Fatalf("expected both ambiguous versions present as separate CachedOnly entries, got %d", cacheOnlyCount)
	}
}

// TestScanPluginCacheNeverPersistsManifestOrSkillContent mirrors the
// existing secret-canary test style: a plugin.json/SKILL.md carrying a
// secret-shaped value must never reach the serialized inventory snapshot,
// and no raw scanned path may leak via PathPseudonym.
func TestScanPluginCacheNeverPersistsManifestOrSkillContent(t *testing.T) {
	stateRoot := t.TempDir()
	writeSettingsJSON(t, stateRoot, `{"enabledPlugins": {"canary-plugin@acme-market": true}}`)
	versionDir := writePluginCacheVersion(t, stateRoot, "acme-market", "canary-plugin", "hash-1", "canary-plugin")
	writeSkillManifest(t, filepath.Join(versionDir, "skills", "canary-skill"),
		"canary-skill", "secret-shaped value sk-live-plugin-cache-must-not-persist")
	host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	adapter := claudeadapter.New()
	target := adaptersdk.Installation{InstallationID: "inst-cache-6", AdapterID: claudeadapter.AdapterID, StateRoot: stateRoot}
	snapshot, err := adapter.Inventory(context.Background(), target, host)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	if strings.Contains(serialized, "sk-live-plugin-cache-must-not-persist") {
		t.Fatal("secret-shaped skill content from the plugin cache must never reach the inventory snapshot")
	}
	if strings.Contains(serialized, stateRoot) {
		t.Fatal("the raw scanned state root path must never reach the inventory snapshot")
	}
	for _, node := range snapshot.Nodes {
		if node.PathPseudonym != "" && strings.Contains(node.PathPseudonym, stateRoot) {
			t.Fatal("path pseudonym must never contain the raw scanned path")
		}
	}
}

// TestScanPluginCacheObservedIndependentlyOfSettingsAndSkillScans proves the
// three scan sources (settings.json, standalone skill roots, plugin cache)
// each independently contribute to "scanned", mirroring
// TestScanHostInventoryScansSkillsEvenWhenSettingsJSONAbsent: an absent
// plugin cache root must not blank out an otherwise-successful settings.json
// scan, and a present plugin cache root must report scanned=true even when
// settings.json and the standalone skill roots are both absent.
func TestScanPluginCacheObservedIndependentlyOfSettingsAndSkillScans(t *testing.T) {
	t.Run("settings.json present, cache absent", func(t *testing.T) {
		stateRoot := t.TempDir()
		writeSettingsJSON(t, stateRoot, `{"enabledPlugins": {}, "mcpServers": {}}`)
		host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
		if err != nil {
			t.Fatal(err)
		}
		target := adaptersdk.Installation{InstallationID: "inst-cache-7a", StateRoot: stateRoot}
		_, scanned := claudeadapter.ScanHostInventory(host, target)
		if !scanned {
			t.Fatal("an absent plugin cache root must not blank out an otherwise-successful settings.json scan")
		}
	})
	t.Run("cache present, settings.json and skill roots absent", func(t *testing.T) {
		stateRoot := t.TempDir()
		writePluginCacheVersion(t, stateRoot, "acme-market", "lonely-cache-plugin", "hash-1", "lonely-cache-plugin")
		host, err := adaptersdk.NewHostView([]string{stateRoot}, nil, testInventoryScanPseudonymKey())
		if err != nil {
			t.Fatal(err)
		}
		target := adaptersdk.Installation{InstallationID: "inst-cache-7b", StateRoot: stateRoot}
		input, scanned := claudeadapter.ScanHostInventory(host, target)
		if !scanned {
			t.Fatal("a present, listable plugin cache root must report scanned=true on its own")
		}
		found := false
		for _, plugin := range input.Plugins {
			if plugin.Name == "lonely-cache-plugin@acme-market" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected the cache-only plugin to be discovered, got %+v", input.Plugins)
		}
	})
}

// TestScanSkillRootRecordsCoverageGapsInsteadOfSkippingSilently pins the
// difference between "this directory holds no skill" and "this skill could not
// be read". Before the tally existed both produced the same empty result, so a
// host whose skill library was symlinked out of the bound tree reported one
// skill out of eight and still called the snapshot complete.
func TestScanSkillRootRecordsCoverageGapsInsteadOfSkippingSilently(t *testing.T) {
	base := t.TempDir()
	library := filepath.Join(base, "library")
	root := filepath.Join(base, "state")
	skillRoot := filepath.Join(root, "skills", "user")
	if err := os.MkdirAll(filepath.Join(library, "linked-skill"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSkillManifest(t, filepath.Join(skillRoot, "real-skill"), "real-skill", "readable")
	// A symlink whose target sits outside the permitted roots: exactly the
	// container case where the link directory is bound but the library is not.
	if err := os.Symlink(filepath.Join(library, "linked-skill"),
		filepath.Join(skillRoot, "linked-skill")); err != nil {
		t.Fatal(err)
	}
	// A manifest that declares no identity.
	if err := os.MkdirAll(filepath.Join(skillRoot, "nameless"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "nameless", "SKILL.md"),
		[]byte("---\ndescription: no name key\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A plain directory that is simply not a skill: not a gap.
	if err := os.MkdirAll(filepath.Join(skillRoot, "not-a-skill"), 0o700); err != nil {
		t.Fatal(err)
	}

	host, err := adaptersdk.NewHostView([]string{root}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	input, scanned := claudeadapter.ScanHostInventory(host, adaptersdk.Installation{
		InstallationID: "ain_coverage_gaps", AdapterID: claudeadapter.AdapterID,
		SurfaceID: "cli", StateRoot: root,
	})
	if !scanned {
		t.Fatal("a readable skill root must report scanned=true")
	}
	names := make([]string, 0, len(input.StandaloneSkills))
	for _, skill := range input.StandaloneSkills {
		names = append(names, skill.Name)
	}
	if len(names) != 1 || names[0] != "real-skill" {
		t.Fatalf("skills=%v want only real-skill", names)
	}
	if got := input.CoverageGaps[adaptersdk.CoverageGapUnresolvableSymlink]; got != 1 {
		t.Fatalf("unresolvable_symlink=%d want 1 (gaps=%v)", got, input.CoverageGaps)
	}
	if got := input.CoverageGaps[adaptersdk.CoverageGapUnparseableManifest]; got != 1 {
		t.Fatalf("unparseable_component_manifest=%d want 1 (gaps=%v)", got, input.CoverageGaps)
	}
	if got := input.CoverageGaps.Total(); got != 2 {
		t.Fatalf("total gaps=%d want 2; a plain non-skill directory must not count (gaps=%v)",
			got, input.CoverageGaps)
	}
}

// TestCleanSkillRootYieldsNoCoverageGapsAndCompleteReconcile is the other half
// of the coupling: a fully readable scan must stay complete, or the new
// eligibility path would exclude every honest installation too.
func TestCleanSkillRootYieldsNoCoverageGapsAndCompleteReconcile(t *testing.T) {
	root := t.TempDir()
	writeSkillManifest(t, filepath.Join(root, "skills", "user", "clean-skill"), "clean-skill", "readable")
	host, err := adaptersdk.NewHostView([]string{root}, nil, testInventoryScanPseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	target := adaptersdk.Installation{
		InstallationID: "ain_clean", AdapterID: claudeadapter.AdapterID,
		SurfaceID: "cli", StateRoot: root,
	}
	input, scanned := claudeadapter.ScanHostInventory(host, target)
	if !scanned || input.CoverageGaps.Total() != 0 {
		t.Fatalf("scanned=%v gaps=%v want a clean scan", scanned, input.CoverageGaps)
	}
	snapshot, err := claudeadapter.BuildInventorySnapshot(input, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CoverageGapCount != 0 {
		t.Fatalf("snapshot gap count=%d want 0", snapshot.CoverageGapCount)
	}
	adapter := claudeadapter.New()
	scope := adaptersdk.ReconcileScope{InstallationID: target.InstallationID}
	if clean := adapter.Reconcile(
		context.Background(), scope, adaptersdk.InventorySnapshot{}, snapshot,
	); clean.Completeness != "complete" {
		t.Fatalf("clean reconcile completeness=%q want complete", clean.Completeness)
	}

	// The same snapshot with one recorded gap must report partial: this is what
	// removes a mis-mounted host from the cold denominator.
	degraded := snapshot
	degraded.CoverageGapCount = 1
	degraded.CoverageGapClasses = adaptersdk.CoverageGaps{
		adaptersdk.CoverageGapUnresolvableSymlink: 1,
	}
	if result := adapter.Reconcile(
		context.Background(), scope, adaptersdk.InventorySnapshot{}, degraded,
	); result.Completeness != "partial" {
		t.Fatalf("reconcile completeness=%q want partial with a coverage gap", result.Completeness)
	}
}
