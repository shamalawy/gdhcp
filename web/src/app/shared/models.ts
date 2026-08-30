// TypeScript interfaces for every gdhcp API entity.
//
// Field names are exact snake_case per docs/api/endpoints.md and the model
// struct tags in docs/CONTRACTS.md Part 1, as required by AMENDMENTS.md A1
// (which voids the camelCase section of CONTRACTS.md Part 2 — snake_case
// wins everywhere).
//
// Go `net.IP` / `net.IPNet` / `net.HardwareAddr` fields marshal as plain
// strings ("10.1.0.101", "10.1.0.0/24", "aa:bb:cc:dd:ee:ff"); `time.Time`
// fields marshal as RFC3339 strings. Nullable Go pointer fields are typed
// `T | null` here to match `encoding/json`'s behavior for nil pointers.

// ---------------------------------------------------------------------------
// Envelope / pagination / errors
// ---------------------------------------------------------------------------

/** Standard paginated list envelope returned by every `GET /{entity}` list endpoint. */
export interface Paginated<T> {
  items: T[];
  total: number;
  limit: number;
  offset: number;
}

/** CONTRACTS.md Part 6: canonical error codes. */
export type ApiErrorCode =
  | 'INVALID_INPUT'
  | 'UNAUTHORIZED'
  | 'FORBIDDEN'
  | 'NOT_FOUND'
  | 'CONFLICT'
  | 'INTERNAL_ERROR'
  | 'SERVICE_UNAVAILABLE';

export interface ApiErrorDetail {
  field?: string;
  issue?: string;
  [key: string]: unknown;
}

export interface ApiErrorEnvelope {
  error: {
    code: ApiErrorCode | string;
    message: string;
    details?: ApiErrorDetail;
    trace_id: string;
  };
}

/** Common list query parameters accepted by every list endpoint. */
export interface ListParams {
  limit?: number;
  offset?: number;
  cursor?: string;
  sort?: string;
  search?: string;
  [key: string]: string | number | boolean | undefined;
}

// ---------------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------------

export interface LoginRequest {
  username: string;
  password: string;
}

export interface LoginResponse {
  token: string;
  expires_at: string;
}

export interface RefreshRequest {
  refresh_token: string;
}

export type RefreshResponse = LoginResponse;

export interface MessageResponse {
  message: string;
}

// ---------------------------------------------------------------------------
// Users & API tokens
// ---------------------------------------------------------------------------

export type UserRole = 'admin' | 'operator' | 'viewer';

