package api

// mockStore is a minimal in-memory store.Store implementation used only by
// this package's tests. It fully implements the aggregate interface: the
// handful of entities the tests actually exercise (users, API tokens, VRFs,
// audit log) are backed by real in-memory state; everything else returns a
// zero value / models.ErrNotFound so any handler that happens to call it
// fails predictably rather than panicking on a nil map.

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// randomID mints a fresh UUID string for entities the mock store creates,
// mirroring how the real Postgres store assigns IDs (amendment A13: string
// UUIDs throughout).
func randomID() string {
	return uuid.New().String()
}

type mockStore struct {
	mu sync.Mutex

	users      map[int64]*models.User
	usersByUN  map[string]int64
	nextUserID int64

	tokens      map[string]*models.APIToken // by hash
	nextTokenID int64

	vrfs map[string]*models.VRF

	leases map[string]*models.DHCPLease

	auditLogs []*models.AuditLog
}

func newMockStore() *mockStore {
	return &mockStore{
		users:     make(map[int64]*models.User),
		usersByUN: make(map[string]int64),
		tokens:    make(map[string]*models.APIToken),
		vrfs:      make(map[string]*models.VRF),
		leases:    make(map[string]*models.DHCPLease),
	}
}

func (m *mockStore) seedUser(u *models.User) *models.User {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextUserID++
	u.ID = m.nextUserID
	u.CreatedAt = time.Now()
	u.UpdatedAt = u.CreatedAt
	m.users[u.ID] = u
	m.usersByUN[u.Username] = u.ID
	return u
}

// ---- UserStore -------------------------------------------------------

func (m *mockStore) GetUserByID(ctx context.Context, userID int64) (*models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return nil, models.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (m *mockStore) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.usersByUN[username]
	if !ok {
		return nil, models.ErrNotFound
	}
	cp := *m.users[id]
	return &cp, nil
}

func (m *mockStore) CreateUser(ctx context.Context, user *models.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.usersByUN[user.Username]; exists {
		return models.ErrConflict
	}
	m.nextUserID++
	user.ID = m.nextUserID
	user.CreatedAt = time.Now()
	user.UpdatedAt = user.CreatedAt
	cp := *user
	m.users[user.ID] = &cp
	m.usersByUN[user.Username] = user.ID
	return nil
}

func (m *mockStore) UpdateUser(ctx context.Context, user *models.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[user.ID]; !ok {
		return models.ErrNotFound
	}
	user.UpdatedAt = time.Now()
	cp := *user
	m.users[user.ID] = &cp
	return nil
}

func (m *mockStore) DeleteUser(ctx context.Context, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return models.ErrNotFound
	}
	delete(m.users, userID)
	delete(m.usersByUN, u.Username)
	return nil
}

func (m *mockStore) ListUsers(ctx context.Context, params store.ListParams) (store.ListResult[*models.User], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]*models.User, 0, len(m.users))
	for _, u := range m.users {
		cp := *u
		items = append(items, &cp)
	}
	return store.ListResult[*models.User]{Items: items, Total: int64(len(items)), Limit: params.Limit, Offset: params.Offset}, nil
}

func (m *mockStore) CreateAPIToken(ctx context.Context, token *models.APIToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextTokenID++
	token.ID = m.nextTokenID
	token.CreatedAt = time.Now()
	cp := *token
	m.tokens[token.TokenHash] = &cp
	return nil
}

func (m *mockStore) ValidateAPIToken(ctx context.Context, tokenHash string) (*models.APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[tokenHash]
	if !ok {
		return nil, models.ErrNotFound
	}
	if t.ExpiresAt != nil && t.ExpiresAt.Before(time.Now()) {
		return nil, models.ErrUnauthorized
	}
	cp := *t
	return &cp, nil
}

func (m *mockStore) RevokeAPIToken(ctx context.Context, tokenID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, t := range m.tokens {
		if t.ID == tokenID {
			delete(m.tokens, h)
			return nil
		}
	}
	return models.ErrNotFound
}

