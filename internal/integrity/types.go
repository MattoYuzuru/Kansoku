// Package integrity implements the Session 08 daily integrity/drift audit
// engine described in "Engineering Proposal/08-integrity-drift-and-daily-audit.md"
// and "Technical Design Document/08-integrity-drift-and-daily-audit.md", and
// specified as a closed contract set in contracts/integrity/*.yaml (locked
// by contracts/integrity-policy-locks.yaml). This package owns the
// repository's first durable scheduler: an AuditRun state machine, a
// PostgreSQL-advisory-lock-backed single-writer guarantee, and (in later
// stages) the 11 daily-audit stage bodies themselves.
//
// This stage (Stage 2) delivers the scheduler/state-machine/locking
// skeleton: RunState/RunMode/Trigger/StageID enums matching
// audit-run-and-schedule.yaml exactly, the AuditRun/AuditCheck durable
// row shapes, a Check interface later stages implement to plug in the real
// 11 audit stages, a Scheduler driving AcquireLock/StartRun/RunStages/
// FinishRun and crash recovery, and the IntegrityIncidentDetail extension
// record described by incident-and-health.yaml. It reuses
// internal/dataplatform's existing *pgxpool.Pool connection pattern
// verbatim (see NewScheduler) and internal/observability's existing
// Watermark/Incident types and internal/adaptersdk's Registry/Manifest,
// never redefining or forking any of them.
package integrity

import (
	"fmt"
	"time"
)

// RunMode is the audit_run "full"/"reduced" scope, matching
// audit-run-and-schedule.yaml `run_modes`.
type RunMode string

const (
	RunModeFull    RunMode = "full"
	RunModeReduced RunMode = "reduced"
)

// Trigger is the closed set of reasons a run was scheduled, matching
// audit-run-and-schedule.yaml `triggers`.
type Trigger string

const (
	TriggerScheduledDaily        Trigger = "scheduled_daily"
	TriggerStartup               Trigger = "startup"
	TriggerVersionChangeDetected Trigger = "version_change_detected"
	TriggerManualOperatorRequest Trigger = "manual_operator_request"
)

// RunState is the closed audit_run state machine, matching
// audit-run-and-schedule.yaml `run_states`:
//
//	scheduled -> running -> passed|degraded|failed|cancelled
//
// A run that reaches a terminal state (passed/degraded/failed/cancelled)
// never transitions again; a subsequent audit is always a new AuditRun.
type RunState string

const (
	RunScheduled RunState = "scheduled"
	RunRunning   RunState = "running"
	RunPassed    RunState = "passed"
	RunDegraded  RunState = "degraded"
	RunFailed    RunState = "failed"
	RunCancelled RunState = "cancelled"
)

// terminal reports whether s is one of the four terminal states from which
// no further transition is legal.
func (s RunState) terminal() bool {
	switch s {
	case RunPassed, RunDegraded, RunFailed, RunCancelled:
		return true
	default:
		return false
	}
}

// FailureReason is the closed set of non-nil reasons an AuditRun can carry,
// beyond its RunState, matching audit-run-and-schedule.yaml's
// crash_recovery.stale_running_detection ("a terminal sub-state of failed
// with failure_reason=crash_recovery_stale_run") and the timeout_bound_rule.
type FailureReason string

const (
	FailureReasonNone               FailureReason = ""
	FailureReasonCrashRecoveryStale FailureReason = "crash_recovery_stale_run"
	FailureReasonStageTimeout       FailureReason = "stage_timeout"
	FailureReasonRedTierCheckFailed FailureReason = "red_tier_check_failed"
	FailureReasonOperatorCancelled  FailureReason = "operator_cancelled"
)

// StageID is one of the 11 ordered daily-audit stages from
// audit-run-and-schedule.yaml `stage_registry`.
type StageID string

