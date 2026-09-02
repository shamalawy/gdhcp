// Package httpx provides the JSON response envelope, error mapping, and
// request-parsing helpers shared by every gdhcp API handler package. It has
// no dependency on package api itself so both api and its resource
// sub-packages (vrfs, scopes, ...) can import it without a cycle.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/shamalawy/gdhcp/server/internal/logger"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

// ErrorCode is one of the canonical error codes in docs/CONTRACTS.md Part 6.
type ErrorCode string

const (
	CodeInvalidInput       ErrorCode = "INVALID_INPUT"
	CodeUnauthorized       ErrorCode = "UNAUTHORIZED"
	CodeForbidden          ErrorCode = "FORBIDDEN"
	CodeNotFound           ErrorCode = "NOT_FOUND"
	CodeConflict           ErrorCode = "CONFLICT"
	CodeInternalError      ErrorCode = "INTERNAL_ERROR"
	CodeServiceUnavailable ErrorCode = "SERVICE_UNAVAILABLE"
	CodeRateLimited        ErrorCode = "RATE_LIMITED"
)

// APIError is a handler-raised error that carries its own HTTP status, code,
// and optional field-level details. Handlers can either return one of these
// directly to RespondError, or return a bare error (including a wrapped
// models sentinel) which RespondError classifies itself.
type APIError struct {
	Status  int
	Code    ErrorCode
	Message string
	Details map[string]any
}

func (e *APIError) Error() string { return e.Message }

// Invalid builds a 400 INVALID_INPUT error, optionally naming the offending
// field/issue for the error envelope's "details".
func Invalid(msg string, kv ...string) *APIError {
	return &APIError{Status: http.StatusBadRequest, Code: CodeInvalidInput, Message: msg, Details: detailsFrom(kv)}
}

// NotFound builds a 404 NOT_FOUND error.
func NotFound(msg string) *APIError {
	return &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: msg}
}

// Conflict builds a 409 CONFLICT error.
func Conflict(msg string, kv ...string) *APIError {
	return &APIError{Status: http.StatusConflict, Code: CodeConflict, Message: msg, Details: detailsFrom(kv)}
}

// Unauthorized builds a 401 UNAUTHORIZED error.
func Unauthorized(msg string) *APIError {
	return &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: msg}
}

// Forbidden builds a 403 FORBIDDEN error.
func Forbidden(msg string) *APIError {
	return &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: msg}
}

// TooLarge builds a 413 error for a request body that exceeded the limit
// imposed by the API's http.MaxBytesReader middleware. It reuses
// CodeInvalidInput rather than introducing a new ErrorCode: the client's
// input is what is wrong, and the documented code set stays closed.
func TooLarge(msg string) *APIError {
	return &APIError{Status: http.StatusRequestEntityTooLarge, Code: CodeInvalidInput, Message: msg}
}

// TooManyRequests builds a 429 RATE_LIMITED error. Callers should also set
// a Retry-After header so a well-behaved client knows when to come back.
func TooManyRequests(msg string) *APIError {
	return &APIError{Status: http.StatusTooManyRequests, Code: CodeRateLimited, Message: msg}
}

// Internal builds a 500 INTERNAL_ERROR error. msg is sent to the client as
// written, so callers (e.g. panic recovery) must pass a safe, generic
// message rather than an internal error's raw text.
func Internal(msg string) *APIError {
	return &APIError{Status: http.StatusInternalServerError, Code: CodeInternalError, Message: msg}
}

// ServiceUnavailable builds a 503 SERVICE_UNAVAILABLE error.
func ServiceUnavailable(msg string) *APIError {
	return &APIError{Status: http.StatusServiceUnavailable, Code: CodeServiceUnavailable, Message: msg}
}

func detailsFrom(kv []string) map[string]any {
	if len(kv) == 0 {
		return nil
	}
	d := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		d[kv[i]] = kv[i+1]
	}
	return d
}

// TraceID returns the request's trace_id ("req_<uuid>") set by
// logger.Middleware (mounted as the outermost middleware in router.go), or
// "" if none is present (e.g. in a handler test that bypasses the
// middleware chain).
func TraceID(r *http.Request) string {
	return logger.TraceID(r.Context())
}

