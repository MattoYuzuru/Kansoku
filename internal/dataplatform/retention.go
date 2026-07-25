package dataplatform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultRetentionHorizonDays mirrors
// contracts/data-platform/retention.yaml `event_expiration.default_horizon_days`.
const DefaultRetentionHorizonDays = 400

// ApplyRetention drops whole expired monthly partitions for every
// partitioned fact table and records an audited retention action per drop,
// matching `event_expiration.audited_actions` in
// contracts/data-platform/retention.yaml. Row-by-row DELETE is never used.
func ApplyRetention(ctx context.Context, pool *pgxpool.Pool, now time.Time, horizonDays int) (map[string][]string, error) {
	horizon := now.AddDate(0, 0, -horizonDays)
	dropped := map[string][]string{}
	for _, table := range PartitionedTables {
		names, err := DropPartitionsOlderThan(ctx, pool, table, horizon)
		if err != nil {
			return dropped, fmt.Errorf("apply retention for %s: %w", table, err)
		}
		if len(names) > 0 {
			dropped[table] = names
		}
	}
	return dropped, nil
}

// BackupManifest mirrors contracts/data-platform/retention.yaml
// `backup.manifest_fields`.
type BackupManifest struct {
	AppVersion            string    `json:"app_version"`
	SchemaVersion         string    `json:"schema_version"`
	FormulaRegistryVersion string   `json:"formula_registry_version"`
	AdapterVersions       []string  `json:"adapter_versions"`
	ChecksumSHA256        string    `json:"checksum_sha256"`
	PrivacyPolicySHA256   string    `json:"privacy_policy_sha256"`
	CreatedAt             time.Time `json:"created_at"`
}

// LogicalBackup is a deterministic, checksum-verifiable logical export of
// every table this session owns, keyed by table name. It stands in for the
// Session 09 PostgreSQL-native pg_dump-based backup strategy: the manifest
// shape and restore-test verification method are fixed now so Session 09 can
// swap the transport without changing the contract.
type LogicalBackup struct {
	Manifest BackupManifest      `json:"manifest"`
	Columns  map[string][]string `json:"columns"`
	Tables   map[string][][]any  `json:"tables"`
}

var backupTables = []string{
	"devices", "agent_installations", "agent_surfaces", "projects", "sessions", "turns",
	"components", "component_versions", "component_installations", "component_relations",
	"adapter_versions", "source_instances",
	"inventory_snapshots", "inventory_nodes", "inventory_edges",
	"component_inventory_state", "inventory_collection_status",
	"events", "event_evidence",
	"metric_rollups_hourly", "metric_rollups_daily", "rollup_status", "formula_versions",
}

// CreateBackup exports every backupTables row set deterministically (stable
// column and row order) and computes a manifest checksum over that export.
func CreateBackup(ctx context.Context, pool *pgxpool.Pool, appVersion, formulaRegistryVersion, privacyPolicySHA256 string, adapterVersions []string, now time.Time) (LogicalBackup, error) {
	backup := LogicalBackup{Tables: map[string][][]any{}, Columns: map[string][]string{}}
	for _, table := range backupTables {
		rows, err := pool.Query(ctx, fmt.Sprintf("SELECT * FROM %s ORDER BY 1", pgIdent(table)))
		if err != nil {
			return LogicalBackup{}, fmt.Errorf("export %s: %w", table, err)
		}
		fields := rows.FieldDescriptions()
		columns := make([]string, len(fields))
		for i, field := range fields {
			columns[i] = string(field.Name)
		}
		var exported [][]any
		for rows.Next() {
			values, err := rows.Values()
			if err != nil {
				rows.Close()
				return LogicalBackup{}, err
			}
			row := make([]any, len(fields))
			copy(row, values)
			exported = append(exported, row)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return LogicalBackup{}, err
		}
		backup.Tables[table] = exported
		backup.Columns[table] = columns
	}
	encoded, err := json.Marshal(backup.Tables)
	if err != nil {
		return LogicalBackup{}, err
	}
	sum := sha256.Sum256(encoded)
	backup.Manifest = BackupManifest{
		AppVersion: appVersion, SchemaVersion: SchemaSpecVersion, FormulaRegistryVersion: formulaRegistryVersion,
		AdapterVersions: adapterVersions, ChecksumSHA256: hex.EncodeToString(sum[:]), PrivacyPolicySHA256: privacyPolicySHA256,
		CreatedAt: now.UTC(),
	}
	return backup, nil
}

// VerifyBackupChecksum recomputes the table-export checksum and compares it
// against the manifest, detecting silent corruption/tampering.
func VerifyBackupChecksum(backup LogicalBackup) error {
	encoded, err := json.Marshal(backup.Tables)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(encoded)
	if hex.EncodeToString(sum[:]) != backup.Manifest.ChecksumSHA256 {
		return fmt.Errorf("backup checksum mismatch")
	}
	return nil
}

// RestoreCounts summarizes a restore test's row counts per table for the
// caller to compare against the source counts, matching
// contracts/data-platform/retention.yaml `backup.restore_test.verifies`.
type RestoreCounts map[string]int

func CountRows(backup LogicalBackup) RestoreCounts {
	counts := RestoreCounts{}
	for table, rows := range backup.Tables {
		counts[table] = len(rows)
	}
	return counts
}

// partitionedTableSet is PartitionedTables as a lookup set.
var partitionedTableSet = func() map[string]bool {
	set := make(map[string]bool, len(PartitionedTables))
	for _, table := range PartitionedTables {
		set[table] = true
	}
	return set
}()

// RestoreBackup loads a LogicalBackup into `pool` (expected to already have
// migrations applied, matching contracts/data-platform/retention.yaml
// `backup.restore_test.target == isolated_temporary_database`) in the same
// dependency order CreateBackup exported them, so foreign keys are always
// satisfied. For partitioned fact tables it first ensures the month
// partition covering each row's `observed_at` exists, since a restored
// database starts with no partitions beyond the ones Migrate creates.
func RestoreBackup(ctx context.Context, pool *pgxpool.Pool, backup LogicalBackup) error {
	for _, table := range backupTables {
		columns := backup.Columns[table]
		rows := backup.Tables[table]
		if len(columns) == 0 || len(rows) == 0 {
			continue
		}
		observedAtIndex := -1
		if partitionedTableSet[table] {
			for i, column := range columns {
				if column == "observed_at" {
					observedAtIndex = i
					break
				}
			}
			if observedAtIndex == -1 {
				return fmt.Errorf("restore %s: partitioned table export is missing observed_at", table)
			}
		}
		quotedColumns := make([]string, len(columns))
		placeholders := make([]string, len(columns))
		for i, column := range columns {
			quotedColumns[i] = pgIdent(column)
			placeholders[i] = fmt.Sprintf("$%d", i+1)
		}
		insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", pgIdent(table), joinStrings(quotedColumns, ", "), joinStrings(placeholders, ", "))
		for _, row := range rows {
			if observedAtIndex != -1 {
				observedAt, ok := row[observedAtIndex].(time.Time)
				if !ok {
					return fmt.Errorf("restore %s: observed_at column is not a timestamp", table)
				}
				if err := EnsurePartition(ctx, pool, table, observedAt); err != nil {
					return fmt.Errorf("restore %s: ensure partition: %w", table, err)
				}
			}
			if _, err := pool.Exec(ctx, insertSQL, row...); err != nil {
				return fmt.Errorf("restore %s: insert row: %w", table, err)
			}
		}
	}
	return nil
}

func joinStrings(values []string, sep string) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += sep
		}
		out += v
	}
	return out
}