const (
	Stage1DiscoveryAndConfiguration   StageID = "stage_1_discovery_and_configuration"
	Stage2EndpointAndHookVerification StageID = "stage_2_endpoint_and_hook_verification"
	Stage3WatermarkVsInactivity       StageID = "stage_3_watermark_vs_inactivity"
	Stage4ParserFixtureReplay         StageID = "stage_4_parser_fixture_replay"
	Stage5SyntheticPipelineProbe      StageID = "stage_5_synthetic_pipeline_probe"
	Stage6CrossSourceReconciliation   StageID = "stage_6_cross_source_reconciliation"
	Stage7UnknownSchemaAndLag         StageID = "stage_7_unknown_schema_and_lag"
	Stage8RollupFormulaAndDBIntegrity StageID = "stage_8_rollup_formula_and_db_integrity"
	Stage9RetentionDiskAndBackup      StageID = "stage_9_retention_disk_and_backup"
	Stage10OptionalLiveCanary         StageID = "stage_10_optional_live_canary"
	Stage11PersistReportAndIncidents  StageID = "stage_11_persist_report_and_raise_incidents"
)

// StageDescriptor is the static, contract-declared shape of one stage in
// stage_registry: its ordinal, timeout bound and mutation posture. Every
// stage in the registry has MutatesTarget=false, matching the contract's
// no_mutation_rule.
type StageDescriptor struct {
	StageID           StageID
	Ordinal           int
	Idempotent        bool
	TimeoutSeconds    int
	MutatesTarget     bool
	DisabledByDefault bool
}

// StageRegistry is the ordered, closed set of daily-audit stages, matching
// contracts/integrity/audit-run-and-schedule.yaml `stage_registry` field
// for field (ordinal, timeout_seconds, mutates_target, disabled_by_default).
// Later stages plug in real Check implementations against these StageIDs;
// this stage only needs the registry shape and ordinal discipline.
var StageRegistry = []StageDescriptor{
	{StageID: Stage1DiscoveryAndConfiguration, Ordinal: 1, Idempotent: true, TimeoutSeconds: 30},
	{StageID: Stage2EndpointAndHookVerification, Ordinal: 2, Idempotent: true, TimeoutSeconds: 30},
	{StageID: Stage3WatermarkVsInactivity, Ordinal: 3, Idempotent: true, TimeoutSeconds: 15},
	{StageID: Stage4ParserFixtureReplay, Ordinal: 4, Idempotent: true, TimeoutSeconds: 60},
	{StageID: Stage5SyntheticPipelineProbe, Ordinal: 5, Idempotent: true, TimeoutSeconds: 45},
	{StageID: Stage6CrossSourceReconciliation, Ordinal: 6, Idempotent: true, TimeoutSeconds: 60},
	{StageID: Stage7UnknownSchemaAndLag, Ordinal: 7, Idempotent: true, TimeoutSeconds: 30},
	{StageID: Stage8RollupFormulaAndDBIntegrity, Ordinal: 8, Idempotent: true, TimeoutSeconds: 60},
	{StageID: Stage9RetentionDiskAndBackup, Ordinal: 9, Idempotent: true, TimeoutSeconds: 90},
	{StageID: Stage10OptionalLiveCanary, Ordinal: 10, Idempotent: true, TimeoutSeconds: 300, DisabledByDefault: true},
	{StageID: Stage11PersistReportAndIncidents, Ordinal: 11, Idempotent: true, TimeoutSeconds: 30},
}

// reducedModeDefaultStages are the stages a startup or version_change_detected
// trigger runs by default, matching audit-run-and-schedule.yaml
// `reduced_mode_stage_scope`. version_change_detected additionally adds
// Stage4/Stage7 scoped to the affected source; that scoping decision is a
// later stage's runtime concern (it needs the drift-fingerprint comparison
// this stage does not yet implement), so it is expressed here as a
// function of trigger rather than a second static list.
var reducedModeDefaultStages = []StageID{
	Stage1DiscoveryAndConfiguration,
	Stage2EndpointAndHookVerification,
	Stage3WatermarkVsInactivity,
}

