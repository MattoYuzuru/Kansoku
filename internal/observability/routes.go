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
	"kansoku.local/kansoku/internal/localhttp"
)

func NewIngressHTTPHandler(guard *localhttp.Guard, ingestor *Ingestor, otlp *OTLPReceiver) (http.Handler, error) {
	if guard == nil || ingestor == nil || otlp == nil {
		return nil, errors.New("invalid_ingress_http_configuration")
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/logs", otlp.HTTPMux())
	mux.Handle("/v1/metrics", otlp.HTTPMux())
	mux.Handle("/v1/traces", otlp.HTTPMux())
	mux.HandleFunc("/v1/hooks/fixture-agent/tool_finished", func(writer http.ResponseWriter, request *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(request.Body, 1<<20+1))
		if err != nil || len(raw) > 1<<20 {
			http.Error(writer, "oversized_input", http.StatusRequestEntityTooLarge)
			return
		}
		result, err := ingestor.IngestHook(raw, 0)
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
	})
	return guard.Wrap(localhttp.RouteHookOTLP, mux), nil
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
