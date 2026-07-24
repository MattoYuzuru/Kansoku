package integrity

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// SchemaParserCheckID is the check_id every SchemaParserCheck outcome
// reports, matching audit-run-and-schedule.yaml's stage_4_parser_fixture_replay
// stage and incident-and-health.yaml's "schema_compatibility"/
// "parser_fixture_status" health dimensions.
const SchemaParserCheckID = "stage_4_parser_fixture_replay"

// FixtureCase is one bundled sample input a HookFixtureReplayer replays
// through its adapter's own DecodeHookInput/BuildHookOutput/
// ValidateHookOutputAllowlist path, e.g. one row of
// tests/fixtures/session-06/hook-otel-golden-map.json's hook_sample_inputs or
// tests/fixtures/session-07's equivalent. CaseName is only used for
// DetailRef/debugging text and is never a durable identity by itself.
type FixtureCase struct {
	CaseName string
	// StdinJSON is the exact bytes DecodeHookInput would read from the hook
	// helper's stdin for this fixture case -- already-committed fixture
	// bytes, never resampled from live data.
	StdinJSON []byte
	// ExpectUnsupported marks a fixture case that is deliberately an event
	// name outside the adapter's manifested vocabulary (e.g. golden map's
	// "SomeFutureHookEventNotYetManifested" row): DecodeHookInput/BuildHookOutput
	// are expected to reject it, and that rejection itself is the fixture's
	// pass condition, not a failure.
	ExpectUnsupported bool
}

// HookFixtureReplayer is the adapter-owned replay function a real caller
// closes over that adapter's own codexadapter.DecodeHookInput/BuildHookOutput/
// ValidateHookOutputAllowlist (or claudeadapter's, or any future adapter's)
// triplet. SchemaParserCheck never re-implements hook decoding/normalization
// itself: it only drives whichever adapter-owned replayer is registered for
// an adapter_id, exactly the same trust boundary path a real hook POST to
// "/v1/hooks/{adapter}/{event}" already crosses (see
// internal/observability/routes.go's codexHookHandler/claudeHookHandler).
//
// Replay must never let a panic escape: implementations are expected to
// recover internally (or SchemaParserCheck's own Evaluate recovers around the
// call, see recoverableReplay) so a parser panic on one fixture case degrades
// only that one fixture's outcome, never the surrounding batch or the whole
// audit run, matching fault-injection-and-live-canary.yaml's
// parser_panic_timeout_or_unknown_field claim.
type HookFixtureReplayer func(ctx context.Context, stdinJSON []byte) (FixtureReplayResult, error)

// FixtureReplayResult is what one HookFixtureReplayer call reports about one
// FixtureCase: whether it decoded, its resulting canonical event_type and the
// STRUCTURAL field-path/primitive-type shape of the allowlisted output --
// never any field VALUE.
type FixtureReplayResult struct {
	Decoded            bool
	Unsupported        bool
	CanonicalEventType string
	// FieldPaths is the sorted list of {path, primitive_type} pairs observed
	// in the already-allowlisted hook output JSON shape, matching
	// drift-fingerprint-and-schema.yaml's event_schema_fingerprint
	// computation input exactly. Values are never included.
	FieldPaths []FieldPathType
}

// FieldPathType is one structural (field path, primitive type) pair, matching
// drift-fingerprint-and-schema.yaml's event_schema_fingerprint "primitive_type
// is one of string|integer|number|boolean|null|array|object exactly as
// observed in the STRUCTURE, never a sampled value".
type FieldPathType struct {
	Path string
	Type string
}

// AdapterFixtureSet is everything SchemaParserCheck needs to replay one
// adapter's bundled fixtures: the closure that performs the actual
// decode/build/validate call, and the ordered fixture cases to replay it
// against (owned by that adapter's own bundled fixture file, e.g.
// tests/fixtures/session-06/hook-otel-golden-map.json's hook_sample_inputs
// plus its hook_golden_map unsupported-event row, or a
// tests/fixtures/session-08 addition only if a genuinely new drift scenario
// is needed).
type AdapterFixtureSet struct {
	AdapterID      string
	AdapterVersion string
	FixtureVersion string
	Replay         HookFixtureReplayer
	Cases          []FixtureCase
}

