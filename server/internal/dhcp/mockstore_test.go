package dhcp

// mockStore is a hand-rolled, in-memory double for store.Store used only by
// this package's own tests (engine_test.go). It exists here — rather than
// importing a shared mock from server/internal/store — because that package
// is owned by a different implementation unit (docs/FILETREE.md's ownership
// matrix) and does not ship a store.Store-satisfying mock; per the
// project's ownership discipline this unit does not create or edit files
// under server/internal/store. mockStore implements the full store.Store
// aggregate interface (engine.New's public signature takes store.Store, per
// the task's binding constructor contract) but only the handful of methods
// the DHCP engine actually calls (see storeDeps in engine.go) carry real
// behavior; everything else is a minimal stub returning zero values so the
// interface is satisfied.
import (
	"context"
	"net"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

type mockStore struct {
	// --- fields exercised by engine_test.go ---
	vrf            *models.VRF
	scopesByVRF    map[string][]*models.DHCPScope
	poolsByScope   map[string][]*models.DHCPPool
	scopeOptions   map[string][]*models.DHCPOption
	poolOptions    map[string][]*models.DHCPOption
	globalOptions  []*models.DHCPOption
	leasesByPoolIP map[string]*models.DHCPLease // key "poolID|ip"

	allocateResult       *models.DHCPLease
	allocateErr          error
	allocateLastIdent    models.ClientIdentity
	allocateLastHostname *string
	allocateLastLeaseDur time.Duration
	renewCalled          bool
	releasedLeaseIDs     []string
	abandonedLeaseIDs    []string

	servers              []*models.ClusterServer
	clusterSizeCallCount int

	discoverCount map[string]int
	offerCount    map[string]int
	requestCount  map[string]int
	ackCount      map[string]int
	nakCount      map[string]int
	declineCount  map[string]int
	releaseCount  map[string]int
	noLeasesCount map[string]int
}

func newMockStore() *mockStore {
	return &mockStore{
		scopesByVRF:    map[string][]*models.DHCPScope{},
		poolsByScope:   map[string][]*models.DHCPPool{},
		scopeOptions:   map[string][]*models.DHCPOption{},
		poolOptions:    map[string][]*models.DHCPOption{},
		leasesByPoolIP: map[string]*models.DHCPLease{},
		discoverCount:  map[string]int{},
		offerCount:     map[string]int{},
		requestCount:   map[string]int{},
		ackCount:       map[string]int{},
		nakCount:       map[string]int{},
		declineCount:   map[string]int{},
		releaseCount:   map[string]int{},
		noLeasesCount:  map[string]int{},
	}
}

var _ store.Store = (*mockStore)(nil)

// ---------------------------------------------------------------------------
// LeaseStore — real behavior for the methods the engine's DORA/allocation
// path calls (AllocateLease, GetLeaseByIP, RenewLease); stubs otherwise.
// ---------------------------------------------------------------------------

func (m *mockStore) AllocateLease(ctx context.Context, poolID string, ident models.ClientIdentity, hostname *string, leaseDur time.Duration) (*models.DHCPLease, error) {
	m.allocateLastIdent = ident
	m.allocateLastHostname = hostname
	m.allocateLastLeaseDur = leaseDur
	if m.allocateErr != nil {
		return nil, m.allocateErr
	}
	return m.allocateResult, nil
}

func (m *mockStore) RenewLease(ctx context.Context, leaseID string, clientMAC string, newExpiry time.Time, hostname *string) (*models.DHCPLease, error) {
	m.renewCalled = true
	for _, l := range m.leasesByPoolIP {
		if l.ID == leaseID {
			l.LeaseEnd = newExpiry
			return l, nil
		}
	}
	return nil, models.ErrLeaseNotFound
}

func (m *mockStore) ReleaseLease(ctx context.Context, leaseID string, clientMAC string) error {
	m.releasedLeaseIDs = append(m.releasedLeaseIDs, leaseID)
	if l, ok := m.leasesByPoolIP[m.poolIPKeyByLeaseID(leaseID)]; ok {
		l.BindingState = models.LeaseStateReleased
	}
	return nil
}

func (m *mockStore) AbandonLease(ctx context.Context, leaseID string, clientMAC string) error {
	m.abandonedLeaseIDs = append(m.abandonedLeaseIDs, leaseID)
	if l, ok := m.leasesByPoolIP[m.poolIPKeyByLeaseID(leaseID)]; ok {
		l.BindingState = models.LeaseStateAbandoned
	}
	return nil
}

// poolIPKeyByLeaseID reverse-maps a lease ID onto its leasesByPoolIP key so
// the release/abandon doubles can mutate the stored row's binding state.
func (m *mockStore) poolIPKeyByLeaseID(leaseID string) string {
	for k, l := range m.leasesByPoolIP {
		if l.ID == leaseID {
			return k
		}
	}
	return ""
}

func (m *mockStore) GetLeaseByIP(ctx context.Context, poolID string, ip net.IP) (*models.DHCPLease, error) {
	if l, ok := m.leasesByPoolIP[poolID+"|"+ip.String()]; ok {
		return l, nil
	}
	return nil, nil
}

func (m *mockStore) GetLeaseByClient(ctx context.Context, ident models.ClientIdentity) (*models.DHCPLease, error) {
	return nil, nil
}

func (m *mockStore) GetLeasesByPool(ctx context.Context, poolID string, state string, offset, limit int) ([]*models.DHCPLease, int64, error) {
	return nil, 0, nil
}

func (m *mockStore) ExpireLeasesOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	return 0, nil
}

