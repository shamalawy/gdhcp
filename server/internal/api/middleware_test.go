package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCORSMiddleware_OnlyAllowlistedOriginGetsCredentialedHeaders guards the
// fix for the CORS misconfiguration: corsMiddleware must reflect
// Access-Control-Allow-Origin/-Credentials only for an Origin present in
// its allowlist, never for an arbitrary Origin — the previous behavior let
// any site a victim's browser visits make credentialed cross-origin
// requests to this API.
func TestCORSMiddleware_OnlyAllowlistedOriginGetsCredentialedHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := corsMiddleware([]string{"http://localhost:4200"})(next)

	cases := []struct {
		name          string
		origin        string
		wantAllowedOn bool
	}{
		{"allowlisted origin", "http://localhost:4200", true},
		{"arbitrary attacker origin", "https://evil.example", false},
		{"no origin header (same-origin/non-browser)", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			gotOrigin := rec.Header().Get("Access-Control-Allow-Origin")
			gotCreds := rec.Header().Get("Access-Control-Allow-Credentials")
			if tc.wantAllowedOn {
				if gotOrigin != tc.origin {
					t.Errorf("Access-Control-Allow-Origin = %q, want %q", gotOrigin, tc.origin)
				}
				if gotCreds != "true" {
					t.Errorf("Access-Control-Allow-Credentials = %q, want %q", gotCreds, "true")
				}
			} else {
				if gotOrigin != "" {
					t.Errorf("Access-Control-Allow-Origin = %q, want empty (Origin not allowlisted)", gotOrigin)
				}
				if gotCreds != "" {
					t.Errorf("Access-Control-Allow-Credentials = %q, want empty (Origin not allowlisted)", gotCreds)
				}
			}
		})
	}
}

// TestCORSMiddleware_PreflightOmitsMethodHeadersForDisallowedOrigin ensures
// an OPTIONS preflight from a non-allowlisted origin gets no
// Access-Control-Allow-Methods/-Headers either, so the browser won't send
// the follow-up credentialed request at all.
func TestCORSMiddleware_PreflightOmitsMethodHeadersForDisallowedOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("preflight must not reach the wrapped handler")
	})
	handler := corsMiddleware([]string{"http://localhost:4200"})(next)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/vrfs", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if v := rec.Header().Get("Access-Control-Allow-Methods"); v != "" {
		t.Errorf("Access-Control-Allow-Methods = %q, want empty for a disallowed origin", v)
	}
	if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty for a disallowed origin", v)
	}
}
