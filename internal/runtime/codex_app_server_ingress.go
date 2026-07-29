package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/codexadapter"
	"kansoku.local/kansoku/internal/observability"
)

const codexAppServerInstallationHeader = "X-Kansoku-Agent-Installation"

var codexAppServerInstallationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/|-]{0,127}$`)

// CodexAppServerIngress supervises explicitly routed App Server JSONL
// streams inside the normal appliance process. It never discovers, launches,
// configures, or writes to Codex; raw frames are consumed transiently by the
// adapter bridge and have no persistence surface.
type CodexAppServerIngress struct {
	pool     *pgxpool.Pool
	ingestor *observability.Ingestor
	key      []byte
	now      func() time.Time
}

func NewCodexAppServerIngress(
	pool *pgxpool.Pool,
	ingestor *observability.Ingestor,
	key []byte,
	now func() time.Time,
) (*CodexAppServerIngress, error) {
	if pool == nil || ingestor == nil || len(key) < 32 {
		return nil, errors.New("invalid_codex_app_server_ingress_configuration")
	}
	if now == nil {
		now = time.Now
	}
	return &CodexAppServerIngress{
		pool: pool, ingestor: ingestor, key: append([]byte(nil), key...), now: now,
	}, nil
}

func (i *CodexAppServerIngress) Configure(ctx context.Context) error {
	return i.persistSourceHealth(ctx, "configured", "not_observed", "")
}

func (i *CodexAppServerIngress) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	installationID := request.Header.Get(codexAppServerInstallationHeader)
	if !codexAppServerInstallationPattern.MatchString(installationID) {
		http.Error(writer, "invalid_evidence_bridge_installation", http.StatusBadRequest)
		return
	}
	bridge, err := codexadapter.NewAppServerBridge(i.key, i.now)
	if err != nil {
		http.Error(writer, "evidence_bridge_unavailable", http.StatusServiceUnavailable)
		return
	}
	sink, err := observability.NewBridgeAssertionSinkForInstallation(
		i.ingestor, installationID,
	)
	if err != nil {
		http.Error(writer, "evidence_bridge_unavailable", http.StatusServiceUnavailable)
		return
	}
	bridgeCtx, cancel := context.WithTimeout(
		request.Context(), bridge.Manifest().ConnectTimeout,
	)
	defer cancel()
	connectResult := make(chan error, 1)
	go func() {
		connectResult <- bridge.Connect(bridgeCtx, adaptersdk.BridgeTarget{
			Installation: adaptersdk.Installation{
				InstallationID: installationID,
				AdapterID:      codexadapter.AdapterID,
			},
			Protocol:      codexadapter.AppServerProtocolVersion,
			SchemaVersion: codexadapter.AppServerSchemaVersion,
			Frames:        request.Body,
		}, sink)
	}()
	select {
	case err = <-connectResult:
	case <-bridgeCtx.Done():
		_ = request.Body.Close()
		err = bridgeCtx.Err()
	}
	health := bridge.Health(request.Context())
	if err != nil {
		errorClass := "bridge_stream_rejected"
		status := http.StatusUnprocessableEntity
		if errors.Is(err, context.DeadlineExceeded) {
			errorClass = "bridge_stream_timeout"
			status = http.StatusRequestTimeout
		} else if errors.Is(err, observability.ErrBackpressure) {
			errorClass = "bridge_backpressure"
			status = http.StatusTooManyRequests
			writer.Header().Set("Retry-After", "1")
		} else if errors.Is(err, observability.ErrDurabilityUnavailable) {
			errorClass = "bridge_durability_unavailable"
			status = http.StatusServiceUnavailable
			writer.Header().Set("Retry-After", "1")
		}
		_ = i.persistSourceHealth(request.Context(), "degraded", bridgeValueState(health), errorClass)
		http.Error(writer, errorClass, status)
		return
	}
	state := "configured"
	valueState := "not_observed"
	errorClass := ""
	if health.AcceptedFrames > 0 {
		state = "producing"
		valueState = "observed"
	}
	if health.RejectedFrames > 0 {
		state = "degraded"
		errorClass = "bridge_frames_rejected"
		if health.AcceptedFrames == 0 {
			valueState = "unknown"
		}
	}
	if err := i.persistSourceHealth(request.Context(), state, valueState, errorClass); err != nil {
		http.Error(writer, "bridge_health_persistence_unavailable", http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"accepted_records": health.AcceptedFrames,
		"rejected_frames":  health.RejectedFrames,
		"lifecycle":        health.Lifecycle,
		"last_observed_at": nullableTime(health.LastObservedAt),
		"source_state":     state,
		"value_state":      valueState,
	})
}

func bridgeValueState(health adaptersdk.BridgeHealth) string {
	if health.AcceptedFrames > 0 {
		return "observed"
	}
	return "unknown"
}

func (i *CodexAppServerIngress) persistSourceHealth(
	ctx context.Context,
	state, valueState, errorClass string,
) error {
	var successfulAt any
	if state == "producing" {
		successfulAt = i.now().UTC()
	}
	_, err := i.pool.Exec(ctx, `
		INSERT INTO runtime_source_health (
			source_id,state,value_state,last_attempted_at,last_successful_at,
			last_error_class,updated_at
		) VALUES ('codex.app_server',$1,$2,now(),$3,NULLIF($4,''),now())
		ON CONFLICT (source_id) DO UPDATE SET
			state=EXCLUDED.state, value_state=EXCLUDED.value_state,
			last_attempted_at=EXCLUDED.last_attempted_at,
			last_successful_at=COALESCE(
				EXCLUDED.last_successful_at,
				runtime_source_health.last_successful_at
			),
			last_error_class=EXCLUDED.last_error_class,
			updated_at=now()
	`, state, valueState, successfulAt, errorClass)
	return err
}
