// Package vrfs implements the VRF Endpoints of docs/api/endpoints.md.
package vrfs

import (
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// List handles GET /api/v1/vrfs.
func List(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := httpx.ParseListParams(r)
		res, err := st.ListVRFs(r.Context(), params)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

// Get handles GET /api/v1/vrfs/{vrf_id}.
func Get(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := st.GetVRFByID(r.Context(), mux.Vars(r)["vrf_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, v)
	}
}

type upsertBody struct {
	Name               string  `json:"name"`
	Description        *string `json:"description"`
	RouteDistinguisher *string `json:"route_distinguisher"`
}

// Create handles POST /api/v1/vrfs.
func Create(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body upsertBody
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		if err := httpx.RequireString("name", body.Name); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		v := &models.VRF{
			Name:               body.Name,
			Description:        body.Description,
			RouteDistinguisher: body.RouteDistinguisher,
		}
		if err := st.CreateVRF(r.Context(), v); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondCreated(w, v)
	}
}

// Update handles PUT /api/v1/vrfs/{vrf_id}.
func Update(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["vrf_id"]
		existing, err := st.GetVRFByID(r.Context(), id)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		var body upsertBody
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
		existing.RouteDistinguisher = body.RouteDistinguisher

		if err := st.UpdateVRF(r.Context(), existing); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, existing)
	}
}

// Delete handles DELETE /api/v1/vrfs/{vrf_id}.
func Delete(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["vrf_id"]
		if err := st.DeleteVRF(r.Context(), id); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondMessage(w, http.StatusOK, "vrf deleted")
	}
}
