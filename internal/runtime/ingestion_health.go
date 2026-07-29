package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/observability"
)

type IngestionHealthOutcome string

const (
	ingestionHealthSuccessful            IngestionHealthOutcome = "successful"
	ingestionHealthBackpressureRejected  IngestionHealthOutcome = "backpressure_rejected"
	ingestionHealthDurabilityUnavailable IngestionHealthOutcome = "durability_unavailable"
)

type DurableIngestionHealth struct {
	LastSuccessful        time.Time
	LastRejected          time.Time
	BackpressureRejected  uint64
	DurabilityUnavailable uint64
}

type IngestionHealthRecorder interface {
	Load(context.Context) (DurableIngestionHealth, error)
	Record(observability.SourceKind, IngestionHealthOutcome, time.Time) error
}

// PostgresIngestionHealthRecorder persists operational counters separately
// from event facts. Success timestamps are throttled per source to avoid an
// extra database write per event; rejection counters are synchronous so an
// acknowledged retry/error response is reflected before it reaches a caller.
type PostgresIngestionHealthRecorder struct {
	pool        *pgxpool.Pool
	timeout     time.Duration
	mu          sync.Mutex
	lastSuccess map[observability.SourceKind]time.Time
}

func NewPostgresIngestionHealthRecorder(
	pool *pgxpool.Pool,
	timeout time.Duration,
) (*PostgresIngestionHealthRecorder, error) {
	if pool == nil || timeout <= 0 {
		return nil, errors.New("invalid_ingestion_health_recorder_configuration")
	}
	return &PostgresIngestionHealthRecorder{
		pool: pool, timeout: timeout,
		lastSuccess: map[observability.SourceKind]time.Time{},
	}, nil
}

func (r *PostgresIngestionHealthRecorder) Load(ctx context.Context) (DurableIngestionHealth, error) {
	var snapshot DurableIngestionHealth
	var lastSuccessful, lastRejected *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT max(last_successful_ingest_at), max(last_rejected_ingest_at),
		       COALESCE(sum(backpressure_rejected_total),0),
		       COALESCE(sum(durability_unavailable_total),0)
		FROM runtime_ingestion_health
	`).Scan(
		&lastSuccessful, &lastRejected, &snapshot.BackpressureRejected,
		&snapshot.DurabilityUnavailable,
	)
	if err != nil {
		return DurableIngestionHealth{}, err
	}
	if lastSuccessful != nil {
		snapshot.LastSuccessful = lastSuccessful.UTC()
	}
	if lastRejected != nil {
		snapshot.LastRejected = lastRejected.UTC()
	}
	return snapshot, nil
}

func (r *PostgresIngestionHealthRecorder) Record(
	source observability.SourceKind,
	outcome IngestionHealthOutcome,
	observedAt time.Time,
) error {
	if !knownProductionSource(source) || observedAt.IsZero() {
		return errors.New("invalid_ingestion_health_record")
	}
	observedAt = observedAt.UTC()
	if outcome == ingestionHealthSuccessful {
		r.mu.Lock()
		last := r.lastSuccess[source]
		if !last.IsZero() && observedAt.Sub(last) < time.Second {
			r.mu.Unlock()
			return nil
		}
		r.lastSuccess[source] = observedAt
		r.mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	switch outcome {
	case ingestionHealthSuccessful:
		_, err := r.pool.Exec(ctx, `
			INSERT INTO runtime_ingestion_health (
				source_kind,last_successful_ingest_at,updated_at
			) VALUES ($1,$2,now())
			ON CONFLICT (source_kind) DO UPDATE SET
				last_successful_ingest_at=GREATEST(
					runtime_ingestion_health.last_successful_ingest_at,
					EXCLUDED.last_successful_ingest_at
				),
				updated_at=now()
		`, source, observedAt)
		return err
	case ingestionHealthBackpressureRejected:
		_, err := r.pool.Exec(ctx, `
			INSERT INTO runtime_ingestion_health (
				source_kind,last_rejected_ingest_at,
				backpressure_rejected_total,updated_at
			) VALUES ($1,$2,1,now())
			ON CONFLICT (source_kind) DO UPDATE SET
				last_rejected_ingest_at=GREATEST(
					runtime_ingestion_health.last_rejected_ingest_at,
					EXCLUDED.last_rejected_ingest_at
				),
				backpressure_rejected_total=
					runtime_ingestion_health.backpressure_rejected_total+1,
				updated_at=now()
		`, source, observedAt)
		return err
	case ingestionHealthDurabilityUnavailable:
		_, err := r.pool.Exec(ctx, `
			INSERT INTO runtime_ingestion_health (
				source_kind,last_rejected_ingest_at,
				durability_unavailable_total,updated_at
			) VALUES ($1,$2,1,now())
			ON CONFLICT (source_kind) DO UPDATE SET
				last_rejected_ingest_at=GREATEST(
					runtime_ingestion_health.last_rejected_ingest_at,
					EXCLUDED.last_rejected_ingest_at
				),
				durability_unavailable_total=
					runtime_ingestion_health.durability_unavailable_total+1,
				updated_at=now()
		`, source, observedAt)
		return err
	default:
		return errors.New("invalid_ingestion_health_outcome")
	}
}

func knownProductionSource(source observability.SourceKind) bool {
	for _, candidate := range productionSources {
		if candidate == source {
			return true
		}
	}
	return false
}