// ReducedModeStages returns the ordered stage IDs a reduced-mode run should
// execute for the given trigger, matching
// audit-run-and-schedule.yaml `reduced_mode_stage_scope`.
func ReducedModeStages(trigger Trigger) []StageID {
	stages := append([]StageID{}, reducedModeDefaultStages...)
	if trigger == TriggerVersionChangeDetected {
		stages = append(stages, Stage4ParserFixtureReplay, Stage7UnknownSchemaAndLag)
	}
	return stages
}

// FullModeStages returns every stage in stage_registry, in ascending
// ordinal order, matching the `stage_ordinal_rule`.
func FullModeStages() []StageID {
	stages := make([]StageID, len(StageRegistry))
	for i, d := range StageRegistry {
		stages[i] = d.StageID
	}
	return stages
}

// StagesForRun returns the stage IDs a run of the given mode/trigger should
// execute, in ascending ordinal order.
func StagesForRun(mode RunMode, trigger Trigger) []StageID {
	if mode == RunModeFull {
		return FullModeStages()
	}
	selected := map[StageID]bool{}
	for _, id := range ReducedModeStages(trigger) {
		selected[id] = true
	}
	ordered := make([]StageID, 0, len(selected))
	for _, d := range StageRegistry {
		if selected[d.StageID] {
			ordered = append(ordered, d.StageID)
		}
	}
	return ordered
}

// ValidateModeTrigger enforces the contract's closed trigger semantics.
func ValidateModeTrigger(mode RunMode, trigger Trigger) error {
	switch trigger {
	case TriggerScheduledDaily, TriggerManualOperatorRequest:
		if mode != RunModeFull {
			return fmt.Errorf("trigger %s requires full mode", trigger)
		}
	case TriggerStartup, TriggerVersionChangeDetected:
		if mode != RunModeReduced {
			return fmt.Errorf("trigger %s requires reduced mode", trigger)
		}
	default:
		return fmt.Errorf("unknown audit trigger %q", trigger)
	}
	return nil
}

// CheckStatus is the closed outcome of one audit_check row. It intentionally
// mirrors internal/adaptersdk.CheckStatus's pass/fail/skipped_unsupported
// vocabulary plus a pending state for a row not yet executed in this run,
// since a Check row is inserted before it necessarily has a result (e.g. an
// interrupted run's incomplete checks stay pending/gray, never silently
// marked passed).
type CheckStatus string

const (
	CheckStatusPending            CheckStatus = "pending"
	CheckStatusPass               CheckStatus = "pass"
	CheckStatusFail               CheckStatus = "fail"
	CheckStatusSkippedUnsupported CheckStatus = "skipped_unsupported"
)

// AuditRun is the durable row shape for one audit_runs table row, matching
// audit-run-and-schedule.yaml's state machine and stage_registry fields.
type AuditRun struct {
	AuditRunID       string
	Mode             RunMode
	Trigger          Trigger
	State            RunState
	FailureReason    FailureReason
	ScheduledAt      time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
	AdvisoryLockKey  int64
	RequestedStages  []StageID
	InputsVersionRef map[string]string
}

// AuditAttempt is a non-overlap observation that did not become an
// audit_run because another live session already held the workflow lock.
type AuditAttempt struct {
	AttemptID       string
	Mode            RunMode
	Trigger         Trigger
	AttemptedAt     time.Time
	Outcome         string
	AdvisoryLockKey int64
}

// AuditCheckKey identifies one check row uniquely. SourceID is explicit so
// two sources on one installation never overload CapabilityID with a
// source name or overwrite each other's evidence.
type AuditCheckKey struct {
	AuditRunID     string
	CheckID        string
	CapabilityID   string
	InstallationID string
	SourceID       string
}