func (m *mockStore) ListAPITokens(ctx context.Context, userID int64) ([]*models.APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.APIToken
	for _, t := range m.tokens {
		if t.UserID == userID {
			cp := *t
			out = append(out, &cp)
		}
	}
	return out, nil
}

// ---- VRFStore / ConfigStore.GetVRFByID --------------------------------

func (m *mockStore) ListVRFs(ctx context.Context, params store.ListParams) (store.ListResult[*models.VRF], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]*models.VRF, 0, len(m.vrfs))
	for _, v := range m.vrfs {
		cp := *v
		items = append(items, &cp)
	}
	return store.ListResult[*models.VRF]{Items: items, Total: int64(len(items)), Limit: params.Limit, Offset: params.Offset}, nil
}

func (m *mockStore) CreateVRF(ctx context.Context, v *models.VRF) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v.ID = randomID()
	v.CreatedAt = time.Now()
	v.UpdatedAt = v.CreatedAt
	cp := *v
	m.vrfs[v.ID] = &cp
	return nil
}

func (m *mockStore) UpdateVRF(ctx context.Context, v *models.VRF) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.vrfs[v.ID]; !ok {
		return models.ErrNotFound
	}
	v.UpdatedAt = time.Now()
	cp := *v
	m.vrfs[v.ID] = &cp
	return nil
}

func (m *mockStore) DeleteVRF(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.vrfs[id]; !ok {
		return models.ErrNotFound
	}
	delete(m.vrfs, id)
	return nil
}

func (m *mockStore) GetVRFByID(ctx context.Context, vrfID string) (*models.VRF, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vrfs[vrfID]
	if !ok {
		return nil, models.ErrNotFound
	}
	cp := *v
	return &cp, nil
}

// ---- AuditStore --------------------------------------------------------

func (m *mockStore) LogAction(ctx context.Context, log *models.AuditLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	log.ID = int64(len(m.auditLogs) + 1)
	log.Timestamp = time.Now()
	m.auditLogs = append(m.auditLogs, log)
	return nil
}

// failedLogins returns the recorded login_failed entries in order, so a test
// can assert both that an attempt was recorded and what it was attributed to.
func (m *mockStore) failedLogins() []*models.AuditLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.AuditLog
	for _, l := range m.auditLogs {
		if l.Action == models.AuditActionLoginFailed {
			out = append(out, l)
		}
	}
	return out
}

func (m *mockStore) failedLoginCount() int { return len(m.failedLogins()) }

func (m *mockStore) ListAuditLog(ctx context.Context, offset, limit int, filters map[string]interface{}) ([]*models.AuditLog, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.auditLogs, int64(len(m.auditLogs)), nil
}

// ---- everything else: unexercised by tests, stubbed to satisfy store.Store.

func (m *mockStore) AllocateLease(ctx context.Context, poolID string, ident models.ClientIdentity, hostname *string, leaseDur time.Duration) (*models.DHCPLease, error) {
	return nil, models.ErrNoLeases
}
func (m *mockStore) RenewLease(ctx context.Context, leaseID, clientMAC string, newExpiry time.Time, hostname *string) (*models.DHCPLease, error) {
	return nil, models.ErrLeaseNotFound
}
func (m *mockStore) ReleaseLease(ctx context.Context, leaseID, clientMAC string) error {
	return m.setLeaseState(leaseID, models.LeaseStateReleased)
}
func (m *mockStore) AbandonLease(ctx context.Context, leaseID, clientMAC string) error {
	return m.setLeaseState(leaseID, models.LeaseStateAbandoned)
}
func (m *mockStore) setLeaseState(leaseID string, state models.LeaseBindingState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[leaseID]
	if !ok {
		return models.ErrLeaseNotFound
	}
	l.BindingState = state
	return nil
}
func (m *mockStore) GetLeaseByIP(ctx context.Context, poolID string, ip net.IP) (*models.DHCPLease, error) {
	return nil, nil
}
func (m *mockStore) GetLeaseByClient(ctx context.Context, ident models.ClientIdentity) (*models.DHCPLease, error) {
	return nil, nil
}
func (m *mockStore) GetLeasesByPool(ctx context.Context, poolID, state string, offset, limit int) ([]*models.DHCPLease, int64, error) {
	return nil, 0, nil
}
func (m *mockStore) ExpireLeasesOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStore) PurgeLeasesBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStore) ReservationForLease(ctx context.Context, poolID, bindingKey string) (*models.DHCPReservation, error) {
	return nil, nil
}
func (m *mockStore) ReservationsForKeys(ctx context.Context, poolID string, keys []string) (*models.DHCPReservation, error) {
	return nil, nil
}

