package integrity

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/adaptersdk"
)

// RollupFormulaDBIntegrityCheckID is the check_id every
// RollupFormulaDBIntegrityCheck outcome reports, matching
// stage_8_rollup_formula_and_db_integrity and incident-and-health.yaml's
// "storage_rollup_health" health dimension (jointly sourced from stage_8 and
// stage_9).
const RollupFormulaDBIntegrityCheckID = "stage_8_rollup_formula_and_db_integrity"

// storageOpsCapabilityID and storageOpsInstallationID are the fixed identity
// stage_8/stage_9 file their evidence under: these checks examine the
// SHARED internal/dataplatform system of record, not any one adapter
// installation, exactly like SyntheticPipelineCheck's fixed
// syntheticProbeCapabilityID/syntheticProbeInstallationID.
const (
	storageOpsCapabilityID   = string(adaptersdk.CapabilityIngestionHistoricalImport)
	storageOpsInstallationID = "kansoku-data-platform"
)

// RollupFreshnessBudget bounds how large RepairQueueDepth may grow before
// stage_8 reports rollup_stale, matching
// fault-injection-and-live-canary.yaml's "delayed_rollup" claim: "calls
// internal/dataplatform.RepairQueueDepth and flags a rollup watermark older
// than its freshness budget". Depth is used as a bounded proxy for staleness
// here (this check calls RepairQueueDepth directly, never reimplementing
// it); a caller may override via RollupFormulaDBIntegrityCheck.MaxRepairQueueDepth.
const RollupFreshnessBudget = 500

const (
	DefaultRepairAgeBudget       = 15 * time.Minute
	DefaultRollupWatermarkBudget = 36 * time.Hour
)

type RollupFreshnessEvidence struct {
	OldestRepairEnqueuedAt time.Time
	OldestPendingWatermark time.Time
}

type RollupFreshnessLookup func(ctx context.Context, pool *pgxpool.Pool) (RollupFreshnessEvidence, error)

// FormulaVersionExpectation is one (formula_id, expected version) pair this
// check verifies is present in formula_versions, matching stage_8's
// "formula-version mismatch" detection. A caller supplies the set of
// formulas currently expected to be registered (e.g.
// dataplatform.MetricFamilyLatencyMS / FormulaVersionLatencyMS1); this
// package never hardcodes a formula catalog of its own.
type FormulaVersionExpectation struct {
	FormulaID       string
	ExpectedVersion int
}

// pgxQuerier is satisfied by both *pgxpool.Pool and pgx.Tx, mirroring
// internal/dataplatform's own pgxQuerier so this check can call
// RepairQueueDepth directly against whichever handle a caller supplies.
type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) interface {
		Scan(dest ...any) error
	}
}

// RollupFormulaDBIntegrityCheck implements stage_8_rollup_formula_and_db_integrity:
// it calls internal/dataplatform's RepairQueueDepth directly (never
// reimplementing rollup-repair bookkeeping) to detect a stale rollup
// watermark, verifies every expected formula_versions row is present
// (formula_version_mismatch), and confirms the data-platform migration
// ledger has no pending migrations (db_integrity_violation) -- all via
// read-only queries, matching stage_registry's mutates_target=false for
// this stage.
type RollupFormulaDBIntegrityCheck struct {
	Pool                *pgxpool.Pool
	RepairQueueDepth    func(ctx context.Context, pool *pgxpool.Pool) (int64, error)
	RollupFreshness     RollupFreshnessLookup
	ExpectedFormulas    func(ctx context.Context, pool *pgxpool.Pool) ([]FormulaVersionExpectation, error)
	MigrationsUpToDate  func(ctx context.Context, pool *pgxpool.Pool) (bool, string, error)
	MaxRepairQueueDepth int64
	MaxRepairAge        time.Duration
	MaxWatermarkAge     time.Duration
	Now                 func() time.Time
}

var _ Check = (*RollupFormulaDBIntegrityCheck)(nil)

