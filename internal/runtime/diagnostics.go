package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	goruntime "runtime"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const DiagnosticsVersion = "kansoku.diagnostics/1"

type ResourceMetrics struct {
	ProcessCPUSeconds float64 `json:"process_cpu_seconds"`
	ProcessRSSBytes   int64   `json:"process_rss_bytes"`
	Goroutines        int     `json:"goroutines"`
	DBPoolAcquired    int32   `json:"db_pool_acquired"`
	DBPoolIdle        int32   `json:"db_pool_idle"`
	QueueDepth        int     `json:"queue_depth"`
	OldestQueueAge    float64 `json:"oldest_queue_age_seconds"`
}

type DiagnosticsBundle struct {
	Format                string            `json:"format"`
	AppVersion            string            `json:"app_version"`
	SchemaVersions        map[string]string `json:"schema_versions"`
	MigrationStatus       map[string]int64  `json:"migration_status"`
	HealthDimensions      map[string]int64  `json:"health_dimensions"`
	IncidentCountsByClass map[string]int64  `json:"incident_counts_by_class"`
	TableCounts           map[string]int64  `json:"table_counts"`
	QueueMetrics          QueueMetrics      `json:"queue_metrics"`
	JobStates             map[string]int64  `json:"job_states"`
	BackupStatus          map[string]any    `json:"backup_status"`
	ResourceMetrics       ResourceMetrics   `json:"resource_metrics"`
	ConfigFingerprint     string            `json:"config_fingerprint"`
}

func (s *OperationsService) Diagnostics(ctx context.Context, _ DiagnosticsRequest) (any, error) {
	queueMetrics, err := s.queue.Metrics()
	if err != nil {
		return nil, &JobFailure{Class: "diagnostics_queue_unavailable"}
	}
	migrations := map[string]int64{}
	for label, table := range map[string]string{
		"dataplatform": "schema_migrations",
		"integrity":    "integrity_schema_migrations",
		"runtime":      "runtime_schema_migrations",
	} {
		var count int64
		if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM "+quoteIdentifier(table)).Scan(&count); err != nil {
			return nil, &JobFailure{Class: "diagnostics_migration_unavailable"}
		}
		migrations[label] = count
	}
	health, err := groupedCounts(ctx, s.pool, `SELECT status, count(*) FROM integrity_audit_checks GROUP BY status`)
	if err != nil {
		return nil, &JobFailure{Class: "diagnostics_health_unavailable"}
	}
	incidents, err := groupedCounts(ctx, s.pool, `
		SELECT d.failure_class, count(*)
		FROM integrity_incident_details d
		JOIN integrity_incidents i ON i.incident_id=d.incident_id
		WHERE i.resolved_at IS NULL GROUP BY d.failure_class
	`)
	if err != nil {
		return nil, &JobFailure{Class: "diagnostics_incidents_unavailable"}
	}
	jobs, err := groupedCounts(ctx, s.pool, `SELECT state, count(*) FROM runtime_job_runs GROUP BY state`)
	if err != nil {
		return nil, &JobFailure{Class: "diagnostics_jobs_unavailable"}
	}
	counts, err := s.tableCounts(ctx, s.pool)
	if err != nil {
		return nil, &JobFailure{Class: "diagnostics_counts_unavailable"}
	}
	backup := map[string]any{
		"recorded": false, "checksum_ok": false, "restore_test_ran": false,
		"restore_test_passed": false,
	}
	var backupAt, restoreAt *time.Time
	var checksumOK, restoreRan, restorePassed bool
	err = s.pool.QueryRow(ctx, `
		SELECT last_backup_at,last_backup_checksum_ok,last_restore_test_at,
		       last_restore_test_ran,last_restore_test_passed
		FROM integrity_backup_status WHERE id=1
	`).Scan(&backupAt, &checksumOK, &restoreAt, &restoreRan, &restorePassed)
	if err == nil {
		backup = map[string]any{
			"recorded": backupAt != nil, "checksum_ok": checksumOK,
			"restore_test_recorded": restoreAt != nil, "restore_test_ran": restoreRan,
			"restore_test_passed": restorePassed,
		}
	}
	resources := collectResourceMetrics(s.pool.Stat(), queueMetrics, s.now().UTC())
	bundle := DiagnosticsBundle{
		Format: DiagnosticsVersion, AppVersion: AppVersion,
		SchemaVersions: map[string]string{
			"dataplatform": "kansoku.data-platform-schema/1",
			"integrity":    "kansoku.integrity-audit-report/1",
			"runtime":      RuntimeSchemaVersion,
		},
		MigrationStatus: migrations, HealthDimensions: health,
		IncidentCountsByClass: incidents, TableCounts: counts,
		QueueMetrics: queueMetrics, JobStates: jobs, BackupStatus: backup,
		ResourceMetrics: resources, ConfigFingerprint: safeConfigFingerprint(s.config),
	}
	encoded, err := json.Marshal(bundle)
	if err != nil || int64(len(encoded)) > s.config.DiagnosticsMaxBytes || containsForbiddenResponseKey(encoded) {
		return nil, &JobFailure{Class: "diagnostics_policy_violation"}
	}
	return bundle, nil
}

