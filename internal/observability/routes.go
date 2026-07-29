package observability

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"kansoku.local/kansoku/internal/claudeadapter"
	"kansoku.local/kansoku/internal/codexadapter"
	"kansoku.local/kansoku/internal/localhttp"
	"kansoku.local/kansoku/internal/privacy"
)

// maxHookBodyBytes bounds every hook_http request body, matching
// contracts/observability/ingress.yaml's limits.max_frame_bytes. It is the
// one bound every adapter's hook route shares; no adapter-specific route
// gets a looser or tighter ceiling.
const maxHookBodyBytes = 1 << 20

// NewIngressHTTPHandler wires the single hook_http mux every adapter's hook
// events are served through. contracts/observability/ingress.yaml declares
// one generic route template, "/v1/hooks/{adapter}/{event}"; this mux
// pattern-matches that template literally via net/http's {adapter}/{event}
// wildcards and dispatches on the resolved adapter path segment, so adding a
// new adapter's hook events (Codex here; Claude/Gemini/others later) means
// adding a case to hookAdapterHandler, never registering a second HTTP
// server, a second auth mechanism, or a second literal path per event.
func NewIngressHTTPHandler(guard *localhttp.Guard, ingestor *Ingestor, otlp *OTLPReceiver) (http.Handler, error) {
	return NewIngressHTTPHandlerWithEvidenceBridge(guard, ingestor, otlp, nil)
}

// NewIngressHTTPHandlerWithEvidenceBridge adds the supervised, explicitly
// routed evidence-bridge lane to the same authenticated ingress listener as
// hooks and OTLP. The supplied handler receives an already authenticated,
// body-bounded POST request; it cannot introduce another listener or secret.
func NewIngressHTTPHandlerWithEvidenceBridge(
	guard *localhttp.Guard,
	ingestor *Ingestor,
	otlp *OTLPReceiver,
	codexAppServer http.Handler,
) (http.Handler, error) {
	if guard == nil || ingestor == nil || otlp == nil {
		return nil, errors.New("invalid_ingress_http_configuration")
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/logs", otlp.HTTPMux())
	mux.Handle("/v1/metrics", otlp.HTTPMux())
	mux.Handle("/v1/traces", otlp.HTTPMux())
	mux.HandleFunc("/v1/hooks/{adapter}/{event}", func(writer http.ResponseWriter, request *http.Request) {
		hookAdapterHandler(writer, request, ingestor)
	})
	mux.HandleFunc("/v1/adapter-events/{adapter}", func(writer http.ResponseWriter, request *http.Request) {
		adapterBatchHandler(writer, request, ingestor)
	})
	if codexAppServer != nil {
		mux.Handle("/v1/evidence-bridges/codex-app-server", codexAppServer)
	}
	return guard.Wrap(localhttp.RouteHookOTLP, mux), nil
}

const adapterBatchVersion = "kansoku.adapter-event-batch/1"

type adapterBatchRequest struct {
	SchemaVersion string               `json:"schema_version"`
	Records       []privacy.SafeRecord `json:"records"`
}

func adapterBatchHandler(writer http.ResponseWriter, request *http.Request, ingestor *Ingestor) {
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxHookBodyBytes+1))
	decoder.DisallowUnknownFields()
	var batch adapterBatchRequest
	if err := decoder.Decode(&batch); err != nil {
		http.Error(writer, "invalid_adapter_batch", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) ||
		batch.SchemaVersion != adapterBatchVersion ||
		len(batch.Records) == 0 || len(batch.Records) > 128 {
		http.Error(writer, "invalid_adapter_batch", http.StatusBadRequest)
		return
	}
	adapterID := request.PathValue("adapter")
	for _, record := range batch.Records {
		if record.AdapterID != adapterID || validateSanitizedAdapterRecord(record) != nil {
			http.Error(writer, "invalid_adapter_batch", http.StatusBadRequest)
			return
		}
	}
	duplicates := 0
	for index, record := range batch.Records {
		result, err := ingestor.IngestSanitizedAdapterRecord(record, uint64(index+1))
		if err != nil {
			writeHookIngestResult(writer, result, err)
			return
		}
		if result.DuplicateReplay {
			duplicates++
		}
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"accepted": true, "accepted_count": len(batch.Records),
		"duplicate_count": duplicates,
	})
}

