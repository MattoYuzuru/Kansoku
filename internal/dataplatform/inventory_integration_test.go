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
	cacheSkillNode := inventoryTestNode("node_cache_skill", adaptersdk.NodeSkillIdentity, "cached-only-skill")
	cacheSkillNode.SourceScope = adaptersdk.ScopePluginCache
	cacheSkillNode.CachedOnly = true
	snapshot := adaptersdk.InventorySnapshot{
		SnapshotID: "snap_inventory_1", AdapterID: "codex", AdapterVersion: "0.1.0",
		InstallationID: "ain_inventory", ObservedAt: observedAt,
		Fingerprint: inventoryTestFingerprint("snapshot"),
		Nodes: []adaptersdk.Node{
			installationNode, skillNode, pluginNode, mcpNode, cacheNode, cacheSkillNode,
		},
		Edges: []adaptersdk.Edge{
			inventoryTestEnabledEdge("edge_skill", skillNode.NodeID, installationNode.NodeID),
			inventoryTestEnabledEdge("edge_mcp", mcpNode.NodeID, installationNode.NodeID),
			{
				EdgeID: "edge_cache_bundle", Kind: adaptersdk.EdgeBundles,
				FromNode: cacheNode.NodeID, ToNode: cacheSkillNode.NodeID,
			},
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
	if second.SnapshotInserted || second.NodeCount != 6 ||
		second.InstalledComponentCount != 3 || second.EnabledComponentCount != 2 {
		t.Fatalf("idempotent replay inserted data: %+v", second)
	}
	var snapshots, nodes, components, states, assertions int64
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
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM component_assertions
		WHERE assertion_kind IN ('installed','enabled')
	`).Scan(&assertions); err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 || nodes != 6 || components != 3 || states != 3 || assertions != 5 {
		t.Fatalf("snapshot projection mismatch: snapshots=%d nodes=%d components=%d states=%d assertions=%d",
			snapshots, nodes, components, states, assertions)
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
		) VALUES ('codex-test', 'codex', 'ain_inventory', 'complete', $1, $1, $2, 6, 3)
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
		stage["succeeded"].ValueState != "unsupported" ||
		stage["opportunity_detected"].ValueState != "unsupported" ||
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

func TestInventorySnapshotReResolvesImmutableUnresolvedEvidence(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	ctx := context.Background()
	base := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	if err := EnsureInventoryInstallation(ctx, pool, "ain_late_inventory", "codex"); err != nil {
		t.Fatal(err)
	}
	installation := adaptersdk.Node{
		NodeID: "node_late_installation", Kind: adaptersdk.NodeAgentInstallation,
		DeclaredName: "codex", SourceScope: adaptersdk.ScopeUser,
		Fingerprint: inventoryTestFingerprint("late-installation"),
	}
	initial := adaptersdk.InventorySnapshot{
		SnapshotID: "snap_late_initial", AdapterID: "codex", AdapterVersion: "0.145.0",
		InstallationID: "ain_late_inventory", ObservedAt: base,
		Fingerprint: inventoryTestFingerprint("late-initial"),
		Nodes:       []adaptersdk.Node{installation},
	}
	if _, err := PersistInventorySnapshot(ctx, pool, initial, "complete"); err != nil {
		t.Fatal(err)
	}
	sourceID := inventoryID(
		"source-instance", initial.InstallationID, initial.AdapterID,
		initial.AdapterVersion, "inventory-scan",
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO component_assertions (
			assertion_id,agent_installation_id,assertion_kind,mode,
			evidence_tier,confidence,source_instance_id,adapter_version,
			schema_version,observed_at,idempotency_key,identity_resolution,
			declared_identity_pseudonym,candidate_count,component_kind,
			qualified_identity,identity_source,invocation_mode,resolution_version
		) VALUES (
			'assert_late_skill','ain_late_inventory','invoked','explicit',
			'reconstructed',0.85,$1,'0.145.0','codex.rollout/2',$2,
			'late-skill-invocation','unresolved',
			'hmac-sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
			0,'skill','late-skill','rollout_corroborated','explicit',1
		)
	`, sourceID, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO component_assertion_resolution_history (
			resolution_history_id,assertion_id,resolution_version,
			identity_resolution,component_installation_id,candidate_count,
			resolver_version,resolution_trigger,resolved_at
		) VALUES (
			'rsh_late_skill_initial','assert_late_skill',1,'unresolved',NULL,0,
			'component-resolver/2','initial_ingest',$1
		)
	`, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	skill := inventoryTestNode(
		"node_late_skill", adaptersdk.NodeSkillIdentity, "late-skill",
	)
	updated := initial
	updated.SnapshotID = "snap_late_with_skill"
	updated.ObservedAt = base.Add(2 * time.Minute)
	updated.Fingerprint = inventoryTestFingerprint("late-with-skill")
	updated.Nodes = []adaptersdk.Node{installation, skill}
	updated.Edges = []adaptersdk.Edge{
		inventoryTestEnabledEdge("edge_late_skill", skill.NodeID, installation.NodeID),
	}
	if _, err := PersistInventorySnapshot(ctx, pool, updated, "complete"); err != nil {
		t.Fatal(err)
	}
	var originalResolution string
	var originalInstallation *string
	if err := pool.QueryRow(ctx, `
		SELECT identity_resolution,component_installation_id
		FROM component_assertions WHERE assertion_id='assert_late_skill'
	`).Scan(&originalResolution, &originalInstallation); err != nil {
		t.Fatal(err)
	}
	if originalResolution != "unresolved" || originalInstallation != nil {
		t.Fatalf("historical assertion was rewritten: %q %v", originalResolution, originalInstallation)
	}
	var currentResolution, currentInstallation string
	var currentVersion int64
	if err := pool.QueryRow(ctx, `
		SELECT identity_resolution,component_installation_id,resolution_version
		FROM component_assertion_current_resolution
		WHERE assertion_id='assert_late_skill'
	`).Scan(&currentResolution, &currentInstallation, &currentVersion); err != nil {
		t.Fatal(err)
	}
	wantInstallation := inventoryID(
		"component-installation", updated.InstallationID, skill.NodeID,
	)
	if currentResolution != "exact" || currentInstallation != wantInstallation ||
		currentVersion != 2 {
		t.Fatalf("current resolution=%q installation=%q version=%d", currentResolution, currentInstallation, currentVersion)
	}
	var historyCount int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM component_assertion_resolution_history
		WHERE assertion_id='assert_late_skill'
	`).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 2 {
		t.Fatalf("resolution history=%d want 2", historyCount)
	}
	observatory, err := SkillObservatory(ctx, pool, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(observatory.Data) != 1 || observatory.Data[0].InvokedCount != 1 ||
		observatory.Exclusions["unresolved_identity"] != 0 {
		t.Fatalf("re-resolved evidence not reflected: %+v", observatory)
	}
}

func TestCompleteInventorySnapshotReplacesOnlyCurrentProjection(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	ctx := context.Background()
	base := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	if err := EnsureInventoryInstallation(ctx, pool, "ain_inventory_replace", "codex"); err != nil {
		t.Fatal(err)
	}
	installation := adaptersdk.Node{
		NodeID: "node_replace_installation", Kind: adaptersdk.NodeAgentInstallation,
		DeclaredName: "codex", SourceScope: adaptersdk.ScopeUser,
		Fingerprint: inventoryTestFingerprint("replace-installation"),
	}
	firstSkill := inventoryTestNode("node_replace_skill_a", adaptersdk.NodeSkillIdentity, "shared-skill")
	secondSkill := inventoryTestNode("node_replace_skill_b", adaptersdk.NodeSkillIdentity, "shared-skill")
	initial := adaptersdk.InventorySnapshot{
		SnapshotID: "snap_replace_initial", AdapterID: "codex", AdapterVersion: "0.145.0",
		InstallationID: "ain_inventory_replace", ObservedAt: base,
		Fingerprint: inventoryTestFingerprint("replace-initial"),
		Nodes:       []adaptersdk.Node{installation, firstSkill, secondSkill},
	}
	if _, err := PersistInventorySnapshot(ctx, pool, initial, "complete"); err != nil {
		t.Fatal(err)
	}
	current := initial
	current.SnapshotID = "snap_replace_current"
	current.ObservedAt = base.Add(time.Minute)
	current.Fingerprint = inventoryTestFingerprint("replace-current")
	current.Nodes = []adaptersdk.Node{installation, firstSkill}
	if _, err := PersistInventorySnapshot(ctx, pool, current, "complete"); err != nil {
		t.Fatal(err)
	}

	var currentStates, historicalComponents, historicalAssertions, snapshots int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM component_inventory_state current_state
		JOIN component_installations installation
		  ON installation.component_installation_id =
		     current_state.component_installation_id
		WHERE installation.agent_installation_id='ain_inventory_replace'
	`).Scan(&currentStates); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(DISTINCT component.component_id)
		FROM components component
		JOIN component_versions version ON version.component_id=component.component_id
		JOIN component_installations installation
		  ON installation.component_version_id=version.component_version_id
		WHERE installation.agent_installation_id='ain_inventory_replace'
	`).Scan(&historicalComponents); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM component_assertions
		WHERE agent_installation_id='ain_inventory_replace'
		  AND assertion_kind='installed'
	`).Scan(&historicalAssertions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_snapshots
		WHERE agent_installation_id='ain_inventory_replace'
	`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if currentStates != 1 || historicalComponents != 2 ||
		historicalAssertions != 3 || snapshots != 2 {
		t.Fatalf(
			"replacement must prune only current projection: states=%d components=%d assertions=%d snapshots=%d",
			currentStates, historicalComponents, historicalAssertions, snapshots,
		)
	}

	// Replaying the older complete snapshot must not resurrect a stale
	// current candidate or erase the newer state.
	if _, err := PersistInventorySnapshot(ctx, pool, initial, "complete"); err != nil {
		t.Fatal(err)
	}
	var replayStates int64
	var currentSnapshot string
	if err := pool.QueryRow(ctx, `
		SELECT count(*),min(current_state.last_snapshot_id)
		FROM component_inventory_state current_state
		JOIN component_installations installation
		  ON installation.component_installation_id =
		     current_state.component_installation_id
		WHERE installation.agent_installation_id='ain_inventory_replace'
	`).Scan(&replayStates, &currentSnapshot); err != nil {
		t.Fatal(err)
	}
	if replayStates != 1 || currentSnapshot != current.SnapshotID {
		t.Fatalf(
			"older replay changed current projection: states=%d snapshot=%q",
			replayStates, currentSnapshot,
		)
	}

	// An incomplete newer scan must also preserve the last complete current
	// projection rather than turning source loss into component removal.
	unknown := current
	unknown.SnapshotID = "snap_replace_unknown"
	unknown.ObservedAt = base.Add(2 * time.Minute)
	unknown.Fingerprint = inventoryTestFingerprint("replace-unknown")
	unknown.Nodes = []adaptersdk.Node{installation}
	if _, err := PersistInventorySnapshot(ctx, pool, unknown, "unknown"); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*),min(current_state.last_snapshot_id)
		FROM component_inventory_state current_state
		JOIN component_installations installation
		  ON installation.component_installation_id =
		     current_state.component_installation_id
		WHERE installation.agent_installation_id='ain_inventory_replace'
	`).Scan(&replayStates, &currentSnapshot); err != nil {
		t.Fatal(err)
	}
	if replayStates != 1 || currentSnapshot != current.SnapshotID {
		t.Fatalf(
			"unknown scan changed last complete projection: states=%d snapshot=%q",
			replayStates, currentSnapshot,
		)
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
