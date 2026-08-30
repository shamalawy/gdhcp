package metrics

import (
	"net/http"
	"strconv"
	"time"
)

// APIRequest records one completed HTTP API request. route should be the
// matched route template (e.g. "/api/v1/scopes/{scope_id}"), not the raw
// path, to keep label cardinality bounded.
func (r *Registry) APIRequest(method, route string, status int, d time.Duration) {
	statusStr := strconv.Itoa(status)
	r.apiRequestsTotal.WithLabelValues(method, route, statusStr).Inc()
	r.apiRequestDuration.WithLabelValues(method, route).Observe(msFloat(d))
}

// Middleware returns HTTP middleware that records APIRequest for every
// request using routeFn to resolve the low-cardinality route label (e.g. a
// gorilla/mux route template via mux.CurrentRoute) from the request.
func (r *Registry) Middleware(routeFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			rec := &statusOnlyRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, req)
			route := req.URL.Path
			if routeFn != nil {
				if rt := routeFn(req); rt != "" {
					route = rt
				}
			}
			r.APIRequest(req.Method, route, rec.status, time.Since(start))
		})
	}
}

// statusOnlyRecorder captures the response status code for Middleware
// without the extra trace-ID bookkeeping logger.statusRecorder does.
type statusOnlyRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusOnlyRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusOnlyRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusOnlyRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