// NewRollupFormulaDBIntegrityCheck constructs a RollupFormulaDBIntegrityCheck.
// Dependencies deliberately remain nil when they were not supplied. Evaluate
// turns that absence into a visible failing dependency finding: a production
// audit must never manufacture a green rollup/database result from a zero
// depth, empty formula set, or assumed-current migration ledger.
func NewRollupFormulaDBIntegrityCheck(pool *pgxpool.Pool, repairQueueDepth func(context.Context, *pgxpool.Pool) (int64, error), expectedFormulas func(context.Context, *pgxpool.Pool) ([]FormulaVersionExpectation, error), migrationsUpToDate func(context.Context, *pgxpool.Pool) (bool, string, error)) *RollupFormulaDBIntegrityCheck {
	return &RollupFormulaDBIntegrityCheck{
		Pool: pool, RepairQueueDepth: repairQueueDepth, ExpectedFormulas: expectedFormulas,
		RollupFreshness: NewRollupFreshnessLookup(), MigrationsUpToDate: migrationsUpToDate,
		MaxRepairQueueDepth: RollupFreshnessBudget, MaxRepairAge: DefaultRepairAgeBudget,
		MaxWatermarkAge: DefaultRollupWatermarkBudget, Now: time.Now,
	}
}

func NewRollupFreshnessLookup() RollupFreshnessLookup {
	return func(ctx context.Context, pool *pgxpool.Pool) (RollupFreshnessEvidence, error) {
		var oldestRepair, oldestWatermark *time.Time
		if err := pool.QueryRow(ctx, `SELECT min(enqueued_at) FROM rollup_repair_queue`).Scan(&oldestRepair); err != nil {
			return RollupFreshnessEvidence{}, err
		}
		if err := pool.QueryRow(ctx, `
			SELECT min(rollup_watermark)
			FROM rollup_status
			WHERE late_events_pending > 0
		`).Scan(&oldestWatermark); err != nil {
			return RollupFreshnessEvidence{}, err
		}
		evidence := RollupFreshnessEvidence{}
		if oldestRepair != nil {
			evidence.OldestRepairEnqueuedAt = oldestRepair.UTC()
		}
		if oldestWatermark != nil {
			evidence.OldestPendingWatermark = oldestWatermark.UTC()
		}
		return evidence, nil
	}
}

func (c *RollupFormulaDBIntegrityCheck) StageID() StageID { return Stage8RollupFormulaAndDBIntegrity }
func (c *RollupFormulaDBIntegrityCheck) CheckID() string  { return RollupFormulaDBIntegrityCheckID }

func (c *RollupFormulaDBIntegrityCheck) validateProductionReady(sharedPool *pgxpool.Pool) error {
	if c == nil || c.Pool == nil || c.Pool != sharedPool {
		return fmt.Errorf("rollup integrity must reuse the assembly PostgreSQL pool")
	}
	if c.RepairQueueDepth == nil || c.RollupFreshness == nil || c.ExpectedFormulas == nil || c.MigrationsUpToDate == nil || c.Now == nil {
		return fmt.Errorf("rollup, formula, migration, and clock dependencies are required")
	}
	return nil
}

// Targets always returns exactly one fixed target: this check examines the
// shared data-platform system of record, not any one adapter installation.
func (c *RollupFormulaDBIntegrityCheck) Targets(_ context.Context, _ CheckInput) ([]CheckTarget, error) {
	return []CheckTarget{{CapabilityID: storageOpsCapabilityID, InstallationID: storageOpsInstallationID}}, nil
}

