package integrity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/claudeadapter"
	"kansoku.local/kansoku/internal/codexadapter"
	"kansoku.local/kansoku/internal/integrity"
)

// fixedNow is a deterministic clock for fixture replay tests so
// event_schema_fingerprint computation never depends on wall-clock time.
func fixedNowFunc() func() time.Time {
	fixed := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return fixed }
}

func buildAdapterFixtureSets(fixturesRoot string, now func() time.Time) (map[string]integrity.AdapterFixtureSet, error) {
	replay := func(decode func([]byte) (string, []byte, error)) integrity.HookFixtureReplayer {
		return func(_ context.Context, raw []byte) (integrity.FixtureReplayResult, error) {
			eventType, encoded, err := decode(raw)
			if err != nil {
				if errors.Is(err, codexadapter.ErrUnsupportedHookEvent) || errors.Is(err, claudeadapter.ErrUnsupportedHookEvent) {
					return integrity.FixtureReplayResult{Unsupported: true}, nil
				}
				return integrity.FixtureReplayResult{}, err
			}
			shape, err := integrity.StructuralShapeOf(encoded)
			if err != nil {
				return integrity.FixtureReplayResult{}, err
			}
			return integrity.FixtureReplayResult{Decoded: true, CanonicalEventType: eventType, FieldPaths: shape}, nil
		}
	}
	codexReplay := replay(func(raw []byte) (string, []byte, error) {
		input, err := codexadapter.DecodeHookInput(bytes.NewReader(raw))
		if err != nil {
			return "", nil, err
		}
		output, err := codexadapter.BuildHookOutput(input, now())
		if err != nil {
			return "", nil, err
		}
		if err := codexadapter.ValidateHookOutputAllowlist(output); err != nil {
			return "", nil, err
		}
		encoded, err := json.Marshal(output)
		return output.EventType, encoded, err
	})
	key := bytes.Repeat([]byte("k"), 32)
	claudeReplay := replay(func(raw []byte) (string, []byte, error) {
		input, err := claudeadapter.DecodeHookInput(bytes.NewReader(raw))
		if err != nil {
			return "", nil, err
		}
		output, err := claudeadapter.BuildHookOutput(input, key, now())
		if err != nil {
			return "", nil, err
		}
		if err := claudeadapter.ValidateHookOutputAllowlist(output); err != nil {
			return "", nil, err
		}
		encoded, err := json.Marshal(output)
		return output.EventType, encoded, err
	})
	return integrity.BuildAdapterFixtureSets(filepath.Join(fixturesRoot, "hook-otel-golden-map.json"), []integrity.FixtureReplayRegistration{
		{AdapterID: codexadapter.AdapterID, AdapterVersion: codexadapter.AdapterVersion, Replay: codexReplay},
		{AdapterID: claudeadapter.AdapterID, AdapterVersion: claudeadapter.AdapterVersion, Replay: claudeReplay},
	})
}

// TestSchemaParserCheckReplaysRealCodexAndClaudeFixturesCleanly proves the
// bundled tests/fixtures/session-06 golden map replays through BOTH real
// adapters' own DecodeHookInput/BuildHookOutput/ValidateHookOutputAllowlist
// paths (via BuildAdapterFixtureSets) without any parser_incompatibility
// finding, matching the documented "adapter's OWN bundled fixtures" contract.
func TestSchemaParserCheckReplaysRealCodexAndClaudeFixturesCleanly(t *testing.T) {
	sets, err := buildAdapterFixtureSets("../../tests/fixtures/session-06", fixedNowFunc())
	if err != nil {
		t.Fatalf("BuildAdapterFixtureSets: %v", err)
	}
	if len(sets) != 2 {
		t.Fatalf("BuildAdapterFixtureSets = %d adapters, want 2 (codex, claude)", len(sets))
	}
	check := integrity.NewSchemaParserCheck(sets, nil)
	in := integrity.CheckInput{AuditRunID: "run-schema-1", Now: time.Now()}
	targets, err := check.Targets(context.Background(), in)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("Targets = %+v, want exactly 2 (one per adapter)", targets)
	}
	for _, target := range targets {
		if target.CapabilityID != string(adaptersdk.CapabilityIngestionHistoricalImport) {
			t.Fatalf("target.CapabilityID = %s, want ingestion.historical_import", target.CapabilityID)
		}
		outcome, err := check.Evaluate(context.Background(), in, target)
		if err != nil {
			t.Fatalf("Evaluate(%s): %v", target.InstallationID, err)
		}
		if outcome.Status != integrity.CheckStatusPass {
			t.Fatalf("Evaluate(%s) = %+v, want pass: bundled fixtures must replay cleanly through the real adapter path", target.InstallationID, outcome)
		}
		if outcome.CheckID != integrity.SchemaParserCheckID {
			t.Fatalf("CheckID = %s, want %s", outcome.CheckID, integrity.SchemaParserCheckID)
		}
	}
}

