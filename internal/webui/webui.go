// Package webui serves the Session 10 dashboard single-page app that is built
// under web/ and embedded here as the production web/dist output.
//
// Everything except index.html is served as an immutable static asset. The
// index document is rendered per request as an html/template so the two
// read-only credential meta tags (kansoku-read-token / kansoku-csrf-token) can
// be injected with the live secret values the same Go process holds. The
// mutation bearer is never embedded: the dashboard is read-only (all 14 routes
// are GET analytics views), so the browser needs only the read bearer to call
// the same-origin /api/v1 surface. Any path that is not an embedded file falls
// back to index.html so client-side routing (wouter) survives refresh and
// deep-links.
//
// This package intentionally does not import internal/runtime or its Secrets/
// Config types; the constructor takes the two raw secret byte slices so the
// dependency direction stays runtime -> webui, never the reverse.
package webui

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

const indexName = "index.html"

// Handler serves embedded static assets and the per-request-rendered index.
type Handler struct {
	assets    fs.FS
	fileSrv   http.Handler
	indexTmpl *template.Template
	read      string
	csrf      string
}

// indexModel is what index.html renders against; html/template attribute-
// escapes both values automatically for the meta content="" context.
type indexModel struct {
	ReadToken string
	CSRFToken string
	CSPNonce  string
}

// NewHandler builds the dashboard handler. readBearer is injected into the
// page so the read-only UI can authenticate its /api/v1 GET calls; csrf is
// injected for a future mutation client but is unused by any current route.
// Both must be non-empty. The secret strings live only in memory for the
// process lifetime and are never logged.
func NewHandler(readBearer, csrf []byte) (http.Handler, error) {
	if len(readBearer) == 0 || len(csrf) == 0 {
		return nil, errors.New("webui_missing_credentials")
	}
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, errors.New("webui_embed_unavailable")
	}
	rawIndex, err := fs.ReadFile(sub, indexName)
	if err != nil {
		return nil, errors.New("webui_index_missing")
	}
	tmpl, err := template.New(indexName).Parse(string(rawIndex))
	if err != nil {
		return nil, errors.New("webui_index_template_invalid")
	}
	return &Handler{
		assets:    sub,
		fileSrv:   http.FileServer(http.FS(sub)),
		indexTmpl: tmpl,
		read:      string(readBearer),
		csrf:      string(csrf),
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	clean := cleanPath(r.URL.Path)
	// The index document is always template-rendered so its tokens are fresh
	// and never cached; serve it for the root and for any SPA deep-link that
	// is not a real embedded asset.
	if clean == "" || clean == indexName || !h.assetExists(clean) {
		h.serveIndex(w, r)
		return
	}
	// Real static asset: no inline script/style ever runs here, so no nonce is
	// needed; long-lived immutable cache (filenames are hashed).
	setStaticSecurityHeaders(w.Header(), "")
	setAssetCache(w.Header(), clean)
	h.fileSrv.ServeHTTP(w, r)
}

func (h *Handler) assetExists(name string) bool {
	f, err := h.assets.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.IsDir() {
		return false
	}
	return true
}

func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	nonce, err := newCSPNonce()
	if err != nil {
		http.Error(w, "nonce_generation_failed", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := h.indexTmpl.Execute(&buf, indexModel{ReadToken: h.read, CSRFToken: h.csrf, CSPNonce: nonce}); err != nil {
		http.Error(w, "index_render_failed", http.StatusInternalServerError)
		return
	}
	setStaticSecurityHeaders(w.Header(), nonce)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The index carries per-request secret tokens; it must never be cached.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(buf.Bytes())
}

// newCSPNonce mints a fresh 128-bit random value per request for the CSP
// script-src allowlist, matching the one inline pre-paint theme script in
// index.html. Hex-encoded (not base64): base64's '+' and '/' get
// HTML-entity-escaped by html/template when rendered into the nonce="..."
// attribute (e.g. '+' -> '&#43;'), which browsers decode back correctly but
// which makes the raw served markup byte-different from the CSP header value.
// Hex uses only [0-9a-f], so the attribute round-trips through html/template
// unescaped and byte-identical to the header.
func newCSPNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// cleanPath maps a URL path to an embedded-FS name: strips the leading slash,
// rejects traversal, and treats a trailing slash as the index.
func cleanPath(p string) string {
	p = strings.TrimPrefix(p, "/")
	if p == "" || strings.HasSuffix(p, "/") {
		return ""
	}
	if strings.Contains(p, "..") {
		return ""
	}
	return p
}

// setStaticSecurityHeaders mirrors the loopback appliance posture used by the
// /api/v1 guard (internal/localhttp) so the served document and its assets get
// the same frame/referrer/type protections, with a CSP scoped to what the SPA
// actually needs:
//   - connect-src 'self'    only same-origin /api/v1 fetches
//   - font-src 'self'       self-hosted WOFF2 (no CDN)
//   - img-src 'self' data:  favicons + the inline data: fallback
//   - script-src 'self' 'nonce-...'  the one inline pre-paint no-flash theme
//     script in index.html carries a fresh per-request nonce (see newCSPNonce)
//     instead of a blanket 'unsafe-inline'; every other script tag is a
//     same-origin src= load, already covered by 'self'. Asset responses pass
//     an empty nonce (no inline script ever runs there).
//   - style-src 'self' 'unsafe-inline'   React sets inline style="" attributes
//     at runtime (its DOM style API), which CSP style-src cannot allow via a
//     nonce; this is a narrower residual (CSS injection only, never script
//     execution) accepted under the loopback-only, no-third-party-content
//     threat model -- see adr/0013 and the Session 10 hardening report.
func setStaticSecurityHeaders(h http.Header, nonce string) {
	scriptSrc := "script-src 'self'"
	if nonce != "" {
		scriptSrc = "script-src 'self' 'nonce-" + nonce + "'"
	}
	h.Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; font-src 'self'; "+scriptSrc+"; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}

// setAssetCache marks vite's content-hashed assets (under /assets/, /fonts/)
// as immutable; everything else gets a conservative short cache.
func setAssetCache(h http.Header, name string) {
	if strings.HasPrefix(name, "assets/") || strings.HasPrefix(name, "fonts/") {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	h.Set("Cache-Control", "public, max-age=3600")
}