// CompatibilityRegistry is the durable record of every event_schema_fingerprint
// already known-compatible for one (adapter_id, adapter_version). It is the
// compatibility registry the TDD describes: a computed fingerprint
// absent from this set for the adapter's current version is a new/
// unrecognized shape, counted but never silently guessed into a known type.
// SchemaParserCheck accepts it as an interface; production uses
// PostgresCompatibilityRegistry and unit tests may use the in-memory form.
type CompatibilityRegistry interface {
	// KnownFingerprints returns the set of event_schema_fingerprint values
	// already recorded as compatible for (adapterID, adapterVersion). A
	// registry with no entry yet for that pair returns an empty set (every
	// fingerprint counts as new), never an error -- "never audited before"
	// is not itself a failure.
	KnownFingerprints(ctx context.Context, adapterID, adapterVersion string) (map[string]bool, error)
}

// InMemoryCompatibilityRegistry is a minimal CompatibilityRegistry backed by
// a plain map, sufficient for a single-process caller or for tests that do
// not yet have a durable registry wired. It is safe for the sequential,
// single-writer-lock-guarded call pattern this package's Scheduler already
// enforces (see audit-run-and-schedule.yaml's single_writer_mechanism); it is
// not independently safe for concurrent callers outside that guarantee.
type InMemoryCompatibilityRegistry struct {
	known map[string]map[string]bool
}

// PostgresCompatibilityRegistry is the durable production registry for
// fixture-reviewed schema fingerprints.
type PostgresCompatibilityRegistry struct {
	Pool *pgxpool.Pool
}

