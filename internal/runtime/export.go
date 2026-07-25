package runtime

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/privacy"
)

const PortableExportVersion = "kansoku.privacy-safe-ndjson/1"

type portableManifest struct {
	RecordType          string   `json:"record_type"`
	Format              string   `json:"format"`
	PayloadSHA256       string   `json:"payload_sha256"`
	RecordCount         int64    `json:"record_count"`
	FormulaVersions     []string `json:"formula_version_references"`
	AdapterVersions     []string `json:"adapter_schema_version_references"`
	PrivacyPolicySHA256 string   `json:"privacy_policy_sha256"`
}

type portableDimensions struct {
	DeviceID            string `json:"device_id"`
	AgentInstallationID string `json:"agent_installation_id"`
	AgentID             string `json:"agent_id"`
	SurfaceID           string `json:"surface_id"`
	ProjectID           string `json:"project_id"`
	SessionID           string `json:"session_id"`
	TurnID              string `json:"turn_id"`
	ComponentID         string `json:"component_id"`
	AdapterVersionID    string `json:"adapter_version_id"`
	AdapterID           string `json:"adapter_id"`
	AdapterVersion      string `json:"adapter_version"`
	SourceInstanceID    string `json:"source_instance_id"`
	SourceKind          string `json:"source_kind"`
}

type portableFact struct {
	EventID             string    `json:"event_id"`
	FactKey             string    `json:"fact_key"`
	EventType           string    `json:"event_type"`
	ObservedAt          time.Time `json:"observed_at"`
	IngestedAt          time.Time `json:"ingested_at"`
	TimestampQuality    string    `json:"timestamp_quality"`
	SourceInstanceID    string    `json:"source_instance_id"`
	SourceNativeEventID string    `json:"source_native_event_id"`
	Sequence            int64     `json:"sequence"`
	AgentInstallationID string    `json:"agent_installation_id"`
	SurfaceID           string    `json:"surface_id"`
	ProjectID           string    `json:"project_id"`
	SessionID           string    `json:"session_id"`
	TurnID              string    `json:"turn_id"`
	ComponentID         string    `json:"component_id"`
	DurationMS          *int64    `json:"duration_ms"`
	Success             *bool     `json:"success"`
	Count               *int64    `json:"count"`
	ValueState          string    `json:"value_state"`
	Outcome             string    `json:"outcome"`
	CorrelationStatus   string    `json:"correlation_status"`
}

type portableEvidence struct {
	EvidenceID        string    `json:"evidence_id"`
	EventID           string    `json:"event_id"`
	ObservedAt        time.Time `json:"observed_at"`
	SourceInstanceID  string    `json:"source_instance_id"`
	Tier              string    `json:"tier"`
	Confidence        float64   `json:"confidence"`
	Completeness      string    `json:"completeness"`
	ReplayCount       int64     `json:"replay_count"`
	FirstSeenAt       time.Time `json:"first_seen_at"`
	LastSeenAt        time.Time `json:"last_seen_at"`
	SanitizerVersion  string    `json:"sanitizer_version"`
	PrivacyContractID string    `json:"privacy_contract_sha256"`
	AssertEventType   string    `json:"assertion_event_type"`
	AssertOutcome     string    `json:"assertion_outcome"`
	AssertValueState  string    `json:"assertion_value_state"`
}

type portableRecord struct {
	RecordType string             `json:"record_type"`
	Dimensions portableDimensions `json:"inventory_ids"`
	Fact       portableFact       `json:"normalized_safe_fact"`
	Evidence   []portableEvidence `json:"safe_evidence"`
}

