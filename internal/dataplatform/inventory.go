package dataplatform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	// The coverage gap tally is persisted with the snapshot it describes: it is
	// evidence about that one observation, and downstream cold eligibility for
	// an adapter with no exposure surface rests on this snapshot's completeness.
	coverageGapClasses, err := inventoryCoverageGapJSON(snapshot.CoverageGapClasses)
	if err != nil {
		return InventoryPersistResult{}, err
	}
	commandTag, err := tx.Exec(ctx, `
		INSERT INTO inventory_snapshots (
			snapshot_id, adapter_id, adapter_version, agent_installation_id,
			observed_at, fingerprint, completeness,
			coverage_gap_count, coverage_gap_classes
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb)
		ON CONFLICT (snapshot_id) DO NOTHING
	`, snapshot.SnapshotID, snapshot.AdapterID, snapshot.AdapterVersion,
		snapshot.InstallationID, snapshot.ObservedAt.UTC(), snapshot.Fingerprint, completeness,
		snapshot.CoverageGapCount, coverageGapClasses)
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
	nodeNames := make(map[string]string, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodeNames[node.NodeID] = node.DeclaredName
	}
	ownerByChild := make(map[string]string)
	for _, edge := range snapshot.Edges {
		if edge.Kind == adaptersdk.EdgeEnabledFor {
			enabledNodes[edge.FromNode] = true
		}
		if edge.Kind == adaptersdk.EdgeBundles && nodeNames[edge.FromNode] != "" {
			ownerByChild[edge.ToNode] = nodeNames[edge.FromNode]
		}
	}
	var newerCurrentProjectionExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM component_inventory_state current_state
			JOIN component_installations installation
			  ON installation.component_installation_id =
			     current_state.component_installation_id
			WHERE installation.agent_installation_id = $1
			  AND current_state.last_seen_at > $2
		)
	`, snapshot.InstallationID, snapshot.ObservedAt.UTC()).Scan(
		&newerCurrentProjectionExists,
	); err != nil {
		return InventoryPersistResult{}, err
	}
	refreshCurrentProjection := !newerCurrentProjectionExists
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
		if refreshCurrentProjection {
			if _, err := tx.Exec(ctx, `
				INSERT INTO component_inventory_state (
					component_installation_id, inventory_node_id, enabled,
					first_seen_at, last_seen_at, last_snapshot_id
				) VALUES ($1,$2,$3,$4,$4,$5)
				ON CONFLICT (component_installation_id) DO UPDATE SET
					enabled = EXCLUDED.enabled,
					last_seen_at = GREATEST(
						component_inventory_state.last_seen_at,
						EXCLUDED.last_seen_at
					),
					last_snapshot_id = EXCLUDED.last_snapshot_id
				WHERE EXCLUDED.last_seen_at >=
				      component_inventory_state.last_seen_at
			`, componentInstallationID, node.NodeID, enabledNodes[node.NodeID],
				snapshot.ObservedAt.UTC(), snapshot.SnapshotID); err != nil {
				return InventoryPersistResult{}, err
			}
		}
		assertionKinds := []string{"installed"}
		if enabledNodes[node.NodeID] {
			assertionKinds = append(assertionKinds, "enabled")
		}
		for _, assertionKind := range assertionKinds {
			idempotencyKey := snapshot.SnapshotID + ":" + node.NodeID + ":" + assertionKind
			qualifiedIdentity := node.DeclaredName
			ownerIdentity := ownerByChild[node.NodeID]
			if ownerIdentity != "" {
				qualifiedIdentity = ownerIdentity + ":" + node.DeclaredName
			}
			assertionID := inventoryID("component-assertion", idempotencyKey)
			inserted, err := tx.Exec(ctx, `
				INSERT INTO component_assertions (
					assertion_id, component_installation_id,
					agent_installation_id, assertion_kind, mode,
					evidence_tier, confidence, source_instance_id,
					adapter_version, schema_version, observed_at,
					idempotency_key, identity_resolution,
					declared_identity_pseudonym, candidate_count,
					component_kind, qualified_identity, identity_source,
					owner_plugin_identity, invocation_mode, resolution_version
				) VALUES (
					$1,$2,$3,$4,'not_observed','native',1,$5,$6,
					'kansoku.inventory-snapshot/1',$7,$8,'exact',$9,1,
					$10,$11,'inventory',NULLIF($12,''),'not_observed',1
				)
				ON CONFLICT (source_instance_id, idempotency_key) DO NOTHING
			`, assertionID,
				componentInstallationID, snapshot.InstallationID, assertionKind,
				sourceInstanceID, snapshot.AdapterVersion, snapshot.ObservedAt.UTC(),
				idempotencyKey, inventoryID("declared-component", node.DeclaredName),
				componentKind, qualifiedIdentity, ownerIdentity)
			if err != nil {
				return InventoryPersistResult{}, err
			}
			if inserted.RowsAffected() > 0 {
				if _, err := tx.Exec(ctx, `
					INSERT INTO component_assertion_resolution_history (
						resolution_history_id, assertion_id, resolution_version,
						identity_resolution, component_installation_id,
						candidate_count, resolver_version, resolution_trigger,
						resolved_at
					) VALUES (
						$1,$2,1,'exact',$3,1,'component-resolver/2',
						'inventory_snapshot',$4
					)
					ON CONFLICT (assertion_id, resolution_version) DO NOTHING
				`, inventoryID("resolution-history", assertionID, "1"),
					assertionID, componentInstallationID, snapshot.ObservedAt.UTC()); err != nil {
					return InventoryPersistResult{}, err
				}
			}
		}
		result.InstalledComponentCount++
		if enabledNodes[node.NodeID] {
			result.EnabledComponentCount++
		}
	}
	if completeness == "complete" && refreshCurrentProjection {
		// component_inventory_state is a replaceable current-state
		// projection. A complete newer snapshot may remove entries that are
		// no longer present; immutable snapshots, component dimensions and
		// historical assertions remain untouched. Partial/degraded/unknown
		// scans never prune, and an older replay cannot erase newer state.
		if _, err := tx.Exec(ctx, `
			DELETE FROM component_inventory_state current_state
			USING component_installations installation
			WHERE current_state.component_installation_id =
			      installation.component_installation_id
			  AND installation.agent_installation_id = $1
			  AND current_state.last_snapshot_id <> $2
			  AND current_state.last_seen_at <= $3
		`, snapshot.InstallationID, snapshot.SnapshotID, snapshot.ObservedAt.UTC()); err != nil {
			return InventoryPersistResult{}, err
		}
	}
	if err := persistInventoryRelations(
		ctx, tx, snapshot, sourceInstanceID, completeness,
	); err != nil {
		return InventoryPersistResult{}, err
	}
	if _, err := reResolveComponentAssertions(ctx, tx, snapshot.InstallationID, snapshot.ObservedAt.UTC()); err != nil {
		return InventoryPersistResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return InventoryPersistResult{}, err
	}
	return result, nil
}

// reResolveComponentAssertions appends resolution history after new
// inventory becomes visible. The original assertion row is immutable.
func reResolveComponentAssertions(
	ctx context.Context,
	tx pgx.Tx,
	installationID string,
	resolvedAt time.Time,
) (int64, error) {
	tag, err := tx.Exec(ctx, `
		WITH current_resolution AS (
			SELECT ca.assertion_id, ca.component_kind, ca.qualified_identity,
			       ca.identity_source, ca.owner_plugin_identity,
			       ca.agent_installation_id,
			       COALESCE(cr.identity_resolution, ca.identity_resolution) AS current_state,
			       COALESCE(cr.candidate_count, ca.candidate_count) AS current_candidates,
			       COALESCE(cr.component_installation_id, ca.component_installation_id) AS current_installation,
			       COALESCE(cr.resolution_version, ca.resolution_version, 0) AS current_version
			FROM component_assertions ca
			LEFT JOIN component_assertion_current_resolution cr
			  ON cr.assertion_id = ca.assertion_id
			WHERE ca.agent_installation_id = $1
			  AND ca.component_kind IS NOT NULL
			  AND ca.qualified_identity IS NOT NULL
			  AND COALESCE(cr.identity_resolution, ca.identity_resolution)
			      IN ('unresolved','ambiguous')
		), candidate_sets AS (
			SELECT current_resolution.*,
			       candidates.candidate_count,
			       candidates.component_installation_id
			FROM current_resolution
			CROSS JOIN LATERAL (
				SELECT count(*)::integer AS candidate_count,
				       min(ci.component_installation_id) AS component_installation_id
				FROM component_inventory_state cis
				JOIN component_installations ci
				  ON ci.component_installation_id = cis.component_installation_id
				JOIN component_versions cv
				  ON cv.component_version_id = ci.component_version_id
				JOIN components c ON c.component_id = cv.component_id
				LEFT JOIN inventory_nodes node
				  ON node.snapshot_id = cis.last_snapshot_id
				 AND node.node_id = cis.inventory_node_id
				LEFT JOIN inventory_edges ownership
				  ON ownership.snapshot_id = cis.last_snapshot_id
				 AND ownership.to_node_id = cis.inventory_node_id
				 AND ownership.kind = 'bundles'
				LEFT JOIN inventory_nodes owner
				  ON owner.snapshot_id = ownership.snapshot_id
				 AND owner.node_id = ownership.from_node_id
				 AND owner.kind = 'plugin_package'
				WHERE ci.agent_installation_id = current_resolution.agent_installation_id
				  AND c.kind = current_resolution.component_kind
				  AND (
					(
					 CASE WHEN owner.declared_name IS NULL
					      THEN c.declared_name
					      ELSE owner.declared_name || ':' || c.declared_name
					 END
					) = current_resolution.qualified_identity
				  OR (
					c.kind='plugin' AND
					split_part(c.declared_name,'@',1) =
						current_resolution.qualified_identity
				  )
				  OR (
					c.kind='skill' AND owner.declared_name IS NOT NULL AND
					split_part(owner.declared_name,'@',1) || ':' ||
						c.declared_name =
						current_resolution.qualified_identity
				  )
				  )
			) candidates
		), changed AS (
			SELECT *,
			       CASE
			         WHEN candidate_count = 1 THEN 'exact'
			         WHEN candidate_count > 1 THEN 'ambiguous'
			         ELSE 'unresolved'
			       END AS next_state
			FROM candidate_sets
		)
		INSERT INTO component_assertion_resolution_history (
			resolution_history_id, assertion_id, resolution_version,
			identity_resolution, component_installation_id, candidate_count,
			resolver_version, resolution_trigger, resolved_at
		)
		SELECT
			'rsh_' || substr(md5(
				assertion_id || ':' || (current_version + 1)::text ||
				':component-resolver/2'
			), 1, 28),
			assertion_id, current_version + 1, next_state,
			CASE WHEN candidate_count = 1 THEN component_installation_id ELSE NULL END,
			candidate_count, 'component-resolver/2', 'inventory_snapshot', $2
		FROM changed
		WHERE next_state <> current_state
		   OR candidate_count <> current_candidates
		   OR (
			candidate_count = 1 AND
			component_installation_id IS DISTINCT FROM current_installation
		   )
		ON CONFLICT (assertion_id, resolution_version) DO NOTHING
	`, installationID, resolvedAt)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
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
	case adaptersdk.NodeMCPTool:
		return "command", true
	case adaptersdk.NodeHookDefinition:
		return "hook", true
	case adaptersdk.NodeCustomCommandDefinition:
		return "command", true
	case adaptersdk.NodeAppDefinition:
		return "app", true
	default:
		return "", false
	}
}

func persistInventoryRelations(
	ctx context.Context,
	tx pgx.Tx,
	snapshot adaptersdk.InventorySnapshot,
	sourceInstanceID string,
	completeness string,
) error {
	componentNodes := make(map[string]bool)
	for _, node := range snapshot.Nodes {
		_, componentNodes[node.NodeID] = inventoryComponentKind(node.Kind)
	}
	for _, edge := range snapshot.Edges {
		relationKind := ""
		switch edge.Kind {
		case adaptersdk.EdgeBundles:
			relationKind = "bundles"
		case adaptersdk.EdgeProvides:
			relationKind = "provides"
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
		idempotencyKey := snapshot.SnapshotID + ":" + edge.EdgeID
		if _, err := tx.Exec(ctx, `
			INSERT INTO component_relation_observations (
				relation_observation_id, relation_id, inventory_snapshot_id,
				source_instance_id, observed_at, completeness, adapter_version,
				schema_version, idempotency_key
			) VALUES ($1,$2,$3,$4,$5,$6,$7,'kansoku.inventory-relation/1',$8)
			ON CONFLICT (source_instance_id, idempotency_key) DO NOTHING
		`, inventoryID("component-relation-observation", idempotencyKey),
			edge.EdgeID, snapshot.SnapshotID, sourceInstanceID,
			snapshot.ObservedAt.UTC(), completeness, snapshot.AdapterVersion,
			idempotencyKey); err != nil {
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

// inventoryCoverageGapJSON encodes a coverage gap tally as the bounded JSONB
// object the schema declares. Only closed-vocabulary class names are ever
// written, so an unrecognised class fails loudly here rather than becoming a
// free-form key in a durable column.
func inventoryCoverageGapJSON(gaps adaptersdk.CoverageGaps) (string, error) {
	if len(gaps) == 0 {
		return "{}", nil
	}
	encoded := make(map[string]int, len(gaps))
	for class, count := range gaps {
		if !adaptersdk.ValidCoverageGapClass(class) {
			return "", errors.New("inventory_coverage_gap_class_unknown")
		}
		if count < 0 {
			return "", errors.New("inventory_coverage_gap_count_negative")
		}
		encoded[string(class)] = count
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
