package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/integrity"
	"kansoku.local/kansoku/internal/localhttp"
	"kansoku.local/kansoku/internal/observability"
	"kansoku.local/kansoku/internal/privacy"
	"kansoku.local/kansoku/internal/webui"
)

// composeAppHandler builds the outer HTTP handler for the appliance UI port:
// requests under /api/ are delegated to the existing guarded API mux
// (unchanged), and every other path is served by the dashboard SPA handler
// (static assets + SPA-fallback index with per-request token injection). This
// is a pure prefix dispatch; it adds no logic to either underlying handler and
// never touches the localhttp guards.
func composeAppHandler(api, dashboard http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		dashboard.ServeHTTP(w, r)
	})
}

type IntegrityRunner interface {
	Run(context.Context) error
}

type supervisedRuntimeSource interface {
	ScanOnce(context.Context) error
	Run(context.Context)
}

type appServerRuntimeSource interface {
	Configure(context.Context) error
}

type Appliance struct {
	Config        Config
	Secrets       Secrets
	Pool          *pgxpool.Pool
	Store         *observability.CompactStore
	MirrorReport  *MirrorReconciliationReport
	Ingestor      *observability.Ingestor
	Queue         *DurableIngressQueue
	Plans         *PlanManager
	Jobs          *JobManager
	Operations    *OperationsService
	Inventory     supervisedRuntimeSource
	Rollout       supervisedRuntimeSource
	AppServer     appServerRuntimeSource
	APIHandler    http.Handler
	IngressHTTP   http.Handler
	GRPCServer    *grpc.Server
	integrity     IntegrityRunner
	httpServer    *http.Server
	ingressServer *http.Server
	closeOnce     sync.Once
}

func OpenAppliance(ctx context.Context, configPath string, integrityRunner IntegrityRunner) (*Appliance, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	secrets, err := LoadSecretFiles(config.Secrets)
	if err != nil {
		return nil, err
	}
	return NewAppliance(ctx, config, secrets, integrityRunner)
}

