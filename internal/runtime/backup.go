package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/integrity"
	"kansoku.local/kansoku/internal/privacy"
)

const BackupVersion = "kansoku.native-backup/1"

var safeArtifactID = regexp.MustCompile(`^[a-z]+_[0-9a-f]{32}$`)

var backupTableGroups = map[string][]string{
	"dataplatform": {
		"devices", "agent_installations", "agent_surfaces", "agent_versions",
		"projects", "providers", "models", "price_catalog_versions",
		"components", "component_versions", "component_installations",
		"component_relations", "adapter_versions", "source_instances",
		"source_schema_fingerprints", "sessions", "turns", "prompt_features",
		"events", "event_evidence", "model_operations", "token_usage",
		"cost_estimates", "component_lifecycle_events", "tool_calls",
		"mcp_connections", "change_outcomes", "correlations",
		"inventory_snapshots", "inventory_nodes", "inventory_edges",
		"component_inventory_state", "inventory_collection_status",
		"source_watermarks", "completeness_intervals", "ingest_failures",
		"schema_quarantine_metadata", "reconciliation_runs",
		"reconciliation_mismatches", "audit_runs", "audit_checks",
		"incidents", "retention_policies", "backup_runs", "restore_tests",
		"formula_versions", "rollup_status", "metric_rollups_hourly",
		"metric_rollups_daily", "rollup_repair_queue",
	},
	"integrity": {
		"integrity_audit_attempts", "integrity_audit_runs", "integrity_audit_checks",
		"integrity_incidents", "integrity_incident_details",
		"integrity_schema_compatibility", "integrity_live_canary_state",
		"integrity_backup_status", "integrity_fingerprints",
		"integrity_audit_reports",
	},
	"runtime": {
		"runtime_job_runs", "runtime_operation_approvals", "runtime_import_receipts",
	},
	"migration_ledgers": {
		"schema_migrations", "integrity_schema_migrations", "runtime_schema_migrations",
	},
}

type NativeBackupManifest struct {
	BackupVersion          string           `json:"backup_version"`
	AppVersion             string           `json:"app_version"`
	DataPlatformSchema     string           `json:"dataplatform_schema_version"`
	IntegritySchema        string           `json:"integrity_schema_version"`
	RuntimeSchema          string           `json:"runtime_schema_version"`
	FormulaRegistryVersion string           `json:"formula_registry_version"`
	AdapterVersions        []string         `json:"adapter_versions"`
	PrivacyPolicySHA256    string           `json:"privacy_policy_sha256"`
	ArchiveSHA256          string           `json:"archive_sha256"`
	CreatedAt              time.Time        `json:"created_at"`
	TableCounts            map[string]int64 `json:"table_counts"`
}

type BackupResult struct {
	BackupID      string           `json:"backup_id"`
	ArchiveSHA256 string           `json:"archive_sha256"`
	CreatedAt     time.Time        `json:"created_at"`
	TableCounts   map[string]int64 `json:"table_counts"`
}

type RestoreVerifyResult struct {
	BackupID    string           `json:"backup_id"`
	Status      string           `json:"status"`
	TableCounts map[string]int64 `json:"table_counts"`
}

type NativeToolRunner interface {
	Run(context.Context, string, []string, []string) error
}

type execNativeTools struct{}

func (execNativeTools) Run(ctx context.Context, name string, args, environment []string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = append([]string{"LANG=C", "LC_ALL=C"}, environment...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		// stderr may contain database names or local locators and is therefore
		// deliberately not returned or persisted.
		return errors.New("native_database_tool_failed")
	}
	return nil
}

type retentionPreview struct {
	RequestID        string    `json:"request_id"`
	HorizonDays      int       `json:"horizon_days"`
	ParametersSHA256 string    `json:"parameters_sha256"`
	ExpiresAt        time.Time `json:"expires_at"`
	Operation        string    `json:"operation"`
}

type OperationsService struct {
	config     Config
	secrets    Secrets
	pool       *pgxpool.Pool
	queue      *DurableIngressQueue
	jobs       *JobManager
	tools      NativeToolRunner
	now        func() time.Time
	mu         sync.Mutex
	retention  map[string]retentionPreview
	usedNonces map[[sha256.Size]byte]bool
}

var _ AdminOperations = (*OperationsService)(nil)

