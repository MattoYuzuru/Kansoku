package integrity

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kansoku.local/kansoku/internal/adaptersdk"
)

type HealthState string

const (
	HealthGreen  HealthState = "green"
	HealthYellow HealthState = "yellow"
	HealthRed    HealthState = "red"
	HealthGray   HealthState = "gray"
)

type HealthDimension string

const (
	HealthConfiguration          HealthDimension = "configuration"
	HealthConnectivity           HealthDimension = "connectivity"
	HealthEventFreshness         HealthDimension = "event_freshness"
	HealthSchemaCompatibility    HealthDimension = "schema_compatibility"
	HealthParserFixtureStatus    HealthDimension = "parser_fixture_status"
	HealthReconciliationCoverage HealthDimension = "reconciliation_coverage"
	HealthPrivacyCanary          HealthDimension = "privacy_canary"
	HealthLiveCanaryAgeResult    HealthDimension = "live_canary_age_result"
	HealthStorageRollup          HealthDimension = "storage_rollup_health"
)

var healthDimensionStages = map[HealthDimension][]StageID{
	HealthConfiguration:          {Stage1DiscoveryAndConfiguration},
	HealthConnectivity:           {Stage2EndpointAndHookVerification},
	HealthEventFreshness:         {Stage3WatermarkVsInactivity},
	HealthSchemaCompatibility:    {Stage4ParserFixtureReplay, Stage7UnknownSchemaAndLag},
	HealthParserFixtureStatus:    {Stage4ParserFixtureReplay},
	HealthReconciliationCoverage: {Stage6CrossSourceReconciliation},
	HealthPrivacyCanary:          {Stage9RetentionDiskAndBackup},
	HealthLiveCanaryAgeResult:    {Stage10OptionalLiveCanary},
	HealthStorageRollup:          {Stage8RollupFormulaAndDBIntegrity, Stage9RetentionDiskAndBackup},
}

var orderedHealthDimensions = []HealthDimension{
	HealthConfiguration, HealthConnectivity, HealthEventFreshness,
	HealthSchemaCompatibility, HealthParserFixtureStatus,
	HealthReconciliationCoverage, HealthPrivacyCanary,
	HealthLiveCanaryAgeResult, HealthStorageRollup,
}

// redFailureClasses is the authoritative observed-breakage tier. Explained
// inactivity and eligibility uncertainty remain yellow/gray evidence;
// every other closed failure class represents observed breakage.
var redFailureClasses = map[string]bool{
	string(FailureClassEndpointUnreachable):            true,
	string(FailureClassHookRemovedDisabledOrUntrusted): true,
	string(FailureClassOTLPMisconfigured):              true,
	string(FailureClassPermissionDenied):               true,
	string(FailureClassWatermarkStall):                 true,
	string(FailureClassParserIncompatibility):          true,
	string(FailureClassUnknownSchema):                  true,
	string(FailureClassDuplicateEvidenceAnomaly):       true,
	string(FailureClassIngestLag):                      true,
	string(FailureClassRollupStale):                    true,
	string(FailureClassFormulaVersionMismatch):         true,
	string(FailureClassDBIntegrityViolation):           true,
	string(FailureClassRetentionJobFailed):             true,
	string(FailureClassDiskBudgetExceeded):             true,
	string(FailureClassBackupStale):                    true,
	string(FailureClassRestoreTestFailed):              true,
	string(FailureClassPrivacyCanaryViolation):         true,
	string(FailureClassSyntheticPipelineProbeFailed):   true,
	string(FailureClassLiveCanaryPartialDAG):           true,
	string(FailureClassLiveCanaryProviderTimeout):      true,
	string(FailureClassReconciliationMismatch):         true,
	string(FailureReasonStageTimeout):                  true,
}

