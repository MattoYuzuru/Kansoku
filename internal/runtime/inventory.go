package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/claudeadapter"
	"kansoku.local/kansoku/internal/codexadapter"
	"kansoku.local/kansoku/internal/dataplatform"
)

// InventoryCollector runs adapter inventory against explicitly configured,
// read-only state roots. Target paths are used only to construct HostView and
// are never written to Postgres or logs.
type InventoryCollector struct {
	pool        *pgxpool.Pool
	registry    *adaptersdk.Registry
	targets     []InventoryTarget
	identityKey []byte
	interval    time.Duration
	now         func() time.Time
}

func newDefaultAdapterRegistry() (*adaptersdk.Registry, error) {
	registry := adaptersdk.NewRegistry()
	for _, adapter := range []adaptersdk.Adapter{codexadapter.New(), claudeadapter.New()} {
		if err := registry.Register(adapter); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func NewInventoryCollector(
	pool *pgxpool.Pool,
	targets []InventoryTarget,
	identityKey []byte,
	interval time.Duration,
) (*InventoryCollector, error) {
	if pool == nil || len(identityKey) < 32 || (len(targets) > 0 && interval <= 0) {
		return nil, errors.New("inventory_collector_configuration_invalid")
	}
	registry, err := newDefaultAdapterRegistry()
	if err != nil {
		return nil, err
	}
	return &InventoryCollector{
		pool: pool, registry: registry, targets: append([]InventoryTarget(nil), targets...),
		identityKey: append([]byte(nil), identityKey...), interval: interval, now: time.Now,
	}, nil
}

// ScanOnce attempts every target independently. A source-level failure is
// durably represented as degraded status and does not hide inventory from
// other targets.
func (c *InventoryCollector) ScanOnce(ctx context.Context) error {
	for _, target := range c.targets {
		if err := c.scanTarget(ctx, target); err != nil {
			if statusErr := c.recordStatus(ctx, target, "", "degraded", "inventory_scan_failed", nil, 0, 0); statusErr != nil {
				return statusErr
			}
		}
	}
	return nil
}

func (c *InventoryCollector) scanTarget(ctx context.Context, target InventoryTarget) error {
	host, err := adaptersdk.NewHostView([]string{target.StateRoot}, nil, c.identityKey)
	if err != nil {
		return err
	}
	root, err := host.ReadProbe(target.StateRoot)
	if err != nil {
		return err
	}
	if !root.Exists {
		return c.recordStatus(ctx, target, "", "not_observed", "state_root_not_mounted", nil, 0, 0)
	}
	adapter, err := c.registry.Get(target.AdapterID)
	if err != nil {
		return err
	}
	installationID := target.InstallationID
	if installationID == "" {
		installationID, err = dataplatform.LatestInstallationForAdapter(ctx, c.pool, target.AdapterID)
		if err != nil {
			return err
		}
	}
	if installationID == "" {
		installationID = normalizedInstallationID(target.AdapterID)
	}
	if err := dataplatform.EnsureInventoryInstallation(ctx, c.pool, installationID, target.AdapterID); err != nil {
		return err
	}
	snapshot, err := adapter.Inventory(ctx, adaptersdk.Installation{
		InstallationID: installationID, AdapterID: target.AdapterID,
		SurfaceID: target.SurfaceID, StateRoot: target.StateRoot,
	}, host)
	if err != nil {
		return err
	}
	result, err := dataplatform.PersistInventorySnapshot(ctx, c.pool, snapshot, "complete")
	if err != nil {
		return err
	}
	return c.recordStatus(
		ctx, target, installationID, "complete", "", &snapshot,
		result.NodeCount, result.EdgeCount,
	)
}

func (c *InventoryCollector) recordStatus(
	ctx context.Context,
	target InventoryTarget,
	installationID, state, errorClass string,
	snapshot *adaptersdk.InventorySnapshot,
	nodeCount, edgeCount int64,
) error {
	var snapshotID any
	var succeededAt any
	if snapshot != nil {
		snapshotID = snapshot.SnapshotID
		succeededAt = snapshot.ObservedAt.UTC()
	}
	_, err := c.pool.Exec(ctx, `
		INSERT INTO inventory_collection_status (
			target_id, adapter_id, agent_installation_id, state, error_class,
			last_attempted_at, last_succeeded_at, snapshot_id, node_count, edge_count
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (target_id) DO UPDATE SET
			adapter_id = EXCLUDED.adapter_id,
			agent_installation_id = EXCLUDED.agent_installation_id,
			state = EXCLUDED.state,
			error_class = EXCLUDED.error_class,
			last_attempted_at = EXCLUDED.last_attempted_at,
			last_succeeded_at = coalesce(
				EXCLUDED.last_succeeded_at,
				inventory_collection_status.last_succeeded_at
			),
			snapshot_id = coalesce(EXCLUDED.snapshot_id, inventory_collection_status.snapshot_id),
			node_count = CASE WHEN EXCLUDED.snapshot_id IS NULL
				THEN inventory_collection_status.node_count ELSE EXCLUDED.node_count END,
			edge_count = CASE WHEN EXCLUDED.snapshot_id IS NULL
				THEN inventory_collection_status.edge_count ELSE EXCLUDED.edge_count END
	`, target.TargetID, target.AdapterID, nullableRuntimeString(installationID), state,
		nullableRuntimeString(errorClass), c.now().UTC(), succeededAt, snapshotID, nodeCount, edgeCount)
	return err
}

func (c *InventoryCollector) Run(ctx context.Context) {
	if len(c.targets) == 0 {
		return
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = c.ScanOnce(ctx)
		}
	}
}

func normalizedInstallationID(adapterID string) string {
	hash := sha256.New()
	hash.Write([]byte("agent-installation/1"))
	hash.Write([]byte{0})
	hash.Write([]byte(adapterID))
	return "ain_" + hex.EncodeToString(hash.Sum(nil))[:32]
}

func nullableRuntimeString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