// NewAppliance performs the ordered compatibility/migration/spool preflight
// and assembles services without activating runtime collectors. Public
// listeners and collector side effects are created only by Run. This
// distinction is material for one-shot operational commands (backup,
// restore-verify, export, import and diagnostics): their intentionally
// narrower containers do not mount agent state and therefore must not
// overwrite source/inventory health as if a serving collector had failed.
func NewAppliance(ctx context.Context, config Config, secrets Secrets, integrityRunner IntegrityRunner) (*Appliance, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(config.DataDir); err != nil {
		return nil, errors.New("data_directory_preflight_failed")
	}
	if err := ensurePrivateDirectory(filepath.Join(config.DataDir, "spool")); err != nil {
		return nil, errors.New("spool_directory_preflight_failed")
	}
	if err := ensurePrivateDirectory(filepath.Join(config.DataDir, "mirror")); err != nil {
		return nil, errors.New("mirror_directory_preflight_failed")
	}
	if err := ensurePrivateDirectory(filepath.Join(config.DataDir, "checkpoints")); err != nil {
		return nil, errors.New("checkpoint_directory_preflight_failed")
	}
	dsn, err := config.DatabaseDSN(secrets.DatabasePassword)
	if err != nil {
		return nil, err
	}
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("database_config_parse_failed")
	}
	poolConfig.MaxConns = 16
	poolConfig.MinConns = 1
	poolConfig.MaxConnLifetime = time.Hour
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, errors.New("database_connect_failed")
	}
	cleanupPool := true
	defer func() {
		if cleanupPool {
			pool.Close()
		}
	}()
	if err := pool.Ping(ctx); err != nil {
		return nil, errors.New("database_connect_failed")
	}
	if err := PreflightMigrations(ctx, pool); err != nil {
		return nil, err
	}
	if err := dataplatform.Migrate(ctx, pool); err != nil {
		return nil, errors.New("dataplatform_migration_failed")
	}
	if err := integrity.Migrate(ctx, pool); err != nil {
		return nil, errors.New("integrity_migration_failed")
	}
	if err := Migrate(ctx, pool); err != nil {
		return nil, errors.New("runtime_migration_failed")
	}
	mirrorReport, legacyState, err := ReconcileAndArchiveLegacyMirror(
		ctx, pool, config.DataDir, config.SpoolMaxBytes,
	)
	if err != nil {
		return nil, err
	}
	inventory, err := NewInventoryCollector(
		pool, config.InventoryTargets, secrets.IdentityHMAC,
		time.Duration(config.InventoryScanIntervalSeconds)*time.Second,
	)
	if err != nil {
		return nil, errors.New("inventory_collector_initialization_failed")
	}
	store, err := observability.OpenCompactStore(
		filepath.Join(config.DataDir, "checkpoints", "state.json"),
		config.CheckpointStateMaxBytes,
	)
	if err != nil {
		return nil, errors.New("checkpoint_store_open_failed")
	}
	for _, watermark := range legacyState.Watermarks {
		value := watermark
		if _, err := store.Commit(observability.CommitRequest{Watermark: &value}); err != nil {
			return nil, errors.New("legacy_watermark_migration_failed")
		}
	}
	for _, checkpoint := range legacyState.Checkpoints {
		value := checkpoint
		if _, err := store.Commit(observability.CommitRequest{Checkpoint: &value}); err != nil {
			return nil, errors.New("legacy_checkpoint_migration_failed")
		}
	}
	ingestor, err := observability.NewIngestor(store, secrets.IdentityHMAC, privacy.DefaultLimits(), config.QueueCapacity)
	if err != nil {
		return nil, errors.New("ingestor_initialization_failed")
	}
	handoff, err := dataplatform.NewObservabilityHandoff(pool, config.QueryTimeout())
	if err != nil {
		return nil, errors.New("database_handoff_initialization_failed")
	}
	queue, err := NewDurableIngressQueue(handoff, config.DataDir, config.QueueCapacity, config.SpoolMaxBytes)
	if err != nil {
		return nil, errors.New("durable_queue_initialization_failed")
	}
	cleanupQueue := true
	defer func() {
		if cleanupQueue {
			queue.Close()
		}
	}()
	healthRecorder, err := NewPostgresIngestionHealthRecorder(pool, config.QueryTimeout())
	if err != nil {
		return nil, errors.New("ingestion_health_recorder_initialization_failed")
	}
	if err := queue.ConfigureHealthRecorder(ctx, healthRecorder); err != nil {
		return nil, errors.New("ingestion_health_state_load_failed")
	}
	if err := queue.ReplaySpools(); err != nil {
		return nil, errors.New("spool_replay_failed")
	}
	if err := ingestor.ConfigureDurableFactSink(queue); err != nil {
		return nil, errors.New("durable_sink_configuration_failed")
	}
	rollout, err := NewCodexRolloutWatcher(
		pool, ingestor, store, config.InventoryTargets, secrets.IdentityHMAC,
		time.Duration(config.RolloutWatchIntervalSeconds)*time.Second,
	)
	if err != nil {
		return nil, errors.New("codex_rollout_watcher_initialization_failed")
	}
	receiver, err := observability.NewOTLPReceiver(ingestor, 1<<20)
	if err != nil {
		return nil, errors.New("otlp_receiver_initialization_failed")
	}
	ingressGuard, err := localhttp.NewApplianceGuard(secrets.IngressBearer, secrets.CSRF, 1<<20, 120, time.Minute, config.ContainerMode)
	if err != nil {
		return nil, errors.New("ingress_guard_initialization_failed")
	}
	appServer, err := NewCodexAppServerIngress(pool, ingestor, secrets.IdentityHMAC, time.Now)
	if err != nil {
		return nil, errors.New("codex_app_server_ingress_initialization_failed")
	}
	ingressHTTP, err := observability.NewIngressHTTPHandlerWithEvidenceBridge(
		ingressGuard, ingestor, receiver, appServer,
	)
	if err != nil {
		return nil, errors.New("ingress_http_initialization_failed")
	}
	grpcServer, err := observability.NewApplianceIngressGRPCServer(receiver, secrets.IngressBearer, config.ContainerMode)
	if err != nil {
		return nil, errors.New("ingress_grpc_initialization_failed")
	}
	if config.IntegrityEnabled && integrityRunner == nil {
		integrityRunner, err = NewDefaultIntegrityRunner(
			config, secrets, pool, ingressGuard, ingestor, receiver, store, handoff,
		)
		if err != nil {
			return nil, errors.New("integrity_assembly_initialization_failed")
		}
	}
	plans, err := NewPlanManager(pool, secrets.AuditHMAC)
	if err != nil {
		return nil, errors.New("plan_manager_initialization_failed")
	}
	var operations *OperationsService
	handlers := map[JobID]JobHandler{
		JobRollupRepair: func(jobCtx context.Context) (map[string]int64, error) {
			count, err := dataplatform.RunRepairWorker(jobCtx, pool)
			return map[string]int64{"recomputed_buckets": int64(count)}, err
		},
		JobRetention: func(jobCtx context.Context) (map[string]int64, error) {
			dropped, err := dataplatform.ApplyRetention(jobCtx, pool, time.Now().UTC(), config.RetentionDays)
			if err != nil {
				return nil, err
			}
			counts := map[string]int64{}
			for table, values := range dropped {
				counts[table] = int64(len(values))
			}
			expired, err := ApplyIncidentMetadataRetention(
				jobCtx, pool, time.Now().UTC(), config.RetentionDays,
			)
			for table, count := range expired {
				counts[table] = count
			}
			return counts, err
		},
		JobBackup: func(jobCtx context.Context) (map[string]int64, error) {
			if operations == nil {
				return nil, &JobFailure{Class: "operations_not_ready"}
			}
			result, err := operations.Backup(jobCtx, BackupRequest{})
			if err != nil {
				return nil, err
			}
			return result.(BackupResult).TableCounts, nil
		},
		JobExport: func(jobCtx context.Context) (map[string]int64, error) {
			if operations == nil {
				return nil, &JobFailure{Class: "operations_not_ready"}
			}
			result, err := operations.Export(jobCtx, ExportRequest{})
			if err != nil {
				return nil, err
			}
			return map[string]int64{"records": result.(ExportResult).RecordCount}, nil
		},
		JobDailyIntegrity: operatorInputJob,
		JobRestoreVerify:  operatorInputJob,
		JobImport:         operatorInputJob,
	}
	jobs, err := NewJobManager(pool, handlers)
	if err != nil {
		return nil, errors.New("job_manager_initialization_failed")
	}
	if err := jobs.RecoverInterrupted(ctx); err != nil {
		return nil, errors.New("job_recovery_failed")
	}
	operations, err = NewOperationsService(config, secrets, pool, queue, jobs)
	if err != nil {
		return nil, errors.New("operations_initialization_failed")
	}
	api, err := NewAPI(config, secrets, pool, queue, plans, jobs, operations)
	if err != nil {
		return nil, errors.New("api_initialization_failed")
	}
	// The Session 10 dashboard is served by this same loopback process: the
	// webui handler owns everything that is not under /api/, and the existing
	// api handler owns /api/ unchanged. Only the read bearer + CSRF are handed
	// to the UI (read-only dashboard); the mutation bearer is never embedded.
	dashboard, err := webui.NewHandler(secrets.ReadBearer, secrets.CSRF)
	if err != nil {
		return nil, errors.New("webui_initialization_failed")
	}
	appliance := &Appliance{
		Config: config, Secrets: secrets, Pool: pool, Store: store, MirrorReport: mirrorReport,
		Ingestor: ingestor, Queue: queue, Plans: plans, Jobs: jobs,
		Operations: operations, Inventory: inventory, Rollout: rollout, AppServer: appServer,
		APIHandler:  composeAppHandler(api, dashboard),
		IngressHTTP: ingressHTTP, GRPCServer: grpcServer, integrity: integrityRunner,
	}
	cleanupQueue = false
	cleanupPool = false
	return appliance, nil
}

