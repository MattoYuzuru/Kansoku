package integrity

import (
	"context"
	"time"
)

// CheckInput is everything one Check implementation needs to evaluate one
// (check_id, capability_id, installation_id) tuple for one stage of one
// audit_run. Later stages populate Registry/Pool references a real Check
// needs (e.g. an internal/adaptersdk.Registry, internal/dataplatform's
// pool for stage 8/9's RepairQueueDepth/ApplyRetention/CreateBackup calls,
// or internal/observability's DurableState for watermark/incident lookups)
// by closing over them when constructing the Check, not by widening this
// struct with fields most stages never use.
type CheckInput struct {
	AuditRunID     string
	Mode           RunMode
	Trigger        Trigger
	Now            time.Time
	InstallationID string
	CapabilityID   string
	SourceID       string
}

// CheckOutcome is one Check's result for one CheckInput, matching the
// (check_id, capability_id, installation_id)-keyed row shape
// audit_checks.up.sql declares and the CheckStatus/Category/DetailRef
// fields incident-and-health.yaml's health dimensions are sourced from.
type CheckOutcome struct {
	CheckID    string
	Status     CheckStatus
	Category   string
	DetailRef  string
	ObservedAt time.Time
}

// Check is the pluggable unit every one of the 11 daily-audit stage bodies
// implements. Stage 2 (this stage) only needs the interface shape and a
// registry keyed by StageID so RunStages can drive whatever later stages
// register; it does not implement any of the 11 real stage bodies itself.
//
// Every Check must be side-effect-free against its target (mutates_target
// is false for every stage in stage_registry): implementations call only
// read/audit-shaped methods (adaptersdk.HostView probes, Adapter.Audit,
// dataplatform's already-exposed read/repair-queue-depth helpers, the
// public hook ingress for the synthetic probe), never a write to a user's
// own agent configuration.
type Check interface {
	// StageID identifies which of the 11 stage_registry entries this Check
	// belongs to; RunStages groups and orders checks by this value using
	// StageRegistry's ordinal order.
	StageID() StageID

	// CheckID is the stable durable identity written before evaluation and
	// reused for the terminal outcome. It prevents a pending row from being
	// stranded under the stage ID when a concrete check reports a more
	// specific ID.
	CheckID() string

	// Targets enumerates the (capability_id, installation_id) pairs this
	// Check should be evaluated for in the given run, so idempotent re-run
	// and interrupted-run retry can address one check row at a time via
	// AuditCheckKey.
	Targets(ctx context.Context, in CheckInput) ([]CheckTarget, error)

	// Evaluate runs the check for one specific target and returns its
	// outcome. Evaluate must be safe to call more than once for the same
	// target (idempotent): running it again recomputes the same key
	// deterministically rather than assuming a prior result.
	Evaluate(ctx context.Context, in CheckInput, target CheckTarget) (CheckOutcome, error)
}

// CheckTarget is one (capability_id, installation_id) pair a Check
// evaluates, matching the audit_checks idempotency key's non-run-scoped
// components.
type CheckTarget struct {
	CapabilityID   string
	InstallationID string
	SourceID       string
	// AdapterID is scheduler-only targeting metadata. It does not widen the
	// durable audit-check key, but lets a version-change run select only
	// targets owned by the adapter whose fingerprint changed.
	AdapterID string
}

// CheckRegistry is the ordered set of Check implementations a Scheduler
// drives. It is keyed by StageID so RunStages can run every registered
// Check for one stage before moving to the next, matching
// audit-run-and-schedule.yaml's stage_ordinal_rule ("stages execute in
// ascending ordinal order within one audit_run ... never reorders the
// stages it does run"). Production assembly validates every mandatory
// stage. A missing stage is represented durably by Scheduler as failed
// unwired evidence; it can never make a run pass.
type CheckRegistry struct {
	byStage map[StageID][]Check
}

// NewCheckRegistry returns an empty CheckRegistry.
func NewCheckRegistry() *CheckRegistry {
	return &CheckRegistry{byStage: map[StageID][]Check{}}
}

// Register adds check under its own StageID.
func (r *CheckRegistry) Register(check Check) {
	if check == nil {
		return
	}
	r.byStage[check.StageID()] = append(r.byStage[check.StageID()], check)
}

// ForStage returns every Check registered under stage, in registration
// order.
func (r *CheckRegistry) ForStage(stage StageID) []Check {
	return r.byStage[stage]
}

// HasStage reports whether at least one concrete check is wired for stage.
// Stage 11 is scheduler-owned and stage 10 is optional.
func (r *CheckRegistry) HasStage(stage StageID) bool {
	return r != nil && len(r.byStage[stage]) > 0
}