export interface User {
  id: number;
  username: string;
  email: string;
  role: UserRole;
  description: string | null;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateUserRequest {
  username: string;
  email: string;
  password: string;
  description?: string | null;
  role?: UserRole;
}

export interface UpdateUserRequest {
  username?: string;
  email?: string;
  description?: string | null;
  is_active?: boolean;
  role?: UserRole;
}

/** Metadata for an existing API token; the raw token is never returned again after creation. */
export interface APIToken {
  id: number;
  user_id: number;
  description: string | null;
  last_used_at: string | null;
  expires_at: string | null;
  created_at: string;
}

export interface CreateAPITokenRequest {
  description?: string | null;
  expires_at?: string | null;
}

/** Per A14: raw tokens are `gdhcp_<random>`, shown exactly once at creation. */
export interface CreateAPITokenResponse extends APIToken {
  token: string;
}

// ---------------------------------------------------------------------------
// VRF
// ---------------------------------------------------------------------------

export interface VRF {
  id: string;
  name: string;
  route_distinguisher: string | null;
  description: string | null;
  is_default: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateVRFRequest {
  name: string;
  description?: string | null;
  route_distinguisher?: string | null;
}

export type UpdateVRFRequest = CreateVRFRequest;

// ---------------------------------------------------------------------------
// IPAM prefixes
// ---------------------------------------------------------------------------

export type PrefixType = 'aggregate' | 'container' | 'subnet';

export interface IPAMPrefix {
  id: string;
  vrf_id: string;
  prefix: string; // CIDR, e.g. "10.1.0.0/24"
  parent_id: string | null;
  prefix_type: PrefixType;
  name: string | null;
  description: string | null;
  /** DHCP lease usage, cached from stats. Null wherever no DHCP scope
   * exists on this prefix or beneath it — use PrefixUtilization.space_pct
   * for a number that is always defined. */
  utilization_percentage: number | null;
  vlan_id: string | null;
  /** Read-only joins from the assigned VLAN, for rendering a chip without a
   * second request. Absent when the prefix has no VLAN. */
  vlan_vid?: number;
  vlan_name?: string;
  created_at: string;
  updated_at: string;
}

/** GET /prefixes/{id}/utilization. Two views, because aggregates and
 * containers have no hosts of their own: the DHCP view rolls up every scope
 * anywhere beneath the prefix, the space view measures how much of the
 * prefix's own CIDR its direct children cover. */
export interface PrefixUtilization {
  prefix_id: string;
  total: number;
  active: number;
  reserved: number;
  available: number;
  pct: number;
  scope_count: number;
  space_total: number;
  space_allocated: number;
  space_pct: number;
  child_prefix_count: number;
}

export interface CreatePrefixRequest {
  vrf_id: string;
  cidr: string;
  type: PrefixType;
  name?: string | null;
  description?: string | null;
  vlan_id?: string | null;
}

export interface UpdatePrefixRequest {
  name?: string | null;
  description?: string | null;
  type?: PrefixType;
  vlan_id?: string | null;
}

// ---------------------------------------------------------------------------
// VLANs (layer-2 segments; migration 0004)
// ---------------------------------------------------------------------------

export type VLANStatus = 'active' | 'reserved' | 'deprecated';

export interface VLANGroup {
  id: string;
  name: string;
  description: string | null;
  vrf_id: string | null;
  min_vid: number;
  max_vid: number;
  created_at: string;
  updated_at: string;
}

export interface VLAN {
  id: string;
  group_id: string | null;
  vid: number;
  name: string;
  description: string | null;
  status: VLANStatus;
  /** Read-only joins filled in by the list/detail endpoints. */
  group_name?: string;
  prefix_count: number;
  created_at: string;
  updated_at: string;
}

export interface CreateVLANRequest {
  vid: number;
  name: string;
  group_id?: string | null;
  description?: string | null;
  status?: VLANStatus;
}

export type UpdateVLANRequest = CreateVLANRequest;

export interface VLANGroupRequest {
  name: string;
  description?: string | null;
  vrf_id?: string | null;
  min_vid?: number;
  max_vid?: number;
}

// ---------------------------------------------------------------------------
// IPAM tree (GET /ipam/tree) & global search (GET /search)
// ---------------------------------------------------------------------------

export type IpamTreeNodeType = 'vrf' | 'prefix' | 'scope' | 'pool';
export type EntityStatus = 'ok' | 'warn' | 'error' | 'unknown';

/**
 * One node of the lazily-expandable IPAM tree returned by
 * `GET /api/v1/ipam/tree`, consumed by shared/ipam-tree/ipam-tree.component.ts.
 *
 * Matches server/internal/models/ipam.go's `IPAMTreeNode` exactly (verified
 * against a live response): every node wraps an `IPAMPrefix` — this endpoint
 * has no separate 'scope'/'pool' node shape, so a prefix a scope was carved
 * from is signaled by `has_scope`/`scope_id` rather than a different `type`.
 * `children` is populated only when the requested `depth` covers more than
 * one level; at `depth=1` (how the tree UI always calls it, fetching one
 * level at a time) it's omitted and `children_more` says whether there's
 * more to lazily fetch under `child_count`.
 */
export interface IpamTreeNode {
  prefix: IPAMPrefix;
  child_count: number;
  has_scope: boolean;
  scope_id?: string;
  /** How much of this node's CIDR its direct children cover. Always present,
   * unlike prefix.utilization_percentage, so every node can render a bar. */
  space_pct: number;
  children?: IpamTreeNode[];
  children_more: boolean;
}

// No 'user' or 'server': GET /search never returns either (see
// postgres_admin.go's globalSearchSQL) — the user directory stays behind
// the admin-only /users endpoints instead of this viewer-accessible one.
export type SearchEntityType =
  | 'vrf'
  | 'prefix'
  | 'vlan'
  | 'vlan_group'
  | 'scope'
  | 'pool'
  | 'reservation'
  | 'lease'
  | 'template'
  | 'option'
  | 'cluster';

export interface SearchResult {
  type: SearchEntityType;
  entity_id: string;
  title: string;
  subtitle?: string;
  description?: string;
  url: string; // frontend route to navigate to, e.g. "/ipam/prefixes/<id>"
}

// ---------------------------------------------------------------------------
// DHCP scopes & pools
// ---------------------------------------------------------------------------

export interface DHCPScope {
  id: string;
  vrf_id: string;
  cluster_id: string;
  ipam_prefix_id: string;
  name: string;
  description: string | null;
  network: string; // CIDR
  is_enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateScopeRequest {
  vrf_id: string;
  cluster_id: string;
  ipam_prefix_id: string;
  name: string;
  description?: string | null;
}

export interface UpdateScopeRequest {
  name?: string;
  description?: string | null;
  is_enabled?: boolean;
}

export interface UtilizationSummary {
  total: number;
  active: number;
  reserved: number;
  pct: number;
}

export interface DHCPPool {
  id: string;
  scope_id: string;
  name: string;
  description: string | null;
  range_start: string; // IP
  range_end: string; // IP
  is_enabled: boolean;
  priority: number;
  created_at: string;
  updated_at: string;
}

export interface CreatePoolRequest {
  name: string;
  range_start: string;
  range_end: string;
  description?: string | null;
  priority?: number;
}

export interface UpdatePoolRequest {
  name?: string;
  range_start?: string;
  range_end?: string;
  priority?: number;
  is_enabled?: boolean;
  description?: string | null;
}

// ---------------------------------------------------------------------------
// Reservations
// ---------------------------------------------------------------------------

export type BindingType = 'mac' | 'client_id' | 'hostname' | 'circuit_id' | 'remote_id' | 'vendor_class' | 'user_class';

export type ClientKind = 'lease' | 'reservation';

/**
 * How a lease (or an IPAM client row) relates to the reservations in its
 * pool. "Is this address reserved?" and "is this client reserved?" are
 * different questions, and an operator who cannot tell them apart keeps
 * trying to pin a client that is already pinned — which the server rejects
 * on UNIQUE (pool_id, binding_key). Amendment A19.
 */
export type LeaseReservationState =
  /** Nothing reserves this client or this address — pinning is safe. */
  | 'not_pinned'
  /** An enabled reservation binds this client to this exact address. */
  | 'pinned'
  /** The same binding exists but is disabled, so the engine ignores it: it
   *  neither holds the address nor blocks anyone else from taking it. */
  | 'pinned_disabled'
  /** This client is pinned to a *different* address. A second reservation
   *  would be rejected; the existing one has to be moved. */
  | 'pinned_elsewhere'
  /** This address is reserved for a different client, which will claim it
   *  once the current holder's lease lapses. */
  | 'address_reserved';

/** The one reservation that concerns a lease — matching its client, or
 *  failing that its address. Enough to explain the state and to act on it. */
export interface LeaseReservation {
  id: string;
  reserved_ip: string;
  binding_key: string;
  binding_type: BindingType;
  is_enabled: boolean;
  /** `binding_key` is one of this client's own candidate keys. */
  matches_client: boolean;
  /** `reserved_ip` is this row's address. */
  matches_address: boolean;
}

export interface DHCPReservation {
  id: string;
  pool_id: string;
  hostname: string | null;
  description: string | null;
  reserved_ip: string;
  binding_key: string; // e.g. "mac:aa:bb:cc:dd:ee:ff"
  binding_type: BindingType;
  is_enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateReservationRequest {
  binding_key: string;
  binding_type: BindingType;
  reserved_ip: string;
  hostname?: string | null;
  description?: string | null;
}

export interface UpdateReservationRequest {
  hostname?: string | null;
  description?: string | null;
  is_enabled?: boolean;
  binding_key?: string;
  binding_type?: BindingType;
  reserved_ip?: string;
}

/** Request for creating a reservation with raw binding values.
 * The client sends raw values (e.g. MAC address without prefix); the server
 * canonicalises them into the canonical "type:value" form. */
export interface CreateReservationRawRequest {
  binding_type: BindingType;
  binding_key: string; // Raw value, e.g. "aa:bb:cc:dd:ee:ff" for MAC
  reserved_ip: string;
  hostname?: string | null;
  description?: string | null;
}

// ---------------------------------------------------------------------------
// DHCP clients (leases + reservations, unified view)
// ---------------------------------------------------------------------------

/** DHCP client representing a lease or reservation under a prefix.
 * Returned by GET /api/v1/prefixes/{prefix_id}/clients — a union row combining
 * lease and reservation attributes (some fields nullable depending on kind).
 * ClientID (if present) is base64-encoded from the raw option 61 bytes. */
export interface DHCPClient {
  id: string;
  kind: ClientKind;
  ip_address: string;
  pool_id: string;
  pool_name: string;
  scope_id: string;
  scope_name: string;
  prefix_id: string;
  /** A lease's client_mac, or the address a MAC-bound reservation names. */
  client_mac: string | null;
  /** Base64-encoded option 61 (DHCP Client Identifier), if captured — Go
   * marshals []byte as base64, so decode before showing it to an operator.
   * Omitted for reservations and for leases without this identifier. */
  client_id?: string | null;
  client_hostname: string | null;

