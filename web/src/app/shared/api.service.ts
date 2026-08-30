import { HttpClient, HttpParams } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { Observable, map } from 'rxjs';

import {
  APIToken,
  AuditLog,
  BootstrapClusterRequest,
  ChartPoint,
  Cluster,
  ClusterHealth,
  ClusterServer,
  ConfigReloadRequest,
  CreateAPITokenRequest,
  CreateAPITokenResponse,
  CreateClusterRequest,
  CreateCustomOptionRequest,
  CreatePoolRequest,
  CreatePrefixRequest,
  CreateReservationRequest,
  CreateReservationRawRequest,
  CreateScopeRequest,
  CreateTagRequest,
  CreateTemplateRequest,
  CreateUserRequest,
  CreateVRFRequest,
  DHCPClient,
  DHCPLease,
  LeaseBulkRequest,
  LeaseBulkResponse,
  DHCPOption,
  DHCPPool,
  DHCPReservation,
  DHCPScope,
  DHCPStats,
  DHCPTemplate,
  DashboardStats,
  EntityTag,
  AddEntityTagRequest,
  FailoverRequest,
  FailoverResponse,
  Heartbeat,
  HealthResponse,
  IPAMPrefix,
  IpamTreeNode,
  PrefixUtilization,
  VLAN,
  VLANGroup,
  VLANGroupRequest,
  CreateVLANRequest,
  UpdateVLANRequest,
  ListParams,
  LoginRequest,
  LoginResponse,
  MessageResponse,
  OptionCatalogEntry,
  OptionValue,
  Paginated,
  RefreshRequest,
  RefreshResponse,
  RegisterServerRequest,
  RunningConfig,
  SearchResult,
  SetOptionValueRequest,
  Tag,
  TemplateScopeAssoc,
  UpdateClusterRequest,
  UpdatePoolRequest,
  UpdatePrefixRequest,
  UpdateReservationRequest,
  UpdateScopeRequest,
  UpdateUserRequest,
  UpdateVRFRequest,
  UpdateOptionValueRequest,
  User,
  UtilizationSummary,
  VRF,
} from './models';

const BASE = '/api/v1';

function toHttpParams(params?: ListParams): HttpParams {
  let httpParams = new HttpParams();
  if (!params) return httpParams;
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null) continue;
    httpParams = httpParams.set(key, String(value));
  }
  return httpParams;
}

/**
 * Typed REST client for every endpoint in docs/api/endpoints.md.
 *
 * Generic helpers (`list`, `get`, `create`, `update`, `patch`, `remove`)
 * cover the uniform CRUD shape; resource-specific methods below wrap the
 * endpoints that don't fit that shape (nested collections, actions, etc.).
 */
@Injectable({ providedIn: 'root' })
export class ApiService {
  constructor(private readonly http: HttpClient) {}

  // -- Generic CRUD -----------------------------------------------------

  list<T>(path: string, params?: ListParams): Observable<Paginated<T>> {
    return this.http.get<Paginated<T>>(`${BASE}${path}`, { params: toHttpParams(params) });
  }

  /** For endpoints that return a bare `[T]` with no pagination envelope
   * (e.g. `/options/catalog`, `/clusters/{id}/heartbeats`). */
  listArray<T>(path: string, params?: ListParams): Observable<T[]> {
    return this.http.get<T[]>(`${BASE}${path}`, { params: toHttpParams(params) });
  }

  get<T>(path: string): Observable<T> {
    return this.http.get<T>(`${BASE}${path}`);
  }

  create<T>(path: string, body: unknown): Observable<T> {
    return this.http.post<T>(`${BASE}${path}`, body);
  }

  update<T>(path: string, body: unknown): Observable<T> {
    return this.http.put<T>(`${BASE}${path}`, body);
  }

  patch<T>(path: string, body: unknown): Observable<T> {
    return this.http.patch<T>(`${BASE}${path}`, body);
  }

  remove(path: string): Observable<MessageResponse> {
    return this.http.delete<MessageResponse>(`${BASE}${path}`);
  }

