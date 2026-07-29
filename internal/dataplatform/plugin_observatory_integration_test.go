//go:build postgres_integration

package dataplatform

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"kansoku.local/kansoku/internal/adaptersdk"
)

func TestPluginGraphLoadChildAttributionUpgradeDisableAndSourceLoss(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	ctx := context.Background()
	base := time.Date(2026, 7, 26, 14, 0, 0, 0, time.UTC)
	if err := EnsureInventoryInstallation(ctx, pool, "ain_plugin_canary", "codex"); err != nil {
		t.Fatal(err)
	}
	installation := inventoryTestNode("node_agent", adaptersdk.NodeAgentInstallation, "codex")
	plugin := inventoryTestNode("node_plugin_canary", adaptersdk.NodePluginPackage, "kansoku-session16-canary")
	plugin.Version = "1.0.0"
	plugin.SourceScope = adaptersdk.ScopeMarketplace
	collision := inventoryTestNode("node_plugin_collision", adaptersdk.NodePluginPackage, "kansoku-session16-canary")
	collision.Version = "9.0.0"
	collision.SourceScope = adaptersdk.ScopeRepository
	skill := inventoryTestNode("node_plugin_skill", adaptersdk.NodeSkillIdentity, "kansoku-noop-skill")
	server := inventoryTestNode("node_plugin_mcp", adaptersdk.NodeMCPServerInstance, "kansoku-do-nothing")
	tool := inventoryTestNode("node_plugin_tool", adaptersdk.NodeCustomCommandDefinition, "noop")
	app := inventoryTestNode("node_plugin_app", adaptersdk.NodeAppDefinition, "kansoku-canary-app")
	edges := []adaptersdk.Edge{
		inventoryTestEnabledEdge("edge_plugin_enabled", plugin.NodeID, installation.NodeID),
		{EdgeID: "edge_plugin_skill", Kind: adaptersdk.EdgeBundles, FromNode: plugin.NodeID, ToNode: skill.NodeID},
		{EdgeID: "edge_plugin_mcp", Kind: adaptersdk.EdgeBundles, FromNode: plugin.NodeID, ToNode: server.NodeID},
		{EdgeID: "edge_plugin_app", Kind: adaptersdk.EdgeProvides, FromNode: plugin.NodeID, ToNode: app.NodeID},
		{EdgeID: "edge_mcp_tool", Kind: adaptersdk.EdgeProvides, FromNode: server.NodeID, ToNode: tool.NodeID},
		{EdgeID: "edge_plugin_collision", Kind: adaptersdk.EdgeCollidesWith, FromNode: plugin.NodeID, ToNode: collision.NodeID},
	}
	snapshot := adaptersdk.InventorySnapshot{
		SnapshotID: "snap_plugin_v1", AdapterID: "codex", AdapterVersion: "0.145.0",
		InstallationID: "ain_plugin_canary", ObservedAt: base,
		Fingerprint: inventoryTestFingerprint("plugin-v1"),
		Nodes:       []adaptersdk.Node{installation, plugin, collision, skill, server, tool, app},
		Edges:       edges,
	}
	if _, err := PersistInventorySnapshot(ctx, pool, snapshot, "complete"); err != nil {
		t.Fatal(err)
	}
	pluginInstallationID := inventoryID("component-installation", snapshot.InstallationID, plugin.NodeID)
	sourceID := inventoryID("source-instance", snapshot.InstallationID, snapshot.AdapterID, snapshot.AdapterVersion, "inventory-scan")
	if _, err := pool.Exec(ctx, `
		INSERT INTO component_assertions (
			assertion_id,component_installation_id,agent_installation_id,
			assertion_kind,mode,evidence_tier,confidence,source_instance_id,
			adapter_version,schema_version,observed_at,idempotency_key,
			identity_resolution,declared_identity_pseudonym,candidate_count,
			component_kind
		) VALUES ('assert_plugin_loaded',$1,$2,'loaded','not_observed','native',1,$3,
			'0.145.0','codex.plugin_loaded/1',$4,'plugin-loaded-1','exact',$5,1,
			'plugin')
	`, pluginInstallationID, snapshot.InstallationID, sourceID, base.Add(5*time.Minute),
		inventoryID("declared-component", plugin.DeclaredName)); err != nil {
		t.Fatal(err)
	}
	insertChild := func() {
		t.Helper()
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			return persistPluginChildActivity(ctx, tx, pluginChildEvidence{
				ChildComponentID: tool.NodeID, AgentInstallationID: snapshot.InstallationID,
				SourceInstanceID: sourceID, AdapterVersion: snapshot.AdapterVersion,
				SchemaVersion: "fixture.mcp-call/1", EvidenceTier: "native", Confidence: 1,
				ObservedAt: base.Add(10 * time.Minute), IdempotencyKey: "mcp-call-noop-1",
			})
		}); err != nil {
			t.Fatal(err)
		}
	}
	insertChild()
	insertChild()

	response, err := PluginObservatory(ctx, pool, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if response.Counts.Installed != 2 || response.Counts.Enabled != 1 ||
		response.Counts.Loaded != 1 || response.Counts.Active != 1 ||
		response.Population != (Population{Numerator: 1, Denominator: 1}) {
		t.Fatalf("plugin list population mismatch: %+v", response)
	}
	var canary PluginObservatoryRow
	for _, row := range response.Data {
		if row.ComponentInstallationID == pluginInstallationID {
			canary = row
		}
	}
	if canary.ChildCount != 3 || canary.CollisionCount != 1 ||
		canary.ChildActivityCount != 1 || canary.OutcomeState != "unsupported" ||
		canary.LoadedCount != 1 || canary.Installed != true {
		t.Fatalf("canary graph/evidence mismatch: %+v", canary)
	}
	profile, err := PluginProfile(ctx, pool, pluginInstallationID, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.Children) != 3 || profile.Population != (Population{Numerator: 3, Denominator: 3}) ||
		len(profile.Versions) != 1 || !profile.Versions[0].Current {
		t.Fatalf("plugin profile mismatch: %+v", profile)
	}

	plugin.Version = "2.0.0"
	snapshot.SnapshotID = "snap_plugin_v2_disabled"
	snapshot.ObservedAt = base.Add(2 * time.Hour)
	snapshot.Fingerprint = inventoryTestFingerprint("plugin-v2-disabled")
	snapshot.Nodes = []adaptersdk.Node{installation, plugin, collision, skill, server, tool, app}
	snapshot.Edges = edges[1:]
	if _, err := PersistInventorySnapshot(ctx, pool, snapshot, "complete"); err != nil {
		t.Fatal(err)
	}
	disabled, err := PluginObservatory(ctx, pool, base, base.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range disabled.Data {
		if row.ComponentInstallationID == pluginInstallationID {
			if row.Enabled || row.LoadedCount != 1 || row.ChildActivityCount != 1 {
				t.Fatalf("disable erased history or current state is wrong: %+v", row)
			}
		}
	}
	upgraded, err := PluginProfile(ctx, pool, pluginInstallationID, base, base.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(upgraded.Versions) != 2 || !upgraded.Versions[0].Current ||
		upgraded.Identity.Version != "2.0.0" {
		t.Fatalf("version history mismatch: %+v", upgraded.Versions)
	}

	snapshot.SnapshotID = "snap_plugin_source_partial"
	snapshot.ObservedAt = base.Add(4 * time.Hour)
	snapshot.Fingerprint = inventoryTestFingerprint("plugin-source-partial")
	if _, err := PersistInventorySnapshot(ctx, pool, snapshot, "partial"); err != nil {
		t.Fatal(err)
	}
	partial, err := PluginObservatory(ctx, pool, base, base.Add(5*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range partial.Data {
		if row.ComponentInstallationID == pluginInstallationID &&
			(row.BundleCompleteness != "partial" || row.ActivityState != "not_observed") {
			t.Fatalf("source loss fabricated complete graph: %+v", row)
		}
	}
	var distinct int64
	if err := pool.QueryRow(ctx, `
		SELECT count(DISTINCT component_id) FROM components
		WHERE kind='plugin' AND declared_name='kansoku-session16-canary'
	`).Scan(&distinct); err != nil {
		t.Fatal(err)
	}
	if distinct != 2 {
		t.Fatalf("same-name marketplace/scope collision merged: %d", distinct)
	}
}
