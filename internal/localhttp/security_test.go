package localhttp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeHTTPPolicyMatchesAuthoritativeDeploymentContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "privacy", "deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		HTTP struct {
			AllowedHosts   []string `json:"allowed_hosts"`
			AllowedOrigins []string `json:"allowed_origins"`
			BearerMin      int      `json:"bearer_secret_min_bytes"`
			CSRFMin        int      `json:"csrf_secret_min_bytes"`
			MaxBody        int64    `json:"max_body_bytes"`
			Rate           int      `json:"requests_per_minute"`
			RouteModes     map[string]struct {
				Methods []string `json:"methods"`
			} `json:"route_modes"`
		} `json:"http"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if !exactSet(contract.HTTP.AllowedHosts, canonicalHosts) || !exactSet(contract.HTTP.AllowedOrigins, canonicalOrigins) || contract.HTTP.BearerMin != 32 || contract.HTTP.CSRFMin != 32 || contract.HTTP.MaxBody != 1<<20 || contract.HTTP.Rate != 120 {
		t.Fatal("runtime HTTP constants drifted")
	}
	modeMap := map[string]RouteMode{"ui_stream": RouteUIStream, "hook_otlp": RouteHookOTLP, "ui_mutation": RouteUIMutation}
	for name, policy := range contract.HTTP.RouteModes {
		mode, ok := modeMap[name]
		if !ok {
			t.Fatalf("unknown route mode %s", name)
		}
		for _, method := range policy.Methods {
			if !validModeMethod(mode, method) {
				t.Fatalf("runtime route method drift %s %s", name, method)
			}
		}
	}
}

var bearer = []byte("bearer-secret-must-be-at-least-32-bytes")
var csrf = []byte("csrf-secret-must-differ-and-be-32-bytes")

func newTestGuard(t *testing.T) *Guard {
	t.Helper()
	guard, err := NewGuard([]string{"127.0.0.1", "::1", "localhost"}, []string{"http://127.0.0.1:3000", "http://[::1]:3000", "http://localhost:3000"}, bearer, csrf, 1<<20, 120, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	guard.SetClockForTest(func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) })
	return guard
}

func handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := io.ReadAll(request.Body); err != nil {
			http.Error(writer, "payload_too_large", http.StatusRequestEntityTooLarge)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
}

func makeRequest(method, host, remote, origin, body string) *http.Request {
	req := httptest.NewRequest(method, "http://127.0.0.1/privacy", strings.NewReader(body))
	req.Host = host
	req.RemoteAddr = remote
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Authorization", "Bearer "+string(bearer))
	return req
}

func serve(t *testing.T, guard *Guard, mode RouteMode, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	guard.Wrap(mode, handler()).ServeHTTP(response, request)
	return response
}

func TestRouteModesAuthenticateUIStreamHookAndMutation(t *testing.T) {
	guard := newTestGuard(t)
	ui := makeRequest(http.MethodGet, "127.0.0.1:3000", "127.0.0.1:50123", "http://127.0.0.1:3000", "")
	if response := serve(t, guard, RouteUIStream, ui); response.Code != http.StatusNoContent {
		t.Fatalf("ui=%d", response.Code)
	}
	hook := makeRequest(http.MethodPost, "127.0.0.1:4318", "127.0.0.1:50124", "", "safe")
	if response := serve(t, guard, RouteHookOTLP, hook); response.Code != http.StatusNoContent {
		t.Fatalf("hook=%d", response.Code)
	}
	mutation := makeRequest(http.MethodPost, "localhost:3000", "[::1]:50125", "http://localhost:3000", "safe")
	if response := serve(t, guard, RouteUIMutation, mutation); response.Code != http.StatusForbidden {
		t.Fatalf("csrf-less=%d", response.Code)
	}
	mutation = makeRequest(http.MethodPost, "localhost:3000", "[::1]:50125", "http://localhost:3000", "safe")
	mutation.Header.Set("X-Kansoku-CSRF", string(csrf))
	response := serve(t, guard, RouteUIMutation, mutation)
	if response.Code != http.StatusNoContent {
		t.Fatalf("mutation=%d", response.Code)
	}
	for _, header := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "X-Frame-Options", "Cross-Origin-Resource-Policy", "Permissions-Policy"} {
		if response.Header().Get(header) == "" {
			t.Errorf("missing %s", header)
		}
	}
}

func TestConstructorAcceptsOnlyExactCanonicalPolicyAndSeparateSecrets(t *testing.T) {
	validHosts := []string{"127.0.0.1", "::1", "localhost"}
	validOrigins := []string{"http://127.0.0.1:3000", "http://[::1]:3000", "http://localhost:3000"}
	cases := []struct {
		name           string
		hosts, origins []string
		b, c           []byte
		max            int64
		rate           int
		window         time.Duration
	}{
		{"evil_host", []string{"127.0.0.1", "::1", "evil"}, validOrigins, bearer, csrf, 1 << 20, 120, time.Minute},
		{"origin_path", validHosts, []string{"http://127.0.0.1:3000/path", "http://[::1]:3000", "http://localhost:3000"}, bearer, csrf, 1 << 20, 120, time.Minute},
		{"same_secret", validHosts, validOrigins, bearer, bearer, 1 << 20, 120, time.Minute},
		{"wrong_limit", validHosts, validOrigins, bearer, csrf, 1, 120, time.Minute},
		{"wrong_rate", validHosts, validOrigins, bearer, csrf, 1 << 20, 1, time.Minute},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if _, err := NewGuard(item.hosts, item.origins, item.b, item.c, item.max, item.rate, item.window); err == nil {
				t.Fatal("accepted unsafe policy")
			}
		})
	}
}

func TestMethodHostPeerOriginForwardedAndAuthAdversarialTable(t *testing.T) {
	tests := []struct {
		name                         string
		mode                         RouteMode
		method, host, remote, origin string
		mutate                       func(*http.Request)
		code                         int
	}{
		{"remote", RouteUIStream, "GET", "127.0.0.1:3000", "192.0.2.1:1", "", nil, http.StatusForbidden},
		{"mapped_loopback", RouteUIStream, "GET", "127.0.0.1:3000", "[::ffff:127.0.0.1]:1", "", nil, http.StatusForbidden},
		{"evil_host", RouteUIStream, "GET", "127.0.0.1.evil:3000", "127.0.0.1:1", "", nil, http.StatusMisdirectedRequest},
		{"host_userinfo", RouteUIStream, "GET", "evil@localhost:3000", "127.0.0.1:1", "", nil, http.StatusMisdirectedRequest},
		{"unbracketed_ipv6", RouteUIStream, "GET", "::1:3000", "[::1]:1", "", nil, http.StatusMisdirectedRequest},
		{"foreign_origin", RouteUIStream, "GET", "localhost:3000", "127.0.0.1:1", "https://evil.invalid", nil, http.StatusForbidden},
		{"hook_origin", RouteHookOTLP, "POST", "127.0.0.1:4318", "127.0.0.1:1", "http://127.0.0.1:4318", nil, http.StatusForbidden},
		{"cross_port", RouteUIStream, "GET", "127.0.0.1:4318", "127.0.0.1:1", "", nil, http.StatusMisdirectedRequest},
		{"connect", RouteUIStream, "CONNECT", "127.0.0.1:3000", "127.0.0.1:1", "", nil, http.StatusMethodNotAllowed},
		{"trace", RouteUIStream, "TRACE", "127.0.0.1:3000", "127.0.0.1:1", "", nil, http.StatusMethodNotAllowed},
		{"custom", RouteHookOTLP, "BREW", "127.0.0.1:4318", "127.0.0.1:1", "", nil, http.StatusMethodNotAllowed},
		{"forwarded", RouteUIStream, "GET", "127.0.0.1:3000", "127.0.0.1:1", "", func(r *http.Request) { r.Header.Set("Forwarded", "for=192.0.2.1") }, http.StatusBadRequest},
		{"missing_auth", RouteUIStream, "GET", "127.0.0.1:3000", "127.0.0.1:1", "", func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
		{"wrong_auth_length", RouteUIStream, "GET", "127.0.0.1:3000", "127.0.0.1:1", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer x") }, http.StatusUnauthorized},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			req := makeRequest(item.method, item.host, item.remote, item.origin, "")
			if item.mutate != nil {
				item.mutate(req)
			}
			response := serve(t, newTestGuard(t), item.mode, req)
			if response.Code != item.code {
				t.Fatalf("code=%d body=%q", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "evil.invalid") || strings.Contains(response.Body.String(), string(bearer)) {
				t.Fatal("response leak")
			}
		})
	}
}

func TestPayloadAndRateLimits(t *testing.T) {
	request := makeRequest(http.MethodPost, "127.0.0.1:4318", "127.0.0.1:44", "", strings.Repeat("x", (1<<20)+1))
	if response := serve(t, newTestGuard(t), RouteHookOTLP, request); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("payload=%d", response.Code)
	}
	guard := newTestGuard(t)
	for index := 0; index < 120; index++ {
		if response := serve(t, guard, RouteUIStream, makeRequest("GET", "127.0.0.1:3000", "127.0.0.1:44", "", "")); response.Code != http.StatusNoContent {
			t.Fatalf("request %d=%d", index, response.Code)
		}
	}
	if response := serve(t, guard, RouteUIStream, makeRequest("GET", "127.0.0.1:3000", "127.0.0.1:44", "", "")); response.Code != http.StatusTooManyRequests {
		t.Fatalf("rate=%d", response.Code)
	}
}