// errorEnvelope is the wire shape of docs/CONTRACTS.md Part 6.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
	TraceID string         `json:"trace_id,omitempty"`
}

// listEnvelope is the {items,total,limit,offset} pagination shape.
type listEnvelope[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// RespondJSON writes v as the response body with the given status code.
func RespondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// RespondCreated writes v with 201 Created.
func RespondCreated(w http.ResponseWriter, v any) { RespondJSON(w, http.StatusCreated, v) }

// RespondOK writes v with 200 OK.
func RespondOK(w http.ResponseWriter, v any) { RespondJSON(w, http.StatusOK, v) }

// RespondNoContent writes an empty 204 No Content response.
func RespondNoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// RespondMessage writes {"message": msg} with the given status. Several
// endpoints.md rows specify a 204 with a "{message}" body; per RFC 9110 a
// 204 MUST NOT carry a body, so gdhcp uses 200 for those instead of a
// technically-invalid 204+body (documented as a deviation).
func RespondMessage(w http.ResponseWriter, status int, msg string) {
	RespondJSON(w, status, map[string]string{"message": msg})
}

// RespondList writes items with the pagination envelope.
func RespondList[T any](w http.ResponseWriter, items []T, total int64, limit, offset int) {
	if items == nil {
		items = []T{}
	}
	RespondJSON(w, http.StatusOK, listEnvelope[T]{Items: items, Total: total, Limit: limit, Offset: offset})
}

// RespondError classifies err (an *APIError, a wrapped models sentinel, or
// an opaque error) into the canonical envelope and status code, logs
// unexpected (5xx) errors, and writes the response.
func RespondError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		writeError(w, r, apiErr.Status, apiErr.Code, apiErr.Message, apiErr.Details)
		return
	}

	status, code := classify(err)
	msg := err.Error()
	if status == http.StatusInternalServerError {
		// Never leak internal error strings (DB DSNs, driver errors, stack
		// fragments) to clients; log the real error server-side instead.
		if log != nil {
			log.Error("api: internal error", "error", err, "path", r.URL.Path, "method", r.Method, "trace_id", TraceID(r))
		}
		msg = "internal server error"
	}
	writeError(w, r, status, code, msg, nil)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, msg string, details map[string]any) {
	RespondJSON(w, status, errorEnvelope{Error: errorBody{
		Code:    code,
		Message: msg,
		Details: details,
		TraceID: TraceID(r),
	}})
}

func classify(err error) (int, ErrorCode) {
	switch {
	case errors.Is(err, models.ErrNotFound), errors.Is(err, models.ErrLeaseNotFound):
		return http.StatusNotFound, CodeNotFound
	case errors.Is(err, models.ErrConflict), errors.Is(err, models.ErrHasDependents), errors.Is(err, models.ErrNoLeases):
		return http.StatusConflict, CodeConflict
	case errors.Is(err, models.ErrUnauthorized):
		return http.StatusUnauthorized, CodeUnauthorized
	case errors.Is(err, models.ErrForbidden):
		return http.StatusForbidden, CodeForbidden
	case errors.Is(err, models.ErrInvalidInput):
		return http.StatusBadRequest, CodeInvalidInput
	case errors.Is(err, models.ErrServiceUnavailable):
		return http.StatusServiceUnavailable, CodeServiceUnavailable
	default:
		return http.StatusInternalServerError, CodeInternalError
	}
}

// DecodeJSON decodes the request body into v, returning a 400 *APIError on
// any malformed-JSON or empty-body condition so handlers can just
// `if err := httpx.DecodeJSON(r, &body); err != nil { httpx.RespondError(...); return }`.
func DecodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return Invalid("request body is required")
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return Invalid("request body is required")
		}
		// The API's maxBodyMiddleware wraps every body in an
		// http.MaxBytesReader; past the cap the decoder surfaces its
		// *http.MaxBytesError. Reporting that as "malformed JSON" would
		// send a caller hunting a syntax error that isn't there.
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return TooLarge(fmt.Sprintf("request body exceeds the %d byte limit", tooLarge.Limit))
		}
		return Invalid(fmt.Sprintf("malformed JSON body: %v", err))
	}
	return nil
}
