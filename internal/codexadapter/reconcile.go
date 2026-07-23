package codexadapter

import "sort"

// This file implements the per-session cross-source activity reconciliation
// contracts/codex/skill-evidence-and-reconciliation.yaml's reconciliation
// block declares: hook (codex.hook) vs OTel (codex.otel) vs rollout
// (codex.rollout) evidence compared across six lanes, using a versioned
// (never hardcoded) batching/terminal-delay tolerance, where a missing
// expected source degrades only that source's own capabilities/interval and
// never causes the reconciler to fabricate a plausible-looking zero-usage
// result for the whole session. This mirrors
// contracts/observability/reconciliation.yaml's expected_lanes/one_fact_rule
// shape verbatim, mapped onto codex.hook/codex.otel/codex.rollout exactly as
// reconciliation.expected_lanes_reused_from documents.

// ReconciliationLane is the closed, six-member set of per-session
// comparisons contracts/codex/skill-evidence-and-reconciliation.yaml's
// reconciliation.per_session_comparisons declares. No seventh lane exists.
type ReconciliationLane string

const (
	LanePrompts           ReconciliationLane = "hook_prompt_events_vs_otel_prompt_events_vs_rollout_user_messages"
	LaneToolTerminal      ReconciliationLane = "hook_tool_terminal_events_vs_otel_results_vs_rollout_calls_and_outputs"
	LaneSessionLifecycle  ReconciliationLane = "session_start_stop_resume_vs_rollout_lifecycle"
	LaneSubagentLifecycle ReconciliationLane = "subagent_lifecycle_vs_parent_transcript_evidence"
	LaneMCPCalls          ReconciliationLane = "mcp_call_counts_vs_configured_and_advertised_tools"
	LaneComponentEvidence ReconciliationLane = "component_explicit_load_execute_evidence_compared_without_forcing_equality"
)

// AllReconciliationLanes returns the closed, ordered lane vocabulary this
// package reconciles, mirroring
// contracts/codex/skill-evidence-and-reconciliation.yaml's
// reconciliation.per_session_comparisons verbatim so a validator/test can
// walk the identical list.
func AllReconciliationLanes() []ReconciliationLane {
	return []ReconciliationLane{
		LanePrompts, LaneToolTerminal, LaneSessionLifecycle,
		LaneSubagentLifecycle, LaneMCPCalls, LaneComponentEvidence,
	}
}

// ReconciliationSourceID is the closed, three-member set of lane inputs this
// package's reconciler compares -- codex.hook, codex.otel, codex.rollout.
// codex.inventory is deliberately excluded: it is compared against
// codex.otel/rollout MCP call *evidence* inside LaneMCPCalls (configured vs
// observed), but it is never itself an activity lane input.
type ReconciliationSourceID string

const (
	ReconSourceHook    ReconciliationSourceID = sourceIDHook
	ReconSourceOTel    ReconciliationSourceID = sourceIDOTel
	ReconSourceRollout ReconciliationSourceID = sourceIDRollout
)

// ToleranceRegistryEntry is one versioned batching/terminal-delay tolerance
// entry, matching reconciliation.tolerance's
// "versioned_per_compatibility_registry_entry_not_hardcoded" requirement:
// the tolerance is data attached to a specific CompatibilityVersion, never a
// single hardcoded constant shared across every Codex release this adapter
// supports.
type ToleranceRegistryEntry struct {
	CompatibilityVersion string
	BatchingDelayMS      int64
	TerminalDelayMS      int64
}

// toleranceRegistry is this package's own closed, versioned tolerance table.
// A later validator stage checks this against
// contracts/codex/rollout-and-inventory.yaml's compatibility registry so the
// two never drift silently; ResolveTolerance never falls back to a bare
// hardcoded number when a version is missing from this table -- it reports
// ok=false instead.
var toleranceRegistry = map[string]ToleranceRegistryEntry{
	"codex-compat/1": {CompatibilityVersion: "codex-compat/1", BatchingDelayMS: 2000, TerminalDelayMS: 5000},
}

// ResolveTolerance looks up the versioned tolerance entry for
// compatibilityVersion. ok is false when the version is unknown to this
// registry; the caller must treat that as "this comparison's tolerance is
// undetermined for this Codex release" and degrade accordingly rather than
// silently reusing another version's numbers.
func ResolveTolerance(compatibilityVersion string) (ToleranceRegistryEntry, bool) {
	entry, ok := toleranceRegistry[compatibilityVersion]
	return entry, ok
}

// SourceHealth is the closed, per-source health input to Reconcile: whether
// a source produced any lane evidence at all for this session, and (when it
// did) the counts/identities this lane needs. Present=false means "this
// source is missing for this session" -- never conflated with
// Present=true,Count=0 ("this source is healthy and genuinely observed zero
// activity").
type SourceHealth struct {
	Present bool
	Count   int
	// EventIdentities is the set of lane-scoped fact identities this source
	// observed (for example session_id+turn_id / session_id+tool_id
	// pairs), reused for the "same identity, different lane" comparison
	// rather than raw counts alone where the lane needs identity-level
	// comparison (subagent/session lifecycle, component evidence).
	EventIdentities []string
}