func (m *mockStore) PurgeLeasesBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	return 0, nil
}

func (m *mockStore) ReservationForLease(ctx context.Context, poolID string, bindingKey string) (*models.DHCPReservation, error) {
	return nil, nil
}

func (m *mockStore) ReservationsForKeys(ctx context.Context, poolID string, keys []string) (*models.DHCPReservation, error) {
	return nil, nil
}

// ReservationByBindingKey and ReservationByIP back the reservation-conflict
// guards added by amendment A19. The engine never calls them -- they exist on
// the store interface for the API's write path -- so the stubs answer "no such
// reservation" (nil, nil), which is the miss case, not an error.
func (m *mockStore) ReservationByBindingKey(ctx context.Context, poolID, bindingKey string) (*models.DHCPReservation, error) {
	return nil, nil
}

func (m *mockStore) ReservationByIP(ctx context.Context, poolID string, ip net.IP) (*models.DHCPReservation, error) {
	return nil, nil
}

func (m *mockStore) ListClientsUnderPrefix(ctx context.Context, prefixID string, params store.ListParams) (store.ListResult[*models.DHCPClient], error) {
	return store.ListResult[*models.DHCPClient]{}, nil
}

// ---------------------------------------------------------------------------
// ConfigStore — real behavior for VRF/scope/pool/option lookups the engine
// resolves on every packet.
// ---------------------------------------------------------------------------

func (m *mockStore) GetVRFByInterface(ctx context.Context, ifname string) (*models.VRF, error) {
	if m.vrf != nil {
		return m.vrf, nil
	}
	return nil, models.ErrNotFound
}

func (m *mockStore) GetVRFByID(ctx context.Context, vrfID string) (*models.VRF, error) {
	if m.vrf != nil && m.vrf.ID == vrfID {
		return m.vrf, nil
	}
	return nil, models.ErrNotFound
}

func (m *mockStore) GetScopesByVRF(ctx context.Context, vrfID string) ([]*models.DHCPScope, error) {
	return m.scopesByVRF[vrfID], nil
}

func (m *mockStore) GetScopeByID(ctx context.Context, scopeID string) (*models.DHCPScope, error) {
	for _, scopes := range m.scopesByVRF {
		for _, s := range scopes {
			if s.ID == scopeID {
				return s, nil
			}
		}
	}
	return nil, models.ErrNotFound
}