func operatorInputJob(context.Context) (map[string]int64, error) {
	return nil, &JobFailure{Class: "operator_input_required"}
}

func (a *Appliance) Run(ctx context.Context) error {
	if a == nil || a.Pool == nil || a.Queue == nil || a.GRPCServer == nil {
		return errors.New("appliance_incomplete")
	}
	if err := a.activateRuntimeSources(ctx); err != nil {
		return err
	}
	httpListener, err := net.Listen("tcp", a.Config.HTTPListen)
	if err != nil {
		return errors.New("http_listener_failed")
	}
	defer httpListener.Close()
	ingressListener, err := net.Listen("tcp", a.Config.OTLPHTTPListen)
	if err != nil {
		return errors.New("otlp_http_listener_failed")
	}
	defer ingressListener.Close()
	grpcListener, err := net.Listen("tcp", a.Config.OTLPGRPCListen)
	if err != nil {
		return errors.New("otlp_grpc_listener_failed")
	}
	defer grpcListener.Close()
	a.httpServer = &http.Server{
		Handler: a.APIHandler, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10,
	}
	a.ingressServer = &http.Server{
		Handler: a.IngressHTTP, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10,
	}
	errorsOut := make(chan error, 5)
	go serveHTTP(a.httpServer, httpListener, errorsOut, "api_http_server_failed")
	go serveHTTP(a.ingressServer, ingressListener, errorsOut, "ingress_http_server_failed")
	go func() {
		if err := a.GRPCServer.Serve(grpcListener); err != nil {
			errorsOut <- errors.New("otlp_grpc_server_failed")
		}
	}()
	schedulerCtx, schedulerCancel := context.WithCancel(ctx)
	defer schedulerCancel()
	go a.runJobScheduler(schedulerCtx, errorsOut)
	go a.Inventory.Run(schedulerCtx)
	if a.Rollout != nil {
		go a.Rollout.Run(schedulerCtx)
	}
	if a.Config.IntegrityEnabled {
		go func() {
			if err := a.integrity.Run(schedulerCtx); err != nil && !errors.Is(err, context.Canceled) {
				errorsOut <- errors.New("integrity_runtime_failed")
			}
		}()
	}
	select {
	case <-ctx.Done():
		return a.Shutdown(context.Background())
	case err := <-errorsOut:
		_ = a.Shutdown(context.Background())
		return err
	}
}

