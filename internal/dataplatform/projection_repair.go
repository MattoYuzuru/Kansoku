package dataplatform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const MaxProjectionRepairBatch = 256

type ProjectionReplayResult struct {
	Attempted int64 `json:"attempted"`
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
}

type projectionRepairRow struct {
	EventID    string
	EvidenceID string
	ObservedAt time.Time
	Input      []byte
}

// ReplayPendingProjections repairs a bounded set of retryable projection
// receipts from PostgreSQL itself. It never re-inserts the canonical fact or
// evidence and therefore cannot inflate source replay_count. The retained
// input is the closed Event/Evidence metadata allowlist, not a raw source
// record.
func (h *ObservabilityHandoff) ReplayPendingProjections(
	ctx context.Context,
	limit int,
) (ProjectionReplayResult, error) {
	if h == nil || h.pool == nil {
		return ProjectionReplayResult{}, errors.New("observability_handoff_not_configured")
	}
	if limit <= 0 || limit > MaxProjectionRepairBatch {
		return ProjectionReplayResult{}, errors.New("projection_repair_limit_invalid")
	}
	rows, err := h.pool.Query(ctx, `
		SELECT event_id,evidence_id,observed_at,projection_input
		FROM observability_projection_receipts
		WHERE state IN ('pending','retryable')
		  AND projection_input_schema=$1
		  AND projection_input IS NOT NULL
		ORDER BY first_enqueued_at,evidence_id
		LIMIT $2
	`, ProjectionInputSpecVersion, limit)
	if err != nil {
		return ProjectionReplayResult{}, errors.New("projection_repair_query_failed")
	}
	pending := make([]projectionRepairRow, 0, limit)
	for rows.Next() {
		var row projectionRepairRow
		if err := rows.Scan(&row.EventID, &row.EvidenceID, &row.ObservedAt, &row.Input); err != nil {
			rows.Close()
			return ProjectionReplayResult{}, errors.New("projection_repair_query_failed")
		}
		pending = append(pending, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ProjectionReplayResult{}, errors.New("projection_repair_query_failed")
	}

	result := ProjectionReplayResult{Attempted: int64(len(pending))}
	for _, row := range pending {
		input, err := decodeProjectionInput(row)
		if err != nil {
			h.markProjectionRepairRowFailure(
				ctx, row, "projection_input_invalid", false,
			)
			result.Failed++
			continue
		}
		scope := ObservabilityScope(input.Event)
		scope, err = h.resolveInventoryLifecycleComponent(ctx, input.Event, scope)
		if err == nil {
			err = h.persistSourceWatermark(ctx, input.Event, scope)
		}
		if err == nil {
			err = h.persistProjections(ctx, input.Event, input.Evidence, scope)
		}
		if err != nil {
			h.markProjectionFailure(
				ctx, input.Event, input.Evidence,
				"derived_projection_failed", true,
			)
			result.Failed++
			continue
		}
		tag, err := h.pool.Exec(ctx, `
			DELETE FROM observability_projection_receipts
			WHERE evidence_id=$1 AND observed_at=$2
		`, row.EvidenceID, row.ObservedAt.UTC())
		if err != nil {
			h.markProjectionFailure(
				ctx, input.Event, input.Evidence,
				"projection_receipt_cleanup_failed", true,
			)
			result.Failed++
			continue
		}
		// A concurrent idempotent spool replay may already have removed the
		// receipt after writing the same projections. Zero affected rows is
		// therefore successful completion, not a lost fact.
		_ = tag.RowsAffected()
		result.Succeeded++
	}
	return result, nil
}

func decodeProjectionInput(row projectionRepairRow) (ObservabilityProjectionInput, error) {
	decoder := json.NewDecoder(bytes.NewReader(row.Input))
	decoder.DisallowUnknownFields()
	var input ObservabilityProjectionInput
	if err := decoder.Decode(&input); err != nil {
		return ObservabilityProjectionInput{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ObservabilityProjectionInput{}, errors.New("projection_input_trailing_data")
	}
	if input.SpecVersion != ProjectionInputSpecVersion ||
		input.Event.EventID != row.EventID ||
		!input.Event.ObservedAt.Equal(row.ObservedAt) ||
		input.Evidence.EvidenceID != row.EvidenceID ||
		input.Evidence.EventID != row.EventID {
		return ObservabilityProjectionInput{}, errors.New("projection_input_identity_mismatch")
	}
	return input, nil
}

func (h *ObservabilityHandoff) markProjectionRepairRowFailure(
	ctx context.Context,
	row projectionRepairRow,
	errorClass string,
	retryable bool,
) {
	state := "permanent_error"
	if retryable {
		state = "retryable"
	}
	_, _ = h.pool.Exec(ctx, `
		UPDATE observability_projection_receipts
		SET state=$3,attempt_count=attempt_count+1,
		    last_error_class=$4,last_attempted_at=now()
		WHERE evidence_id=$1 AND observed_at=$2
	`, row.EvidenceID, row.ObservedAt.UTC(), state, errorClass)
}
