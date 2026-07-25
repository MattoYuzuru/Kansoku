//go:build postgres_integration

package dataplatform

import (
	"context"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
)

func TestInventorySnapshotPersistsIdempotentlyAndBacksLifecycleFunnel(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	ctx := context.Background()
	observedAt := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	if err := EnsureInventoryInstallation(ctx, pool, "ain_inventory", "codex"); err != nil {
		t.Fatal(err)
	}
	installationNode := adaptersdk.Node{
		NodeID: "node_installation", Kind: adaptersdk.NodeAgentInstallation,
		DeclaredName: "codex", SourceScope: adaptersdk.ScopeUser,
		Fingerprint: inventoryTestFingerprint("installation"),
	}
	skillNode := inventoryTestNode("node_skill", adaptersdk.NodeSkillIdentity, "kansoku-noop-canary")
	pluginNode := inventoryTestNode("node_plugin", adaptersdk.NodePluginPackage, "github@openai-curated")
	mcpNode := inventoryTestNode("node_mcp", adaptersdk.NodeMCPServerInstance, "kansoku-do-nothing")
	cacheNode := inventoryTestNode("node_cache", adaptersdk.NodeCacheArtifact, "cached-only")
	cacheNode.CachedOnly = true
	snapshot := adaptersdk.InventorySnapshot{
		SnapshotID: "snap_inventory_1", AdapterID: "codex", AdapterVersion: "0.1.0",
		InstallationID: "ain_inventory", ObservedAt: observedAt,
		Fingerprint: inventoryTestFingerprint("snapshot"),
		Nodes:       []adaptersdk.Node{installationNode, skillNode, pluginNode, mcpNode, cacheNode},
		Edges: []adaptersdk.Edge{
			inventoryTestEnabledEdge("edge_skill", skillNode.NodeID, installationNode.NodeID),
			inventoryTestEnabledEdge("edge_mcp", mcpNode.NodeID, installationNode.NodeID),
		},
	}
	first, err := PersistInventorySnapshot(ctx, pool, snapshot, "complete")
	if err != nil {
		t.Fatal(err)
	}
	if !first.SnapshotInserted || first.InstalledComponentCount != 3 ||
		first.EnabledComponentCount != 2 {
		t.Fatalf("unexpected first persistence result: %+v", first)
	}
	replayed := snapshot
	replayed.ObservedAt = observedAt.Add(30 * time.Minute)
	second, err := PersistInventorySnapshot(ctx, pool, replayed, "complete")
	if err != nil {
		t.Fatal(err)
	}
	if second.SnapshotInserted || second.NodeCount != 5 ||
		second.InstalledComponentCount != 3 || second.EnabledComponentCount != 2 {
		t.Fatalf("idempotent replay inserted data: %+v", second)
	}
	var snapshots, nodes, components, states int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inventory_snapshots`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inventory_nodes`).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM components WHERE first_seen_at IS NOT NULL`).Scan(&components); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM component_inventory_state`).Scan(&states); err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 || nodes != 5 || components != 3 || states != 3 {
		t.Fatalf("snapshot projection mismatch: snapshots=%d nodes=%d components=%d states=%d", snapshots, nodes, components, states)
	}
	var lastSeen time.Time
	if err := pool.QueryRow(ctx, `
		SELECT max(last_seen_at) FROM component_inventory_state
	`).Scan(&lastSeen); err != nil {
		t.Fatal(err)
	}
	if !lastSeen.Equal(replayed.ObservedAt) {
		t.Fatalf("idempotent content replay must refresh current-state observation: %s", lastSeen)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO inventory_collection_status (
			target_id, adapter_id, agent_installation_id, state,
			last_attempted_at, last_succeeded_at, snapshot_id, node_count, edge_count
		) VALUES ('codex-test', 'codex', 'ain_inventory', 'complete', $1, $1, $2, 5, 2)
	`, replayed.ObservedAt, replayed.SnapshotID); err != nil {
		t.Fatal(err)
	}
	inventory, err := ComponentInventory(ctx, pool, "skill")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Data) != 1 || inventory.Data[0].DeclaredName != "kansoku-noop-canary" ||
		!inventory.Data[0].Enabled || inventory.Completeness.Status != "complete" {
		t.Fatalf("current inventory query mismatch: %+v", inventory)
	}
	funnel, err := ComponentLifecycleFunnel(ctx, pool, "", observedAt.Add(-time.Hour), observedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	stage := map[string]FunnelStageRow{}
	for _, row := range funnel.Data {
		stage[row.Stage] = row
	}
	if stage["installed"].ComponentCount != 3 || stage["enabled"].ComponentCount != 2 {
		t.Fatalf("inventory-backed funnel mismatch: %+v", funnel.Data)
	}
	if stage["invoked"].ValueState != "not_observed" ||
		funnel.Completeness.Status != "complete" ||
		funnel.Population.Numerator != 3 || funnel.Population.Denominator != 3 {
		t.Fatalf("funnel states/completeness mismatch: %+v", funnel)
	}
}

func TestInventorySnapshotRejectsRawPaths(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	ctx := context.Background()
	if err := EnsureInventoryInstallation(ctx, pool, "ain_inventory", "codex"); err != nil {
		t.Fatal(err)
	}
	snapshot := adaptersdk.InventorySnapshot{
		SnapshotID: "snap_invalid", AdapterID: "codex", AdapterVersion: "0.1.0",
		InstallationID: "ain_inventory", ObservedAt: time.Now().UTC(),
		Fingerprint: inventoryTestFingerprint("snapshot-invalid"),
		Nodes: []adaptersdk.Node{{
			NodeID: "node_invalid", Kind: adaptersdk.NodeSkillIdentity,
			DeclaredName: "invalid", SourceScope: adaptersdk.ScopeUser,
			PathPseudonym: "/Users/example/private/SKILL.md",
			Fingerprint:   inventoryTestFingerprint("invalid"),
		}},
	}
	if _, err := PersistInventorySnapshot(ctx, pool, snapshot, "complete"); err == nil {
		t.Fatal("raw filesystem path must fail before persistence")
	}
}

func inventoryTestFingerprint(seed string) string {
	return inventoryID("test-fingerprint", seed)[4:] +
		inventoryID("test-fingerprint-tail", seed)[4:]
}

func inventoryTestNode(id string, kind adaptersdk.NodeKind, name string) adaptersdk.Node {
	return adaptersdk.Node{
		NodeID: id, Kind: kind, DeclaredName: name, SourceScope: adaptersdk.ScopeUser,
		PathPseudonym: "hmac-sha256:" + inventoryTestFingerprint("path-"+id),
		Fingerprint:   inventoryTestFingerprint("node-" + id),
	}
}

func inventoryTestEnabledEdge(id, from, to string) adaptersdk.Edge {
	return adaptersdk.Edge{
		EdgeID: id, Kind: adaptersdk.EdgeEnabledFor, FromNode: from, ToNode: to,
	}
}