var failureClassDimensions = map[FailureClass][]HealthDimension{
	FailureClassEndpointUnreachable:            {HealthConnectivity},
	FailureClassHookRemovedDisabledOrUntrusted: {HealthConnectivity},
	FailureClassOTLPMisconfigured:              {HealthConnectivity},
	FailureClassPermissionDenied:               {HealthConfiguration},
	FailureClassWatermarkStall:                 {HealthEventFreshness},
	FailureClassTrueInactivityFlagged:          {HealthEventFreshness},
	FailureClassEligibilityUnknown:             {HealthEventFreshness},
	FailureClassParserIncompatibility:          {HealthSchemaCompatibility, HealthParserFixtureStatus},
	FailureClassUnknownSchema:                  {HealthSchemaCompatibility},
	FailureClassDuplicateEvidenceAnomaly:       {HealthEventFreshness},
	FailureClassIngestLag:                      {HealthEventFreshness},
	FailureClassRollupStale:                    {HealthStorageRollup},
	FailureClassFormulaVersionMismatch:         {HealthStorageRollup},
	FailureClassDBIntegrityViolation:           {HealthStorageRollup},
	FailureClassRetentionJobFailed:             {HealthStorageRollup},
	FailureClassDiskBudgetExceeded:             {HealthStorageRollup},
	FailureClassBackupStale:                    {HealthStorageRollup},
	FailureClassRestoreTestFailed:              {HealthStorageRollup},
	FailureClassPrivacyCanaryViolation:         {HealthPrivacyCanary},
	FailureClassSyntheticPipelineProbeFailed:   {HealthConnectivity},
	FailureClassLiveCanaryPartialDAG:           {HealthLiveCanaryAgeResult},
	FailureClassLiveCanaryProviderTimeout:      {HealthLiveCanaryAgeResult},
	FailureClassReconciliationMismatch:         {HealthReconciliationCoverage},
}

type DimensionHealth struct {
	Dimension       HealthDimension            `json:"dimension"`
	State           HealthState                `json:"state"`
	CapabilityID    adaptersdk.CapabilityID    `json:"capability_id"`
	CapabilityState adaptersdk.CapabilityState `json:"capability_state"`
	EvidenceAge     time.Duration              `json:"evidence_age"`
	EvidenceRefs    []string                   `json:"evidence_refs"`
}

type HealthSnapshot struct {
	InstallationID  string                  `json:"installation_id"`
	CapabilityID    adaptersdk.CapabilityID `json:"capability_id"`
	Dimensions      []DimensionHealth       `json:"dimensions"`
	WorstApplicable HealthState             `json:"worst_applicable_state"`
	GeneratedAt     time.Time               `json:"generated_at"`
}

// DeriveHealth builds the decomposed API response from actual durable check
// evidence. No numeric score is computed or persisted.
func DeriveHealth(installationID string, capabilityID adaptersdk.CapabilityID, capabilityState adaptersdk.CapabilityState, checks []AuditCheck, now time.Time, freshness time.Duration) HealthSnapshot {
	if freshness <= 0 {
		freshness = DefaultFreshnessWindow
	}
	latest := map[StageID][]AuditCheck{}
	byIdentity := map[string]AuditCheck{}
	for _, check := range checks {
		if check.InstallationID != installationID || check.CapabilityID != string(capabilityID) {
			continue
		}
		identity := string(check.StageID) + "\x00" + check.CheckID + "\x00" + check.SourceID
		previous, exists := byIdentity[identity]
		if !exists || checkTime(check).After(checkTime(previous)) {
			byIdentity[identity] = check
		}
	}
	for _, check := range byIdentity {
		latest[check.StageID] = append(latest[check.StageID], check)
	}
	dimensions := make([]DimensionHealth, 0, len(orderedHealthDimensions))
	for _, dimension := range orderedHealthDimensions {
		dimensions = append(dimensions, deriveDimension(dimension, capabilityID, capabilityState, latest, now, freshness))
	}
	return HealthSnapshot{
		InstallationID: installationID, CapabilityID: capabilityID,
		Dimensions: dimensions, WorstApplicable: worstHealth(dimensions),
		GeneratedAt: now.UTC(),
	}
}

// DeriveHealthWithIncidents overlays still-open incident evidence so a later
// passing check from a different source cannot hide an unresolved failure.
func DeriveHealthWithIncidents(installationID string, capabilityID adaptersdk.CapabilityID, capabilityState adaptersdk.CapabilityState, checks []AuditCheck, incidents []IntegrityIncidentDetail, now time.Time, freshness time.Duration) HealthSnapshot {
	snapshot := DeriveHealth(installationID, capabilityID, capabilityState, checks, now, freshness)
	for _, incident := range incidents {
		if incident.InstallationID != installationID || incident.CapabilityID != string(capabilityID) || incident.ResolvedAt != nil {
			continue
		}
		state := HealthYellow
		if redFailureClasses[string(incident.FailureClass)] {
			state = HealthRed
		}
		for _, dimension := range failureClassDimensions[incident.FailureClass] {
			for i := range snapshot.Dimensions {
				if snapshot.Dimensions[i].Dimension == dimension {
					snapshot.Dimensions[i].State = state
					snapshot.Dimensions[i].EvidenceRefs = append(snapshot.Dimensions[i].EvidenceRefs, incident.CheckEvidenceRef)
				}
			}
		}
	}
	snapshot.WorstApplicable = worstHealth(snapshot.Dimensions)
	return snapshot
}

