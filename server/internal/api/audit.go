package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// handleAuditLog handles GET /api/v1/audit/log?limit&offset&sort&filter&
// entity_type&entity_id. `filter` is the JSON-encoded object from
// docs/CONTRACTS.md Part 2's common query parameters; entity_type/entity_id
// are convenience top-level filters merged into the same map.
func handleAuditLog(audit store.AuditStore, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := httpx.ParseListParams(r)

		filters := map[string]interface{}{}
		if raw := r.URL.Query().Get("filter"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &filters); err != nil {
				httpx.RespondError(w, r, log, httpx.Invalid("filter must be a JSON object", "field", "filter"))
				return
			}
		}
		if v := r.URL.Query().Get("entity_type"); v != "" {
			filters["entity_type"] = v
		}
		if v := r.URL.Query().Get("entity_id"); v != "" {
			filters["entity_id"] = v
		}

		logs, total, err := audit.ListAuditLog(r.Context(), params.Offset, params.Limit, filters)
		if err != nil {
			httpx.RespondError(w, r, log, err)
			return
		}
		httpx.RespondList(w, logs, total, params.Limit, params.Offset)
	}
}