// AuditCheck is the durable row shape for one audit_checks table row.
type AuditCheck struct {
	AuditCheckKey
	StageID    StageID
	Status     CheckStatus
	Category   string
	DetailRef  string
	ObservedAt *time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// FreshnessWindow bounds how long a passing check's evidence may be reused
// as "still fresh" before a green health dimension requires a new runtime
// check, matching incident-and-health.yaml's `state_colors.green` rule and
// health_dimensions' "this run or a still-fresh prior run" language. Each
// stage may declare its own window; this default matches the daily cadence
// (a passing check from the previous daily run is fresh until the next one
// is due, plus a bounded grace period).
const DefaultFreshnessWindow = 36 * time.Hour

// IsFresh reports whether observedAt is still inside window as of now.
func IsFresh(observedAt time.Time, now time.Time, window time.Duration) bool {
	if observedAt.IsZero() {
		return false
	}
	return now.Sub(observedAt) <= window
}

// FailureClass is the closed vocabulary from
// contracts/integrity/incident-and-health.yaml `incident_key.failure_class_vocabulary`.
type FailureClass string

const (
	FailureClassEndpointUnreachable            FailureClass = "endpoint_unreachable"
	FailureClassHookRemovedDisabledOrUntrusted FailureClass = "hook_removed_disabled_or_untrusted"
	FailureClassOTLPMisconfigured              FailureClass = "otlp_misconfigured"
	FailureClassPermissionDenied               FailureClass = "permission_denied"
	FailureClassWatermarkStall                 FailureClass = "watermark_stall"
	FailureClassTrueInactivityFlagged          FailureClass = "true_inactivity_flagged"
	FailureClassEligibilityUnknown             FailureClass = "eligibility_unknown"
	FailureClassParserIncompatibility          FailureClass = "parser_incompatibility"
	FailureClassUnknownSchema                  FailureClass = "unknown_schema"
	FailureClassDuplicateEvidenceAnomaly       FailureClass = "duplicate_evidence_anomaly"
	FailureClassIngestLag                      FailureClass = "ingest_lag"
	FailureClassRollupStale                    FailureClass = "rollup_stale"
	FailureClassFormulaVersionMismatch         FailureClass = "formula_version_mismatch"
	FailureClassDBIntegrityViolation           FailureClass = "db_integrity_violation"
	FailureClassRetentionJobFailed             FailureClass = "retention_job_failed"
	FailureClassDiskBudgetExceeded             FailureClass = "disk_budget_exceeded"
	FailureClassBackupStale                    FailureClass = "backup_stale"
	FailureClassRestoreTestFailed              FailureClass = "restore_test_failed"
	FailureClassPrivacyCanaryViolation         FailureClass = "privacy_canary_violation"
	FailureClassSyntheticPipelineProbeFailed   FailureClass = "synthetic_pipeline_probe_failed"
	FailureClassLiveCanaryPartialDAG           FailureClass = "live_canary_partial_dag"
	FailureClassLiveCanaryProviderTimeout      FailureClass = "live_canary_provider_timeout"
	FailureClassReconciliationMismatch         FailureClass = "reconciliation_mismatch"
)

// IncidentKey is the closed identity every Session 08 incident is deduped
// and looked up by, matching incident-and-health.yaml `incident_key`.
type IncidentKey struct {
	InstallationID string
	SourceID       string
	CapabilityID   string
	FailureClass   FailureClass
}

// AffectedInterval is the metric/completeness interval an incident's
// evidence implicates, matching incident-and-health.yaml
// `session_08_extension_fields.affected_interval`.
type AffectedInterval struct {
	From time.Time
	To   time.Time
}

// IntegrityIncidentDetail is the session-08-owned extension record keyed
// 1:1 by IncidentID, referencing internal/observability.Incident by ID
// rather than forking it, matching incident-and-health.yaml
// `incident_field_extension`. Later stages populate/update this alongside
// an internal/observability.Incident row sharing the same IncidentID.
type IntegrityIncidentDetail struct {
	IncidentID            string
	InstallationID        string
	SourceID              string
	CapabilityID          string
	FailureClass          FailureClass
	FirstSeenAt           time.Time
	AffectedInterval      AffectedInterval
	CheckEvidenceRef      string
	AgentOrAdapterVersion string
	RecoveryCriteria      string
	UserNotes             string
	ResolvedAt            *time.Time
}