// ReservationByBindingKey and ReservationByIP back amendment A19's
// reservation-conflict guards. This bare double answers "no such reservation"
// (nil, nil -- a miss, not an error); the handler tests that exercise the
// conflicts use store.Mock, which implements them for real.
func (m *mockStore) ReservationByBindingKey(ctx context.Context, poolID, bindingKey string) (*models.DHCPReservation, error) {
	return nil, nil
}
func (m *mockStore) ReservationByIP(ctx context.Context, poolID string, ip net.IP) (*models.DHCPReservation, error) {
	return nil, nil
}
func (m *mockStore) ListClientsUnderPrefix(ctx context.Context, prefixID string, params store.ListParams) (store.ListResult[*models.DHCPClient], error) {
	return store.ListResult[*models.DHCPClient]{}, nil
}

func (m *mockStore) GetVRFByInterface(ctx context.Context, ifname string) (*models.VRF, error) {
	return nil, models.ErrNotFound
}
func (m *mockStore) GetScopesByVRF(ctx context.Context, vrfID string) ([]*models.DHCPScope, error) {
	return nil, nil
}
func (m *mockStore) GetScopeByID(ctx context.Context, scopeID string) (*models.DHCPScope, error) {
	return nil, models.ErrNotFound
}
func (m *mockStore) FindScopeByGiaddr(ctx context.Context, giaddr net.IP) (*models.DHCPScope, error) {
	return nil, models.ErrNotFound
}
func (m *mockStore) FindScopeByNetwork(ctx context.Context, vrfID string, network *net.IPNet) (*models.DHCPScope, error) {
	return nil, models.ErrNotFound
}
func (m *mockStore) GetPoolsByScope(ctx context.Context, scopeID string) ([]*models.DHCPPool, error) {
	return nil, nil
}
func (m *mockStore) GetPoolByID(ctx context.Context, poolID string) (*models.DHCPPool, error) {
	return nil, models.ErrNotFound
}
func (m *mockStore) GetOptionsByScope(ctx context.Context, scopeID string) ([]*models.DHCPOption, error) {
	return nil, nil
}
func (m *mockStore) GetOptionsByPool(ctx context.Context, poolID string) ([]*models.DHCPOption, error) {
	return nil, nil
}
func (m *mockStore) GetOptionsByReservation(ctx context.Context, reservationID string) ([]*models.DHCPOption, error) {
	return nil, nil
}
func (m *mockStore) GetOptionsByTemplate(ctx context.Context, templateID string) ([]*models.DHCPOption, error) {
	return nil, nil
}
func (m *mockStore) GetGlobalOptions(ctx context.Context) ([]*models.DHCPOption, error) {
	return nil, nil
}
func (m *mockStore) ResolveOption(ctx context.Context, optCode int16, scopeID, poolID, reservationID string) (*models.DHCPOption, error) {
	return nil, nil
}
func (m *mockStore) GetTemplateByID(ctx context.Context, templateID string) (*models.Template, error) {
	return nil, models.ErrNotFound
}
func (m *mockStore) GetTemplatesByScope(ctx context.Context, scopeID string) ([]*models.Template, error) {
	return nil, nil
}
func (m *mockStore) CanAllocateInPool(ctx context.Context, poolID string, ip net.IP) (bool, error) {
	return false, nil
}

