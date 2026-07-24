package integrity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/observability"
	"kansoku.local/kansoku/internal/privacy"
)

// NewMigrationsUpToDateCheckFunc returns a MigrationsUpToDate closure for
// RollupFormulaDBIntegrityCheck that verifies both the shared data-platform
// ledger and Session 08 integrity ledger have an exact version/checksum
// match to their embedded migration sets.
func NewMigrationsUpToDateCheckFunc() func(ctx context.Context, pool *pgxpool.Pool) (bool, string, error) {
	return func(ctx context.Context, pool *pgxpool.Pool) (bool, string, error) {
		dataMigrations, err := dataplatform.LoadMigrations()
		if err != nil {
			return false, "", fmt.Errorf("load dataplatform migrations: %w", err)
		}
		dataExpected := make(map[string]string, len(dataMigrations))
		for _, migration := range dataMigrations {
			dataExpected[migration.Version] = migration.UpSHA256
		}
		if ok, detail, err := verifyMigrationLedger(ctx, pool, "schema_migrations", dataExpected); err != nil || !ok {
			return ok, "dataplatform: " + detail, err
		}

		integrityMigrations, err := LoadMigrations()
		if err != nil {
			return false, "", fmt.Errorf("load integrity migrations: %w", err)
		}
		integrityExpected := make(map[string]string, len(integrityMigrations))
		for _, migration := range integrityMigrations {
			integrityExpected[migration.Version] = migration.UpSHA256
		}
		if ok, detail, err := verifyMigrationLedger(ctx, pool, migrationLedgerTable, integrityExpected); err != nil || !ok {
			return ok, "integrity: " + detail, err
		}
		return true, "", nil
	}
}

func verifyMigrationLedger(ctx context.Context, pool *pgxpool.Pool, table string, expected map[string]string) (bool, string, error) {
	if table != "schema_migrations" && table != migrationLedgerTable {
		return false, "", fmt.Errorf("unsupported migration ledger %q", table)
	}
	rows, err := pool.Query(ctx, `SELECT version, checksum_sha256 FROM `+table)
	if err != nil {
		return false, "", fmt.Errorf("read %s ledger: %w", table, err)
	}
	defer rows.Close()
	applied := map[string]string{}
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return false, "", err
		}
		applied[version] = checksum
	}
	if err := rows.Err(); err != nil {
		return false, "", err
	}
	for version, checksum := range expected {
		appliedChecksum, ok := applied[version]
		if !ok {
			return false, fmt.Sprintf("migration %s not yet applied", version), nil
		}
		if appliedChecksum != checksum {
			return false, fmt.Sprintf("migration %s checksum mismatch: ledger=%s file=%s", version, appliedChecksum, checksum), nil
		}
	}
	for version := range applied {
		if _, known := expected[version]; !known {
			return false, fmt.Sprintf("migration %s exists in ledger but not this binary", version), nil
		}
	}
	return true, "", nil
}

// DefaultExpectedFormulas is the formula catalog stage_8 currently expects
// registered, matching internal/dataplatform's own
// MetricFamilyLatencyMS/FormulaVersionLatencyMS1 constants (the only
// rollup formula dataplatform ships as of Session 04). A later session
// extending the formula catalog updates this list alongside dataplatform's
// own constants, never a Session-08-private duplicate of the version
// number.
var DefaultExpectedFormulas = []FormulaVersionExpectation{
	{FormulaID: dataplatform.MetricFamilyLatencyMS, ExpectedVersion: 1},
}

// NewExpectedFormulasLookupFunc returns an ExpectedFormulas closure that
// always reports DefaultExpectedFormulas, for a caller wiring
// RollupFormulaDBIntegrityCheck without a more specific formula catalog
// source.
func NewExpectedFormulasLookupFunc() func(ctx context.Context, pool *pgxpool.Pool) ([]FormulaVersionExpectation, error) {
	return func(ctx context.Context, pool *pgxpool.Pool) ([]FormulaVersionExpectation, error) {
		return DefaultExpectedFormulas, nil
	}
}

// NewDiskForecastLookup returns a DiskForecastLookup reading real host disk
// usage for dataDir via the POSIX statfs(2) syscall (stdlib syscall
// package -- no new dependency, matching ADR 0011's "no new external
// dependency unless truly unavoidable"). ProjectedExhaustionWithinBudget is
// derived from a simple linear extrapolation: if used-fraction growth over
// the last sampleWindow already implies the budget will be crossed before
// the next scheduled daily cycle, it reports true. A caller with no prior
// sample yet (growthPerDay == 0) gets ProjectedExhaustionWithinBudget=false
// (never a fabricated exhaustion warning from a single sample).
func NewDiskForecastLookup(dataDir string, previousUsedFraction func() (float64, time.Time, bool)) DiskForecastLookup {
	return func(ctx context.Context, budget float64) (DiskForecast, error) {
		usedFraction, err := statfsUsedFraction(dataDir)
		if err != nil {
			return DiskForecast{}, fmt.Errorf("statfs %s: %w", dataDir, err)
		}
		forecast := DiskForecast{UsedFraction: usedFraction}
		if previousUsedFraction == nil {
			return forecast, nil
		}
		previous, at, ok := previousUsedFraction()
		if !ok || at.IsZero() {
			return forecast, nil
		}
		elapsedDays := time.Since(at).Hours() / 24
		if elapsedDays <= 0 {
			return forecast, nil
		}
		forecast.ProjectedExhaustionWithinBudget = projectedDiskBudgetCrossing(
			previous, usedFraction, elapsedDays, budget, 1,
		)
		// "before the next scheduled retention/backup cycle" -- this package's
		// own daily cadence (DefaultFreshnessWindow-adjacent, one calendar day)
		// is the bound used here. The projection targets the configured
		// threshold, not physical 100% capacity.
		return forecast, nil
	}
}

