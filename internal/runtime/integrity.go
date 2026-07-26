package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/claudeadapter"
	"kansoku.local/kansoku/internal/codexadapter"
	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/integrity"
	"kansoku.local/kansoku/internal/localhttp"
	"kansoku.local/kansoku/internal/observability"
)

func NewDefaultIntegrityRunner(
	config Config,
	secrets Secrets,
	pool *pgxpool.Pool,
	guard *localhttp.Guard,
	ingestor *observability.Ingestor,
	receiver *observability.OTLPReceiver,
	store *observability.FileStore,
	handoff *dataplatform.ObservabilityHandoff,
) (*integrity.ProductionAssembly, error) {
	registry := adaptersdk.NewRegistry()
	if err := registry.Register(codexadapter.New()); err != nil {
		return nil, err
	}
	if err := registry.Register(claudeadapter.New()); err != nil {
		return nil, err
	}
	host, err := adaptersdk.NewHostView(nil, nil, secrets.IdentityHMAC)
	if err != nil {
		return nil, err
	}
	noInstallations := func(context.Context, string) ([]adaptersdk.Installation, error) {
		return nil, nil
	}
	discovery := integrity.NewDiscoveryConfigCheck(registry, host, noInstallations, nil)
	endpoints := integrity.NewEndpointAndHookCheck(
		func(context.Context) ([]integrity.EndpointTarget, error) { return nil, nil },
		func(context.Context, integrity.EndpointTarget) (integrity.PassiveEndpointEvidence, error) {
			return integrity.PassiveEndpointEvidence{}, nil
		},
	)
	freshness := integrity.NewSourceFreshnessCheck(registry,
		func(context.Context, string) ([]integrity.SourceTarget, error) { return nil, nil },
		func(_ context.Context, sourceID string) (observability.Watermark, bool, error) {
			watermark, ok := store.Snapshot().Watermarks[sourceID]
			return watermark, ok, nil
		},
	)
	adapterAudit := integrity.NewAdapterFixtureAuditCheck(registry, noInstallations)
	synthetic := integrity.NewSyntheticPipelineCheck(guard, ingestor, receiver, store, secrets.IngressBearer)
	synthetic.Postgres = pool
	synthetic.Handoff = handoff
	synthetic.RequirePostgres = true
	reconciliation := integrity.NewCrossSourceReconciliationCheck(registry, noInstallations, nil, nil)
	unknown := integrity.NewUnknownSchemaAndLagCheck(func(context.Context) ([]integrity.SourceIntegritySnapshot, error) {
		return nil, nil
	})
	incidentWorkbench := integrity.NewIncidentWorkbenchAuditCheck(pool)
	rollup := integrity.NewRollupFormulaDBIntegrityCheck(
		pool,
		func(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
			return dataplatform.RepairQueueDepth(ctx, pool)
		},
		integrity.NewExpectedFormulasLookupFunc(),
		integrity.NewMigrationsUpToDateCheckFunc(),
	)
	storage := integrity.NewRetentionDiskBackupCheck(
		integrity.NewRetentionDryRunLookup(pool),
		integrity.NewDiskForecastLookup(config.DataDir, nil),
		integrity.NewBackupStatusLookup(pool),
		integrity.NewPrivacyCanaryLookup(config.PrivacyCanaryFixture, secrets.IdentityHMAC, time.Now),
	)
	storage.HorizonDays = config.RetentionDays
	storage.DiskForecastBudget = config.DiskBudgetFraction
	storage.SpoolIntegrity = func(context.Context) error {
		for _, source := range productionSources {
			path := filepath.Join(config.DataDir, "spool", string(source)+".jsonl")
			if err := observability.CheckDurableSpool(path, config.SpoolMaxBytes); err != nil {
				return err
			}
		}
		return nil
	}
	liveCanary := integrity.NewLiveCanaryCheck(nil, map[string]integrity.LiveCanaryGate{}, nil, nil)
	liveCanary.Registry = registry
	return integrity.NewProductionAssembly(integrity.ProductionAssemblyConfig{
		Pool: pool, AdapterRegistry: registry,
		Checks: []integrity.Check{
			discovery, endpoints, freshness, adapterAudit, synthetic,
			reconciliation, unknown, incidentWorkbench, rollup, storage, liveCanary,
		},
		ReportSigningKeyID: "runtime-audit-hmac/1",
		ReportSigningKey:   secrets.AuditHMAC,
		DailySchedule: integrity.DailyScheduleConfig{
			LocalHour: 3, LocalMinute: 0, MaxJitter: 15 * time.Minute,
			Location: time.Local,
		},
		VersionPollInterval: time.Minute,
		Fingerprints:        runtimeFingerprints(registry),
	})
}

func runtimeFingerprints(registry *adaptersdk.Registry) integrity.FingerprintProvider {
	return func(_ context.Context, now time.Time) ([]integrity.DriftFingerprint, error) {
		var result []integrity.DriftFingerprint
		for _, adapterID := range registry.IDs() {
			adapter, err := registry.Get(adapterID)
			if err != nil {
				return nil, err
			}
			manifest := adapter.Manifest()
			sources, err := json.Marshal(manifest.Sources)
			if err != nil {
				return nil, err
			}
			fixtureSHA := sha256.Sum256([]byte(adapterID + "\x00" + manifest.Version + "\x00fixture-contract"))
			schemaSHA := sha256.Sum256(sources)
			configSHA := sha256.Sum256([]byte(adapterID + "\x00" + manifest.Version + "\x00configuration-recipe"))
			result = append(result,
				integrity.DriftFingerprint{Kind: integrity.FingerprintExecutableVersion, SubjectID: adapterID, ValueRef: "not_observed", ObservedAt: now.UTC()},
				integrity.DriftFingerprint{Kind: integrity.FingerprintConfigRecipe, SubjectID: adapterID, ValueRef: "sha256:" + hex.EncodeToString(configSHA[:]), ObservedAt: now.UTC()},
				integrity.DriftFingerprint{Kind: integrity.FingerprintAdapterVersion, SubjectID: adapterID, ValueRef: manifest.Version, ObservedAt: now.UTC()},
				integrity.DriftFingerprint{Kind: integrity.FingerprintFixtureVersion, SubjectID: adapterID, ValueRef: "sha256:" + hex.EncodeToString(fixtureSHA[:]), ObservedAt: now.UTC()},
				integrity.DriftFingerprint{Kind: integrity.FingerprintEventSchema, SubjectID: adapterID, ValueRef: "sha256:" + hex.EncodeToString(schemaSHA[:]), ObservedAt: now.UTC()},
			)
		}
		result = append(result, integrity.DriftFingerprint{
			Kind: integrity.FingerprintFormulaRegistry, SubjectID: "rollup-formulas",
			ValueRef: "1", ObservedAt: now.UTC(),
		})
		for _, fingerprint := range result {
			if err := fingerprint.Validate(); err != nil {
				return nil, err
			}
		}
		return result, nil
	}
}
