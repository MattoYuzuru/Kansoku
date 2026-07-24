package integrity

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const AuditReportSchemaVersion = "kansoku.integrity-audit-report/1"

type AuditReportCheck struct {
	CheckID        string      `json:"check_id"`
	StageID        StageID     `json:"stage_id"`
	CapabilityID   string      `json:"capability_id"`
	InstallationID string      `json:"installation_id"`
	SourceID       string      `json:"source_id"`
	Status         CheckStatus `json:"status"`
	FailureClass   string      `json:"failure_class,omitempty"`
	ObservedAt     *time.Time  `json:"observed_at,omitempty"`
}

type AuditReport struct {
	SchemaVersion string             `json:"schema_version"`
	AuditRunID    string             `json:"audit_run_id"`
	Mode          RunMode            `json:"mode"`
	Trigger       Trigger            `json:"trigger"`
	State         RunState           `json:"state"`
	FailureReason FailureReason      `json:"failure_reason,omitempty"`
	GeneratedAt   time.Time          `json:"generated_at"`
	Checks        []AuditReportCheck `json:"checks"`
}

type SignedAuditReport struct {
	Report             AuditReport `json:"report"`
	ReportSHA256       string      `json:"report_sha256"`
	SignatureAlgorithm string      `json:"signature_algorithm"`
	SignatureKeyID     string      `json:"signature_key_id"`
	Signature          string      `json:"signature"`
}

func BuildSignedAuditReport(run AuditRun, checks []AuditCheck, generatedAt time.Time, keyID string, key []byte) (SignedAuditReport, error) {
	if len(key) < 32 || keyID == "" {
		return SignedAuditReport{}, errors.New("report signing requires a named key of at least 32 bytes")
	}
	report := AuditReport{
		SchemaVersion: AuditReportSchemaVersion, AuditRunID: run.AuditRunID,
		Mode: run.Mode, Trigger: run.Trigger, State: run.State,
		// PostgreSQL timestamptz is microsecond-precision. Canonicalize before
		// both JSON signing and the duplicated generated_at column are
		// persisted so strict envelope comparison cannot fail solely because
		// one representation retained nanoseconds.
		FailureReason: run.FailureReason, GeneratedAt: generatedAt.UTC().Truncate(time.Microsecond),
	}
	for _, check := range checks {
		report.Checks = append(report.Checks, AuditReportCheck{
			CheckID: check.CheckID, StageID: check.StageID,
			CapabilityID: check.CapabilityID, InstallationID: check.InstallationID,
			SourceID: check.SourceID, Status: check.Status,
			FailureClass: check.Category, ObservedAt: check.ObservedAt,
		})
	}
	sort.Slice(report.Checks, func(i, j int) bool {
		left, right := report.Checks[i], report.Checks[j]
		return string(left.StageID)+"\x00"+left.CheckID+"\x00"+left.InstallationID+"\x00"+left.SourceID <
			string(right.StageID)+"\x00"+right.CheckID+"\x00"+right.InstallationID+"\x00"+right.SourceID
	})
	canonical, err := json.Marshal(report)
	if err != nil {
		return SignedAuditReport{}, err
	}
	sum := sha256.Sum256(canonical)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(canonical)
	return SignedAuditReport{
		Report: report, ReportSHA256: hex.EncodeToString(sum[:]),
		SignatureAlgorithm: "hmac-sha256", SignatureKeyID: keyID,
		Signature: hex.EncodeToString(mac.Sum(nil)),
	}, nil
}

func VerifySignedAuditReport(signed SignedAuditReport, key []byte) error {
	if signed.SignatureAlgorithm != "hmac-sha256" || len(key) < 32 {
		return errors.New("unsupported signature or invalid key")
	}
	canonical, err := json.Marshal(signed.Report)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(canonical)
	if !hmac.Equal([]byte(signed.ReportSHA256), []byte(hex.EncodeToString(sum[:]))) {
		return errors.New("audit report digest mismatch")
	}
	expected, err := hex.DecodeString(signed.Signature)
	if err != nil {
		return errors.New("audit report signature is not hex")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(canonical)
	if !hmac.Equal(expected, mac.Sum(nil)) {
		return errors.New("audit report signature mismatch")
	}
	return nil
}

func PersistSignedAuditReport(ctx context.Context, pool *pgxpool.Pool, signed SignedAuditReport) error {
	if pool == nil {
		return errors.New("report persistence requires PostgreSQL pool")
	}
	return persistSignedAuditReportWith(ctx, pool, signed)
}

func persistSignedAuditReportWith(ctx context.Context, executor integrityExecer, signed SignedAuditReport) error {
	canonical, err := json.Marshal(signed.Report)
	if err != nil {
		return err
	}
	_, err = executor.Exec(ctx, `
		INSERT INTO integrity_audit_reports (
		    audit_run_id, report_schema_version, generated_at, canonical_report,
		    report_sha256, signature_algorithm, signature_key_id, signature
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (audit_run_id) DO UPDATE SET
		    report_schema_version=EXCLUDED.report_schema_version,
		    generated_at=EXCLUDED.generated_at,
		    canonical_report=EXCLUDED.canonical_report,
		    report_sha256=EXCLUDED.report_sha256,
		    signature_algorithm=EXCLUDED.signature_algorithm,
		    signature_key_id=EXCLUDED.signature_key_id,
		    signature=EXCLUDED.signature
	`, signed.Report.AuditRunID, signed.Report.SchemaVersion, signed.Report.GeneratedAt,
		canonical, signed.ReportSHA256, signed.SignatureAlgorithm, signed.SignatureKeyID, signed.Signature)
	if err != nil {
		return fmt.Errorf("persist signed audit report: %w", err)
	}
	return nil
}

// LoadSignedAuditReport returns the exact signed envelope persisted for one
// audit run. Verification remains an explicit caller step because the
// process-local signing key is deliberately never stored in PostgreSQL.
func LoadSignedAuditReport(ctx context.Context, pool *pgxpool.Pool, auditRunID string) (SignedAuditReport, error) {
	if pool == nil || auditRunID == "" {
		return SignedAuditReport{}, errors.New("report lookup requires PostgreSQL pool and audit run ID")
	}
	var (
		canonical           []byte
		signed              SignedAuditReport
		storedSchemaVersion string
		storedGeneratedAt   time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT report_schema_version, generated_at, canonical_report,
		       report_sha256, signature_algorithm,
		       signature_key_id, signature
		FROM integrity_audit_reports
		WHERE audit_run_id = $1
	`, auditRunID).Scan(
		&storedSchemaVersion, &storedGeneratedAt, &canonical,
		&signed.ReportSHA256, &signed.SignatureAlgorithm,
		&signed.SignatureKeyID, &signed.Signature,
	)
	if err != nil {
		return SignedAuditReport{}, fmt.Errorf("load signed audit report: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&signed.Report); err != nil {
		return SignedAuditReport{}, fmt.Errorf("decode signed audit report: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SignedAuditReport{}, errors.New("decode signed audit report: trailing JSON value")
	}
	if signed.Report.AuditRunID != auditRunID {
		return SignedAuditReport{}, errors.New("signed report audit_run_id does not match row key")
	}
	if signed.Report.SchemaVersion != storedSchemaVersion {
		return SignedAuditReport{}, errors.New("signed report schema version does not match duplicated column")
	}
	if !signed.Report.GeneratedAt.Equal(storedGeneratedAt.UTC()) {
		return SignedAuditReport{}, errors.New("signed report generated_at does not match duplicated column")
	}
	return signed, nil
}

var _ integrityExecer = (pgx.Tx)(nil)
