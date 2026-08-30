// Package leases implements the read-only Lease Endpoints of
// docs/api/endpoints.md. Leases have no Create/Update/Delete via the API —
// only the DHCP engine and CLI/HA admin tooling mutate them.
package leases

import (
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// List handles GET /api/v1/leases?scope_id&pool_id&state.
func List(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := httpx.ParseListParams(r, "scope_id", "pool_id", "state")
		res, err := st.ListLeases(r.Context(), params)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

// Get handles GET /api/v1/leases/{lease_id}.
func Get(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l, err := st.GetLease(r.Context(), mux.Vars(r)["lease_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, l)
	}
}

// ListByPool handles GET /api/v1/pools/{pool_id}/leases?state.
func ListByPool(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		poolID := mux.Vars(r)["pool_id"]
		params := httpx.ParseListParams(r, "state")
		res, err := st.ListLeasesByPool(r.Context(), poolID, params)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, res.Items, res.Total, res.Limit, res.Offset)
	}
}

// GetByIP handles GET /api/v1/leases/by-ip/{ip_address}.
func GetByIP(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip, err := httpx.ParseIP("ip_address", mux.Vars(r)["ip_address"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		l, err := st.GetLeaseByIPGlobal(r.Context(), ip)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, l)
	}
}

// GetByMAC handles GET /api/v1/leases/by-mac/{mac_address}.
func GetByMAC(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mac, err := httpx.ParseMAC("mac_address", mux.Vars(r)["mac_address"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		list, err := st.ListLeasesByMAC(r.Context(), mac.String())
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, list)
	}
}
