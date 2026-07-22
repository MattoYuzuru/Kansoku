package dataplatform

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PartitionedTables lists the fact tables partitioned monthly by observed_at,
// matching contracts/data-platform/schema.yaml `partitioning.partitioned_tables`.
var PartitionedTables = []string{"events", "event_evidence", "model_operations", "token_usage", "tool_calls", "mcp_connections"}

func partitionName(table string, month time.Time) string {
	return fmt.Sprintf("%s_p%04d%02d", table, month.Year(), int(month.Month()))
}

func monthBounds(month time.Time) (time.Time, time.Time) {
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	return start, end
}

// EnsurePartition creates the monthly partition for table covering the month
// containing `at`, if it does not already exist. Partition creation is
// idempotent and safe to call before every insert window.
//
// The partition bounds are embedded as literals rather than bind
// parameters: PostgreSQL's DDL grammar does not support genuine bind
// parameters inside `FOR VALUES FROM (...) TO (...)` (its parser reports
// zero parameters for the statement during Describe), so pgx's extended
// query protocol rejects `$1`/`$2` placeholders here with "mismatched param
// and argument count". `start`/`end` are always program-computed UTC month
// boundaries (see monthBounds), never raw external input, so formatting
// them as a quoted ISO-8601 literal via pgTimestampLiteral is safe.
func EnsurePartition(ctx context.Context, pool *pgxpool.Pool, table string, at time.Time) error {
	name := partitionName(table, at)
	start, end := monthBounds(at)
	_, err := pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM (%s) TO (%s)`,
		pgIdent(name), pgIdent(table), pgTimestampLiteral(start), pgTimestampLiteral(end),
	))
	return err
}

// pgTimestampLiteral formats a UTC time.Time as a quoted `timestamptz`
// literal safe for direct embedding in DDL text. Only ever called with
// program-computed month boundaries, never external input.
func pgTimestampLiteral(at time.Time) string {
	return "'" + at.UTC().Format("2006-01-02T15:04:05.000000Z") + "'"
}

// EnsureCurrentAndNextPartitions creates the partition for `now`'s month and
// the following month for every partitioned fact table, matching the
// `creation` policy in contracts/data-platform/schema.yaml.
func EnsureCurrentAndNextPartitions(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	for _, table := range PartitionedTables {
		if err := EnsurePartition(ctx, pool, table, now); err != nil {
			return fmt.Errorf("ensure current partition for %s: %w", table, err)
		}
		if err := EnsurePartition(ctx, pool, table, now.AddDate(0, 1, 0)); err != nil {
			return fmt.Errorf("ensure next partition for %s: %w", table, err)
		}
	}
	return nil
}

// DropPartitionsOlderThan drops whole monthly partitions of table whose
// entire range is at or before the retention horizon. It never issues a
// row-by-row DELETE against a partitioned fact table. Returns the dropped
// partition names for audit logging.
func DropPartitionsOlderThan(ctx context.Context, pool *pgxpool.Pool, table string, horizon time.Time) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT c.relname
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = $1
	`, table)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	rows.Close()
	var dropped []string
	for _, name := range names {
		var upperBound time.Time
		bound, err := partitionUpperBound(ctx, pool, name)
		if err != nil {
			return dropped, err
		}
		upperBound = bound
		if !upperBound.After(horizon) {
			if _, err := pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", pgIdent(name))); err != nil {
				return dropped, fmt.Errorf("drop partition %s: %w", name, err)
			}
			dropped = append(dropped, name)
		}
	}
	return dropped, nil
}

// partitionBoundPattern matches PostgreSQL's pg_get_expr rendering of a
// range partition bound, e.g.
// `FOR VALUES FROM ('2026-01-01 00:00:00+00') TO ('2026-02-01 00:00:00+00')`.
// fmt.Sscanf's "%[...]" scanset verb does not compose the way a C scanf
// caller would expect once it is interleaved with literal quote characters
// (it does not stop at the literal "'" that follows), so it silently
// consumed the wrong span and every retention run failed with "bad verb
// '%[' for string". A regexp with an explicit non-quote character class is
// unambiguous and independently testable without a live Postgres connection.
var partitionBoundPattern = regexp.MustCompile(`FOR VALUES FROM \('([^']*)'\) TO \('([^']*)'\)`)

func partitionUpperBound(ctx context.Context, pool *pgxpool.Pool, partition string) (time.Time, error) {
	var expr string
	err := pool.QueryRow(ctx, `
		SELECT pg_get_expr(c.relpartbound, c.oid)
		FROM pg_class c WHERE c.relname = $1
	`, partition).Scan(&expr)
	if err != nil {
		return time.Time{}, err
	}
	return parsePartitionUpperBound(expr)
}

// parsePartitionUpperBound is the pure, connection-free half of
// partitionUpperBound so the regexp/time-layout parsing logic is directly
// unit-testable without a live Postgres instance.
func parsePartitionUpperBound(expr string) (time.Time, error) {
	// Format: FOR VALUES FROM ('2026-01-01 00:00:00+00') TO ('2026-02-01 00:00:00+00')
	match := partitionBoundPattern.FindStringSubmatch(expr)
	if match == nil {
		return time.Time{}, fmt.Errorf("parse partition bound %q: unrecognized format", expr)
	}
	upper := match[2]
	return time.Parse("2006-01-02 15:04:05-07", upper)
}

// pgIdent quotes a Postgres identifier we generated ourselves (table/month
// derived names only; never used with untrusted input).
func pgIdent(name string) string {
	return `"` + name + `"`
}