func (c *RollupFormulaDBIntegrityCheck) Evaluate(ctx context.Context, in CheckInput, _ CheckTarget) (CheckOutcome, error) {
	now := c.Now()
	if !in.Now.IsZero() {
		now = in.Now
	}
	if c.Pool == nil {
		return CheckOutcome{
			CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusFail,
			Category: string(FailureClassDBIntegrityViolation), DetailRef: "dataplatform_pool_not_wired", ObservedAt: now,
		}, nil
	}
	if c.RepairQueueDepth == nil || c.RollupFreshness == nil || c.ExpectedFormulas == nil || c.MigrationsUpToDate == nil {
		return CheckOutcome{
			CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusFail,
			Category: string(FailureClassDBIntegrityViolation), DetailRef: "rollup_integrity_dependency_not_wired", ObservedAt: now,
		}, nil
	}

	depth, err := c.RepairQueueDepth(ctx, c.Pool)
	if err != nil {
		return CheckOutcome{
			CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusFail,
			Category: string(FailureClassDBIntegrityViolation), DetailRef: "repair_queue_depth_query_failed",
			ObservedAt: now,
		}, nil
	}
	maxDepth := c.MaxRepairQueueDepth
	if maxDepth <= 0 {
		maxDepth = RollupFreshnessBudget
	}
	if outcome := classifyRepairQueueDepth(depth, maxDepth, now); outcome.Status == CheckStatusFail {
		return outcome, nil
	}
	freshness, err := c.RollupFreshness(ctx, c.Pool)
	if err != nil {
		return CheckOutcome{
			CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusFail,
			Category: string(FailureClassDBIntegrityViolation), DetailRef: "rollup_freshness_query_failed", ObservedAt: now,
		}, nil
	}
	if depth > 0 {
		repairBudget := c.MaxRepairAge
		if repairBudget <= 0 {
			repairBudget = DefaultRepairAgeBudget
		}
		if freshness.OldestRepairEnqueuedAt.IsZero() {
			return rollupStaleOutcome(now, "pending_repair_age_missing"), nil
		}
		if age := now.Sub(freshness.OldestRepairEnqueuedAt); age > repairBudget {
			return rollupStaleOutcome(now, fmt.Sprintf("oldest_repair_age=%s exceeds_budget=%s", age, repairBudget)), nil
		}
	}
	// A persisted late_events_pending signal is independently actionable.
	// It must not become green merely because the repair queue was drained
	// or lost while the watermark remained stale.
	if !freshness.OldestPendingWatermark.IsZero() {
		watermarkBudget := c.MaxWatermarkAge
		if watermarkBudget <= 0 {
			watermarkBudget = DefaultRollupWatermarkBudget
		}
		if age := now.Sub(freshness.OldestPendingWatermark); age > watermarkBudget {
			return rollupStaleOutcome(now, fmt.Sprintf("pending_rollup_watermark_age=%s exceeds_budget=%s", age, watermarkBudget)), nil
		}
	}

	expected, err := c.ExpectedFormulas(ctx, c.Pool)
	if err != nil {
		return CheckOutcome{
			CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusFail,
			Category: string(FailureClassFormulaVersionMismatch), DetailRef: "expected_formulas_lookup_failed",
			ObservedAt: now,
		}, nil
	}
	for _, e := range expected {
		var registered int
		if err := c.Pool.QueryRow(ctx, `SELECT count(*) FROM formula_versions WHERE formula_id = $1 AND version = $2`, e.FormulaID, e.ExpectedVersion).Scan(&registered); err != nil {
			return CheckOutcome{
				CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusFail,
				Category: string(FailureClassFormulaVersionMismatch), DetailRef: "formula_version_query_failed",
				ObservedAt: now,
			}, nil
		}
		if registered == 0 {
			return CheckOutcome{
				CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusFail,
				Category:   string(FailureClassFormulaVersionMismatch),
				DetailRef:  fmt.Sprintf("formula_version_missing formula_id=%s version=%d", e.FormulaID, e.ExpectedVersion),
				ObservedAt: now,
			}, nil
		}
	}

	upToDate, detail, err := c.MigrationsUpToDate(ctx, c.Pool)
	if err != nil {
		return CheckOutcome{
			CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusFail,
			Category: string(FailureClassDBIntegrityViolation), DetailRef: "migration_integrity_query_failed",
			ObservedAt: now,
		}, nil
	}
	if !upToDate {
		return CheckOutcome{
			CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusFail,
			Category: string(FailureClassDBIntegrityViolation), DetailRef: "migrations_not_up_to_date: " + detail,
			ObservedAt: now,
		}, nil
	}

	return CheckOutcome{
		CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusPass,
		Category: "", DetailRef: fmt.Sprintf("repair_queue_depth=%d formulas_verified=%d migrations_up_to_date", depth, len(expected)),
		ObservedAt: now,
	}, nil
}

func rollupStaleOutcome(now time.Time, detail string) CheckOutcome {
	return CheckOutcome{
		CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusFail,
		Category: string(FailureClassRollupStale), DetailRef: detail, ObservedAt: now,
	}
}

