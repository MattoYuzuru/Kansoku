package runtime

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var runtimeMigrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.(up|down)\.sql$`)

type Migration struct {
	Version  string
	Up       string
	UpSHA256 string
	Down     string
}

func LoadMigrations() ([]Migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	up, down := map[string]string{}, map[string]string{}
	for _, entry := range entries {
		match := runtimeMigrationName.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("runtime migration file has unexpected name: %s", entry.Name())
		}
		raw, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, err
		}
		if match[2] == "up" {
			up[match[1]] = string(raw)
		} else {
			down[match[1]] = string(raw)
		}
	}
	var versions []string
	for version := range up {
		if _, ok := down[version]; !ok {
			return nil, fmt.Errorf("runtime migration %s has no down pair", version)
		}
		versions = append(versions, version)
	}
	sort.Strings(versions)
	out := make([]Migration, 0, len(versions))
	for _, version := range versions {
		sum := sha256.Sum256([]byte(up[version]))
		out = append(out, Migration{Version: version, Up: up[version], Down: down[version], UpSHA256: hex.EncodeToString(sum[:])})
	}
	return out, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("runtime_migration_pool_required")
	}
	migrations, err := LoadMigrations()
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS runtime_schema_migrations (
		version TEXT PRIMARY KEY,
		checksum_sha256 TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create runtime migration ledger: %w", err)
	}
	applied := map[string]string{}
	rows, err := pool.Query(ctx, `SELECT version, checksum_sha256 FROM runtime_schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return err
		}
		applied[version] = checksum
	}
	rows.Close()
	for _, migration := range migrations {
		if checksum, ok := applied[migration.Version]; ok {
			if checksum != migration.UpSHA256 {
				return fmt.Errorf("runtime migration %s checksum mismatch", migration.Version)
			}
			continue
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, migration.Up); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply runtime migration %s: %w", migration.Version, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO runtime_schema_migrations (version, checksum_sha256) VALUES ($1,$2)`, migration.Version, migration.UpSHA256); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func PreflightMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("runtime_preflight_pool_required")
	}
	var rawVersion string
	if err := pool.QueryRow(ctx, `SHOW server_version_num`).Scan(&rawVersion); err != nil {
		return errors.New("database_version_probe_failed")
	}
	version, err := strconv.Atoi(rawVersion)
	if err != nil {
		return errors.New("database_version_probe_failed")
	}
	if version < 180000 || version >= 190000 {
		return fmt.Errorf("unsupported_postgresql_version")
	}
	return nil
}
