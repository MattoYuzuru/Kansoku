package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	h, err := NewHandler([]byte("read-bearer-token-value-000000000000"), []byte("csrf-token-value-1111111111111111111"))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func TestIndexInjectsReadTokenNotMutation(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	html := string(body)
	if !strings.Contains(html, `content="read-bearer-token-value-000000000000"`) {
		t.Errorf("read token not injected into index meta tag")
	}
	if !strings.Contains(html, `content="csrf-token-value-1111111111111111111"`) {
		t.Errorf("csrf token not injected into index meta tag")
	}
	// The index must never be cached (carries per-request secrets).
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("index Cache-Control = %q, want no-store", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("index Content-Type = %q", ct)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("missing CSP: %q", csp)
	}
}

func TestIndexCSPUsesPerRequestNonceNotUnsafeInline(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("script-src still carries 'unsafe-inline': %q", csp)
	}
	start := strings.Index(csp, "'nonce-")
	if start == -1 {
		t.Fatalf("script-src missing a nonce token: %q", csp)
	}
	valueStart := start + len("'nonce-")
	end := strings.Index(csp[valueStart:], "'")
	if end == -1 {
		t.Fatalf("malformed nonce token in CSP: %q", csp)
	}
	nonce := csp[valueStart : valueStart+end]
	if nonce == "" {
		t.Fatal("empty nonce token")
	}

	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `nonce="`+nonce+`"`) {
		t.Errorf("rendered script nonce attribute does not match CSP header nonce %q", nonce)
	}

	// A second request must mint a different nonce.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Header().Get("Content-Security-Policy") == csp {
		t.Error("expected a fresh nonce per request, got an identical CSP header")
	}
}

func TestSPAFallbackServesIndexForDeepLink(t *testing.T) {
	h := newTestHandler(t)
	// A client-side route that is not a real asset must fall back to index.
	req := httptest.NewRequest(http.MethodGet, "/agents/opaque-alias", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deep-link status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `<div id="root">`) {
		t.Errorf("deep-link did not serve the SPA index document")
	}
}

func TestStaticAssetServedWithImmutableCache(t *testing.T) {
	h := newTestHandler(t)
	// Discover a hashed asset name from the embedded FS.
	entries, err := distFS.ReadDir("dist/assets")
	if err != nil {
		t.Fatalf("read embedded assets: %v", err)
	}
	var jsName string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".js") {
			jsName = e.Name()
			break
		}
	}
	if jsName == "" {
		t.Fatal("no embedded js asset found")
	}
	req := httptest.NewRequest(http.MethodGet, "/assets/"+jsName, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("asset Cache-Control = %q, want immutable", cc)
	}
}

func TestFontsEmbedded(t *testing.T) {
	h := newTestHandler(t)
	for _, name := range []string{"/fonts/Inter-Variable.woff2", "/fonts/JetBrainsMono-Variable.woff2"} {
		req := httptest.NewRequest(http.MethodGet, name, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", name, rec.Code)
		}
	}
}

func TestFaviconAssetsEmbedded(t *testing.T) {
	h := newTestHandler(t)
	for _, name := range []string{
		"/favicon.ico", "/favicon-16.png", "/favicon-32.png",
		"/favicon.svg", "/apple-touch-icon.png", "/mask-icon.svg", "/site.webmanifest",
	} {
		req := httptest.NewRequest(http.MethodGet, name, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", name, rec.Code)
		}
	}
}

func TestRejectsNonGet(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
}

func TestNewHandlerRejectsEmptyCredentials(t *testing.T) {
	if _, err := NewHandler(nil, []byte("x")); err == nil {
		t.Error("expected error for empty read bearer")
	}
	if _, err := NewHandler([]byte("x"), nil); err == nil {
		t.Error("expected error for empty csrf")
	}
}