func classifyRepairQueueDepth(depth, maxDepth int64, now time.Time) CheckOutcome {
	if depth > maxDepth {
		return CheckOutcome{
			CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusFail,
			Category:   string(FailureClassRollupStale),
			DetailRef:  fmt.Sprintf("repair_queue_depth=%d exceeds budget=%d", depth, maxDepth),
			ObservedAt: now,
		}
	}
	return CheckOutcome{
		CheckID: RollupFormulaDBIntegrityCheckID, Status: CheckStatusPass,
		DetailRef:  fmt.Sprintf("repair_queue_depth=%d within budget=%d", depth, maxDepth),
		ObservedAt: now,
	}
}

// RetentionDiskBackupCheckID is the check_id every RetentionDiskBackupCheck
// outcome reports, matching stage_9_retention_disk_and_backup.
const RetentionDiskBackupCheckID = "stage_9_retention_disk_and_backup"

// DefaultBackupAgeBudget and DefaultRestoreTestAgeBudget bound how stale the
// latest backup/restore-test may be before stage_9 flags backup_stale/
// restore_test_failed respectively. These are generous defaults a caller may
// override; they exist so a Check constructed with zero values still
// behaves sanely rather than flagging every run as stale by an
// accidental zero-duration budget.
const (
	DefaultBackupAgeBudget      = 48 * time.Hour
	DefaultRestoreTestAgeBudget = 7 * 24 * time.Hour
	DefaultDiskForecastBudget   = 0.90 // fraction of disk capacity; exceeding this fails disk_budget_exceeded
)

// DiskForecast is the read-only disk-budget signal stage_9's forecast check
// consumes: UsedFraction is the current fraction of the configured budget
// already consumed, and ProjectedExhaustionWithinBudget is true when, at the
// current growth rate, exhaustion is projected to occur before the next
// scheduled retention/backup cycle -- matching
// fault-injection-and-live-canary.yaml's "full_disk" claim: "flags projected
// exhaustion before the configured budget threshold is reached". A real
// caller computes this from the host filesystem; this package never reads
// disk usage itself (no new OS-level dependency), it only consumes an
// already-computed forecast.
type DiskForecast struct {
	UsedFraction                    float64
	ProjectedExhaustionWithinBudget bool
}

// DiskForecastLookup returns the current DiskForecast for the data
// directory the caller's storage lives on.
type DiskForecastLookup func(ctx context.Context, budget float64) (DiskForecast, error)

// BackupStatus is the latest-known backup/restore-test evidence stage_9
// reads. LastBackupAt/LastRestoreTestAt are zero when no backup/restore test
// has ever run (reported as a distinct, honest gray-leaning failure rather
// than silently treated as "just happened").
type BackupStatus struct {
	LastBackupAt          time.Time
	LastBackupChecksumOK  bool
	LastRestoreTestAt     time.Time
	LastRestoreTestPassed bool
	LastRestoreTestRan    bool
}

// BackupStatusLookup returns the latest-known BackupStatus. A real caller
// wires this to whatever durably records CreateBackup/RestoreBackup
// invocations (a small metadata table or file this session's own store.go
// may add); stage_9 never fabricates a restore-test result that did not
// actually run -- LastRestoreTestRan=false is reported honestly rather than
// defaulted to "passed".
type BackupStatusLookup func(ctx context.Context) (BackupStatus, error)

// RetentionDryRunResult is the read-only outcome of previewing which
// partitions ApplyRetention WOULD drop, without actually dropping them.
// stage_9 runs retention in dry-run/preview mode on every audit run (per the
// "verify configured endpoints/hooks WITHOUT mutating them" spirit extended
// to retention: an audit run itself never mutates partitions), and only
// flags retention_job_failed when the preview errors OR reports partitions
// already older than the retention horizon. An actual ApplyRetention drop is
// a separate, explicitly scheduled operation this check does not perform.
type RetentionDryRunResult struct {
	EligibleForDrop map[string][]string
}

// RetentionDryRunLookup previews retention-eligible partitions without
// dropping them. A real caller implements this against
// internal/dataplatform's own partition metadata (e.g. by listing
// pg_partitions the same way DropPartitionsOlderThan enumerates them,
// stopping short of the actual DROP), so stage_9 never actually mutates
// partitions on a routine audit run.
type RetentionDryRunLookup func(ctx context.Context, now time.Time, horizonDays int) (RetentionDryRunResult, error)