func projectedDiskBudgetCrossing(previous, current, elapsedDays, budget, forecastDays float64) bool {
	if budget <= 0 || budget > 1 {
		budget = DefaultDiskForecastBudget
	}
	if current >= budget {
		return true
	}
	if elapsedDays <= 0 || forecastDays <= 0 {
		return false
	}
	growthPerDay := (current - previous) / elapsedDays
	if growthPerDay <= 0 {
		return false
	}
	return current+growthPerDay*forecastDays >= budget
}

// statfsUsedFraction reports the fraction of dataDir's filesystem currently
// used, via the POSIX statfs(2) syscall.
func statfsUsedFraction(dataDir string) (float64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dataDir, &stat); err != nil {
		return 0, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	if total == 0 {
		return 0, fmt.Errorf("statfs reported zero total blocks for %s", dataDir)
	}
	used := total - free
	return float64(used) / float64(total), nil
}

// NewPrivacyCanaryLookup returns a PrivacyCanaryLookup that runs the
// raw-content privacy canary IN-PROCESS via internal/privacy's own
// DecodeAndExtract/SerializeAllSinks/ScanCanaries/ScanSecretFormats
// pipeline -- the exact Go-native functions cmd/privacy-canary/main.go
// already calls -- reading the same fixture file, rather than shelling out
// to a second script or a subprocess. This is "the Go equivalent" the TDD
// asks stage_9 to integrate as one of its own checks rather than a separate
// unrelated script.
func NewPrivacyCanaryLookup(fixturePath string, key []byte, clock func() time.Time) PrivacyCanaryLookup {
	return func(ctx context.Context) (bool, string, error) {
		raw, err := os.ReadFile(fixturePath)
		if err != nil {
			return false, "", fmt.Errorf("read privacy canary fixture: %w", err)
		}
		var input privacyCanaryFixture
		if err := json.Unmarshal(raw, &input); err != nil {
			return false, "", fmt.Errorf("decode privacy canary fixture: %w", err)
		}
		sanitizer, err := privacy.NewIngressSanitizer(key, privacy.DefaultLimits())
		if err != nil {
			return false, "", fmt.Errorf("construct sanitizer: %w", err)
		}
		if clock != nil {
			sanitizer.SetClockForTest(clock)
		}
		records, safeErr := sanitizer.DecodeAndExtract(bytes.NewReader(input.Payload), privacy.FixtureSourceSchema())
		if safeErr != nil {
			return false, "", fmt.Errorf("decode canary payload: %s", safeErr.Error())
		}
		snapshot, err := privacy.SerializeAllSinks(records, nil)
		if err != nil {
			return false, "", fmt.Errorf("serialize sinks: %w", err)
		}
		allCanaries := map[string]string{}
		for k, v := range input.Canaries {
			allCanaries[k] = v
		}
		for k, v := range input.TransformedCanaries {
			allCanaries[k] = v
		}
		canaryMatches := privacy.ScanCanaries(snapshot, allCanaries)
		secretMatches := privacy.ScanSecretFormats(snapshot)
		matchCount := 0
		for _, values := range canaryMatches {
			matchCount += len(values)
		}
		for _, values := range secretMatches {
			matchCount += len(values)
		}
		if matchCount > 0 {
			return true, fmt.Sprintf("raw_content_reached_durable_sink matches=%d", matchCount), nil
		}
		return false, fmt.Sprintf("clean sinks=%d records=%d", len(snapshot), len(records)), nil
	}
}

// NewSpoolIntegrityLookup reuses observability's own strict, read-only spool
// validator; it never replays or drains queued evidence.
func NewSpoolIntegrityLookup(path string, maxBytes int64) SpoolIntegrityLookup {
	return func(context.Context) error {
		return observability.CheckDurableSpool(path, maxBytes)
	}
}

// privacyCanaryFixture mirrors cmd/privacy-canary/main.go's own fixture
// shape (this package reads the identical fixture file, never a
// Session-08-private copy of the canary/secret payloads).
type privacyCanaryFixture struct {
	Canaries            map[string]string `json:"canaries"`
	TransformedCanaries map[string]string `json:"transformed_canaries"`
	Payload             json.RawMessage   `json:"payload"`
}