// TestSchemaParserCheckUnrecognizedEventShapeIsCountedNeverCrashesBatch
// proves an unrecognized/never-before-seen event shape (empty
// CompatibilityRegistry so every replayed shape counts as new) is counted
// via the outcome's DetailRef, still returns Status=pass (new-shape counting
// alone is not itself a red-tier failure per new_shape_counting_rule), and
// evaluating a SECOND adapter/target in the same batch is entirely
// unaffected -- i.e. one target's "everything is new" outcome never aborts
// or corrupts a sibling target's evaluation.
func TestSchemaParserCheckUnrecognizedEventShapeIsCountedNeverCrashesBatch(t *testing.T) {
	sets, err := buildAdapterFixtureSets("../../tests/fixtures/session-06", fixedNowFunc())
	if err != nil {
		t.Fatalf("BuildAdapterFixtureSets: %v", err)
	}
	// A fresh, empty registry: no fingerprint has ever been recorded, so
	// every one of this run's decoded cases is a "new shape" by definition.
	check := integrity.NewSchemaParserCheck(sets, integrity.NewInMemoryCompatibilityRegistry())
	in := integrity.CheckInput{AuditRunID: "run-schema-2", Now: time.Now()}
	targets, err := check.Targets(context.Background(), in)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	seenAdapters := map[string]bool{}
	for _, target := range targets {
		outcome, err := check.Evaluate(context.Background(), in, target)
		if err != nil {
			t.Fatalf("Evaluate(%s) returned an error rather than a folded outcome: %v", target.InstallationID, err)
		}
		if outcome.Status != integrity.CheckStatusPass {
			t.Fatalf("Evaluate(%s) = %+v, want pass: new-shape counting alone is never a failure", target.InstallationID, outcome)
		}
		if outcome.DetailRef == "" {
			t.Fatalf("Evaluate(%s) DetailRef empty, want it to record new_shapes count", target.InstallationID)
		}
		seenAdapters[target.InstallationID] = true
	}
	if len(seenAdapters) != 2 {
		t.Fatalf("only %d/2 adapter targets were evaluated -- one target's outcome must not prevent evaluating the other", len(seenAdapters))
	}
}