// PrivacyCanaryLookup runs the raw-content privacy canary and reports
// whether any prohibited-content field reached a durable sink, matching
// contracts/integrity/incident-and-health.yaml's "privacy_canary" health
// dimension and fault-injection-and-live-canary.yaml's
// privacy_canary_violation claim. A real caller wires this to
// internal/privacy's own DecodeAndExtract/SerializeAllSinks/ScanCanaries/
// ScanSecretFormats pipeline (the same Go-native functions
// cmd/privacy-canary/main.go already calls), never a second raw-content
// scanner.
type PrivacyCanaryLookup func(ctx context.Context) (violated bool, detail string, err error)

// SpoolIntegrityLookup performs a read-only strict validation of the
// configured durable spool.
type SpoolIntegrityLookup func(ctx context.Context) error

// RetentionDiskBackupCheck implements stage_9_retention_disk_and_backup: it
// previews (never applies) retention-eligible partitions, checks disk
// forecast against budget, verifies backup/restore-test recency using
// internal/dataplatform's own CreateBackup/VerifyBackupChecksum/CountRows/
// RestoreBackup machinery (via the caller-supplied BackupStatusLookup this
// package's own store.go populates -- see RunBackupCycle), and integrates
// the raw-content privacy canary as one of its own checks.
type RetentionDiskBackupCheck struct {
	RetentionDryRun      RetentionDryRunLookup
	HorizonDays          int
	DiskForecast         DiskForecastLookup
	DiskForecastBudget   float64
	BackupStatus         BackupStatusLookup
	BackupAgeBudget      time.Duration
	RestoreTestAgeBudget time.Duration
	SpoolIntegrity       SpoolIntegrityLookup
	PrivacyCanary        PrivacyCanaryLookup
	Now                  func() time.Time
}

var _ Check = (*RetentionDiskBackupCheck)(nil)

// NewRetentionDiskBackupCheck constructs a RetentionDiskBackupCheck with
// DefaultBackupAgeBudget/DefaultRestoreTestAgeBudget/DefaultDiskForecastBudget
// applied. Any lookup may be nil, degrading to a safe skipped-unsupported
// default for that sub-check rather than a caller panic.
func NewRetentionDiskBackupCheck(retentionDryRun RetentionDryRunLookup, diskForecast DiskForecastLookup, backupStatus BackupStatusLookup, privacyCanary PrivacyCanaryLookup) *RetentionDiskBackupCheck {
	return &RetentionDiskBackupCheck{
		RetentionDryRun: retentionDryRun, HorizonDays: DefaultRetentionHorizonDaysPlaceholder,
		DiskForecast: diskForecast, DiskForecastBudget: DefaultDiskForecastBudget,
		BackupStatus: backupStatus, BackupAgeBudget: DefaultBackupAgeBudget, RestoreTestAgeBudget: DefaultRestoreTestAgeBudget,
		PrivacyCanary: privacyCanary, Now: time.Now,
	}
}

// DefaultRetentionHorizonDaysPlaceholder mirrors
// internal/dataplatform.DefaultRetentionHorizonDays without importing
// dataplatform into this file's own default wiring (the real horizon value
// is supplied by whichever caller closes RetentionDryRunLookup over
// dataplatform's own DefaultRetentionHorizonDays constant; this package-local
// default only applies when a caller constructs RetentionDiskBackupCheck via
// NewRetentionDiskBackupCheck without overriding HorizonDays).
const DefaultRetentionHorizonDaysPlaceholder = 400

func (c *RetentionDiskBackupCheck) StageID() StageID { return Stage9RetentionDiskAndBackup }
func (c *RetentionDiskBackupCheck) CheckID() string  { return RetentionDiskBackupCheckID }

func (c *RetentionDiskBackupCheck) validateProductionReady(_ *pgxpool.Pool) error {
	if c == nil || c.RetentionDryRun == nil || c.DiskForecast == nil ||
		c.BackupStatus == nil || c.SpoolIntegrity == nil ||
		c.PrivacyCanary == nil || c.Now == nil {
		return fmt.Errorf("retention, disk, backup/restore, spool, privacy, and clock dependencies are required")
	}
	return nil
}

