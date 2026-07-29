package runtime

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/observability"
)

type CapacityMeasure struct {
	CurrentBytes        int64      `json:"current_bytes"`
	BudgetBytes         int64      `json:"budget_bytes"`
	Percentage          float64    `json:"percentage"`
	State               string     `json:"state"`
	GrowthBytesPerDay   *float64   `json:"growth_bytes_per_day"`
	EstimatedExhaustion *time.Time `json:"estimated_exhaustion_at"`
}

type StorageComponent struct {
	Bytes      *int64 `json:"bytes"`
	ValueState string `json:"value_state"`
	Notes      string `json:"notes,omitempty"`
}

type FilesystemCapacity struct {
	AvailableBytes int64   `json:"available_bytes"`
	TotalBytes     int64   `json:"total_bytes"`
	FreePercentage float64 `json:"free_percentage"`
	MinimumBytes   int64   `json:"minimum_recommended_free_bytes"`
	State          string  `json:"state"`
}

type CapacitySnapshot struct {
	Database       CapacityMeasure                    `json:"database_budget"`
	TableHeap      StorageComponent                   `json:"table_heap"`
	Indexes        StorageComponent                   `json:"indexes"`
	WALHeadroom    StorageComponent                   `json:"wal_headroom"`
	TemporaryFiles StorageComponent                   `json:"temporary_files"`
	Backups        StorageComponent                   `json:"backups"`
	EmergencySpool map[observability.SourceKind]int64 `json:"emergency_spool_bytes"`
	Checkpoint     CapacityMeasure                    `json:"checkpoint_budget"`
	Filesystem     FilesystemCapacity                 `json:"filesystem"`
	Numerator      int                                `json:"numerator"`
	Denominator    int                                `json:"denominator"`
	Exclusions     []string                           `json:"exclusions"`
	Completeness   string                             `json:"completeness"`
}

