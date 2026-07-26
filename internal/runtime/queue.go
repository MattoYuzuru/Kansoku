package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"kansoku.local/kansoku/internal/observability"
)

type SpoolStore interface {
	Append(observability.CommitRequest) error
	Replay(func(observability.CommitRequest) error) error
	Stats() (observability.SpoolStats, error)
}

type QueueMetrics struct {
	Depth                 map[observability.SourceKind]int
	OldestSpoolRecord     map[observability.SourceKind]time.Time
	Accepted              uint64
	Spooled               uint64
	Replayed              uint64
	BackpressureRejected  uint64
	DurabilityUnavailable uint64
}

type queueLane struct {
	mu       sync.Mutex
	capacity int
	inUse    int
	jobs     chan *persistJob
	spool    SpoolStore
}

type persistJob struct {
	event    observability.Event
	evidence observability.Evidence
	result   chan error
}

// DurableIngressQueue is the production durability boundary. A lane slot is
// reserved before the compatibility FileStore mirror commits; completion is
// acknowledged only after PostgreSQL or the fsynced sanitized spool owns it.
type DurableIngressQueue struct {
	sink        observability.DurableFactSink
	lanes       map[observability.SourceKind]*queueLane
	stateMu     sync.RWMutex
	accepting   bool
	closeOnce   sync.Once
	outstanding sync.WaitGroup
	workers     sync.WaitGroup
	metricsMu   sync.Mutex
	metrics     QueueMetrics
}

var _ observability.ReservingDurableFactSink = (*DurableIngressQueue)(nil)
var _ observability.DurableMetadataSink = (*DurableIngressQueue)(nil)

var productionSources = []observability.SourceKind{
	observability.SourceHook,
	observability.SourceOTLPLog,
	observability.SourceOTLPSpan,
	observability.SourceOTLPMetric,
	observability.SourceTranscript,
	observability.SourceAdapterBatch,
	observability.SourceEvidenceBridge,
}

func NewDurableIngressQueue(sink observability.DurableFactSink, dataDir string, queueCapacity int, spoolMaxBytes int64) (*DurableIngressQueue, error) {
	if sink == nil || !filepath.IsAbs(dataDir) || queueCapacity != 64 || spoolMaxBytes != 64<<20 {
		return nil, errors.New("invalid_durable_queue_configuration")
	}
	spools := make(map[observability.SourceKind]SpoolStore, len(productionSources))
	for _, source := range productionSources {
		spool, err := observability.NewDurableSpool(filepath.Join(dataDir, "spool", string(source)+".jsonl"), spoolMaxBytes)
		if err != nil {
			return nil, fmt.Errorf("create %s spool: %w", source, err)
		}
		spools[source] = spool
	}
	return NewDurableIngressQueueWithSpools(sink, spools, queueCapacity)
}

// NewDurableIngressQueueWithSpools is an injectable construction path for
// deterministic tests; production uses one secure DurableSpool per lane.
func NewDurableIngressQueueWithSpools(sink observability.DurableFactSink, spools map[observability.SourceKind]SpoolStore, queueCapacity int) (*DurableIngressQueue, error) {
	if sink == nil || queueCapacity != 64 || len(spools) != len(productionSources) {
		return nil, errors.New("invalid_durable_queue_configuration")
	}
	queue := &DurableIngressQueue{
		sink:      sink,
		lanes:     make(map[observability.SourceKind]*queueLane, len(productionSources)),
		accepting: true,
		metrics: QueueMetrics{
			Depth:             make(map[observability.SourceKind]int, len(productionSources)),
			OldestSpoolRecord: make(map[observability.SourceKind]time.Time, len(productionSources)),
		},
	}
	for _, source := range productionSources {
		spool := spools[source]
		if spool == nil {
			return nil, fmt.Errorf("spool required for %s", source)
		}
		capacity := queueCapacity
		if source == observability.SourceTranscript || source == observability.SourceAdapterBatch ||
			source == observability.SourceEvidenceBridge {
			capacity = 16
		}
		lane := &queueLane{capacity: capacity, jobs: make(chan *persistJob, capacity), spool: spool}
		queue.lanes[source] = lane
		queue.workers.Add(1)
		go queue.runLane(source, lane)
	}
	return queue, nil
}

func (q *DurableIngressQueue) PersistNormalizedFact(event observability.Event, evidence observability.Evidence) error {
	reservation, err := q.ReserveNormalizedFact(event, evidence)
	if err != nil {
		return err
	}
	defer reservation.Cancel()
	return reservation.Commit()
}

func (q *DurableIngressQueue) PersistQuarantineMetadata(quarantine observability.Quarantine, incident observability.Incident) error {
	q.stateMu.RLock()
	accepting := q.accepting
	q.stateMu.RUnlock()
	if !accepting {
		return observability.ErrDurabilityUnavailable
	}
	sink, ok := q.sink.(observability.DurableMetadataSink)
	if !ok {
		return observability.ErrDurabilityUnavailable
	}
	if err := sink.PersistQuarantineMetadata(quarantine, incident); err != nil {
		q.incrementMetric(func(metrics *QueueMetrics) { metrics.DurabilityUnavailable++ })
		return observability.ErrDurabilityUnavailable
	}
	return nil
}

