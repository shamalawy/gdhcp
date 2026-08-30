package ipam

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// Utilization handles GET /api/v1/prefixes/{prefix_id}/utilization, the
// prefix-level counterpart to the documented /scopes/{id}/utilization and
// /pools/{id}/utilization endpoints. It answers for every level of the IPAM
// tree at once with two views (models.PrefixUtilization): a DHCP view
// (total/active/reserved/available/pct, the same shape those two endpoints
// use) rolled up over every scope anywhere beneath this prefix, and an
// address-space view of how much of the prefix's own CIDR its direct
// children cover — see store.GetPrefixUtilization.
func Utilization(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := st.GetPrefixUtilization(r.Context(), mux.Vars(r)["prefix_id"])
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, u)
	}
}

// nextAvailableSubnetResponse is the shape specified in
// docs/design/api-cli.md §5.18 "Helper Endpoints (Next Available)".
type nextAvailableSubnetResponse struct {
	Subnet         string `json:"subnet"`
	ParentPrefixID string `json:"parent_prefix_id"`
	Available      bool   `json:"available"`
}

// NextAvailableSubnet handles
// GET /api/v1/ipam/next-available-subnet?prefix_id=&size=: suggests the first
// unused /size block inside prefix_id, for carving new subnets out of an
// aggregate or container.
//
// Running out of space is a normal answer, not an error: it responds 200 with
// available=false and an empty subnet so the UI can say "no space at this
// size" without treating it as a failure.
func NextAvailableSubnet(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		prefixID := r.URL.Query().Get("prefix_id")
		if err := httpx.RequireString("prefix_id", prefixID); err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}

		size := httpx.ParseIntQuery(r, "size", 0)
		if size <= 0 || size > 128 {
			httpx.RespondError(w, r, log, httpx.Invalid("size must be a prefix length between 1 and 128", "field", "size"))
			return
		}

		subnet, err := st.NextAvailableSubnet(r.Context(), prefixID, size)
		if errors.Is(err, models.ErrNoLeases) {
			httpx.RespondOK(w, nextAvailableSubnetResponse{ParentPrefixID: prefixID, Available: false})
			return
		}
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, nextAvailableSubnetResponse{
			Subnet:         subnet.String(),
			ParentPrefixID: prefixID,
			Available:      true,
		})
	}
}
