package server

import (
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"

	"github.com/shamalawy/gdhcp/server/internal/metrics"
)

// spaFileServer wraps fsys (web.Assets(), the embedded Angular build) with
// SPA fallback (docs/AMENDMENTS.md A4): a request path that doesn't match a
// real file under fsys is served index.html instead, so Angular's
// client-side router handles a deep link or hard refresh rather than the
// static file server 404ing on it.
func spaFileServer(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if upath == "" || upath == "." {
			upath = "index.html"
		}

		if f, err := fsys.Open(upath); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}

// rootHandler dispatches every inbound HTTP request to exactly one of: the
// REST API (amendment A4: /api/v1/*), the same-port Prometheus metrics
// endpoint (only non-nil when metrics.listen_addr equals server.api_addr),
// or the embedded Angular SPA for everything else.
type rootHandler struct {
	api         http.Handler
	assets      http.Handler
	metricsPath string
	metrics     http.Handler // nil unless metrics is mounted on this same port
}

func (h *rootHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v1" || strings.HasPrefix(r.URL.Path, "/api/v1/"):
		h.api.ServeHTTP(w, r)
	case h.metrics != nil && r.URL.Path == h.metricsPath:
		// Same-port metrics ride the API's listener but not its Bearer-auth
		// middleware (only apiHandler.ServeHTTP passes through that), so
		// without this check anyone who can reach the API port could read
		// DB pool stats, DHCP/HA counters, and build info with no token at
		// all. Requiring a private/loopback caller narrows that back down
		// to roughly the same network position a same-port Prometheus
		// scraper already needs, without forcing scrape configs to carry a
		// Bearer token the way reusing the API's own auth middleware would.
		if !remoteIsPrivate(r) {
			http.Error(w, "metrics: forbidden from this address", http.StatusForbidden)
			return
		}
		h.metrics.ServeHTTP(w, r)
	default:
		h.assets.ServeHTTP(w, r)
	}
}

// remoteIsPrivate reports whether r arrived from a loopback, link-local, or
// RFC1918/ULA-private address. It reads only r.RemoteAddr -- the real TCP
// peer net/http fills in from the socket -- deliberately never a
// client-suppliable header like X-Forwarded-For, since this gates access
// rather than just labeling a log line.
func remoteIsPrivate(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// securityHeaders wraps h with the response headers that stop a browser
// second-guessing this server. It sits outside rootHandler so the REST API,
// the same-port metrics endpoint and the embedded SPA all carry them —
// putting it inside would leave whichever branch was forgotten uncovered.
//
// The CSP is written against what web/dist actually ships: the Angular build
// emits inline <style> blocks (so style-src needs 'unsafe-inline') but loads
// its JavaScript only from same-origin module scripts, so script-src gets no
// such escape hatch. Font files come from fonts.gstatic.com; the
// fonts.googleapis.com stylesheet is inlined at build time but is allowed
// too, so a dev build served from this binary is not broken by the policy.
//
// hsts is set only when the API is actually served over TLS: sending
// Strict-Transport-Security over plain HTTP is ignored by browsers at best,
// and pins a hostname to a scheme it cannot serve at worst.
func securityHeaders(h http.Handler, hsts bool) http.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"font-src 'self' https://fonts.gstatic.com; " +
		"img-src 'self' data:; " +
		"connect-src 'self'; " +
		"object-src 'none'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("X-Frame-Options", "DENY")
		hdr.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		hdr.Set("Content-Security-Policy", csp)
		if hsts {
			hdr.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		h.ServeHTTP(w, r)
	})
}

// metricsHandler mounts reg's Prometheus handler at path on its own
// http.ServeMux, for use either as a same-port rootHandler.metrics or as the
// Handler of a standalone metrics *http.Server.
func metricsHandler(path string, reg *metrics.Registry) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(path, reg.Handler())
	return mux
}