func NewOperationsService(config Config, secrets Secrets, pool *pgxpool.Pool, queue *DurableIngressQueue, jobs *JobManager) (*OperationsService, error) {
	if err := config.Validate(); err != nil || pool == nil || queue == nil || jobs == nil {
		return nil, errors.New("invalid_operations_configuration")
	}
	return &OperationsService{
		config: config, secrets: secrets, pool: pool, queue: queue, jobs: jobs,
		tools: execNativeTools{}, now: time.Now, retention: map[string]retentionPreview{},
		usedNonces: map[[sha256.Size]byte]bool{},
	}, nil
}

func (s *OperationsService) PreviewRetention(request RetentionPreviewRequest) (any, error) {
	if request.HorizonDays < 30 || request.HorizonDays > 3650 {
		return nil, errors.New("invalid_retention_horizon")
	}
	requestID, err := newOpaqueID("retention")
	if err != nil {
		return nil, err
	}
	parameters := sha256.Sum256([]byte(fmt.Sprintf("retention_apply\x00%d", request.HorizonDays)))
	preview := retentionPreview{
		RequestID: requestID, HorizonDays: request.HorizonDays,
		ParametersSHA256: hex.EncodeToString(parameters[:]),
		ExpiresAt:        s.now().UTC().Add(planTTL), Operation: "partition_drop_only",
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.retention) >= maxOpenPreviews {
		return nil, errors.New("preview_capacity_exhausted")
	}
	s.retention[requestID] = preview
	return preview, nil
}