// TestSchemaParserCheckKnownFingerprintIsNotCountedAsNewOnSecondRun proves a
// fingerprint recorded via CompatibilityRegistry.RecordFingerprint is no
// longer counted as "new" on a subsequent replay of the identical fixture
// shape, matching the compatibility-registry contract's whole purpose. It
// drives RecordFingerprint directly with every fingerprint a real replay of
// the fixture set would produce (the same computation stage 11's persistence
// step performs on this stage's behalf), then asserts DetailRef reports
// new_shapes=0 on the following Evaluate call.
func TestSchemaParserCheckKnownFingerprintIsNotCountedAsNewOnSecondRun(t *testing.T) {
	sets, err := buildAdapterFixtureSets("../../tests/fixtures/session-06", fixedNowFunc())
	if err != nil {
		t.Fatalf("BuildAdapterFixtureSets: %v", err)
	}
	codexOnly := map[string]integrity.AdapterFixtureSet{"codex": sets["codex"]}
	set := codexOnly["codex"]
	target := integrity.CheckTarget{CapabilityID: string(adaptersdk.CapabilityIngestionHistoricalImport), InstallationID: "codex"}
	in := integrity.CheckInput{AuditRunID: "run-schema-3", Now: time.Now()}

	// A fresh registry: every decoded case's fingerprint is new on this run.
	freshRegistry := integrity.NewInMemoryCompatibilityRegistry()
	freshCheck := integrity.NewSchemaParserCheck(codexOnly, freshRegistry)
	freshOutcome, err := freshCheck.Evaluate(context.Background(), in, target)
	if err != nil {
		t.Fatalf("Evaluate (fresh registry): %v", err)
	}
	if freshOutcome.Status != integrity.CheckStatusPass {
		t.Fatalf("fresh Evaluate = %+v, want pass", freshOutcome)
	}
	if strings.Contains(freshOutcome.DetailRef, "new_fingerprints=") ||
		!strings.Contains(freshOutcome.DetailRef, "new_shapes=") {
		t.Fatalf("fresh DetailRef = %q, want count-only new-shape evidence without persisting fingerprints", freshOutcome.DetailRef)
	}

	// Now record every fingerprint the fixture set will actually produce
	// (mirroring stage 11's persistence responsibility) and re-run.
	primedRegistry := integrity.NewInMemoryCompatibilityRegistry()
	for _, fixtureCase := range set.Cases {
		if fixtureCase.ExpectUnsupported {
			continue
		}
		result, err := set.Replay(context.Background(), fixtureCase.StdinJSON)
		if err != nil || !result.Decoded {
			continue
		}
		fingerprint := integrity.ComputeEventSchemaFingerprintForTest(result.CanonicalEventType, result.FieldPaths)
		if err := primedRegistry.RecordFingerprint(context.Background(), set.AdapterID, set.AdapterVersion, fingerprint); err != nil {
			t.Fatalf("RecordFingerprint: %v", err)
		}
	}
	primedCheck := integrity.NewSchemaParserCheck(codexOnly, primedRegistry)
	primedOutcome, err := primedCheck.Evaluate(context.Background(), in, target)
	if err != nil {
		t.Fatalf("Evaluate (primed registry): %v", err)
	}
	if primedOutcome.Status != integrity.CheckStatusPass {
		t.Fatalf("primed Evaluate = %+v, want pass", primedOutcome)
	}
	if !strings.Contains(primedOutcome.DetailRef, "new_shapes=0") {
		t.Fatalf("primed DetailRef = %q, want new_shapes=0 once every fingerprint is already known-compatible", primedOutcome.DetailRef)
	}
}

// TestSchemaParserCheckExpectedUnsupportedCaseIsNotAFailure proves the golden
// map's deliberately out-of-manifest event row (ExpectUnsupported=true) is
// treated as a pass condition, not folded into panicCount/unrecognized.
func TestSchemaParserCheckExpectedUnsupportedCaseIsNotAFailure(t *testing.T) {
	sets, err := buildAdapterFixtureSets("../../tests/fixtures/session-06", fixedNowFunc())
	if err != nil {
		t.Fatalf("BuildAdapterFixtureSets: %v", err)
	}
	foundUnsupportedCase := false
	for _, fixtureCase := range sets["codex"].Cases {
		if fixtureCase.ExpectUnsupported {
			foundUnsupportedCase = true
		}
	}
	if !foundUnsupportedCase {
		t.Fatalf("fixture set never produced an ExpectUnsupported case from the golden map's expect_unsupported row")
	}
	check := integrity.NewSchemaParserCheck(sets, nil)
	target := integrity.CheckTarget{CapabilityID: string(adaptersdk.CapabilityIngestionHistoricalImport), InstallationID: "codex"}
	outcome, err := check.Evaluate(context.Background(), integrity.CheckInput{Now: time.Now()}, target)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusPass {
		t.Fatalf("outcome = %+v, want pass: the deliberately-unsupported fixture row is expected to reject cleanly", outcome)
	}
}

