package integrity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kansoku.local/kansoku/internal/privacy"
)

type FingerprintKind string

const (
	FingerprintExecutableVersion FingerprintKind = "executable_version"
	FingerprintConfigRecipe      FingerprintKind = "config_recipe_fingerprint"
	FingerprintAdapterVersion    FingerprintKind = "adapter_version"
	FingerprintFixtureVersion    FingerprintKind = "fixture_version"
	FingerprintFormulaRegistry   FingerprintKind = "formula_registry_version"
	FingerprintEventSchema       FingerprintKind = "event_schema_fingerprint"
)

type PrimitiveType string

const (
	PrimitiveString  PrimitiveType = "string"
	PrimitiveInteger PrimitiveType = "integer"
	PrimitiveNumber  PrimitiveType = "number"
	PrimitiveBoolean PrimitiveType = "boolean"
	PrimitiveNull    PrimitiveType = "null"
	PrimitiveArray   PrimitiveType = "array"
	PrimitiveObject  PrimitiveType = "object"
)

type StructuralField struct {
	Path          string        `json:"path"`
	PrimitiveType PrimitiveType `json:"primitive_type"`
}

type structuralSchema struct {
	EventTypeName string            `json:"event_type_name"`
	FieldPaths    []StructuralField `json:"field_paths"`
}

// EventSchemaFingerprint hashes canonical structural metadata. It cannot
// accept values: the input type contains only field paths and primitive
// types, and privacy's shared prohibited-field categorizer rejects unsafe
// path segments before canonicalization.
func EventSchemaFingerprint(eventType string, fields []StructuralField) (string, error) {
	if strings.TrimSpace(eventType) == "" {
		return "", errors.New("event_type_name_required")
	}
	canonical := append([]StructuralField(nil), fields...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Path < canonical[j].Path })
	seen := map[string]bool{}
	for _, field := range canonical {
		if field.Path == "" {
			return "", errors.New("field_path_required")
		}
		if privacy.IsProhibitedDurableField(field.Path) {
			return "", fmt.Errorf("prohibited durable field path: %s", field.Path)
		}
		if seen[field.Path] {
			return "", fmt.Errorf("duplicate field path: %s", field.Path)
		}
		seen[field.Path] = true
		if !validPrimitiveType(field.PrimitiveType) {
			return "", fmt.Errorf("unsupported primitive type %q", field.PrimitiveType)
		}
	}
	raw, err := json.Marshal(structuralSchema{EventTypeName: eventType, FieldPaths: canonical})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func validPrimitiveType(value PrimitiveType) bool {
	switch value {
	case PrimitiveString, PrimitiveInteger, PrimitiveNumber, PrimitiveBoolean, PrimitiveNull, PrimitiveArray, PrimitiveObject:
		return true
	default:
		return false
	}
}

// DriftFingerprint is the durable, metadata-only identity for one observed
// version/shape. ValueRef is already a version, PlanSHA256 or content hash,
// never raw config or event data.
type DriftFingerprint struct {
	Kind         FingerprintKind
	SubjectID    string
	SourceID     string
	CapabilityID string
	ValueRef     string
	ObservedAt   time.Time
}

func (f DriftFingerprint) Validate() error {
	if !validFingerprintKind(f.Kind) || f.SubjectID == "" || f.ValueRef == "" || f.ObservedAt.IsZero() {
		return errors.New("invalid drift fingerprint")
	}
	if privacy.IsProhibitedDurableField(f.SubjectID) || privacy.IsProhibitedDurableField(f.SourceID) {
		return errors.New("fingerprint identity contains prohibited durable field alias")
	}
	switch f.Kind {
	case FingerprintConfigRecipe, FingerprintFixtureVersion, FingerprintEventSchema:
		value := strings.TrimPrefix(f.ValueRef, "sha256:")
		if len(value) != 64 {
			return errors.New("content fingerprint must be a sha256 reference")
		}
		if _, err := hex.DecodeString(value); err != nil {
			return errors.New("content fingerprint must be hexadecimal")
		}
	case FingerprintFormulaRegistry:
		for _, current := range f.ValueRef {
			if current < '0' || current > '9' {
				return errors.New("formula registry version must be a positive integer")
			}
		}
		if f.ValueRef == "" || f.ValueRef == "0" {
			return errors.New("formula registry version must be positive")
		}
	}
	return nil
}

func validFingerprintKind(kind FingerprintKind) bool {
	switch kind {
	case FingerprintExecutableVersion, FingerprintConfigRecipe, FingerprintAdapterVersion,
		FingerprintFixtureVersion, FingerprintFormulaRegistry, FingerprintEventSchema:
		return true
	default:
		return false
	}
}