  /** Lease-only: binding state ("active", "expired", …). Null for reservations. */
  binding_state: LeaseBindingState | null;
  /** Lease-only. Null for reservations. */
  lease_start: string | null;
  /** Lease-only. Null for reservations. */
  lease_end: string | null;

  /** Reservation-only: canonical key ("type:value"). Null for leases. */
  binding_key: string | null;
  /** Reservation-only. Null for leases. */
  binding_type: BindingType | null;
  /** Reservation-only. Null for leases. */
  is_enabled: boolean | null;

  /** A19: the same pin state the DHCP leases table renders, so both tables
   *  read one field instead of each inferring it differently. A lease row
   *  gets the reservation that concerns it; a reservation row describes
   *  itself and is always pinned (or pinned_disabled). */
  reservation_state: LeaseReservationState;
  /** The reservation behind `reservation_state`, or null when not_pinned. */
  reservation: LeaseReservation | null;

  created_at: string;
  updated_at: string;
}

// ---------------------------------------------------------------------------
// Leases
// ---------------------------------------------------------------------------

export type LeaseBindingState = 'active' | 'expired' | 'released' | 'abandoned';

export interface DHCPLease {
  id: string;
  pool_id: string;
  ip_address: string;
  client_mac: string | null;
  /** Base64-encoded option 61 (DHCP Client Identifier), if captured from the
   * client's DHCP DISCOVER/REQUEST. Null if the client did not send this identifier. */
  client_id: string | null;
  client_hostname: string | null;
  binding_state: LeaseBindingState;
  /** A5: FK has ON DELETE SET NULL, so this is nullable despite the CONTRACTS.md draft. */
  binding_server_id: string | null;
  lease_start: string;
  lease_end: string;
  renewal_time: string | null;
  rebinding_time: string | null;
  created_at: string;
  updated_at: string;

