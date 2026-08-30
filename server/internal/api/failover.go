package api

import (
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
)

type failoverRequest struct {
	TargetServerID string `json:"target_server_id"`
}

type failoverResponse struct {
	Message    string `json:"message"`
	NewPrimary string `json:"new_primary"`
}

// handleFailover handles POST /api/v1/clusters/{cluster_id}/failover
// (admin only, per docs/api/endpoints.md's Notes column).
func handleFailover(ha HAStatus, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ha == nil {
			httpx.RespondError(w, r, log, httpx.ServiceUnavailable("HA is not configured on this node"))
			return
		}

		clusterID := mux.Vars(r)["cluster_id"]

		var req failoverRequest
		// target_server_id is optional (empty = let the HA manager elect);
		// an empty/malformed body is not itself an error here.
		_ = httpx.DecodeJSON(r, &req)

		newPrimary, err := ha.Failover(r.Context(), clusterID, req.TargetServerID)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondOK(w, failoverResponse{Message: "failover complete", NewPrimary: newPrimary})
	}
}