func (m *mockStore) FindScopeByGiaddr(ctx context.Context, giaddr net.IP) (*models.DHCPScope, error) {
	for _, scopes := range m.scopesByVRF {
		for _, s := range scopes {
			if s.Network == nil {
				continue
			}
			n := net.IPNet(*s.Network)
			if n.Contains(giaddr) {
				return s, nil
			}
		}
	}
	return nil, nil
}

func (m *mockStore) FindScopeByNetwork(ctx context.Context, vrfID string, network *net.IPNet) (*models.DHCPScope, error) {
	return nil, nil
}

func (m *mockStore) GetPoolsByScope(ctx context.Context, scopeID string) ([]*models.DHCPPool, error) {
	return m.poolsByScope[scopeID], nil
}

func (m *mockStore) GetPoolByID(ctx context.Context, poolID string) (*models.DHCPPool, error) {
	for _, pools := range m.poolsByScope {
		for _, p := range pools {
			if p.ID == poolID {
				return p, nil
			}
		}
	}
	return nil, models.ErrNotFound
}

func (m *mockStore) GetOptionsByScope(ctx context.Context, scopeID string) ([]*models.DHCPOption, error) {
	return m.scopeOptions[scopeID], nil
}

func (m *mockStore) GetOptionsByPool(ctx context.Context, poolID string) ([]*models.DHCPOption, error) {
	return m.poolOptions[poolID], nil
}

func (m *mockStore) GetOptionsByReservation(ctx context.Context, reservationID string) ([]*models.DHCPOption, error) {
	return nil, nil
}

func (m *mockStore) GetOptionsByTemplate(ctx context.Context, templateID string) ([]*models.DHCPOption, error) {
	return nil, nil
}

func (m *mockStore) GetGlobalOptions(ctx context.Context) ([]*models.DHCPOption, error) {
	return m.globalOptions, nil
}

func (m *mockStore) ResolveOption(ctx context.Context, optCode int16, scopeID, poolID, reservationID string) (*models.DHCPOption, error) {
	find := func(opts []*models.DHCPOption) *models.DHCPOption {
		for _, o := range opts {
			if o.OptionCode == optCode {
				return o
			}
		}
		return nil
	}
	if reservationID != "" {
		if o := find(nil); o != nil { // reservation options not modeled in this mock
			return o, nil
		}
	}
	if poolID != "" {
		if o := find(m.poolOptions[poolID]); o != nil {
			return o, nil
		}
	}
	if scopeID != "" {
		if o := find(m.scopeOptions[scopeID]); o != nil {
			return o, nil
		}
	}
	if o := find(m.globalOptions); o != nil {
		return o, nil
	}
	return nil, nil
}

func (m *mockStore) GetTemplateByID(ctx context.Context, templateID string) (*models.Template, error) {
	return nil, nil
}

func (m *mockStore) GetTemplatesByScope(ctx context.Context, scopeID string) ([]*models.Template, error) {
	return nil, nil
}

func (m *mockStore) CanAllocateInPool(ctx context.Context, poolID string, ip net.IP) (bool, error) {
	return true, nil
}

// ---------------------------------------------------------------------------
// IPAMStore / IPAMAdminStore — unused by the DHCP engine; stubs.
// ---------------------------------------------------------------------------

func (m *mockStore) GetPrefixByID(ctx context.Context, prefixID string) (*models.IPAMPrefix, error) {
	return nil, nil
}
func (m *mockStore) GetPrefixesByVRF(ctx context.Context, vrfID string) ([]*models.IPAMPrefix, error) {
	return nil, nil
}
func (m *mockStore) GetChildPrefixes(ctx context.Context, parentID string) ([]*models.IPAMPrefix, error) {
	return nil, nil
}
func (m *mockStore) GetAncestorPrefixes(ctx context.Context, prefixID string) ([]*models.IPAMPrefix, error) {
	return nil, nil
}
func (m *mockStore) FindPrefixContaining(ctx context.Context, vrfID string, cidr *net.IPNet) (*models.IPAMPrefix, error) {
	return nil, nil
}
func (m *mockStore) ListPrefixesInCIDR(ctx context.Context, vrfID string, cidr *net.IPNet) ([]*models.IPAMPrefix, error) {
	return nil, nil
}
func (m *mockStore) ListPrefixes(ctx context.Context, params store.ListParams) (store.ListResult[*models.IPAMPrefix], error) {
	return store.ListResult[*models.IPAMPrefix]{}, nil
}
func (m *mockStore) CreatePrefix(ctx context.Context, p *models.IPAMPrefix) error { return nil }
func (m *mockStore) UpdatePrefix(ctx context.Context, p *models.IPAMPrefix) error { return nil }
func (m *mockStore) DeletePrefix(ctx context.Context, id string) error            { return nil }
func (m *mockStore) GetIPAMTree(ctx context.Context, vrfID string, parentID *string, depth int) ([]*models.IPAMTreeNode, error) {
	return nil, nil
}