type portableRollup struct {
	RecordType           string    `json:"record_type"`
	Granularity          string    `json:"granularity"`
	MetricFamily         string    `json:"metric_family"`
	BucketStart          time.Time `json:"bucket_start"`
	DimensionScope       string    `json:"dimension_scope"`
	FormulaVersion       string    `json:"formula_version"`
	EventCount           int64     `json:"event_count"`
	UnknownCount         int64     `json:"unknown_count"`
	CompletenessDuration int64     `json:"completeness_duration_ms"`
	ValueNumeric         *float64  `json:"value_numeric"`
	ValueP50             *float64  `json:"value_p50"`
	ValueP90             *float64  `json:"value_p90"`
	ValueP95             *float64  `json:"value_p95"`
	ValueP99             *float64  `json:"value_p99"`
	ComputedAt           time.Time `json:"computed_at"`
}

type portableCompleteness struct {
	RecordType     string         `json:"record_type"`
	IntervalID     string         `json:"completeness_interval_id"`
	DimensionScope map[string]any `json:"dimension_scope"`
	IntervalStart  time.Time      `json:"interval_start"`
	IntervalEnd    time.Time      `json:"interval_end"`
	Status         string         `json:"status"`
}

type portableRecordHeader struct {
	RecordType string `json:"record_type"`
}

type ExportResult struct {
	ExportID      string `json:"export_id"`
	PayloadSHA256 string `json:"payload_sha256"`
	RecordCount   int64  `json:"record_count"`
}

type ImportResult struct {
	ImportID       string `json:"import_id"`
	ImportedCount  int64  `json:"imported_count"`
	DuplicateCount int64  `json:"duplicate_count"`
}

func (s *OperationsService) Export(ctx context.Context, _ ExportRequest) (any, error) {
	exportID, err := newOpaqueID("export")
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(s.config.BackupDir); err != nil {
		return nil, err
	}
	payload, err := os.CreateTemp(s.config.BackupDir, ".export-payload-*")
	if err != nil {
		return nil, errors.New("export_create_failed")
	}
	payloadPath := payload.Name()
	defer func() { _ = os.Remove(payloadPath) }()
	if err := payload.Chmod(0o600); err != nil {
		_ = payload.Close()
		return nil, errors.New("export_permission_failed")
	}
	hash := sha256.New()
	writer := io.MultiWriter(payload, hash)
	encoder := json.NewEncoder(writer)
	rows, err := s.pool.Query(ctx, portableExportSQL)
	if err != nil {
		_ = payload.Close()
		return nil, errors.New("export_query_failed")
	}
	var current *portableRecord
	var count int64
	flush := func() error {
		if current == nil {
			return nil
		}
		if err := encoder.Encode(current); err != nil {
			return err
		}
		count++
		return nil
	}
	for rows.Next() {
		record, evidence, err := scanPortableRow(rows)
		if err != nil {
			rows.Close()
			_ = payload.Close()
			return nil, errors.New("export_row_invalid")
		}
		if current == nil || current.Fact.EventID != record.Fact.EventID || !current.Fact.ObservedAt.Equal(record.Fact.ObservedAt) {
			if err := flush(); err != nil {
				rows.Close()
				_ = payload.Close()
				return nil, errors.New("export_encode_failed")
			}
			current = &record
		}
		current.Evidence = append(current.Evidence, evidence)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		_ = payload.Close()
		return nil, errors.New("export_query_failed")
	}
	if err := flush(); err != nil {
		_ = payload.Close()
		return nil, errors.New("export_write_failed")
	}
	rollupCount, err := exportPortableRollups(ctx, s.pool, encoder)
	if err != nil {
		_ = payload.Close()
		return nil, err
	}
	completenessCount, err := exportPortableCompleteness(ctx, s.pool, encoder)
	if err != nil {
		_ = payload.Close()
		return nil, err
	}
	count += rollupCount + completenessCount
	if payload.Sync() != nil || payload.Close() != nil {
		return nil, errors.New("export_write_failed")
	}
	formulas, adapters, err := s.exportRegistryVersions(ctx)
	if err != nil {
		return nil, err
	}
	payloadHash := hex.EncodeToString(hash.Sum(nil))
	manifest := portableManifest{
		RecordType: "manifest", Format: PortableExportVersion,
		PayloadSHA256: payloadHash, RecordCount: count,
		FormulaVersions: formulas, AdapterVersions: adapters,
		PrivacyPolicySHA256: privacy.PrivacyContractSemanticSHA256,
	}
	finalPath := filepath.Join(s.config.BackupDir, exportID+".ndjson")
	final, err := os.OpenFile(finalPath+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, errors.New("export_create_failed")
	}
	if err := json.NewEncoder(final).Encode(manifest); err != nil {
		_ = final.Close()
		_ = os.Remove(finalPath + ".tmp")
		return nil, errors.New("export_manifest_write_failed")
	}
	source, err := os.Open(payloadPath)
	if err != nil {
		_ = final.Close()
		_ = os.Remove(finalPath + ".tmp")
		return nil, errors.New("export_payload_open_failed")
	}
	_, copyErr := io.Copy(final, source)
	closeSourceErr := source.Close()
	syncErr := final.Sync()
	closeErr := final.Close()
	if copyErr != nil || closeSourceErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(finalPath + ".tmp")
		return nil, errors.New("export_publish_failed")
	}
	if err := os.Rename(finalPath+".tmp", finalPath); err != nil {
		_ = os.Remove(finalPath + ".tmp")
		return nil, errors.New("export_publish_failed")
	}
	return ExportResult{ExportID: exportID, PayloadSHA256: payloadHash, RecordCount: count}, nil
}