// hookAdapterHandler resolves the {adapter} path segment matched by the
// generic hook_http route and dispatches to that adapter's hook decoding.
// An adapter segment this repository does not recognize is rejected
// visibly (404) rather than silently accepted and dropped.
func hookAdapterHandler(writer http.ResponseWriter, request *http.Request, ingestor *Ingestor) {
	switch request.PathValue("adapter") {
	case "fixture-agent":
		fixtureAgentHookHandler(writer, request, ingestor)
	case codexadapter.AdapterID:
		codexHookHandler(writer, request, ingestor)
	case claudeadapter.AdapterID:
		claudeHookHandler(writer, request, ingestor)
	default:
		http.Error(writer, "unknown_hook_adapter", http.StatusNotFound)
	}
}

// fixtureAgentHookHandler preserves the exact fixture-agent conformance
// route (source kind hook_http, event tool_finished) the Session 03
// ingestion pipeline was built and tested against; the {adapter}/{event}
// generalization does not change fixture-agent's own behavior.
func fixtureAgentHookHandler(writer http.ResponseWriter, request *http.Request, ingestor *Ingestor) {
	if request.PathValue("event") != "tool_finished" {
		http.Error(writer, "unknown_hook_event", http.StatusNotFound)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxHookBodyBytes+1))
	if err != nil || len(raw) > maxHookBodyBytes {
		http.Error(writer, "oversized_input", http.StatusRequestEntityTooLarge)
		return
	}
	result, err := ingestor.IngestHook(raw, 0)
	writeHookIngestResult(writer, result, err)
}

// codexHookHandler serves every codex.hook event
// contracts/codex/hooks-and-otel.yaml declares through this same generic
// mux. It never accepts an event outside that closed, version-manifested
// vocabulary, decodes stdin-shaped input with unknown-field rejection,
// computes prompt features in memory only, and re-validates the resulting
// output's field allowlist before ever forwarding it to the Ingestor -- the
// same durable trust boundary every other hook_http source crosses, not a
// parallel one.
func codexHookHandler(writer http.ResponseWriter, request *http.Request, ingestor *Ingestor) {
	event := codexadapter.HookEvent(request.PathValue("event"))
	input, err := codexadapter.DecodeHookInput(io.LimitReader(request.Body, maxHookBodyBytes+1))
	if err != nil {
		if errors.Is(err, codexadapter.ErrOversizedHookInput) {
			http.Error(writer, "oversized_input", http.StatusRequestEntityTooLarge)
			return
		}
		if errors.Is(err, codexadapter.ErrUnsupportedHookEvent) {
			http.Error(writer, "unsupported_hook_event", http.StatusBadRequest)
			return
		}
		http.Error(writer, "invalid_hook", http.StatusBadRequest)
		return
	}
	if input.Event != event {
		http.Error(writer, "hook_event_path_mismatch", http.StatusBadRequest)
		return
	}
	output, err := codexadapter.BuildHookOutput(input, ingestor.now())
	if err != nil {
		http.Error(writer, "invalid_hook", http.StatusBadRequest)
		return
	}
	if err := codexadapter.ValidateHookOutputAllowlist(output); err != nil {
		http.Error(writer, "hook_output_not_allowlisted", http.StatusInternalServerError)
		return
	}
	result, err := ingestor.IngestSafeHookFields(codexHookSafeFields(output), codexadapter.AdapterID, 0)
	writeHookIngestResult(writer, result, err)
}

