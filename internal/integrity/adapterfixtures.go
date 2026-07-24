package integrity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// goldenMapHookSampleInput mirrors one entry of
// tests/fixtures/session-06/hook-otel-golden-map.json's hook_sample_inputs
// array, decoded generically so this package never forks a second copy of
// that fixture's schema.
type goldenMapHookSampleInput struct {
	Case                          string          `json:"case"`
	StdinJSON                     json.RawMessage `json:"stdin_json"`
	ExpectedEventType             string          `json:"expected_event_type"`
	ExpectedPromptFeaturesPresent bool            `json:"expected_prompt_features_present"`
}

type goldenMapHookRow struct {
	HookEventName              string `json:"hook_event_name"`
	ExpectedCanonicalEventType string `json:"expected_canonical_event_type"`
	ExpectUnsupported          bool   `json:"expect_unsupported"`
}

type hookOTELGoldenMap struct {
	FixtureVersion  string                     `json:"fixture_version"`
	Synthetic       bool                       `json:"synthetic"`
	HookGoldenMap   []goldenMapHookRow         `json:"hook_golden_map"`
	HookSampleInput []goldenMapHookSampleInput `json:"hook_sample_inputs"`
}

// LoadHookOTELGoldenMap reads and strictly decodes one adapter's bundled
// hook-otel-golden-map.json fixture (e.g.
// "../../tests/fixtures/session-06/hook-otel-golden-map.json" from
// internal/integrity's own test binary, or an equivalent path a real caller
// supplies). It never resamples fixture bytes from live data: this is the
// exact committed fixture content SchemaParserCheck replays.
func LoadHookOTELGoldenMap(path string) (hookOTELGoldenMap, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return hookOTELGoldenMap{}, fmt.Errorf("read golden map %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var golden hookOTELGoldenMap
	if err := decoder.Decode(&golden); err != nil {
		return hookOTELGoldenMap{}, fmt.Errorf("decode golden map %s: %w", path, err)
	}
	if !golden.Synthetic {
		return hookOTELGoldenMap{}, fmt.Errorf("golden map %s missing synthetic=true acceptance marker", path)
	}
	return golden, nil
}

// fixtureCasesFromGoldenMap converts a decoded hookOTELGoldenMap's
// hook_sample_inputs (plus the hook_golden_map's own
// expect_unsupported=true row, matched to sample input's stdin shape when
// present) into the ordered []FixtureCase SchemaParserCheck replays.
func fixtureCasesFromGoldenMap(golden hookOTELGoldenMap) []FixtureCase {
	unsupportedEventNames := map[string]bool{}
	for _, row := range golden.HookGoldenMap {
		if row.ExpectUnsupported {
			unsupportedEventNames[row.HookEventName] = true
		}
	}
	cases := make([]FixtureCase, 0, len(golden.HookSampleInput)+len(unsupportedEventNames))
	for _, sample := range golden.HookSampleInput {
		cases = append(cases, FixtureCase{CaseName: sample.Case, StdinJSON: sample.StdinJSON})
	}
	for eventName := range unsupportedEventNames {
		stdin, _ := json.Marshal(map[string]any{
			"hook_event_name": eventName,
			"session_id":      "sess-unsupported-golden-probe",
		})
		cases = append(cases, FixtureCase{CaseName: "unsupported:" + eventName, StdinJSON: stdin, ExpectUnsupported: true})
	}
	return cases
}

// FixtureReplayRegistration lets application wiring register any adapter's
// own fixture replayer without adding an agent name or import to the
// integrity core.
type FixtureReplayRegistration struct {
	AdapterID      string
	AdapterVersion string
	Replay         HookFixtureReplayer
}

// BuildAdapterFixtureSets builds the generic fixture registry from one
// committed golden-map path and caller-supplied adapter registrations.
func BuildAdapterFixtureSets(goldenMapPath string, registrations []FixtureReplayRegistration) (map[string]AdapterFixtureSet, error) {
	golden, err := LoadHookOTELGoldenMap(goldenMapPath)
	if err != nil {
		return nil, err
	}
	cases := fixtureCasesFromGoldenMap(golden)
	sets := make(map[string]AdapterFixtureSet, len(registrations))
	for _, registration := range registrations {
		if registration.AdapterID == "" || registration.AdapterVersion == "" || registration.Replay == nil {
			return nil, fmt.Errorf("fixture replay registration must provide adapter_id, version and replay")
		}
		if _, duplicate := sets[registration.AdapterID]; duplicate {
			return nil, fmt.Errorf("duplicate fixture replay registration: %s", registration.AdapterID)
		}
		sets[registration.AdapterID] = AdapterFixtureSet{
			AdapterID: registration.AdapterID, AdapterVersion: registration.AdapterVersion,
			FixtureVersion: golden.FixtureVersion, Replay: registration.Replay, Cases: cases,
		}
	}
	return sets, nil
}
