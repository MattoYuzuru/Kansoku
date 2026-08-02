package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"kansoku.local/kansoku/internal/dataplatform"
	"kansoku.local/kansoku/internal/observability"
)

const projectionRepairOperation = "projection_repair_retry"

type ProjectionRepairPreviewRequest struct{}

type ProjectionRepairApplyRequest struct {
	RequestID        string `json:"request_id"`
	ParametersSHA256 string `json:"parameters_sha256"`
	ApprovalNonce    string `json:"approval_nonce"`
}

type ProjectionRepairLaneState struct {
	QueueAndSpoolDepth int        `json:"queue_and_spool_depth"`
	SpoolBytes         int64      `json:"spool_bytes"`
	SpoolBudgetBytes   int64      `json:"spool_budget_bytes"`
	OldestSpooledAt    *time.Time `json:"oldest_spooled_at"`
}

// ProjectionRepairState intentionally contains aggregate identifiers and
// bounded operational metadata only. Event/evidence payloads and spool frames
// never cross the operator API.
type ProjectionRepairState struct {
	ReceiptCounts          map[string]int64                     `json:"receipt_counts"`
	TotalReceiptCount      int64                                `json:"total_receipt_count"`
	RepairableInputCount   int64                                `json:"repairable_input_count"`
	LegacyInputAbsentCount int64                                `json:"legacy_input_absent_count"`
	MaxAttemptCount        int64                                `json:"max_attempt_count"`
	OldestEnqueuedAt       *time.Time                           `json:"oldest_enqueued_at"`
	LastAttemptedAt        *time.Time                           `json:"last_attempted_at"`
	ErrorClassCounts       map[string]int64                     `json:"error_class_counts"`
	Lanes                  map[string]ProjectionRepairLaneState `json:"lanes"`
	PayloadsExposed        bool                                 `json:"payloads_exposed"`
	AutomaticDiscard       bool                                 `json:"automatic_discard"`
	Completeness           string                               `json:"completeness"`
	CompletenessNotes      []string                             `json:"completeness_notes"`
}

type projectionRepairPreview struct {
	RequestID        string                `json:"request_id"`
	Operation        string                `json:"operation"`
	ParametersSHA256 string                `json:"parameters_sha256"`
	ExpiresAt        time.Time             `json:"expires_at"`
	ExactEffect      string                `json:"exact_effect"`
	CurrentState     ProjectionRepairState `json:"current_state"`
}

type ProjectionRepairResult struct {
	RequestID       string                              `json:"request_id"`
	Operation       string                              `json:"operation"`
	DatabaseReplay  dataplatform.ProjectionReplayResult `json:"database_replay"`
	SpoolReplayPass bool                                `json:"spool_replay_pass"`
	Before          ProjectionRepairState               `json:"before"`
	After           ProjectionRepairState               `json:"after"`
}

func projectionRepairParametersSHA256() string {
	digest := sha256.Sum256([]byte(projectionRepairOperation + "\x00v1"))
	return hex.EncodeToString(digest[:])
}