// TestSchemaParserCheckPanickingReplayerNeverCrashesSurroundingBatch proves a
// HookFixtureReplayer that panics on a specific case degrades only that
// stage's outcome to fail/parser_incompatibility rather than propagating the
// panic out of Evaluate or preventing a sibling target's own Evaluate call.
func TestSchemaParserCheckPanickingReplayerNeverCrashesSurroundingBatch(t *testing.T) {
	panicky := integrity.AdapterFixtureSet{
		AdapterID: "panicky-adapter", AdapterVersion: "9.9.9", FixtureVersion: "1.0.0",
		Replay: func(context.Context, []byte) (integrity.FixtureReplayResult, error) {
			panic("synthetic_test_panic_inside_adapter_replay")
		},
		Cases: []integrity.FixtureCase{{CaseName: "panics-on-purpose", StdinJSON: []byte(`{}`)}},
	}
	healthy := integrity.AdapterFixtureSet{
		AdapterID: "healthy-adapter", AdapterVersion: "1.0.0", FixtureVersion: "1.0.0",
		Replay: func(context.Context, []byte) (integrity.FixtureReplayResult, error) {
			return integrity.FixtureReplayResult{
				Decoded: true, CanonicalEventType: "session.started",
				FieldPaths: []integrity.FieldPathType{{Path: "event_id", Type: "string"}},
			}, nil
		},
		Cases: []integrity.FixtureCase{{CaseName: "clean-case", StdinJSON: []byte(`{}`)}},
	}
	sets := map[string]integrity.AdapterFixtureSet{
		"panicky-adapter": panicky,
		"healthy-adapter": healthy,
	}
	check := integrity.NewSchemaParserCheck(sets, nil)
	in := integrity.CheckInput{AuditRunID: "run-panic-1", Now: time.Now()}

	// The panicking target's own Evaluate call must not itself panic (the
	// test harness would fail the whole test process if it did): it must
	// return a normal (outcome, nil) result with Status=fail.
	panickyOutcome, err := check.Evaluate(context.Background(), in, integrity.CheckTarget{CapabilityID: string(adaptersdk.CapabilityIngestionHistoricalImport), InstallationID: "panicky-adapter"})
	if err != nil {
		t.Fatalf("Evaluate(panicky-adapter) returned an error instead of a recovered outcome: %v", err)
	}
	if panickyOutcome.Status != integrity.CheckStatusFail {
		t.Fatalf("Evaluate(panicky-adapter) = %+v, want fail (recovered panic folded into outcome)", panickyOutcome)
	}
	if panickyOutcome.Category != string(integrity.FailureClassParserIncompatibility) {
		t.Fatalf("Category = %s, want %s", panickyOutcome.Category, integrity.FailureClassParserIncompatibility)
	}

	// The sibling healthy target must still evaluate cleanly afterward,
	// proving the panic never escaped to abort the surrounding batch/run.
	healthyOutcome, err := check.Evaluate(context.Background(), in, integrity.CheckTarget{CapabilityID: string(adaptersdk.CapabilityIngestionHistoricalImport), InstallationID: "healthy-adapter"})
	if err != nil {
		t.Fatalf("Evaluate(healthy-adapter): %v", err)
	}
	if healthyOutcome.Status != integrity.CheckStatusPass {
		t.Fatalf("Evaluate(healthy-adapter) = %+v, want pass: a sibling target's panic must never affect this one", healthyOutcome)
	}
}

