package claudeadapter

import "sort"

// This file implements the per-session cross-source activity reconciliation
// contracts/claude/skill-evidence-and-reconciliation.yaml's reconciliation
// block declares: hook (claude.hook) vs OTel (claude.otel) vs transcript
// (claude.transcript) evidence compared across eight lanes, using a
// versioned (never hardcoded) batching/terminal-delay tolerance, where a
// missing expected source degrades only that source's own capabilities/
// interval and never causes the reconciler to fabricate a
// plausible-looking zero-usage result for the whole session. This mirrors
// contracts/observability/reconciliation.yaml's expected_lanes/one_fact_rule
// shape verbatim, mapped onto claude.hook/claude.otel/claude.transcript
// exactly as reconciliation.expected_lanes_reused_from documents.

// ReconciliationLane is the closed, eight-member set of per-session
// comparisons contracts/claude/skill-evidence-and-reconciliation.yaml's
// reconciliation.per_session_comparisons declares. No ninth lane exists.
type ReconciliationLane string

const (
	LanePrompts           ReconciliationLane = "hook_prompt_events_vs_otel_prompt_events_vs_transcript_user_messages"
	LaneToolTerminal      ReconciliationLane = "hook_tool_terminal_events_vs_otel_results_vs_transcript_calls_and_outputs"
	LaneSessionLifecycle  ReconciliationLane = "session_start_stop_resume_vs_transcript_lifecycle"
	LaneSubagentLifecycle ReconciliationLane = "subagent_lifecycle_vs_parent_transcript_evidence"
	LaneMCPCalls          ReconciliationLane = "mcp_call_counts_vs_configured_and_advertised_tools"
	LaneSkillAttribution  ReconciliationLane = "skill_transcript_calls_vs_skill_otel_attribution_vs_tool_hooks"
	LanePluginOwnership   ReconciliationLane = "plugin_ownership_vs_bundled_component_inventory"
	LaneComponentEvidence ReconciliationLane = "component_explicit_load_execute_evidence_compared_without_forcing_equality"
)

// AllReconciliationLanes returns the closed, ordered lane vocabulary this
// package reconciles, mirroring
// contracts/claude/skill-evidence-and-reconciliation.yaml's
// reconciliation.per_session_comparisons verbatim so a validator/test can
// walk the identical list.
func AllReconciliationLanes() []ReconciliationLane {
	return []ReconciliationLane{
		LanePrompts, LaneToolTerminal, LaneSessionLifecycle, LaneSubagentLifecycle,
		LaneMCPCalls, LaneSkillAttribution, LanePluginOwnership, LaneComponentEvidence,
	}
}

// ReconciliationSourceID is the closed, three-member set of lane inputs this
// package's reconciler compares -- claude.hook, claude.otel,
// claude.transcript. claude.inventory is deliberately excluded as an
// activity-lane input: it is compared against configured-vs-observed MCP
// evidence inside LaneMCPCalls and against plugin-bundling evidence inside
// LanePluginOwnership, but it is never itself an activity lane input.
type ReconciliationSourceID string

const (
	ReconSourceHook       ReconciliationSourceID = sourceIDHook
	ReconSourceOTel       ReconciliationSourceID = sourceIDOTel
	ReconSourceTranscript ReconciliationSourceID = sourceIDTranscript
)

// ToleranceRegistryEntry is one versioned batching/terminal-delay tolerance
// entry, matching reconciliation.tolerance's
// "versioned_per_compatibility_registry_entry_not_hardcoded" requirement:
// the tolerance is data attached to a specific CompatibilityVersion, never a
// single hardcoded constant shared across every Claude Code release this
// adapter supports.
type ToleranceRegistryEntry struct {
	CompatibilityVersion string
	BatchingDelayMS      int64
	TerminalDelayMS      int64
}

// toleranceRegistry is this package's own closed, versioned tolerance table.
// A later validator stage checks this against
// contracts/claude/manifest.yaml's documented_version_gates so the two
// never drift silently; ResolveTolerance never falls back to a bare
// hardcoded number when a version is missing from this table -- it reports
// ok=false instead.
var toleranceRegistry = map[string]ToleranceRegistryEntry{
	"claude-compat/1": {CompatibilityVersion: "claude-compat/1", BatchingDelayMS: 2000, TerminalDelayMS: 5000},
}

// ResolveTolerance looks up the versioned tolerance entry for
// compatibilityVersion. ok is false when the version is unknown to this
// registry; the caller must treat that as "this comparison's tolerance is
// undetermined for this Claude Code release" and degrade accordingly rather
// than silently reusing another version's numbers.
func ResolveTolerance(compatibilityVersion string) (ToleranceRegistryEntry, bool) {
	entry, ok := toleranceRegistry[compatibilityVersion]
	return entry, ok
}