  // -- Auth ---------------------------------------------------------------

  login(body: LoginRequest): Observable<LoginResponse> {
    return this.create<LoginResponse>('/auth/login', body);
  }

  logout(): Observable<MessageResponse> {
    return this.create<MessageResponse>('/auth/logout', {});
  }

  refresh(body: RefreshRequest): Observable<RefreshResponse> {
    return this.create<RefreshResponse>('/auth/refresh', body);
  }

  // -- Users & API tokens ---------------------------------------------------

  listUsers(params?: ListParams): Observable<Paginated<User>> {
    return this.list<User>('/users', params);
  }

  getUser(userId: number): Observable<User> {
    return this.get<User>(`/users/${userId}`);
  }

  createUser(body: CreateUserRequest): Observable<User> {
    return this.create<User>('/users', body);
  }

  updateUser(userId: number, body: UpdateUserRequest): Observable<User> {
    return this.update<User>(`/users/${userId}`, body);
  }

  deleteUser(userId: number): Observable<MessageResponse> {
    return this.remove(`/users/${userId}`);
  }

  listUserTokens(userId: number, params?: ListParams): Observable<Paginated<APIToken>> {
    return this.list<APIToken>(`/users/${userId}/tokens`, params);
  }

  createUserToken(userId: number, body: CreateAPITokenRequest): Observable<CreateAPITokenResponse> {
    return this.create<CreateAPITokenResponse>(`/users/${userId}/tokens`, body);
  }

  revokeUserToken(userId: number, tokenId: number): Observable<MessageResponse> {
    return this.remove(`/users/${userId}/tokens/${tokenId}`);
  }

  // -- VRFs -----------------------------------------------------------------

  listVRFs(params?: ListParams): Observable<Paginated<VRF>> {
    return this.list<VRF>('/vrfs', params);
  }

  getVRF(vrfId: string): Observable<VRF> {
    return this.get<VRF>(`/vrfs/${vrfId}`);
  }

  createVRF(body: CreateVRFRequest): Observable<VRF> {
    return this.create<VRF>('/vrfs', body);
  }

  updateVRF(vrfId: string, body: UpdateVRFRequest): Observable<VRF> {
    return this.update<VRF>(`/vrfs/${vrfId}`, body);
  }

  deleteVRF(vrfId: string): Observable<MessageResponse> {
    return this.remove(`/vrfs/${vrfId}`);
  }

  // -- IPAM prefixes, tree, search --------------------------------------------

  listPrefixes(
    params?: ListParams & { vrf_id?: string; type?: string },
  ): Observable<Paginated<IPAMPrefix>> {
    return this.list<IPAMPrefix>('/prefixes', params);
  }

  getPrefix(prefixId: string): Observable<IPAMPrefix> {
    return this.get<IPAMPrefix>(`/prefixes/${prefixId}`);
  }

  createPrefix(body: CreatePrefixRequest): Observable<IPAMPrefix> {
    return this.create<IPAMPrefix>('/prefixes', body);
  }

  updatePrefix(prefixId: string, body: UpdatePrefixRequest): Observable<IPAMPrefix> {
    return this.update<IPAMPrefix>(`/prefixes/${prefixId}`, body);
  }

  deletePrefix(prefixId: string): Observable<MessageResponse> {
    return this.remove(`/prefixes/${prefixId}`);
  }

  ipamTree(params?: { vrf_id?: string; depth?: number; parent_id?: string }): Observable<IpamTreeNode[]> {
    return this.http.get<IpamTreeNode[]>(`${BASE}/ipam/tree`, { params: toHttpParams(params) });
  }

  /** Root-first chain from the VRF's top-level prefix down to this prefix's
   * immediate parent — breadcrumbs, and the tree's expand-to-reveal. */
  prefixAncestors(prefixId: string): Observable<IPAMPrefix[]> {
    return this.list<IPAMPrefix>(`/prefixes/${prefixId}/ancestors`).pipe(map((page) => page.items));
  }

  prefixUtilization(prefixId: string): Observable<PrefixUtilization> {
    return this.get<PrefixUtilization>(`/prefixes/${prefixId}/utilization`);
  }

