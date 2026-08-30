package api

import (
	"log/slog"
	"net/http"

	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// dummyBcryptHash is a valid bcrypt hash of a fixed, unrelated string
// ("gdhcp-dummy-timing-safe-compare"), used only so handleLogin can pay
// bcrypt.CompareHashAndPassword's ~50-200ms cost on the unknown-username/
// inactive-user path too. Without it, that path returns immediately while a
// known, active username always waits on the real compare — an attacker can
// enumerate valid usernames by timing POST /auth/login responses even
// though both paths return an identical error message.
var dummyBcryptHash = []byte("$2a$10$ut/XkOtkmQVXCEpDr8uiRek2ma9bEBjNN0OYcPjIVOTjKYgy.E09a")

type tokenResponse struct {
	Token        string `json:"token"`
	ExpiresAt    string `json:"expires_at"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// handleLogin handles POST /api/v1/auth/login. On success it issues a
// session JWT (24h TTL per docs/api/endpoints.md) plus a longer-lived
// refresh JWT — the "refresh_token" field is an addition beyond
// endpoints.md's documented {token, expires_at} response, since without it
// POST /auth/refresh's {refresh_token} request body would have no legitimate
// source (see notes_for_integration).
func handleLogin(users store.UserStore, audit store.AuditStore, jwt *jwtIssuer, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := httpx.DecodeJSON(r, &req); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if req.Username == "" || req.Password == "" {
			httpx.RespondError(w, r, log, httpx.Unauthorized("invalid username or password"))
			return
		}

		user, err := users.GetUserByUsername(r.Context(), req.Username)
		if err != nil || !user.IsActive {
			// Unknown/inactive user: still pay bcrypt's cost against a
			// fixed dummy hash (discarding the result, which never
			// matches) so this path takes the same time as a real user
			// whose password happens to be wrong, closing the timing gap
			// an attacker could otherwise use to enumerate usernames.
			_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(req.Password))
			httpx.RespondError(w, r, log, httpx.Unauthorized("invalid username or password"))
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
			httpx.RespondError(w, r, log, httpx.Unauthorized("invalid username or password"))
			return
		}

		access, expiresAt, err := jwt.issueAccessToken(user)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		refresh, _, err := jwt.issueRefreshToken(user)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		ip := clientIPPtr(r)
		_ = audit.LogAction(r.Context(), &models.AuditLog{
			UserID:     &user.ID,
			Action:     models.AuditActionLogin,
			EntityType: "user",
			EntityID:   user.Username,
			EntityName: &user.Username,
			IPAddress:  ip,
			UserAgent:  headerPtr(r, "User-Agent"),
		})

		httpx.RespondOK(w, tokenResponse{
			Token:        access,
			ExpiresAt:    expiresAt.Format(rfc3339),
			RefreshToken: refresh,
		})
	}
}

// handleLogout handles POST /api/v1/auth/logout. Sessions are stateless
// JWTs (no server-side session table in the store surface this unit was
// given), so there is nothing to revoke server-side; this simply
// acknowledges the request. A deployment that needs true revocation would
// add a token-blocklist store and check it in authenticator.resolveJWT —
// noted in notes_for_integration as a deviation from a literal "Revoke
// current session" per docs/api/endpoints.md.
func handleLogout(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httpx.RespondMessage(w, http.StatusOK, "logged out")
	}
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// handleRefresh handles POST /api/v1/auth/refresh.
func handleRefresh(users store.UserStore, jwt *jwtIssuer, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req refreshRequest
		if err := httpx.DecodeJSON(r, &req); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if req.RefreshToken == "" {
			httpx.RespondError(w, r, log, httpx.Unauthorized("refresh_token is required"))
			return
		}

		claims, err := jwt.parse(req.RefreshToken, tokenTypeRefresh)
		if err != nil {
			httpx.RespondError(w, r, log, httpx.Unauthorized("invalid or expired refresh token"))
			return
		}
		userID, err := claims.userID()
		if err != nil {
			httpx.RespondError(w, r, log, httpx.Unauthorized("invalid refresh token"))
			return
		}
		user, err := users.GetUserByID(r.Context(), userID)
		if err != nil || !user.IsActive {
			httpx.RespondError(w, r, log, httpx.Unauthorized("account no longer active"))
			return
		}

		access, expiresAt, err := jwt.issueAccessToken(user)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		newRefresh, _, err := jwt.issueRefreshToken(user)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		httpx.RespondOK(w, tokenResponse{
			Token:        access,
			ExpiresAt:    expiresAt.Format(rfc3339),
			RefreshToken: newRefresh,
		})
	}
}

const rfc3339 = "2006-01-02T15:04:05Z07:00"
