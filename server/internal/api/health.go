package api

import (
	"log/slog"
	"net/http"

	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/store"
	"github.com/shamalawy/gdhcp/server/internal/version"
)

type healthResponse struct {
	Status      string `json:"status"`
	DB          bool   `json:"db"`
	HA          bool   `json:"ha"`
	EngineReady bool   `json:"engine_ready"`
	Version     string `json:"version"`
}

// handleHealth handles GET /api/v1/health. It never requires authentication
// (see authExempt in middleware.go).
//
// The router.New constructor signature this unit was given doesn't carry a
// raw DB handle (only store.Store), so "db" health is inferred from a
// lightweight store round trip (AuditStore.ListAuditLog, limit 1) rather
// than a dedicated Ping — noted in notes_for_integration.
func handleHealth(st store.Store, ha HAStatus, engine EngineInfo, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dbHealthy := true
		if _, _, err := st.ListAuditLog(r.Context(), 0, 1, nil); err != nil {
			dbHealthy = false
			log.Warn("api: health check: db round trip failed", "error", err)
		}

		serving := false
		if ha != nil {
			serving = ha.Serving()
		}
		engineReady := false
		if engine != nil {
			engineReady = engine.Ready()
		}

		resp := healthResponse{
			Status:      "ok",
			DB:          dbHealthy,
			HA:          serving,
			EngineReady: engineReady,
			Version:     version.Get().String(),
		}

		status := http.StatusOK
		if !dbHealthy {
			resp.Status = "unavailable"
			status = http.StatusServiceUnavailable
		}

		httpx.RespondJSON(w, status, resp)
	}
}
