package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/observability"
)

const mirrorReconciliationVersion = "kansoku.mirror-reconciliation/1"

type mirrorFactLineage struct {
	EventID       string
	NativeEventID string
	Sequence      uint64
}

type mirrorEvidenceLineage struct {
	EventID string
	Tier    string
}

type MirrorReconciliationReport struct {
	Version                            string         `json:"version"`
	ReconciliationID                   string         `json:"reconciliation_id"`
	MirrorSHA256                       string         `json:"mirror_sha256"`
	MirrorBytes                        int64          `json:"mirror_bytes"`
	MirrorRevision                     uint64         `json:"mirror_revision"`
	MirrorFactCount                    int            `json:"mirror_fact_count"`
	DatabaseFactCount                  int            `json:"database_fact_count"`
	MirrorOnlyFactCount                int            `json:"mirror_only_fact_count"`
	DatabaseOnlyFactCount              int            `json:"database_only_fact_count"`
	MirrorEvidenceCount                int            `json:"mirror_evidence_count"`
	DatabaseEvidenceCount              int            `json:"database_evidence_count"`
	MirrorOnlyEvidenceCount            int            `json:"mirror_only_evidence_count"`
	DatabaseOnlyEvidenceCount          int            `json:"database_only_evidence_count"`
	LineageMismatchCount               int            `json:"lineage_mismatch_count"`
	CheckpointCount                    int            `json:"checkpoint_count"`
	WatermarkCount                     int            `json:"watermark_count"`
	QuarantineFingerprintCount         int            `json:"quarantine_fingerprint_count"`
	DatabaseQuarantineFingerprintCount int            `json:"database_quarantine_fingerprint_count"`
	Status                             string         `json:"status"`
	BackupArtifactID                   string         `json:"backup_artifact_id"`
	ArchiveArtifactID                  string         `json:"archive_artifact_id,omitempty"`
	Exclusions                         map[string]any `json:"exclusions"`
	ReconciledAt                       time.Time      `json:"reconciled_at"`
}

// ReconcileAndArchiveLegacyMirror verifies the legacy compatibility mirror
// against PostgreSQL, creates an immutable private backup, writes a
// metadata-only reconciliation report and only then atomically archives the
// original. It never deletes the backup or archive.
func ReconcileAndArchiveLegacyMirror(
	ctx context.Context,
	pool *pgxpool.Pool,
	dataDir string,
	maxBytes int64,
) (*MirrorReconciliationReport, observability.DurableState, error) {
	empty := observability.DurableState{}
	legacyPath := filepath.Join(dataDir, "mirror", "state.json")
	info, err := os.Stat(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, empty, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, empty, errors.New("legacy_mirror_preflight_failed")
	}
	sum, err := sha256File(legacyPath)
	if err != nil {
		return nil, empty, errors.New("legacy_mirror_checksum_failed")
	}
	short := sum[:16]
	backupID := "legacy-backup-" + short
	archiveID := "legacy-archive-" + short
	reconciliationID := "mrr_" + short
	backupDir := filepath.Join(dataDir, "mirror", "legacy-backups")
	archiveDir := filepath.Join(dataDir, "mirror", "legacy-archive")
	if err := ensurePrivateDirectory(backupDir); err != nil {
		return nil, empty, errors.New("legacy_mirror_backup_directory_failed")
	}
	if err := ensurePrivateDirectory(archiveDir); err != nil {
		return nil, empty, errors.New("legacy_mirror_archive_directory_failed")
	}
	backupPath := filepath.Join(backupDir, backupID+".json")
	if err := copyOrVerifyPrivateFile(legacyPath, backupPath, sum, info.Size()); err != nil {
		return nil, empty, errors.New("legacy_mirror_backup_failed")
	}
	store, err := observability.OpenFileStore(legacyPath, maxBytes)
	if err != nil {
		return nil, empty, errors.New("legacy_mirror_validation_failed")
	}
	state := store.Snapshot()
	report, err := reconcileMirrorState(ctx, pool, state)
	if err != nil {
		return nil, empty, err
	}
	report.Version = mirrorReconciliationVersion
	report.ReconciliationID = reconciliationID
	report.MirrorSHA256 = sum
	report.MirrorBytes = info.Size()
	report.MirrorRevision = state.Revision
	report.BackupArtifactID = backupID
	report.ArchiveArtifactID = archiveID
	report.ReconciledAt = time.Now().UTC()
	if report.MirrorOnlyFactCount != 0 || report.MirrorOnlyEvidenceCount != 0 ||
		report.LineageMismatchCount != 0 {
		report.Status = "blocked"
		report.ArchiveArtifactID = ""
		if persistErr := persistMirrorReconciliation(ctx, pool, report); persistErr != nil {
			return nil, empty, persistErr
		}
		return report, state, errors.New("legacy_mirror_reconciliation_blocked")
	}
	report.Status = "reconciled"
	if err := persistMirrorReconciliation(ctx, pool, report); err != nil {
		return nil, empty, err
	}
	reportPath := filepath.Join(archiveDir, reconciliationID+".json")
	if err := writePrivateJSONAtomic(reportPath, report); err != nil {
		return nil, empty, errors.New("legacy_mirror_report_write_failed")
	}
	archivePath := filepath.Join(archiveDir, archiveID+".json")
	if _, statErr := os.Stat(archivePath); errors.Is(statErr, os.ErrNotExist) {
		if err := os.Rename(legacyPath, archivePath); err != nil {
			return nil, empty, errors.New("legacy_mirror_archive_failed")
		}
		if err := syncDirectory(archiveDir); err != nil {
			return nil, empty, errors.New("legacy_mirror_archive_sync_failed")
		}
		if err := syncDirectory(filepath.Dir(legacyPath)); err != nil {
			return nil, empty, errors.New("legacy_mirror_archive_sync_failed")
		}
	} else if statErr != nil {
		return nil, empty, errors.New("legacy_mirror_archive_preflight_failed")
	}
	return report, state, nil
}