// activateRuntimeSources performs the initial read-only collection and
// source-health registration for the long-running serving process only.
// NewAppliance deliberately does not call this method: operational one-shot
// commands reuse the durable queue/operations assembly but have no agent-state
// mounts and must remain observationally inert toward collector health.
func (a *Appliance) activateRuntimeSources(ctx context.Context) error {
	if a.Inventory == nil || a.AppServer == nil {
		return errors.New("runtime_sources_incomplete")
	}
	if err := a.Inventory.ScanOnce(ctx); err != nil {
		return errors.New("inventory_initial_scan_failed")
	}
	if a.Rollout != nil {
		// The watcher persists a degraded/unknown source-health row before
		// returning a discovery or scan error. Keep the dashboard and read API
		// available so that degradation is visible and the supervised loop can
		// recover on a later scan.
		if err := a.Rollout.ScanOnce(ctx); errors.Is(err, errCodexRolloutHealthPersistence) {
			return errors.New("codex_rollout_source_health_initialization_failed")
		}
	}
	if err := a.AppServer.Configure(ctx); err != nil {
		return errors.New("codex_app_server_source_health_initialization_failed")
	}
	return nil
}

func serveHTTP(server *http.Server, listener net.Listener, errorsOut chan<- error, errorClass string) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errorsOut <- errors.New(errorClass)
	}
}

func (a *Appliance) runJobScheduler(ctx context.Context, errorsOut chan<- error) {
	repair := time.NewTicker(time.Minute)
	spoolReplay := time.NewTicker(15 * time.Second)
	daily := time.NewTicker(24 * time.Hour)
	defer repair.Stop()
	defer spoolReplay.Stop()
	defer daily.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-repair.C:
			if _, err := a.Jobs.Run(ctx, JobRollupRepair); err != nil && !errors.Is(err, context.Canceled) {
				errorsOut <- errors.New("rollup_repair_job_failed")
				return
			}
		case <-spoolReplay.C:
			// A canonical fact may already be durable while one derived
			// projection is temporarily unavailable. The queue owns the
			// sanitized Event/Evidence retry record and replays it
			// idempotently; a continued failure remains visible as spool
			// occupancy and a pending projection receipt, never as process
			// termination or a green health state.
			_ = a.Queue.ReplaySpools()
		case <-daily.C:
			for _, job := range []JobID{JobRetention, JobBackup} {
				if _, err := a.Jobs.Run(ctx, job); err != nil && !errors.Is(err, context.Canceled) {
					errorsOut <- errors.New("daily_operations_job_failed")
					return
				}
			}
		}
	}
}

func (a *Appliance) Shutdown(parent context.Context) error {
	var shutdownErr error
	a.closeOnce.Do(func() {
		a.Queue.StopAccepting()
		ctx, cancel := context.WithTimeout(parent, a.Config.ShutdownTimeout())
		defer cancel()
		if a.httpServer != nil {
			if err := a.httpServer.Shutdown(ctx); err != nil {
				shutdownErr = errors.New("api_http_shutdown_failed")
			}
		}
		if a.ingressServer != nil {
			if err := a.ingressServer.Shutdown(ctx); err != nil && shutdownErr == nil {
				shutdownErr = errors.New("ingress_http_shutdown_failed")
			}
		}
		grpcDone := make(chan struct{})
		go func() {
			a.GRPCServer.GracefulStop()
			close(grpcDone)
		}()
		select {
		case <-grpcDone:
		case <-ctx.Done():
			a.GRPCServer.Stop()
			if shutdownErr == nil {
				shutdownErr = errors.New("grpc_shutdown_timeout")
			}
		}
		if err := a.Queue.Drain(ctx); err != nil {
			if shutdownErr == nil {
				shutdownErr = err
			}
		} else {
			a.Queue.Close()
		}
		a.Pool.Close()
	})
	return shutdownErr
}

