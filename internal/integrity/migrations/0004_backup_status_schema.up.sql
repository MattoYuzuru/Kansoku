-- Durably records the latest-known backup/restore-test evidence stage_9's
-- RetentionDiskBackupCheck reads via BackupStatusLookup, matching
-- storageops.go's own instruction: "stage_9 never fabricates a restore-test
-- result that did not actually run". A single row (id = 1) is upserted after
-- every real RunBackupCycle invocation; the table starts empty, which
-- BackupStatusLookup reports honestly as "no backup/restore test ever ran"
-- rather than defaulting to a fabricated pass.

CREATE TABLE IF NOT EXISTS integrity_backup_status (
    id                       INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    last_backup_at           TIMESTAMPTZ,
    last_backup_checksum_ok  BOOLEAN NOT NULL DEFAULT false,
    last_restore_test_at     TIMESTAMPTZ,
    last_restore_test_ran    BOOLEAN NOT NULL DEFAULT false,
    last_restore_test_passed BOOLEAN NOT NULL DEFAULT false,
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);