// claudeHookHandler serves every claude.hook event
// contracts/claude/hooks-and-otel.yaml declares through this same generic
// mux, never a parallel ingress mechanism and never colliding with the
// reserved "fixture-agent" adapter path segment or with codex's own case. It
// decodes stdin-shaped input with unknown-field rejection, computes prompt
// features in memory only, pseudonymizes transcript_path/cwd with the same
// device-scoped HMAC key the Ingestor already carries for identity
// pseudonymization (internal/privacy's own trust boundary, not a second key
// or mechanism), and re-validates the resulting output's field allowlist
// before ever forwarding it to the Ingestor.
func claudeHookHandler(writer http.ResponseWriter, request *http.Request, ingestor *Ingestor) {
	event := claudeadapter.HookEvent(request.PathValue("event"))
	input, err := claudeadapter.DecodeHookInput(io.LimitReader(request.Body, maxHookBodyBytes+1))
	if err != nil {
		if errors.Is(err, claudeadapter.ErrOversizedHookInput) {
			http.Error(writer, "oversized_input", http.StatusRequestEntityTooLarge)
			return
		}
		if errors.Is(err, claudeadapter.ErrUnsupportedHookEvent) {
			http.Error(writer, "unsupported_hook_event", http.StatusBadRequest)
			return
		}
		http.Error(writer, "invalid_hook", http.StatusBadRequest)
		return
	}
	if input.Event != event {
		http.Error(writer, "hook_event_path_mismatch", http.StatusBadRequest)
		return
	}
	output, err := claudeadapter.BuildHookOutput(input, ingestor.identityKey, ingestor.now())
	if err != nil {
		http.Error(writer, "invalid_hook", http.StatusBadRequest)
		return
	}
	if err := claudeadapter.ValidateHookOutputAllowlist(output); err != nil {
		http.Error(writer, "hook_output_not_allowlisted", http.StatusInternalServerError)
		return
	}
	result, err := ingestor.IngestSafeHookFields(claudeHookSafeFields(output), claudeadapter.AdapterID, 0)
	writeHookIngestResult(writer, result, err)
}

func hookOutcome(status string) string {
	switch strings.ToLower(status) {
	case "success", "succeeded", "ok", "completed":
		return "succeeded"
	case "failure", "failed", "error":
		return "failed"
	case "cancelled", "interrupted", "timed_out", "abandoned":
		return strings.ToLower(status)
	default:
		return "unknown"
	}
}

func addOptionalHookField(fields map[string]any, key string, value any) {
	switch typed := value.(type) {
	case string:
		if typed != "" {
			fields[key] = typed
		}
	case int64:
		if typed != 0 {
			fields[key] = typed
		}
	}
}

func codexHookSafeFields(output codexadapter.HookHelperOutput) map[string]any {
	fields := map[string]any{
		"event_id": output.EventID, "session_id": output.SessionID,
		"observed_at": output.ObservedAt, "event_type": output.EventType,
		"outcome": hookOutcome(output.ToolStatus), "value_state": "observed",
	}
	addOptionalHookField(fields, "turn_id", output.TurnID)
	addOptionalHookField(fields, "model", output.ModelID)
	addOptionalHookField(fields, "tool_name", output.ToolID)
	addOptionalHookField(fields, "duration_ms", output.TimingMS)
	if output.PromptFeatures != nil {
		fields["prompt_character_count"] = int64(output.PromptFeatures.CharacterCount)
	}
	return fields
}

func claudeHookSafeFields(output claudeadapter.HookHelperOutput) map[string]any {
	fields := map[string]any{
		"event_id": output.EventID, "session_id": output.SessionID,
		"observed_at": output.ObservedAt, "event_type": output.EventType,
		"outcome": hookOutcome(output.ToolStatus), "value_state": "observed",
	}
	addOptionalHookField(fields, "turn_id", output.TurnID)
	addOptionalHookField(fields, "model", output.ModelID)
	addOptionalHookField(fields, "tool_name", output.ToolID)
	addOptionalHookField(fields, "duration_ms", output.TimingMS)
	if output.PromptFeatures != nil {
		fields["prompt_character_count"] = int64(output.PromptFeatures.CharacterCount)
	}
	return fields
}

