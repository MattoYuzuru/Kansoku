package integrity

import (
	"context"
	"fmt"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/adaptersdk/fakeadapter"
	"kansoku.local/kansoku/internal/observability"
)

type FaultDefinition struct {
	FaultID      string
	Stages       []StageID
	FailureClass FailureClass
	DetectionSLO time.Duration
	Evidence     FaultEvidenceLevel
}

type FaultEvidenceLevel string

const (
	FaultEvidenceComponentClassifier   FaultEvidenceLevel = "component_classifier"
	FaultEvidenceDeterministicMutation FaultEvidenceLevel = "deterministic_mutation_integration"
	FaultEvidenceRuntimeRequired       FaultEvidenceLevel = "runtime_required"
)

// FaultCatalog is the exact executable mirror of
// fault-injection-and-live-canary.yaml. The Session-08 validator enforces
// the exact 17 classifier / 2 mutation-integration / 2 runtime-required
// evidence partition.
var FaultCatalog = []FaultDefinition{
	{"hook_removed_disabled_or_untrusted", []StageID{Stage2EndpointAndHookVerification}, FailureClassHookRemovedDisabledOrUntrusted, 30 * time.Second, FaultEvidenceComponentClassifier},
	{"otlp_wrong_port_protocol_or_auth", []StageID{Stage2EndpointAndHookVerification}, FailureClassOTLPMisconfigured, 30 * time.Second, FaultEvidenceComponentClassifier},
	{"transcript_truncate_rotate_schema_or_permission_change", []StageID{Stage1DiscoveryAndConfiguration, Stage4ParserFixtureReplay}, FailureClassPermissionDenied, 60 * time.Second, FaultEvidenceComponentClassifier},
	{"active_process_with_absent_events", []StageID{Stage3WatermarkVsInactivity}, FailureClassWatermarkStall, 90 * time.Second, FaultEvidenceComponentClassifier},
	{"duplicate_and_stalled_watermarks", []StageID{Stage3WatermarkVsInactivity, Stage7UnknownSchemaAndLag}, FailureClassDuplicateEvidenceAnomaly, 60 * time.Second, FaultEvidenceComponentClassifier},
	{"parser_panic_timeout_or_unknown_field", []StageID{Stage4ParserFixtureReplay}, FailureClassParserIncompatibility, 60 * time.Second, FaultEvidenceComponentClassifier},
	{"delayed_rollup", []StageID{Stage8RollupFormulaAndDBIntegrity}, FailureClassRollupStale, 60 * time.Second, FaultEvidenceComponentClassifier},
	{"full_disk", []StageID{Stage9RetentionDiskAndBackup}, FailureClassDiskBudgetExceeded, 90 * time.Second, FaultEvidenceComponentClassifier},
	{"db_restart", []StageID{Stage8RollupFormulaAndDBIntegrity}, FailureClassDBIntegrityViolation, 90 * time.Second, FaultEvidenceRuntimeRequired},
	{"corrupt_spool", []StageID{Stage9RetentionDiskAndBackup}, FailureClassDBIntegrityViolation, 90 * time.Second, FaultEvidenceDeterministicMutation},
	{"stale_backup", []StageID{Stage9RetentionDiskAndBackup}, FailureClassBackupStale, 90 * time.Second, FaultEvidenceComponentClassifier},
	{"failed_restore", []StageID{Stage9RetentionDiskAndBackup}, FailureClassRestoreTestFailed, 90 * time.Second, FaultEvidenceRuntimeRequired},
	{"privacy_canary_violation", []StageID{Stage9RetentionDiskAndBackup}, FailureClassPrivacyCanaryViolation, 45 * time.Second, FaultEvidenceComponentClassifier},
	{"live_canary_partial_dag", []StageID{Stage10OptionalLiveCanary}, FailureClassLiveCanaryPartialDAG, 300 * time.Second, FaultEvidenceComponentClassifier},
	{"live_canary_provider_timeout", []StageID{Stage10OptionalLiveCanary}, FailureClassLiveCanaryProviderTimeout, 300 * time.Second, FaultEvidenceComponentClassifier},
	{"endpoint_unreachable", []StageID{Stage2EndpointAndHookVerification}, FailureClassEndpointUnreachable, 30 * time.Second, FaultEvidenceComponentClassifier},
	{"synthetic_pipeline_probe_failure", []StageID{Stage5SyntheticPipelineProbe}, FailureClassSyntheticPipelineProbeFailed, 45 * time.Second, FaultEvidenceDeterministicMutation},
	{"unknown_schema_quarantine", []StageID{Stage7UnknownSchemaAndLag}, FailureClassUnknownSchema, 30 * time.Second, FaultEvidenceComponentClassifier},
	{"cross_source_reconciliation_regression", []StageID{Stage6CrossSourceReconciliation}, FailureClassReconciliationMismatch, 60 * time.Second, FaultEvidenceComponentClassifier},
	{"ingest_lag", []StageID{Stage7UnknownSchemaAndLag}, FailureClassIngestLag, 60 * time.Second, FaultEvidenceComponentClassifier},
	{"inventory_cache_miscount", []StageID{Stage1DiscoveryAndConfiguration}, FailureClassPermissionDenied, 30 * time.Second, FaultEvidenceComponentClassifier},
}