  /** List DHCP clients (leases and reservations) under a prefix and all its
   * descendants. Supports filtering by kind and state. */
  listPrefixClients(
    prefixId: string,
    params?: ListParams & { kind?: string; state?: string },
  ): Observable<Paginated<DHCPClient>> {
    return this.list<DHCPClient>(`/prefixes/${prefixId}/clients`, params);
  }

  // -- VLANs ------------------------------------------------------------------

  listVLANs(params?: ListParams & { group_id?: string; status?: string }): Observable<Paginated<VLAN>> {
    return this.list<VLAN>('/vlans', params);
  }

  getVLAN(vlanId: string): Observable<VLAN> {
    return this.get<VLAN>(`/vlans/${vlanId}`);
  }

  createVLAN(body: CreateVLANRequest): Observable<VLAN> {
    return this.create<VLAN>('/vlans', body);
  }

  updateVLAN(vlanId: string, body: UpdateVLANRequest): Observable<VLAN> {
    return this.update<VLAN>(`/vlans/${vlanId}`, body);
  }

  deleteVLAN(vlanId: string): Observable<MessageResponse> {
    return this.remove(`/vlans/${vlanId}`);
  }

  listVLANPrefixes(vlanId: string): Observable<IPAMPrefix[]> {
    return this.list<IPAMPrefix>(`/vlans/${vlanId}/prefixes`).pipe(map((page) => page.items));
  }

  listVLANGroups(params?: ListParams): Observable<Paginated<VLANGroup>> {
    return this.list<VLANGroup>('/vlan-groups', params);
  }

  createVLANGroup(body: VLANGroupRequest): Observable<VLANGroup> {
    return this.create<VLANGroup>('/vlan-groups', body);
  }

  updateVLANGroup(groupId: string, body: VLANGroupRequest): Observable<VLANGroup> {
    return this.update<VLANGroup>(`/vlan-groups/${groupId}`, body);
  }

  deleteVLANGroup(groupId: string): Observable<MessageResponse> {
    return this.remove(`/vlan-groups/${groupId}`);
  }

  search(q: string, entityTypes?: string[], limit?: number): Observable<SearchResult[]> {
    const params: ListParams = { q, limit };
    if (entityTypes?.length) params['entity_types'] = entityTypes.join(',');
    return this.http.get<SearchResult[]>(`${BASE}/search`, { params: toHttpParams(params) });
  }

  // -- DHCP scopes ------------------------------------------------------------

  listScopes(
    params?: ListParams & { vrf_id?: string; cluster_id?: string },
  ): Observable<Paginated<DHCPScope>> {
    return this.list<DHCPScope>('/scopes', params);
  }

  getScope(scopeId: string): Observable<DHCPScope> {
    return this.get<DHCPScope>(`/scopes/${scopeId}`);
  }

  createScope(body: CreateScopeRequest): Observable<DHCPScope> {
    return this.create<DHCPScope>('/scopes', body);
  }

  updateScope(scopeId: string, body: UpdateScopeRequest): Observable<DHCPScope> {
    return this.update<DHCPScope>(`/scopes/${scopeId}`, body);
  }

  deleteScope(scopeId: string): Observable<MessageResponse> {
    return this.remove(`/scopes/${scopeId}`);
  }

  scopeUtilization(scopeId: string): Observable<UtilizationSummary> {
    return this.get<UtilizationSummary>(`/scopes/${scopeId}/utilization`);
  }

  scopeStats(scopeId: string, window?: string): Observable<DHCPStats> {
    return this.http.get<DHCPStats>(`${BASE}/scopes/${scopeId}/stats`, {
      params: toHttpParams(window ? { window } : undefined),
    });
  }

  // -- DHCP pools ---------------------------------------------------------

  listPools(scopeId: string, params?: ListParams): Observable<Paginated<DHCPPool>> {
    return this.list<DHCPPool>(`/scopes/${scopeId}/pools`, params);
  }

