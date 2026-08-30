// Package api implements gdhcp's REST HTTP API: every endpoint in
// docs/api/endpoints.md, snake_case JSON (amendment A1), the
// {items,total,limit,offset} list envelope and canonical error envelope
// (docs/CONTRACTS.md Part 2/6), JWT + API-token auth (amendment A14), RBAC
// (amendment A15), and audit logging for every successful mutation.
//
// New builds the http.Handler; it serves only /api/v1/* (and
// /api/v1/health) — static SPA serving is the integration layer's job
// (amendment A4), not this package's.
package api

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shamalawy/gdhcp/server/internal/api/clusters"
	"github.com/shamalawy/gdhcp/server/internal/api/dashboard"
	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
	"github.com/shamalawy/gdhcp/server/internal/api/ipam"
	"github.com/shamalawy/gdhcp/server/internal/api/leases"
	"github.com/shamalawy/gdhcp/server/internal/api/options"
	"github.com/shamalawy/gdhcp/server/internal/api/pools"
	"github.com/shamalawy/gdhcp/server/internal/api/reservations"
	"github.com/shamalawy/gdhcp/server/internal/api/scopes"
	"github.com/shamalawy/gdhcp/server/internal/api/tags"
	"github.com/shamalawy/gdhcp/server/internal/api/templates"
	"github.com/shamalawy/gdhcp/server/internal/api/users"
	"github.com/shamalawy/gdhcp/server/internal/api/vlans"
	"github.com/shamalawy/gdhcp/server/internal/api/vrfs"
	"github.com/shamalawy/gdhcp/server/internal/config"
	"github.com/shamalawy/gdhcp/server/internal/logger"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"

	"log/slog"
)

// routeReg wraps the /api/v1 subrouter with the RBAC + audit-logging
// registration helpers used to mount every route below.
type routeReg struct {
	r     *mux.Router
	log   *slog.Logger
	audit store.AuditStore
}

// get registers a read route. Every GET requires at least the viewer role
// (amendment A15: "viewer=GET") — any authenticated principal, regardless of
// role, passes.
func (rr *routeReg) get(path string, h http.HandlerFunc) {
	rr.r.HandleFunc(path, requireRole(models.RoleViewer, rr.log, h)).Methods(http.MethodGet)
}

// mutate registers a write route (POST/PUT/DELETE) requiring minRole and
// logging a successful response via AuditStore.LogAction. idVar names the
// mux path variable holding the mutated entity's ID ("" to fall back to the
// response body's id/option_code field, e.g. for POST create routes with no
// path variable).
func (rr *routeReg) mutate(method, path string, minRole models.Role, entityType string, action models.AuditAction, idVar string, h http.HandlerFunc) {
	wrapped := auditWrap(rr.audit, rr.log, entityType, action, idVar, h)
	rr.r.HandleFunc(path, requireRole(minRole, rr.log, wrapped)).Methods(method)
}

