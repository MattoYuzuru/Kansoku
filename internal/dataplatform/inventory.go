package dataplatform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/adaptersdk"
)

const (
	maxInventoryNodes = 4096
	maxInventoryEdges = 8192
)

var (
	inventoryIDPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/|-]{0,255}$`)
	pathPseudonymPattern      = regexp.MustCompile(`^hmac-sha256:[0-9a-f]{64}$`)
	inventoryFingerprintShape = regexp.MustCompile(`^(?:[0-9a-f]{32}|[0-9a-f]{64})$`)
)

// InventoryPersistResult exposes the exact projection cardinalities so a
// caller and the daily audit can reconcile snapshots without scanning raw
// agent configuration again.
type InventoryPersistResult struct {
	SnapshotInserted        bool  `json:"snapshot_inserted"`
	NodeCount               int64 `json:"node_count"`
	EdgeCount               int64 `json:"edge_count"`
	InstalledComponentCount int64 `json:"installed_component_count"`
	EnabledComponentCount   int64 `json:"enabled_component_count"`
}

func inventoryID(kind string, values ...string) string {
	hash := sha256.New()
	hash.Write([]byte("kansoku-inventory-projection/1"))
	hash.Write([]byte{0})
	hash.Write([]byte(kind))
	for _, value := range values {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	return "inv_" + hex.EncodeToString(hash.Sum(nil)[:16])
}

// EnsureInventoryInstallation creates the same generic agent dimension shape
// telemetry ingestion uses when an inventory scan happens before the first
// live event. Existing installations are never renamed across adapters.
func EnsureInventoryInstallation(ctx context.Context, pool *pgxpool.Pool, installationID, adapterID string) error {
	if pool == nil || !inventoryIDPattern.MatchString(installationID) ||
		!inventoryIDPattern.MatchString(adapterID) {
		return errors.New("inventory_installation_invalid")
	}
	deviceID := inventoryID("device", adapterID)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO devices (device_id) VALUES ($1) ON CONFLICT DO NOTHING`, deviceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_installations (agent_installation_id, device_id, agent_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (agent_installation_id) DO NOTHING
	`, installationID, deviceID, adapterID); err != nil {
		return err
	}
	var storedAdapter string
	if err := tx.QueryRow(ctx, `
		SELECT agent_id FROM agent_installations WHERE agent_installation_id = $1
	`, installationID).Scan(&storedAdapter); err != nil {
		return err
	}
	if storedAdapter != adapterID {
		return errors.New("inventory_installation_adapter_mismatch")
	}
	return tx.Commit(ctx)
}

// PersistInventorySnapshot validates and stores one closed adaptersdk graph,
// then refreshes the current component projection transactionally. It is
// idempotent by snapshot_id and component installation identity.
func PersistInventorySnapshot(
	ctx context.Context,
	pool *pgxpool.Pool,
	snapshot adaptersdk.InventorySnapshot,
	completeness string,
) (InventoryPersistResult, error) {
	if pool == nil {
		return InventoryPersistResult{}, errors.New("inventory_pool_required")
	}
	if err := validateInventorySnapshot(snapshot, completeness); err != nil {
		return InventoryPersistResult{}, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return InventoryPersistResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	adapterVersionID := inventoryID(
		"adapter-version", snapshot.AdapterID, snapshot.AdapterVersion,
	)
	sourceInstanceID := inventoryID(
		"source-instance", snapshot.InstallationID, snapshot.AdapterID,
		snapshot.AdapterVersion, "inventory-scan",
	)
	if _, err := tx.Exec(ctx, `
		INSERT INTO adapter_versions (adapter_version_id, adapter_id, version)
		VALUES ($1,$2,$3)
		ON CONFLICT (adapter_version_id) DO NOTHING
	`, adapterVersionID, snapshot.AdapterID, snapshot.AdapterVersion); err != nil {
		return InventoryPersistResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO source_instances (
			source_instance_id, adapter_version_id, source_kind
		) VALUES ($1,$2,'inventory_scan')
		ON CONFLICT (source_instance_id) DO NOTHING
	`, sourceInstanceID, adapterVersionID); err != nil {
		return InventoryPersistResult{}, err
	}

	commandTag, err := tx.Exec(ctx, `
		INSERT INTO inventory_snapshots (
			snapshot_id, adapter_id, adapter_version, agent_installation_id,
			observed_at, fingerprint, completeness
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (snapshot_id) DO NOTHING
	`, snapshot.SnapshotID, snapshot.AdapterID, snapshot.AdapterVersion,
		snapshot.InstallationID, snapshot.ObservedAt.UTC(), snapshot.Fingerprint, completeness)
	if err != nil {
		return InventoryPersistResult{}, err
	}
	result := InventoryPersistResult{SnapshotInserted: commandTag.RowsAffected() == 1}
	if !result.SnapshotInserted {
		var storedAdapter, storedVersion, storedInstallation, storedFingerprint string
		if err := tx.QueryRow(ctx, `
			SELECT adapter_id, adapter_version, agent_installation_id, fingerprint
			FROM inventory_snapshots WHERE snapshot_id = $1
		`, snapshot.SnapshotID).Scan(
			&storedAdapter, &storedVersion, &storedInstallation, &storedFingerprint,
		); err != nil {
			return InventoryPersistResult{}, err
		}
		if storedAdapter != snapshot.AdapterID ||
			storedVersion != snapshot.AdapterVersion ||
			storedInstallation != snapshot.InstallationID ||
			storedFingerprint != snapshot.Fingerprint {
			return InventoryPersistResult{}, errors.New("inventory_snapshot_id_collision")
		}
	}
	result.NodeCount = int64(len(snapshot.Nodes))
	result.EdgeCount = int64(len(snapshot.Edges))
	if result.SnapshotInserted {
		for _, node := range snapshot.Nodes {
			if _, err := tx.Exec(ctx, `
			INSERT INTO inventory_nodes (
				snapshot_id, node_id, kind, declared_name, version, source_scope,
				path_pseudonym, display_alias, cached_only, fingerprint
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		`, snapshot.SnapshotID, node.NodeID, string(node.Kind), node.DeclaredName,
				nullableInventoryString(node.Version), string(node.SourceScope),
				nullableInventoryString(node.PathPseudonym), nullableInventoryString(node.DisplayAlias),
				node.CachedOnly, node.Fingerprint); err != nil {
				return InventoryPersistResult{}, err
			}
		}
		for _, edge := range snapshot.Edges {
			if _, err := tx.Exec(ctx, `
			INSERT INTO inventory_edges (
				snapshot_id, edge_id, kind, from_node_id, to_node_id
			) VALUES ($1,$2,$3,$4,$5)
		`, snapshot.SnapshotID, edge.EdgeID, string(edge.Kind), edge.FromNode, edge.ToNode); err != nil {
				return InventoryPersistResult{}, err
			}
		}
	}

	enabledNodes := make(map[string]bool)
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeEnabledFor {
			enabledNodes[edge.FromNode] = true
		}
	}
	for _, node := range snapshot.Nodes {
		componentKind, ok := inventoryComponentKind(node.Kind)
		if !ok || node.CachedOnly {
			continue
		}
		componentVersionID := inventoryID("component-version", node.NodeID, node.Version)
		componentInstallationID := inventoryID("component-installation", snapshot.InstallationID, node.NodeID)
		versionState := "observed"
		if node.Version == "" {
			versionState = "not_observed"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO components (
				component_id, kind, declared_name, source_scope, path_pseudonym,
				inventory_fingerprint, first_seen_at, last_seen_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$7)
			ON CONFLICT (component_id) DO UPDATE SET
				declared_name = EXCLUDED.declared_name,
				source_scope = EXCLUDED.source_scope,
				path_pseudonym = EXCLUDED.path_pseudonym,
				inventory_fingerprint = EXCLUDED.inventory_fingerprint,
				last_seen_at = GREATEST(
					coalesce(components.last_seen_at, EXCLUDED.last_seen_at),
					EXCLUDED.last_seen_at
				)
		`, node.NodeID, componentKind, node.DeclaredName, string(node.SourceScope),
			nullableInventoryString(node.PathPseudonym), node.Fingerprint, snapshot.ObservedAt.UTC()); err != nil {
			return InventoryPersistResult{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO component_versions (
				component_version_id, component_id, version, version_state
			) VALUES ($1,$2,$3,$4)
			ON CONFLICT (component_version_id) DO NOTHING
		`, componentVersionID, node.NodeID, node.Version, versionState); err != nil {
			return InventoryPersistResult{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO component_installations (
				component_installation_id, component_version_id, agent_installation_id
			) VALUES ($1,$2,$3)
			ON CONFLICT (component_installation_id) DO UPDATE SET
				component_version_id = EXCLUDED.component_version_id
		`, componentInstallationID, componentVersionID, snapshot.InstallationID); err != nil {
			return InventoryPersistResult{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO component_inventory_state (
				component_installation_id, inventory_node_id, enabled,
				first_seen_at, last_seen_at, last_snapshot_id
			) VALUES ($1,$2,$3,$4,$4,$5)
			ON CONFLICT (component_installation_id) DO UPDATE SET
				enabled = EXCLUDED.enabled,
				last_seen_at = GREATEST(component_inventory_state.last_seen_at, EXCLUDED.last_seen_at),
				last_snapshot_id = EXCLUDED.last_snapshot_id
		`, componentInstallationID, node.NodeID, enabledNodes[node.NodeID],
			snapshot.ObservedAt.UTC(), snapshot.SnapshotID); err != nil {
			return InventoryPersistResult{}, err
		}
		assertionKinds := []string{"installed"}
		if enabledNodes[node.NodeID] {
			assertionKinds = append(assertionKinds, "enabled")
		}
		for _, assertionKind := range assertionKinds {
			idempotencyKey := snapshot.SnapshotID + ":" + node.NodeID + ":" + assertionKind
			if _, err := tx.Exec(ctx, `
				INSERT INTO component_assertions (
					assertion_id, component_installation_id,
					agent_installation_id, assertion_kind, mode,
					evidence_tier, confidence, source_instance_id,
					adapter_version, schema_version, observed_at,
					idempotency_key, identity_resolution,
					declared_identity_pseudonym, candidate_count
				) VALUES (
					$1,$2,$3,$4,'not_observed','native',1,$5,$6,
					'kansoku.inventory-snapshot/1',$7,$8,'exact',$9,1
				)
				ON CONFLICT (source_instance_id, idempotency_key) DO NOTHING
			`, inventoryID("component-assertion", idempotencyKey),
				componentInstallationID, snapshot.InstallationID, assertionKind,
				sourceInstanceID, snapshot.AdapterVersion, snapshot.ObservedAt.UTC(),
				idempotencyKey, inventoryID("declared-component", node.DeclaredName)); err != nil {
				return InventoryPersistResult{}, err
			}
		}
		result.InstalledComponentCount++
		if enabledNodes[node.NodeID] {
			result.EnabledComponentCount++
		}
	}
	if err := persistInventoryRelations(ctx, tx, snapshot); err != nil {
		return InventoryPersistResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return InventoryPersistResult{}, err
	}
	return result, nil
}