  getPool(poolId: string): Observable<DHCPPool> {
    return this.get<DHCPPool>(`/pools/${poolId}`);
  }

  createPool(scopeId: string, body: CreatePoolRequest): Observable<DHCPPool> {
    return this.create<DHCPPool>(`/scopes/${scopeId}/pools`, body);
  }

  updatePool(poolId: string, body: UpdatePoolRequest): Observable<DHCPPool> {
    return this.update<DHCPPool>(`/pools/${poolId}`, body);
  }

  deletePool(poolId: string): Observable<MessageResponse> {
    return this.remove(`/pools/${poolId}`);
  }

  poolUtilization(poolId: string): Observable<UtilizationSummary> {
    return this.get<UtilizationSummary>(`/pools/${poolId}/utilization`);
  }

  // -- Reservations ---------------------------------------------------------

  listReservations(poolId: string, params?: ListParams): Observable<Paginated<DHCPReservation>> {
    return this.list<DHCPReservation>(`/pools/${poolId}/reservations`, params);
  }

  getReservation(reservationId: string): Observable<DHCPReservation> {
    return this.get<DHCPReservation>(`/reservations/${reservationId}`);
  }

  createReservation(poolId: string, body: CreateReservationRequest): Observable<DHCPReservation> {
    return this.create<DHCPReservation>(`/pools/${poolId}/reservations`, body);
  }

  updateReservation(reservationId: string, body: UpdateReservationRequest): Observable<DHCPReservation> {
    return this.update<DHCPReservation>(`/reservations/${reservationId}`, body);
  }

  deleteReservation(reservationId: string): Observable<MessageResponse> {
    return this.remove(`/reservations/${reservationId}`);
  }

  // -- Leases ------------------------------------------------------------------

  listLeases(
    params?: ListParams & { scope_id?: string; pool_id?: string; state?: string },
  ): Observable<Paginated<DHCPLease>> {
    return this.list<DHCPLease>('/leases', params);
  }

  getLease(leaseId: string): Observable<DHCPLease> {
    return this.get<DHCPLease>(`/leases/${leaseId}`);
  }

  listPoolLeases(
    poolId: string,
    params?: ListParams & { state?: string },
  ): Observable<Paginated<DHCPLease>> {
    return this.list<DHCPLease>(`/pools/${poolId}/leases`, params);
  }

  getLeaseByIp(ipAddress: string): Observable<DHCPLease> {
    return this.get<DHCPLease>(`/leases/by-ip/${encodeURIComponent(ipAddress)}`);
  }

  getLeasesByMac(macAddress: string): Observable<DHCPLease[]> {
    return this.listArray<DHCPLease>(`/leases/by-mac/${encodeURIComponent(macAddress)}`);
  }

  releaseLease(leaseId: string): Observable<MessageResponse> {
    return this.http.post<MessageResponse>(`${BASE}/leases/${leaseId}/release`, {});
  }

  abandonLease(leaseId: string): Observable<MessageResponse> {
    return this.http.post<MessageResponse>(`${BASE}/leases/${leaseId}/abandon`, {});
  }

  /** `durationSeconds` of 0/undefined reuses the lease's own original duration. */
  renewLease(leaseId: string, durationSeconds?: number): Observable<DHCPLease> {
    return this.http.post<DHCPLease>(`${BASE}/leases/${leaseId}/renew`, {
      duration_seconds: durationSeconds ?? 0,
    });
  }

  deleteLease(leaseId: string): Observable<MessageResponse> {
    return this.remove(`/leases/${leaseId}`);
  }

  /** One action over many leases in a single call. Resolves with a
   * per-lease breakdown even when some of them failed. */
  bulkLeaseAction(body: LeaseBulkRequest): Observable<LeaseBulkResponse> {
    return this.http.post<LeaseBulkResponse>(`${BASE}/leases/bulk`, body);
  }

  // -- DHCP options -----------------------------------------------------------

  listOptions(
    params?: ListParams & { standard?: boolean; custom?: boolean },
  ): Observable<Paginated<DHCPOption>> {
    return this.list<DHCPOption>('/options', params);
  }

