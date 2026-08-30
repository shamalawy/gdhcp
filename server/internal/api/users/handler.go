// Package users implements the User Management Endpoints of
// docs/api/endpoints.md. All routes here are mounted admin-only by
// router.go per amendment A15.
package users

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// List handles GET /api/v1/users.
func List(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := st.ListUsers(r.Context(), httpx.ParseListParams(r))
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

func parseUserID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(mux.Vars(r)["user_id"], 10, 64)
	if err != nil {
		return 0, httpx.Invalid("user_id must be numeric", "field", "user_id")
	}
	return id, nil
}

// Get handles GET /api/v1/users/{user_id}.
func Get(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseUserID(r)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		u, err := st.GetUserByID(r.Context(), id)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, u)
	}
}

type createBody struct {
	Username    string  `json:"username"`
	Email       string  `json:"email"`
	Password    string  `json:"password"`
	Role        string  `json:"role"`
	Description *string `json:"description"`
}

// Create handles POST /api/v1/users.
func Create(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body createBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		for field, v := range map[string]string{"username": body.Username, "email": body.Email, "password": body.Password} {
			if err := httpx.RequireString(field, v); err != nil {
				httpx.RespondError(w, r, log, err)
				return
			}
		}
		role := models.Role(body.Role)
		if role == "" {
			role = models.RoleViewer
		}
		if !role.Valid() {
			httpx.RespondError(w, r, log, httpx.Invalid("role must be one of admin, operator, viewer", "field", "role"))
			return
		}
		if len(body.Password) < 8 {
			httpx.RespondError(w, r, log, httpx.Invalid("password must be at least 8 characters", "field", "password"))
			return
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			httpx.RespondError(w, r, log, httpx.Internal("failed to hash password"))
			return
		}

		u := &models.User{
			Username:     body.Username,
			Email:        body.Email,
			PasswordHash: string(hash),
			Role:         role,
			Description:  body.Description,
			IsActive:     true,
		}
		if err := st.CreateUser(r.Context(), u); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, u)
	}
}

type updateBody struct {
	Username    string  `json:"username"`
	Email       string  `json:"email"`
	Description *string `json:"description"`
	IsActive    *bool   `json:"is_active"`
	Role        string  `json:"role"`
}

// Update handles PUT /api/v1/users/{user_id}.
func Update(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseUserID(r)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		existing, err := st.GetUserByID(r.Context(), id)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		var body updateBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		for field, v := range map[string]string{"username": body.Username, "email": body.Email} {
			if err := httpx.RequireString(field, v); err != nil {
				httpx.RespondError(w, r, log, err)
				return
			}
		}

		existing.Username = body.Username
		existing.Email = body.Email
		existing.Description = body.Description
		if body.IsActive != nil {
			existing.IsActive = *body.IsActive
		}
		if body.Role != "" {
			role := models.Role(body.Role)
			if !role.Valid() {
				httpx.RespondError(w, r, log, httpx.Invalid("role must be one of admin, operator, viewer", "field", "role"))
				return
			}
			existing.Role = role
		}

		if err := st.UpdateUser(r.Context(), existing); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, existing)
	}
}

// Delete handles DELETE /api/v1/users/{user_id}.
func Delete(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseUserID(r)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := st.DeleteUser(r.Context(), id); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "user deleted")
	}
}

// ListTokens handles GET /api/v1/users/{user_id}/tokens.
func ListTokens(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseUserID(r)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		toks, err := st.ListAPITokens(r.Context(), id)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, toks)
	}
}

type createTokenBody struct {
	Description string     `json:"description"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

// CreateToken handles POST /api/v1/users/{user_id}/tokens. The raw token
// value is returned exactly once, per amendment A14; it is never stored or
// retrievable again.
func CreateToken(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseUserID(r)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if _, err := st.GetUserByID(r.Context(), id); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		var body createTokenBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		raw, hash, err := generateAPIToken()
		if err != nil {
			httpx.RespondError(w, r, log, httpx.Internal("failed to generate token"))
			return
		}

		var desc *string
		if body.Description != "" {
			desc = &body.Description
		}
		tok := &models.APIToken{
			UserID:      id,
			TokenHash:   hash,
			Description: desc,
			ExpiresAt:   body.ExpiresAt,
		}
		if err := st.CreateAPIToken(r.Context(), tok); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, models.APITokenCreated{APIToken: *tok, Token: raw})
	}
}

// RevokeToken handles DELETE /api/v1/users/{user_id}/tokens/{token_id}.
func RevokeToken(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenID, err := strconv.ParseInt(mux.Vars(r)["token_id"], 10, 64)
		if err != nil {
			httpx.RespondError(w, r, log, httpx.Invalid("token_id must be numeric", "field", "token_id"))
			return
		}
		if err := st.RevokeAPIToken(r.Context(), tokenID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "token revoked")
	}
}