func validateInventorySnapshot(snapshot adaptersdk.InventorySnapshot, completeness string) error {
	if !inventoryIDPattern.MatchString(snapshot.SnapshotID) ||
		!inventoryIDPattern.MatchString(snapshot.AdapterID) ||
		!inventoryIDPattern.MatchString(snapshot.InstallationID) ||
		snapshot.AdapterVersion == "" || len(snapshot.AdapterVersion) > 128 ||
		!inventoryFingerprintShape.MatchString(snapshot.Fingerprint) ||
		snapshot.ObservedAt.IsZero() ||
		(completeness != "complete" && completeness != "partial" &&
			completeness != "degraded" && completeness != "unknown") ||
		len(snapshot.Nodes) > maxInventoryNodes || len(snapshot.Edges) > maxInventoryEdges {
		return errors.New("inventory_snapshot_invalid")
	}
	nodes := make(map[string]struct{}, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if !inventoryIDPattern.MatchString(node.NodeID) || node.DeclaredName == "" ||
			len(node.DeclaredName) > 256 || strings.ContainsAny(node.DeclaredName, "\x00\r\n") ||
			!inventoryFingerprintShape.MatchString(node.Fingerprint) ||
			(node.PathPseudonym != "" && !pathPseudonymPattern.MatchString(node.PathPseudonym)) {
			return errors.New("inventory_node_invalid")
		}
		if _, duplicate := nodes[node.NodeID]; duplicate {
			return errors.New("inventory_node_duplicate")
		}
		nodes[node.NodeID] = struct{}{}
	}
	edges := make(map[string]struct{}, len(snapshot.Edges))
	for _, edge := range snapshot.Edges {
		if !inventoryIDPattern.MatchString(edge.EdgeID) {
			return errors.New("inventory_edge_invalid")
		}
		if _, ok := nodes[edge.FromNode]; !ok {
			return errors.New("inventory_edge_endpoint_missing")
		}
		if _, ok := nodes[edge.ToNode]; !ok {
			return errors.New("inventory_edge_endpoint_missing")
		}
		if _, duplicate := edges[edge.EdgeID]; duplicate {
			return errors.New("inventory_edge_duplicate")
		}
		edges[edge.EdgeID] = struct{}{}
	}
	return nil
}

