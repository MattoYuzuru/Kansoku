package dataplatform

import (
	"testing"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/observability"
)

// TestCanonicalSourceScopeFilterNeverNarrowsOnUnknownVocabulary pins the
// behaviour that turned Claude Code's skill telemetry into a silent zero: the
// resolver's `node.source_scope = $5` predicate was fed the raw attribute
// value. Claude sends skill.source="plugin", which is not a member of
// adaptersdk.SourceScope -- inventory stores those same skills at
// "plugin_cache" -- so every candidate set narrowed to nothing while the
// assertion still reported a confident "unresolved".
//
// A non-vocabulary value must widen (empty filter) and be classified
// "unknown", which is a different state from "not_observed" and must never be
// coerced onto the nearest-looking member.
func TestCanonicalSourceScopeFilterNeverNarrowsOnUnknownVocabulary(t *testing.T) {
	for _, test := range []struct {
		raw        string
		wantFilter string
		wantState  string
	}{
		{string(adaptersdk.ScopeSystem), "system", "observed"},
		{string(adaptersdk.ScopeUser), "user", "observed"},
		{string(adaptersdk.ScopeRepository), "repository", "observed"},
		{string(adaptersdk.ScopeAdmin), "admin", "observed"},
		{string(adaptersdk.ScopeMarketplace), "marketplace", "observed"},
		{string(adaptersdk.ScopePluginCache), "plugin_cache", "observed"},
		{string(adaptersdk.ScopeTransientSession), "transient_session", "observed"},
		// The exact value Claude Code 2.1.220 sends.
		{"plugin", "", "unknown"},
		// The plugin.scope sibling, non-vocabulary in the same way.
		{"user-local", "", "unknown"},
		{"Plugin_Cache", "", "unknown"},
		{"", "", "not_observed"},
	} {
		t.Run(test.raw, func(t *testing.T) {
			filter, state := canonicalSourceScopeFilter(test.raw)
			if filter != test.wantFilter || state != test.wantState {
				t.Fatalf("canonicalSourceScopeFilter(%q)=(%q,%q) want (%q,%q)",
					test.raw, filter, state, test.wantFilter, test.wantState)
			}
		})
	}
}

// TestCanonicalSourceScopeFilterNeverCoercesPluginToPluginCache states the
// rejected alternative directly (ADR 0023 decision 2). Mapping "plugin" onto
// "plugin_cache" would look like a fix and would resolve the skills that
// happen to live in the cache, while recreating the identical silent zero for
// every plugin-bundled component that does not.
func TestCanonicalSourceScopeFilterNeverCoercesPluginToPluginCache(t *testing.T) {
	if filter, _ := canonicalSourceScopeFilter("plugin"); filter == string(adaptersdk.ScopePluginCache) {
		t.Fatal("\"plugin\" was coerced to plugin_cache")
	}
}

// TestObservabilityScopeClassifiesSourceScopeState proves the classification
// reaches the durable scope every assertion is written from, so the raw value
// and its state travel together onto the row.
func TestObservabilityScopeClassifiesSourceScopeState(t *testing.T) {
	for _, test := range []struct{ raw, wantState string }{
		{"plugin_cache", "observed"},
		{"plugin", "unknown"},
		{"", "not_observed"},
	} {
		event := observability.Event{
			EventType: "component.invoked",
			Subject:   observability.Subject{Kind: "skill"},
			Source:    observability.SourceRef{AdapterID: "claude"},
			ComponentEvidence: observability.ComponentEvidenceMetadata{
				QualifiedIdentity: "owner:skill", SourceScope: test.raw,
			},
		}
		scope := ObservabilityScope(event)
		if scope.ComponentSourceScope != test.raw {
			t.Fatalf("raw scope=%q want %q", scope.ComponentSourceScope, test.raw)
		}
		if scope.ComponentSourceScopeState != test.wantState {
			t.Fatalf("state for %q=%q want %q", test.raw, scope.ComponentSourceScopeState, test.wantState)
		}
	}
}