func CreateDiagnosticsBundle(ctx context.Context, service *OperationsService) (DiagnosticsBundle, error) {
	if service == nil {
		return DiagnosticsBundle{}, &JobFailure{Class: "operations_service_required"}
	}
	result, err := service.Diagnostics(ctx, DiagnosticsRequest{})
	if err != nil {
		return DiagnosticsBundle{}, err
	}
	return result.(DiagnosticsBundle), nil
}

func groupedCounts(ctx context.Context, pool *pgxpool.Pool, query string) (map[string]int64, error) {
	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var key string
		var count int64
		if err := rows.Scan(&key, &count); err != nil {
			return nil, err
		}
		result[key] = count
	}
	return result, rows.Err()
}

func collectResourceMetrics(stats *pgxpool.Stat, queue QueueMetrics, now time.Time) ResourceMetrics {
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	cpu := timevalSeconds(usage.Utime) + timevalSeconds(usage.Stime)
	rss := usage.Maxrss
	if goruntime.GOOS != "darwin" {
		rss *= 1024
	}
	depth := 0
	oldestAge := 0.0
	for source, value := range queue.Depth {
		_ = source
		depth += value
	}
	for _, oldest := range queue.OldestSpoolRecord {
		if !oldest.IsZero() {
			age := now.Sub(oldest).Seconds()
			if age > oldestAge {
				oldestAge = age
			}
		}
	}
	return ResourceMetrics{
		ProcessCPUSeconds: cpu, ProcessRSSBytes: rss, Goroutines: goruntime.NumGoroutine(),
		DBPoolAcquired: stats.AcquiredConns(), DBPoolIdle: stats.IdleConns(),
		QueueDepth: depth, OldestQueueAge: oldestAge,
	}
}

func timevalSeconds(value syscall.Timeval) float64 {
	return float64(value.Sec) + float64(value.Usec)/1_000_000
}

func safeConfigFingerprint(config Config) string {
	material := struct {
		Version          string
		AppVersion       string
		HTTPListen       string
		OTLPHTTPListen   string
		OTLPGRPCListen   string
		ContainerMode    bool
		DatabaseHost     string
		DatabasePort     int
		DatabaseName     string
		DatabaseUser     string
		QueueCapacity    int
		SpoolMaxBytes    int64
		RetentionDays    int
		IntegrityEnabled bool
	}{
		config.Version, config.AppVersion, config.HTTPListen, config.OTLPHTTPListen,
		config.OTLPGRPCListen, config.ContainerMode, config.Database.Host,
		config.Database.Port, config.Database.Name, config.Database.User,
		config.QueueCapacity, config.SpoolMaxBytes, config.RetentionDays,
		config.IntegrityEnabled,
	}
	encoded, _ := json.Marshal(material)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
