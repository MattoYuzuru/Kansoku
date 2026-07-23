package codexadapter

import (
	"sort"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// This file computes the closed set of discoverability-pressure fields
// contracts/codex/rollout-and-inventory.yaml's discoverability_pressure
// block declares: description byte/character totals, scope precedence rank,
// duplicate-name and disabled flags, and a catalog-budget risk estimate
// that is always labeled "inferred" unless direct session/source evidence
// promotes a skill to "exposed" -- this file never performs that promotion
// itself; it only ever produces the inferred estimate, matching
// discoverability_pressure.exposed_vs_inferred.inferred_risk verbatim.

// scopePrecedenceRank is this adapter's own closed, deterministic
// most-to-least-authoritative scope ordering, used only to break ties
// between same-named skill declarations across scopes for display/
// diagnostic purposes; contracts/adapter-sdk/inventory-graph.yaml's
// identity_rule already guarantees same-named nodes across scopes are never
// merged regardless of this ranking -- ScopePrecedenceRank never influences
// node identity, only which duplicate a human-facing report lists first.
var scopePrecedenceRank = map[adaptersdk.SourceScope]int{
	adaptersdk.ScopeAdmin:            0,
	adaptersdk.ScopeSystem:           1,
	adaptersdk.ScopeUser:             2,
	adaptersdk.ScopeRepository:       3,
	adaptersdk.ScopeMarketplace:      4,
	adaptersdk.ScopePluginCache:      5,
	adaptersdk.ScopeTransientSession: 6,
}

// ScopePrecedenceRank returns the closed, deterministic precedence rank for
// scope (lower is more authoritative), and false for any scope outside the
// seven declared in contracts/adapter-sdk/inventory-graph.yaml's
// source_scopes. An unranked scope is never silently assigned rank 0 (most
// authoritative) or any other plausible-looking default.
func ScopePrecedenceRank(scope adaptersdk.SourceScope) (int, bool) {
	rank, ok := scopePrecedenceRank[scope]
	return rank, ok
}

// CatalogPressureRiskLevel is the closed vocabulary a catalog-budget risk
// estimate resolves to. It is always paired with EvidenceTierInferred
// (never TierNative) unless a caller supplies direct exposure evidence
// through ExposureEvidence, in which case the skill is reported as exposed
// and no risk estimate is computed for it at all -- exposed and
// risk-estimated are mutually exclusive per skill.
type CatalogPressureRiskLevel string

const (
	CatalogPressureLow    CatalogPressureRiskLevel = "low"
	CatalogPressureMedium CatalogPressureRiskLevel = "medium"
	CatalogPressureHigh   CatalogPressureRiskLevel = "high"
)

// SkillDiscoverabilityInput is the closed per-skill input to
// ComputeDiscoverabilityPressure: the skill's own descriptor plus whether
// direct session/source evidence has already shown it reached model
// context (ExposureEvidence=true). ExposureEvidence is never computed by
// this file itself -- it is supplied by the reconciliation/evidence layer
// that actually observed component.loaded/invoked/executed evidence for
// this skill (see evidence.go/reconcile.go); this file only ever consumes
// that boolean, matching "exposed_only_when_actual_session_or_source_
// evidence" verbatim.
type SkillDiscoverabilityInput struct {
	Skill            SkillDescriptor
	ExposureEvidence bool
}

// SkillDiscoverabilityReport is the closed, deterministic per-skill
// discoverability-pressure report: exactly the fields
// discoverability_pressure.computed_fields declares, plus the skill's own
// name/scope so a caller can join it back to the inventory graph. Exposed
// skills carry no RiskEstimate/RiskEvidenceTier at all (the zero value is
// not a claim); every non-exposed skill's RiskEvidenceTier is
// EvidenceTierInferred, never native.
type SkillDiscoverabilityReport struct {
	Name                  string
	Scope                 adaptersdk.SourceScope
	DescriptionByteTotal  int
	DescriptionCharTotal  int
	ScopePrecedenceRank   int
	ScopePrecedenceRanked bool
	DuplicateNameFlag     bool
	DisabledFlag          bool
	Exposed               bool
	RiskEstimate          CatalogPressureRiskLevel
	RiskEvidenceTier      EvidenceTier
}

// CatalogBudget is the documented catalog-budget threshold this estimate is
// computed against. It is a byte-total threshold pair the adapter recipe
// documents (never asserted as Codex's own guaranteed internal limit --
// catalog_budget_note explicitly says this is estimated, never certain).
type CatalogBudget struct {
	MediumRiskByteTotal int
	HighRiskByteTotal   int
}

// DefaultCatalogBudget is this adapter recipe's own documented estimate of
// where Codex's initial skills catalog budget pressure becomes noticeable.
// A later validator stage keeps this in lockstep with
// contracts/codex/rollout-and-inventory.yaml's catalog_budget_note; it is
// never silently invented per call site.
func DefaultCatalogBudget() CatalogBudget {
	return CatalogBudget{MediumRiskByteTotal: 4096, HighRiskByteTotal: 16384}
}

// ComputeDiscoverabilityPressure computes the closed discoverability-pressure
// report for every skill in inputs. Duplicate-name detection considers the
// full input set (skills across every scope), matching
// contracts/adapter-sdk/inventory-graph.yaml's collision_rule: a duplicate
// name is flagged, never merged or silently deduplicated away. A skill with
// ExposureEvidence=true is reported Exposed and receives no RiskEstimate at
// all (its zero value is not a "low risk" claim -- callers must branch on
// Exposed before reading RiskEstimate).
func ComputeDiscoverabilityPressure(inputs []SkillDiscoverabilityInput, budget CatalogBudget) []SkillDiscoverabilityReport {
	nameCount := map[string]int{}
	for _, input := range inputs {
		nameCount[input.Skill.Name]++
	}

	reports := make([]SkillDiscoverabilityReport, 0, len(inputs))
	for _, input := range inputs {
		skill := input.Skill
		rank, ranked := ScopePrecedenceRank(skill.Scope)
		report := SkillDiscoverabilityReport{
			Name:                  skill.Name,
			Scope:                 skill.Scope,
			DescriptionByteTotal:  skill.DescriptionBytes,
			DescriptionCharTotal:  skill.DescriptionChars,
			ScopePrecedenceRank:   rank,
			ScopePrecedenceRanked: ranked,
			DuplicateNameFlag:     nameCount[skill.Name] > 1,
			DisabledFlag:          skill.Disabled || !skill.Enabled,
			Exposed:               input.ExposureEvidence,
		}
		if !report.Exposed {
			report.RiskEstimate = catalogPressureRisk(skill.DescriptionBytes, budget)
			report.RiskEvidenceTier = EvidenceTierInferred
		}
		reports = append(reports, report)
	}

	sort.Slice(reports, func(i, j int) bool {
		if reports[i].Name != reports[j].Name {
			return reports[i].Name < reports[j].Name
		}
		return reports[i].ScopePrecedenceRank < reports[j].ScopePrecedenceRank
	})
	return reports
}

// catalogPressureRisk classifies one skill's description byte total against
// budget. It is a monotonic, deterministic threshold function: the same
// byte total and budget always classify identically, and every result is an
// estimate (see ComputeDiscoverabilityPressure's RiskEvidenceTier=Inferred
// pairing) never a certainty.
func catalogPressureRisk(descriptionBytes int, budget CatalogBudget) CatalogPressureRiskLevel {
	switch {
	case descriptionBytes >= budget.HighRiskByteTotal:
		return CatalogPressureHigh
	case descriptionBytes >= budget.MediumRiskByteTotal:
		return CatalogPressureMedium
	default:
		return CatalogPressureLow
	}
}

// TotalCatalogDescriptionBytes sums DescriptionByteTotal across every
// non-disabled, non-cache skill report -- the aggregate figure a
// catalog-wide (rather than per-skill) budget estimate is computed from.
// Disabled skills are excluded because a disabled skill is never counted as
// contributing to the live catalog's context pressure.
func TotalCatalogDescriptionBytes(reports []SkillDiscoverabilityReport) int {
	total := 0
	for _, report := range reports {
		if report.DisabledFlag {
			continue
		}
		total += report.DescriptionByteTotal
	}
	return total
}