type FaultDetection struct {
	Definition       FaultDefinition
	Outcome          CheckOutcome
	InjectedAt       time.Time
	DetectedAt       time.Time
	AffectedInterval AffectedInterval
	AuditRunID       string
}

func FaultDefinitionByID(id string) (FaultDefinition, bool) {
	for _, definition := range FaultCatalog {
		if definition.FaultID == id {
			return definition, true
		}
	}
	return FaultDefinition{}, false
}

// InjectFaultForTest injects a closed structured failure, never arbitrary
// content. Every catalog row runs the production check or the exact
// production classifier used by that check; no row may return a prebuilt
// expected outcome.
func InjectFaultForTest(ctx context.Context, id string, injectedAt time.Time) (FaultDetection, error) {
	definition, ok := FaultDefinitionByID(id)
	if !ok {
		return FaultDetection{}, fmt.Errorf("unknown fault id %q", id)
	}
	if definition.Evidence != FaultEvidenceComponentClassifier {
		return FaultDetection{}, fmt.Errorf("fault %s requires %s evidence", id, definition.Evidence)
	}
	outcome, err := evaluateInjectedFault(ctx, definition, injectedAt)
	if err != nil {
		return FaultDetection{}, err
	}
	// Component-classifier coverage records the actual time the classifier
	// returned, but does not claim an end-to-end incident SLO. Measured SLO
	// evidence is produced only by mutation integration tests that persist a
	// Stage-11 incident.
	detectedAt := time.Now().UTC()
	if detectedAt.Before(injectedAt) {
		detectedAt = injectedAt
	}
	return FaultDetection{
		Definition: definition, Outcome: outcome, InjectedAt: injectedAt,
		DetectedAt: detectedAt, AffectedInterval: AffectedInterval{From: injectedAt, To: detectedAt},
		AuditRunID: "fault-run-" + id,
	}, nil
}

