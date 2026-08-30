package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/authctx"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// ---------------------------------------------------------------------------
// Panic recovery
// ---------------------------------------------------------------------------

// recoverMiddleware turns a panic anywhere downstream into a logged 500
// INTERNAL_ERROR envelope instead of tearing down the connection/server.
func recoverMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("api: panic recovered", "panic", rec, "path", r.URL.Path, "method", r.Method, "trace_id", httpx.TraceID(r))
					httpx.RespondError(w, r, log, httpx.Internal("internal server error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// CORS (permissive same-origin default)
// ---------------------------------------------------------------------------

// corsMiddleware implements an allowlisted CORS policy: since the SPA is
// served from the same origin as the API in production (amendment A4),
// there is normally no cross-origin request to allow at all, so
// allowedOrigins (server.cors_allowed_origins) is empty by default and no
// CORS headers are sent for any Origin. Only an Origin explicitly present
// in allowedOrigins (e.g. a local frontend dev server such as
// `ng serve` on a different port) gets Access-Control-Allow-Origin
// reflected back with credentials enabled. This used to reflect *any*
// Origin header with credentials enabled unconditionally — letting any
// site a victim's browser visits make credentialed cross-origin requests
// to this API and read the response; an explicit allowlist closes that.
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o = strings.TrimSpace(o); o != "" {
			allowed[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			isAllowed := origin != "" && allowed[origin]
			if origin != "" {
				// Vary regardless of outcome: this handler's response to
				// the exact same path/method can legitimately differ by
				// Origin, so any cache in front of it must not conflate an
				// allowed-origin response with a disallowed one.
				w.Header().Set("Vary", "Origin")
			}
			if isAllowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if r.Method == http.MethodOptions {
				if isAllowed {
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
					w.Header().Set("Access-Control-Max-Age", "600")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// Authentication: session JWT or API token, per docs/AMENDMENTS.md A14.
// ---------------------------------------------------------------------------

// authNoop is the set of exact request paths that never require
// authentication, per this unit's task spec ("/auth/login and /health").
// POST /auth/refresh is included too: by definition the caller's access
// token is expired/expiring at the moment they call it, so requiring a
// still-valid Authorization header there would make the endpoint unusable —
// documented as a deviation from the literal task wording, which named only
// login+health.
var authExempt = map[string]bool{
	"/api/v1/auth/login":   true,
	"/api/v1/auth/refresh": true,
	"/api/v1/health":       true,
}

type authenticator struct {
	users store.UserStore
	jwt   *jwtIssuer
	log   *slog.Logger
}

// Middleware resolves the caller's Principal from the Authorization header
// and stores it on the request context. Requests to authExempt paths pass
// through unauthenticated; everything else gets a 401 UNAUTHORIZED envelope
// if the header is missing or the credential is invalid/expired.
func (a *authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authExempt[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		header := r.Header.Get("Authorization")
		raw, ok := strings.CutPrefix(header, "Bearer ")
		if (!ok || strings.TrimSpace(raw) == "") && r.URL.Path == "/api/v1/events" {
			// EventSource (used by the SSE live-update stream) cannot set
			// custom request headers, so it has no way to send a Bearer
			// Authorization header. As a narrow exception, scoped to exactly
			// this one path, accept the token as an `?access_token=` query
			// parameter instead. Query strings can end up in proxy/access
			// logs, so this fallback intentionally isn't extended to any
			// other endpoint; the resolved token is never logged here.
			if qs := r.URL.Query().Get("access_token"); strings.TrimSpace(qs) != "" {
				raw, ok = qs, true
			}
		}
		if !ok || strings.TrimSpace(raw) == "" {
			httpx.RespondError(w, r, a.log, httpx.Unauthorized("missing bearer token"))
			return
		}

		principal, err := a.resolve(r.Context(), raw)
		if err != nil {
			httpx.RespondError(w, r, a.log, httpx.Unauthorized(err.Error()))
			return
		}

		r = r.WithContext(authctx.WithPrincipal(r.Context(), principal))
		next.ServeHTTP(w, r)
	})
}

// apiTokenPrefix identifies raw API tokens (amendment A14:
// "gdhcp_<random 32 bytes base64url>") so the authenticator can route them
// to the SHA-256-hash lookup path instead of attempting a JWT parse.
const apiTokenPrefix = "gdhcp_"

func (a *authenticator) resolve(ctx context.Context, raw string) (*authctx.Principal, error) {
	if strings.HasPrefix(raw, apiTokenPrefix) {
		return a.resolveAPIToken(ctx, raw)
	}
	return a.resolveJWT(raw)
}

func (a *authenticator) resolveJWT(raw string) (*authctx.Principal, error) {
	claims, err := a.jwt.parse(raw, tokenTypeAccess)
	if err != nil {
		return nil, err
	}
	userID, err := claims.userID()
	if err != nil {
		return nil, err
	}
	return &authctx.Principal{UserID: userID, Username: claims.Username, Role: claims.Role}, nil
}

func (a *authenticator) resolveAPIToken(ctx context.Context, raw string) (*authctx.Principal, error) {
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])

	tok, err := a.users.ValidateAPIToken(ctx, hash)
	if err != nil {
		return nil, err
	}
	user, err := a.users.GetUserByID(ctx, tok.UserID)
	if err != nil {
		return nil, err
	}
	if !user.IsActive {
		return nil, errUserInactive
	}
	return &authctx.Principal{UserID: user.ID, Username: user.Username, Role: user.Role, TokenID: &tok.ID}, nil
}

var errUserInactive = errors.New("user account is disabled")

// ---------------------------------------------------------------------------
// RBAC (docs/AMENDMENTS.md A15): viewer=GET, operator=mutations except
// users/tokens/clusters config, admin=all.
// ---------------------------------------------------------------------------

// requireRole wraps h so it 403s unless the request's Principal meets
// minRole. Mount this per-route (via the route-registration helpers in
// router.go) rather than globally, since the required role varies by
// endpoint.
func requireRole(minRole models.Role, log *slog.Logger, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := authctx.FromContext(r.Context())
		if !p.Allows(minRole) {
			httpx.RespondError(w, r, log, httpx.Forbidden("requires "+string(minRole)+" role or higher"))
			return
		}
		h(w, r)
	}
}

// ---------------------------------------------------------------------------
// Audit logging: every successful mutation writes AuditStore.LogAction
// (entity type/id/name, a JSON change summary, the acting user, and their
// IP).
// ---------------------------------------------------------------------------

// bodyCaptureRecorder buffers the response status+body so auditWrap can
// inspect the created/updated resource's "id"/"name" fields after the
// handler runs, without changing what's actually sent to the client.
type bodyCaptureRecorder struct {
	http.ResponseWriter
	status int
	body   []byte
}

func (r *bodyCaptureRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *bodyCaptureRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.body = append(r.body, b...)
	return r.ResponseWriter.Write(b)
}

func (r *bodyCaptureRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// auditWrap wraps h so that, on a successful (2xx) response, it logs an
// AuditLog entry. entityType is the fixed entity name for the route (e.g.
// "vrf"); action is the AuditAction to record. idVar names the mux path
// variable holding the entity ID (e.g. "vrf_id"); pass "" for routes with no
// such variable (POST create), in which case the response body's "id" field
// is used instead.
// sensitiveField names request-body keys that must never be written to the
// audit log's change summary in the clear (a login password, a raw
// pre-hashed secret).
var sensitiveField = map[string]bool{
	"password": true,
}

func auditWrap(audit store.AuditStore, log *slog.Logger, entityType string, action models.AuditAction, idVar string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]any
		if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) {
			raw, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(raw))
			_ = json.Unmarshal(raw, &reqBody)
		}

		rec := &bodyCaptureRecorder{ResponseWriter: w}
		h(rec, r)

		if rec.status < 200 || rec.status >= 300 {
			return
		}

		p := authctx.FromContext(r.Context())
		var userID *int64
		if p != nil {
			userID = &p.UserID
		}

		var respBody map[string]any
		_ = json.Unmarshal(rec.body, &respBody)

		entityID := pathVar(r, idVar)
		if entityID == "" {
			entityID = firstNonEmpty(anyField(respBody, "id"), anyField(respBody, "option_code"))
		}
		entityName := firstNonEmpty(
			anyField(respBody, "name"),
			anyField(respBody, "username"),
			anyField(respBody, "node_name"),
			anyField(respBody, "hostname"),
		)

		changes := make(map[string]models.AuditChange, len(reqBody))
		for k, v := range reqBody {
			if sensitiveField[k] {
				v = "***redacted***"
			}
			changes[k] = models.AuditChange{New: v}
		}

		entry := &models.AuditLog{
			UserID:     userID,
			Action:     action,
			EntityType: entityType,
			EntityID:   entityID,
			Changes:    changes,
			IPAddress:  clientIPPtr(r),
			UserAgent:  headerPtr(r, "User-Agent"),
		}
		if entityName != "" {
			entry.EntityName = &entityName
		}

		// Best-effort: a logging failure must not fail (or re-run side
		// effects of) the mutation that already succeeded and was already
		// sent to the client.
		if err := audit.LogAction(r.Context(), entry); err != nil {
			log.Warn("api: audit log write failed", "error", err, "entity_type", entityType, "entity_id", entityID)
		}
	}
}

func pathVar(r *http.Request, name string) string {
	if name == "" {
		return ""
	}
	return mux.Vars(r)[name]
}

// anyField reads m[key] as a string suitable for an audit entity_id/name,
// handling both JSON strings and JSON numbers (encoding/json decodes numeric
// fields like a catalog entry's "option_code" into float64 when unmarshaled
// into map[string]any).
func anyField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func clientIPPtr(r *http.Request) *string {
	ip := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ip = strings.TrimSpace(strings.Split(fwd, ",")[0])
	} else if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	if ip == "" {
		return nil
	}
	return &ip
}

func headerPtr(r *http.Request, name string) *string {
	v := r.Header.Get(name)
	if v == "" {
		return nil
	}
	return &v
}