func (r PostgresCompatibilityRegistry) KnownFingerprints(ctx context.Context, adapterID, adapterVersion string) (map[string]bool, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT event_schema_fingerprint
		FROM integrity_schema_compatibility
		WHERE adapter_id = $1 AND adapter_version = $2
	`, adapterID, adapterVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var fingerprint string
		if err := rows.Scan(&fingerprint); err != nil {
			return nil, err
		}
		out[fingerprint] = true
	}
	return out, rows.Err()
}

func (r PostgresCompatibilityRegistry) RecordFingerprint(ctx context.Context, adapterID, adapterVersion, fingerprint string) error {
	return errors.New("durable schema approval requires ApproveReviewedFingerprint with review reference")
}

// ApproveReviewedFingerprint is the only production write path. The
// non-empty review reference binds durable compatibility state to an
// explicit human/governance review rather than treating observation as
// approval.
func (r PostgresCompatibilityRegistry) ApproveReviewedFingerprint(ctx context.Context, adapterID, adapterVersion, fingerprint, reviewReference string) error {
	if adapterID == "" || adapterVersion == "" || len(fingerprint) != 64 {
		return errors.New("invalid schema compatibility identity")
	}
	if reviewReference == "" {
		return errors.New("schema compatibility review reference required")
	}
	if _, err := hex.DecodeString(fingerprint); err != nil {
		return errors.New("invalid schema fingerprint")
	}
	_, err := r.Pool.Exec(ctx, `
		INSERT INTO integrity_schema_compatibility
		    (adapter_id, adapter_version, event_schema_fingerprint, review_reference)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT DO NOTHING
	`, adapterID, adapterVersion, fingerprint, reviewReference)
	return err
}

// NewInMemoryCompatibilityRegistry returns an empty InMemoryCompatibilityRegistry.
func NewInMemoryCompatibilityRegistry() *InMemoryCompatibilityRegistry {
	return &InMemoryCompatibilityRegistry{known: map[string]map[string]bool{}}
}

func compatKey(adapterID, adapterVersion string) string {
	return adapterID + "@" + adapterVersion
}

func (r *InMemoryCompatibilityRegistry) KnownFingerprints(_ context.Context, adapterID, adapterVersion string) (map[string]bool, error) {
	set := r.known[compatKey(adapterID, adapterVersion)]
	out := make(map[string]bool, len(set))
	for fp := range set {
		out[fp] = true
	}
	return out, nil
}

func (r *InMemoryCompatibilityRegistry) RecordFingerprint(_ context.Context, adapterID, adapterVersion, fingerprint string) error {
	key := compatKey(adapterID, adapterVersion)
	if r.known[key] == nil {
		r.known[key] = map[string]bool{}
	}
	r.known[key][fingerprint] = true
	return nil
}

// SchemaParserCheck implements stage_4_parser_fixture_replay: for every
// registered AdapterFixtureSet, it replays each bundled FixtureCase through
// that adapter's OWN decode/build/validate path (never a re-derived parser),
// computes each successfully-decoded case's structural
// event_schema_fingerprint per drift-fingerprint-and-schema.yaml, compares it
// against the CompatibilityRegistry for the adapter's current version, and
// counts (never stores the values of) new/unrecognized shapes. A fixture case
// that panics or exceeds its own bounded call is recovered and recorded as a
// parser_incompatibility finding for that one case, never aborting the
// surrounding stage or the audit run.
type SchemaParserCheck struct {
	FixtureSets map[string]AdapterFixtureSet
	Compat      CompatibilityRegistry
	Now         func() time.Time
}

var _ Check = (*SchemaParserCheck)(nil)

// NewSchemaParserCheck constructs a SchemaParserCheck. compat may be nil, in
// which case an InMemoryCompatibilityRegistry is used so every fixture shape
// counts as new on the very first run (matching "gray/no evidence is the
// honest default before any check has run") rather than panicking on a nil
// interface value.
func NewSchemaParserCheck(fixtureSets map[string]AdapterFixtureSet, compat CompatibilityRegistry) *SchemaParserCheck {
	if compat == nil {
		compat = NewInMemoryCompatibilityRegistry()
	}
	return &SchemaParserCheck{FixtureSets: fixtureSets, Compat: compat, Now: time.Now}
}

func (c *SchemaParserCheck) StageID() StageID { return Stage4ParserFixtureReplay }
func (c *SchemaParserCheck) CheckID() string  { return SchemaParserCheckID }

func (c *SchemaParserCheck) validateProductionReady(sharedPool *pgxpool.Pool) error {
	if c == nil || c.Now == nil || len(c.FixtureSets) == 0 {
		return errors.New("schema parser fixtures and clock are required")
	}
	switch registry := c.Compat.(type) {
	case PostgresCompatibilityRegistry:
		if registry.Pool == nil || registry.Pool != sharedPool {
			return errors.New("schema compatibility registry must reuse assembly PostgreSQL pool")
		}
	case *PostgresCompatibilityRegistry:
		if registry == nil || registry.Pool == nil || registry.Pool != sharedPool {
			return errors.New("schema compatibility registry must reuse assembly PostgreSQL pool")
		}
	default:
		return errors.New("production schema compatibility registry must be durable PostgreSQL state")
	}
	return nil
}

// Targets enumerates one CheckTarget per registered adapter_id, keyed under
// CapabilityID="schema_compatibility" (the health dimension this stage's
// evidence is filed under) and InstallationID=adapter_id, since fixture
// replay is scoped per adapter recipe/version rather than per discovered
// installation (the same bundled fixtures apply to every installation of the
// same adapter_id/version).
func (c *SchemaParserCheck) Targets(_ context.Context, _ CheckInput) ([]CheckTarget, error) {
	ids := make([]string, 0, len(c.FixtureSets))
	for id := range c.FixtureSets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	targets := make([]CheckTarget, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, CheckTarget{CapabilityID: string(adaptersdk.CapabilityIngestionHistoricalImport), InstallationID: id, AdapterID: id})
	}
	return targets, nil
}

// Evaluate replays every FixtureCase for one adapter_id's AdapterFixtureSet.
func (c *SchemaParserCheck) Evaluate(ctx context.Context, in CheckInput, target CheckTarget) (CheckOutcome, error) {
	now := c.Now()
	if !in.Now.IsZero() {
		now = in.Now
	}
	adapterID := target.InstallationID
	set, ok := c.FixtureSets[adapterID]
	if !ok {
		return CheckOutcome{
			CheckID: SchemaParserCheckID, Status: CheckStatusSkippedUnsupported,
			Category: "", DetailRef: "no_fixture_set_registered_for_adapter:" + adapterID,
			ObservedAt: now,
		}, nil
	}
	known, err := c.Compat.KnownFingerprints(ctx, set.AdapterID, set.AdapterVersion)
	if err != nil {
		return CheckOutcome{}, fmt.Errorf("compat lookup for %s: %w", adapterID, err)
	}

	var (
		newShapeCount int
		panicCount    int
		unrecognized  []string
	)
	for _, fixtureCase := range set.Cases {
		result, replayErr := recoverableReplay(ctx, set.Replay, fixtureCase.StdinJSON)
		if replayErr != nil {
			// A recovered panic or a returned decode error on THIS ONE case
			// never aborts the batch: it is folded into the outcome as a
			// parser_incompatibility finding for that case, matching
			// fault-injection-and-live-canary.yaml's
			// parser_panic_timeout_or_unknown_field claim.
			if fixtureCase.ExpectUnsupported {
				// Expected: this fixture case is deliberately an
				// out-of-manifest event; a decode/build error here is the
				// pass condition, not a failure.
				continue
			}
			panicCount++
			unrecognized = append(unrecognized, fixtureCase.CaseName+":replay_failed")
			continue
		}
		if fixtureCase.ExpectUnsupported {
			if !result.Unsupported {
				// A case documented as out-of-manifest actually decoded: the
				// adapter's manifest silently grew acceptance beyond its
				// declared vocabulary, which is itself a drift signal worth
				// surfacing rather than a silent pass.
				panicCount++
				unrecognized = append(unrecognized, fixtureCase.CaseName+":expected_unsupported_but_decoded")
			}
			continue
		}
		fingerprint, fingerprintErr := computeEventSchemaFingerprint(result.CanonicalEventType, result.FieldPaths)
		if fingerprintErr != nil {
			panicCount++
			unrecognized = append(unrecognized, fixtureCase.CaseName+":invalid_structural_shape")
			continue
		}
		if !known[fingerprint] {
			newShapeCount++
		}
	}

	status := CheckStatusPass
	category := ""
	detail := fmt.Sprintf("fixture_version=%s new_shapes=%d cases=%d", set.FixtureVersion, newShapeCount, len(set.Cases))
	if panicCount > 0 {
		status = CheckStatusFail
		category = string(FailureClassParserIncompatibility)
		detail += fmt.Sprintf(" recovered_failures=%d detail=%v", panicCount, unrecognized)
	}
	// New shapes are counted, but per
	// new_shape_counting_rule ("the count itself ... is the only durable
	// artifact") a nonzero new-shape count alone is not itself a red-tier
	// parser failure here: stage_7_unknown_schema_and_lag is the stage that
	// escalates an unrecognized event_schema_fingerprint into a quarantine
	// finding. This stage's job is only to count/replay/verify parser
	// compatibility without crashing. Approval is a separate explicit
	// governance write through
	// PostgresCompatibilityRegistry.ApproveReviewedFingerprint;
	// Evaluate never auto-approves a newly observed shape.
	return CheckOutcome{
		CheckID: SchemaParserCheckID, Status: status, Category: category,
		DetailRef: detail, ObservedAt: now,
	}, nil
}

// recoverableReplay calls replay(stdinJSON) with a recover() guard so a panic
// inside an adapter's own decode/build/validate path can never escape and
// abort the surrounding stage/audit run; a recovered panic is folded into the
// same error return path as a normal decode error.
func recoverableReplay(ctx context.Context, replay HookFixtureReplayer, stdinJSON []byte) (FixtureReplayResult, error) {
	if replay == nil {
		return FixtureReplayResult{}, errors.New("nil_fixture_replayer")
	}
	type replayResponse struct {
		result FixtureReplayResult
		err    error
	}
	response := make(chan replayResponse, 1)
	input := bytes.Clone(stdinJSON)
	go func() {
		current := replayResponse{}
		defer func() {
			if recovered := recover(); recovered != nil {
				current.err = fmt.Errorf("recovered_panic_in_fixture_replay:%v", recovered)
			}
			response <- current
		}()
		current.result, current.err = replay(ctx, input)
	}()
	select {
	case <-ctx.Done():
		return FixtureReplayResult{}, ctx.Err()
	case current := <-response:
		return current.result, current.err
	}
}

func structuralFields(fieldPaths []FieldPathType) []StructuralField {
	fields := make([]StructuralField, 0, len(fieldPaths))
	for _, field := range fieldPaths {
		fields = append(fields, StructuralField{
			Path: field.Path, PrimitiveType: PrimitiveType(field.Type),
		})
	}
	return fields
}

// ComputeEventSchemaFingerprintForTest exposes the same strict production
// computation to external tests. Invalid structural metadata returns an
// empty string; production Evaluate retains the concrete validation error
// and folds it into parser_incompatibility evidence.
func ComputeEventSchemaFingerprintForTest(eventTypeName string, fieldPaths []FieldPathType) string {
	fingerprint, _ := computeEventSchemaFingerprint(eventTypeName, fieldPaths)
	return fingerprint
}

func computeEventSchemaFingerprint(eventTypeName string, fieldPaths []FieldPathType) (string, error) {
	return EventSchemaFingerprint(eventTypeName, structuralFields(fieldPaths))
}

// StructuralShapeOf walks an already-allowlisted JSON value (e.g. a
// HookHelperOutput already marshaled to JSON by the adapter's own
// BuildHookOutput+ValidateHookOutputAllowlist) and returns its sorted
// (field path, primitive type) shape. Repeated array elements contribute
// one structural path, so cardinality and element order cannot change the
// fingerprint. A heterogeneous array that assigns two primitive types to
// the same path is rejected visibly rather than coerced.
func StructuralShapeOf(raw []byte) ([]FieldPathType, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("multiple_or_invalid_trailing_json_values")
	}
	shape := map[string]string{}
	if err := walkStructuralShape("", generic, shape); err != nil {
		return nil, err
	}
	out := make([]FieldPathType, 0, len(shape))
	for path, primitiveType := range shape {
		out = append(out, FieldPathType{Path: path, Type: primitiveType})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Type < out[j].Type
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func walkStructuralShape(prefix string, value any, out map[string]string) error {
	add := func(path, primitiveType string) error {
		if path == "" {
			path = "$"
		}
		if previous, exists := out[path]; exists && previous != primitiveType {
			return fmt.Errorf("heterogeneous structural path %s: %s/%s", path, previous, primitiveType)
		}
		out[path] = primitiveType
		return nil
	}
	switch typed := value.(type) {
	case map[string]any:
		if prefix != "" {
			if err := add(prefix, string(PrimitiveObject)); err != nil {
				return err
			}
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			if err := walkStructuralShape(path, typed[key], out); err != nil {
				return err
			}
		}
	case []any:
		if err := add(prefix, string(PrimitiveArray)); err != nil {
			return err
		}
		for _, item := range typed {
			if err := walkStructuralShape(prefix+"[]", item, out); err != nil {
				return err
			}
		}
	case string:
		return add(prefix, string(PrimitiveString))
	case bool:
		return add(prefix, string(PrimitiveBoolean))
	case json.Number:
		if _, err := typed.Int64(); err == nil {
			return add(prefix, string(PrimitiveInteger))
		} else {
			if _, err := typed.Float64(); err != nil {
				return err
			}
			return add(prefix, string(PrimitiveNumber))
		}
	case nil:
		return add(prefix, string(PrimitiveNull))
	default:
		return fmt.Errorf("unsupported structural value %T", value)
	}
	return nil
}
