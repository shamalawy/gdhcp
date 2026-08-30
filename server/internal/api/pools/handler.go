// Package pools implements the DHCP Pool Endpoints of docs/api/endpoints.md.
package pools

import (
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// List handles GET /api/v1/scopes/{scope_id}/pools.
func List(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scopeID := mux.Vars(r)["scope_id"]
		params := httpx.ParseListParams(r)
		res, err := st.ListPoolsByScope(r.Context(), scopeID, params)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

// Get handles GET /api/v1/pools/{pool_id}.
func Get(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := st.GetPoolByID(r.Context(), mux.Vars(r)["pool_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, p)
	}
}

type createBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	RangeStart  string  `json:"range_start"`
	RangeEnd    string  `json:"range_end"`
	Priority    int     `json:"priority"`
}

// Create handles POST /api/v1/scopes/{scope_id}/pools.
func Create(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scopeID := mux.Vars(r)["scope_id"]
		if _, err := st.GetScopeByID(r.Context(), scopeID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		var body createBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := httpx.RequireString("name", body.Name); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		start, err := httpx.ParseIP("range_start", body.RangeStart)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		end, err := httpx.ParseIP("range_end", body.RangeEnd)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		p := &models.DHCPPool{
			ScopeID:     scopeID,
			Name:        body.Name,
			Description: body.Description,
			RangeStart:  start,
			RangeEnd:    end,
			Priority:    body.Priority,
			IsEnabled:   true,
		}
		if err := st.CreatePool(r.Context(), p); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, p)
	}
}

type updateBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	RangeStart  string  `json:"range_start"`
	RangeEnd    string  `json:"range_end"`
	Priority    int     `json:"priority"`
	IsEnabled   *bool   `json:"is_enabled"`
}

// Update handles PUT /api/v1/pools/{pool_id}.
func Update(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["pool_id"]
		existing, err := st.GetPoolByID(r.Context(), id)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		var body updateBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := httpx.RequireString("name", body.Name); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		start, err := httpx.ParseIP("range_start", body.RangeStart)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		end, err := httpx.ParseIP("range_end", body.RangeEnd)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		existing.Name = body.Name
		existing.Description = body.Description
		existing.RangeStart = start
		existing.RangeEnd = end
		existing.Priority = body.Priority
		if body.IsEnabled != nil {
			existing.IsEnabled = *body.IsEnabled
		}

		if err := st.UpdatePool(r.Context(), existing); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, existing)
	}
}

// Delete handles DELETE /api/v1/pools/{pool_id}.
func Delete(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeletePool(r.Context(), mux.Vars(r)["pool_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "pool deleted")
	}
}

// Utilization handles GET /api/v1/pools/{pool_id}/utilization.
func Utilization(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := st.GetPoolUtilization(r.Context(), mux.Vars(r)["pool_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, u)
	}
}