func exportPortableRollups(ctx context.Context, pool interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, encoder *json.Encoder) (int64, error) {
	var count int64
	for _, item := range []struct {
		table       string
		granularity string
	}{
		{"metric_rollups_hourly", "hourly"},
		{"metric_rollups_daily", "daily"},
	} {
		rows, err := pool.Query(ctx, `
			SELECT metric_family,bucket_start,dimension_scope,formula_version,
			       event_count,unknown_count,completeness_duration_ms,
			       value_numeric,value_p50,value_p90,value_p95,value_p99,computed_at
			FROM `+quoteIdentifier(item.table)+`
			ORDER BY metric_family,bucket_start,dimension_scope
		`)
		if err != nil {
			return count, errors.New("export_rollup_query_failed")
		}
		for rows.Next() {
			record := portableRollup{RecordType: "rollup", Granularity: item.granularity}
			if err := rows.Scan(
				&record.MetricFamily, &record.BucketStart, &record.DimensionScope,
				&record.FormulaVersion, &record.EventCount, &record.UnknownCount,
				&record.CompletenessDuration, &record.ValueNumeric, &record.ValueP50,
				&record.ValueP90, &record.ValueP95, &record.ValueP99, &record.ComputedAt,
			); err != nil || !validPortableRollup(record) || encoder.Encode(record) != nil {
				rows.Close()
				return count, errors.New("export_rollup_invalid")
			}
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return count, errors.New("export_rollup_query_failed")
		}
	}
	return count, nil
}

func exportPortableCompleteness(ctx context.Context, pool interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, encoder *json.Encoder) (int64, error) {
	rows, err := pool.Query(ctx, `
		SELECT completeness_interval_id,dimension_scope,interval_start,interval_end,status
		FROM completeness_intervals
		ORDER BY completeness_interval_id
	`)
	if err != nil {
		return 0, errors.New("export_completeness_query_failed")
	}
	defer rows.Close()
	var count int64
	for rows.Next() {
		record := portableCompleteness{RecordType: "completeness"}
		var scope []byte
		if err := rows.Scan(
			&record.IntervalID, &scope, &record.IntervalStart, &record.IntervalEnd, &record.Status,
		); err != nil || strictJSONLine(scope, &record.DimensionScope) != nil ||
			!validPortableCompleteness(record) || encoder.Encode(record) != nil {
			return count, errors.New("export_completeness_invalid")
		}
		count++
	}
	if rows.Err() != nil {
		return count, errors.New("export_completeness_query_failed")
	}
	return count, nil
}