// TargetedStagesForFingerprint returns only the stages named by the
// fingerprint contract. The result is ordinal-sorted and never contains the
// optional live canary or retention sweep.
func TargetedStagesForFingerprint(kind FingerprintKind) []StageID {
	var stages []StageID
	switch kind {
	case FingerprintExecutableVersion:
		stages = []StageID{Stage1DiscoveryAndConfiguration, Stage2EndpointAndHookVerification, Stage3WatermarkVsInactivity, Stage4ParserFixtureReplay, Stage7UnknownSchemaAndLag}
	case FingerprintConfigRecipe:
		stages = []StageID{Stage1DiscoveryAndConfiguration, Stage2EndpointAndHookVerification}
	case FingerprintAdapterVersion:
		stages = []StageID{Stage1DiscoveryAndConfiguration, Stage2EndpointAndHookVerification, Stage3WatermarkVsInactivity, Stage4ParserFixtureReplay, Stage7UnknownSchemaAndLag}
	case FingerprintFixtureVersion:
		stages = []StageID{Stage4ParserFixtureReplay}
	case FingerprintFormulaRegistry:
		stages = []StageID{Stage8RollupFormulaAndDBIntegrity}
	case FingerprintEventSchema:
		stages = []StageID{Stage4ParserFixtureReplay, Stage7UnknownSchemaAndLag}
	}
	return stages
}

type FingerprintChange struct {
	Previous *DriftFingerprint
	Current  *DriftFingerprint
}

// ChangedFingerprints returns additions, modifications and removals. First
// observation establishes a baseline and is not reported as drift.
func ChangedFingerprints(previous, current []DriftFingerprint) []FingerprintChange {
	if len(previous) == 0 {
		return nil
	}
	prior := map[string]DriftFingerprint{}
	currentByID := map[string]DriftFingerprint{}
	for _, row := range previous {
		prior[fingerprintIdentity(row)] = row
	}
	var changed []FingerprintChange
	for _, row := range current {
		identity := fingerprintIdentity(row)
		currentByID[identity] = row
		if value, ok := prior[identity]; !ok || value.ValueRef != row.ValueRef {
			currentCopy := row
			var previousCopy *DriftFingerprint
			if ok {
				priorCopy := value
				previousCopy = &priorCopy
			}
			changed = append(changed, FingerprintChange{Previous: previousCopy, Current: &currentCopy})
		}
	}
	for identity, row := range prior {
		if _, exists := currentByID[identity]; !exists {
			previousCopy := row
			changed = append(changed, FingerprintChange{Previous: &previousCopy})
		}
	}
	sort.Slice(changed, func(i, j int) bool {
		return fingerprintChangeIdentity(changed[i]) < fingerprintChangeIdentity(changed[j])
	})
	return changed
}

func fingerprintChangeIdentity(change FingerprintChange) string {
	if change.Current != nil {
		return fingerprintIdentity(*change.Current)
	}
	return fingerprintIdentity(*change.Previous)
}

// TargetedStagesForChanges unions and ordinal-sorts only the stages named by
// changed fingerprint kinds.
func TargetedStagesForChanges(changes []FingerprintChange) []StageID {
	selected := map[StageID]bool{}
	for _, change := range changes {
		row := change.Current
		if row == nil {
			row = change.Previous
		}
		for _, stage := range TargetedStagesForFingerprint(row.Kind) {
			selected[stage] = true
		}
	}
	var stages []StageID
	for _, descriptor := range StageRegistry {
		if selected[descriptor.StageID] {
			stages = append(stages, descriptor.StageID)
		}
	}
	return stages
}

// StoreFingerprints replaces the current metadata-only snapshot atomically.
func StoreFingerprints(ctx context.Context, pool *pgxpool.Pool, rows []DriftFingerprint) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM integrity_fingerprints`); err != nil {
		return err
	}
	for _, row := range rows {
		if err := row.Validate(); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO integrity_fingerprints
			    (fingerprint_kind, subject_id, source_id, capability_id, value_ref, observed_at)
			VALUES ($1,$2,$3,$4,$5,$6)
		`, string(row.Kind), row.SubjectID, row.SourceID, row.CapabilityID, row.ValueRef, row.ObservedAt.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func LoadFingerprints(ctx context.Context, pool *pgxpool.Pool) ([]DriftFingerprint, error) {
	rows, err := pool.Query(ctx, `
		SELECT fingerprint_kind, subject_id, source_id, capability_id, value_ref, observed_at
		FROM integrity_fingerprints
		ORDER BY fingerprint_kind, subject_id, source_id, capability_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DriftFingerprint
	for rows.Next() {
		var row DriftFingerprint
		if err := rows.Scan(&row.Kind, &row.SubjectID, &row.SourceID, &row.CapabilityID, &row.ValueRef, &row.ObservedAt); err != nil {
			return nil, err
		}
		if err := row.Validate(); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func fingerprintIdentity(row DriftFingerprint) string {
	return string(row.Kind) + "\x00" + row.SubjectID + "\x00" + row.SourceID + "\x00" + row.CapabilityID
}