func evaluateInjectedFault(ctx context.Context, definition FaultDefinition, now time.Time) (CheckOutcome, error) {
	switch definition.FaultID {
	case "hook_removed_disabled_or_untrusted", "otlp_wrong_port_protocol_or_auth", "endpoint_unreachable":
		target := EndpointTarget{
			InstallationID: "fixture-installation", CapabilityID: string(adaptersdk.CapabilityIngestionLiveStream),
			SourceID: "fixture-source", Kind: EndpointKindHook, Configured: true, Enabled: true, Trusted: true,
		}
		if definition.FaultID == "hook_removed_disabled_or_untrusted" {
			target.Enabled = false
		}
		if definition.FaultID == "otlp_wrong_port_protocol_or_auth" {
			target.Kind = EndpointKindOTLP
		}
		check := NewEndpointAndHookCheck(
			func(context.Context) ([]EndpointTarget, error) { return []EndpointTarget{target}, nil },
			func(context.Context, EndpointTarget) (PassiveEndpointEvidence, error) {
				if definition.FaultID == "endpoint_unreachable" {
					return PassiveEndpointEvidence{Reachable: false}, nil
				}
				return PassiveEndpointEvidence{Reachable: true, ProtocolMatches: false, PortMatches: false, AuthMatches: false, ObservedAt: now}, nil
			},
		)
		targets, err := check.Targets(ctx, CheckInput{Now: now})
		if err != nil {
			return CheckOutcome{}, err
		}
		return check.Evaluate(ctx, CheckInput{Now: now}, targets[0])
	case "duplicate_and_stalled_watermarks", "unknown_schema_quarantine", "ingest_lag":
		row := SourceIntegritySnapshot{
			InstallationID: "fixture-installation", CapabilityID: string(adaptersdk.CapabilityIngestionLiveStream),
			SourceID: "fixture-source", SchemaFingerprint: "sha256:fixture", SchemaKnown: true,
			LatestReceivedAt: now, IngestLagBudget: time.Minute,
		}
		if definition.FaultID == "duplicate_and_stalled_watermarks" {
			row.DuplicateReplayCount, row.InflatedFactCount = 1, 1
		}
		if definition.FaultID == "unknown_schema_quarantine" {
			row.SchemaKnown, row.Quarantined = false, true
		}
		if definition.FaultID == "ingest_lag" {
			row.LateEventsPending = 1
		}
		check := NewUnknownSchemaAndLagCheck(func(context.Context) ([]SourceIntegritySnapshot, error) {
			return []SourceIntegritySnapshot{row}, nil
		})
		targets, err := check.Targets(ctx, CheckInput{Now: now})
		if err != nil {
			return CheckOutcome{}, err
		}
		return check.Evaluate(ctx, CheckInput{Now: now}, targets[0])
	case "transcript_truncate_rotate_schema_or_permission_change":
		finding := (&DiscoveryConfigCheck{}).checkStateRootReadable(adaptersdk.Installation{
			InstallationID: "fixture-installation",
			StateRoot:      "/fixture/unreadable-transcript",
		})
		return discoveryFaultOutcome(finding, now), nil
	case "inventory_cache_miscount":
		finding := classifyInventorySnapshot(adaptersdk.InventorySnapshot{
			SnapshotID: "fixture-inventory",
			Nodes: []adaptersdk.Node{{
				NodeID: "cached-plugin", Kind: adaptersdk.NodeCacheArtifact, CachedOnly: true,
			}, {
				NodeID: "surface", Kind: adaptersdk.NodeAgentSurface,
			}},
			Edges: []adaptersdk.Edge{{
				EdgeID: "bad-enabled-edge", Kind: adaptersdk.EdgeEnabledFor,
				FromNode: "cached-plugin", ToNode: "surface",
			}},
		})
		return discoveryFaultOutcome(finding, now), nil
	case "active_process_with_absent_events":
		return classifyWatermark(observability.Watermark{
			SourceID: "fixture-source", Inactivity: false,
			LastDiscovered:       now.Add(-2 * time.Minute),
			LastEligibleActivity: now.Add(-2 * time.Minute),
			ExpectedCadenceMS:    int64((30 * time.Second) / time.Millisecond),
		}, now), nil
	case "parser_panic_timeout_or_unknown_field":
		check := NewSchemaParserCheck(map[string]AdapterFixtureSet{
			"fixture-parser": {
				AdapterID: "fixture-parser", AdapterVersion: "1.0.0", FixtureVersion: "1.0.0",
				Replay: func(context.Context, []byte) (FixtureReplayResult, error) {
					panic("fault-injected-parser-panic")
				},
				Cases: []FixtureCase{{CaseName: "panic", StdinJSON: []byte(`{}`)}},
			},
		}, NewInMemoryCompatibilityRegistry())
		return check.Evaluate(ctx, CheckInput{Now: now}, CheckTarget{
			CapabilityID:   string(adaptersdk.CapabilityIngestionHistoricalImport),
			InstallationID: "fixture-parser",
		})
	case "delayed_rollup":
		return classifyRepairQueueDepth(RollupFreshnessBudget+1, RollupFreshnessBudget, now), nil
	case "db_restart", "failed_restore":
		return CheckOutcome{}, fmt.Errorf("fault %s requires a real runtime transition", definition.FaultID)
	case "corrupt_spool", "synthetic_pipeline_probe_failure":
		return CheckOutcome{}, fmt.Errorf("fault %s requires deterministic mutation integration", definition.FaultID)
	case "full_disk", "stale_backup", "privacy_canary_violation":
		check := NewRetentionDiskBackupCheck(
			func(context.Context, time.Time, int) (RetentionDryRunResult, error) {
				return RetentionDryRunResult{EligibleForDrop: map[string][]string{}}, nil
			},
			func(context.Context, float64) (DiskForecast, error) {
				return DiskForecast{UsedFraction: 0.5}, nil
			},
			func(context.Context) (BackupStatus, error) {
				return BackupStatus{
					LastBackupAt: now.Add(-time.Hour), LastBackupChecksumOK: true,
					LastRestoreTestAt: now.Add(-time.Hour), LastRestoreTestPassed: true,
					LastRestoreTestRan: true,
				}, nil
			},
			func(context.Context) (bool, string, error) { return false, "", nil },
		)
		check.SpoolIntegrity = func(context.Context) error { return nil }
		sourceID := ""
		switch definition.FaultID {
		case "full_disk":
			sourceID = "disk-forecast"
			check.DiskForecast = func(context.Context, float64) (DiskForecast, error) {
				return DiskForecast{UsedFraction: 1}, nil
			}
		case "stale_backup":
			sourceID = "backup-status"
			check.BackupStatus = func(context.Context) (BackupStatus, error) {
				return BackupStatus{LastBackupAt: now.Add(-DefaultBackupAgeBudget - time.Hour), LastBackupChecksumOK: true}, nil
			}
		case "privacy_canary_violation":
			sourceID = "privacy-canary"
			check.PrivacyCanary = func(context.Context) (bool, string, error) {
				return true, "prohibited canary reached durable sink", nil
			}
		}
		return check.Evaluate(ctx, CheckInput{Now: now}, CheckTarget{SourceID: sourceID})
	case "live_canary_partial_dag", "live_canary_provider_timeout":
		recipe := fixtureLiveCanaryRecipe()
		check := NewLiveCanaryCheck(
			[]LiveCanaryRecipe{recipe},
			map[string]LiveCanaryGate{recipe.RecipeID: {
				ExplicitCredentialsPresent: true, ExplicitUserConsentRecorded: true, ConsentRecordedAt: now.Add(-time.Hour),
			}},
			func(context.Context, LiveCanaryRecipe) (LiveCanaryObservation, error) {
				if definition.FaultID == "live_canary_provider_timeout" {
					return LiveCanaryObservation{ProviderTimedOut: true}, nil
				}
				return LiveCanaryObservation{EventDAG: DefaultExpectedCanaryDAG[:len(DefaultExpectedCanaryDAG)-1], ObservedAt: now}, nil
			},
			func(context.Context, LiveCanaryRecipe) error { return nil },
		)
		targets, err := check.Targets(ctx, CheckInput{Now: now})
		if err != nil {
			return CheckOutcome{}, err
		}
		return check.Evaluate(ctx, CheckInput{Now: now}, targets[0])
	case "cross_source_reconciliation_regression":
		registry := adaptersdk.NewRegistry()
		if err := registry.Register(fakeadapter.New()); err != nil {
			return CheckOutcome{}, err
		}
		check := NewCrossSourceReconciliationCheck(
			registry,
			func(context.Context, string) ([]adaptersdk.Installation, error) {
				return []adaptersdk.Installation{{
					InstallationID: "fixture-installation", AdapterID: fakeadapter.AdapterID,
				}}, nil
			},
			func(context.Context, string, string) (ReconciliationWindowSummary, error) {
				return ReconciliationWindowSummary{
					InstallationID: "fixture-installation", SourceID: "fixture-source",
					MinimumRatio: 1,
					Sessions: []SessionReconciliationSummary{{
						SessionID: "fixture-session", CompatibilityVersion: "v1",
						ToleranceKnown: true, TotalLanes: 2, AgreeingLanes: 1,
						MismatchLanes: 1, AdapterVersion: fakeadapter.AdapterVersion,
					}},
				}, nil
			},
			func(context.Context, string, string, string) (float64, ReconciliationMismatchClass, bool, error) {
				return 1, MismatchClassNone, true, nil
			},
		)
		return check.Evaluate(ctx, CheckInput{Now: now}, CheckTarget{
			CapabilityID:   string(adaptersdk.CapabilityActivitySessions),
			InstallationID: "fixture-installation",
		})
	default:
		return CheckOutcome{}, fmt.Errorf("fault %s has no production detector injection", definition.FaultID)
	}
}