func (c *RetentionDiskBackupCheck) Targets(_ context.Context, _ CheckInput) ([]CheckTarget, error) {
	sources := []string{
		"retention-preview", "disk-forecast", "backup-status",
		"restore-status", "durable-spool", "privacy-canary",
	}
	targets := make([]CheckTarget, 0, len(sources))
	for _, sourceID := range sources {
		targets = append(targets, CheckTarget{
			CapabilityID: storageOpsCapabilityID, InstallationID: storageOpsInstallationID,
			SourceID: sourceID,
		})
	}
	return targets, nil
}

// storageFinding mirrors DiscoveryConfigCheck's discoveryFinding pattern:
// named sub-check results folded into one CheckOutcome so stage 11 can see
// exactly which sub-check failed via Category/DetailRef.
type storageFinding struct {
	category string
	failed   bool
	detail   string
}

func (c *RetentionDiskBackupCheck) Evaluate(ctx context.Context, in CheckInput, target CheckTarget) (CheckOutcome, error) {
	now := c.Now()
	if !in.Now.IsZero() {
		now = in.Now
	}
	var finding storageFinding
	switch target.SourceID {
	case "retention-preview":
		finding = c.checkRetentionDryRun(ctx, now)
	case "disk-forecast":
		finding = c.checkDiskForecast(ctx)
	case "backup-status":
		finding = c.checkBackupAge(ctx, now)
	case "restore-status":
		finding = c.checkRestoreTestAge(ctx, now)
	case "durable-spool":
		finding = c.checkSpoolIntegrity(ctx)
	case "privacy-canary":
		finding = c.checkPrivacyCanary(ctx)
	default:
		finding = storageFinding{
			category: string(FailureClassDBIntegrityViolation),
			failed:   true, detail: "storage_subcheck_target_not_enumerated",
		}
	}
	status := CheckStatusPass
	if finding.failed {
		status = CheckStatusFail
	}
	return CheckOutcome{
		CheckID: RetentionDiskBackupCheckID, Status: status, Category: finding.category,
		DetailRef: finding.detail, ObservedAt: now,
	}, nil
}

func (c *RetentionDiskBackupCheck) checkRetentionDryRun(ctx context.Context, now time.Time) storageFinding {
	if c.RetentionDryRun == nil {
		return storageFinding{category: string(FailureClassRetentionJobFailed), failed: true, detail: "retention_dry_run_not_wired"}
	}
	horizon := c.HorizonDays
	if horizon <= 0 {
		horizon = DefaultRetentionHorizonDaysPlaceholder
	}
	result, err := c.RetentionDryRun(ctx, now, horizon)
	if err != nil {
		return storageFinding{category: string(FailureClassRetentionJobFailed), failed: true, detail: "retention_dry_run_failed"}
	}
	overdue := 0
	for _, partitions := range result.EligibleForDrop {
		overdue += len(partitions)
	}
	if overdue > 0 {
		return storageFinding{
			category: string(FailureClassRetentionJobFailed), failed: true,
			detail: fmt.Sprintf("retention_overdue eligible_tables=%d eligible_partitions=%d", len(result.EligibleForDrop), overdue),
		}
	}
	return storageFinding{detail: fmt.Sprintf("retention_dry_run_ok eligible_tables=%d", len(result.EligibleForDrop))}
}

func (c *RetentionDiskBackupCheck) checkDiskForecast(ctx context.Context) storageFinding {
	if c.DiskForecast == nil {
		return storageFinding{category: string(FailureClassDiskBudgetExceeded), failed: true, detail: "disk_forecast_not_wired"}
	}
	budget := c.DiskForecastBudget
	if budget <= 0 {
		budget = DefaultDiskForecastBudget
	}
	forecast, err := c.DiskForecast(ctx, budget)
	if err != nil {
		return storageFinding{category: string(FailureClassDiskBudgetExceeded), failed: true, detail: "disk_forecast_failed"}
	}
	if forecast.ProjectedExhaustionWithinBudget || forecast.UsedFraction >= budget {
		return storageFinding{
			category: string(FailureClassDiskBudgetExceeded), failed: true,
			detail: fmt.Sprintf("disk_used_fraction=%.4f budget=%.4f projected_exhaustion=%v", forecast.UsedFraction, budget, forecast.ProjectedExhaustionWithinBudget),
		}
	}
	return storageFinding{detail: fmt.Sprintf("disk_used_fraction=%.4f within_budget=%.4f", forecast.UsedFraction, budget)}
}