  /** A19: derived on read from the pool's reservations, never stored. */
  reservation_state: LeaseReservationState;
  /** The reservation behind `reservation_state`, or null when not_pinned. */
  reservation: LeaseReservation | null;
}

/** The operator actions POST /leases/bulk accepts, and that each lease
 * exposes individually at /leases/{id}/<action>. */
export type LeaseAction = 'release' | 'abandon' | 'renew' | 'delete';

export interface LeaseBulkRequest {
  action: LeaseAction;
  lease_ids: string[];
  /** Renew only; 0/omitted reuses each lease's own original duration. */
  duration_seconds?: number;
}

export interface LeaseBulkResult {
  lease_id: string;
  ok: boolean;
  error?: string;
}

/** A bulk action reports per-lease outcomes instead of failing as a whole,
 * so `failed > 0` alongside `succeeded > 0` is a normal partial result. */
export interface LeaseBulkResponse {
  action: LeaseAction;
  succeeded: number;
  failed: number;
  results: LeaseBulkResult[];
}

// ---------------------------------------------------------------------------
// DHCP options (catalog + values)
// ---------------------------------------------------------------------------

export type OptionValueType =
  | 'string'
  | 'uint8'
  | 'uint16'
  | 'uint32'
  | 'ip'
  | 'ip_list'
  | 'bytes'
  | 'boolean'
  | 'fqdn' // added to the catalog CHECK constraint per A5.4
  | 'route_list'; // classless static routes, option 121, per A5.4

export type OptionScopeLevel = 'global' | 'template' | 'scope' | 'pool' | 'reservation';

/** A row of the IANA DHCPv4 option catalog (seeded by migration 0002 per A5.4). */
export interface OptionCatalogEntry {
  option_code: number;
  name: string;
  value_type: OptionValueType;
  description: string;
  iana_reference: string | null;
}

/** A configured option value at some level of the inheritance hierarchy. */
export interface DHCPOption {
  id: string;
  option_code: number;
  scope_level: OptionScopeLevel;
  scope_entity_id: string;
  value: string;
  is_enabled: boolean;
  created_at: string;
  updated_at: string;
}

/** Response/request shape for the `/scopes/{id}/options`, `/pools/{id}/options`,
 * `/templates/{id}/options` endpoints, which key on `option_code` rather than
 * the catalog-wide `/options` CRUD (custom options). */
export interface OptionValue {
  id: string;
  option_code: number;
  scope_level: OptionScopeLevel;
  scope_entity_id: string;
  value: string;
  is_enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface SetOptionValueRequest {
  option_code: number;
  value: string;
}

export interface UpdateOptionValueRequest {
  value?: string;
  is_enabled?: boolean;
}

export interface CreateCustomOptionRequest {
  name: string;
  description: string;
  code: number;
  value_type: OptionValueType;
}

// ---------------------------------------------------------------------------
// Templates
// ---------------------------------------------------------------------------

export interface DHCPTemplate {
  id: string;
  name: string;
  description: string | null;
  is_enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateTemplateRequest {
  name: string;
  description?: string | null;
}

export interface UpdateTemplateRequest {
  name?: string;
  description?: string | null;
  is_enabled?: boolean;
}

export interface TemplateScopeAssoc {
  id: string;
  template_id: string;
  scope_id: string;
  priority: number;
  created_at: string;
}

// ---------------------------------------------------------------------------
// Tags
// ---------------------------------------------------------------------------

export type TaggableEntityType = 'vrf' | 'prefix' | 'vlan' | 'scope' | 'pool' | 'reservation' | 'server' | 'cluster';

export interface Tag {
  id: string;
  name: string;
  color: string | null; // "#RRGGBB"
  description: string | null;
  created_at: string;
}

export interface CreateTagRequest {
  name: string;
  description?: string | null;
  color?: string | null;
}

export interface EntityTag {
  id: string;
  tag_id: string;
  entity_type: TaggableEntityType;
  entity_id: string;
  created_at: string;
}

export interface AddEntityTagRequest {
  entity_type: TaggableEntityType;
  entity_id: string;
}

// ---------------------------------------------------------------------------
// Clusters & HA
// ---------------------------------------------------------------------------

export interface Cluster {
  id: string;
  name: string;
  description: string | null;
  vip_address: string | null;
  vip_interface: string | null;
  vip_virtual_mac: string | null;
  heartbeat_interval_ms: number;
  heartbeat_ttl_ms: number;
  election_interval_ms: number;
  created_at: string;
  updated_at: string;
}

export interface CreateClusterRequest {
  name: string;
  description?: string | null;
  vip_address?: string | null;
  vip_interface?: string | null;
  heartbeat_interval_ms?: number;
  heartbeat_ttl_ms?: number;
  election_interval_ms?: number;
}

export interface UpdateClusterRequest {
  name?: string;
  description?: string | null;
  heartbeat_interval_ms?: number;
  heartbeat_ttl_ms?: number;
  election_interval_ms?: number;
}

export interface ClusterServer {
  id: string;
  cluster_id: string;
  node_name: string;
  node_id: number;
  api_address: string;
  dhcp_address: string;
  created_at: string;
  updated_at: string;
}

export interface RegisterServerRequest {
  node_name: string;
  node_id: number;
  api_address: string;
  dhcp_address: string;
}

export type ClusterRole = 'PRIMARY' | 'SECONDARY';

export interface Heartbeat {
  id: string;
  cluster_id: string;
  server_id: string;
  heartbeat_time: string;
  role: ClusterRole;
  vip_assigned: boolean;
  lease_version: number;
  last_config_version: number;
  created_at: string;
  updated_at: string;
}

export interface ClusterHealthServer {
  server_id: string;
  node_name: string;
  node_id: number;
  role: ClusterRole;
  vip_assigned: boolean;
  heartbeat_time: string;
  heartbeat_age_ms: number;
  is_healthy: boolean;
  api_address: string;
}

// 'HEALTHY' | 'DEGRADED' | 'DOWN' matches models.ClusterState
// (server/internal/models/cluster.go) exactly — that's the full set the API
// ever sends. 'UNKNOWN' is a frontend-only sentinel for "not loaded /
// failed to load yet", never a value the API returns.
export type ClusterState = 'HEALTHY' | 'DEGRADED' | 'DOWN' | 'UNKNOWN';

export interface ClusterHealth {
  cluster_id: string;
  local_server_id: string;
  local_role: ClusterRole;
  timestamp: string;
  servers: ClusterHealthServer[];
  cluster_state: ClusterState;
  notes: string;
}

export interface FailoverRequest {
  target_server_id: string;
}

export interface FailoverResponse {
  message: string;
  new_primary: string;
}

export interface BootstrapClusterRequest {
  server_ids: string[];
}

// ---------------------------------------------------------------------------
// Dashboard & stats
// ---------------------------------------------------------------------------

export interface DHCPStats {
  scope_id: string;
  discover_count: number;
  offer_count: number;
  request_count: number;
  ack_count: number;
  nak_count: number;
  decline_count: number;
  release_count: number;
  no_leases_count: number;
}

export interface DashboardStats {
  timestamp: string;
  total_vrfs: number;
  total_scopes: number;
  total_pools: number;
  total_addresses: number;
  total_active_leases: number;
  total_reserved_addresses: number;
  total_available_addresses: number;
  overall_utilization_percentage: number;
  dhcp_stats_window?: DHCPStats | null;
  cluster_health?: ClusterHealth[] | null;
  top_utilized_scopes?: ScopeUtilizationSummary[] | null;
}

export interface ScopeUtilizationSummary {
  scope_id: string;
  scope_name: string;
  utilization_percentage: number;
  active: number;
  total: number;
}

export interface ChartPoint {
  timestamp: string;
  utilization_percentage: number;
  used_addresses: number;
  total_addresses: number;
}

export type HealthStatus = 'ok' | 'degraded' | 'down';

export interface HealthResponse {
  status: HealthStatus;
  db: boolean;
  ha: boolean;
  version: string;
}

// ---------------------------------------------------------------------------
// Audit & admin
// ---------------------------------------------------------------------------

export type AuditAction =
  | 'create'
  | 'update'
  | 'delete'
  | 'lease_assign'
  | 'lease_release'
  | 'failover'
  | 'login'
  | 'logout';

export interface AuditLog {
  id: number;
  user_id: number | null;
  action: AuditAction | string;
  entity_type: string;
  entity_id: string;
  entity_name: string | null;
  changes: Record<string, { old: unknown; new: unknown }> | null;
  ip_address: string | null;
  user_agent: string | null;
  timestamp: string;
  description: string | null;
}

export interface RunningConfig {
  api_addr: string;
  ha_enabled: boolean;
  [key: string]: unknown;
}

export interface ConfigReloadRequest {
  source: string;
}

// ---------------------------------------------------------------------------
// SSE events (GET /events) — A12
// ---------------------------------------------------------------------------

export interface SSEStatsEvent {
  type: 'stats';
  data: DashboardStats;
}

// ScopeChurn is one scope's lease-churn snapshot, matching
// server/internal/api/dashboard/events.go's scopeChurn exactly.
export interface ScopeChurn {
  scope_id: string;
  active: number;
  total: number;
  pct: number;
}

export interface SSELeaseEvent {
  type: 'leases';
  // The `leases` SSE event carries lease churn *per scope* (amendment
  // A12), not a single lease's own fields — it's an array of ScopeChurn,
  // one entry per scope that changed since the last tick.
  data: ScopeChurn[];
}

export interface SSEClusterEvent {
  type: 'cluster';
  data: ClusterHealth;
}

export type SSEEvent = SSEStatsEvent | SSELeaseEvent | SSEClusterEvent;