func (s *OperationsService) PreviewProjectionRepair(
	ctx context.Context,
	_ ProjectionRepairPreviewRequest,
) (any, error) {
	state, err := s.projectionRepairState(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := newOpaqueID("projection")
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	preview := projectionRepairPreview{
		RequestID: requestID, Operation: projectionRepairOperation,
		ParametersSHA256: projectionRepairParametersSHA256(),
		ExpiresAt:        now.Add(planTTL),
		ExactEffect: "one_bounded_postgresql_projection_replay_then_one_idempotent_spool_replay" +
			"_without_automatic_discard",
		CurrentState: state,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, current := range s.projectionRepairs {
		if !current.ExpiresAt.After(now) {
			delete(s.projectionRepairs, id)
		}
	}
	if len(s.projectionRepairs) >= maxOpenPreviews {
		return nil, errors.New("preview_capacity_exhausted")
	}
	s.projectionRepairs[requestID] = preview
	return preview, nil
}

func (s *OperationsService) ApplyProjectionRepair(
	ctx context.Context,
	request ProjectionRepairApplyRequest,
) (any, error) {
	nonceHash := sha256.Sum256([]byte(request.ApprovalNonce))
	s.mu.Lock()
	preview, ok := s.projectionRepairs[request.RequestID]
	if !ok || !preview.ExpiresAt.After(s.now().UTC()) {
		delete(s.projectionRepairs, request.RequestID)
		s.mu.Unlock()
		return nil, errors.New("unknown_or_expired_preview")
	}
	if request.ApprovalNonce == "" || s.usedNonces[nonceHash] {
		s.mu.Unlock()
		return nil, errors.New("replay_nonce")
	}
	if request.ParametersSHA256 != preview.ParametersSHA256 ||
		request.ParametersSHA256 != projectionRepairParametersSHA256() {
		s.mu.Unlock()
		return nil, errors.New("approval_binding_mismatch")
	}
	s.usedNonces[nonceHash] = true
	delete(s.projectionRepairs, request.RequestID)
	s.mu.Unlock()

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO runtime_operation_approvals
			(request_id, operation, parameters_sha256, approval_nonce_sha256,
			 approved_at, consumed_at, result)
		VALUES ($1,$2,$3,$4,$5,$5,'approved')
	`, request.RequestID, projectionRepairOperation, request.ParametersSHA256,
		hex.EncodeToString(nonceHash[:]), s.now().UTC()); err != nil {
		return nil, errors.New("approval_persistence_failed")
	}
	repairer, ok := s.queue.sink.(interface {
		ReplayPendingProjections(
			context.Context,
			int,
		) (dataplatform.ProjectionReplayResult, error)
	})
	if !ok {
		_, _ = s.pool.Exec(
			context.WithoutCancel(ctx),
			`UPDATE runtime_operation_approvals SET result='failed' WHERE request_id=$1`,
			request.RequestID,
		)
		return nil, errors.New("projection_repair_unavailable")
	}
	databaseReplay, databaseErr := repairer.ReplayPendingProjections(
		ctx, dataplatform.MaxProjectionRepairBatch,
	)
	spoolErr := s.queue.ReplaySpools()
	if databaseErr != nil || spoolErr != nil {
		_, _ = s.pool.Exec(
			context.WithoutCancel(ctx),
			`UPDATE runtime_operation_approvals SET result='failed' WHERE request_id=$1`,
			request.RequestID,
		)
		return nil, errors.New("projection_repair_incomplete")
	}
	after, err := s.projectionRepairState(ctx)
	if err != nil {
		_, _ = s.pool.Exec(
			context.WithoutCancel(ctx),
			`UPDATE runtime_operation_approvals SET result='failed' WHERE request_id=$1`,
			request.RequestID,
		)
		return nil, errors.New("projection_repair_state_unavailable")
	}
	if after.TotalReceiptCount != 0 {
		_, _ = s.pool.Exec(
			context.WithoutCancel(ctx),
			`UPDATE runtime_operation_approvals SET result='failed' WHERE request_id=$1`,
			request.RequestID,
		)
		return nil, errors.New("projection_repair_incomplete")
	}
	_, _ = s.pool.Exec(
		context.WithoutCancel(ctx),
		`UPDATE runtime_operation_approvals SET result='applied' WHERE request_id=$1`,
		request.RequestID,
	)
	return ProjectionRepairResult{
		RequestID: request.RequestID, Operation: projectionRepairOperation,
		DatabaseReplay: databaseReplay, SpoolReplayPass: true,
		Before: preview.CurrentState, After: after,
	}, nil
}

func (s *OperationsService) projectionRepairState(ctx context.Context) (ProjectionRepairState, error) {
	state := ProjectionRepairState{
		ReceiptCounts: map[string]int64{
			"pending": 0, "retryable": 0, "permanent_error": 0,
		},
		ErrorClassCounts:  map[string]int64{},
		Lanes:             map[string]ProjectionRepairLaneState{},
		PayloadsExposed:   false,
		AutomaticDiscard:  false,
		Completeness:      "complete",
		CompletenessNotes: []string{},
	}
	var pendingCount, retryableCount, permanentErrorCount int64
	if err := s.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE state='pending'),
			count(*) FILTER (WHERE state='retryable'),
			count(*) FILTER (WHERE state='permanent_error'),
			count(*) FILTER (
				WHERE state IN ('pending','retryable')
				  AND projection_input_schema='kansoku.projection-input/1'
				  AND projection_input IS NOT NULL
			),
			count(*) FILTER (WHERE projection_input IS NULL),
			COALESCE(max(attempt_count),0),
			min(first_enqueued_at),
			max(last_attempted_at)
		FROM observability_projection_receipts
	`).Scan(
		&pendingCount,
		&retryableCount,
		&permanentErrorCount,
		&state.RepairableInputCount,
		&state.LegacyInputAbsentCount,
		&state.MaxAttemptCount,
		&state.OldestEnqueuedAt,
		&state.LastAttemptedAt,
	); err != nil {
		return ProjectionRepairState{}, errors.New("projection_repair_state_unavailable")
	}
	state.ReceiptCounts["pending"] = pendingCount
	state.ReceiptCounts["retryable"] = retryableCount
	state.ReceiptCounts["permanent_error"] = permanentErrorCount
	state.TotalReceiptCount = state.ReceiptCounts["pending"] +
		state.ReceiptCounts["retryable"] +
		state.ReceiptCounts["permanent_error"]
	if state.LegacyInputAbsentCount > 0 {
		state.Completeness = "partial"
		state.CompletenessNotes = append(
			state.CompletenessNotes,
			"legacy_receipts_without_projection_input_require_an_owned_spool_frame",
		)
	}
	if state.ReceiptCounts["permanent_error"] > 0 {
		state.Completeness = "partial"
		state.CompletenessNotes = append(
			state.CompletenessNotes,
			"permanent_projection_errors_are_never_automatically_discarded",
		)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT last_error_class,count(*)
		FROM observability_projection_receipts
		WHERE last_error_class IS NOT NULL
		GROUP BY last_error_class
		ORDER BY last_error_class
	`)
	if err != nil {
		return ProjectionRepairState{}, errors.New("projection_repair_state_unavailable")
	}
	for rows.Next() {
		var errorClass string
		var count int64
		if err := rows.Scan(&errorClass, &count); err != nil {
			rows.Close()
			return ProjectionRepairState{}, errors.New("projection_repair_state_unavailable")
		}
		state.ErrorClassCounts[errorClass] = count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ProjectionRepairState{}, errors.New("projection_repair_state_unavailable")
	}
	metrics, err := s.queue.Metrics()
	if err != nil {
		return ProjectionRepairState{}, errors.New("projection_repair_state_unavailable")
	}
	sources := make([]observability.SourceKind, 0, len(metrics.Depth))
	for source := range metrics.Depth {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(left, right int) bool {
		return sources[left] < sources[right]
	})
	for _, kind := range sources {
		source := string(kind)
		var oldest *time.Time
		if at := metrics.OldestSpoolRecord[kind]; !at.IsZero() {
			value := at.UTC()
			oldest = &value
		}
		state.Lanes[source] = ProjectionRepairLaneState{
			QueueAndSpoolDepth: metrics.Depth[kind],
			SpoolBytes:         metrics.SpoolBytes[kind],
			SpoolBudgetBytes:   metrics.SpoolCapacityBytes[kind],
			OldestSpooledAt:    oldest,
		}
	}
	return state, nil
}