func (c *RetentionDiskBackupCheck) checkBackupAge(ctx context.Context, now time.Time) storageFinding {
	if c.BackupStatus == nil {
		return storageFinding{category: string(FailureClassBackupStale), failed: true, detail: "backup_status_not_wired"}
	}
	status, err := c.BackupStatus(ctx)
	if err != nil {
		return storageFinding{category: string(FailureClassBackupStale), failed: true, detail: "backup_status_lookup_failed"}
	}
	budget := c.BackupAgeBudget
	if budget <= 0 {
		budget = DefaultBackupAgeBudget
	}
	if status.LastBackupAt.IsZero() {
		return storageFinding{category: string(FailureClassBackupStale), failed: true, detail: "no_backup_on_record"}
	}
	if !status.LastBackupChecksumOK {
		return storageFinding{category: string(FailureClassBackupStale), failed: true, detail: "last_backup_checksum_invalid"}
	}
	age := now.Sub(status.LastBackupAt)
	if age > budget {
		return storageFinding{category: string(FailureClassBackupStale), failed: true, detail: fmt.Sprintf("backup_age=%s exceeds_budget=%s", age, budget)}
	}
	return storageFinding{detail: fmt.Sprintf("backup_age=%s within_budget=%s", age, budget)}
}

func (c *RetentionDiskBackupCheck) checkRestoreTestAge(ctx context.Context, now time.Time) storageFinding {
	if c.BackupStatus == nil {
		return storageFinding{category: string(FailureClassRestoreTestFailed), failed: true, detail: "restore_status_not_wired"}
	}
	status, err := c.BackupStatus(ctx)
	if err != nil {
		return storageFinding{category: string(FailureClassRestoreTestFailed), failed: true, detail: "restore_status_lookup_failed"}
	}
	budget := c.RestoreTestAgeBudget
	if budget <= 0 {
		budget = DefaultRestoreTestAgeBudget
	}
	if !status.LastRestoreTestRan {
		// Never fabricate a restore-test result that did not actually run:
		// report the honest age-unknown state as a failure (restore-test
		// coverage has never been established), matching the TDD's
		// "do not fabricate a restore-test result that didn't actually run"
		// instruction.
		return storageFinding{category: string(FailureClassRestoreTestFailed), failed: true, detail: "no_restore_test_ever_ran"}
	}
	if !status.LastRestoreTestPassed {
		return storageFinding{category: string(FailureClassRestoreTestFailed), failed: true, detail: "last_restore_test_failed"}
	}
	age := now.Sub(status.LastRestoreTestAt)
	if age > budget {
		return storageFinding{category: string(FailureClassRestoreTestFailed), failed: true, detail: fmt.Sprintf("restore_test_age=%s exceeds_budget=%s", age, budget)}
	}
	return storageFinding{detail: fmt.Sprintf("restore_test_age=%s within_budget=%s", age, budget)}
}

func (c *RetentionDiskBackupCheck) checkSpoolIntegrity(ctx context.Context) storageFinding {
	if c.SpoolIntegrity == nil {
		return storageFinding{category: string(FailureClassDBIntegrityViolation), failed: true, detail: "spool_integrity_not_wired"}
	}
	if err := c.SpoolIntegrity(ctx); err != nil {
		return storageFinding{category: string(FailureClassDBIntegrityViolation), failed: true, detail: "durable_spool_corrupt_or_unsafe"}
	}
	return storageFinding{detail: "durable_spool_integrity_passed"}
}

func (c *RetentionDiskBackupCheck) checkPrivacyCanary(ctx context.Context) storageFinding {
	if c.PrivacyCanary == nil {
		return storageFinding{category: string(FailureClassPrivacyCanaryViolation), failed: true, detail: "privacy_canary_not_wired"}
	}
	violated, _, err := c.PrivacyCanary(ctx)
	if err != nil {
		return storageFinding{category: string(FailureClassPrivacyCanaryViolation), failed: true, detail: "privacy_canary_check_failed"}
	}
	if violated {
		return storageFinding{category: string(FailureClassPrivacyCanaryViolation), failed: true, detail: "privacy_canary_violation"}
	}
	return storageFinding{detail: "privacy_canary_clean"}
}