func inventoryComponentKind(kind adaptersdk.NodeKind) (string, bool) {
	switch kind {
	case adaptersdk.NodeSkillIdentity:
		return "skill", true
	case adaptersdk.NodePluginPackage:
		return "plugin", true
	case adaptersdk.NodeMCPServerInstance:
		return "mcp", true
	case adaptersdk.NodeHookDefinition:
		return "hook", true
	case adaptersdk.NodeCustomCommandDefinition:
		return "command", true
	default:
		return "", false
	}
}

func persistInventoryRelations(ctx context.Context, tx pgx.Tx, snapshot adaptersdk.InventorySnapshot) error {
	componentNodes := make(map[string]bool)
	for _, node := range snapshot.Nodes {
		_, componentNodes[node.NodeID] = inventoryComponentKind(node.Kind)
	}
	for _, edge := range snapshot.Edges {
		relationKind := ""
		switch edge.Kind {
		case adaptersdk.EdgeBundles:
			relationKind = "bundles"
		case adaptersdk.EdgeCollidesWith:
			relationKind = "collides_with"
		case adaptersdk.EdgeShadows:
			relationKind = "shadows"
		}
		if relationKind == "" || !componentNodes[edge.FromNode] || !componentNodes[edge.ToNode] {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO component_relations (relation_id, parent_id, child_id, relation_kind)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (relation_id) DO NOTHING
		`, edge.EdgeID, edge.FromNode, edge.ToNode, relationKind); err != nil {
			return err
		}
	}
	return nil
}

func nullableInventoryString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// LatestInstallationForAdapter resolves the logical installation already
// used by telemetry so a read-only inventory scan joins existing activity.
func LatestInstallationForAdapter(ctx context.Context, pool *pgxpool.Pool, adapterID string) (string, error) {
	var installationID string
	err := pool.QueryRow(ctx, `
		SELECT agent_installation_id
		FROM agent_installations
		WHERE agent_id = $1
		ORDER BY created_at DESC, agent_installation_id
		LIMIT 1
	`, adapterID).Scan(&installationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return installationID, err
}

// InventorySnapshotFreshness is used by UI/API diagnostics without exposing
// any raw host path.
type InventorySnapshotFreshness struct {
	AdapterID      string
	InstallationID string
	ObservedAt     time.Time
	Completeness   string
}
