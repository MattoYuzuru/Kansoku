package claudeadapter

import (
	"errors"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// EvidenceTier is reused verbatim from adaptersdk.EvidenceTier (itself
// mirroring contracts/observability/lifecycles.yaml's evidence_tiers): this
// file invents no new tier vocabulary, matching
// contracts/claude/skill-evidence-and-reconciliation.yaml's
// evidence_tiers_reused_from note exactly.
type EvidenceTier = adaptersdk.EvidenceTier

const (
	EvidenceTierNative        = adaptersdk.TierNative
	EvidenceTierReconstructed = adaptersdk.TierReconstructed
	EvidenceTierInferred      = adaptersdk.TierInferred
)

// SkillEvidenceKind is the closed, seven-member vocabulary
// contracts/claude/skill-evidence-and-reconciliation.yaml's
// skill_evidence_model.evidence_kinds declares. No eighth kind exists, and
// no code path in this package ever fabricates one.
type SkillEvidenceKind string

const (
	EvidenceSkillToolCallExplicit    SkillEvidenceKind = "skill_tool_call_explicit"
	EvidenceSkillToolCallImplicit    SkillEvidenceKind = "skill_tool_call_implicit"
	EvidenceOTelSkillNameAttribution SkillEvidenceKind = "otel_skill_name_attribution"
	EvidenceSkillMDLoad              SkillEvidenceKind = "skill_md_load_evidence"
	EvidencePluginOrMCPDeclaredUse   SkillEvidenceKind = "plugin_or_mcp_declared_use"
	EvidenceUniquelyOwnedHelperCall  SkillEvidenceKind = "uniquely_owned_helper_execution"
	EvidenceSemanticOpportunity      SkillEvidenceKind = "semantic_opportunity_classifier"
)

// componentInvoked/componentLoaded/componentExecuted/componentOpportunity
// are the closed canonical_event_type vocabulary a skill evidence kind may
// resolve to, reused verbatim from
// contracts/claude/skill-evidence-and-reconciliation.yaml's
// skill_evidence_model.evidence_kinds table.
const (
	CanonicalComponentInvoked     = "component.invoked"
	CanonicalComponentLoaded      = "component.loaded"
	CanonicalComponentExecuted    = "component.executed"
	CanonicalComponentOpportunity = "component.opportunity"
)

// skillEvidenceDefaultTier is the tier for each evidence kind, reused
// verbatim from
// contracts/claude/skill-evidence-and-reconciliation.yaml's evidence_kinds
// table: skill_tool_call_explicit/implicit and otel_skill_name_attribution
// are always "native" (they are native-labeled Skill tool calls or a
// documented native OTel attribute, never a reconstruction); skill_md_load_
// evidence/plugin_or_mcp_declared_use/uniquely_owned_helper_execution are
// "reconstructed"; semantic_opportunity_classifier is always "inferred" and
// can never resolve to any other tier.
var skillEvidenceDefaultTier = map[SkillEvidenceKind]EvidenceTier{
	EvidenceSkillToolCallExplicit:    EvidenceTierNative,
	EvidenceSkillToolCallImplicit:    EvidenceTierNative,
	EvidenceOTelSkillNameAttribution: EvidenceTierNative,
	EvidenceSkillMDLoad:              EvidenceTierReconstructed,
	EvidencePluginOrMCPDeclaredUse:   EvidenceTierReconstructed,
	EvidenceUniquelyOwnedHelperCall:  EvidenceTierReconstructed,
	EvidenceSemanticOpportunity:      EvidenceTierInferred,
}

// skillEvidenceCanonicalEvent is the closed evidence-kind -> canonical event
// type mapping, reused verbatim from
// contracts/claude/skill-evidence-and-reconciliation.yaml's evidence_kinds
// table.
var skillEvidenceCanonicalEvent = map[SkillEvidenceKind]string{
	EvidenceSkillToolCallExplicit:    CanonicalComponentInvoked,
	EvidenceSkillToolCallImplicit:    CanonicalComponentInvoked,
	EvidenceOTelSkillNameAttribution: CanonicalComponentInvoked,
	EvidenceSkillMDLoad:              CanonicalComponentLoaded,
	EvidencePluginOrMCPDeclaredUse:   CanonicalComponentInvoked,
	EvidenceUniquelyOwnedHelperCall:  CanonicalComponentExecuted,
	EvidenceSemanticOpportunity:      CanonicalComponentOpportunity,
}

// AllSkillEvidenceKinds returns the closed, ordered seven-member evidence
// kind vocabulary, mirroring
// contracts/claude/skill-evidence-and-reconciliation.yaml's evidence_kinds
// verbatim so a validator/test can walk the identical list.
func AllSkillEvidenceKinds() []SkillEvidenceKind {
	return []SkillEvidenceKind{
		EvidenceSkillToolCallExplicit, EvidenceSkillToolCallImplicit, EvidenceOTelSkillNameAttribution,
		EvidenceSkillMDLoad, EvidencePluginOrMCPDeclaredUse, EvidenceUniquelyOwnedHelperCall,
		EvidenceSemanticOpportunity,
	}
}

// ErrUnknownSkillEvidenceKind is returned for any SkillEvidenceKind outside
// the closed seven-member vocabulary.
var ErrUnknownSkillEvidenceKind = errors.New("claude_unknown_skill_evidence_kind")

// ErrAmbiguousOwnershipPromotion is returned by ResolveSkillEvidence when a
// caller attempts to resolve EvidenceUniquelyOwnedHelperCall with more than
// one candidate skill identity: contracts/claude/skill-evidence-and-
// reconciliation.yaml's ambiguous_ownership_rule states no rule ever
// converts a helper/MCP call into component.invoked when ownership across
// multiple candidate skill identity, plugin or subagent nodes is ambiguous;
// it remains component.executed with plural candidates. This package
// enforces that independently of any dashboard rendering logic -- the
// caller cannot even construct a resolved single-owner
// SkillEvidenceResolution for an ambiguous call; it can only obtain a
// SkillEvidenceResolution whose CandidateSkillIdentities has more than one
// entry and whose CanonicalEventType is still component.executed (never
// promoted to component.invoked).
var ErrAmbiguousOwnershipPromotion = errors.New("claude_ambiguous_ownership_never_promoted_to_invoked")

// SkillEvidenceInput is the closed input to ResolveSkillEvidence: one
// observed evidence kind, the candidate skill/plugin/subagent identities it
// could be attributed to (exactly one for an unambiguous call, more than one
// for a genuinely ambiguous shared helper/MCP dependency), and whether the
// native Skill tool call or hook field itself distinguishes explicit vs.
// implicit invocation mode. ModeKnown=false means the native source did not
// itself label a mode; the caller must render mode as "unknown", never a
// guessed explicit/implicit value, matching
// explicit_vs_implicit_mode_rule verbatim.
type SkillEvidenceInput struct {
	Kind                     SkillEvidenceKind
	CandidateSkillIdentities []string
	ModeKnown                bool
}

// SkillEvidenceResolution is the closed, durable-safe outcome of resolving
// one SkillEvidenceInput. CanonicalEventType and Tier are exactly the pair
// contracts/claude/skill-evidence-and-reconciliation.yaml's
// native_exact_activation_prohibition forbids ever conflating: Tier is
// never silently upgraded to native for a reconstructed/inferred kind, and
// CanonicalEventType is never component.invoked when
// CandidateSkillIdentities has more than one member. Mode is "explicit",
// "implicit" or "unknown"; it is only ever "explicit"/"implicit" for the
// two skill_tool_call_* kinds and only when ModeKnown was true on the input.
type SkillEvidenceResolution struct {
	Kind                     SkillEvidenceKind
	CanonicalEventType       string
	Tier                     EvidenceTier
	CandidateSkillIdentities []string
	Mode                     string
}

// ResolveSkillEvidence maps one observed SkillEvidenceInput onto its closed
// canonical event type and evidence tier, enforcing the invariants that are
// this session's central exit-gate text:
//
//  1. no_false_exact_count / native_exact_activation_prohibition: the
//     returned Tier is always the evidence kind's own documented default
//     tier -- semantic_opportunity_classifier can never resolve to
//     anything but "inferred", and reconstructed kinds never become
//     "native".
//  2. explicit_vs_implicit_mode_rule: Mode is "explicit"/"implicit" only for
//     the two skill_tool_call_* kinds, and only when ModeKnown is true; an
//     absent native distinction yields Mode="unknown", never a guessed
//     mode.
//  3. ambiguous_ownership_rule: EvidenceUniquelyOwnedHelperCall with more
//     than one candidate skill identity never resolves to
//     component.invoked; it remains component.executed with every
//     candidate preserved, and ResolveSkillEvidence signals this
//     explicitly via a non-nil error the caller must not discard, rather
//     than silently downgrading in a way a careless caller could overlook.
func ResolveSkillEvidence(input SkillEvidenceInput) (SkillEvidenceResolution, error) {
	canonical, ok := skillEvidenceCanonicalEvent[input.Kind]
	if !ok {
		return SkillEvidenceResolution{}, ErrUnknownSkillEvidenceKind
	}
	tier := skillEvidenceDefaultTier[input.Kind]

	mode := "unknown"
	if input.ModeKnown {
		switch input.Kind {
		case EvidenceSkillToolCallExplicit:
			mode = "explicit"
		case EvidenceSkillToolCallImplicit:
			mode = "implicit"
		}
	}

	candidates := append([]string(nil), input.CandidateSkillIdentities...)
	resolution := SkillEvidenceResolution{
		Kind:                     input.Kind,
		CanonicalEventType:       canonical,
		Tier:                     tier,
		CandidateSkillIdentities: candidates,
		Mode:                     mode,
	}

	if input.Kind == EvidenceUniquelyOwnedHelperCall && len(candidates) > 1 {
		// Ambiguous ownership: the canonical event type and tier are left at
		// their unpromoted defaults (component.executed / reconstructed) --
		// resolution is still returned (never a plausible-looking zero) but
		// paired with a non-nil error the caller must check before ever
		// treating this as an exact invocation.
		return resolution, ErrAmbiguousOwnershipPromotion
	}
	if input.Kind == EvidenceUniquelyOwnedHelperCall && len(candidates) == 0 {
		return SkillEvidenceResolution{}, errors.New("claude_helper_execution_requires_at_least_one_candidate")
	}
	return resolution, nil
}

// SourceToCanonicalMapping is one row of the source-to-canonical mapping
// table from contracts/claude/skill-evidence-and-reconciliation.yaml, kept
// as explicit, enumerable data (rather than only comments) so a validator
// or test can walk every row and check it against the contract file
// mechanically.
type SourceToCanonicalMapping struct {
	SourceEvidence     string
	CanonicalEventType string
	Tier               string
}

// SourceToCanonicalTable is the closed, ordered source-to-canonical mapping
// table reused verbatim from
// contracts/claude/skill-evidence-and-reconciliation.yaml's
// source_to_canonical_mapping. It is exported so a validator/test walks the
// identical table this package's Go logic actually implements, rather than
// re-deriving it from prose.
func SourceToCanonicalTable() []SourceToCanonicalMapping {
	return []SourceToCanonicalMapping{
		{"SessionStart_hook_or_otel_session_started_event", "session.started", "native"},
		{"user_prompt_hook_or_otel_metadata", "prompt.submitted", "native"},
		{"tool_pre_post_hook_or_otel_result", "tool.called_plus_terminal", "native"},
		{"mcp_tool_name", "tool.*_with_mcp_component_relation", "native"},
		{"Skill_tool_call_explicit_or_implicit_native_field", "component.invoked", "native"},
		{"otel_skill.name_plugin.name_agent.name_attribution", "component.invoked_or_subagent.started_per_attribute", "native"},
		{"bounded_skill_md_load_evidence", "component.loaded", "reconstructed"},
		{"uniquely_owned_helper_call", "component.executed", "reconstructed"},
		{"semantic_opportunity_classifier", "component.opportunity", "inferred"},
	}
}
