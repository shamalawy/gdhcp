// Package store defines the repository-pattern interfaces gdhcp's DHCP
// engine, HA cluster manager, and REST API are written against. This file
// (interface.go) is the compiled source of truth for those interfaces per
// amendment A13: docs/CONTRACTS.md Part 1 is its blueprint, but type
// references here must compile against internal/models, and signatures were
// adjusted where needed (package qualifiers, string UUIDs throughout) while
// preserving CONTRACTS' names and semantics.
//
// The concrete PostgreSQL implementation (postgres.go and friends) is owned
// by the Store unit, not Foundation — this file only declares interfaces
// and the two small generic types (ListParams/ListResult) they share.
package store

import (
	"context"
	"net"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// ---------------------------------------------------------------------------
// Shared list/pagination types
// ---------------------------------------------------------------------------

// ListParams carries the common query parameters every paginated List
// method accepts (docs/CONTRACTS.md Part 2 "Common Query Parameters").
// Filters holds entity-specific equality filters (e.g. {"vrf_id": "...",
// "state": "active"}); each store method documents which keys it honors.
type ListParams struct {
	Limit   int
	Offset  int
	Sort    string // Comma-separated fields; "-" prefix means descending.
	Search  string // Global substring search, entity-specific fields.
	Filters map[string]string
}

// ListResult is the generic envelope every paginated List method returns,
// mirroring the {items, total, limit, offset} JSON shape from
// docs/CONTRACTS.md Part 2.
type ListResult[T any] struct {
	Items  []T
	Total  int64
	Limit  int
	Offset int
}

// ---------------------------------------------------------------------------
// LeaseStore — docs/CONTRACTS.md Part 1, adapted per amendment A6 (the
// pre-materialized "free row" pseudocode there is void; allocation claims a
// candidate IP via INSERT ... ON CONFLICT DO NOTHING, retried up to 3
// rounds).
// ---------------------------------------------------------------------------

// LeaseStore handles DHCP lease allocation, renewal, and expiry. All methods
// must be thread-safe and support concurrent calls from every server in a
// cluster racing to allocate against the same pool.
type LeaseStore interface {
	// AllocateLease reserves a free IP in the pool for this client following
	// amendment A6's algorithm: reservation match (via ReservationsForKeys),
	// then renew affinity, then a scan of unused candidates claimed via ON
	// CONFLICT DO NOTHING with up to 3 retry rounds. Returns models.ErrNoLeases
	// if no IP is available after retries are exhausted. The ident parameter
	// is a models.ClientIdentity; hostname, if non-nil, is stored in the lease.
	//
	// leaseDur is the lease length to persist — the caller's resolved
	// Option 51 / config.DHCP.DefaultLeaseTime value, so the stored expiry
	// matches what the client is told on the wire. A non-positive leaseDur
	// falls back to 24h so a bad caller never writes a zero-length lease.
	AllocateLease(ctx context.Context, poolID string, ident models.ClientIdentity, hostname *string, leaseDur time.Duration) (*models.DHCPLease, error)

	// RenewLease extends the lease expiry time for an existing lease.
	// Returns models.ErrLeaseNotFound if the lease is expired or clientMAC
	// doesn't match the lease on file.
	// hostname carries the client's option 12 name so a client that renames
	// itself between DISCOVER and RENEW does not leave a stale
	// client_hostname on the lease; nil leaves the stored value alone.
	RenewLease(ctx context.Context, leaseID string, clientMAC string, newExpiry time.Time, hostname *string) (*models.DHCPLease, error)

	// ReleaseLease marks a lease as released by the client. Idempotent: safe
	// to call multiple times.
	ReleaseLease(ctx context.Context, leaseID string, clientMAC string) error

	// AbandonLease marks a lease as abandoned (e.g. a ping-check found the
	// address already answering on the wire despite having no lease
	// record). Unlike ReleaseLease, an abandoned lease is excluded from
	// AllocateLease's renew-affinity/candidate reclaim until it is next
	// swept by ExpireLeasesOlderThan — so a caller retrying allocation for
	// the same client right after abandoning an address is not handed that
	// exact address straight back. Idempotent: safe to call multiple times.
	AbandonLease(ctx context.Context, leaseID string, clientMAC string) error

	// GetLeaseByIP returns the lease for an IP within poolID if it exists
	// and is not expired. Returns nil, nil if no lease found (not an error).
	GetLeaseByIP(ctx context.Context, poolID string, ip net.IP) (*models.DHCPLease, error)

	// GetLeaseByClient returns the active lease for this client, if any.
	// Searches across all pools using ident.BindingKeys() if an existing
	// lease is bound to any of this client's identifiers. Returns nil, nil
	// if not found.
	GetLeaseByClient(ctx context.Context, ident models.ClientIdentity) (*models.DHCPLease, error)

	// GetLeasesByPool lists leases in a pool, optionally filtered by state
	// ("" for all states). Supports pagination: limit=100, offset=0 default.
	GetLeasesByPool(ctx context.Context, poolID string, state string, offset, limit int) ([]*models.DHCPLease, int64, error)

	// ExpireLeasesOlderThan marks all leases with lease_end < cutoff as
	// expired. Called by the background reaper; returns count expired.
	ExpireLeasesOlderThan(ctx context.Context, cutoff time.Time) (int64, error)

	// ReservationsForKeys checks if a reservation exists for any of the given
	// binding keys in pool, returning the first match in the keys' precedence
	// order (which must match models.ClientIdentity.BindingKeys()' order).
	// Returns nil, nil if no reservation found.
	ReservationsForKeys(ctx context.Context, poolID string, keys []string) (*models.DHCPReservation, error)

	// ReservationForLease checks if a reservation exists for this binding
	// key in pool. Returns nil, nil if no reservation found.
	// (Deprecated: use ReservationsForKeys instead, but kept for compatibility.)
	ReservationForLease(ctx context.Context, poolID string, bindingKey string) (*models.DHCPReservation, error)
}

// ---------------------------------------------------------------------------
// ConfigStore — docs/CONTRACTS.md Part 1.
// ---------------------------------------------------------------------------

// ConfigStore fetches DHCP configuration (scopes, pools, options,
// templates). All methods must be thread-safe and support concurrent reads;
// the DHCP engine calls these on the hot path for every packet.
type ConfigStore interface {
	// GetVRFByInterface returns the VRF that owns this interface, or the
	// default VRF if not found.
	GetVRFByInterface(ctx context.Context, ifname string) (*models.VRF, error)

	// GetVRFByID fetches a VRF by UUID.
	GetVRFByID(ctx context.Context, vrfID string) (*models.VRF, error)

	// GetScopesByVRF lists all scopes in a VRF.
	GetScopesByVRF(ctx context.Context, vrfID string) ([]*models.DHCPScope, error)

	// GetScopeByID fetches a scope by UUID.
	GetScopeByID(ctx context.Context, scopeID string) (*models.DHCPScope, error)

	// FindScopeByGiaddr returns the scope containing this giaddr (relay
	// agent IP), used for relay agent Option 82 routing.
	FindScopeByGiaddr(ctx context.Context, giaddr net.IP) (*models.DHCPScope, error)

	// FindScopeByNetwork returns a scope matching the given network CIDR.
	FindScopeByNetwork(ctx context.Context, vrfID string, network *net.IPNet) (*models.DHCPScope, error)

	// GetPoolsByScope lists all pools in a scope, ordered by priority.
	GetPoolsByScope(ctx context.Context, scopeID string) ([]*models.DHCPPool, error)

	// GetPoolByID fetches a pool by UUID.
	GetPoolByID(ctx context.Context, poolID string) (*models.DHCPPool, error)

	// GetOptionsByScope fetches option values defined on a scope.
	GetOptionsByScope(ctx context.Context, scopeID string) ([]*models.DHCPOption, error)

	// GetOptionsByPool fetches option values defined on a pool.
	GetOptionsByPool(ctx context.Context, poolID string) ([]*models.DHCPOption, error)

	// GetOptionsByReservation fetches option values defined on a
	// reservation.
	GetOptionsByReservation(ctx context.Context, reservationID string) ([]*models.DHCPOption, error)

	// GetOptionsByTemplate fetches option values defined on a template.
	// (Not present in CONTRACTS.md's ConfigStore, added here because
	// GET /api/v1/templates/{id}/options needs a store method and the other
	// three scope levels each already have one — see docs/AMENDMENTS.md A13
	// on adjusting for completeness while preserving semantics.)
	GetOptionsByTemplate(ctx context.Context, templateID string) ([]*models.DHCPOption, error)

	// GetGlobalOptions fetches globally-defined DHCP options.
	GetGlobalOptions(ctx context.Context) ([]*models.DHCPOption, error)

	// ResolveOption returns the effective option value traversing the
	// inheritance hierarchy. Precedence: reservation > pool > scope >
	// template > global. Returns nil, nil if the option is not set anywhere
	// in the chain.
	ResolveOption(ctx context.Context, optCode int16, scopeID, poolID, reservationID string) (*models.DHCPOption, error)

	// GetTemplateByID fetches a template by UUID.
	GetTemplateByID(ctx context.Context, templateID string) (*models.Template, error)

	// GetTemplatesByScope lists templates applied to a scope, ordered by
	// priority.
	GetTemplatesByScope(ctx context.Context, scopeID string) ([]*models.Template, error)

	// CanAllocateInPool checks if a specific IP can be allocated in pool.
	// Returns false if reserved, leased, or outside the pool's range.
	CanAllocateInPool(ctx context.Context, poolID string, ip net.IP) (bool, error)
}

// ---------------------------------------------------------------------------
// IPAMStore — docs/CONTRACTS.md Part 1 (read-hierarchy queries).
// ---------------------------------------------------------------------------

// IPAMStore provides hierarchical IPAM prefix queries.
type IPAMStore interface {
	// GetPrefixByID fetches a prefix by UUID.
	GetPrefixByID(ctx context.Context, prefixID string) (*models.IPAMPrefix, error)

	// GetPrefixesByVRF lists all top-level prefixes in a VRF.
	GetPrefixesByVRF(ctx context.Context, vrfID string) ([]*models.IPAMPrefix, error)

	// GetChildPrefixes lists direct children of a prefix (next level in the
	// hierarchy).
	GetChildPrefixes(ctx context.Context, parentID string) ([]*models.IPAMPrefix, error)

	// GetAncestorPrefixes returns ancestors of a prefix (parent,
	// grandparent, etc.), nearest first.
	GetAncestorPrefixes(ctx context.Context, prefixID string) ([]*models.IPAMPrefix, error)

	// FindPrefixContaining returns the immediate parent prefix containing
	// this CIDR, or nil if none does.
	FindPrefixContaining(ctx context.Context, vrfID string, cidr *net.IPNet) (*models.IPAMPrefix, error)

	// ListPrefixesInCIDR returns all prefixes (at any level) within a given
	// CIDR.
	ListPrefixesInCIDR(ctx context.Context, vrfID string, cidr *net.IPNet) ([]*models.IPAMPrefix, error)
}

// ---------------------------------------------------------------------------
// StatsRecorder — docs/CONTRACTS.md Part 1.
// ---------------------------------------------------------------------------

// StatsRecorder records DHCP transaction and utilization statistics. Record*
// methods are called on the DHCP engine's hot path and must not block on
// I/O (implementations typically buffer and flush asynchronously).
type StatsRecorder interface {
	RecordDiscover(scopeID string)
	RecordOffer(scopeID string)
	RecordRequest(scopeID string)
	RecordAck(scopeID string)
	RecordNak(scopeID string)
	RecordDecline(scopeID string)
	RecordRelease(scopeID string)
	RecordNoLeases(scopeID string)

	// GetStats returns aggregated stats for a scope over a time window.
	GetStats(ctx context.Context, scopeID string, window time.Duration) (*models.DHCPStats, error)

	// SampleUtilization captures a current utilization snapshot for every
	// scope/pool/VRF. Called every 60 seconds by a background job.
	SampleUtilization(ctx context.Context) error
}

// ---------------------------------------------------------------------------
// UserStore — docs/CONTRACTS.md Part 1, plus List/Delete for full endpoint
// coverage.
// ---------------------------------------------------------------------------

// UserStore manages users and API tokens.
type UserStore interface {
	GetUserByID(ctx context.Context, userID int64) (*models.User, error)
	GetUserByUsername(ctx context.Context, username string) (*models.User, error)

	// CreateUser creates a new user; PasswordHash must already be hashed.
	CreateUser(ctx context.Context, user *models.User) error

	// UpdateUser updates user fields except password/role changes callers
	// must authorize separately (RBAC, amendment A15).
	UpdateUser(ctx context.Context, user *models.User) error

	// DeleteUser removes a user. Not in CONTRACTS.md's UserStore; added for
	// DELETE /api/v1/users/{user_id}.
	DeleteUser(ctx context.Context, userID int64) error

	// ListUsers lists users (admin only at the API layer). Not in
	// CONTRACTS.md's UserStore; added for GET /api/v1/users.
	ListUsers(ctx context.Context, params ListParams) (ListResult[*models.User], error)

	// CreateAPIToken creates a new API token; TokenHash must already be the
	// SHA-256 hex digest of the raw token (amendment A14).
	CreateAPIToken(ctx context.Context, token *models.APIToken) error

	// ValidateAPIToken checks if a token hash exists and is not expired.
	ValidateAPIToken(ctx context.Context, tokenHash string) (*models.APIToken, error)

	// RevokeAPIToken deletes an API token by ID.
	RevokeAPIToken(ctx context.Context, tokenID int64) error

	// ListAPITokens returns all tokens for a user.
	ListAPITokens(ctx context.Context, userID int64) ([]*models.APIToken, error)
}

// ---------------------------------------------------------------------------
// ClusterStore — docs/CONTRACTS.md Part 1, plus CRUD/health for full
// endpoint coverage.
// ---------------------------------------------------------------------------

// ClusterStore manages cluster heartbeat and election state.
type ClusterStore interface {
	// GetClusterByID fetches cluster config.
	GetClusterByID(ctx context.Context, clusterID string) (*models.Cluster, error)

	// WriteHeartbeat updates this server's heartbeat row: heartbeat_time =
	// now(), role = 'PRIMARY'. Returns models.ErrNotFound if the server
	// isn't registered in the cluster.
	WriteHeartbeat(ctx context.Context, clusterID, serverID string) error

	// GetHeartbeats returns all heartbeat rows in a cluster.
	GetHeartbeats(ctx context.Context, clusterID string) ([]*models.Heartbeat, error)

	// ElectionQuery returns the elected primary server ID per the election
	// algorithm: lowest node_id among servers with a heartbeat fresher than
	// the cluster's heartbeat_ttl_ms.
	ElectionQuery(ctx context.Context, clusterID string) (string, error)

	// RegisterServer adds a server to a cluster (self-registration on first
	// boot, or manual pre-registration).
	RegisterServer(ctx context.Context, server *models.ClusterServer) error

	// ListServers returns all servers in a cluster.
	ListServers(ctx context.Context, clusterID string) ([]*models.ClusterServer, error)

	// UpdateLeaseVersion increments the cluster's config version (the
	// primary calls this on config change) and returns the new version.
	UpdateLeaseVersion(ctx context.Context, clusterID, serverID string) (int, error)

	// UpdateLastConfigVersion records the config version this server has
	// applied.
	UpdateLastConfigVersion(ctx context.Context, clusterID, serverID string, version int) error

	// ListClusters lists clusters. Not in CONTRACTS.md's ClusterStore;
	// added for GET /api/v1/clusters.
	ListClusters(ctx context.Context, params ListParams) (ListResult[*models.Cluster], error)

	// CreateCluster creates a cluster.
	CreateCluster(ctx context.Context, c *models.Cluster) error

	// UpdateCluster updates cluster config (name, VIP, heartbeat timing).
	UpdateCluster(ctx context.Context, c *models.Cluster) error

	// DeleteCluster deletes a cluster. Implementations should reject this
	// (models.ErrHasDependents) if servers are still registered, matching
	// "Delete cluster (no servers)" in docs/api/endpoints.md.
	DeleteCluster(ctx context.Context, clusterID string) error

	// DeregisterServer removes a server from a cluster.
	DeregisterServer(ctx context.Context, clusterID, serverID string) error

	// GetClusterHealth computes the health rollup for
	// GET /api/v1/clusters/{id}/health: each server's role, VIP ownership,
	// and heartbeat freshness, plus an overall cluster_state.
	GetClusterHealth(ctx context.Context, clusterID string) (*models.ClusterHealth, error)
}

// ---------------------------------------------------------------------------
// AuditStore — docs/CONTRACTS.md Part 1.
// ---------------------------------------------------------------------------

// AuditStore logs all user actions for compliance.
type AuditStore interface {
	// LogAction records a user action.
	LogAction(ctx context.Context, log *models.AuditLog) error

	// ListAuditLog returns paginated audit logs. filters may include
	// "entity_type", "entity_id", "user_id", "action".
	ListAuditLog(ctx context.Context, offset, limit int, filters map[string]interface{}) ([]*models.AuditLog, int64, error)
}

// ---------------------------------------------------------------------------
// The following interfaces are not present in docs/CONTRACTS.md Part 1 —
// they fill the CRUD/List surface docs/api/endpoints.md requires that Part 1
// (a read-heavy blueprint for the DHCP engine's hot path) does not cover.
// ---------------------------------------------------------------------------

// VRFStore provides CRUD + List for VRFs (docs/api/endpoints.md "VRF
// Endpoints"). Single-entity reads go through ConfigStore.GetVRFByID.
type VRFStore interface {
	ListVRFs(ctx context.Context, params ListParams) (ListResult[*models.VRF], error)
	CreateVRF(ctx context.Context, v *models.VRF) error
	UpdateVRF(ctx context.Context, v *models.VRF) error

	// DeleteVRF deletes a VRF. Implementations must reject
	// (models.ErrHasDependents) a VRF that still has scopes or prefixes,
	// matching "Delete VRF (no scopes)" in docs/api/endpoints.md.
	DeleteVRF(ctx context.Context, id string) error
}

// IPAMAdminStore provides CRUD, tree assembly, and search over IPAM
// prefixes (docs/api/endpoints.md "IPAM Prefix Endpoints"). Hierarchy reads
// go through IPAMStore.
type IPAMAdminStore interface {
	// ListPrefixes lists prefixes with optional filters "vrf_id", "type",
	// and "parent_id" (docs/api/endpoints.md GET /prefixes query params).
	// "parent_id" is not in the documented table but is supported for the
	// VRF-detail and prefix-detail UIs: the literal value "root" means
	// "parent_id IS NULL", any other value is an exact parent_id match, and
	// omitting the filter entirely returns the flat unscoped list.
	ListPrefixes(ctx context.Context, params ListParams) (ListResult[*models.IPAMPrefix], error)

	// CreatePrefix creates a prefix. Implementations must reject
	// (models.ErrConflict) an overlapping subnet-type prefix within the
	// same VRF, matching the ipam_prefixes EXCLUDE constraint.
	CreatePrefix(ctx context.Context, p *models.IPAMPrefix) error
	UpdatePrefix(ctx context.Context, p *models.IPAMPrefix) error

	// DeletePrefix deletes a prefix. Implementations must reject
	// (models.ErrHasDependents) a prefix that still has children, matching
	// "Delete prefix (no children)" in docs/api/endpoints.md.
	DeletePrefix(ctx context.Context, id string) error

	// GetIPAMTree returns the lazily-expandable tree rooted at parentID (nil
	// for VRF top-level) down to depth levels, for GET /api/v1/ipam/tree.
	GetIPAMTree(ctx context.Context, vrfID string, parentID *string, depth int) ([]*models.IPAMTreeNode, error)

	// GetPrefixUtilization computes current utilization for any prefix, for
	// GET /api/v1/prefixes/{id}/utilization: a DHCP view rolled up over
	// every scope anywhere beneath this prefix, plus an address-space view
	// of how much of the prefix's own CIDR its direct children cover.
	GetPrefixUtilization(ctx context.Context, prefixID string) (*models.PrefixUtilization, error)

	// ListClientsUnderPrefix returns a paginated list of DHCP clients (leases
	// and reservations, both enabled and disabled) living in prefixID or any
	// of its descendant prefixes. The list yields ONE ROW PER ADDRESS: if an
	// address holds both a lease and a reservation, only the lease row is
	// returned (omitting the reservation) to avoid duplication. Clients are
	// returned as models.DHCPClient (a union row with kind "lease" or
	// "reservation"). Supports filters: "kind" (lease|reservation), "state"
	// (binding_state for leases; the protocol-level filter), "status"
	// (free|assigned|reserved|abandoned; the operator-facing classification),
	// and "q" (free-text search across IP/MAC/hostname). Supports sorts:
	// ip_address (default), lease_end, client_mac, client_hostname, each with
	// an id tiebreaker for deterministic pagination. Used for GET /api/v1/
	// prefixes/{prefix_id}/clients.
	ListClientsUnderPrefix(ctx context.Context, prefixID string, params ListParams) (ListResult[*models.DHCPClient], error)
}

// VLANStore provides CRUD for VLAN groups and VLANs, plus the reverse
// lookup from a VLAN to the IPAM prefixes assigned to it. VLANs record
// layer-2 segmentation; VRFs (above) record layer-3 routing isolation.
type VLANStore interface {
	// ListVLANGroups supports free-text search over the group name.
	ListVLANGroups(ctx context.Context, params ListParams) (ListResult[*models.VLANGroup], error)
	GetVLANGroupByID(ctx context.Context, id string) (*models.VLANGroup, error)
	CreateVLANGroup(ctx context.Context, g *models.VLANGroup) error
	UpdateVLANGroup(ctx context.Context, g *models.VLANGroup) error
	DeleteVLANGroup(ctx context.Context, id string) error

	// ListVLANs supports filters "group_id" (the literal "none" selects
	// ungrouped VLANs) and "status", plus search over name and VID.
	ListVLANs(ctx context.Context, params ListParams) (ListResult[*models.VLAN], error)
	GetVLANByID(ctx context.Context, id string) (*models.VLAN, error)
	CreateVLAN(ctx context.Context, v *models.VLAN) error
	UpdateVLAN(ctx context.Context, v *models.VLAN) error

	// DeleteVLAN removes a VLAN. Prefixes assigned to it are unassigned
	// (ipam_prefixes.vlan_id ON DELETE SET NULL), never deleted.
	DeleteVLAN(ctx context.Context, id string) error

	// ListPrefixesByVLAN returns every prefix assigned to a VLAN.
	ListPrefixesByVLAN(ctx context.Context, vlanID string) ([]*models.IPAMPrefix, error)
}

// ScopeStore provides CRUD, List, and current utilization for DHCP scopes.
// Single-entity/VRF-scoped reads go through ConfigStore.
type ScopeStore interface {
	// ListScopes supports filters "vrf_id" and "cluster_id".
	ListScopes(ctx context.Context, params ListParams) (ListResult[*models.DHCPScope], error)
	CreateScope(ctx context.Context, s *models.DHCPScope) error
	UpdateScope(ctx context.Context, s *models.DHCPScope) error

	// DeleteScope deletes a scope and cascades to its pools/leases, per
	// "Delete scope (cascade pools)" in docs/api/endpoints.md.
	DeleteScope(ctx context.Context, id string) error

	// GetScopeUtilization computes current total/active/reserved/available
	// counts for GET /api/v1/scopes/{id}/utilization.
	GetScopeUtilization(ctx context.Context, scopeID string) (*models.Utilization, error)
}

// PoolStore provides CRUD, List, and current utilization for DHCP pools.
// Single-entity reads go through ConfigStore.
type PoolStore interface {
	ListPoolsByScope(ctx context.Context, scopeID string, params ListParams) (ListResult[*models.DHCPPool], error)
	CreatePool(ctx context.Context, p *models.DHCPPool) error
	UpdatePool(ctx context.Context, p *models.DHCPPool) error

	// DeletePool deletes a pool and cascades to its leases, per "Delete
	// pool (cascade leases)" in docs/api/endpoints.md.
	DeletePool(ctx context.Context, id string) error

	GetPoolUtilization(ctx context.Context, poolID string) (*models.Utilization, error)
}

// ReservationStore provides CRUD + List for DHCP reservations.
type ReservationStore interface {
	ListReservationsByPool(ctx context.Context, poolID string, params ListParams) (ListResult[*models.DHCPReservation], error)
	GetReservation(ctx context.Context, id string) (*models.DHCPReservation, error)

	// ReservationByBindingKey checks if a reservation exists for this binding
	// key in pool. Returns nil, nil if no reservation found (not an error) — a
	// nil result means "no such reservation" and allows the API handler to
	// ask "is this client already pinned?" without treating absence as an error.
	ReservationByBindingKey(ctx context.Context, poolID, bindingKey string) (*models.DHCPReservation, error)

	// ReservationByIP checks if a reservation exists for this IP in pool.
	// Returns nil, nil if no reservation found (not an error) — a nil result
	// means "no such reservation" and allows the API handler to ask "is this
	// address already reserved?" without treating absence as an error.
	ReservationByIP(ctx context.Context, poolID string, ip net.IP) (*models.DHCPReservation, error)

	// CreateReservation creates a reservation. Implementations must reject:
	// - (models.ErrInvalidInput) a ReservedIP outside the pool's range,
	// - (models.ErrReservationClientExists) wrapped with models.ErrConflict when
	//   a reservation for this binding_key already exists in the pool (duplicate
	//   UNIQUE (pool_id, binding_key) constraint, per amendment A19),
	// - (models.ErrReservationIPTaken) wrapped with models.ErrConflict when a
	//   different client already reserves this IP in the pool (amendment A19),
	// - (models.ErrReservationIPLeased) wrapped with models.ErrConflict when an
	//   active lease for a different client already holds this IP (the
	//   allocator's reservation-match step always wins the allocation round, so
	//   an unguarded conflict here would starve that other client of any
	//   address, per amendment A19).
	CreateReservation(ctx context.Context, r *models.DHCPReservation) error

	// UpdateReservation updates a reservation. Implementations must reject the
	// same conflicts as CreateReservation except for the row being updated
	// (checking a duplicate binding key or active lease against all other rows,
	// not including this reservation itself). This guards the PUT handler's
	// reserved_ip field reassignment (amendment A19).
	UpdateReservation(ctx context.Context, r *models.DHCPReservation) error

	DeleteReservation(ctx context.Context, id string) error
}

// LeaseQueryStore provides the read-only listing/lookup surface
// docs/api/endpoints.md's "Lease Endpoints (Read-Only)" needs beyond
// LeaseStore's allocation-path methods. Operator-driven lease mutations
// live in LeaseAdminStore and on LeaseStore (release/abandon/renew); the
// "read-only" in that endpoints.md heading is now scoped to the listing
// routes only.
type LeaseQueryStore interface {
	// ListLeases supports filters "scope_id", "pool_id", "state" (binding_state;
	// the protocol-level filter), and "status" (free|assigned|reserved|
	// abandoned; the operator-facing classification, amendment A20), plus
	// ListParams.Search, matched as a case-insensitive substring against
	// the lease's IP, MAC and client hostname.
	ListLeases(ctx context.Context, params ListParams) (ListResult[*models.DHCPLease], error)
	GetLease(ctx context.Context, id string) (*models.DHCPLease, error)
	// ListLeasesByPool supports filter "state" (binding_state; the protocol-level
	// filter) and "status" (free|assigned|reserved|abandoned; the operator-facing
	// classification, amendment A20), plus ListParams.Search, matched as a
	// case-insensitive substring against the lease's IP, MAC and client hostname.
	ListLeasesByPool(ctx context.Context, poolID string, params ListParams) (ListResult[*models.DHCPLease], error)

	// GetLeaseByIPGlobal looks up a lease by IP across all pools, for
	// GET /api/v1/leases/by-ip/{ip_address} (unlike LeaseStore.GetLeaseByIP,
	// which is scoped to one pool for the allocation hot path).
	GetLeaseByIPGlobal(ctx context.Context, ip net.IP) (*models.DHCPLease, error)

	// ListLeasesByMAC returns every lease (active or historical, across
	// pools) bound to mac, for GET /api/v1/leases/by-mac/{mac_address}.
	ListLeasesByMAC(ctx context.Context, mac string) ([]*models.DHCPLease, error)
}

// LeaseAdminStore holds the one operator-driven lease mutation that has no
// counterpart on the DHCP engine's allocation path. Release, abandon and
// renew are the engine's own LeaseStore methods, reused as-is by the API's
// lease action endpoints; deletion exists only for operators, so it lives
// here.
type LeaseAdminStore interface {
	// DeleteLease removes the lease row outright. Deleting an active lease
	// also decrements the pool's cached active-lease count, so the
	// dashboard rollup stays consistent. Returns models.ErrLeaseNotFound if
	// no such lease exists.
	DeleteLease(ctx context.Context, id string) error

	// ForceRenewLease extends a lease to expiry on an operator's say-so,
	// without the MAC check or the active-state precondition
	// LeaseStore.RenewLease applies to the client-driven renewal path. A
	// lease that had expired, been released or been abandoned is put back
	// into 'active' — safe because the (pool_id, ip_address) unique
	// constraint means this row *is* that address: there is no second row
	// another client could be holding it through. Returns
	// models.ErrLeaseNotFound if no such lease exists.
	ForceRenewLease(ctx context.Context, id string, expiry time.Time) (*models.DHCPLease, error)
}

// OptionCatalogStore provides CRUD + List over the IANA/custom DHCP option
// catalog (dhcp_option_catalog). Migration 0002 (owned by a peer unit)
// seeds the full IANA set; this store lets admins add/remove custom entries
// beyond it via docs/api/endpoints.md's POST/DELETE /options.
type OptionCatalogStore interface {
	// ListOptionCatalog supports filters "standard"/"custom" ("true"/
	// "false") for GET /api/v1/options.
	ListOptionCatalog(ctx context.Context, params ListParams) (ListResult[*models.OptionCatalogEntry], error)

	// GetAllOptionCatalog returns every catalog entry, unpaginated, for
	// GET /api/v1/options/catalog.
	GetAllOptionCatalog(ctx context.Context) ([]*models.OptionCatalogEntry, error)

	GetOptionCatalogEntry(ctx context.Context, code int16) (*models.OptionCatalogEntry, error)
	CreateOptionCatalogEntry(ctx context.Context, entry *models.OptionCatalogEntry) error
	DeleteOptionCatalogEntry(ctx context.Context, code int16) error
}

// OptionValueStore provides writes for option values at every inheritance
// level (dhcp_option_values). Reads live on ConfigStore
// (GetOptionsByScope/Pool/Reservation/Template/GetGlobalOptions).
type OptionValueStore interface {
	// CreateOptionValue sets an option at whatever scope level/entity ov
	// specifies (global/template/scope/pool/reservation).
	CreateOptionValue(ctx context.Context, ov *models.DHCPOption) error
	UpdateOptionValue(ctx context.Context, id string, value string, isEnabled bool) (*models.DHCPOption, error)
	DeleteOptionValue(ctx context.Context, id string) error
}

// TemplateStore provides CRUD for templates and their scope/pool
// associations. Single-entity reads go through ConfigStore.
type TemplateStore interface {
	ListTemplates(ctx context.Context, params ListParams) (ListResult[*models.Template], error)
	CreateTemplate(ctx context.Context, t *models.Template) error
	UpdateTemplate(ctx context.Context, t *models.Template) error
	DeleteTemplate(ctx context.Context, id string) error

	ListTemplateScopeAssocs(ctx context.Context, templateID string) ([]*models.TemplateScopeAssoc, error)
	AssociateTemplateScope(ctx context.Context, templateID, scopeID string, priority int) (*models.TemplateScopeAssoc, error)
	DisassociateTemplateScope(ctx context.Context, templateID, scopeID string) error

	ListTemplatePoolAssocs(ctx context.Context, templateID string) ([]*models.TemplatePoolAssoc, error)
	AssociateTemplatePool(ctx context.Context, templateID, poolID string, priority int) (*models.TemplatePoolAssoc, error)
	DisassociateTemplatePool(ctx context.Context, templateID, poolID string) error
}

// TagStore provides CRUD for tags and their entity assignments.
type TagStore interface {
	ListTags(ctx context.Context, params ListParams) (ListResult[*models.Tag], error)
	CreateTag(ctx context.Context, t *models.Tag) error
	DeleteTag(ctx context.Context, id string) error

	AddEntityTag(ctx context.Context, tagID string, entityType models.TaggableEntityType, entityID string) (*models.EntityTag, error)
	RemoveEntityTag(ctx context.Context, tagID string, entityType models.TaggableEntityType, entityID string) error

	// ListTagsForEntity returns every tag attached to one entity, for
	// entity detail views.
	ListTagsForEntity(ctx context.Context, entityType models.TaggableEntityType, entityID string) ([]*models.Tag, error)
}

// DashboardStore aggregates system-wide rollups for the dashboard and its
// SSE `stats` push (amendment A12).
type DashboardStore interface {
	GetDashboardStats(ctx context.Context, window time.Duration) (*models.DashboardStats, error)

	// GetUtilizationChart returns a daily/hourly history series for
	// GET /api/v1/dashboard/utilization-chart.
	GetUtilizationChart(ctx context.Context, entityType models.UtilizationEntityType, entityID string, days int) ([]*models.ChartPoint, error)
}

// SearchStore provides the global cross-entity search behind
// GET /api/v1/search.
type SearchStore interface {
	// GlobalSearch matches query as a substring across the entity types in
	// entityTypes (all known types if empty), returning up to limit hits
	// ranked by relevance.
	GlobalSearch(ctx context.Context, query string, entityTypes []string, limit int) ([]*models.SearchResult, error)
}

// AllocationHelperStore previews allocation decisions without committing
// them, for UI affordances ("this pool's next IP would be X").
type AllocationHelperStore interface {
	// NextAvailableIP previews the next candidate IP the LeaseStore
	// allocation algorithm (amendment A6) would offer in poolID, without
	// claiming it. Returns models.ErrNoLeases if the pool is exhausted.
	NextAvailableIP(ctx context.Context, poolID string) (net.IP, error)

	// NextAvailableSubnet previews the next unused /prefixLen child CIDR
	// beneath parentPrefixID, for carving new subnets in the IPAM tree.
	NextAvailableSubnet(ctx context.Context, parentPrefixID string, prefixLen int) (*models.IPNet, error)
}

// ConfigChangeStore implements the config-change propagation primitive from
// docs/CONTRACTS.md Part 7: "Propagation: LISTEN/NOTIFY with fallback
// polling (5 seconds)." The HA config watcher combines this with its own
// polling timer per amendment's ha.config_check_interval_ms.
type ConfigChangeStore interface {
	// NotifyConfigChange sends a NOTIFY on channel (e.g. "gdhcp_config") so
	// LISTENing secondaries can react without waiting for their next poll.
	NotifyConfigChange(ctx context.Context, channel string) error

	// ListenConfigChanges opens a dedicated LISTEN connection on channel.
	// The returned channel delivers each NOTIFY payload; closeFn releases
	// the underlying connection and must be called when the caller is done
	// listening (e.g. on shutdown or ctx cancellation).
	ListenConfigChanges(ctx context.Context, channel string) (notifications <-chan string, closeFn func() error, err error)
}

// ---------------------------------------------------------------------------
// Store — the aggregate interface the API, DHCP engine, and HA cluster
// manager depend on. The DHCP engine and HA units should prefer depending
// on the narrower interfaces above (or their own consumer-side interfaces,
// per amendment A10) rather than the full Store where practical, but Store
// is what server/app.go wires a single concrete implementation into.
// ---------------------------------------------------------------------------

type Store interface {
	LeaseStore
	ConfigStore
	IPAMStore
	IPAMAdminStore
	StatsRecorder
	UserStore
	ClusterStore
	AuditStore

	VRFStore
	VLANStore
	ScopeStore
	PoolStore
	ReservationStore
	LeaseQueryStore
	LeaseAdminStore
	OptionCatalogStore
	OptionValueStore
	TemplateStore
	TagStore
	DashboardStore
	SearchStore
	AllocationHelperStore
	ConfigChangeStore
}

// NewPostgres constructs a Store backed by PostgreSQL. This is a forward
// declaration only — the Store unit implements it in store/postgres.go and
// friends (docs/FILETREE.md); Foundation does not implement store code.
//
//	func NewPostgres(pool *pgxpool.Pool) Store
