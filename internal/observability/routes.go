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
	"kansoku.local/kansoku/internal/codexadapter"
	"kansoku.local/kansoku/internal/localhttp"
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
	return guard.Wrap(localhttp.RouteHookOTLP, mux), nil
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
	raw, err := json.Marshal(output)
	if err != nil {
		http.Error(writer, "invalid_hook", http.StatusInternalServerError)
		return
	}
	result, err := ingestor.IngestHook(raw, 0)
	writeHookIngestResult(writer, result, err)
}

func writeHookIngestResult(writer http.ResponseWriter, result CommitResult, err error) {
	if err != nil {
		if errors.Is(err, ErrBackpressure) {
			writer.Header().Set("Retry-After", "1")
			http.Error(writer, "backpressure_retryable", http.StatusServiceUnavailable)
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
	if receiver == nil || receiver.maxBytes != maxOTLPFrameBytes {
		return nil, errors.New("invalid_grpc_server_configuration")
	}
	interceptor, err := grpcAuthUnary(bearer)
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
	if len(bearer) < 32 {
		return nil, errors.New("invalid_grpc_auth_configuration")
	}
	expected := sha256.Sum256(bearer)
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		remote, ok := peer.FromContext(ctx)
		if !ok || !loopbackAddress(remote.Addr) {
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

func loopbackAddress(address net.Addr) bool {
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && (host == "127.0.0.1" || host == "::1")
}