func discoveryFaultOutcome(finding discoveryFinding, now time.Time) CheckOutcome {
	status := CheckStatusPass
	if finding.failed {
		status = CheckStatusFail
	}
	return CheckOutcome{
		CheckID: DiscoveryConfigCheckID, Status: status,
		Category: finding.category, DetailRef: finding.detail, ObservedAt: now,
	}
}

func fixtureLiveCanaryRecipe() LiveCanaryRecipe {
	return LiveCanaryRecipe{
		RecipeID: "fixture-canary-v1", AdapterID: "fixture-adapter",
		Command:          []string{"fixture-agent", "--non-interactive"},
		FixtureWorkspace: "/tmp/kansoku-canary-fixture", CanarySkillName: "kansoku-canary-fixture",
		LocalMCPEchoTool: "mcp.echo", ExpectedEventDAG: append([]string(nil), DefaultExpectedCanaryDAG...),
		MaxTurns: 2, MaxTokens: 128, MaxCostUSD: 0.01, MaxDuration: 30 * time.Second,
		Cooldown: time.Hour, Cleanup: "remove_fixture_workspace_and_stop_child",
		NamespaceExclusion: "kansoku-canary namespace excluded from usage", Enabled: true,
	}
}

type FaultRecovery struct {
	ResolvedAt *time.Time
	RunID      string
}

// ApplyFreshFaultRecovery closes a detected fault only for a later run with
// a fresh passing check for the same failure class.
func ApplyFreshFaultRecovery(detection FaultDetection, laterRunID string, pass CheckOutcome) FaultRecovery {
	if laterRunID == "" || laterRunID == detection.AuditRunID || pass.Status != CheckStatusPass ||
		pass.ObservedAt.IsZero() || !pass.ObservedAt.After(detection.DetectedAt) {
		return FaultRecovery{}
	}
	resolved := pass.ObservedAt.UTC()
	return FaultRecovery{ResolvedAt: &resolved, RunID: laterRunID}
}