func (s *OperationsService) ApplyRetention(ctx context.Context, request RetentionApplyRequest) (any, error) {
	nonceHash := sha256.Sum256([]byte(request.ApprovalNonce))
	s.mu.Lock()
	preview, ok := s.retention[request.RequestID]
	if !ok || !preview.ExpiresAt.After(s.now().UTC()) {
		delete(s.retention, request.RequestID)
		s.mu.Unlock()
		return nil, errors.New("unknown_or_expired_preview")
	}
	if request.ApprovalNonce == "" || s.usedNonces[nonceHash] {
		s.mu.Unlock()
		return nil, errors.New("replay_nonce")
	}
	if request.HorizonDays != preview.HorizonDays || request.ParametersSHA256 != preview.ParametersSHA256 {
		s.mu.Unlock()
		return nil, errors.New("approval_binding_mismatch")
	}
	s.usedNonces[nonceHash] = true
	delete(s.retention, request.RequestID)
	s.mu.Unlock()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO runtime_operation_approvals
			(request_id, operation, parameters_sha256, approval_nonce_sha256,
			 approved_at, consumed_at, result)
		VALUES ($1,'retention_apply',$2,$3,$4,$4,'approved')
	`, request.RequestID, request.ParametersSHA256, hex.EncodeToString(nonceHash[:]), s.now().UTC()); err != nil {
		return nil, errors.New("approval_persistence_failed")
	}
	dropped, err := dataplatform.ApplyRetention(ctx, s.pool, s.now().UTC(), request.HorizonDays)
	if err != nil {
		_, _ = s.pool.Exec(context.WithoutCancel(ctx), `UPDATE runtime_operation_approvals SET result='failed' WHERE request_id=$1`, request.RequestID)
		return nil, errors.New("retention_failed")
	}
	_, _ = s.pool.Exec(context.WithoutCancel(ctx), `UPDATE runtime_operation_approvals SET result='applied' WHERE request_id=$1`, request.RequestID)
	counts := map[string]int64{}
	for table, partitions := range dropped {
		counts[table] = int64(len(partitions))
	}
	return map[string]any{"request_id": request.RequestID, "dropped_partition_counts": counts}, nil
}

func (s *OperationsService) Backup(ctx context.Context, _ BackupRequest) (any, error) {
	backupID, err := newOpaqueID("backup")
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(s.config.BackupDir); err != nil {
		return nil, err
	}
	archive := filepath.Join(s.config.BackupDir, backupID+".dump")
	temporary := archive + ".tmp"
	defer func() { _ = os.Remove(temporary) }()
	passfile, cleanup, err := s.pgPassFile()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	args := []string{
		"--format=custom", "--no-owner", "--no-acl",
		"--host=" + s.config.Database.Host,
		fmt.Sprintf("--port=%d", s.config.Database.Port),
		"--username=" + s.config.Database.User,
		"--file=" + temporary,
		s.config.Database.Name,
	}
	if err := s.tools.Run(ctx, "pg_dump", args, []string{"PGPASSFILE=" + passfile}); err != nil {
		return nil, err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return nil, errors.New("backup_archive_permission_failed")
	}
	archiveHash, err := fileSHA256(temporary, 1<<40)
	if err != nil {
		return nil, err
	}
	counts, err := s.tableCounts(ctx, s.pool)
	if err != nil {
		return nil, errors.New("backup_count_snapshot_failed")
	}
	formulaVersion, adapterVersions, err := s.registryVersions(ctx)
	if err != nil {
		return nil, errors.New("backup_registry_snapshot_failed")
	}
	manifest := NativeBackupManifest{
		BackupVersion: BackupVersion, AppVersion: AppVersion,
		DataPlatformSchema: dataplatform.SchemaSpecVersion,
		IntegritySchema:    integrity.AuditReportSchemaVersion,
		RuntimeSchema:      RuntimeSchemaVersion, FormulaRegistryVersion: formulaVersion,
		AdapterVersions: adapterVersions, PrivacyPolicySHA256: privacy.PrivacyContractSemanticSHA256,
		ArchiveSHA256: archiveHash, CreatedAt: s.now().UTC(), TableCounts: counts,
	}
	if err := os.Rename(temporary, archive); err != nil {
		return nil, errors.New("backup_archive_publish_failed")
	}
	if err := writePrivateJSON(archive+".manifest.json", manifest); err != nil {
		_ = os.Remove(archive)
		return nil, err
	}
	return BackupResult{BackupID: backupID, ArchiveSHA256: archiveHash, CreatedAt: manifest.CreatedAt, TableCounts: counts}, nil
}

func CreateNativeBackup(ctx context.Context, service *OperationsService) (BackupResult, error) {
	if service == nil {
		return BackupResult{}, errors.New("operations_service_required")
	}
	result, err := service.Backup(ctx, BackupRequest{})
	if err != nil {
		return BackupResult{}, err
	}
	return result.(BackupResult), nil
}

func (s *OperationsService) RestoreVerify(ctx context.Context, request RestoreVerifyRequest) (any, error) {
	if !safeArtifactID.MatchString(request.BackupID) {
		return nil, errors.New("invalid_backup_id")
	}
	archive := filepath.Join(s.config.BackupDir, request.BackupID+".dump")
	var manifest NativeBackupManifest
	if err := readStrictJSONFile(archive+".manifest.json", 1<<20, &manifest); err != nil {
		return nil, errors.New("backup_manifest_invalid")
	}
	if err := validateBackupManifest(manifest); err != nil {
		return nil, err
	}
	actualHash, err := fileSHA256(archive, 1<<40)
	if err != nil || actualHash != manifest.ArchiveSHA256 {
		return nil, errors.New("backup_checksum_mismatch")
	}
	randomID, err := newOpaqueID("restore")
	if err != nil {
		return nil, err
	}
	databaseName := strings.ReplaceAll(randomID, "_", "")
	if _, err := s.pool.Exec(ctx, "CREATE DATABASE "+quoteIdentifier(databaseName)); err != nil {
		return nil, errors.New("restore_database_create_failed")
	}
	cleaned := false
	defer func() {
		if !cleaned {
			_ = dropRestoreDatabase(s.pool, databaseName)
		}
	}()
	passfile, cleanup, err := s.pgPassFile()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	args := []string{
		"--no-owner", "--no-acl", "--exit-on-error",
		"--host=" + s.config.Database.Host,
		fmt.Sprintf("--port=%d", s.config.Database.Port),
		"--username=" + s.config.Database.User,
		"--dbname=" + databaseName,
		archive,
	}
	if err := s.tools.Run(ctx, "pg_restore", args, []string{"PGPASSFILE=" + passfile}); err != nil {
		return nil, err
	}
	restoreConfig := s.config
	restoreConfig.Database.Name = databaseName
	dsn, err := restoreConfig.DatabaseDSN(s.secrets.DatabasePassword)
	if err != nil {
		return nil, err
	}
	restorePool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, errors.New("restore_database_connect_failed")
	}
	defer restorePool.Close()
	counts, err := s.tableCounts(ctx, restorePool)
	if err != nil {
		return nil, errors.New("restore_count_verification_failed")
	}
	if !equalCounts(counts, manifest.TableCounts) {
		return nil, errors.New("restore_count_mismatch")
	}
	if err := verifyMigrationLedgers(ctx, restorePool); err != nil {
		return nil, err
	}
	if err := verifyRestoredSemantics(ctx, s.pool, restorePool, manifest); err != nil {
		return nil, err
	}
	restorePool.Close()
	if err := dropRestoreDatabase(s.pool, databaseName); err != nil {
		return nil, err
	}
	cleaned = true
	return RestoreVerifyResult{BackupID: request.BackupID, Status: "pass", TableCounts: counts}, nil
}

func validateBackupManifest(manifest NativeBackupManifest) error {
	if manifest.BackupVersion != BackupVersion || manifest.AppVersion != AppVersion ||
		manifest.DataPlatformSchema != dataplatform.SchemaSpecVersion ||
		manifest.IntegritySchema != integrity.AuditReportSchemaVersion ||
		manifest.RuntimeSchema != RuntimeSchemaVersion ||
		manifest.PrivacyPolicySHA256 != privacy.PrivacyContractSemanticSHA256 ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(manifest.ArchiveSHA256) ||
		manifest.CreatedAt.IsZero() || len(manifest.TableCounts) == 0 {
		return errors.New("backup_manifest_incompatible")
	}
	for table, count := range manifest.TableCounts {
		if !safeArtifactTable.MatchString(table) || count < 0 {
			return errors.New("backup_manifest_incompatible")
		}
	}
	return nil
}

var safeArtifactTable = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func dropRestoreDatabase(pool *pgxpool.Pool, databaseName string) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := pool.Exec(cleanupCtx, `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname=$1 AND pid<>pg_backend_pid()
	`, databaseName); err != nil {
		return errors.New("restore_database_cleanup_failed")
	}
	if _, err := pool.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+quoteIdentifier(databaseName)); err != nil {
		return errors.New("restore_database_cleanup_failed")
	}
	return nil
}

func verifyRestoredSemantics(ctx context.Context, source, restored *pgxpool.Pool, manifest NativeBackupManifest) error {
	formulaVersion, adapterVersions, err := registryVersionsForPool(ctx, restored)
	if err != nil || formulaVersion != manifest.FormulaRegistryVersion ||
		!equalStrings(adapterVersions, manifest.AdapterVersions) {
		return errors.New("restore_registry_lineage_mismatch")
	}
	for _, ledger := range []string{"schema_migrations", "integrity_schema_migrations", "runtime_schema_migrations"} {
		query := "SELECT version, checksum_sha256 FROM " + quoteIdentifier(ledger) + " ORDER BY version"
		sourceDigest, err := queryDigest(ctx, source, query)
		if err != nil {
			return errors.New("restore_migration_ledger_invalid")
		}
		restoredDigest, err := queryDigest(ctx, restored, query)
		if err != nil || sourceDigest != restoredDigest {
			return errors.New("restore_migration_ledger_mismatch")
		}
	}
	const constraintQuery = `
		SELECT conrelid::regclass::text, conname, pg_get_constraintdef(oid), convalidated
		FROM pg_constraint
		WHERE connamespace='public'::regnamespace
		ORDER BY conrelid::regclass::text, conname
	`
	sourceConstraints, err := queryDigest(ctx, source, constraintQuery)
	if err != nil {
		return errors.New("restore_constraint_verification_failed")
	}
	restoredConstraints, err := queryDigest(ctx, restored, constraintQuery)
	if err != nil || sourceConstraints != restoredConstraints {
		return errors.New("restore_constraint_mismatch")
	}
	for _, table := range []string{"metric_rollups_hourly", "metric_rollups_daily"} {
		query := `
			SELECT metric_family,bucket_start,dimension_scope,formula_version,
			       event_count,unknown_count,completeness_duration_ms,
			       value_numeric,value_p50,value_p90,value_p95,value_p99
			FROM ` + quoteIdentifier(table) + `
			ORDER BY metric_family,bucket_start,dimension_scope
			LIMIT 64
		`
		sourceSample, err := queryDigest(ctx, source, query)
		if err != nil {
			return errors.New("restore_formula_sample_failed")
		}
		restoredSample, err := queryDigest(ctx, restored, query)
		if err != nil || sourceSample != restoredSample {
			return errors.New("restore_formula_sample_mismatch")
		}
	}
	return nil
}

func registryVersionsForPool(ctx context.Context, pool *pgxpool.Pool) (string, []string, error) {
	var formulas []string
	rows, err := pool.Query(ctx, `SELECT formula_id || '/' || version::text FROM formula_versions ORDER BY formula_id, version`)
	if err != nil {
		return "", nil, err
	}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return "", nil, err
		}
		formulas = append(formulas, version)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", nil, err
	}
	rows.Close()
	formulaHash := sha256.Sum256([]byte(strings.Join(formulas, "\x00")))
	var adapters []string
	rows, err = pool.Query(ctx, `SELECT adapter_id || '@' || version FROM adapter_versions ORDER BY adapter_id, version`)
	if err != nil {
		return "", nil, err
	}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return "", nil, err
		}
		adapters = append(adapters, version)
	}
	err = rows.Err()
	rows.Close()
	return "sha256:" + hex.EncodeToString(formulaHash[:]), adapters, err
}

func queryDigest(ctx context.Context, pool *pgxpool.Pool, query string) (string, error) {
	rows, err := pool.Query(ctx, query)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	for rows.Next() {
		values, err := rows.Values()
		if err != nil || encoder.Encode(values) != nil {
			return "", errors.New("query_digest_failed")
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *OperationsService) pgPassFile() (string, func(), error) {
	if err := ensurePrivateDirectory(s.config.DataDir); err != nil {
		return "", func() {}, err
	}
	file, err := os.CreateTemp(s.config.DataDir, ".pgpass-*")
	if err != nil {
		return "", func() {}, errors.New("database_passfile_create_failed")
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, errors.New("database_passfile_permission_failed")
	}
	escape := func(value string) string {
		value = strings.ReplaceAll(value, `\`, `\\`)
		return strings.ReplaceAll(value, ":", `\:`)
	}
	line := fmt.Sprintf("%s:%d:*:%s:%s\n", escape(s.config.Database.Host), s.config.Database.Port, escape(s.config.Database.User), escape(string(s.secrets.DatabasePassword)))
	_, writeErr := file.WriteString(line)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		cleanup()
		return "", func() {}, errors.New("database_passfile_write_failed")
	}
	return path, cleanup, nil
}

func (s *OperationsService) tableCounts(ctx context.Context, pool *pgxpool.Pool) (map[string]int64, error) {
	counts := map[string]int64{}
	groups := make([]string, 0, len(backupTableGroups))
	for group := range backupTableGroups {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	for _, group := range groups {
		for _, table := range backupTableGroups[group] {
			var count int64
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+quoteIdentifier(table)).Scan(&count); err != nil {
				return nil, err
			}
			counts[table] = count
		}
	}
	return counts, nil
}

func (s *OperationsService) registryVersions(ctx context.Context) (string, []string, error) {
	return registryVersionsForPool(ctx, s.pool)
}

func ensurePrivateDirectory(path string) error {
	if !filepath.IsAbs(path) || path == "/" {
		return errors.New("private_directory_invalid")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return errors.New("private_directory_create_failed")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private_directory_unsafe")
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(path, 0o700); err != nil {
			return errors.New("private_directory_permission_failed")
		}
	}
	return nil
}

func fileSHA256(path string, maximum int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("artifact_open_failed")
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil || written > maximum {
		return "", errors.New("artifact_hash_failed")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writePrivateJSON(path string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("manifest_encode_failed")
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("manifest_create_failed")
	}
	_, writeErr := file.Write(append(encoded, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return errors.New("manifest_write_failed")
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return errors.New("manifest_publish_failed")
	}
	return nil
}

func readStrictJSONFile(path string, maximum int64, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return errors.New("manifest_read_failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func equalCounts(left, right map[string]int64) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func verifyMigrationLedgers(ctx context.Context, pool *pgxpool.Pool) error {
	for _, ledger := range []string{"schema_migrations", "integrity_schema_migrations", "runtime_schema_migrations"} {
		var invalid int64
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+quoteIdentifier(ledger)+" WHERE version='' OR checksum_sha256 !~ '^[0-9a-f]{64}$'").Scan(&invalid); err != nil || invalid != 0 {
			return errors.New("restore_migration_ledger_invalid")
		}
	}
	return nil
}