// New builds the gdhcp REST API handler. cfg supplies the JWT signing
// secret; st is the single aggregate store the whole API reads/writes
// through; ha and engine are the consumer-side views (amendment A10) of the
// HA cluster manager and DHCP engine, both of which may be nil (standalone
// API/UI-only mode per amendment A11 — handlers degrade gracefully rather
// than panicking).
func New(cfg *config.Config, st store.Store, ha HAStatus, engine EngineInfo, log *slog.Logger) http.Handler {
	root := mux.NewRouter()
	api := root.PathPrefix("/api/v1").Subrouter()

	jwt := newJWTIssuer(cfg.Server.JWTSecret)
	auther := &authenticator{users: st, jwt: jwt, log: log}
	api.Use(auther.Middleware)

	reg := &routeReg{r: api, log: log, audit: st}

	// -- Auth & health (no RBAC role check: login/refresh run before a
	// principal exists, logout only needs *a* valid principal of any role,
	// and health is unauthenticated entirely). -----------------------------
	api.HandleFunc("/auth/login", handleLogin(st, st, jwt, log)).Methods(http.MethodPost)
	api.HandleFunc("/auth/logout", handleLogout(log)).Methods(http.MethodPost)
	api.HandleFunc("/auth/refresh", handleRefresh(st, jwt, log)).Methods(http.MethodPost)
	api.HandleFunc("/health", handleHealth(st, ha, engine, log)).Methods(http.MethodGet)

	// -- Users & API tokens (admin only, per endpoints.md's explicit notes
	// and amendment A15). ---------------------------------------------------
	reg.r.HandleFunc("/users", requireRole(models.RoleAdmin, log, users.List(st, log))).Methods(http.MethodGet)
	reg.r.HandleFunc("/users/{user_id}", requireRole(models.RoleAdmin, log, users.Get(st, log))).Methods(http.MethodGet)
	reg.mutate(http.MethodPost, "/users", models.RoleAdmin, "user", models.AuditActionCreate, "", users.Create(st, log))
	reg.mutate(http.MethodPut, "/users/{user_id}", models.RoleAdmin, "user", models.AuditActionUpdate, "user_id", users.Update(st, log))
	reg.mutate(http.MethodDelete, "/users/{user_id}", models.RoleAdmin, "user", models.AuditActionDelete, "user_id", users.Delete(st, log))
	reg.r.HandleFunc("/users/{user_id}/tokens", requireRole(models.RoleAdmin, log, users.ListTokens(st, log))).Methods(http.MethodGet)
	reg.mutate(http.MethodPost, "/users/{user_id}/tokens", models.RoleAdmin, "api_token", models.AuditActionCreate, "user_id", users.CreateToken(st, log))
	reg.mutate(http.MethodDelete, "/users/{user_id}/tokens/{token_id}", models.RoleAdmin, "api_token", models.AuditActionDelete, "token_id", users.RevokeToken(st, log))

	// -- VRFs -----------------------------------------------------------
	reg.get("/vrfs", vrfs.List(st, log))
	reg.get("/vrfs/{vrf_id}", vrfs.Get(st, log))
	reg.mutate(http.MethodPost, "/vrfs", models.RoleOperator, "vrf", models.AuditActionCreate, "", vrfs.Create(st, log))
	reg.mutate(http.MethodPut, "/vrfs/{vrf_id}", models.RoleOperator, "vrf", models.AuditActionUpdate, "vrf_id", vrfs.Update(st, log))
	reg.mutate(http.MethodDelete, "/vrfs/{vrf_id}", models.RoleOperator, "vrf", models.AuditActionDelete, "vrf_id", vrfs.Delete(st, log))

	// -- VLANs (layer-2 segmentation; migration 0004) ---------------------
	reg.get("/vlans", vlans.List(st, log))
	reg.get("/vlans/{vlan_id}", vlans.Get(st, log))
	reg.get("/vlans/{vlan_id}/prefixes", vlans.Prefixes(st, log))
	reg.mutate(http.MethodPost, "/vlans", models.RoleOperator, "vlan", models.AuditActionCreate, "", vlans.Create(st, log))
	reg.mutate(http.MethodPut, "/vlans/{vlan_id}", models.RoleOperator, "vlan", models.AuditActionUpdate, "vlan_id", vlans.Update(st, log))
	reg.mutate(http.MethodDelete, "/vlans/{vlan_id}", models.RoleOperator, "vlan", models.AuditActionDelete, "vlan_id", vlans.Delete(st, log))

	reg.get("/vlan-groups", vlans.ListGroups(st, log))
	reg.get("/vlan-groups/{group_id}", vlans.GetGroup(st, log))
	reg.mutate(http.MethodPost, "/vlan-groups", models.RoleOperator, "vlan_group", models.AuditActionCreate, "", vlans.CreateGroup(st, log))
	reg.mutate(http.MethodPut, "/vlan-groups/{group_id}", models.RoleOperator, "vlan_group", models.AuditActionUpdate, "group_id", vlans.UpdateGroup(st, log))
	reg.mutate(http.MethodDelete, "/vlan-groups/{group_id}", models.RoleOperator, "vlan_group", models.AuditActionDelete, "group_id", vlans.DeleteGroup(st, log))

	// -- IPAM prefixes, tree, search -------------------------------------
	reg.get("/prefixes", ipam.List(st, log))
	reg.get("/prefixes/{prefix_id}", ipam.Get(st, log))
	reg.mutate(http.MethodPost, "/prefixes", models.RoleOperator, "prefix", models.AuditActionCreate, "", ipam.Create(st, log))
	reg.mutate(http.MethodPut, "/prefixes/{prefix_id}", models.RoleOperator, "prefix", models.AuditActionUpdate, "prefix_id", ipam.Update(st, log))
	reg.mutate(http.MethodDelete, "/prefixes/{prefix_id}", models.RoleOperator, "prefix", models.AuditActionDelete, "prefix_id", ipam.Delete(st, log))
	reg.get("/prefixes/{prefix_id}/utilization", ipam.Utilization(st, log))
	reg.get("/prefixes/{prefix_id}/ancestors", ipam.Ancestors(st, log))
	reg.get("/prefixes/{prefix_id}/clients", ipam.Clients(st, log))
	reg.get("/ipam/tree", ipam.Tree(st, log))
	reg.get("/ipam/next-available-subnet", ipam.NextAvailableSubnet(st, log))
	reg.get("/search", ipam.Search(st, log))

	// -- DHCP scopes ------------------------------------------------------
	reg.get("/scopes", scopes.List(st, log))
	reg.get("/scopes/{scope_id}", scopes.Get(st, log))
	reg.mutate(http.MethodPost, "/scopes", models.RoleOperator, "scope", models.AuditActionCreate, "", scopes.Create(st, log))
	reg.mutate(http.MethodPut, "/scopes/{scope_id}", models.RoleOperator, "scope", models.AuditActionUpdate, "scope_id", scopes.Update(st, log))
	reg.mutate(http.MethodDelete, "/scopes/{scope_id}", models.RoleOperator, "scope", models.AuditActionDelete, "scope_id", scopes.Delete(st, log))
	reg.get("/scopes/{scope_id}/utilization", scopes.Utilization(st, log))
	reg.get("/scopes/{scope_id}/stats", scopes.Stats(st, log))

	// -- DHCP pools ---------------------------------------------------------
	reg.get("/scopes/{scope_id}/pools", pools.List(st, log))
	reg.get("/pools/{pool_id}", pools.Get(st, log))
	reg.mutate(http.MethodPost, "/scopes/{scope_id}/pools", models.RoleOperator, "pool", models.AuditActionCreate, "", pools.Create(st, log))
	reg.mutate(http.MethodPut, "/pools/{pool_id}", models.RoleOperator, "pool", models.AuditActionUpdate, "pool_id", pools.Update(st, log))
	reg.mutate(http.MethodDelete, "/pools/{pool_id}", models.RoleOperator, "pool", models.AuditActionDelete, "pool_id", pools.Delete(st, log))
	reg.get("/pools/{pool_id}/utilization", pools.Utilization(st, log))

	// -- Reservations -------------------------------------------------------
	reg.get("/pools/{pool_id}/reservations", reservations.List(st, log))
	reg.get("/reservations/{reservation_id}", reservations.Get(st, log))
	reg.mutate(http.MethodPost, "/pools/{pool_id}/reservations", models.RoleOperator, "reservation", models.AuditActionCreate, "", reservations.Create(st, log))
	reg.mutate(http.MethodPut, "/reservations/{reservation_id}", models.RoleOperator, "reservation", models.AuditActionUpdate, "reservation_id", reservations.Update(st, log))
	reg.mutate(http.MethodDelete, "/reservations/{reservation_id}", models.RoleOperator, "reservation", models.AuditActionDelete, "reservation_id", reservations.Delete(st, log))

	// -- Leases (read-only) ---------------------------------------------
	reg.get("/leases", leases.List(st, log))
	reg.get("/leases/{lease_id}", leases.Get(st, log))
	reg.get("/pools/{pool_id}/leases", leases.ListByPool(st, log))
	reg.get("/leases/by-ip/{ip_address}", leases.GetByIP(st, log))
	reg.get("/leases/by-mac/{mac_address}", leases.GetByMAC(st, log))

	// -- DHCP option catalog + values -------------------------------------
	reg.get("/options", options.ListCatalog(st, log))
	reg.get("/options/catalog", options.FullCatalog(st, log))
	reg.get("/options/{option_id}", options.GetCatalogEntry(st, log))
	reg.mutate(http.MethodPost, "/options", models.RoleOperator, "option_catalog", models.AuditActionCreate, "", options.CreateCatalogEntry(st, log))
	reg.mutate(http.MethodDelete, "/options/{option_id}", models.RoleOperator, "option_catalog", models.AuditActionDelete, "option_id", options.DeleteCatalogEntry(st, log))

	reg.get("/scopes/{scope_id}/options", options.GetScopeOptions(st, log))
	reg.mutate(http.MethodPost, "/scopes/{scope_id}/options", models.RoleOperator, "option_value", models.AuditActionCreate, "", options.SetScopeOption(st, log))
	reg.mutate(http.MethodPut, "/scopes/{scope_id}/options/{option_id}", models.RoleOperator, "option_value", models.AuditActionUpdate, "option_id", options.UpdateScopeOption(st, log))
	reg.mutate(http.MethodDelete, "/scopes/{scope_id}/options/{option_id}", models.RoleOperator, "option_value", models.AuditActionDelete, "option_id", options.DeleteScopeOption(st, log))

	reg.get("/pools/{pool_id}/options", options.GetPoolOptions(st, log))
	reg.mutate(http.MethodPost, "/pools/{pool_id}/options", models.RoleOperator, "option_value", models.AuditActionCreate, "", options.SetPoolOption(st, log))
	reg.mutate(http.MethodDelete, "/pools/{pool_id}/options/{option_id}", models.RoleOperator, "option_value", models.AuditActionDelete, "option_id", options.DeletePoolOption(st, log))

	// -- Templates -----------------------------------------------------
	reg.get("/templates", templates.List(st, log))
	reg.get("/templates/{template_id}", templates.Get(st, log))
	reg.mutate(http.MethodPost, "/templates", models.RoleOperator, "template", models.AuditActionCreate, "", templates.Create(st, log))
	reg.mutate(http.MethodPut, "/templates/{template_id}", models.RoleOperator, "template", models.AuditActionUpdate, "template_id", templates.Update(st, log))
	reg.mutate(http.MethodDelete, "/templates/{template_id}", models.RoleOperator, "template", models.AuditActionDelete, "template_id", templates.Delete(st, log))
	reg.get("/templates/{template_id}/options", templates.GetOptions(st, log))
	reg.mutate(http.MethodPost, "/templates/{template_id}/options", models.RoleOperator, "option_value", models.AuditActionCreate, "", templates.SetOption(st, log))
	reg.get("/templates/{template_id}/scopes", templates.ListScopeAssocs(st, log))
	reg.mutate(http.MethodPost, "/templates/{template_id}/scopes/{scope_id}", models.RoleOperator, "template_scope_assoc", models.AuditActionCreate, "scope_id", templates.AssociateScope(st, log))
	reg.mutate(http.MethodDelete, "/templates/{template_id}/scopes/{scope_id}", models.RoleOperator, "template_scope_assoc", models.AuditActionDelete, "scope_id", templates.DisassociateScope(st, log))

	// -- Tags -------------------------------------------------------------
	reg.get("/tags", tags.List(st, log))
	reg.mutate(http.MethodPost, "/tags", models.RoleOperator, "tag", models.AuditActionCreate, "", tags.Create(st, log))
	reg.mutate(http.MethodDelete, "/tags/{tag_id}", models.RoleOperator, "tag", models.AuditActionDelete, "tag_id", tags.Delete(st, log))
	reg.mutate(http.MethodPost, "/tags/{tag_id}/entities", models.RoleOperator, "entity_tag", models.AuditActionCreate, "tag_id", tags.AddEntity(st, log))
	reg.mutate(http.MethodDelete, "/tags/{tag_id}/entities/{entity_type}/{entity_id}", models.RoleOperator, "entity_tag", models.AuditActionDelete, "entity_id", tags.RemoveEntity(st, log))

	// -- Clusters & HA (config mutations are admin-only, per amendment A15).
	reg.get("/clusters", clusters.List(st, log))
	reg.get("/clusters/{cluster_id}", clusters.Get(st, log))
	reg.mutate(http.MethodPost, "/clusters", models.RoleAdmin, "cluster", models.AuditActionCreate, "", clusters.Create(st, log))
	reg.mutate(http.MethodPut, "/clusters/{cluster_id}", models.RoleAdmin, "cluster", models.AuditActionUpdate, "cluster_id", clusters.Update(st, log))
	reg.mutate(http.MethodDelete, "/clusters/{cluster_id}", models.RoleAdmin, "cluster", models.AuditActionDelete, "cluster_id", clusters.Delete(st, log))
	reg.get("/clusters/{cluster_id}/servers", clusters.ListServers(st, log))
	reg.mutate(http.MethodPost, "/clusters/{cluster_id}/servers", models.RoleAdmin, "cluster_server", models.AuditActionCreate, "", clusters.RegisterServer(st, log))
	reg.mutate(http.MethodDelete, "/clusters/{cluster_id}/servers/{server_id}", models.RoleAdmin, "cluster_server", models.AuditActionDelete, "server_id", clusters.DeregisterServer(st, log))
	reg.get("/clusters/{cluster_id}/health", clusters.Health(st, log))
	reg.get("/clusters/{cluster_id}/heartbeats", clusters.Heartbeats(st, log))
	reg.mutate(http.MethodPost, "/clusters/{cluster_id}/failover", models.RoleAdmin, "cluster", models.AuditActionFailover, "cluster_id", handleFailover(ha, log))
	reg.mutate(http.MethodPost, "/clusters/{cluster_id}/bootstrap", models.RoleAdmin, "cluster", models.AuditActionUpdate, "cluster_id", clusters.Bootstrap(st, log))

	// -- Dashboard & live updates -----------------------------------------
	reg.get("/dashboard/stats", dashboard.Stats(st, log))
	reg.get("/dashboard/utilization-chart", dashboard.UtilizationChart(st, log))
	reg.get("/dashboard/dhcp-stats", dashboard.DHCPStats(st, log))
	reg.get("/events", dashboard.Events(st, ha, log))

	// -- Audit & config -----------------------------------------------------
	reg.get("/audit/log", handleAuditLog(st, log))
	reg.r.HandleFunc("/config", requireRole(models.RoleAdmin, log, handleGetConfig(cfg, log))).Methods(http.MethodGet)
	reg.r.HandleFunc("/config/reload", requireRole(models.RoleAdmin, log, handleConfigReload(log))).Methods(http.MethodPost)

	root.NotFoundHandler = notFoundHandler(log)
	root.MethodNotAllowedHandler = methodNotAllowedHandler(log)

	var handler http.Handler = root
	handler = corsMiddleware(cfg.Server.CORSAllowedOrigins)(handler)
	handler = recoverMiddleware(log)(handler)
	handler = logger.Middleware(log)(handler)
	return handler
}

// notFoundHandler and methodNotAllowedHandler replace gorilla/mux's default
// plain-text responses with the canonical error envelope so every response
// under /api/v1 — including routing misses — is consistent JSON.
func notFoundHandler(log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.RespondError(w, r, log, httpx.NotFound("no such route: "+r.Method+" "+r.URL.Path))
	})
}

func methodNotAllowedHandler(log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.RespondError(w, r, log, &httpx.APIError{
			Status:  http.StatusMethodNotAllowed,
			Code:    httpx.CodeInvalidInput,
			Message: r.Method + " not allowed on " + r.URL.Path,
		})
	})
}
