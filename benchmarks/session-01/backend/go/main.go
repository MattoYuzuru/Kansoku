package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	maxBodyBytes  = 1 << 20
	maxLogRecords = 10_000
	batchSize     = 64
)

type logRecord struct {
	TimeUnixNano string `json:"timeUnixNano,omitempty"`
}

type scopeLogs struct {
	LogRecords []logRecord `json:"logRecords"`
}

type resourceLogs struct {
	ScopeLogs []scopeLogs `json:"scopeLogs"`
}

type exportLogsRequest struct {
	ResourceLogs []resourceLogs `json:"resourceLogs"`
}

type safeRow struct {
	ReceivedAt        string `json:"received_at"`
	Route             string `json:"route"`
	RecordCount       int    `json:"record_count"`
	BodyBytes         int64  `json:"body_bytes"`
	SchemaFingerprint string `json:"schema_fingerprint"`
}

type workItem struct {
	row safeRow
	ack chan error
}

type sink struct {
	queue     chan workItem
	done      chan struct{}
	accepted  atomic.Uint64
	persisted atomic.Uint64
	mu        sync.RWMutex
	lastError string
}

func newSink(path string) (*sink, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	s := &sink{queue: make(chan workItem, 1024), done: make(chan struct{})}
	go s.writer(file)
	return s, nil
}

func (s *sink) writer(file *os.File) {
	defer close(s.done)
	defer file.Close()
	buffer := bufio.NewWriterSize(file, 64*1024)
	for first := range s.queue {
		batch := []workItem{first}
		deadline := time.NewTimer(25 * time.Millisecond)
	collect:
		for len(batch) < batchSize {
			select {
			case item, ok := <-s.queue:
				if !ok {
					break collect
				}
				batch = append(batch, item)
			case <-deadline.C:
				break collect
			}
		}
		if !deadline.Stop() {
			select {
			case <-deadline.C:
			default:
			}
		}
		var writeErr error
		for _, item := range batch {
			encoded, err := json.Marshal(item.row)
			if err == nil {
				_, err = buffer.Write(append(encoded, '\n'))
			}
			if err != nil && writeErr == nil {
				writeErr = err
			}
		}
		if writeErr == nil {
			writeErr = buffer.Flush()
		}
		if writeErr == nil {
			writeErr = file.Sync()
		}
		if writeErr != nil {
			s.mu.Lock()
			s.lastError = "batch_write_failed"
			s.mu.Unlock()
		} else {
			s.persisted.Add(uint64(len(batch)))
		}
		for _, item := range batch {
			item.ack <- writeErr
			close(item.ack)
		}
	}
	_ = buffer.Flush()
	_ = file.Sync()
}

func (s *sink) submit(ctx context.Context, row safeRow) error {
	item := workItem{row: row, ack: make(chan error, 1)}
	select {
	case s.queue <- item:
		s.accepted.Add(1)
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-item.ack:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *sink) close() {
	close(s.queue)
	<-s.done
}

func (s *sink) health() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{
		"status":            "ok",
		"accepted_batches":  s.accepted.Load(),
		"persisted_batches": s.persisted.Load(),
		"queue_depth":       len(s.queue),
		"last_error_class":  s.lastError,
	}
}

func countRecords(request exportLogsRequest) int {
	count := 0
	for _, resource := range request.ResourceLogs {
		for _, scope := range resource.ScopeLogs {
			count += len(scope.LogRecords)
		}
	}
	return count
}

func main() {
	path := os.Getenv("SINK_PATH")
	if path == "" {
		path = "/data/batches.jsonl"
	}
	port := 8080
	if raw := os.Getenv("PORT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			log.Fatal("invalid PORT")
		}
		port = parsed
	}

	batchSink, err := newSink(path)
	if err != nil {
		log.Fatal("sink_open_failed")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(batchSink.health())
	})
	mux.HandleFunc("POST /v1/logs", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "unsupported_content_type", http.StatusUnsupportedMediaType)
			return
		}
		reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		defer reader.Close()
		decoder := json.NewDecoder(reader)
		decoder.DisallowUnknownFields()
		var request exportLogsRequest
		if err := decoder.Decode(&request); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(w, "payload_too_large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "invalid_or_unknown_schema", http.StatusBadRequest)
			}
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "trailing_json", http.StatusBadRequest)
			return
		}
		count := countRecords(request)
		if count < 1 || count > maxLogRecords {
			http.Error(w, "invalid_record_count", http.StatusBadRequest)
			return
		}
		row := safeRow{
			ReceivedAt:        time.Now().UTC().Format(time.RFC3339Nano),
			Route:             "otlp_http_json_logs",
			RecordCount:       count,
			BodyBytes:         r.ContentLength,
			SchemaFingerprint: "spike.otlp-json-safe-counts/1",
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := batchSink.submit(ctx, row); err != nil {
			http.Error(w, "durable_batch_failed", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	})

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      6 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print("http_server_failed")
	}
	batchSink.close()
}