func reconcileMirrorState(ctx context.Context, pool *pgxpool.Pool, state observability.DurableState) (*MirrorReconciliationReport, error) {
	if pool == nil {
		return nil, errors.New("legacy_mirror_database_required")
	}
	dbFacts := map[string]mirrorFactLineage{}
	rows, err := pool.Query(ctx, `SELECT fact_key, event_id, source_native_event_id, sequence FROM events`)
	if err != nil {
		return nil, errors.New("legacy_mirror_fact_query_failed")
	}
	for rows.Next() {
		var key string
		var value mirrorFactLineage
		if err := rows.Scan(&key, &value.EventID, &value.NativeEventID, &value.Sequence); err != nil {
			rows.Close()
			return nil, errors.New("legacy_mirror_fact_scan_failed")
		}
		dbFacts[key] = value
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, errors.New("legacy_mirror_fact_scan_failed")
	}
	dbEvidence := map[string]mirrorEvidenceLineage{}
	rows, err = pool.Query(ctx, `SELECT evidence_id, event_id, tier FROM event_evidence`)
	if err != nil {
		return nil, errors.New("legacy_mirror_evidence_query_failed")
	}
	for rows.Next() {
		var key string
		var value mirrorEvidenceLineage
		if err := rows.Scan(&key, &value.EventID, &value.Tier); err != nil {
			rows.Close()
			return nil, errors.New("legacy_mirror_evidence_scan_failed")
		}
		dbEvidence[key] = value
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, errors.New("legacy_mirror_evidence_scan_failed")
	}
	report := &MirrorReconciliationReport{
		MirrorFactCount:       len(state.Facts),
		DatabaseFactCount:     len(dbFacts),
		MirrorEvidenceCount:   len(state.Evidence),
		DatabaseEvidenceCount: len(dbEvidence),
		CheckpointCount:       len(state.Checkpoints),
		WatermarkCount:        len(state.Watermarks),
		Exclusions: map[string]any{
			"incident_occurrences":  "aggregated metadata uses a later PostgreSQL projection and is not required to be row-identical",
			"postgres_only_records": "expected for emergency-spool replay and post-mirror accepted writes",
		},
	}
	for key, fact := range state.Facts {
		db, ok := dbFacts[key]
		if !ok {
			report.MirrorOnlyFactCount++
			continue
		}
		if db.EventID != fact.Event.EventID ||
			db.NativeEventID != fact.Event.Source.NativeEventID ||
			db.Sequence != fact.Event.Source.Sequence {
			report.LineageMismatchCount++
		}
	}
	report.DatabaseOnlyFactCount = len(dbFacts) - (len(state.Facts) - report.MirrorOnlyFactCount)
	for key, evidence := range state.Evidence {
		db, ok := dbEvidence[key]
		if !ok {
			report.MirrorOnlyEvidenceCount++
			continue
		}
		if db.EventID != evidence.EventID || db.Tier != string(evidence.Tier) {
			report.LineageMismatchCount++
		}
	}
	report.DatabaseOnlyEvidenceCount = len(dbEvidence) - (len(state.Evidence) - report.MirrorOnlyEvidenceCount)
	fingerprints := map[string]bool{}
	for _, quarantine := range state.Quarantine {
		fingerprints[quarantine.SchemaFingerprint] = true
	}
	report.QuarantineFingerprintCount = len(fingerprints)
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT schema_fingerprint) FROM schema_quarantine_metadata`).Scan(
		&report.DatabaseQuarantineFingerprintCount,
	); err != nil {
		return nil, errors.New("legacy_mirror_quarantine_query_failed")
	}
	return report, nil
}

func persistMirrorReconciliation(ctx context.Context, pool *pgxpool.Pool, report *MirrorReconciliationReport) error {
	exclusions, err := json.Marshal(report.Exclusions)
	if err != nil {
		return errors.New("legacy_mirror_reconciliation_encode_failed")
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO runtime_mirror_reconciliations (
			reconciliation_id, mirror_sha256, mirror_bytes, mirror_revision,
			mirror_fact_count, database_fact_count, mirror_only_fact_count,
			database_only_fact_count, mirror_evidence_count, database_evidence_count,
			mirror_only_evidence_count, database_only_evidence_count,
			lineage_mismatch_count, checkpoint_count, watermark_count,
			quarantine_fingerprint_count, status, backup_artifact_id,
			archive_artifact_id, exclusions, reconciled_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,
			NULLIF($19,''),$20,$21
		)
		ON CONFLICT (reconciliation_id) DO UPDATE SET
			status = EXCLUDED.status,
			archive_artifact_id = EXCLUDED.archive_artifact_id,
			reconciled_at = EXCLUDED.reconciled_at
	`, report.ReconciliationID, report.MirrorSHA256, report.MirrorBytes, report.MirrorRevision,
		report.MirrorFactCount, report.DatabaseFactCount, report.MirrorOnlyFactCount,
		report.DatabaseOnlyFactCount, report.MirrorEvidenceCount, report.DatabaseEvidenceCount,
		report.MirrorOnlyEvidenceCount, report.DatabaseOnlyEvidenceCount,
		report.LineageMismatchCount, report.CheckpointCount, report.WatermarkCount,
		report.QuarantineFingerprintCount, report.Status, report.BackupArtifactID,
		report.ArchiveArtifactID, exclusions, report.ReconciledAt)
	if err != nil {
		return errors.New("legacy_mirror_reconciliation_persist_failed")
	}
	return nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyOrVerifyPrivateFile(source, destination, expectedSHA string, expectedBytes int64) error {
	if info, err := os.Stat(destination); err == nil {
		if !info.Mode().IsRegular() || info.Size() != expectedBytes {
			return errors.New("backup_mismatch")
		}
		actual, hashErr := sha256File(destination)
		if hashErr != nil || actual != expectedSHA {
			return errors.New("backup_mismatch")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = output.Close()
		_ = os.Remove(destination + ".tmp")
	}
	written, err := io.Copy(output, input)
	if err != nil || written != expectedBytes || output.Sync() != nil || output.Close() != nil {
		cleanup()
		return errors.New("backup_copy_failed")
	}
	if err := os.Rename(destination+".tmp", destination); err != nil {
		cleanup()
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}

func writePrivateJSONAtomic(path string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