func (a *Appliance) SelfHealth(ctx context.Context) map[string]string {
	result := map[string]string{
		"config": "pass", "secret_files": "pass", "database": "pass",
		"migration_ledgers": "pass", "spool": "pass", "workers": "pass",
	}
	if err := a.Config.Validate(); err != nil {
		result["config"] = "fail"
	}
	if _, err := LoadSecretFiles(a.Config.Secrets); err != nil {
		result["secret_files"] = "fail"
	}
	if err := a.Pool.Ping(ctx); err != nil {
		result["database"] = "fail"
	}
	if err := verifyMigrationLedgers(ctx, a.Pool); err != nil {
		result["migration_ledgers"] = "fail"
	}
	if _, err := a.Queue.Metrics(); err != nil {
		result["spool"] = "fail"
	}
	return result
}

// ProbeRunningAppliance is the healthcheck path used by Compose. It never
// constructs a second appliance, replays spools, runs migrations, or starts
// competing workers beside the serving process.
func ProbeRunningAppliance(parent context.Context, config Config, secrets Secrets) (map[string]string, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if len(secrets.ReadBearer) < minSecretBytes {
		return nil, errors.New("read_bearer_missing")
	}
	_, port, err := net.SplitHostPort(config.HTTPListen)
	if err != nil || port != "43100" {
		return nil, errors.New("health_endpoint_invalid")
	}
	target := net.JoinHostPort("127.0.0.1", port)
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target+"/api/v1/health", nil)
	if err != nil {
		return nil, errors.New("health_request_build_failed")
	}
	request.Host = target
	request.Header.Set("Authorization", "Bearer "+string(secrets.ReadBearer))
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout:   2 * time.Second,
				KeepAlive: -1,
			}).DialContext,
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("health_redirect_forbidden")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("appliance_health_unreachable")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil || len(raw) == 0 || len(raw) >= 64<<10 || response.StatusCode != http.StatusOK {
		return nil, errors.New("appliance_health_failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope APIEnvelope
	if decoder.Decode(&envelope) != nil || ensureJSONEOF(decoder) != nil ||
		envelope.APIVersion != APIVersion || envelope.Error != "" {
		return nil, errors.New("appliance_health_response_invalid")
	}
	encoded, err := json.Marshal(envelope.Data)
	if err != nil || containsForbiddenResponseKey(encoded) {
		return nil, errors.New("appliance_health_response_invalid")
	}
	var data map[string]any
	if json.Unmarshal(encoded, &data) != nil {
		return nil, errors.New("appliance_health_response_invalid")
	}
	for _, key := range []string{"database", "migration_ledgers", "spool", "workers"} {
		if data[key] != "pass" && data[key] != "warning" {
			return nil, errors.New("appliance_health_failed")
		}
	}
	status, _ := data["status"].(string)
	if status == "degraded" || status == "critical" || status == "unknown" || status == "" {
		return nil, errors.New("appliance_health_degraded")
	}
	return map[string]string{
		"config": "pass", "secret_files": "pass", "database": "pass",
		"migration_ledgers": "pass", "spool": data["spool"].(string),
		"workers": "pass", "status": status,
	}, nil
}

func MigrateOnly(ctx context.Context, config Config, secrets Secrets) error {
	dsn, err := config.DatabaseDSN(secrets.DatabasePassword)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("database_connect_failed")
	}
	defer pool.Close()
	if err := PreflightMigrations(ctx, pool); err != nil {
		return err
	}
	if err := dataplatform.Migrate(ctx, pool); err != nil {
		return errors.New("dataplatform_migration_failed")
	}
	if err := integrity.Migrate(ctx, pool); err != nil {
		return errors.New("integrity_migration_failed")
	}
	if err := Migrate(ctx, pool); err != nil {
		return errors.New("runtime_migration_failed")
	}
	return nil
}
