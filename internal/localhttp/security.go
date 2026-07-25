package localhttp

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type RouteMode string

const (
	RouteUIStream                    RouteMode = "ui_stream"
	RouteHookOTLP                    RouteMode = "hook_otlp"
	RouteUIMutation                  RouteMode = "ui_mutation"
	DeploymentContractSemanticSHA256           = "aae9dd52465391d01140d2886430f3ae4b4af082de24e5359a2d8e8103d43fca"
)

var canonicalHosts = map[string]struct{}{"127.0.0.1": {}, "::1": {}, "localhost": {}}
var canonicalOrigins = map[string]struct{}{"http://127.0.0.1:3000": {}, "http://[::1]:3000": {}, "http://localhost:3000": {}}
var forwardedHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"}

type Guard struct {
	allowedHosts         map[string]struct{}
	allowedOrigins       map[string]struct{}
	bearerDigest         [sha256.Size]byte
	csrfDigest           [sha256.Size]byte
	maxBodyBytes         int64
	requests             int
	window               time.Duration
	uiPort               string
	ingressPort          string
	allowContainerBridge bool
	now                  func() time.Time
	mu                   sync.Mutex
	counters             map[string]counter
}

type counter struct {
	started time.Time
	count   int
}

func NewGuard(hosts, origins []string, bearer, csrf []byte, maxBodyBytes int64, requests int, window time.Duration) (*Guard, error) {
	if !exactSet(hosts, canonicalHosts) || !exactSet(origins, canonicalOrigins) || len(bearer) < 32 || len(csrf) < 32 || subtle.ConstantTimeCompare(bearer, csrf) == 1 || maxBodyBytes != 1<<20 || requests != 120 || window != time.Minute {
		return nil, errors.New("invalid_local_http_security_configuration")
	}
	for _, origin := range origins {
		if !canonicalOrigin(origin) {
			return nil, errors.New("invalid_local_http_security_configuration")
		}
	}
	return newGuard(canonicalOrigins, bearer, csrf, maxBodyBytes, requests, window, "3000", "4318", false), nil
}

// NewApplianceGuard is the Session 09 construction path. Container bridge
// peers are accepted only when the caller explicitly selects appliance mode;
// Host remains an exact loopback name, forwarded headers remain forbidden,
// and route credentials are still mandatory.
func NewApplianceGuard(bearer, csrf []byte, maxBodyBytes int64, requests int, window time.Duration, allowContainerBridge bool) (*Guard, error) {
	if len(bearer) < 32 || len(csrf) < 32 || subtle.ConstantTimeCompare(bearer, csrf) == 1 ||
		maxBodyBytes != 1<<20 || requests != 120 || window != time.Minute {
		return nil, errors.New("invalid_local_http_security_configuration")
	}
	origins := map[string]struct{}{
		"http://127.0.0.1:43100": {},
		"http://[::1]:43100":     {},
		"http://localhost:43100": {},
	}
	return newGuard(origins, bearer, csrf, maxBodyBytes, requests, window, "43100", "4318", allowContainerBridge), nil
}

func newGuard(origins map[string]struct{}, bearer, csrf []byte, maxBodyBytes int64, requests int, window time.Duration, uiPort, ingressPort string, allowContainerBridge bool) *Guard {
	return &Guard{
		allowedHosts: copySet(canonicalHosts), allowedOrigins: copySet(origins),
		bearerDigest: sha256.Sum256(bearer), csrfDigest: sha256.Sum256(csrf),
		maxBodyBytes: maxBodyBytes, requests: requests, window: window,
		uiPort: uiPort, ingressPort: ingressPort, allowContainerBridge: allowContainerBridge,
		now: time.Now, counters: map[string]counter{},
	}
}

func (g *Guard) SetClockForTest(now func() time.Time) { g.now = now }

func (g *Guard) Wrap(mode RouteMode, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		reject := func(reason string, status int) {
			http.Error(writer, reason, status)
		}
		setSecurityHeaders(writer.Header())
		if !validModeMethod(mode, request.Method) {
			writer.Header().Set("Allow", allowedMethods(mode))
			reject("method_not_allowed", http.StatusMethodNotAllowed)
			return
		}
		for _, header := range forwardedHeaders {
			if request.Header.Get(header) != "" {
				reject("forwarded_headers_rejected", http.StatusBadRequest)
				return
			}
		}
		peer := g.canonicalPeerIP(request.RemoteAddr)
		if peer == "" {
			reject("forbidden_peer", http.StatusForbidden)
			return
		}
		host, ok := g.canonicalRequestHost(request.Host)
		if !ok {
			reject("invalid_host", http.StatusMisdirectedRequest)
			return
		}
		if _, allowed := g.allowedHosts[host]; !allowed {
			reject("invalid_host", http.StatusMisdirectedRequest)
			return
		}
		if !g.hostMatchesMode(mode, request.Host) {
			reject("invalid_route_host", http.StatusMisdirectedRequest)
			return
		}
		origin := request.Header.Get("Origin")
		switch mode {
		case RouteHookOTLP:
			if origin != "" {
				reject("origin_not_allowed", http.StatusForbidden)
				return
			}
		case RouteUIMutation:
			if !g.validOrigin(origin, request.Host) {
				reject("invalid_origin", http.StatusForbidden)
				return
			}
		case RouteUIStream:
			if origin != "" && !g.validOrigin(origin, request.Host) {
				reject("invalid_origin", http.StatusForbidden)
				return
			}
		}
		if origin != "" {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Vary", "Origin")
		}
		if !g.allow(peer) {
			reject("rate_limited", http.StatusTooManyRequests)
			return
		}
		providedBearer, ok := bearerValue(request.Header.Get("Authorization"))
		if !ok || !constantDigestEqual(providedBearer, g.bearerDigest) {
			reject("authentication_required", http.StatusUnauthorized)
			return
		}
		if mode == RouteUIMutation && !constantDigestEqual([]byte(request.Header.Get("X-Kansoku-CSRF")), g.csrfDigest) {
			reject("csrf_required", http.StatusForbidden)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, g.maxBodyBytes)
		next.ServeHTTP(writer, request)
	})
}