  optionCatalog(): Observable<OptionCatalogEntry[]> {
    return this.listArray<OptionCatalogEntry>('/options/catalog');
  }

  getOption(optionId: string): Observable<DHCPOption> {
    return this.get<DHCPOption>(`/options/${optionId}`);
  }

  createCustomOption(body: CreateCustomOptionRequest): Observable<DHCPOption> {
    return this.create<DHCPOption>('/options', body);
  }

  deleteOption(optionId: string): Observable<MessageResponse> {
    return this.remove(`/options/${optionId}`);
  }

  getScopeOptions(scopeId: string): Observable<OptionValue[]> {
    return this.listArray<OptionValue>(`/scopes/${scopeId}/options`);
  }

  setScopeOption(scopeId: string, body: SetOptionValueRequest): Observable<OptionValue> {
    return this.create<OptionValue>(`/scopes/${scopeId}/options`, body);
  }

  updateScopeOption(
    scopeId: string,
    optionId: string,
    body: UpdateOptionValueRequest,
  ): Observable<OptionValue> {
    return this.update<OptionValue>(`/scopes/${scopeId}/options/${optionId}`, body);
  }

  removeScopeOption(scopeId: string, optionId: string): Observable<MessageResponse> {
    return this.remove(`/scopes/${scopeId}/options/${optionId}`);
  }

  getPoolOptions(poolId: string): Observable<OptionValue[]> {
    return this.listArray<OptionValue>(`/pools/${poolId}/options`);
  }

  setPoolOption(poolId: string, body: SetOptionValueRequest): Observable<OptionValue> {
    return this.create<OptionValue>(`/pools/${poolId}/options`, body);
  }

  removePoolOption(poolId: string, optionId: string): Observable<MessageResponse> {
    return this.remove(`/pools/${poolId}/options/${optionId}`);
  }

  // -- Templates ----------------------------------------------------------

  listTemplates(params?: ListParams): Observable<Paginated<DHCPTemplate>> {
    return this.list<DHCPTemplate>('/templates', params);
  }

  getTemplate(templateId: string): Observable<DHCPTemplate> {
    return this.get<DHCPTemplate>(`/templates/${templateId}`);
  }

  createTemplate(body: CreateTemplateRequest): Observable<DHCPTemplate> {
    return this.create<DHCPTemplate>('/templates', body);
  }

  updateTemplate(templateId: string, body: Partial<CreateTemplateRequest & { is_enabled: boolean }>): Observable<DHCPTemplate> {
    return this.update<DHCPTemplate>(`/templates/${templateId}`, body);
  }

  deleteTemplate(templateId: string): Observable<MessageResponse> {
    return this.remove(`/templates/${templateId}`);
  }

  getTemplateOptions(templateId: string): Observable<OptionValue[]> {
    return this.listArray<OptionValue>(`/templates/${templateId}/options`);
  }

  setTemplateOption(templateId: string, body: SetOptionValueRequest): Observable<OptionValue> {
    return this.create<OptionValue>(`/templates/${templateId}/options`, body);
  }

  getTemplateScopes(templateId: string): Observable<TemplateScopeAssoc[]> {
    return this.listArray<TemplateScopeAssoc>(`/templates/${templateId}/scopes`);
  }

  attachTemplateToScope(
    templateId: string,
    scopeId: string,
    priority?: number,
  ): Observable<TemplateScopeAssoc> {
    return this.create<TemplateScopeAssoc>(`/templates/${templateId}/scopes/${scopeId}`, { priority });
  }

  detachTemplateFromScope(templateId: string, scopeId: string): Observable<MessageResponse> {
    return this.remove(`/templates/${templateId}/scopes/${scopeId}`);
  }

  // -- Tags -----------------------------------------------------------------

  listTags(params?: ListParams): Observable<Paginated<Tag>> {
    return this.list<Tag>('/tags', params);
  }

  createTag(body: CreateTagRequest): Observable<Tag> {
    return this.create<Tag>('/tags', body);
  }