func collectCapacitySnapshot(
	ctx context.Context,
	pool *pgxpool.Pool,
	config Config,
	queue QueueMetrics,
) (CapacitySnapshot, error) {
	var result CapacitySnapshot
	var databaseBytes, heapBytes, indexBytes, tempBytes int64
	if err := pool.QueryRow(ctx, `
		SELECT
			pg_database_size(current_database()),
			COALESCE((
				SELECT sum(pg_relation_size(c.oid))
				FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
				WHERE n.nspname NOT IN ('pg_catalog','information_schema')
				  AND c.relkind IN ('r','m')
			), 0),
			COALESCE((
				SELECT sum(pg_relation_size(i.indexrelid))
				FROM pg_index i
				JOIN pg_class c ON c.oid = i.indexrelid
				JOIN pg_namespace n ON n.oid = c.relnamespace
				WHERE n.nspname NOT IN ('pg_catalog','information_schema')
			), 0),
			COALESCE((
				SELECT temp_bytes FROM pg_stat_database
				WHERE datname = current_database()
			), 0)
	`).Scan(&databaseBytes, &heapBytes, &indexBytes, &tempBytes); err != nil {
		return CapacitySnapshot{}, err
	}
	result.Database = databaseBudgetMeasure(databaseBytes, config, nil)
	result.TableHeap = observedStorageComponent(heapBytes)
	result.Indexes = observedStorageComponent(indexBytes)
	result.TemporaryFiles = StorageComponent{
		Bytes: &tempBytes, ValueState: "observed",
		Notes: "cumulative PostgreSQL temporary bytes; current temporary-file occupancy is not observed",
	}
	result.WALHeadroom = StorageComponent{
		ValueState: "not_observed",
		Notes:      "WAL occupies shared database storage; a safe independent WAL reservation is not observable from this role",
	}
	result.Exclusions = append(result.Exclusions,
		"current WAL occupancy and rollback headroom are not independently observed",
		"temporary-file value is cumulative rather than current occupancy",
	)
	result.Numerator = 3
	result.Denominator = 5

	if backupBytes, err := regularTreeBytes(config.BackupDir); err == nil {
		result.Backups = observedStorageComponent(backupBytes)
		result.Numerator++
	} else {
		result.Backups = StorageComponent{
			ValueState: "not_observed",
			Notes:      "backup artifact occupancy is unavailable",
		}
		result.Exclusions = append(result.Exclusions, "backup artifact occupancy unavailable")
	}
	result.EmergencySpool = make(map[observability.SourceKind]int64, len(queue.SpoolBytes))
	var totalSpool int64
	for source, bytes := range queue.SpoolBytes {
		result.EmergencySpool[source] = bytes
		totalSpool += bytes
	}
	checkpointBytes := int64(0)
	checkpointInfo, checkpointErr := os.Stat(filepath.Join(config.DataDir, "checkpoints", "state.json"))
	if checkpointErr == nil {
		checkpointBytes = checkpointInfo.Size()
	} else if !errors.Is(checkpointErr, os.ErrNotExist) {
		return CapacitySnapshot{}, checkpointErr
	}
	result.Checkpoint = CapacityMeasure{
		CurrentBytes: checkpointBytes, BudgetBytes: config.CheckpointStateMaxBytes,
		Percentage: percentage(checkpointBytes, config.CheckpointStateMaxBytes),
		State:      budgetState(checkpointBytes, config.CheckpointStateMaxBytes, .70, .85, .95),
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(config.DataDir, &stat); err != nil {
		return CapacitySnapshot{}, err
	}
	blockSize := int64(stat.Bsize)
	available := int64(stat.Bavail) * blockSize
	total := int64(stat.Blocks) * blockSize
	freePercentage := percentage(available, total)
	filesystemState := "pass"
	if available < config.StoragePreflightMinFreeBytes {
		filesystemState = "degraded"
	}
	if freePercentage < 20 {
		filesystemState = "critical"
	}
	result.Filesystem = FilesystemCapacity{
		AvailableBytes: available, TotalBytes: total, FreePercentage: freePercentage,
		MinimumBytes: config.StoragePreflightMinFreeBytes, State: filesystemState,
	}

	sampleAt := time.Now().UTC().Truncate(time.Hour)
	backupSample := result.Backups.Bytes
	if _, err := pool.Exec(ctx, `
		INSERT INTO runtime_capacity_samples (
			sampled_at, database_bytes, index_bytes, backup_bytes,
			emergency_spool_bytes, checkpoint_bytes, filesystem_available_bytes
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (sampled_at) DO UPDATE SET
			database_bytes = EXCLUDED.database_bytes,
			index_bytes = EXCLUDED.index_bytes,
			backup_bytes = EXCLUDED.backup_bytes,
			emergency_spool_bytes = EXCLUDED.emergency_spool_bytes,
			checkpoint_bytes = EXCLUDED.checkpoint_bytes,
			filesystem_available_bytes = EXCLUDED.filesystem_available_bytes
	`, sampleAt, databaseBytes, indexBytes, backupSample, totalSpool, checkpointBytes, available); err != nil {
		return CapacitySnapshot{}, err
	}
	var earlierAt time.Time
	var earlierBytes int64
	err := pool.QueryRow(ctx, `
		SELECT sampled_at, database_bytes
		FROM runtime_capacity_samples
		WHERE sampled_at < $1
		ORDER BY sampled_at ASC
		LIMIT 1
	`, sampleAt).Scan(&earlierAt, &earlierBytes)
	if err == nil {
		days := sampleAt.Sub(earlierAt).Hours() / 24
		if days > 0 {
			growth := float64(databaseBytes-earlierBytes) / days
			result.Database.GrowthBytesPerDay = &growth
			if growth > 0 && databaseBytes < config.DatabaseSoftLimitBytes {
				remainingDays := float64(config.DatabaseSoftLimitBytes-databaseBytes) / growth
				exhaustion := sampleAt.Add(time.Duration(remainingDays * float64(24*time.Hour)))
				result.Database.EstimatedExhaustion = &exhaustion
			}
		}
	}
	result.Completeness = "partial"
	if result.Numerator == result.Denominator {
		result.Completeness = "complete"
	}
	return result, nil
}

func databaseBudgetMeasure(current int64, config Config, growth *float64) CapacityMeasure {
	return CapacityMeasure{
		CurrentBytes: current, BudgetBytes: config.DatabaseSoftLimitBytes,
		Percentage: percentage(current, config.DatabaseSoftLimitBytes),
		State: budgetState(
			current, config.DatabaseSoftLimitBytes,
			config.DatabaseBudgetWarning, config.DatabaseBudgetDegraded,
			config.DatabaseBudgetCritical,
		),
		GrowthBytesPerDay: growth,
	}
}

func budgetState(current, budget int64, warning, degraded, critical float64) string {
	if budget <= 0 {
		return "unknown"
	}
	ratio := float64(current) / float64(budget)
	switch {
	case ratio >= critical:
		return "critical"
	case ratio >= degraded:
		return "degraded"
	case ratio >= warning:
		return "warning"
	default:
		return "pass"
	}
}

func percentage(current, budget int64) float64 {
	if budget <= 0 {
		return 0
	}
	return math.Round((float64(current)/float64(budget))*10000) / 100
}

func observedStorageComponent(value int64) StorageComponent {
	return StorageComponent{Bytes: &value, ValueState: "observed"}
}

func regularTreeBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}
