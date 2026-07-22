package dataplatform

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect opens a bounded pgx pool for the given DSN. Callers own the
// pool's lifetime and must call Close.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// SeedFormulaVersion registers formula_versions row (formula_id, version),
// matching contracts/data-platform/rollups.yaml `formula_registry`.
// Formula rows are append-only: a second call with the same
// (formula_id, version) and identical content is idempotent; a call with
// different content for the same key fails so a formula can never be
// silently mutated in place.
func SeedFormulaVersion(ctx context.Context, pool *pgxpool.Pool, formulaID string, version int, sqlTemplate, unit string, dimensions []string, numerator, denominator, population *string, minimumSample int, allowedCompleteness []string, formatting *string) error {
	dimsJSON, err := marshalStrings(dimensions)
	if err != nil {
		return err
	}
	completenessJSON, err := marshalStrings(allowedCompleteness)
	if err != nil {
		return err
	}
	tag, err := pool.Exec(ctx, `
		INSERT INTO formula_versions (formula_id, version, sql_template, unit, dimensions, numerator, denominator, population, minimum_sample, allowed_completeness, formatting)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (formula_id, version) DO NOTHING
	`, formulaID, version, sqlTemplate, unit, dimsJSON, numerator, denominator, population, minimumSample, completenessJSON, formatting)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var existingSQL string
	if err := pool.QueryRow(ctx, `SELECT sql_template FROM formula_versions WHERE formula_id = $1 AND version = $2`, formulaID, version).Scan(&existingSQL); err != nil {
		return err
	}
	if existingSQL != sqlTemplate {
		return errFormulaImmutable(formulaID, version)
	}
	return nil
}

type formulaImmutableError struct {
	formulaID string
	version   int
}

func (e *formulaImmutableError) Error() string {
	return "formula version is append-only and cannot be edited in place: " + e.formulaID
}

func errFormulaImmutable(formulaID string, version int) error {
	return &formulaImmutableError{formulaID: formulaID, version: version}
}

func marshalStrings(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
