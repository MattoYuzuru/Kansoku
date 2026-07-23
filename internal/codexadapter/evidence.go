package codexadapter

import (
	"errors"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// EvidenceTier is reused verbatim from adaptersdk.EvidenceTier (itself
// mirroring contracts/observability/lifecycles.yaml's evidence_tiers): this
// file invents no new tier vocabulary, matching
// contracts/codex/skill-evidence-and-reconciliation.yaml's
// evidence_tiers_reused_from note exactly. Of the four shared tiers, no
// codex skill-evidence kind maps to TierCorroborated; only Native,
// Reconstructed and Inferred are used below.
type EvidenceTier = adaptersdk.EvidenceTier

const (
	EvidenceTierNative        = adaptersdk.TierNative
	EvidenceTierReconstructed = adaptersdk.TierReconstructed
	EvidenceTierInferred      = adaptersdk.TierInferred
)

// SkillEvidenceKind is the closed, five-member vocabulary
// contracts/codex/skill-evidence-and-reconciliation.yaml's
// skill_evidence_model.evidence_kinds declares. No sixth kind exists, and no
// code path in this package ever fabricates one.
type SkillEvidenceKind string

const (
	EvidenceExplicitUserInvocation  SkillEvidenceKind = "explicit_user_invocation"
	EvidenceSkillMDLoad             SkillEvidenceKind = "skill_md_load_evidence"
	EvidenceAgentDeclaredUse        SkillEvidenceKind = "agent_declared_use"
	EvidenceUniquelyOwnedHelperCall SkillEvidenceKind = "uniquely_owned_helper_execution"
	EvidenceSemanticOpportunity     SkillEvidenceKind = "semantic_opportunity_classifier"
)

// componentInvoked/componentLoaded/componentExecuted/componentOpportunity
// are the closed canonical_event_type vocabulary a skill evidence kind may
// resolve to, reused verbatim from
// contracts/codex/skill-evidence-and-reconciliation.yaml's
// skill_evidence_model.evidence_kinds table.
const (
	CanonicalComponentInvoked     = "component.invoked"
	CanonicalComponentLoaded      = "component.loaded"
	CanonicalComponentExecuted    = "component.executed"
	CanonicalComponentOpportunity = "component.opportunity"
)

// skillEvidenceDefaultTier is the default (source-does-not-itself-label-it)
// tier for each evidence kind, reused verbatim from
// contracts/codex/skill-evidence-and-reconciliation.yaml's evidence_kinds
// table. EvidenceExplicitUserInvocation is "native_when_source_labels_it_
// reconstructed_otherwise": SourceLabelsNative in EvidenceInput carries that
// distinction; this map only holds the "otherwise" default.
var skillEvidenceDefaultTier = map[SkillEvidenceKind]EvidenceTier{
	EvidenceExplicitUserInvocation:  EvidenceTierReconstructed,
	EvidenceSkillMDLoad:             EvidenceTierReconstructed,
	EvidenceAgentDeclaredUse:        EvidenceTierReconstructed,
	EvidenceUniquelyOwnedHelperCall: EvidenceTierReconstructed,
	EvidenceSemanticOpportunity:     EvidenceTierInferred,
}

// skillEvidenceCanonicalEvent is the closed evidence-kind -> canonical event
// type mapping, reused verbatim from
// contracts/codex/skill-evidence-and-reconciliation.yaml's evidence_kinds
// table.
var skillEvidenceCanonicalEvent = map[SkillEvidenceKind]string{
	EvidenceExplicitUserInvocation:  CanonicalComponentInvoked,
	EvidenceSkillMDLoad:             CanonicalComponentLoaded,
	EvidenceAgentDeclaredUse:        CanonicalComponentInvoked,
	EvidenceUniquelyOwnedHelperCall: CanonicalComponentExecuted,
	EvidenceSemanticOpportunity:     CanonicalComponentOpportunity,
}

// ErrUnknownSkillEvidenceKind is returned for any SkillEvidenceKind outside
// the closed five-member vocabulary.
var ErrUnknownSkillEvidenceKind = errors.New("codex_unknown_skill_evidence_kind")

// ErrAmbiguousOwnershipPromotion is returned by ResolveSkillEvidence when a
// caller attempts to resolve EvidenceUniquelyOwnedHelperCall with more than
// one candidate skill identity: contracts/codex/skill-evidence-and-reconciliation.yaml's
// ambiguous_ownership_rule states no rule ever converts a helper/MCP call
// into component.invoked when ownership is ambiguous, and this package
// enforces that independently of any dashboard rendering logic -- the
// caller cannot even construct a resolved single-owner
// SkillEvidenceResolution for an ambiguous call; it can only obtain a
// SkillEvidenceResolution whose CandidateSkillIdentities has more than one
// entry and whose CanonicalEventType is still component.executed (never
// promoted to component.invoked).
var ErrAmbiguousOwnershipPromotion = errors.New("codex_ambiguous_ownership_never_promoted_to_invoked")

// SkillEvidenceInput is the closed input to ResolveSkillEvidence: one
// observed evidence kind, the candidate skill identities it could be
// attributed to (exactly one for an unambiguous call, more than one for a
// genuinely ambiguous shared helper/MCP dependency), and whether the source
// itself already labeled an explicit invocation as native (for example a
// hook/OTel field that unambiguously names the invoked skill, as opposed to
// an ephemeral in-memory prompt-text match).
type SkillEvidenceInput struct {
	Kind                     SkillEvidenceKind
	CandidateSkillIdentities []string
	SourceLabelsNative       bool
}

// SkillEvidenceResolution is the closed, durable-safe outcome of resolving
// one SkillEvidenceInput. CanonicalEventType and Tier are exactly the pair
// contracts/codex/skill-evidence-and-reconciliation.yaml's
// native_exact_activation_prohibition forbids ever conflating: Tier is
// never silently upgraded to native for a reconstructed/inferred kind, and
// CanonicalEventType is never component.invoked when
// CandidateSkillIdentities has more than one member.
type SkillEvidenceResolution struct {
	Kind                     SkillEvidenceKind
	CanonicalEventType       string
	Tier                     EvidenceTier
	CandidateSkillIdentities []string
}

// ResolveSkillEvidence maps one observed SkillEvidenceInput onto its closed
// canonical event type and evidence tier, enforcing two invariants that are
// this session's central exit-gate text:
//
//  1. no_false_exact_count / native_exact_activation_prohibition: the
//     returned Tier is never "native" unless kind is
//     EvidenceExplicitUserInvocation *and* the source itself labeled it
//     native; every other kind resolves to its documented default tier,
//     and EvidenceSemanticOpportunity can never resolve to anything but
//     "inferred".
//  2. ambiguous_ownership_rule: EvidenceUniquelyOwnedHelperCall with more
//     than one candidate skill identity never resolves to
//     component.invoked; it remains component.executed with every
//     candidate preserved, and ResolveSkillEvidence signals this
//     explicitly via a non-nil error the caller must not discard, rather
//     than silently downgrading in a way a careless caller could
//     overlook.
func ResolveSkillEvidence(input SkillEvidenceInput) (SkillEvidenceResolution, error) {
	canonical, ok := skillEvidenceCanonicalEvent[input.Kind]
	if !ok {
		return SkillEvidenceResolution{}, ErrUnknownSkillEvidenceKind
	}
	tier := skillEvidenceDefaultTier[input.Kind]
	if input.Kind == EvidenceExplicitUserInvocation && input.SourceLabelsNative {
		tier = EvidenceTierNative
	}

	candidates := append([]string(nil), input.CandidateSkillIdentities...)
	resolution := SkillEvidenceResolution{
		Kind:                     input.Kind,
		CanonicalEventType:       canonical,
		Tier:                     tier,
		CandidateSkillIdentities: candidates,
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
		return SkillEvidenceResolution{}, errors.New("codex_helper_execution_requires_at_least_one_candidate")
	}
	return resolution, nil
}

// SourceToCanonicalMapping is one row of the source-to-canonical mapping
// table from contracts/codex/skill-evidence-and-reconciliation.yaml, kept
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
// contracts/codex/skill-evidence-and-reconciliation.yaml's
// source_to_canonical_mapping. It is exported so a validator/test walks the
// identical table this package's Go logic actually implements, rather than
// re-deriving it from prose.
func SourceToCanonicalTable() []SourceToCanonicalMapping {
	return []SourceToCanonicalMapping{
		{"SessionStart_or_conversation_start", "session.started", "native"},
		{"user_prompt_hook_or_otel_metadata", "prompt.submitted", "native"},
		{"tool_pre_post_or_result", "tool.called_plus_terminal", "native"},
		{"mcp_tool_name", "tool.*_with_mcp_component_relation", "native"},
		{"$skill_explicit_mention_safely_matched_to_inventory", "component.invoked_explicit", "reconstructed_or_native_when_source_labels_it"},
		{"bounded_skill_md_load_evidence", "component.loaded", "reconstructed"},
		{"uniquely_owned_helper_call", "component.executed", "reconstructed"},
		{"semantic_opportunity_classifier", "component.opportunity", "inferred"},
	}
}
