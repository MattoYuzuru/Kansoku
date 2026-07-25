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