  deleteTag(tagId: string): Observable<MessageResponse> {
    return this.remove(`/tags/${tagId}`);
  }

  addTagToEntity(tagId: string, body: AddEntityTagRequest): Observable<EntityTag> {
    return this.create<EntityTag>(`/tags/${tagId}/entities`, body);
  }

  removeTagFromEntity(tagId: string, entityType: string, entityId: string): Observable<MessageResponse> {
    return this.remove(`/tags/${tagId}/entities/${entityType}/${entityId}`);
  }

  // -- Clusters & HA --------------------------------------------------------

  listClusters(params?: ListParams): Observable<Paginated<Cluster>> {
    return this.list<Cluster>('/clusters', params);
  }

  getCluster(clusterId: string): Observable<Cluster> {
    return this.get<Cluster>(`/clusters/${clusterId}`);
  }

  createCluster(body: CreateClusterRequest): Observable<Cluster> {
    return this.create<Cluster>('/clusters', body);
  }

  updateCluster(clusterId: string, body: UpdateClusterRequest): Observable<Cluster> {
    return this.update<Cluster>(`/clusters/${clusterId}`, body);
  }

  deleteCluster(clusterId: string): Observable<MessageResponse> {
    return this.remove(`/clusters/${clusterId}`);
  }

  listClusterServers(clusterId: string): Observable<ClusterServer[]> {
    return this.listArray<ClusterServer>(`/clusters/${clusterId}/servers`);
  }

  registerClusterServer(clusterId: string, body: RegisterServerRequest): Observable<ClusterServer> {
    return this.create<ClusterServer>(`/clusters/${clusterId}/servers`, body);
  }

  deregisterClusterServer(clusterId: string, serverId: string): Observable<MessageResponse> {
    return this.remove(`/clusters/${clusterId}/servers/${serverId}`);
  }

  clusterHealth(clusterId: string): Observable<ClusterHealth> {
    return this.get<ClusterHealth>(`/clusters/${clusterId}/health`);
  }

  clusterHeartbeats(clusterId: string): Observable<Heartbeat[]> {
    return this.listArray<Heartbeat>(`/clusters/${clusterId}/heartbeats`);
  }

  failoverCluster(clusterId: string, body: FailoverRequest): Observable<FailoverResponse> {
    return this.create<FailoverResponse>(`/clusters/${clusterId}/failover`, body);
  }

  bootstrapCluster(clusterId: string, body: BootstrapClusterRequest): Observable<MessageResponse> {
    return this.create<MessageResponse>(`/clusters/${clusterId}/bootstrap`, body);
  }

  // -- Dashboard --------------------------------------------------------------

  dashboardStats(window?: string): Observable<DashboardStats> {
    return this.http.get<DashboardStats>(`${BASE}/dashboard/stats`, {
      params: toHttpParams(window ? { window } : undefined),
    });
  }

  dashboardUtilizationChart(
    entityType: string,
    entityId: string,
    days?: number,
  ): Observable<ChartPoint[]> {
    return this.http.get<ChartPoint[]>(`${BASE}/dashboard/utilization-chart`, {
      params: toHttpParams({ entity_type: entityType, entity_id: entityId, days }),
    });
  }

  dashboardDhcpStats(scopeId?: string): Observable<DHCPStats> {
    return this.http.get<DHCPStats>(`${BASE}/dashboard/dhcp-stats`, {
      params: toHttpParams(scopeId ? { scope_id: scopeId } : undefined),
    });
  }

  health(): Observable<HealthResponse> {
    return this.get<HealthResponse>('/health');
  }

  // -- Audit & admin --------------------------------------------------------

  auditLog(
    params?: ListParams & { filter?: string; entity_type?: string; entity_id?: string },
  ): Observable<Paginated<AuditLog>> {
    return this.list<AuditLog>('/audit/log', params);
  }

  runningConfig(): Observable<RunningConfig> {
    return this.get<RunningConfig>('/config');
  }

  reloadConfig(body: ConfigReloadRequest): Observable<MessageResponse> {
    return this.create<MessageResponse>('/config/reload', body);
  }
}
