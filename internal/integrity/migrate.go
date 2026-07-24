package integrity

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.(up|down)\.sql$`)

// Migration is one named, checksummed forward/backward SQL pair. This
// mirrors internal/dataplatform.Migration's shape exactly, per ADR 0011's
// instruction to follow the same migration-loader pattern rather than
// invent a second runner design.
type Migration struct {
	Version  string
	Up       string
	UpSHA256 string
	Down     string
}

// LoadMigrations reads the embedded migration pairs, ordered by version.
func LoadMigrations() ([]Migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	up := map[string]string{}
	down := map[string]string{}
	for _, entry := range entries {
		match := migrationName.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("migration file has unexpected name: %s", entry.Name())
		}
		data, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, err
		}
		version := match[1]
		if match[2] == "up" {
			up[version] = string(data)
		} else {
			down[version] = string(data)
		}
	}
	versions := make([]string, 0, len(up))
	for version := range up {
		if _, ok := down[version]; !ok {
			return nil, fmt.Errorf("migration %s has no down pair", version)
		}
		versions = append(versions, version)
	}
	sort.Strings(versions)
	migrations := make([]Migration, 0, len(versions))
	for _, version := range versions {
		sum := sha256.Sum256([]byte(up[version]))
		migrations = append(migrations, Migration{Version: version, Up: up[version], UpSHA256: hex.EncodeToString(sum[:]), Down: down[version]})
	}
	return migrations, nil
}

// migrationLedgerTable is deliberately distinct from
// internal/dataplatform's "schema_migrations" table name. Both packages
// share the same PostgreSQL instance (internal/dataplatform's *pgxpool.Pool
// is reused verbatim per ADR 0011) but each embeds its own
// migrations/*.sql directory starting numbering back at 0001; a shared
// ledger table would collide on that numeric primary key across packages.
// Using a package-scoped ledger table name keeps each package's migration
// history independently and unambiguously tracked in the one shared
// database, without introducing a second connection pool or a second
// migration *runner design* (LoadMigrations/Migrate/Downgrade here are
// structurally identical to internal/dataplatform's).
const migrationLedgerTable = "integrity_schema_migrations"

// Migrate applies every pending integrity migration inside its own
// transaction and records a checksum ledger entry, exactly mirroring
// internal/dataplatform.Migrate's checksum-fail-closed behavior.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migrations, err := LoadMigrations()
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+migrationLedgerTable+` (
		version TEXT PRIMARY KEY,
		checksum_sha256 TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create ledger: %w", err)
	}
	rows, err := pool.Query(ctx, `SELECT version, checksum_sha256 FROM `+migrationLedgerTable)
	if err != nil {
		return fmt.Errorf("read ledger: %w", err)
	}
	applied := map[string]string{}
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
				return fmt.Errorf("migration %s checksum mismatch: ledger has changed or migration file was edited after being applied", migration.Version)
			}
			continue
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, migration.Up); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", migration.Version, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+migrationLedgerTable+` (version, checksum_sha256) VALUES ($1, $2)`, migration.Version, migration.UpSHA256); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", migration.Version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", migration.Version, err)
		}
	}
	return nil
}

// Downgrade reverses migrations down to (and excluding) target, in reverse
// version order. An empty target reverses every migration.
func Downgrade(ctx context.Context, pool *pgxpool.Pool, target string) error {
	migrations, err := LoadMigrations()
	if err != nil {
		return err
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version > migrations[j].Version })
	for _, migration := range migrations {
		if migration.Version <= target {
			break
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, migration.Down); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("downgrade migration %s: %w", migration.Version, err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM `+migrationLedgerTable+` WHERE version = $1`, migration.Version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("unrecord migration %s: %w", migration.Version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

var ErrNoPendingMigrations = errors.New("no pending migrations")