// SourceHealth is the closed, per-source health input to Reconcile: whether
// a source produced any lane evidence at all for this session, and (when it
// did) the counts/identities this lane needs. Present=false means "this
// source is missing for this session" -- never conflated with
// Present=true,Count=0 ("this source is healthy and genuinely observed zero
// activity"). This is the concrete "unknown is not zero" enforcement point:
// a caller that cannot observe a source at all must set Present=false, never
// Present=true with a fabricated Count=0.
type SourceHealth struct {
	Present bool
	Count   int
	// EventIdentities is the set of lane-scoped fact identities this source
	// observed (for example session_id+turn_id / session_id+tool_id pairs),
	// reused for the "same identity, different lane" comparison rather than
	// raw counts alone where the lane needs identity-level comparison
	// (subagent/session lifecycle, component evidence, skill attribution).
	EventIdentities []string
}

// LaneInput is the closed, per-lane input to ReconcileLane: this session's
// hook/otel/transcript SourceHealth for the one lane being compared, plus
// the compatibility version whose tolerance applies.
type LaneInput struct {
	Lane                 ReconciliationLane
	CompatibilityVersion string
	Hook                 SourceHealth
	OTel                 SourceHealth
	Transcript           SourceHealth
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
// Present, but their counts/identities disagree beyond
// ToleranceRegistryEntry; a missing source alone never sets Mismatched.
type LaneResult struct {
	Lane            ReconciliationLane
	Completeness    LaneCompleteness
	DegradedSources []ReconciliationSourceID
	Mismatched      bool
	HookCount       int
	OTelCount       int
	TranscriptCount int
}

// ReconcileLane compares one lane's hook/otel/transcript evidence for one
// session. A source with Present=false is recorded in DegradedSources and
// excluded from the count/identity comparison entirely -- it never
// contributes an implicit zero to that comparison, matching
// reconciliation.missing_source_rule verbatim: "missing one expected source
// marks only that source's own capabilities and reconciliation interval
// degraded; it never silently reports zero usage for the whole session."
//
// When two or more sources are Present, their counts are compared against
// each other. Mismatched is set when present-source counts disagree.
func ReconcileLane(input LaneInput) LaneResult {
	result := LaneResult{Lane: input.Lane}

	type present struct {
		id     ReconciliationSourceID
		health SourceHealth
	}
	all := []present{
		{ReconSourceHook, input.Hook},
		{ReconSourceOTel, input.OTel},
		{ReconSourceTranscript, input.Transcript},
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
	result.TranscriptCount = input.Transcript.Count

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
// LaneSubagentLifecycle input with Present=true,Count=0 on every source
// that actually observed the session, to distinguish "no subagents
// happened" from "we didn't check").
func ReconcileSession(sessionID string, inputs []LaneInput) SessionReconciliation {
	lanes := make(map[ReconciliationLane]LaneResult, len(inputs))
	for _, input := range inputs {
		lanes[input.Lane] = ReconcileLane(input)
	}
	return SessionReconciliation{SessionID: sessionID, Lanes: lanes}
}

// --- Skill/plugin cost and token attribution + double-attribution guard ---

// SkillCallObservation is one observed skill-related activity fact carrying
// a stable, cross-source event identity (for example a hook's EventID or
// an OTel record's own deterministic identity derived from
// session_id+turn_id+tool_id). The same underlying call may be visible
// through more than one source (for example both claude.otel's
// skill.name-attributed span and claude.hook's PreToolUse/PostToolUse pair
// for the same Skill tool invocation); Source records which source this
// particular observation came from so AttributeSkillCost can recognize and
// collapse duplicates by EventIdentity rather than by source alone.
type SkillCallObservation struct {
	EventIdentity string
	Source        ReconciliationSourceID
	SkillID       string
	// TokenCount/CostUSD are only ever populated when NativeSemanticsSupport
	// is true for this observation -- contracts/claude/skill-evidence-and-
	// reconciliation.yaml's cost_and_token_attribution rule requires token
	// and cost attribution to a skill be backed by native source semantics,
	// never inferred from a mismatched or unrelated call.
	TokenCount             int64
	CostUSD                float64
	NativeSemanticsSupport bool
	// ConcurrentSubagentShare is set when this observation is documented as
	// one of several concurrent subagents sharing a single billed call. This
	// is the documented potential-double-attribution case
	// cost_and_token_attribution requires be retained and surfaced, never
	// silently divided or summed away.
	ConcurrentSubagentShare bool
}

// SkillCostAttribution is one skill's aggregated, de-duplicated cost/token
// evidence for a session. ObservationCount is the number of distinct
// EventIdentity values that contributed (never a raw sum across every
// source that merely re-observed the same call); DuplicateObservations
// counts how many additional same-EventIdentity observations from a
// different source were recognized and excluded from the total -- this is
// the double-attribution guard's own visible proof that a call seen in
// both OTel and hooks was not counted twice. ConcurrentSubagentShareNoted is
// true when at least one contributing observation was itself flagged as a
// shared-call case; TotalTokens/TotalCostUSD are never computed by
// dividing or otherwise re-interpreting a shared-call observation -- the
// raw, retained value is summed once per distinct EventIdentity exactly as
// cost_and_token_attribution requires.
type SkillCostAttribution struct {
	SkillID                       string
	ObservationCount              int
	DuplicateObservationsExcluded int
	ConcurrentSubagentShareNoted  bool
	TotalTokens                   int64
	TotalCostUSD                  float64
}

// AttributeSkillCost aggregates token/cost evidence per skill from a set of
// SkillCallObservation values, enforcing the double-attribution guard: two
// observations sharing the same EventIdentity (regardless of which source
// produced them) are recognized as the *same* underlying call and
// contribute to the total exactly once, never once per source. Only
// observations with NativeSemanticsSupport=true ever contribute tokens/
// cost to a skill's total; an observation without native semantics support
// still counts toward disambiguating duplicates (so a hook-only
// observation of the same call a native OTel record already attributed
// cost to is still recognized as a duplicate) but never adds its own
// (absent) cost/token figures. When more than one *distinct* EventIdentity
// for the same skill is flagged ConcurrentSubagentShare, every one of them
// is still summed once each -- the guard collapses same-identity
// duplicates, never distinct concurrent calls, matching
// cost_and_token_attribution's "retained and surfaced rather than silently
// divided or summed" requirement (a genuinely distinct concurrent call is
// its own fact, not a duplicate of another).
func AttributeSkillCost(observations []SkillCallObservation) map[string]SkillCostAttribution {
	// identityKey groups every observation of the same underlying call
	// (same skill + same EventIdentity) together *before* any tokens/cost
	// decision is made, regardless of which source produced which
	// observation or what order they arrived in. This is the fix for the
	// double-attribution guard's own correctness: picking "whichever
	// observation is processed first" is not order-independent in the way
	// that matters here, because a non-native (e.g. hook-only, zero
	// tokens/cost) observation of the same call must never win over a
	// native-semantics observation of that same call merely by sorting
	// first -- the guard must always prefer the native-semantics-bearing
	// observation's figures when one exists among the duplicates.
	type identityKey struct {
		skillID  string
		identity string
	}
	groups := map[identityKey][]SkillCallObservation{}
	var order []identityKey
	for _, obs := range observations {
		if obs.SkillID == "" || obs.EventIdentity == "" {
			continue
		}
		key := identityKey{obs.SkillID, obs.EventIdentity}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], obs)
	}

	// Sort the distinct (skill, identity) keys so iteration order -- and
	// therefore every derived total -- is deterministic regardless of the
	// input slice's original order.
	sort.Slice(order, func(i, j int) bool {
		if order[i].skillID != order[j].skillID {
			return order[i].skillID < order[j].skillID
		}
		return order[i].identity < order[j].identity
	})

	bySkill := map[string]*SkillCostAttribution{}
	for _, key := range order {
		group := groups[key]
		entry, ok := bySkill[key.skillID]
		if !ok {
			entry = &SkillCostAttribution{SkillID: key.skillID}
			bySkill[key.skillID] = entry
		}

		entry.ObservationCount++
		if len(group) > 1 {
			// Every observation of this identity beyond the first counted
			// one is a duplicate sighting of the *same* underlying call
			// (for example both claude.otel and claude.hook observed the
			// same Skill tool invocation) -- it must never be counted
			// twice, matching the "visible in both OTel and hooks" guard.
			entry.DuplicateObservationsExcluded += len(group) - 1
		}
		for _, obs := range group {
			if obs.ConcurrentSubagentShare {
				entry.ConcurrentSubagentShareNoted = true
			}
		}

		// Prefer the native-semantics-bearing observation's figures when
		// collapsing duplicates: a call visible through both a native
		// OTel record (with real tokens/cost) and a hook-only observation
		// (with none) must still attribute the real, native tokens/cost --
		// never zero merely because the non-native observation happened to
		// sort first. When more than one observation in the group carries
		// native semantics (never expected for a single genuine call, but
		// never silently ignored either), their figures are summed so no
		// native evidence is dropped.
		for _, obs := range group {
			if obs.NativeSemanticsSupport {
				entry.TotalTokens += obs.TokenCount
				entry.TotalCostUSD += obs.CostUSD
			}
		}
	}

	result := make(map[string]SkillCostAttribution, len(bySkill))
	for skillID, entry := range bySkill {
		result[skillID] = *entry
	}
	return result
}