// TestSchemaParserCheckUnknownAdapterTargetSkipsRatherThanErrors proves a
// CheckTarget naming an adapter_id with no registered AdapterFixtureSet is
// reported skipped_unsupported, not an error and not a crash.
func TestSchemaParserCheckUnknownAdapterTargetSkipsRatherThanErrors(t *testing.T) {
	check := integrity.NewSchemaParserCheck(map[string]integrity.AdapterFixtureSet{}, nil)
	outcome, err := check.Evaluate(context.Background(), integrity.CheckInput{Now: time.Now()}, integrity.CheckTarget{CapabilityID: string(adaptersdk.CapabilityIngestionHistoricalImport), InstallationID: "gemini"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusSkippedUnsupported {
		t.Fatalf("outcome = %+v, want skipped_unsupported for an adapter with no registered fixture set", outcome)
	}
}

// TestComputeEventSchemaFingerprintIsStableAndFieldOrderIndependent proves
// the exported StructuralShapeOf + fingerprint computation path used inside
// fixture replay is a pure function of (event_type, sorted field paths+types)
// and never depends on input field ORDER or on any field VALUE, matching
// drift-fingerprint-and-schema.yaml's event_schema_fingerprint computation.
func TestComputeEventSchemaFingerprintIsStableAndFieldOrderIndependent(t *testing.T) {
	shapeA, err := integrity.StructuralShapeOf([]byte(`{"b":"value-one","a":1}`))
	if err != nil {
		t.Fatalf("StructuralShapeOf A: %v", err)
	}
	shapeB, err := integrity.StructuralShapeOf([]byte(`{"a":999,"b":"totally-different-value"}`))
	if err != nil {
		t.Fatalf("StructuralShapeOf B: %v", err)
	}
	if len(shapeA) != len(shapeB) {
		t.Fatalf("shapes differ in length: %v vs %v", shapeA, shapeB)
	}
	for i := range shapeA {
		if shapeA[i] != shapeB[i] {
			t.Fatalf("shape at %d differs despite identical structure/types with different values: %+v vs %+v", i, shapeA[i], shapeB[i])
		}
	}
}

func TestStructuralShapeArrayCardinalityAndOrderDoNotChangeFingerprint(t *testing.T) {
	shapeA, err := integrity.StructuralShapeOf([]byte(`{"items":[{"name":"first","ok":true}]}`))
	if err != nil {
		t.Fatalf("StructuralShapeOf A: %v", err)
	}
	shapeB, err := integrity.StructuralShapeOf([]byte(`{"items":[{"ok":false,"name":"second"},{"name":"third","ok":true}]}`))
	if err != nil {
		t.Fatalf("StructuralShapeOf B: %v", err)
	}
	fingerprintA := integrity.ComputeEventSchemaFingerprintForTest("component.executed", shapeA)
	fingerprintB := integrity.ComputeEventSchemaFingerprintForTest("component.executed", shapeB)
	if fingerprintA == "" || fingerprintA != fingerprintB {
		t.Fatalf("array fingerprints differ across cardinality/order: %s vs %s; shapes=%v/%v", fingerprintA, fingerprintB, shapeA, shapeB)
	}
	if _, err := integrity.StructuralShapeOf([]byte(`{"items":[1,"two"]}`)); err == nil {
		t.Fatalf("heterogeneous array shape was silently coerced")
	}
}

func TestSchemaParserCheckHonorsContextCancellationForBlockedReplay(t *testing.T) {
	blocked := integrity.AdapterFixtureSet{
		AdapterID: "blocked-adapter", AdapterVersion: "1.0.0", FixtureVersion: "1.0.0",
		Replay: func(context.Context, []byte) (integrity.FixtureReplayResult, error) {
			select {}
		},
		Cases: []integrity.FixtureCase{{CaseName: "blocked", StdinJSON: []byte(`{}`)}},
	}
	check := integrity.NewSchemaParserCheck(map[string]integrity.AdapterFixtureSet{
		blocked.AdapterID: blocked,
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	outcome, err := check.Evaluate(ctx, integrity.CheckInput{Now: time.Now()}, integrity.CheckTarget{
		CapabilityID:   string(adaptersdk.CapabilityIngestionHistoricalImport),
		InstallationID: blocked.AdapterID,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("blocked replay ignored audit context cancellation")
	}
	if outcome.Status != integrity.CheckStatusFail ||
		outcome.Category != string(integrity.FailureClassParserIncompatibility) {
		t.Fatalf("outcome = %+v, want bounded parser_incompatibility", outcome)
	}
}
