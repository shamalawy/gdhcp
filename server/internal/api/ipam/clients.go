package ipam

import (
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// Clients handles GET /api/v1/prefixes/{prefix_id}/clients, returning a paginated
// list of DHCP clients (leases and enabled reservations) living in the prefix and
// all its descendant prefixes. Clients are returned as models.DHCPClient union rows
// with kind ("lease" or "reservation"), supporting filters for kind, state (binding
// state for leases), and q (free-text search across IP/MAC/hostname).
func Clients(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		prefixID := mux.Vars(r)["prefix_id"]

		// Verify the prefix exists.
		if _, err := st.GetPrefixByID(r.Context(), prefixID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		// Parse list parameters: support kind, state, q filters.
		params := httpx.ParseListParams(r, "kind", "state", "q")

		// Query the store for clients under this prefix and its descendants.
		result, err := st.ListClientsUnderPrefix(r.Context(), prefixID, params)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		httpx.RespondList(w, result.Items, result.Total, result.Limit, result.Offset)
	}
}
