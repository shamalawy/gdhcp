// Package scopes implements the DHCP Scope Endpoints of docs/api/endpoints.md.
package scopes

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// List handles GET /api/v1/scopes?vrf_id&cluster_id.
func List(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := httpx.ParseListParams(r, "vrf_id", "cluster_id", "ipam_prefix_id")
		res, err := st.ListScopes(r.Context(), params)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

// Get handles GET /api/v1/scopes/{scope_id}.
func Get(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := st.GetScopeByID(r.Context(), mux.Vars(r)["scope_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, s)
	}
}

type createBody struct {
	VRFID        string  `json:"vrf_id"`
	ClusterID    string  `json:"cluster_id"`
	IPAMPrefixID string  `json:"ipam_prefix_id"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
}

// Create handles POST /api/v1/scopes. Network is not client-supplied — it is
// cached from the referenced IPAM prefix's CIDR, matching
// DHCPScope.Network's "cached from ipam_prefix" contract.
func Create(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body createBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		for field, v := range map[string]string{
			"vrf_id": body.VRFID, "cluster_id": body.ClusterID,
			"ipam_prefix_id": body.IPAMPrefixID, "name": body.Name,
		} {
			if err := httpx.RequireString(field, v); err != nil {
				httpx.RespondError(w, r, log, err)
				return
			}
		}

		prefix, err := st.GetPrefixByID(r.Context(), body.IPAMPrefixID)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		s := &models.DHCPScope{
			VRFID:        body.VRFID,
			ClusterID:    body.ClusterID,
			IPAMPrefixID: body.IPAMPrefixID,
			Name:         body.Name,
			Description:  body.Description,
			Network:      prefix.Prefix,
			IsEnabled:    true,
		}
		if err := st.CreateScope(r.Context(), s); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, s)
	}
}

type updateBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	IsEnabled   *bool   `json:"is_enabled"`
}

// Update handles PUT /api/v1/scopes/{scope_id}.
func Update(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["scope_id"]
		existing, err := st.GetScopeByID(r.Context(), id)
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

		existing.Name = body.Name
		existing.Description = body.Description
		if body.IsEnabled != nil {
			existing.IsEnabled = *body.IsEnabled
		}

		if err := st.UpdateScope(r.Context(), existing); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, existing)
	}
}

// Delete handles DELETE /api/v1/scopes/{scope_id}.
func Delete(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := st.DeleteScope(r.Context(), mux.Vars(r)["scope_id"]); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "scope deleted")
	}
}

// Utilization handles GET /api/v1/scopes/{scope_id}/utilization.
func Utilization(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := st.GetScopeUtilization(r.Context(), mux.Vars(r)["scope_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, u)
	}
}

const defaultStatsWindow = 24 * time.Hour

// Stats handles GET /api/v1/scopes/{scope_id}/stats?window.
func Stats(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scopeID := mux.Vars(r)["scope_id"]
		if _, err := st.GetScopeByID(r.Context(), scopeID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		window := httpx.ParseWindow(r, defaultStatsWindow)
		stats, err := st.GetStats(r.Context(), scopeID, window)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, stats)
	}
}
