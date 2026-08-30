package ipam

import (
	"log/slog"
	"net/http"

	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

const defaultTreeDepth = 1

// Tree handles GET /api/v1/ipam/tree?vrf_id&parent_id&depth: the
// lazily-expandable hierarchy view. Omitting parent_id returns the VRF's
// top-level prefixes; depth bounds how many levels are eagerly expanded
// beneath each returned node (further levels are fetched on demand by the
// frontend re-calling with parent_id set to a node it wants to expand).
func Tree(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vrfID := r.URL.Query().Get("vrf_id")
		if err := httpx.RequireString("vrf_id", vrfID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		depth := httpx.ParseIntQuery(r, "depth", defaultTreeDepth)
		if depth < 1 {
			depth = 1
		}

		var parentID *string
		if pid := r.URL.Query().Get("parent_id"); pid != "" {
			parentID = &pid
		}

		nodes, err := st.GetIPAMTree(r.Context(), vrfID, parentID, depth)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, nodes)
	}
}
