package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/installer"
)

const (
	planTTL         = 10 * time.Minute
	maxOpenPreviews = 32
)

type PlanPreviewRequest struct {
	TargetID string         `json:"target_id"`
	Original map[string]any `json:"original"`
}

type PlanPreview struct {
	PlanID           string                `json:"plan_id"`
	TargetID         string                `json:"target_id"`
	PlanSHA256       string                `json:"plan_sha256"`
	OriginalSHA256   string                `json:"original_sha256"`
	PlannedSHA256    string                `json:"planned_sha256"`
	ExactOperations  []installer.Operation `json:"exact_operations"`
	PrivacyTradeoffs []string              `json:"privacy_tradeoffs"`
	ExpiresAt        time.Time             `json:"expires_at"`
}

type PlanApplyRequest struct {
	PlanID         string         `json:"plan_id"`
	PlanSHA256     string         `json:"plan_sha256"`
	TargetID       string         `json:"target_id"`
	OriginalSHA256 string         `json:"original_sha256"`
	PlannedSHA256  string         `json:"planned_sha256"`
	ApprovalNonce  string         `json:"approval_nonce"`
	Current        map[string]any `json:"current"`
}

type PlanApplyResult struct {
	Materialized map[string]any         `json:"materialized"`
	Receipt      installer.AuditReceipt `json:"receipt"`
}

type previewRecord struct {
	PlanPreview
}

// PlanManager stores only hashes and operations. Raw current configuration is
// transient request data and is rebuilt at apply time to detect config races.
type PlanManager struct {
	mu       sync.Mutex
	previews map[string]previewRecord
	used     map[[sha256.Size]byte]bool
	pool     *pgxpool.Pool
	auditKey []byte
	now      func() time.Time
}

type PlanService = PlanManager

func NewPlanManager(pool *pgxpool.Pool, auditKey []byte) (*PlanManager, error) {
	if pool == nil || len(auditKey) < minSecretBytes {
		return nil, errors.New("invalid_plan_manager_configuration")
	}
	return &PlanManager{
		previews: map[string]previewRecord{}, used: map[[sha256.Size]byte]bool{},
		pool: pool, auditKey: append([]byte(nil), auditKey...), now: time.Now,
	}, nil
}

func (m *PlanManager) Preview(request PlanPreviewRequest) (PlanPreview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	for id, preview := range m.previews {
		if !preview.ExpiresAt.After(now) {
			delete(m.previews, id)
		}
	}
	if len(m.previews) >= maxOpenPreviews {
		return PlanPreview{}, errors.New("preview_capacity_exhausted")
	}
	planID, err := newOpaqueID("plan")
	if err != nil {
		return PlanPreview{}, err
	}
	plan, err := buildInstallerPlan(planID, request.TargetID, request.Original)
	if err != nil {
		return PlanPreview{}, err
	}
	planHash, err := installer.PlanSHA256(plan)
	if err != nil {
		return PlanPreview{}, err
	}
	preview := PlanPreview{
		PlanID: plan.PlanID, TargetID: plan.TargetID, PlanSHA256: planHash,
		OriginalSHA256: plan.OriginalSHA256, PlannedSHA256: plan.PlannedSHA256,
		ExactOperations:  append([]installer.Operation(nil), plan.ExactOperations...),
		PrivacyTradeoffs: append([]string(nil), plan.PrivacyTradeoffs...),
		ExpiresAt:        now.Add(planTTL),
	}
	m.previews[preview.PlanID] = previewRecord{PlanPreview: preview}
	return preview, nil
}

func (m *PlanManager) Apply(ctx context.Context, request PlanApplyRequest) (PlanApplyResult, error) {
	nonceHash := sha256.Sum256([]byte(request.ApprovalNonce))
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.previews[request.PlanID]
	if !ok {
		return PlanApplyResult{}, errors.New("unknown_preview")
	}
	if !record.ExpiresAt.After(m.now().UTC()) {
		delete(m.previews, request.PlanID)
		return PlanApplyResult{}, errors.New("expired_preview")
	}
	if request.ApprovalNonce == "" || m.used[nonceHash] {
		return PlanApplyResult{}, errors.New("replay_nonce")
	}
	if request.TargetID != record.TargetID || request.PlanSHA256 != record.PlanSHA256 ||
		request.OriginalSHA256 != record.OriginalSHA256 || request.PlannedSHA256 != record.PlannedSHA256 {
		return PlanApplyResult{}, errors.New("approval_binding_mismatch")
	}
	plan, err := buildInstallerPlan(request.PlanID, request.TargetID, request.Current)
	if err != nil {
		return PlanApplyResult{}, errors.New("config_race")
	}
	rebuiltHash, err := installer.PlanSHA256(plan)
	if err != nil || rebuiltHash != record.PlanSHA256 || plan.OriginalSHA256 != record.OriginalSHA256 ||
		plan.PlannedSHA256 != record.PlannedSHA256 {
		return PlanApplyResult{}, errors.New("config_race")
	}
	approval := installer.Approval{
		PlanSHA256: request.PlanSHA256, TargetID: request.TargetID,
		OriginalSHA256: request.OriginalSHA256, PlannedSHA256: request.PlannedSHA256,
		ApprovalNonce: request.ApprovalNonce,
	}
	parametersHash := sha256.Sum256([]byte(request.PlanSHA256 + "\x00" + request.TargetID + "\x00" + request.OriginalSHA256 + "\x00" + request.PlannedSHA256))
	if _, err := m.pool.Exec(ctx, `
		INSERT INTO runtime_operation_approvals
			(request_id, operation, parameters_sha256, approval_nonce_sha256, approved_at, consumed_at, result)
		VALUES ($1, 'plan_apply', $2, $3, $4, $4, 'approved')
	`, request.PlanID, hex.EncodeToString(parametersHash[:]), hex.EncodeToString(nonceHash[:]), m.now().UTC()); err != nil {
		return PlanApplyResult{}, errors.New("approval_persistence_failed")
	}
	m.used[nonceHash] = true
	materialized, receipt, err := installer.SimulateApply(request.Current, plan, approval, m.auditKey)
	if err != nil {
		_, _ = m.pool.Exec(ctx, `UPDATE runtime_operation_approvals SET result='failed' WHERE request_id=$1`, request.PlanID)
		return PlanApplyResult{}, err
	}
	delete(m.previews, request.PlanID)
	_, _ = m.pool.Exec(ctx, `UPDATE runtime_operation_approvals SET result='applied' WHERE request_id=$1`, request.PlanID)
	return PlanApplyResult{Materialized: materialized, Receipt: receipt}, nil
}

func buildInstallerPlan(planID, targetID string, original map[string]any) (installer.Plan, error) {
	locator := "/runtime-plan/" + targetID
	backup := "/runtime-plan-backup/" + targetID
	switch targetID {
	case "codex.user_otel":
		return installer.BuildCodexPlan(planID, locator, backup, "kansoku rollback", original)
	case "claude.user_otel":
		return installer.BuildClaudePlan(planID, locator, backup, "kansoku rollback", original)
	case "gemini.user_otel":
		return installer.BuildGeminiPlan(planID, locator, backup, "kansoku rollback", original)
	case "cursor.user_hooks":
		return installer.BuildCursorPlan(planID, locator, backup, "kansoku rollback", original)
	default:
		return installer.Plan{}, errors.New("unsupported_plan_target")
	}
}

func newOpaqueID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(value[:]), nil
}