func (m *mockStore) GetPrefixByID(ctx context.Context, prefixID string) (*models.IPAMPrefix, error) {
	return nil, models.ErrNotFound
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

func (m *mockStore) RecordDiscover(scopeID string) {}
func (m *mockStore) RecordOffer(scopeID string)    {}
func (m *mockStore) RecordRequest(scopeID string)  {}
func (m *mockStore) RecordAck(scopeID string)      {}
func (m *mockStore) RecordNak(scopeID string)      {}
func (m *mockStore) RecordDecline(scopeID string)  {}
func (m *mockStore) RecordRelease(scopeID string)  {}
func (m *mockStore) RecordNoLeases(scopeID string) {}
func (m *mockStore) GetStats(ctx context.Context, scopeID string, window time.Duration) (*models.DHCPStats, error) {
	return &models.DHCPStats{ScopeID: scopeID}, nil
}
func (m *mockStore) SampleUtilization(ctx context.Context) error { return nil }

func (m *mockStore) GetClusterByID(ctx context.Context, clusterID string) (*models.Cluster, error) {
	return nil, models.ErrNotFound
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
	return nil, nil
}
func (m *mockStore) UpdateLeaseVersion(ctx context.Context, clusterID, serverID string) (int, error) {
	return 0, nil
}
func (m *mockStore) UpdateLastConfigVersion(ctx context.Context, clusterID, serverID string, version int) error {
	return nil
}
func (m *mockStore) ListClusters(ctx context.Context, params store.ListParams) (store.ListResult[*models.Cluster], error) {
	return store.ListResult[*models.Cluster]{Limit: params.Limit, Offset: params.Offset}, nil
}
func (m *mockStore) CreateCluster(ctx context.Context, c *models.Cluster) error { return nil }
func (m *mockStore) UpdateCluster(ctx context.Context, c *models.Cluster) error { return nil }
func (m *mockStore) DeleteCluster(ctx context.Context, clusterID string) error  { return nil }
func (m *mockStore) DeregisterServer(ctx context.Context, clusterID, serverID string) error {
	return nil
}
func (m *mockStore) GetClusterHealth(ctx context.Context, clusterID string) (*models.ClusterHealth, error) {
	return nil, models.ErrNotFound
}

func (m *mockStore) ListPrefixes(ctx context.Context, params store.ListParams) (store.ListResult[*models.IPAMPrefix], error) {
	return store.ListResult[*models.IPAMPrefix]{Limit: params.Limit, Offset: params.Offset}, nil
}
func (m *mockStore) CreatePrefix(ctx context.Context, p *models.IPAMPrefix) error { return nil }
func (m *mockStore) UpdatePrefix(ctx context.Context, p *models.IPAMPrefix) error { return nil }
func (m *mockStore) DeletePrefix(ctx context.Context, id string) error            { return nil }
func (m *mockStore) GetIPAMTree(ctx context.Context, vrfID string, parentID *string, depth int) ([]*models.IPAMTreeNode, error) {
	return nil, nil
}

func (m *mockStore) GetPrefixUtilization(ctx context.Context, prefixID string) (*models.PrefixUtilization, error) {
	return nil, models.ErrNotFound
}

func (m *mockStore) ListScopes(ctx context.Context, params store.ListParams) (store.ListResult[*models.DHCPScope], error) {
	return store.ListResult[*models.DHCPScope]{Limit: params.Limit, Offset: params.Offset}, nil
}
func (m *mockStore) CreateScope(ctx context.Context, s *models.DHCPScope) error { return nil }
func (m *mockStore) UpdateScope(ctx context.Context, s *models.DHCPScope) error { return nil }
func (m *mockStore) DeleteScope(ctx context.Context, id string) error           { return nil }
func (m *mockStore) GetScopeUtilization(ctx context.Context, scopeID string) (*models.Utilization, error) {
	return nil, models.ErrNotFound
}

func (m *mockStore) ListPoolsByScope(ctx context.Context, scopeID string, params store.ListParams) (store.ListResult[*models.DHCPPool], error) {
	return store.ListResult[*models.DHCPPool]{Limit: params.Limit, Offset: params.Offset}, nil
}
func (m *mockStore) CreatePool(ctx context.Context, p *models.DHCPPool) error { return nil }
func (m *mockStore) UpdatePool(ctx context.Context, p *models.DHCPPool) error { return nil }
func (m *mockStore) DeletePool(ctx context.Context, id string) error          { return nil }
func (m *mockStore) GetPoolUtilization(ctx context.Context, poolID string) (*models.Utilization, error) {
	return nil, models.ErrNotFound
}

func (m *mockStore) ListReservationsByPool(ctx context.Context, poolID string, params store.ListParams) (store.ListResult[*models.DHCPReservation], error) {
	return store.ListResult[*models.DHCPReservation]{Limit: params.Limit, Offset: params.Offset}, nil
}
func (m *mockStore) GetReservation(ctx context.Context, id string) (*models.DHCPReservation, error) {
	return nil, models.ErrNotFound
}
func (m *mockStore) CreateReservation(ctx context.Context, r *models.DHCPReservation) error {
	return nil
}
func (m *mockStore) UpdateReservation(ctx context.Context, r *models.DHCPReservation) error {
	return nil
}
func (m *mockStore) DeleteReservation(ctx context.Context, id string) error { return nil }

// seedLease registers a lease so the lease-action handler tests have
// something with real state to release/renew/delete.
func (m *mockStore) seedLease(l *models.DHCPLease) *models.DHCPLease {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l.ID == "" {
		l.ID = randomID()
	}
	m.leases[l.ID] = l
	return l
}

func (m *mockStore) ListLeases(ctx context.Context, params store.ListParams) (store.ListResult[*models.DHCPLease], error) {
	return store.ListResult[*models.DHCPLease]{Limit: params.Limit, Offset: params.Offset}, nil
}
func (m *mockStore) GetLease(ctx context.Context, id string) (*models.DHCPLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[id]
	if !ok {
		return nil, models.ErrLeaseNotFound
	}
	return l, nil
}
func (m *mockStore) DeleteLease(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.leases[id]; !ok {
		return models.ErrLeaseNotFound
	}
	delete(m.leases, id)
	return nil
}
func (m *mockStore) ForceRenewLease(ctx context.Context, id string, expiry time.Time) (*models.DHCPLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[id]
	if !ok {
		return nil, models.ErrLeaseNotFound
	}
	l.LeaseStart = time.Now().UTC()
	l.LeaseEnd = expiry
	l.BindingState = models.LeaseStateActive
	return l, nil
}
func (m *mockStore) ListLeasesByPool(ctx context.Context, poolID string, params store.ListParams) (store.ListResult[*models.DHCPLease], error) {
	return store.ListResult[*models.DHCPLease]{Limit: params.Limit, Offset: params.Offset}, nil
}
func (m *mockStore) GetLeaseByIPGlobal(ctx context.Context, ip net.IP) (*models.DHCPLease, error) {
	return nil, models.ErrNotFound
}
func (m *mockStore) ListLeasesByMAC(ctx context.Context, mac string) ([]*models.DHCPLease, error) {
	return nil, nil
}

func (m *mockStore) ListOptionCatalog(ctx context.Context, params store.ListParams) (store.ListResult[*models.OptionCatalogEntry], error) {
	return store.ListResult[*models.OptionCatalogEntry]{Limit: params.Limit, Offset: params.Offset}, nil
}
func (m *mockStore) GetAllOptionCatalog(ctx context.Context) ([]*models.OptionCatalogEntry, error) {
	return nil, nil
}
func (m *mockStore) GetOptionCatalogEntry(ctx context.Context, code int16) (*models.OptionCatalogEntry, error) {
	return nil, models.ErrNotFound
}
func (m *mockStore) CreateOptionCatalogEntry(ctx context.Context, entry *models.OptionCatalogEntry) error {
	return nil
}
func (m *mockStore) DeleteOptionCatalogEntry(ctx context.Context, code int16) error { return nil }

func (m *mockStore) CreateOptionValue(ctx context.Context, ov *models.DHCPOption) error { return nil }
func (m *mockStore) UpdateOptionValue(ctx context.Context, id string, value string, isEnabled bool) (*models.DHCPOption, error) {
	return nil, models.ErrNotFound
}
func (m *mockStore) DeleteOptionValue(ctx context.Context, id string) error { return nil }

func (m *mockStore) ListTemplates(ctx context.Context, params store.ListParams) (store.ListResult[*models.Template], error) {
	return store.ListResult[*models.Template]{Limit: params.Limit, Offset: params.Offset}, nil
}
func (m *mockStore) CreateTemplate(ctx context.Context, t *models.Template) error { return nil }
func (m *mockStore) UpdateTemplate(ctx context.Context, t *models.Template) error { return nil }
func (m *mockStore) DeleteTemplate(ctx context.Context, id string) error          { return nil }
func (m *mockStore) ListTemplateScopeAssocs(ctx context.Context, templateID string) ([]*models.TemplateScopeAssoc, error) {
	return nil, nil
}
func (m *mockStore) AssociateTemplateScope(ctx context.Context, templateID, scopeID string, priority int) (*models.TemplateScopeAssoc, error) {
	return &models.TemplateScopeAssoc{TemplateID: templateID, ScopeID: scopeID, Priority: priority}, nil
}
func (m *mockStore) DisassociateTemplateScope(ctx context.Context, templateID, scopeID string) error {
	return nil
}
func (m *mockStore) ListTemplatePoolAssocs(ctx context.Context, templateID string) ([]*models.TemplatePoolAssoc, error) {
	return nil, nil
}
func (m *mockStore) AssociateTemplatePool(ctx context.Context, templateID, poolID string, priority int) (*models.TemplatePoolAssoc, error) {
	return &models.TemplatePoolAssoc{TemplateID: templateID, PoolID: poolID, Priority: priority}, nil
}
func (m *mockStore) DisassociateTemplatePool(ctx context.Context, templateID, poolID string) error {
	return nil
}

func (m *mockStore) ListTags(ctx context.Context, params store.ListParams) (store.ListResult[*models.Tag], error) {
	return store.ListResult[*models.Tag]{Limit: params.Limit, Offset: params.Offset}, nil
}
func (m *mockStore) CreateTag(ctx context.Context, t *models.Tag) error { return nil }
func (m *mockStore) DeleteTag(ctx context.Context, id string) error     { return nil }
func (m *mockStore) AddEntityTag(ctx context.Context, tagID string, entityType models.TaggableEntityType, entityID string) (*models.EntityTag, error) {
	return &models.EntityTag{TagID: tagID, EntityType: entityType, EntityID: entityID}, nil
}
func (m *mockStore) RemoveEntityTag(ctx context.Context, tagID string, entityType models.TaggableEntityType, entityID string) error {
	return nil
}
func (m *mockStore) ListTagsForEntity(ctx context.Context, entityType models.TaggableEntityType, entityID string) ([]*models.Tag, error) {
	return nil, nil
}

func (m *mockStore) GetDashboardStats(ctx context.Context, window time.Duration) (*models.DashboardStats, error) {
	return &models.DashboardStats{}, nil
}
func (m *mockStore) GetUtilizationChart(ctx context.Context, entityType models.UtilizationEntityType, entityID string, days int) ([]*models.ChartPoint, error) {
	return nil, nil
}

func (m *mockStore) GlobalSearch(ctx context.Context, query string, entityTypes []string, limit int) ([]*models.SearchResult, error) {
	return nil, nil
}

func (m *mockStore) NextAvailableIP(ctx context.Context, poolID string) (net.IP, error) {
	return nil, models.ErrNoLeases
}
func (m *mockStore) NextAvailableSubnet(ctx context.Context, parentPrefixID string, prefixLen int) (*models.IPNet, error) {
	return nil, models.ErrNotFound
}

func (m *mockStore) NotifyConfigChange(ctx context.Context, channel string) error { return nil }
func (m *mockStore) ListenConfigChanges(ctx context.Context, channel string) (<-chan string, func() error, error) {
	ch := make(chan string)
	return ch, func() error { return nil }, nil
}

var _ store.Store = (*mockStore)(nil)

// ---------------------------------------------------------------------------
// VLANStore — stubs (VLAN handlers are covered by the store-backed tests).
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