func writeHookIngestResult(writer http.ResponseWriter, result CommitResult, err error) {
	if err != nil {
		if errors.Is(err, ErrBackpressure) {
			writer.Header().Set("Retry-After", "1")
			http.Error(writer, "backpressure_retryable", http.StatusTooManyRequests)
			return
		}
		if errors.Is(err, ErrDurabilityUnavailable) {
			writer.Header().Set("Retry-After", "1")
			http.Error(writer, "durability_unavailable_retryable", http.StatusServiceUnavailable)
			return
		}
		http.Error(writer, "invalid_hook", http.StatusBadRequest)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(map[string]any{"accepted": true, "revision": result.Revision, "duplicate": result.DuplicateReplay})
}

// NewIngressGRPCServer is the only exported construction path for the
// production OTLP/gRPC services. Authentication and both message ceilings are
// inseparable from service registration.
func NewIngressGRPCServer(receiver *OTLPReceiver, bearer []byte) (*grpc.Server, error) {
	return newIngressGRPCServer(receiver, bearer, false)
}

// NewApplianceIngressGRPCServer permits an RFC1918 container-bridge peer only
// in explicit appliance mode. Authentication and message ceilings remain
// identical to the loopback-only constructor.
func NewApplianceIngressGRPCServer(receiver *OTLPReceiver, bearer []byte, allowContainerBridge bool) (*grpc.Server, error) {
	return newIngressGRPCServer(receiver, bearer, allowContainerBridge)
}

func newIngressGRPCServer(receiver *OTLPReceiver, bearer []byte, allowContainerBridge bool) (*grpc.Server, error) {
	if receiver == nil || receiver.maxBytes != maxOTLPFrameBytes {
		return nil, errors.New("invalid_grpc_server_configuration")
	}
	interceptor, err := grpcAuthUnaryMode(bearer, allowContainerBridge)
	if err != nil {
		return nil, err
	}
	server := grpc.NewServer(
		grpc.UnaryInterceptor(interceptor),
		grpc.MaxRecvMsgSize(maxOTLPFrameBytes),
		grpc.MaxSendMsgSize(maxOTLPFrameBytes),
	)
	receiver.register(server)
	return server, nil
}

func grpcAuthUnary(bearer []byte) (grpc.UnaryServerInterceptor, error) {
	return grpcAuthUnaryMode(bearer, false)
}

func grpcAuthUnaryMode(bearer []byte, allowContainerBridge bool) (grpc.UnaryServerInterceptor, error) {
	if len(bearer) < 32 {
		return nil, errors.New("invalid_grpc_auth_configuration")
	}
	expected := sha256.Sum256(bearer)
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		remote, ok := peer.FromContext(ctx)
		if !ok || !allowedGRPCPeer(remote.Addr, allowContainerBridge) {
			return nil, status.Error(codes.PermissionDenied, "forbidden_peer")
		}
		values := metadata.ValueFromIncomingContext(ctx, "authorization")
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || strings.Count(values[0], " ") != 1 {
			return nil, status.Error(codes.Unauthenticated, "authentication_required")
		}
		provided := sha256.Sum256([]byte(strings.TrimPrefix(values[0], "Bearer ")))
		if subtle.ConstantTimeCompare(provided[:], expected[:]) != 1 {
			return nil, status.Error(codes.Unauthenticated, "authentication_required")
		}
		return handler(ctx, request)
	}, nil
}

func allowedGRPCPeer(address net.Addr, allowContainerBridge bool) bool {
	if loopbackAddress(address) {
		return true
	}
	if !allowContainerBridge {
		return false
	}
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsPrivate()
}

func loopbackAddress(address net.Addr) bool {
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && (host == "127.0.0.1" || host == "::1")
}