func deriveDimension(dimension HealthDimension, capabilityID adaptersdk.CapabilityID, capabilityState adaptersdk.CapabilityState, latest map[StageID][]AuditCheck, now time.Time, freshness time.Duration) DimensionHealth {
	result := DimensionHealth{Dimension: dimension, State: HealthGray, CapabilityID: capabilityID, CapabilityState: capabilityState}
	if capabilityState == adaptersdk.StateUnsupported {
		return result
	}
	stages := healthDimensionStages[dimension]
	seen := 0
	passed := 0
	maxAge := time.Duration(0)
	for _, stage := range stages {
		stageChecks := latest[stage]
		if len(stageChecks) == 0 {
			continue
		}
		stagePassed := true
		stageObserved := false
		for _, check := range stageChecks {
			if check.Status == CheckStatusSkippedUnsupported || check.Status == CheckStatusPending {
				continue
			}
			stageObserved = true
			seen++
			if check.ObservedAt == nil {
				stagePassed = false
				continue
			}
			age := now.Sub(check.ObservedAt.UTC())
			if age > maxAge {
				maxAge = age
			}
			result.EvidenceRefs = append(result.EvidenceRefs, check.AuditRunID+":"+check.CheckID+":"+check.SourceID)
			if check.Status == CheckStatusFail {
				if redFailureClasses[check.Category] {
					result.State = HealthRed
				} else {
					result.State = HealthYellow
				}
				result.EvidenceAge = maxAge
				sort.Strings(result.EvidenceRefs)
				return result
			}
			if check.Status != CheckStatusPass || age > freshness {
				stagePassed = false
			}
		}
		if stageObserved && stagePassed {
			passed++
		}
	}
	result.EvidenceAge = maxAge
	sort.Strings(result.EvidenceRefs)
	if seen == 0 {
		return result
	}
	if passed == len(stages) {
		result.State = HealthGreen
		return result
	}
	result.State = HealthYellow
	return result
}

func checkTime(check AuditCheck) time.Time {
	if check.ObservedAt != nil {
		return check.ObservedAt.UTC()
	}
	if check.FinishedAt != nil {
		return check.FinishedAt.UTC()
	}
	return time.Time{}
}

func worstHealth(dimensions []DimensionHealth) HealthState {
	rank := map[HealthState]int{HealthGreen: 0, HealthGray: 1, HealthYellow: 2, HealthRed: 3}
	worst := HealthGreen
	for _, dimension := range dimensions {
		if rank[dimension.State] > rank[worst] {
			worst = dimension.State
		}
	}
	return worst
}

// LoadHealthSnapshot is the durable Health API: it reads latest check rows
// from PostgreSQL and then applies the same pure derivation used by tests.
func LoadHealthSnapshot(ctx context.Context, pool *pgxpool.Pool, installationID string, capabilityID adaptersdk.CapabilityID, capabilityState adaptersdk.CapabilityState, now time.Time) (HealthSnapshot, error) {
	if pool == nil {
		return HealthSnapshot{}, errors.New("health API requires PostgreSQL pool")
	}
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT ON (stage_id, check_id, source_id)
		       audit_run_id, check_id, capability_id, installation_id, source_id, stage_id, status,
		       COALESCE(category, ''), COALESCE(detail_ref, ''), observed_at, started_at, finished_at
		FROM integrity_audit_checks
		WHERE installation_id = $1 AND capability_id = $2
		ORDER BY stage_id, check_id, source_id, COALESCE(observed_at, finished_at, started_at) DESC NULLS LAST
	`, installationID, string(capabilityID))
	if err != nil {
		return HealthSnapshot{}, err
	}
	defer rows.Close()
	var checks []AuditCheck
	for rows.Next() {
		check, err := scanCheck(rows)
		if err != nil {
			return HealthSnapshot{}, err
		}
		checks = append(checks, check)
	}
	if err := rows.Err(); err != nil {
		return HealthSnapshot{}, err
	}
	incidents, err := ListOpenIncidents(ctx, pool)
	if err != nil {
		return HealthSnapshot{}, err
	}
	return DeriveHealthWithIncidents(installationID, capabilityID, capabilityState, checks, incidents, now, DefaultFreshnessWindow), nil
}