func (s *OperationsService) Import(ctx context.Context, request ImportRequest) (any, error) {
	if !safeArtifactID.MatchString(request.ExportID) || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 {
		return nil, errors.New("invalid_import_request")
	}
	path := filepath.Join(s.config.BackupDir, request.ExportID+".ndjson")
	manifest, months, err := s.validatePortableExport(ctx, path)
	if err != nil {
		return nil, err
	}
	idempotencyHash := sha256.Sum256([]byte(request.IdempotencyKey))
	var existing ImportResult
	err = s.pool.QueryRow(ctx, `
		SELECT import_id, imported_count, duplicate_count
		FROM runtime_import_receipts WHERE idempotency_key_sha256=$1
	`, hex.EncodeToString(idempotencyHash[:])).Scan(&existing.ImportID, &existing.ImportedCount, &existing.DuplicateCount)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("import_receipt_lookup_failed")
	}
	for month := range months {
		at, parseErr := time.Parse("2006-01", month)
		if parseErr != nil {
			return nil, errors.New("import_month_invalid")
		}
		if err := dataplatform.EnsurePartition(ctx, s.pool, "events", at); err != nil {
			return nil, errors.New("import_partition_failed")
		}
		if err := dataplatform.EnsurePartition(ctx, s.pool, "event_evidence", at); err != nil {
			return nil, errors.New("import_partition_failed")
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("import_open_failed")
	}
	defer file.Close()
	scanner := newPortableScanner(file)
	if !scanner.Scan() {
		return nil, errors.New("import_manifest_missing")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, errors.New("import_transaction_failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var imported, duplicates int64
	for scanner.Scan() {
		line := scanner.Bytes()
		recordType, err := peekPortableRecordType(line)
		if err != nil {
			return nil, errors.New("import_record_invalid")
		}
		var inserted bool
		switch recordType {
		case "fact":
			var record portableRecord
			if err := strictJSONLine(line, &record); err != nil {
				return nil, errors.New("import_record_invalid")
			}
			inserted, err = importPortableRecord(ctx, tx, record)
		case "rollup":
			var record portableRollup
			if err := strictJSONLine(line, &record); err != nil {
				return nil, errors.New("import_record_invalid")
			}
			inserted, err = importPortableRollup(ctx, tx, record)
		case "completeness":
			var record portableCompleteness
			if err := strictJSONLine(line, &record); err != nil {
				return nil, errors.New("import_record_invalid")
			}
			inserted, err = importPortableCompleteness(ctx, tx, record)
		default:
			return nil, errors.New("import_record_invalid")
		}
		if err != nil {
			return nil, errors.New("import_write_failed")
		}
		if inserted {
			imported++
		} else {
			duplicates++
		}
	}
	if scanner.Err() != nil || imported+duplicates != manifest.RecordCount {
		return nil, errors.New("import_record_count_mismatch")
	}
	importID, err := newOpaqueID("import")
	if err != nil {
		return nil, err
	}
	manifestHash := sha256.Sum256([]byte(manifest.PayloadSHA256 + "\x00" + PortableExportVersion))
	if _, err := tx.Exec(ctx, `
		INSERT INTO runtime_import_receipts
			(import_id, idempotency_key_sha256, manifest_sha256,
			 imported_count, duplicate_count, completed_at)
		VALUES ($1,$2,$3,$4,$5,$6)
	`, importID, hex.EncodeToString(idempotencyHash[:]), hex.EncodeToString(manifestHash[:]), imported, duplicates, s.now().UTC()); err != nil {
		return nil, errors.New("import_receipt_failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, errors.New("import_commit_failed")
	}
	return ImportResult{ImportID: importID, ImportedCount: imported, DuplicateCount: duplicates}, nil
}

func (s *OperationsService) validatePortableExport(ctx context.Context, path string) (portableManifest, map[string]bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return portableManifest{}, nil, errors.New("import_open_failed")
	}
	defer file.Close()
	scanner := newPortableScanner(file)
	if !scanner.Scan() {
		return portableManifest{}, nil, errors.New("import_manifest_missing")
	}
	var manifest portableManifest
	if err := strictJSONLine(scanner.Bytes(), &manifest); err != nil ||
		manifest.RecordType != "manifest" || manifest.Format != PortableExportVersion ||
		manifest.PrivacyPolicySHA256 != privacy.PrivacyContractSemanticSHA256 ||
		manifest.RecordCount < 0 || manifest.RecordCount > 100_000_000 {
		return portableManifest{}, nil, errors.New("import_manifest_invalid")
	}
	if err := s.validatePortableRegistries(ctx, manifest); err != nil {
		return portableManifest{}, nil, err
	}
	hash := sha256.New()
	months := map[string]bool{}
	manifestAdapters := map[string]bool{}
	for _, adapterVersion := range manifest.AdapterVersions {
		manifestAdapters[adapterVersion] = true
	}
	var count int64
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		_, _ = hash.Write(line)
		_, _ = hash.Write([]byte{'\n'})
		recordType, err := peekPortableRecordType(line)
		if err != nil {
			return portableManifest{}, nil, errors.New("import_record_invalid")
		}
		switch recordType {
		case "fact":
			var record portableRecord
			if err := strictJSONLine(line, &record); err != nil || !validPortableRecord(record) ||
				!manifestAdapters[record.Dimensions.AdapterVersionID] {
				return portableManifest{}, nil, errors.New("import_record_invalid")
			}
			months[record.Fact.ObservedAt.UTC().Format("2006-01")] = true
		case "rollup":
			var record portableRollup
			if err := strictJSONLine(line, &record); err != nil || !validPortableRollup(record) {
				return portableManifest{}, nil, errors.New("import_record_invalid")
			}
		case "completeness":
			var record portableCompleteness
			if err := strictJSONLine(line, &record); err != nil || !validPortableCompleteness(record) {
				return portableManifest{}, nil, errors.New("import_record_invalid")
			}
		default:
			return portableManifest{}, nil, errors.New("import_record_invalid")
		}
		count++
	}
	if scanner.Err() != nil || count != manifest.RecordCount ||
		hex.EncodeToString(hash.Sum(nil)) != manifest.PayloadSHA256 {
		return portableManifest{}, nil, errors.New("import_checksum_or_count_mismatch")
	}
	return manifest, months, nil
}

func (s *OperationsService) validatePortableRegistries(ctx context.Context, manifest portableManifest) error {
	formulas, adapters, err := s.exportRegistryVersions(ctx)
	if err != nil {
		return errors.New("import_registry_lookup_failed")
	}
	formulaSet, adapterSet := map[string]bool{}, map[string]bool{}
	for _, value := range formulas {
		formulaSet[value] = true
	}
	for _, value := range adapters {
		adapterSet[value] = true
	}
	for _, value := range manifest.FormulaVersions {
		if !formulaSet[value] {
			return errors.New("unknown_formula_version")
		}
	}
	for _, value := range manifest.AdapterVersions {
		if !adapterSet[value] {
			return errors.New("unknown_adapter_schema_version")
		}
	}
	return nil
}

func (s *OperationsService) exportRegistryVersions(ctx context.Context) ([]string, []string, error) {
	var formulas, adapters []string
	rows, err := s.pool.Query(ctx, `SELECT formula_id || '/' || version::text FROM formula_versions ORDER BY formula_id, version`)
	if err != nil {
		return nil, nil, errors.New("export_registry_query_failed")
	}
	for rows.Next() {
		var value string
		if rows.Scan(&value) != nil {
			rows.Close()
			return nil, nil, errors.New("export_registry_query_failed")
		}
		formulas = append(formulas, value)
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT adapter_version_id FROM adapter_versions ORDER BY adapter_version_id`)
	if err != nil {
		return nil, nil, errors.New("export_registry_query_failed")
	}
	for rows.Next() {
		var value string
		if rows.Scan(&value) != nil {
			rows.Close()
			return nil, nil, errors.New("export_registry_query_failed")
		}
		adapters = append(adapters, value)
	}
	rows.Close()
	return formulas, adapters, rows.Err()
}

const portableExportSQL = `
	SELECT
		e.event_id, e.fact_key, e.event_type, e.observed_at, e.ingested_at,
		e.timestamp_quality, e.source_instance_id, e.source_native_event_id,
		e.sequence, COALESCE(e.agent_installation_id,''), COALESCE(e.surface_id,''),
		COALESCE(e.project_id,''), COALESCE(e.session_id,''), COALESCE(e.turn_id,''),
		COALESCE(e.component_id,''), e.duration_ms, e.success, e.count,
		e.value_state, e.outcome, e.correlation_status,
		ev.evidence_id, ev.event_id, ev.observed_at, ev.source_instance_id,
		ev.tier, ev.confidence, ev.completeness, ev.replay_count,
		ev.first_seen_at, ev.last_seen_at, ev.sanitizer_version,
		ev.privacy_contract_sha256, ev.assertion_event_type,
		ev.assertion_outcome, ev.assertion_value_state,
		COALESCE(ai.device_id,''), av.adapter_version_id, av.adapter_id,
		av.version, si.source_kind
	FROM events e
	JOIN event_evidence ev
	  ON ev.event_id=e.event_id AND ev.observed_at=e.observed_at
	JOIN source_instances si ON si.source_instance_id=e.source_instance_id
	JOIN adapter_versions av ON av.adapter_version_id=si.adapter_version_id
	LEFT JOIN agent_installations ai
	  ON ai.agent_installation_id=e.agent_installation_id
	ORDER BY e.event_id, e.observed_at, ev.evidence_id
`

func scanPortableRow(row pgx.Row) (portableRecord, portableEvidence, error) {
	var record portableRecord
	record.RecordType = "fact"
	var evidence portableEvidence
	if err := row.Scan(
		&record.Fact.EventID, &record.Fact.FactKey, &record.Fact.EventType,
		&record.Fact.ObservedAt, &record.Fact.IngestedAt, &record.Fact.TimestampQuality,
		&record.Fact.SourceInstanceID, &record.Fact.SourceNativeEventID,
		&record.Fact.Sequence, &record.Fact.AgentInstallationID, &record.Fact.SurfaceID,
		&record.Fact.ProjectID, &record.Fact.SessionID, &record.Fact.TurnID,
		&record.Fact.ComponentID, &record.Fact.DurationMS, &record.Fact.Success,
		&record.Fact.Count, &record.Fact.ValueState, &record.Fact.Outcome,
		&record.Fact.CorrelationStatus,
		&evidence.EvidenceID, &evidence.EventID, &evidence.ObservedAt,
		&evidence.SourceInstanceID, &evidence.Tier, &evidence.Confidence,
		&evidence.Completeness, &evidence.ReplayCount, &evidence.FirstSeenAt,
		&evidence.LastSeenAt, &evidence.SanitizerVersion, &evidence.PrivacyContractID,
		&evidence.AssertEventType, &evidence.AssertOutcome, &evidence.AssertValueState,
		&record.Dimensions.DeviceID, &record.Dimensions.AdapterVersionID,
		&record.Dimensions.AdapterID, &record.Dimensions.AdapterVersion,
		&record.Dimensions.SourceKind,
	); err != nil {
		return portableRecord{}, portableEvidence{}, err
	}
	record.Dimensions.AgentInstallationID = record.Fact.AgentInstallationID
	record.Dimensions.SurfaceID = record.Fact.SurfaceID
	record.Dimensions.ProjectID = record.Fact.ProjectID
	record.Dimensions.SessionID = record.Fact.SessionID
	record.Dimensions.TurnID = record.Fact.TurnID
	record.Dimensions.ComponentID = record.Fact.ComponentID
	record.Dimensions.SourceInstanceID = record.Fact.SourceInstanceID
	record.Dimensions.AgentID = record.Dimensions.AdapterID
	return record, evidence, nil
}

func newPortableScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	return scanner
}

func strictJSONLine(line []byte, destination any) error {
	decoder := json.NewDecoder(bytesReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func bytesReader(value []byte) io.Reader {
	return &byteReader{value: value}
}

type byteReader struct {
	value []byte
}

func (r *byteReader) Read(destination []byte) (int, error) {
	if len(r.value) == 0 {
		return 0, io.EOF
	}
	n := copy(destination, r.value)
	r.value = r.value[n:]
	return n, nil
}

func validPortableRecord(record portableRecord) bool {
	if record.RecordType != "fact" || record.Fact.EventID == "" ||
		record.Fact.EventID != firstEvidenceEventID(record.Evidence) ||
		record.Fact.SourceInstanceID != record.Dimensions.SourceInstanceID ||
		record.Fact.ObservedAt.IsZero() || len(record.Evidence) == 0 ||
		record.Dimensions.AdapterVersionID == "" || record.Dimensions.AdapterID == "" ||
		record.Dimensions.AdapterVersion == "" || record.Dimensions.SourceKind == "" {
		return false
	}
	for _, evidence := range record.Evidence {
		if evidence.EventID != record.Fact.EventID ||
			!evidence.ObservedAt.Equal(record.Fact.ObservedAt) ||
			evidence.SourceInstanceID != record.Fact.SourceInstanceID ||
			evidence.PrivacyContractID != privacy.PrivacyContractSemanticSHA256 {
			return false
		}
	}
	return true
}

func validPortableRollup(record portableRollup) bool {
	if record.RecordType != "rollup" ||
		(record.Granularity != "hourly" && record.Granularity != "daily") ||
		record.MetricFamily == "" || record.DimensionScope == "" ||
		record.FormulaVersion == "" || record.BucketStart.IsZero() ||
		record.ComputedAt.IsZero() || record.EventCount < 0 ||
		record.UnknownCount < 0 || record.CompletenessDuration < 0 {
		return false
	}
	return true
}

func validPortableCompleteness(record portableCompleteness) bool {
	if record.RecordType != "completeness" || record.IntervalID == "" ||
		record.Status == "" || record.DimensionScope == nil ||
		record.IntervalStart.IsZero() || record.IntervalEnd.IsZero() ||
		!record.IntervalStart.Before(record.IntervalEnd) {
		return false
	}
	return true
}

func firstEvidenceEventID(evidence []portableEvidence) string {
	if len(evidence) == 0 {
		return ""
	}
	return evidence[0].EventID
}

func importPortableRecord(ctx context.Context, tx pgx.Tx, record portableRecord) (bool, error) {
	d := record.Dimensions
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO devices (device_id) VALUES ($1) ON CONFLICT DO NOTHING`, []any{d.DeviceID}},
		{`INSERT INTO agent_installations (agent_installation_id,device_id,agent_id) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, []any{d.AgentInstallationID, d.DeviceID, d.AgentID}},
		{`INSERT INTO agent_surfaces (surface_id,agent_installation_id,surface_kind) VALUES ($1,$2,'cli') ON CONFLICT DO NOTHING`, []any{d.SurfaceID, d.AgentInstallationID}},
		{`INSERT INTO projects (project_id) VALUES ($1) ON CONFLICT DO NOTHING`, []any{d.ProjectID}},
		{`INSERT INTO sessions (session_id,project_id,started_at) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, []any{d.SessionID, d.ProjectID, record.Fact.ObservedAt}},
		{`INSERT INTO turns (turn_id,session_id,started_at) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, []any{d.TurnID, d.SessionID, record.Fact.ObservedAt}},
		{`INSERT INTO components (component_id,kind) VALUES ($1,'skill') ON CONFLICT DO NOTHING`, []any{d.ComponentID}},
		{`INSERT INTO adapter_versions (adapter_version_id,adapter_id,version) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, []any{d.AdapterVersionID, d.AdapterID, d.AdapterVersion}},
		{`INSERT INTO source_instances (source_instance_id,adapter_version_id,source_kind) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, []any{d.SourceInstanceID, d.AdapterVersionID, d.SourceKind}},
	}
	for _, statement := range statements {
		if len(statement.args) > 0 {
			if value, ok := statement.args[0].(string); ok && value == "" {
				continue
			}
		}
		if _, err := tx.Exec(ctx, statement.query, statement.args...); err != nil {
			return false, err
		}
	}
	f := record.Fact
	tag, err := tx.Exec(ctx, `
		INSERT INTO events (
			event_id,fact_key,event_type,observed_at,ingested_at,timestamp_quality,
			source_instance_id,source_native_event_id,sequence,agent_installation_id,
			surface_id,project_id,session_id,turn_id,component_id,duration_ms,success,
			count,value_state,outcome,correlation_status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),NULLIF($11,''),
			NULLIF($12,''),NULLIF($13,''),NULLIF($14,''),NULLIF($15,''),$16,$17,$18,$19,$20,$21)
		ON CONFLICT (source_instance_id,source_native_event_id,observed_at) DO NOTHING
	`, f.EventID, f.FactKey, f.EventType, f.ObservedAt, f.IngestedAt, f.TimestampQuality,
		f.SourceInstanceID, f.SourceNativeEventID, f.Sequence, f.AgentInstallationID,
		f.SurfaceID, f.ProjectID, f.SessionID, f.TurnID, f.ComponentID, f.DurationMS,
		f.Success, f.Count, f.ValueState, f.Outcome, f.CorrelationStatus)
	if err != nil {
		return false, err
	}
	for _, e := range record.Evidence {
		if _, err := tx.Exec(ctx, `
			INSERT INTO event_evidence (
				evidence_id,event_id,observed_at,source_instance_id,tier,confidence,
				completeness,replay_count,first_seen_at,last_seen_at,sanitizer_version,
				privacy_contract_sha256,assertion_event_type,assertion_outcome,
				assertion_value_state
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			ON CONFLICT (evidence_id,observed_at) DO UPDATE SET
				replay_count=GREATEST(event_evidence.replay_count,EXCLUDED.replay_count),
				last_seen_at=GREATEST(event_evidence.last_seen_at,EXCLUDED.last_seen_at)
		`, e.EvidenceID, e.EventID, e.ObservedAt, e.SourceInstanceID, e.Tier,
			e.Confidence, e.Completeness, e.ReplayCount, e.FirstSeenAt, e.LastSeenAt,
			e.SanitizerVersion, e.PrivacyContractID, e.AssertEventType,
			e.AssertOutcome, e.AssertValueState); err != nil {
			return false, err
		}
	}
	return tag.RowsAffected() == 1, nil
}

func peekPortableRecordType(line []byte) (string, error) {
	var header portableRecordHeader
	if err := json.Unmarshal(line, &header); err != nil || header.RecordType == "" {
		return "", errors.New("import_record_type_missing")
	}
	return header.RecordType, nil
}

func importPortableRollup(ctx context.Context, tx pgx.Tx, record portableRollup) (bool, error) {
	table := "metric_rollups_hourly"
	if record.Granularity == "daily" {
		table = "metric_rollups_daily"
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO `+quoteIdentifier(table)+` (
			metric_family,bucket_start,dimension_scope,formula_version,
			event_count,unknown_count,completeness_duration_ms,
			value_numeric,value_p50,value_p90,value_p95,value_p99,computed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (metric_family,bucket_start,dimension_scope) DO NOTHING
	`, record.MetricFamily, record.BucketStart, record.DimensionScope, record.FormulaVersion,
		record.EventCount, record.UnknownCount, record.CompletenessDuration,
		record.ValueNumeric, record.ValueP50, record.ValueP90, record.ValueP95, record.ValueP99, record.ComputedAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func importPortableCompleteness(ctx context.Context, tx pgx.Tx, record portableCompleteness) (bool, error) {
	scope, err := json.Marshal(record.DimensionScope)
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO completeness_intervals (
			completeness_interval_id,dimension_scope,interval_start,interval_end,status
		) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (completeness_interval_id) DO NOTHING
	`, record.IntervalID, scope, record.IntervalStart, record.IntervalEnd, record.Status)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