func (m *mockStore) GetPrefixUtilization(ctx context.Context, prefixID string) (*models.PrefixUtilization, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// StatsRecorder — real behavior (counts recorded per scope for assertions).
// ---------------------------------------------------------------------------

func (m *mockStore) RecordDiscover(scopeID string) { m.discoverCount[scopeID]++ }
func (m *mockStore) RecordOffer(scopeID string)    { m.offerCount[scopeID]++ }
func (m *mockStore) RecordRequest(scopeID string)  { m.requestCount[scopeID]++ }
func (m *mockStore) RecordAck(scopeID string)      { m.ackCount[scopeID]++ }
func (m *mockStore) RecordNak(scopeID string)      { m.nakCount[scopeID]++ }
func (m *mockStore) RecordDecline(scopeID string)  { m.declineCount[scopeID]++ }
func (m *mockStore) RecordRelease(scopeID string)  { m.releaseCount[scopeID]++ }
func (m *mockStore) RecordNoLeases(scopeID string) { m.noLeasesCount[scopeID]++ }
func (m *mockStore) GetStats(ctx context.Context, scopeID string, window time.Duration) (*models.DHCPStats, error) {
	return nil, nil
}
func (m *mockStore) SampleUtilization(ctx context.Context) error { return nil }

// ---------------------------------------------------------------------------
// UserStore — unused by the DHCP engine; stubs.
// ---------------------------------------------------------------------------

func (m *mockStore) GetUserByID(ctx context.Context, userID int64) (*models.User, error) {
	return nil, nil
}
func (m *mockStore) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	return nil, nil
}
func (m *mockStore) CreateUser(ctx context.Context, user *models.User) error { return nil }
func (m *mockStore) UpdateUser(ctx context.Context, user *models.User) error { return nil }
func (m *mockStore) DeleteUser(ctx context.Context, userID int64) error      { return nil }
func (m *mockStore) ListUsers(ctx context.Context, params store.ListParams) (store.ListResult[*models.User], error) {
	return store.ListResult[*models.User]{}, nil
}
func (m *mockStore) CreateAPIToken(ctx context.Context, token *models.APIToken) error { return nil }
func (m *mockStore) ValidateAPIToken(ctx context.Context, tokenHash string) (*models.APIToken, error) {
	return nil, nil
}
func (m *mockStore) RevokeAPIToken(ctx context.Context, tokenID int64) error { return nil }
func (m *mockStore) ListAPITokens(ctx context.Context, userID int64) ([]*models.APIToken, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// ClusterStore — ListServers has real behavior (drives lease.go's T1
// fraction decision); the rest are unused stubs.
// ---------------------------------------------------------------------------

func (m *mockStore) GetClusterByID(ctx context.Context, clusterID string) (*models.Cluster, error) {
	return nil, nil
}
func (m *mockStore) WriteHeartbeat(ctx context.Context, clusterID, serverID string) error {
	return nil
}
func (m *mockStore) GetHeartbeats(ctx context.Context, clusterID string) ([]*models.Heartbeat, error) {
	return nil, nil
}
func (m *mockStore) ElectionQuery(ctx context.Context, clusterID string) (string, error) {
	return "", nil
}
func (m *mockStore) RegisterServer(ctx context.Context, server *models.ClusterServer) error {
	return nil
}
func (m *mockStore) ListServers(ctx context.Context, clusterID string) ([]*models.ClusterServer, error) {
	m.clusterSizeCallCount++
	return m.servers, nil
}
func (m *mockStore) UpdateLeaseVersion(ctx context.Context, clusterID, serverID string) (int, error) {
	return 0, nil
}
func (m *mockStore) UpdateLastConfigVersion(ctx context.Context, clusterID, serverID string, version int) error {
	return nil
}
func (m *mockStore) ListClusters(ctx context.Context, params store.ListParams) (store.ListResult[*models.Cluster], error) {
	return store.ListResult[*models.Cluster]{}, nil
}
func (m *mockStore) CreateCluster(ctx context.Context, c *models.Cluster) error { return nil }
func (m *mockStore) UpdateCluster(ctx context.Context, c *models.Cluster) error { return nil }
func (m *mockStore) DeleteCluster(ctx context.Context, clusterID string) error  { return nil }
func (m *mockStore) DeregisterServer(ctx context.Context, clusterID, serverID string) error {
	return nil
}
func (m *mockStore) GetClusterHealth(ctx context.Context, clusterID string) (*models.ClusterHealth, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// AuditStore — unused; stubs.
// ---------------------------------------------------------------------------

func (m *mockStore) LogAction(ctx context.Context, log *models.AuditLog) error { return nil }
func (m *mockStore) ListAuditLog(ctx context.Context, offset, limit int, filters map[string]interface{}) ([]*models.AuditLog, int64, error) {
	return nil, 0, nil
}

// ---------------------------------------------------------------------------
// VRFStore / ScopeStore / PoolStore / ReservationStore / LeaseQueryStore —
// CRUD/List surface for the API layer; unused by the DHCP engine, stubs.
// ---------------------------------------------------------------------------

func (m *mockStore) ListVRFs(ctx context.Context, params store.ListParams) (store.ListResult[*models.VRF], error) {
	return store.ListResult[*models.VRF]{}, nil
}
func (m *mockStore) CreateVRF(ctx context.Context, v *models.VRF) error { return nil }
func (m *mockStore) UpdateVRF(ctx context.Context, v *models.VRF) error { return nil }
func (m *mockStore) DeleteVRF(ctx context.Context, id string) error     { return nil }

func (m *mockStore) ListScopes(ctx context.Context, params store.ListParams) (store.ListResult[*models.DHCPScope], error) {
	return store.ListResult[*models.DHCPScope]{}, nil
}
func (m *mockStore) CreateScope(ctx context.Context, s *models.DHCPScope) error { return nil }
func (m *mockStore) UpdateScope(ctx context.Context, s *models.DHCPScope) error { return nil }
func (m *mockStore) DeleteScope(ctx context.Context, id string) error           { return nil }
func (m *mockStore) GetScopeUtilization(ctx context.Context, scopeID string) (*models.Utilization, error) {
	return nil, nil
}

func (m *mockStore) ListPoolsByScope(ctx context.Context, scopeID string, params store.ListParams) (store.ListResult[*models.DHCPPool], error) {
	return store.ListResult[*models.DHCPPool]{}, nil
}
func (m *mockStore) CreatePool(ctx context.Context, p *models.DHCPPool) error { return nil }
func (m *mockStore) UpdatePool(ctx context.Context, p *models.DHCPPool) error { return nil }
func (m *mockStore) DeletePool(ctx context.Context, id string) error          { return nil }
func (m *mockStore) GetPoolUtilization(ctx context.Context, poolID string) (*models.Utilization, error) {
	return nil, nil
}

func (m *mockStore) ListReservationsByPool(ctx context.Context, poolID string, params store.ListParams) (store.ListResult[*models.DHCPReservation], error) {
	return store.ListResult[*models.DHCPReservation]{}, nil
}
func (m *mockStore) GetReservation(ctx context.Context, id string) (*models.DHCPReservation, error) {
	return nil, nil
}
func (m *mockStore) CreateReservation(ctx context.Context, r *models.DHCPReservation) error {
	return nil
}
func (m *mockStore) UpdateReservation(ctx context.Context, r *models.DHCPReservation) error {
	return nil
}
func (m *mockStore) DeleteReservation(ctx context.Context, id string) error { return nil }

func (m *mockStore) ListLeases(ctx context.Context, params store.ListParams) (store.ListResult[*models.DHCPLease], error) {
	return store.ListResult[*models.DHCPLease]{}, nil
}
func (m *mockStore) GetLease(ctx context.Context, id string) (*models.DHCPLease, error) {
	return nil, nil
}
func (m *mockStore) DeleteLease(ctx context.Context, id string) error { return nil }
func (m *mockStore) ForceRenewLease(ctx context.Context, id string, expiry time.Time) (*models.DHCPLease, error) {
	return nil, nil
}
func (m *mockStore) ListLeasesByPool(ctx context.Context, poolID string, params store.ListParams) (store.ListResult[*models.DHCPLease], error) {
	return store.ListResult[*models.DHCPLease]{}, nil
}
func (m *mockStore) GetLeaseByIPGlobal(ctx context.Context, ip net.IP) (*models.DHCPLease, error) {
	return nil, nil
}
func (m *mockStore) ListLeasesByMAC(ctx context.Context, mac string) ([]*models.DHCPLease, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// OptionCatalogStore / OptionValueStore / TemplateStore / TagStore /
// DashboardStore / SearchStore / AllocationHelperStore / ConfigChangeStore —
// unused by the DHCP engine (option catalog entries are supplied inline via
// models.DHCPOption.Catalog in test fixtures); stubs.
// ---------------------------------------------------------------------------

func (m *mockStore) ListOptionCatalog(ctx context.Context, params store.ListParams) (store.ListResult[*models.OptionCatalogEntry], error) {
	return store.ListResult[*models.OptionCatalogEntry]{}, nil
}
func (m *mockStore) GetAllOptionCatalog(ctx context.Context) ([]*models.OptionCatalogEntry, error) {
	return nil, nil
}
func (m *mockStore) GetOptionCatalogEntry(ctx context.Context, code int16) (*models.OptionCatalogEntry, error) {
	return nil, nil
}
func (m *mockStore) CreateOptionCatalogEntry(ctx context.Context, entry *models.OptionCatalogEntry) error {
	return nil
}
func (m *mockStore) DeleteOptionCatalogEntry(ctx context.Context, code int16) error { return nil }

func (m *mockStore) CreateOptionValue(ctx context.Context, ov *models.DHCPOption) error { return nil }
func (m *mockStore) UpdateOptionValue(ctx context.Context, id string, value string, isEnabled bool) (*models.DHCPOption, error) {
	return nil, nil
}
func (m *mockStore) DeleteOptionValue(ctx context.Context, id string) error { return nil }

func (m *mockStore) ListTemplates(ctx context.Context, params store.ListParams) (store.ListResult[*models.Template], error) {
	return store.ListResult[*models.Template]{}, nil
}
func (m *mockStore) CreateTemplate(ctx context.Context, t *models.Template) error { return nil }
func (m *mockStore) UpdateTemplate(ctx context.Context, t *models.Template) error { return nil }
func (m *mockStore) DeleteTemplate(ctx context.Context, id string) error          { return nil }
func (m *mockStore) ListTemplateScopeAssocs(ctx context.Context, templateID string) ([]*models.TemplateScopeAssoc, error) {
	return nil, nil
}
func (m *mockStore) AssociateTemplateScope(ctx context.Context, templateID, scopeID string, priority int) (*models.TemplateScopeAssoc, error) {
	return nil, nil
}
func (m *mockStore) DisassociateTemplateScope(ctx context.Context, templateID, scopeID string) error {
	return nil
}
func (m *mockStore) ListTemplatePoolAssocs(ctx context.Context, templateID string) ([]*models.TemplatePoolAssoc, error) {
	return nil, nil
}
func (m *mockStore) AssociateTemplatePool(ctx context.Context, templateID, poolID string, priority int) (*models.TemplatePoolAssoc, error) {
	return nil, nil
}
func (m *mockStore) DisassociateTemplatePool(ctx context.Context, templateID, poolID string) error {
	return nil
}

func (m *mockStore) ListTags(ctx context.Context, params store.ListParams) (store.ListResult[*models.Tag], error) {
	return store.ListResult[*models.Tag]{}, nil
}
func (m *mockStore) CreateTag(ctx context.Context, t *models.Tag) error { return nil }
func (m *mockStore) DeleteTag(ctx context.Context, id string) error     { return nil }
func (m *mockStore) AddEntityTag(ctx context.Context, tagID string, entityType models.TaggableEntityType, entityID string) (*models.EntityTag, error) {
	return nil, nil
}
func (m *mockStore) RemoveEntityTag(ctx context.Context, tagID string, entityType models.TaggableEntityType, entityID string) error {
	return nil
}
func (m *mockStore) ListTagsForEntity(ctx context.Context, entityType models.TaggableEntityType, entityID string) ([]*models.Tag, error) {
	return nil, nil
}

func (m *mockStore) GetDashboardStats(ctx context.Context, window time.Duration) (*models.DashboardStats, error) {
	return nil, nil
}
func (m *mockStore) GetUtilizationChart(ctx context.Context, entityType models.UtilizationEntityType, entityID string, days int) ([]*models.ChartPoint, error) {
	return nil, nil
}

func (m *mockStore) GlobalSearch(ctx context.Context, query string, entityTypes []string, limit int) ([]*models.SearchResult, error) {
	return nil, nil
}

func (m *mockStore) NextAvailableIP(ctx context.Context, poolID string) (net.IP, error) {
	return nil, nil
}
func (m *mockStore) NextAvailableSubnet(ctx context.Context, parentPrefixID string, prefixLen int) (*models.IPNet, error) {
	return nil, nil
}

func (m *mockStore) NotifyConfigChange(ctx context.Context, channel string) error { return nil }
func (m *mockStore) ListenConfigChanges(ctx context.Context, channel string) (<-chan string, func() error, error) {
	ch := make(chan string)
	return ch, func() error { return nil }, nil
}

// ---------------------------------------------------------------------------
// VLANStore — stubs (the DHCP engine never touches VLANs).
// ---------------------------------------------------------------------------

func (m *mockStore) ListVLANGroups(ctx context.Context, params store.ListParams) (store.ListResult[*models.VLANGroup], error) {
	return store.ListResult[*models.VLANGroup]{Limit: params.Limit, Offset: params.Offset}, nil
}
func (m *mockStore) GetVLANGroupByID(ctx context.Context, id string) (*models.VLANGroup, error) {
	return nil, models.ErrNotFound
}
func (m *mockStore) CreateVLANGroup(ctx context.Context, g *models.VLANGroup) error { return nil }
func (m *mockStore) UpdateVLANGroup(ctx context.Context, g *models.VLANGroup) error { return nil }
func (m *mockStore) DeleteVLANGroup(ctx context.Context, id string) error           { return nil }
func (m *mockStore) ListVLANs(ctx context.Context, params store.ListParams) (store.ListResult[*models.VLAN], error) {
	return store.ListResult[*models.VLAN]{Limit: params.Limit, Offset: params.Offset}, nil
}
func (m *mockStore) GetVLANByID(ctx context.Context, id string) (*models.VLAN, error) {
	return nil, models.ErrNotFound
}
func (m *mockStore) CreateVLAN(ctx context.Context, v *models.VLAN) error { return nil }
func (m *mockStore) UpdateVLAN(ctx context.Context, v *models.VLAN) error { return nil }
func (m *mockStore) DeleteVLAN(ctx context.Context, id string) error      { return nil }
func (m *mockStore) ListPrefixesByVLAN(ctx context.Context, vlanID string) ([]*models.IPAMPrefix, error) {
	return nil, nil
}