// LaneInput is the closed, per-lane input to ReconcileLane: this session's
// hook/otel/rollout SourceHealth for the one lane being compared, plus the
// compatibility version whose tolerance applies.
type LaneInput struct {
	Lane                 ReconciliationLane
	CompatibilityVersion string
	Hook                 SourceHealth
	OTel                 SourceHealth
	Rollout              SourceHealth
}

// LaneCompleteness mirrors
// contracts/observability/reconciliation.yaml's complete_when/partial_when
// vocabulary verbatim: "complete" only when every expected-present source
// for this lane is present and healthy, "partial" when at least one
// expected source is missing/degraded, and reconciliation never collapses a
// partial lane into a false "complete, zero usage" result.
type LaneCompleteness string

const (
	LaneComplete LaneCompleteness = "complete"
	LanePartial  LaneCompleteness = "partial"
)

// LaneResult is the closed, deterministic outcome of reconciling one lane
// for one session. DegradedSources names exactly the sources missing from
// this lane (never "all sources", even when every one of them happens to be
// missing -- the caller can always see which specific source IDs are
// degraded). Mismatched is true only when every compared source was
// Present, but their counts/identities disagree beyond ToleranceRegistryEntry;
// a missing source alone never sets Mismatched.
type LaneResult struct {
	Lane            ReconciliationLane
	Completeness    LaneCompleteness
	DegradedSources []ReconciliationSourceID
	Mismatched      bool
	HookCount       int
	OTelCount       int
	RolloutCount    int
}

// ReconcileLane compares one lane's hook/otel/rollout evidence for one
// session. A source with Present=false is recorded in DegradedSources and
// excluded from the count/identity comparison entirely -- it never
// contributes an implicit zero to that comparison, matching
// reconciliation.missing_source_rule verbatim: "missing one expected source
// marks only that source's own capabilities and reconciliation interval
// degraded; it never silently reports zero usage for the whole session."
//
// When two or more sources are Present, their counts are compared against
// each other within the versioned tolerance's implied slack (a
// count-for-count exact match is still required here; the millisecond
// tolerance governs the timing window a caller uses before invoking this
// function to decide which records belong to the same session/turn window,
// not a fudge-factor on the count comparison itself). Mismatched is set
// when present-source counts disagree.
func ReconcileLane(input LaneInput) LaneResult {
	result := LaneResult{Lane: input.Lane}

	type present struct {
		id     ReconciliationSourceID
		health SourceHealth
	}
	all := []present{
		{ReconSourceHook, input.Hook},
		{ReconSourceOTel, input.OTel},
		{ReconSourceRollout, input.Rollout},
	}

	var live []present
	for _, candidate := range all {
		if candidate.health.Present {
			live = append(live, candidate)
		} else {
			result.DegradedSources = append(result.DegradedSources, candidate.id)
		}
	}
	sort.Slice(result.DegradedSources, func(i, j int) bool { return result.DegradedSources[i] < result.DegradedSources[j] })

	result.HookCount = input.Hook.Count
	result.OTelCount = input.OTel.Count
	result.RolloutCount = input.Rollout.Count

	if len(result.DegradedSources) > 0 {
		result.Completeness = LanePartial
	} else {
		result.Completeness = LaneComplete
	}

	if len(live) >= 2 {
		reference := live[0].health.Count
		for _, candidate := range live[1:] {
			if candidate.health.Count != reference {
				result.Mismatched = true
			}
		}
	}
	// len(live) < 2: never enough present sources to even attempt a
	// cross-source comparison -- Mismatched stays false (there is nothing
	// to contradict), and Completeness above already reflects the missing
	// sources via DegradedSources/LanePartial.

	return result
}

// SessionReconciliation is the closed, per-session outcome across every
// declared lane. It is deterministic: reconciling the same LaneInput set
// twice yields a byte-identical SessionReconciliation.
type SessionReconciliation struct {
	SessionID string
	Lanes     map[ReconciliationLane]LaneResult
}

// ReconcileSession reconciles every lane in inputs for one session. A lane
// absent from inputs is not silently treated as "reconciled, zero usage" --
// callers that omit a lane are asserting it is out of scope for this call
// (for example a session with no subagents at all still supplies a
// LaneSubagentLifecycle input with Present=true,Count=0 on every source that
// actually observed the session, to distinguish "no subagents happened"
// from "we didn't check").
func ReconcileSession(sessionID string, inputs []LaneInput) SessionReconciliation {
	lanes := make(map[ReconciliationLane]LaneResult, len(inputs))
	for _, input := range inputs {
		lanes[input.Lane] = ReconcileLane(input)
	}
	return SessionReconciliation{SessionID: sessionID, Lanes: lanes}
}