func (g *Guard) validOrigin(origin, requestHost string) bool {
	if _, ok := g.allowedOrigins[origin]; !ok {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	originHost, ok := g.canonicalRequestHost(parsed.Host)
	if !ok {
		return false
	}
	requestName, ok := g.canonicalRequestHost(requestHost)
	return ok && originHost == requestName && parsed.Host == requestHost
}

func (g *Guard) allow(peer string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	value := g.counters[peer]
	if value.started.IsZero() || now.Sub(value.started) >= g.window {
		value = counter{started: now}
	}
	value.count++
	g.counters[peer] = value
	return value.count <= g.requests
}

func setSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Cache-Control", "no-store")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}

func validModeMethod(mode RouteMode, method string) bool {
	switch mode {
	case RouteUIStream:
		return method == http.MethodGet || method == http.MethodHead
	case RouteHookOTLP:
		return method == http.MethodPost
	case RouteUIMutation:
		return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
	default:
		return false
	}
}

func allowedMethods(mode RouteMode) string {
	switch mode {
	case RouteUIStream:
		return "GET, HEAD"
	case RouteHookOTLP:
		return "POST"
	case RouteUIMutation:
		return "POST, PUT, PATCH, DELETE"
	default:
		return ""
	}
}

func canonicalRequestHost(value string) (string, bool) {
	return canonicalRequestHostFor(value, map[string]bool{"3000": true, "4318": true})
}

func (g *Guard) canonicalRequestHost(value string) (string, bool) {
	return canonicalRequestHostFor(value, map[string]bool{g.uiPort: true, g.ingressPort: true})
}

func canonicalRequestHostFor(value string, ports map[string]bool) (string, bool) {
	if value == "" || strings.ContainsAny(value, "@/\\ \t\r\n") {
		return "", false
	}
	if value == "127.0.0.1" || value == "localhost" {
		return value, true
	}
	if value == "[::1]" {
		return "::1", true
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || port == "" || !ports[port] {
		return "", false
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return "", false
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(value, "[") {
		return "", false
	}
	return host, true
}

func hostMatchesMode(mode RouteMode, host string) bool {
	return hostMatchesModeFor(mode, host, "3000", "4318")
}

func (g *Guard) hostMatchesMode(mode RouteMode, host string) bool {
	return hostMatchesModeFor(mode, host, g.uiPort, g.ingressPort)
}

func hostMatchesModeFor(mode RouteMode, host, uiPort, ingressPort string) bool {
	wantedPort := uiPort
	if mode == RouteHookOTLP {
		wantedPort = ingressPort
	}
	parsedHost, port, err := net.SplitHostPort(host)
	if err != nil || port != wantedPort {
		return false
	}
	return parsedHost == "127.0.0.1" || parsedHost == "localhost" || parsedHost == "::1"
}

func canonicalPeerIP(value string) string {
	host, port, err := net.SplitHostPort(value)
	if err != nil || port == "" {
		return ""
	}
	if host != "127.0.0.1" && host != "::1" {
		return ""
	}
	return host
}

func (g *Guard) canonicalPeerIP(value string) string {
	host, port, err := net.SplitHostPort(value)
	if err != nil || port == "" {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	if ip.IsLoopback() && (host == "127.0.0.1" || host == "::1") {
		return host
	}
	if g.allowContainerBridge && ip.IsPrivate() {
		return host
	}
	return ""
}

func canonicalOrigin(value string) bool { _, ok := canonicalOrigins[value]; return ok }

func bearerValue(header string) ([]byte, bool) {
	if !strings.HasPrefix(header, "Bearer ") || strings.Count(header, " ") != 1 {
		return nil, false
	}
	value := strings.TrimPrefix(header, "Bearer ")
	return []byte(value), value != ""
}

func constantDigestEqual(value []byte, expected [sha256.Size]byte) bool {
	digest := sha256.Sum256(value)
	return subtle.ConstantTimeCompare(digest[:], expected[:]) == 1
}

func exactSet(values []string, expected map[string]struct{}) bool {
	if len(values) != len(expected) {
		return false
	}
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, ok := expected[value]; !ok {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func copySet(values map[string]struct{}) map[string]struct{} {
	result := map[string]struct{}{}
	for value := range values {
		result[value] = struct{}{}
	}
	return result
}