func (q *DurableIngressQueue) ReserveNormalizedFact(event observability.Event, evidence observability.Evidence) (observability.DurableFactReservation, error) {
	if evidence.EventID != event.EventID {
		return nil, errors.New("durable_queue_evidence_mismatch")
	}
	lane, ok := q.lanes[event.Source.Kind]
	if !ok {
		return nil, errors.New("durable_queue_source_unsupported")
	}
	q.stateMu.RLock()
	accepting := q.accepting
	if accepting {
		lane.mu.Lock()
		if lane.inUse >= lane.capacity {
			lane.mu.Unlock()
			q.stateMu.RUnlock()
			q.incrementMetric(func(metrics *QueueMetrics) { metrics.BackpressureRejected++ })
			return nil, observability.ErrBackpressure
		}
		lane.inUse++
		q.outstanding.Add(1)
		lane.mu.Unlock()
	}
	q.stateMu.RUnlock()
	if !accepting {
		return nil, observability.ErrDurabilityUnavailable
	}
	return &queueReservation{
		queue: q, lane: lane, event: event, evidence: evidence,
	}, nil
}

type queueReservation struct {
	mu        sync.Mutex
	queue     *DurableIngressQueue
	lane      *queueLane
	event     observability.Event
	evidence  observability.Evidence
	completed bool
}

func (r *queueReservation) Commit() error {
	r.mu.Lock()
	if r.completed {
		r.mu.Unlock()
		return errors.New("durable_reservation_already_completed")
	}
	r.completed = true
	r.mu.Unlock()
	result := make(chan error, 1)
	r.lane.jobs <- &persistJob{event: r.event, evidence: r.evidence, result: result}
	return <-result
}

func (r *queueReservation) Cancel() {
	r.mu.Lock()
	if r.completed {
		r.mu.Unlock()
		return
	}
	r.completed = true
	r.mu.Unlock()
	r.queue.release(r.lane)
}

func (q *DurableIngressQueue) runLane(source observability.SourceKind, lane *queueLane) {
	defer q.workers.Done()
	for job := range lane.jobs {
		err := q.sink.PersistNormalizedFact(job.event, job.evidence)
		if err != nil {
			request := observability.CommitRequest{Event: &job.event, Evidence: &job.evidence}
			if spoolErr := lane.spool.Append(request); spoolErr != nil {
				err = observability.ErrDurabilityUnavailable
				q.incrementMetric(func(metrics *QueueMetrics) { metrics.DurabilityUnavailable++ })
			} else {
				err = nil
				q.incrementMetric(func(metrics *QueueMetrics) { metrics.Spooled++ })
			}
		}
		if err == nil {
			q.incrementMetric(func(metrics *QueueMetrics) { metrics.Accepted++ })
		}
		job.result <- err
		close(job.result)
		q.release(lane)
	}
}

func (q *DurableIngressQueue) release(lane *queueLane) {
	lane.mu.Lock()
	lane.inUse--
	lane.mu.Unlock()
	q.outstanding.Done()
}

// ReplaySpools must run after migrations and before public listeners.
func (q *DurableIngressQueue) ReplaySpools() error {
	for _, source := range productionSources {
		lane := q.lanes[source]
		var replayed uint64
		err := lane.spool.Replay(func(request observability.CommitRequest) error {
			if err := q.sink.PersistNormalizedFact(*request.Event, *request.Evidence); err != nil {
				return err
			}
			replayed++
			return nil
		})
		if err != nil {
			return fmt.Errorf("replay %s spool: %w", source, err)
		}
		if replayed > 0 {
			q.incrementMetric(func(metrics *QueueMetrics) { metrics.Replayed += replayed })
		}
	}
	return nil
}

func (q *DurableIngressQueue) Metrics() (QueueMetrics, error) {
	q.metricsMu.Lock()
	metrics := QueueMetrics{
		Depth:                 make(map[observability.SourceKind]int, len(q.lanes)),
		OldestSpoolRecord:     make(map[observability.SourceKind]time.Time, len(q.lanes)),
		Accepted:              q.metrics.Accepted,
		Spooled:               q.metrics.Spooled,
		Replayed:              q.metrics.Replayed,
		BackpressureRejected:  q.metrics.BackpressureRejected,
		DurabilityUnavailable: q.metrics.DurabilityUnavailable,
	}
	q.metricsMu.Unlock()
	for source, lane := range q.lanes {
		lane.mu.Lock()
		metrics.Depth[source] = lane.inUse
		lane.mu.Unlock()
		stats, err := lane.spool.Stats()
		if err != nil {
			return QueueMetrics{}, err
		}
		metrics.Depth[source] += stats.Depth
		metrics.OldestSpoolRecord[source] = stats.OldestAt
	}
	return metrics, nil
}

func (q *DurableIngressQueue) incrementMetric(update func(*QueueMetrics)) {
	q.metricsMu.Lock()
	update(&q.metrics)
	q.metricsMu.Unlock()
}

func (q *DurableIngressQueue) Close() {
	q.closeOnce.Do(func() {
		q.StopAccepting()
		q.outstanding.Wait()
		for _, lane := range q.lanes {
			close(lane.jobs)
		}
		q.workers.Wait()
	})
}

func (q *DurableIngressQueue) Drain(ctx context.Context) error {
	q.StopAccepting()
	done := make(chan struct{})
	go func() {
		q.outstanding.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("ingress_drain_timeout")
	}
}

func (q *DurableIngressQueue) StopAccepting() {
	q.stateMu.Lock()
	if !q.accepting {
		q.stateMu.Unlock()
		return
	}
	q.accepting = false
	q.stateMu.Unlock()
}

func (q *DurableIngressQueue) IsAccepting() bool {
	q.stateMu.RLock()
	defer q.stateMu.RUnlock()
	return q.accepting
}
