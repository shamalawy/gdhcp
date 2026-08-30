package logger

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type ctxKey int

const traceIDKey ctxKey = iota

// TraceID returns the request trace ID stored in ctx by Middleware, or ""
// if none is present. The API layer's error envelope (docs/CONTRACTS.md
// Part 2) embeds this as `error.trace_id` so operators can correlate a
// client-visible error with server logs.
func TraceID(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

// WithTraceID returns a copy of ctx carrying traceID, for callers (tests,
// background jobs) that need to synthesize one outside Middleware.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// statusRecorder wraps http.ResponseWriter to capture the status code
// actually written, defaulting to 200 if WriteHeader is never called
// explicitly (matching net/http's own behavior).
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.status = http.StatusOK
		r.wroteHeader = true
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController (and anything else using
// http.ResponseWriter type-assertion, e.g. for Flush/Hijack — needed by the
// SSE endpoint from amendment A12) reach the underlying ResponseWriter.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// Middleware returns HTTP middleware that: generates a "req_<uuid>" trace ID
// for each request (reusing an inbound X-Request-Id if present), stores it
// in the request context and response header, and logs one structured line
// per request at completion with method, path, status, duration, and
// trace_id. Mount it as the outermost middleware in the API router so every
// handler — including error paths — is covered.
func Middleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID := r.Header.Get("X-Request-Id")
			if traceID == "" {
				traceID = "req_" + uuid.NewString()
			}
			w.Header().Set("X-Request-Id", traceID)

			ctx := WithTraceID(r.Context(), traceID)
			r = r.WithContext(ctx)

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()

			next.ServeHTTP(rec, r)

			log.LogAttrs(ctx, slog.LevelInfo, "http_request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Duration("duration", time.Since(start)),
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("trace_id", traceID),
			)
		})
	}
}
