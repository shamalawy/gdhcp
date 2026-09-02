package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func okHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})
}

// TestSecurityHeaders_CoversEveryBranch is the reason the middleware wraps
// rootHandler from outside rather than living inside its switch: the API,
// the same-port metrics endpoint and the SPA must all carry the headers, and
// wrapping from within is exactly how one of them gets forgotten.
func TestSecurityHeaders_CoversEveryBranch(t *testing.T) {
	root := &rootHandler{
		api:         okHandler("api"),
		assets:      okHandler("spa"),
		metricsPath: "/metrics",
		metrics:     okHandler("metrics"),
	}
	h := securityHeaders(root, false)

	for _, path := range []string{"/api/v1/scopes", "/metrics", "/dashboard"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:40000" // metrics is loopback-gated
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		for header, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "strict-origin-when-cross-origin",
		} {
			if got := rec.Header().Get(header); got != want {
				t.Errorf("%s: %s = %q, want %q", path, header, got, want)
			}
		}
		if csp := rec.Header().Get("Content-Security-Policy"); csp == "" {
			t.Errorf("%s: no Content-Security-Policy", path)
		}
	}
}

// TestSecurityHeaders_CSPAllowsWhatTheSPAActuallyLoads: the built Angular
// bundle emits inline <style> blocks and pulls font files from
// fonts.gstatic.com, so a policy that forbids either would render the UI
// unusable. Scripts get no such allowance — the bundle loads them only as
// same-origin module files.
func TestSecurityHeaders_CSPAllowsWhatTheSPAActuallyLoads(t *testing.T) {
	rec := httptest.NewRecorder()
	securityHeaders(okHandler("spa"), false).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src 'self' https://fonts.gstatic.com",
		"script-src 'self'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q is missing %q", csp, want)
		}
	}
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Error("CSP allows inline scripts; the built bundle does not need it")
	}
}

// TestSecurityHeaders_HSTSOnlyOverTLS: sending Strict-Transport-Security on a
// plain-HTTP deployment pins a hostname to a scheme this server is not
// serving.
func TestSecurityHeaders_HSTSOnlyOverTLS(t *testing.T) {
	for _, tc := range []struct {
		name string
		tls  bool
		want bool
	}{
		{"plain http", false, false},
		{"tls enabled", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			securityHeaders(okHandler("x"), tc.tls).
				ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			got := rec.Header().Get("Strict-Transport-Security") != ""
			if got != tc.want {
				t.Fatalf("HSTS present = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRootHandler_Routing pins the three-way split, including the metrics
// endpoint's loopback gate — it rides the API's listener without the API's
// auth middleware, so the address check is the only thing protecting it.
func TestRootHandler_Routing(t *testing.T) {
	root := &rootHandler{
		api:         okHandler("api"),
		assets:      okHandler("spa"),
		metricsPath: "/metrics",
		metrics:     okHandler("metrics"),
	}

	for _, tc := range []struct {
		name, path, remote, wantBody string
		wantStatus                   int
	}{
		{"api prefix", "/api/v1/scopes", "10.0.0.5:1000", "api", http.StatusOK},
		{"api bare", "/api/v1", "10.0.0.5:1000", "api", http.StatusOK},
		{"spa deep link", "/scopes/abc", "10.0.0.5:1000", "spa", http.StatusOK},
		{"metrics from loopback", "/metrics", "127.0.0.1:1000", "metrics", http.StatusOK},
		{"metrics from private", "/metrics", "10.0.0.5:1000", "metrics", http.StatusOK},
		{"metrics from public", "/metrics", "203.0.113.9:1000", "", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.RemoteAddr = tc.remote
			rec := httptest.NewRecorder()
			root.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
				t.Fatalf("routed to %q, want %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

// TestSPAFileServer_FallsBackToIndex covers amendment A4's SPA fallback: a
// deep link or hard refresh must reach Angular's router, not a 404.
func TestSPAFileServer_FallsBackToIndex(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":  {Data: []byte("<app-root>")},
		"main-abc.js": {Data: []byte("console.log(1)")},
	}
	h := spaFileServer(fsys)

	for _, tc := range []struct{ name, path, want string }{
		{"real asset", "/main-abc.js", "console.log(1)"},
		{"root", "/", "<app-root>"},
		{"deep link", "/scopes/9f3c/pools", "<app-root>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Body.String(); got != tc.want {
				t.Fatalf("body = %q, want %q", got, tc.want)
			}
		})
	}
}
