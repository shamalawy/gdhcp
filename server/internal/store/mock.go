// mock.go implements an in-memory Store for unit tests of consumers (the
// DHCP engine, HA manager, and API handlers) that don't want a live
// PostgreSQL instance. It follows the same documented semantics as the
// Postgres implementation (amendment A6 allocation order, option
// inheritance precedence, HasDependents delete guards, etc.) at a level of
// fidelity sufficient for exercising consumer logic, not as a schema-perfect
// reimplementation of every constraint/trigger in docs/db/schema.sql.
package store

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// Mock is an in-memory Store. The zero value is not usable; construct with
// NewMock. All state is guarded by mu; every exported method takes the lock
// itself, so Mock is safe for concurrent use like the Postgres
// implementation.
type Mock struct {
	mu sync.Mutex

	vrfs       map[string]*models.VRF
	vlans      map[string]*models.VLAN
	vlanGroups map[string]*models.VLANGroup
	prefixes   map[string]*models.IPAMPrefix

	clusters   map[string]*models.Cluster
	servers    map[string]*models.ClusterServer
	heartbeats map[string]*models.Heartbeat // key: clusterID+"/"+serverID

	scopes       map[string]*models.DHCPScope
	pools        map[string]*models.DHCPPool
	reservations map[string]*models.DHCPReservation
	leases       map[string]*models.DHCPLease

	optionCatalog map[int16]*models.OptionCatalogEntry
	optionValues  map[string]*models.DHCPOption

	templates      map[string]*models.Template
	templateScopes map[string]*models.TemplateScopeAssoc // key: id
	templatePools  map[string]*models.TemplatePoolAssoc

	tags       map[string]*models.Tag
	entityTags map[string]*models.EntityTag

	users      map[int64]*models.User
	nextUserID int64

	tokens      map[int64]*models.APIToken
	nextTokenID int64

	auditLog    []*models.AuditLog
	nextAuditID int64

	txnStats map[string]*models.DHCPStats // scopeID -> cumulative counters

	listeners map[string][]chan string // ConfigChangeStore pub/sub, keyed by channel
}

// NewMock constructs an empty in-memory Store.
func NewMock() *Mock {
	return &Mock{
		vrfs:           make(map[string]*models.VRF),
		vlans:          make(map[string]*models.VLAN),
		vlanGroups:     make(map[string]*models.VLANGroup),
		prefixes:       make(map[string]*models.IPAMPrefix),
		clusters:       make(map[string]*models.Cluster),
		servers:        make(map[string]*models.ClusterServer),
		heartbeats:     make(map[string]*models.Heartbeat),
		scopes:         make(map[string]*models.DHCPScope),
		pools:          make(map[string]*models.DHCPPool),
		reservations:   make(map[string]*models.DHCPReservation),
		leases:         make(map[string]*models.DHCPLease),
		optionCatalog:  make(map[int16]*models.OptionCatalogEntry),
		optionValues:   make(map[string]*models.DHCPOption),
		templates:      make(map[string]*models.Template),
		templateScopes: make(map[string]*models.TemplateScopeAssoc),
		templatePools:  make(map[string]*models.TemplatePoolAssoc),
		tags:           make(map[string]*models.Tag),
		entityTags:     make(map[string]*models.EntityTag),
		users:          make(map[int64]*models.User),
		tokens:         make(map[int64]*models.APIToken),
		txnStats:       make(map[string]*models.DHCPStats),
		listeners:      make(map[string][]chan string),
	}
}

var _ Store = (*Mock)(nil)

// ---------------------------------------------------------------------------
// Small shared helpers
// ---------------------------------------------------------------------------

func matchStr(s, needle string) bool {
	return needle == "" || strings.Contains(strings.ToLower(s), strings.ToLower(needle))
}

func paginate[T any](items []T, limit, offset int) []T {
	if offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) || limit <= 0 {
		end = len(items)
	}
	return items[offset:end]
}

func macPtr(mac string) *models.HardwareAddr {
	if mac == "" {
		return nil
	}
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return nil
	}
	h := models.HardwareAddr(hw)
	return &h
}

func macEquals(h *models.HardwareAddr, mac string) bool {
	if h == nil || mac == "" {
		return false
	}
	return strings.EqualFold(h.String(), mac)
}

// cidrNetworkAndBroadcast returns n's network and broadcast addresses,
// mirroring PostgreSQL's network()/broadcast() functions used by the
// Postgres store's candidate scan and utilization math.
func cidrNetworkAndBroadcast(n *net.IPNet) (net.IP, net.IP) {
	if n == nil || n.IP == nil {
		return nil, nil
	}
	ip := n.IP.To4()
	mask := n.Mask
	if ip == nil {
		ip = n.IP.To16()
	}
	network := make(net.IP, len(ip))
	broadcast := make(net.IP, len(ip))
	for i := range ip {
		network[i] = ip[i] & mask[i]
		broadcast[i] = ip[i] | ^mask[i]
	}
	return network, broadcast
}

// poolTotalAddresses computes usable address count for pool the same way
// amendment A6's candidate scan does: range size minus the scope network's
// network/broadcast addresses when they fall inside the pool's range.
func poolTotalAddresses(pool *models.DHCPPool, scope *models.DHCPScope) int64 {
	n := new(big.Int).Sub(ipToBigInt(pool.RangeEnd), ipToBigInt(pool.RangeStart))
	n.Add(n, big.NewInt(1))
	total := n.Int64()
	if total < 0 {
		return 0
	}
	if scope != nil && scope.Network != nil {
		ipnet := net.IPNet(*scope.Network)
		netAddr, bcastAddr := cidrNetworkAndBroadcast(&ipnet)
		if netAddr != nil && ipInRange(netAddr, pool.RangeStart, pool.RangeEnd) {
			total--
		}
		if bcastAddr != nil && ipInRange(bcastAddr, pool.RangeStart, pool.RangeEnd) {
			total--
		}
	}
	if total < 0 {
		total = 0
	}
	return total
}

// ---------------------------------------------------------------------------
// VRFStore + ConfigStore VRF reads
// ---------------------------------------------------------------------------

func (m *Mock) GetVRFByInterface(ctx context.Context, ifname string) (*models.VRF, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.vrfs {
		if v.IsDefault {
			return v, nil
		}
	}
	return nil, fmt.Errorf("mock: get vrf by interface %s: %w", ifname, models.ErrNotFound)
}

func (m *Mock) GetVRFByID(ctx context.Context, vrfID string) (*models.VRF, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vrfs[vrfID]
	if !ok {
		return nil, fmt.Errorf("mock: get vrf %s: %w", vrfID, models.ErrNotFound)
	}
	return v, nil
}

func (m *Mock) ListVRFs(ctx context.Context, params ListParams) (ListResult[*models.VRF], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*models.VRF
	for _, v := range m.vrfs {
		if matchStr(v.Name, params.Search) {
			items = append(items, v)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	limit, offset := normalizeListParams(params)
	total := int64(len(items))
	return ListResult[*models.VRF]{Items: paginate(items, limit, offset), Total: total, Limit: limit, Offset: offset}, nil
}

func (m *Mock) CreateVRF(ctx context.Context, v *models.VRF) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.vrfs {
		if strings.EqualFold(existing.Name, v.Name) {
			return fmt.Errorf("mock: create vrf: name %q: %w", v.Name, models.ErrConflict)
		}
	}
	if v.ID == "" {
		v.ID = newUUID()
	}
	now := time.Now().UTC()
	v.CreatedAt, v.UpdatedAt = now, now
	m.vrfs[v.ID] = v
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) UpdateVRF(ctx context.Context, v *models.VRF) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.vrfs[v.ID]; !ok {
		return fmt.Errorf("mock: update vrf %s: %w", v.ID, models.ErrNotFound)
	}
	v.UpdatedAt = time.Now().UTC()
	m.vrfs[v.ID] = v
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) DeleteVRF(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.vrfs[id]; !ok {
		return fmt.Errorf("mock: delete vrf %s: %w", id, models.ErrNotFound)
	}
	for _, p := range m.prefixes {
		if p.VRFID == id {
			return fmt.Errorf("mock: delete vrf %s: has prefixes: %w", id, models.ErrHasDependents)
		}
	}
	for _, s := range m.scopes {
		if s.VRFID == id {
			return fmt.Errorf("mock: delete vrf %s: has scopes: %w", id, models.ErrHasDependents)
		}
	}
	delete(m.vrfs, id)
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

// ---------------------------------------------------------------------------
// IPAMStore + IPAMAdminStore
// ---------------------------------------------------------------------------

func (m *Mock) GetPrefixByID(ctx context.Context, prefixID string) (*models.IPAMPrefix, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.prefixes[prefixID]
	if !ok {
		return nil, fmt.Errorf("mock: get prefix %s: %w", prefixID, models.ErrNotFound)
	}
	return p, nil
}

func (m *Mock) GetPrefixesByVRF(ctx context.Context, vrfID string) ([]*models.IPAMPrefix, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.IPAMPrefix
	for _, p := range m.prefixes {
		if p.VRFID == vrfID && p.ParentID == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *Mock) GetChildPrefixes(ctx context.Context, parentID string) ([]*models.IPAMPrefix, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.IPAMPrefix
	for _, p := range m.prefixes {
		if p.ParentID != nil && *p.ParentID == parentID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *Mock) GetAncestorPrefixes(ctx context.Context, prefixID string) ([]*models.IPAMPrefix, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.IPAMPrefix
	cur, ok := m.prefixes[prefixID]
	if !ok {
		return nil, fmt.Errorf("mock: get ancestors of %s: %w", prefixID, models.ErrNotFound)
	}
	for cur.ParentID != nil {
		parent, ok := m.prefixes[*cur.ParentID]
		if !ok {
			break
		}
		out = append(out, parent)
		cur = parent
	}
	// Root first, matching Postgres.GetAncestorPrefixes' ORDER BY depth DESC:
	// the result reads as a path from the top of the tree down.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (m *Mock) FindPrefixContaining(ctx context.Context, vrfID string, cidr *net.IPNet) (*models.IPAMPrefix, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *models.IPAMPrefix
	bestOnes := -1
	for _, p := range m.prefixes {
		if p.VRFID != vrfID || p.Prefix == nil {
			continue
		}
		pn := net.IPNet(*p.Prefix)
		if networksOverlap(&pn, cidr) && cidrContains(&pn, cidr) {
			ones, _ := pn.Mask.Size()
			if ones > bestOnes {
				bestOnes = ones
				best = p
			}
		}
	}
	return best, nil
}

func (m *Mock) ListPrefixesInCIDR(ctx context.Context, vrfID string, cidr *net.IPNet) ([]*models.IPAMPrefix, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.IPAMPrefix
	for _, p := range m.prefixes {
		if p.VRFID != vrfID || p.Prefix == nil {
			continue
		}
		pn := net.IPNet(*p.Prefix)
		if cidrContains(cidr, &pn) {
			out = append(out, p)
		}
	}
	return out, nil
}

// cidrContains reports whether outer fully contains inner (outer's network
// and broadcast bound inner's).
func cidrContains(outer, inner *net.IPNet) bool {
	if outer == nil || inner == nil {
		return false
	}
	outerOnes, _ := outer.Mask.Size()
	innerOnes, _ := inner.Mask.Size()
	if innerOnes < outerOnes {
		return false
	}
	return outer.Contains(inner.IP)
}

func (m *Mock) ListPrefixes(ctx context.Context, params ListParams) (ListResult[*models.IPAMPrefix], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vrfID := params.Filters["vrf_id"]
	ptype := params.Filters["type"]
	parentID := params.Filters["parent_id"]
	var items []*models.IPAMPrefix
	for _, p := range m.prefixes {
		if vrfID != "" && p.VRFID != vrfID {
			continue
		}
		if ptype != "" && string(p.PrefixType) != ptype {
			continue
		}
		if parentID == "root" {
			if p.ParentID != nil {
				continue
			}
		} else if parentID != "" && (p.ParentID == nil || *p.ParentID != parentID) {
			continue
		}
		if p.Name != nil && !matchStr(*p.Name, params.Search) {
			continue
		}
		items = append(items, p)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Prefix.String() < items[j].Prefix.String() })
	limit, offset := normalizeListParams(params)
	total := int64(len(items))
	return ListResult[*models.IPAMPrefix]{Items: paginate(items, limit, offset), Total: total, Limit: limit, Offset: offset}, nil
}

func (m *Mock) CreatePrefix(ctx context.Context, p *models.IPAMPrefix) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.Prefix == nil {
		return fmt.Errorf("mock: create prefix: %w", models.ErrInvalidInput)
	}
	if p.PrefixType == models.PrefixTypeSubnet {
		pn := net.IPNet(*p.Prefix)
		for _, existing := range m.prefixes {
			if existing.VRFID != p.VRFID || existing.PrefixType != models.PrefixTypeSubnet || existing.Prefix == nil {
				continue
			}
			en := net.IPNet(*existing.Prefix)
			if networksOverlap(&en, &pn) {
				return fmt.Errorf("mock: create prefix: overlaps %s: %w", en.String(), models.ErrConflict)
			}
		}
	}
	if p.ID == "" {
		p.ID = newUUID()
	}
	now := time.Now().UTC()
	p.CreatedAt, p.UpdatedAt = now, now
	m.prefixes[p.ID] = p
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) UpdatePrefix(ctx context.Context, p *models.IPAMPrefix) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.prefixes[p.ID]; !ok {
		return fmt.Errorf("mock: update prefix %s: %w", p.ID, models.ErrNotFound)
	}
	p.UpdatedAt = time.Now().UTC()
	m.prefixes[p.ID] = p
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) DeletePrefix(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.prefixes[id]; !ok {
		return fmt.Errorf("mock: delete prefix %s: %w", id, models.ErrNotFound)
	}
	for _, p := range m.prefixes {
		if p.ParentID != nil && *p.ParentID == id {
			return fmt.Errorf("mock: delete prefix %s: has children: %w", id, models.ErrHasDependents)
		}
	}
	delete(m.prefixes, id)
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) GetIPAMTree(ctx context.Context, vrfID string, parentID *string, depth int) ([]*models.IPAMTreeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if depth < 1 {
		depth = 1
	}
	var build func(pid *string, level int) []*models.IPAMTreeNode
	build = func(pid *string, level int) []*models.IPAMTreeNode {
		var nodes []*models.IPAMTreeNode
		for _, p := range m.prefixes {
			if p.VRFID != vrfID {
				continue
			}
			match := (pid == nil && p.ParentID == nil) || (pid != nil && p.ParentID != nil && *p.ParentID == *pid)
			if !match {
				continue
			}
			childCount := 0
			for _, c := range m.prefixes {
				if c.ParentID != nil && *c.ParentID == p.ID {
					childCount++
				}
			}
			var scopeID *string
			for _, sc := range m.scopes {
				if sc.IPAMPrefixID == p.ID {
					id := sc.ID
					scopeID = &id
					break
				}
			}
			node := &models.IPAMTreeNode{Prefix: p, ChildCount: childCount, HasScope: scopeID != nil, ScopeID: scopeID}
			if p.Prefix != nil {
				if own := cidrSize(net.IPNet(*p.Prefix)); own > 0 {
					var covered int64
					for _, c := range m.prefixes {
						if c.ParentID != nil && *c.ParentID == p.ID && c.Prefix != nil {
							covered += cidrSize(net.IPNet(*c.Prefix))
						}
					}
					node.SpacePercentage = float64(covered) / float64(own) * 100
				}
			}
			if level < depth-1 {
				node.Children = build(&p.ID, level+1)
			} else if childCount > 0 {
				node.ChildrenMore = true
			}
			nodes = append(nodes, node)
		}
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].Prefix.Prefix.String() < nodes[j].Prefix.Prefix.String() })
		return nodes
	}
	return build(parentID, 0), nil
}

// prefixAddressCount returns the number of addresses in n's CIDR block
// (network through broadcast, inclusive), mirroring the Postgres store's
// broadcast(prefix)-network(prefix)+1 arithmetic.
func prefixAddressCount(n *net.IPNet) int64 {
	network, broadcast := cidrNetworkAndBroadcast(n)
	diff := new(big.Int).Sub(ipToBigInt(broadcast), ipToBigInt(network))
	diff.Add(diff, big.NewInt(1))
	if !diff.IsInt64() {
		return math.MaxInt64
	}
	return diff.Int64()
}

// GetPrefixUtilization mirrors Postgres.GetPrefixUtilization: a DHCP view
// rolled up over every scope beneath the prefix, plus an address-space view
// of how much of its CIDR its direct children cover.
func (m *Mock) GetPrefixUtilization(ctx context.Context, prefixID string) (*models.PrefixUtilization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.prefixes[prefixID]
	if !ok || p.Prefix == nil {
		return nil, fmt.Errorf("mock: get prefix utilization %s: %w", prefixID, models.ErrNotFound)
	}

	pu := &models.PrefixUtilization{PrefixID: prefixID}

	// Address-space view: direct children only.
	pu.SpaceTotal = cidrSize(net.IPNet(*p.Prefix))
	for _, c := range m.prefixes {
		if c.ParentID == nil || *c.ParentID != prefixID || c.Prefix == nil {
			continue
		}
		pu.ChildPrefixCount++
		pu.SpaceAllocated += cidrSize(net.IPNet(*c.Prefix))
	}
	if pu.SpaceTotal > 0 {
		pu.SpacePercentage = float64(pu.SpaceAllocated) / float64(pu.SpaceTotal) * 100
	}

	// DHCP view: every scope on this prefix or any descendant.
	subtree := map[string]bool{prefixID: true}
	for changed := true; changed; {
		changed = false
		for _, c := range m.prefixes {
			if c.ParentID != nil && subtree[*c.ParentID] && !subtree[c.ID] {
				subtree[c.ID] = true
				changed = true
			}
		}
	}
	var total, active, reserved, reservedUnleased int64
	for _, sc := range m.scopes {
		if !subtree[sc.IPAMPrefixID] {
			continue
		}
		pu.ScopeCount++
		for _, pool := range m.pools {
			if pool.ScopeID != sc.ID {
				continue
			}
			t, a, r, ru := m.poolTotalsLocked(pool)
			total += t
			active += a
			reserved += r
			reservedUnleased += ru
		}
	}
	u := utilizationTotals{total: total, active: active, reservedTotal: reserved, reservedUnleased: reservedUnleased}.
		toUtilization(models.UtilizationEntityPrefix, prefixID)
	pu.Total, pu.Active, pu.Reserved, pu.Available = u.Total, u.Active, u.Reserved, u.Available
	pu.UtilizationPercentage = u.UtilizationPercentage
	return pu, nil
}

// cidrSize is the number of addresses a CIDR block covers, saturating at
// math.MaxInt64 for very large IPv6 blocks.
func cidrSize(n net.IPNet) int64 {
	ones, bits := n.Mask.Size()
	if exp := bits - ones; exp < 63 {
		return int64(1) << uint(exp)
	}
	return math.MaxInt64
}

func (m *Mock) NextAvailableSubnet(ctx context.Context, parentPrefixID string, prefixLen int) (*models.IPNet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	parent, ok := m.prefixes[parentPrefixID]
	if !ok || parent.Prefix == nil {
		return nil, fmt.Errorf("mock: next available subnet: parent %s: %w", parentPrefixID, models.ErrNotFound)
	}
	parentNet := net.IPNet(*parent.Prefix)
	parentOnes, bits := parentNet.Mask.Size()
	if prefixLen < parentOnes || prefixLen > bits {
		return nil, fmt.Errorf("mock: next available subnet: /%d not within parent: %w", prefixLen, models.ErrInvalidInput)
	}

	var used []*net.IPNet
	var collect func(id string)
	collect = func(id string) {
		for _, p := range m.prefixes {
			if p.ParentID != nil && *p.ParentID == id && p.Prefix != nil {
				n := net.IPNet(*p.Prefix)
				used = append(used, &n)
				collect(p.ID)
			}
		}
	}
	collect(parent.ID)

	blockSize := new(big.Int).Lsh(big.NewInt(1), uint(bits-prefixLen))
	numBlocks := new(big.Int).Lsh(big.NewInt(1), uint(prefixLen-parentOnes))
	base := ipToBigInt(parentNet.IP)
	candidate := new(big.Int).Set(base)
	for i := int64(0); i < numBlocks.Int64(); i++ {
		candNet := &net.IPNet{IP: bigIntToIP(candidate, len(parentNet.IP)), Mask: net.CIDRMask(prefixLen, bits)}
		if !overlapsAny(candNet, used) {
			n := models.IPNet(*candNet)
			return &n, nil
		}
		candidate.Add(candidate, blockSize)
	}
	return nil, fmt.Errorf("mock: next available subnet under %s: %w", parentPrefixID, models.ErrNoLeases)
}

// ---------------------------------------------------------------------------
// ScopeStore + ConfigStore scope reads
// ---------------------------------------------------------------------------

func (m *Mock) GetScopesByVRF(ctx context.Context, vrfID string) ([]*models.DHCPScope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.DHCPScope
	for _, s := range m.scopes {
		if s.VRFID == vrfID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *Mock) GetScopeByID(ctx context.Context, scopeID string) (*models.DHCPScope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.scopes[scopeID]
	if !ok {
		return nil, fmt.Errorf("mock: get scope %s: %w", scopeID, models.ErrNotFound)
	}
	return s, nil
}

func (m *Mock) FindScopeByGiaddr(ctx context.Context, giaddr net.IP) (*models.DHCPScope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.scopes {
		if s.Network == nil {
			continue
		}
		n := net.IPNet(*s.Network)
		if n.Contains(giaddr) {
			return s, nil
		}
	}
	return nil, fmt.Errorf("mock: find scope by giaddr %s: %w", giaddr, models.ErrNotFound)
}

func (m *Mock) FindScopeByNetwork(ctx context.Context, vrfID string, network *net.IPNet) (*models.DHCPScope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.scopes {
		if s.VRFID != vrfID || s.Network == nil {
			continue
		}
		n := net.IPNet(*s.Network)
		if n.String() == network.String() {
			return s, nil
		}
	}
	return nil, fmt.Errorf("mock: find scope by network %s: %w", network, models.ErrNotFound)
}

func (m *Mock) GetPoolsByScope(ctx context.Context, scopeID string) ([]*models.DHCPPool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.DHCPPool
	for _, p := range m.pools {
		if p.ScopeID == scopeID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority > out[j].Priority })
	return out, nil
}

func (m *Mock) ListScopes(ctx context.Context, params ListParams) (ListResult[*models.DHCPScope], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vrfID := params.Filters["vrf_id"]
	clusterID := params.Filters["cluster_id"]
	prefixID := params.Filters["ipam_prefix_id"]
	var items []*models.DHCPScope
	for _, s := range m.scopes {
		if vrfID != "" && s.VRFID != vrfID {
			continue
		}
		if clusterID != "" && s.ClusterID != clusterID {
			continue
		}
		if prefixID != "" && s.IPAMPrefixID != prefixID {
			continue
		}
		if !matchStr(s.Name, params.Search) {
			continue
		}
		items = append(items, s)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	limit, offset := normalizeListParams(params)
	total := int64(len(items))
	return ListResult[*models.DHCPScope]{Items: paginate(items, limit, offset), Total: total, Limit: limit, Offset: offset}, nil
}

func (m *Mock) CreateScope(ctx context.Context, s *models.DHCPScope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.ID == "" {
		s.ID = newUUID()
	}
	now := time.Now().UTC()
	s.CreatedAt, s.UpdatedAt = now, now
	m.scopes[s.ID] = s
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) UpdateScope(ctx context.Context, s *models.DHCPScope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.scopes[s.ID]; !ok {
		return fmt.Errorf("mock: update scope %s: %w", s.ID, models.ErrNotFound)
	}
	s.UpdatedAt = time.Now().UTC()
	m.scopes[s.ID] = s
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) DeleteScope(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.scopes[id]; !ok {
		return fmt.Errorf("mock: delete scope %s: %w", id, models.ErrNotFound)
	}
	for poolID, p := range m.pools {
		if p.ScopeID == id {
			for leaseID, l := range m.leases {
				if l.PoolID == poolID {
					delete(m.leases, leaseID)
				}
			}
			delete(m.pools, poolID)
		}
	}
	delete(m.scopes, id)
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) GetScopeUtilization(ctx context.Context, scopeID string) (*models.Utilization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.scopes[scopeID]; !ok {
		return nil, fmt.Errorf("mock: get scope utilization %s: %w", scopeID, models.ErrNotFound)
	}
	var total, active, reserved, reservedUnleased int64
	for _, p := range m.pools {
		if p.ScopeID != scopeID {
			continue
		}
		t, a, r, ru := m.poolTotalsLocked(p)
		total += t
		active += a
		reserved += r
		reservedUnleased += ru
	}
	return utilizationTotals{total: total, active: active, reservedTotal: reserved, reservedUnleased: reservedUnleased}.
		toUtilization(models.UtilizationEntityScope, scopeID), nil
}

// ---------------------------------------------------------------------------
// PoolStore + ConfigStore pool reads
// ---------------------------------------------------------------------------

func (m *Mock) GetPoolByID(ctx context.Context, poolID string) (*models.DHCPPool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pools[poolID]
	if !ok {
		return nil, fmt.Errorf("mock: get pool %s: %w", poolID, models.ErrNotFound)
	}
	return p, nil
}

func (m *Mock) CanAllocateInPool(ctx context.Context, poolID string, ip net.IP) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.canAllocateInPoolLocked(poolID, ip)
}

func (m *Mock) canAllocateInPoolLocked(poolID string, ip net.IP) (bool, error) {
	pool, ok := m.pools[poolID]
	if !ok {
		return false, fmt.Errorf("mock: can allocate in pool %s: %w", poolID, models.ErrNotFound)
	}
	if !ipInRange(ip, pool.RangeStart, pool.RangeEnd) {
		return false, nil
	}
	if scope, ok := m.scopes[pool.ScopeID]; ok && scope.Network != nil {
		n := net.IPNet(*scope.Network)
		netAddr, bcastAddr := cidrNetworkAndBroadcast(&n)
		if ip.Equal(netAddr) || ip.Equal(bcastAddr) {
			return false, nil
		}
	}
	for _, l := range m.leases {
		if l.PoolID == poolID && l.IPAddress.Equal(ip) && l.BindingState == models.LeaseStateActive {
			return false, nil
		}
	}
	for _, r := range m.reservations {
		if r.PoolID == poolID && r.ReservedIP.Equal(ip) && r.IsEnabled {
			return false, nil
		}
	}
	return true, nil
}

func (m *Mock) ListPoolsByScope(ctx context.Context, scopeID string, params ListParams) (ListResult[*models.DHCPPool], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*models.DHCPPool
	for _, p := range m.pools {
		if p.ScopeID == scopeID && matchStr(p.Name, params.Search) {
			items = append(items, p)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Priority > items[j].Priority })
	limit, offset := normalizeListParams(params)
	total := int64(len(items))
	return ListResult[*models.DHCPPool]{Items: paginate(items, limit, offset), Total: total, Limit: limit, Offset: offset}, nil
}

func (m *Mock) CreatePool(ctx context.Context, p *models.DHCPPool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.ID == "" {
		p.ID = newUUID()
	}
	now := time.Now().UTC()
	p.CreatedAt, p.UpdatedAt = now, now
	m.pools[p.ID] = p
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) UpdatePool(ctx context.Context, p *models.DHCPPool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pools[p.ID]; !ok {
		return fmt.Errorf("mock: update pool %s: %w", p.ID, models.ErrNotFound)
	}
	p.UpdatedAt = time.Now().UTC()
	m.pools[p.ID] = p
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) DeletePool(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pools[id]; !ok {
		return fmt.Errorf("mock: delete pool %s: %w", id, models.ErrNotFound)
	}
	for leaseID, l := range m.leases {
		if l.PoolID == id {
			delete(m.leases, leaseID)
		}
	}
	delete(m.pools, id)
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

// poolTotalsLocked returns (total, active, reservedTotal, reservedUnleased)
// for pool, matching amendment A6's candidacy test. Caller holds m.mu.
func (m *Mock) poolTotalsLocked(pool *models.DHCPPool) (total, active, reservedTotal, reservedUnleased int64) {
	total = poolTotalAddresses(pool, m.scopes[pool.ScopeID])
	for _, l := range m.leases {
		if l.PoolID == pool.ID && l.BindingState == models.LeaseStateActive {
			active++
		}
	}
	for _, r := range m.reservations {
		if r.PoolID != pool.ID || !r.IsEnabled {
			continue
		}
		reservedTotal++
		leased := false
		for _, l := range m.leases {
			if l.PoolID == pool.ID && l.IPAddress.Equal(r.ReservedIP) && l.BindingState == models.LeaseStateActive {
				leased = true
				break
			}
		}
		if !leased {
			reservedUnleased++
		}
	}
	return
}

func (m *Mock) GetPoolUtilization(ctx context.Context, poolID string) (*models.Utilization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pool, ok := m.pools[poolID]
	if !ok {
		return nil, fmt.Errorf("mock: get pool utilization %s: %w", poolID, models.ErrNotFound)
	}
	t, a, r, ru := m.poolTotalsLocked(pool)
	return utilizationTotals{total: t, active: a, reservedTotal: r, reservedUnleased: ru}.
		toUtilization(models.UtilizationEntityPool, poolID), nil
}

// ---------------------------------------------------------------------------
// ReservationStore
// ---------------------------------------------------------------------------

func (m *Mock) ListReservationsByPool(ctx context.Context, poolID string, params ListParams) (ListResult[*models.DHCPReservation], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*models.DHCPReservation
	for _, r := range m.reservations {
		if r.PoolID == poolID {
			items = append(items, r)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ReservedIP.String() < items[j].ReservedIP.String() })
	limit, offset := normalizeListParams(params)
	total := int64(len(items))
	return ListResult[*models.DHCPReservation]{Items: paginate(items, limit, offset), Total: total, Limit: limit, Offset: offset}, nil
}

func (m *Mock) GetReservation(ctx context.Context, id string) (*models.DHCPReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.reservations[id]
	if !ok {
		return nil, fmt.Errorf("mock: get reservation %s: %w", id, models.ErrNotFound)
	}
	return r, nil
}

// ReservationByBindingKey looks up a reservation by pool and binding key
// (amendment A19: the handler needs to check if a client is already pinned).
func (m *Mock) ReservationByBindingKey(ctx context.Context, poolID, bindingKey string) (*models.DHCPReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.reservations {
		if r.PoolID == poolID && r.BindingKey == bindingKey {
			return r, nil
		}
	}
	return nil, nil
}

// ReservationByIP looks up a reservation by pool and reserved IP (amendment
// A19: the handler needs to check if an address is already reserved).
func (m *Mock) ReservationByIP(ctx context.Context, poolID string, ip net.IP) (*models.DHCPReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.reservations {
		if r.PoolID == poolID && r.ReservedIP.Equal(ip) {
			return r, nil
		}
	}
	return nil, nil
}

func (m *Mock) CreateReservation(ctx context.Context, r *models.DHCPReservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !r.BindingType.Valid() {
		return fmt.Errorf("mock: create reservation: invalid binding_type %q: %w", r.BindingType, models.ErrInvalidInput)
	}
	// Validate binding_key is canonical form (type:value).
	if !models.IsCanonicalBindingKey(r.BindingKey) {
		return fmt.Errorf("mock: create reservation: non-canonical binding_key %q (must be 'type:value'): %w", r.BindingKey, models.ErrInvalidInput)
	}
	pool, ok := m.pools[r.PoolID]
	if !ok {
		return fmt.Errorf("mock: create reservation: pool %s: %w", r.PoolID, models.ErrNotFound)
	}
	if !ipInRange(r.ReservedIP, pool.RangeStart, pool.RangeEnd) {
		return fmt.Errorf("mock: create reservation: reserved_ip %s outside pool range: %w", r.ReservedIP, models.ErrInvalidInput)
	}
	// Check for duplicate binding key (UNIQUE (pool_id, binding_key), amendment A19).
	for _, existing := range m.reservations {
		if existing.PoolID == r.PoolID && existing.BindingKey == r.BindingKey {
			return fmt.Errorf("mock: create reservation: binding key %q: %w: %w", r.BindingKey, models.ErrReservationClientExists, models.ErrConflict)
		}
	}
	// Check if the IP is already reserved for a different client (amendment A19).
	for _, existing := range m.reservations {
		if existing.PoolID == r.PoolID && existing.ReservedIP.Equal(r.ReservedIP) && existing.BindingKey != r.BindingKey {
			return fmt.Errorf("mock: create reservation: ip %s: %w: %w", r.ReservedIP, models.ErrReservationIPTaken, models.ErrConflict)
		}
	}
	// Check if the IP already has an active lease for a different client (amendment A19).
	for _, l := range m.leases {
		if l.PoolID != r.PoolID || l.BindingState != models.LeaseStateActive || !l.IPAddress.Equal(r.ReservedIP) {
			continue
		}
		var macStr, clientIDStr string
		if l.ClientMAC != nil {
			macStr = l.ClientMAC.String()
		}
		clientIDStr = encodeClientID(l.ClientID)
		sameClient := false
		for _, key := range leaseIdentity(macStr, clientIDStr, l.ClientHostname).BindingKeys() {
			if key == r.BindingKey {
				sameClient = true
				break
			}
		}
		if !sameClient {
			return fmt.Errorf("mock: create reservation: ip %s: %w: %w", r.ReservedIP, models.ErrReservationIPLeased, models.ErrConflict)
		}
	}
	if r.ID == "" {
		r.ID = newUUID()
	}
	now := time.Now().UTC()
	r.CreatedAt, r.UpdatedAt = now, now
	m.reservations[r.ID] = r
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) UpdateReservation(ctx context.Context, r *models.DHCPReservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !r.BindingType.Valid() {
		return fmt.Errorf("mock: update reservation: invalid binding_type %q: %w", r.BindingType, models.ErrInvalidInput)
	}
	// Validate binding_key is canonical form (type:value).
	if !models.IsCanonicalBindingKey(r.BindingKey) {
		return fmt.Errorf("mock: update reservation: non-canonical binding_key %q (must be 'type:value'): %w", r.BindingKey, models.ErrInvalidInput)
	}
	existing, ok := m.reservations[r.ID]
	if !ok {
		return fmt.Errorf("mock: update reservation %s: %w", r.ID, models.ErrNotFound)
	}
	// If binding key changed, check for conflicts (but not against this row itself, amendment A19).
	if existing.BindingKey != r.BindingKey {
		for _, res := range m.reservations {
			if res.ID != r.ID && res.PoolID == r.PoolID && res.BindingKey == r.BindingKey {
				return fmt.Errorf("mock: update reservation: binding key %q: %w: %w", r.BindingKey, models.ErrReservationClientExists, models.ErrConflict)
			}
		}
	}
	// If reserved IP changed, check that no other client reserves it and no different
	// client has an active lease on it (amendment A19).
	if !existing.ReservedIP.Equal(r.ReservedIP) {
		for _, res := range m.reservations {
			if res.ID != r.ID && res.PoolID == r.PoolID && res.ReservedIP.Equal(r.ReservedIP) && res.BindingKey != r.BindingKey {
				return fmt.Errorf("mock: update reservation: ip %s: %w: %w", r.ReservedIP, models.ErrReservationIPTaken, models.ErrConflict)
			}
		}
		for _, l := range m.leases {
			if l.PoolID != r.PoolID || l.BindingState != models.LeaseStateActive || !l.IPAddress.Equal(r.ReservedIP) {
				continue
			}
			var macStr, clientIDStr string
			if l.ClientMAC != nil {
				macStr = l.ClientMAC.String()
			}
			clientIDStr = encodeClientID(l.ClientID)
			sameClient := false
			for _, key := range leaseIdentity(macStr, clientIDStr, l.ClientHostname).BindingKeys() {
				if key == r.BindingKey {
					sameClient = true
					break
				}
			}
			if !sameClient {
				return fmt.Errorf("mock: update reservation: ip %s: %w: %w", r.ReservedIP, models.ErrReservationIPLeased, models.ErrConflict)
			}
		}
	}
	r.UpdatedAt = time.Now().UTC()
	m.reservations[r.ID] = r
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) DeleteReservation(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.reservations[id]; !ok {
		return fmt.Errorf("mock: delete reservation %s: %w", id, models.ErrNotFound)
	}
	delete(m.reservations, id)
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

// ---------------------------------------------------------------------------
// LeaseStore + LeaseQueryStore + AllocationHelperStore.NextAvailableIP
// ---------------------------------------------------------------------------

func (m *Mock) reservationForKeysLocked(poolID string, keys []string) *models.DHCPReservation {
	for _, key := range keys {
		for _, r := range m.reservations {
			if r.PoolID == poolID && r.IsEnabled && r.BindingKey == key {
				return r
			}
		}
	}
	return nil
}

// leaseReservationLocked finds the one reservation that concerns a lease,
// following amendment A19's precedence: prefer a reservation matching both
// the client and the address (enabled takes precedence), then one matching
// only the client, then one sitting on the address (amendment A19).
func (m *Mock) leaseReservationLocked(lease *models.DHCPLease) *models.LeaseReservation {
	var macStr, clientIDStr string
	if lease.ClientMAC != nil {
		macStr = lease.ClientMAC.String()
	}
	clientIDStr = encodeClientID(lease.ClientID)
	clientKeys := leaseIdentity(macStr, clientIDStr, lease.ClientHostname).BindingKeys()

	// Try to find a matching client in enabled, then disabled.
	for _, key := range clientKeys {
		for _, r := range m.reservations {
			if r.PoolID != lease.PoolID || r.BindingKey != key {
				continue
			}
			if r.IsEnabled {
				return &models.LeaseReservation{
					ID:             r.ID,
					ReservedIP:     r.ReservedIP,
					BindingKey:     r.BindingKey,
					BindingType:    r.BindingType,
					IsEnabled:      r.IsEnabled,
					MatchesClient:  true,
					MatchesAddress: r.ReservedIP.Equal(lease.IPAddress),
				}
			}
		}
	}
	// Second pass for disabled client matches.
	for _, key := range clientKeys {
		for _, r := range m.reservations {
			if r.PoolID != lease.PoolID || r.BindingKey != key {
				continue
			}
			return &models.LeaseReservation{
				ID:             r.ID,
				ReservedIP:     r.ReservedIP,
				BindingKey:     r.BindingKey,
				BindingType:    r.BindingType,
				IsEnabled:      r.IsEnabled,
				MatchesClient:  true,
				MatchesAddress: r.ReservedIP.Equal(lease.IPAddress),
			}
		}
	}
	// Finally, check if this address is reserved for anyone (enabled preferred).
	var candidateDisabled *models.LeaseReservation
	for _, r := range m.reservations {
		if r.PoolID != lease.PoolID || !r.ReservedIP.Equal(lease.IPAddress) {
			continue
		}
		lr := &models.LeaseReservation{
			ID:             r.ID,
			ReservedIP:     r.ReservedIP,
			BindingKey:     r.BindingKey,
			BindingType:    r.BindingType,
			IsEnabled:      r.IsEnabled,
			MatchesClient:  false,
			MatchesAddress: true,
		}
		if r.IsEnabled {
			return lr
		}
		if candidateDisabled == nil {
			candidateDisabled = lr
		}
	}
	return candidateDisabled
}

func (m *Mock) mostRecentClientLeaseLocked(poolID, clientMAC, clientID string) *models.DHCPLease {
	cidBytes := decodeClientID(clientID)
	var best *models.DHCPLease
	for _, l := range m.leases {
		if l.PoolID != poolID {
			continue
		}
		matches := (clientMAC != "" && macEquals(l.ClientMAC, clientMAC)) ||
			(len(cidBytes) > 0 && string(l.ClientID) == string(cidBytes))
		if !matches {
			continue
		}
		if best == nil || l.UpdatedAt.After(best.UpdatedAt) {
			best = l
		}
	}
	return best
}

// claimLeaseIPLocked mirrors postgres_lease.go's claimLeaseIP: create the
// lease row if none exists for (poolID, ip), or reclaim it in place if it's
// expired/released/this same client's own active lease.
func (m *Mock) claimLeaseIPLocked(poolID string, ip net.IP, clientMAC, clientID string, hostname *string, start, end, renewal, rebind time.Time) (*models.DHCPLease, bool) {
	for _, l := range m.leases {
		if l.PoolID != poolID || !l.IPAddress.Equal(ip) {
			continue
		}
		sameClient := macEquals(l.ClientMAC, clientMAC) && string(l.ClientID) == string(decodeClientID(clientID))
		if l.BindingState == models.LeaseStateActive && !sameClient {
			return nil, false
		}
		if l.BindingState == models.LeaseStateAbandoned {
			// Mirrors postgres claimLeaseIP: only 'expired'/'released' (or
			// an active lease's own client) are reclaimable. An abandoned
			// row stays unclaimable until ExpireLeasesOlderThan sweeps it,
			// so a client's own ping-check retry can't immediately reclaim
			// the exact address it just found in use.
			return nil, false
		}
		l.ClientMAC = macPtr(clientMAC)
		l.ClientID = decodeClientID(clientID)
		l.ClientHostname = hostname
		l.BindingState = models.LeaseStateActive
		l.LeaseStart, l.LeaseEnd, l.RenewalTime, l.RebindingTime = start, end, &renewal, &rebind
		l.UpdatedAt = time.Now().UTC()
		return l, true
	}
	now := time.Now().UTC()
	lease := &models.DHCPLease{
		ID: newUUID(), PoolID: poolID, IPAddress: ip,
		ClientMAC: macPtr(clientMAC), ClientID: decodeClientID(clientID), ClientHostname: hostname,
		BindingState: models.LeaseStateActive,
		LeaseStart:   start, LeaseEnd: end, RenewalTime: &renewal, RebindingTime: &rebind,
		CreatedAt: now, UpdatedAt: now,
	}
	m.leases[lease.ID] = lease
	return lease, true
}

func (m *Mock) AllocateLease(ctx context.Context, poolID string, ident models.ClientIdentity, hostname *string) (*models.DHCPLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.pools[poolID]
	if !ok {
		return nil, fmt.Errorf("mock: allocate lease: pool %s: %w", poolID, models.ErrNotFound)
	}
	if ident.MAC != "" {
		if _, err := net.ParseMAC(ident.MAC); err != nil {
			return nil, fmt.Errorf("mock: allocate lease: invalid client MAC %q: %w", ident.MAC, models.ErrInvalidInput)
		}
	}

	now := time.Now().UTC()
	leaseTime := 24 * time.Hour
	end := now.Add(leaseTime)
	renewal := now.Add(leaseTime / 2)
	rebind := now.Add(leaseTime * 7 / 8)

	// 1. Reservation match using multi-identifier precedence order.
	if res := m.reservationForKeysLocked(poolID, ident.BindingKeys()); res != nil {
		// Preference: use client-provided hostname, fall back to reservation's.
		allocHostname := hostname
		if allocHostname == nil {
			allocHostname = res.Hostname
		}
		if lease, claimed := m.claimLeaseIPLocked(poolID, res.ReservedIP, ident.MAC, ident.ClientID, allocHostname, now, end, renewal, rebind); claimed {
			return lease, nil
		}
		return nil, fmt.Errorf("mock: allocate lease in pool %s: %w", poolID, models.ErrNoLeases)
	}

	// 2. Client renew affinity.
	if existing := m.mostRecentClientLeaseLocked(poolID, ident.MAC, ident.ClientID); existing != nil {
		ok, _ := m.canAllocateInPoolLocked(poolID, existing.IPAddress)
		if ok || existing.BindingState == models.LeaseStateActive {
			// Preference: use client-provided hostname, fall back to existing lease's.
			renewHostname := hostname
			if renewHostname == nil {
				renewHostname = existing.ClientHostname
			}
			if lease, claimed := m.claimLeaseIPLocked(poolID, existing.IPAddress, ident.MAC, ident.ClientID, renewHostname, now, end, renewal, rebind); claimed {
				return lease, nil
			}
		}
	}

	// 3. Scan candidates in ascending address order.
	start := ipToBigInt(pool.RangeStart)
	stop := ipToBigInt(pool.RangeEnd)
	byteLen := 4
	if pool.RangeStart.To4() == nil {
		byteLen = 16
	}
	for cur := new(big.Int).Set(start); cur.Cmp(stop) <= 0; cur.Add(cur, big.NewInt(1)) {
		candidate := bigIntToIP(cur, byteLen)
		ok, _ := m.canAllocateInPoolLocked(poolID, candidate)
		if !ok {
			continue
		}
		if lease, claimed := m.claimLeaseIPLocked(poolID, candidate, ident.MAC, ident.ClientID, hostname, now, end, renewal, rebind); claimed {
			return lease, nil
		}
	}

	return nil, fmt.Errorf("mock: allocate lease in pool %s: %w", poolID, models.ErrNoLeases)
}

func (m *Mock) RenewLease(ctx context.Context, leaseID string, clientMAC string, newExpiry time.Time, hostname *string) (*models.DHCPLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[leaseID]
	if !ok || l.BindingState != models.LeaseStateActive {
		return nil, fmt.Errorf("mock: renew lease %s: %w", leaseID, models.ErrLeaseNotFound)
	}
	if clientMAC != "" && !macEquals(l.ClientMAC, clientMAC) {
		return nil, fmt.Errorf("mock: renew lease %s: %w", leaseID, models.ErrLeaseNotFound)
	}
	now := time.Now().UTC()
	if !newExpiry.After(now) {
		return nil, fmt.Errorf("mock: renew lease %s: new expiry not in the future: %w", leaseID, models.ErrInvalidInput)
	}
	duration := newExpiry.Sub(now)
	renewal := now.Add(duration / 2)
	rebind := now.Add(duration * 7 / 8)
	l.LeaseStart, l.LeaseEnd, l.RenewalTime, l.RebindingTime = now, newExpiry, &renewal, &rebind
	l.UpdatedAt = now
	return l, nil
}

func (m *Mock) ReleaseLease(ctx context.Context, leaseID string, clientMAC string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[leaseID]
	if !ok {
		return fmt.Errorf("mock: release lease %s: %w", leaseID, models.ErrLeaseNotFound)
	}
	if l.BindingState != models.LeaseStateActive {
		return nil // idempotent no-op
	}
	if clientMAC != "" && !macEquals(l.ClientMAC, clientMAC) {
		return fmt.Errorf("mock: release lease %s: %w", leaseID, models.ErrLeaseNotFound)
	}
	l.BindingState = models.LeaseStateReleased
	l.UpdatedAt = time.Now().UTC()
	return nil
}

func (m *Mock) AbandonLease(ctx context.Context, leaseID string, clientMAC string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[leaseID]
	if !ok {
		return fmt.Errorf("mock: abandon lease %s: %w", leaseID, models.ErrLeaseNotFound)
	}
	if l.BindingState != models.LeaseStateActive {
		return nil // idempotent no-op
	}
	if clientMAC != "" && !macEquals(l.ClientMAC, clientMAC) {
		return fmt.Errorf("mock: abandon lease %s: %w", leaseID, models.ErrLeaseNotFound)
	}
	l.BindingState = models.LeaseStateAbandoned
	l.UpdatedAt = time.Now().UTC()
	return nil
}

// GetLeaseByIP is an allocation hot-path method (amendment A6), so it is not
// annotated with ReservationState/Reservation per amendment A19; those leases
// never reach JSON.
func (m *Mock) GetLeaseByIP(ctx context.Context, poolID string, ip net.IP) (*models.DHCPLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range m.leases {
		if l.PoolID == poolID && l.IPAddress.Equal(ip) && l.BindingState == models.LeaseStateActive {
			return l, nil
		}
	}
	return nil, nil
}

// GetLeaseByClient is an allocation hot-path method (amendment A6), so it is
// not annotated with ReservationState/Reservation per amendment A19; those
// leases never reach JSON.
func (m *Mock) GetLeaseByClient(ctx context.Context, ident models.ClientIdentity) (*models.DHCPLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cidBytes := decodeClientID(ident.ClientID)
	var best *models.DHCPLease
	for _, l := range m.leases {
		if l.BindingState != models.LeaseStateActive {
			continue
		}
		matches := (ident.MAC != "" && macEquals(l.ClientMAC, ident.MAC)) || (len(cidBytes) > 0 && string(l.ClientID) == string(cidBytes))
		if matches && (best == nil || l.UpdatedAt.After(best.UpdatedAt)) {
			best = l
		}
	}
	return best, nil
}

func (m *Mock) leasesMatching(pred func(*models.DHCPLease) bool) []*models.DHCPLease {
	var out []*models.DHCPLease
	for _, l := range m.leases {
		if pred(l) {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IPAddress.String() < out[j].IPAddress.String() })
	return out
}

func (m *Mock) GetLeasesByPool(ctx context.Context, poolID string, state string, offset, limit int) ([]*models.DHCPLease, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.leasesMatching(func(l *models.DHCPLease) bool {
		return l.PoolID == poolID && (state == "" || string(l.BindingState) == state)
	})
	// Annotate with reservation state, per amendment A19.
	for _, l := range items {
		l.Reservation = m.leaseReservationLocked(l)
		l.ReservationState = models.ClassifyLeaseReservation(l.Reservation)
	}
	total := int64(len(items))
	if limit <= 0 {
		limit = defaultListLimit
	}
	return paginate(items, limit, offset), total, nil
}

func (m *Mock) ExpireLeasesOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, l := range m.leases {
		reclaimable := l.BindingState == models.LeaseStateActive ||
			l.BindingState == models.LeaseStateReleased ||
			l.BindingState == models.LeaseStateAbandoned
		if l.LeaseEnd.Before(cutoff) && reclaimable {
			if l.BindingState == models.LeaseStateActive {
				n++
			}
			l.BindingState = models.LeaseStateExpired
			l.UpdatedAt = time.Now().UTC()
		}
	}
	return n, nil
}

func (m *Mock) ReservationForLease(ctx context.Context, poolID string, bindingKey string) (*models.DHCPReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.reservations {
		if r.PoolID == poolID && r.BindingKey == bindingKey {
			return r, nil
		}
	}
	return nil, nil
}

func (m *Mock) ReservationsForKeys(ctx context.Context, poolID string, keys []string) (*models.DHCPReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reservationForKeysLocked(poolID, keys), nil
}

func (m *Mock) ListLeases(ctx context.Context, params ListParams) (ListResult[*models.DHCPLease], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	scopeID := params.Filters["scope_id"]
	poolID := params.Filters["pool_id"]
	state := params.Filters["state"]
	search := strings.ToLower(strings.TrimSpace(params.Search))
	items := m.leasesMatching(func(l *models.DHCPLease) bool {
		if poolID != "" && l.PoolID != poolID {
			return false
		}
		if scopeID != "" {
			pool, ok := m.pools[l.PoolID]
			if !ok || pool.ScopeID != scopeID {
				return false
			}
		}
		if state != "" && string(l.BindingState) != state {
			return false
		}
		return search == "" || leaseMatchesSearch(l, search)
	})
	// Annotate with reservation state, per amendment A19.
	for _, l := range items {
		l.Reservation = m.leaseReservationLocked(l)
		l.ReservationState = models.ClassifyLeaseReservation(l.Reservation)
	}
	limit, offset := normalizeListParams(params)
	total := int64(len(items))
	return ListResult[*models.DHCPLease]{Items: paginate(items, limit, offset), Total: total, Limit: limit, Offset: offset}, nil
}

// leaseMatchesSearch mirrors Postgres.ListLeases' search predicate: the one
// query string is tried against IP, MAC and hostname so callers need not
// know which field a term belongs to. needle must already be lowercased.
func leaseMatchesSearch(l *models.DHCPLease, needle string) bool {
	if strings.Contains(strings.ToLower(l.IPAddress.String()), needle) {
		return true
	}
	if l.ClientMAC != nil && strings.Contains(strings.ToLower(l.ClientMAC.String()), needle) {
		return true
	}
	return l.ClientHostname != nil && strings.Contains(strings.ToLower(*l.ClientHostname), needle)
}

// DeleteLease implements LeaseAdminStore.
func (m *Mock) DeleteLease(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.leases[id]; !ok {
		return fmt.Errorf("mock: delete lease %s: %w", id, models.ErrLeaseNotFound)
	}
	delete(m.leases, id)
	return nil
}

// ForceRenewLease implements LeaseAdminStore.
func (m *Mock) ForceRenewLease(ctx context.Context, id string, expiry time.Time) (*models.DHCPLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[id]
	if !ok {
		return nil, fmt.Errorf("mock: force renew lease %s: %w", id, models.ErrLeaseNotFound)
	}
	now := time.Now().UTC()
	if !expiry.After(now) {
		return nil, fmt.Errorf("mock: force renew lease %s: new expiry not in the future: %w", id, models.ErrInvalidInput)
	}
	duration := expiry.Sub(now)
	renewal := now.Add(duration / 2)
	rebind := now.Add(duration * 7 / 8)
	l.LeaseStart = now
	l.LeaseEnd = expiry
	l.RenewalTime = &renewal
	l.RebindingTime = &rebind
	l.BindingState = models.LeaseStateActive
	l.UpdatedAt = now
	return l, nil
}

func (m *Mock) GetLease(ctx context.Context, id string) (*models.DHCPLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[id]
	if !ok {
		return nil, fmt.Errorf("mock: get lease %s: %w", id, models.ErrNotFound)
	}
	// Annotate with reservation state, per amendment A19.
	l.Reservation = m.leaseReservationLocked(l)
	l.ReservationState = models.ClassifyLeaseReservation(l.Reservation)
	return l, nil
}

func (m *Mock) ListLeasesByPool(ctx context.Context, poolID string, params ListParams) (ListResult[*models.DHCPLease], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := params.Filters["state"]
	search := strings.ToLower(strings.TrimSpace(params.Search))
	items := m.leasesMatching(func(l *models.DHCPLease) bool {
		if l.PoolID != poolID {
			return false
		}
		if state != "" && string(l.BindingState) != state {
			return false
		}
		return search == "" || leaseMatchesSearch(l, search)
	})
	// Annotate with reservation state, per amendment A19.
	for _, l := range items {
		l.Reservation = m.leaseReservationLocked(l)
		l.ReservationState = models.ClassifyLeaseReservation(l.Reservation)
	}
	limit, offset := normalizeListParams(params)
	total := int64(len(items))
	return ListResult[*models.DHCPLease]{Items: paginate(items, limit, offset), Total: total, Limit: limit, Offset: offset}, nil
}

func (m *Mock) GetLeaseByIPGlobal(ctx context.Context, ip net.IP) (*models.DHCPLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *models.DHCPLease
	for _, l := range m.leases {
		if l.IPAddress.Equal(ip) && l.BindingState == models.LeaseStateActive {
			if best == nil || l.UpdatedAt.After(best.UpdatedAt) {
				best = l
			}
		}
	}
	if best != nil {
		// Annotate with reservation state, per amendment A19.
		best.Reservation = m.leaseReservationLocked(best)
		best.ReservationState = models.ClassifyLeaseReservation(best.Reservation)
	}
	return best, nil
}

func (m *Mock) ListLeasesByMAC(ctx context.Context, mac string) ([]*models.DHCPLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := net.ParseMAC(mac); err != nil {
		return nil, fmt.Errorf("mock: list leases by mac: invalid MAC %q: %w", mac, models.ErrInvalidInput)
	}
	items := m.leasesMatching(func(l *models.DHCPLease) bool { return macEquals(l.ClientMAC, mac) })
	// Annotate with reservation state, per amendment A19.
	for _, l := range items {
		l.Reservation = m.leaseReservationLocked(l)
		l.ReservationState = models.ClassifyLeaseReservation(l.Reservation)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (m *Mock) NextAvailableIP(ctx context.Context, poolID string) (net.IP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pool, ok := m.pools[poolID]
	if !ok {
		return nil, fmt.Errorf("mock: next available ip: pool %s: %w", poolID, models.ErrNotFound)
	}
	start := ipToBigInt(pool.RangeStart)
	stop := ipToBigInt(pool.RangeEnd)
	byteLen := 4
	if pool.RangeStart.To4() == nil {
		byteLen = 16
	}
	for cur := new(big.Int).Set(start); cur.Cmp(stop) <= 0; cur.Add(cur, big.NewInt(1)) {
		candidate := bigIntToIP(cur, byteLen)
		if ok, _ := m.canAllocateInPoolLocked(poolID, candidate); ok {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("mock: next available ip in pool %s: %w", poolID, models.ErrNoLeases)
}

// ---------------------------------------------------------------------------
// ConfigStore options + OptionCatalogStore + OptionValueStore
// ---------------------------------------------------------------------------

func (m *Mock) optionsAtLevel(level models.OptionScopeLevel, entityID string) []*models.DHCPOption {
	// Initialized (not nil) so a zero-row result marshals to `[]`, not
	// `null` — see postgres_config.go's optionsAtLevel for why that matters.
	out := []*models.DHCPOption{}
	for _, o := range m.optionValues {
		if o.ScopeLevel == level && o.ScopeEntityID == entityID {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OptionCode < out[j].OptionCode })
	return out
}

func (m *Mock) GetOptionsByScope(ctx context.Context, scopeID string) ([]*models.DHCPOption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.optionsAtLevel(models.OptionScopeScope, scopeID), nil
}

func (m *Mock) GetOptionsByPool(ctx context.Context, poolID string) ([]*models.DHCPOption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.optionsAtLevel(models.OptionScopePool, poolID), nil
}

func (m *Mock) GetOptionsByReservation(ctx context.Context, reservationID string) ([]*models.DHCPOption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.optionsAtLevel(models.OptionScopeReservation, reservationID), nil
}

func (m *Mock) GetOptionsByTemplate(ctx context.Context, templateID string) ([]*models.DHCPOption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.optionsAtLevel(models.OptionScopeTemplate, templateID), nil
}

func (m *Mock) GetGlobalOptions(ctx context.Context) ([]*models.DHCPOption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.optionsAtLevel(models.OptionScopeGlobal, globalScopeEntityID), nil
}

// orderedTemplateIDsLocked returns template IDs applicable to scopeID/poolID
// ordered by association priority descending, matching
// postgres_config.go's orderedTemplateIDs.
func (m *Mock) orderedTemplateIDsLocked(scopeID, poolID string) []string {
	type entry struct {
		id       string
		priority int
	}
	var entries []entry
	if scopeID != "" {
		for _, a := range m.templateScopes {
			if a.ScopeID == scopeID {
				entries = append(entries, entry{a.TemplateID, a.Priority})
			}
		}
	}
	if poolID != "" {
		for _, a := range m.templatePools {
			if a.PoolID == poolID {
				entries = append(entries, entry{a.TemplateID, a.Priority})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].priority > entries[j].priority })
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.id
	}
	return ids
}

func (m *Mock) ResolveOption(ctx context.Context, optCode int16, scopeID, poolID, reservationID string) (*models.DHCPOption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	at := func(level models.OptionScopeLevel, entityID string) *models.DHCPOption {
		for _, o := range m.optionValues {
			if o.OptionCode == optCode && o.ScopeLevel == level && o.ScopeEntityID == entityID && o.IsEnabled {
				return o
			}
		}
		return nil
	}

	if reservationID != "" {
		if v := at(models.OptionScopeReservation, reservationID); v != nil {
			return v, nil
		}
	}
	if poolID != "" {
		if v := at(models.OptionScopePool, poolID); v != nil {
			return v, nil
		}
	}
	if scopeID != "" {
		if v := at(models.OptionScopeScope, scopeID); v != nil {
			return v, nil
		}
	}
	for _, tid := range m.orderedTemplateIDsLocked(scopeID, poolID) {
		if v := at(models.OptionScopeTemplate, tid); v != nil {
			return v, nil
		}
	}
	return at(models.OptionScopeGlobal, globalScopeEntityID), nil
}

func (m *Mock) ListOptionCatalog(ctx context.Context, params ListParams) (ListResult[*models.OptionCatalogEntry], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*models.OptionCatalogEntry
	for _, o := range m.optionCatalog {
		if matchStr(o.Name, params.Search) {
			items = append(items, o)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].OptionCode < items[j].OptionCode })
	limit, offset := normalizeListParams(params)
	total := int64(len(items))
	return ListResult[*models.OptionCatalogEntry]{Items: paginate(items, limit, offset), Total: total, Limit: limit, Offset: offset}, nil
}

func (m *Mock) GetAllOptionCatalog(ctx context.Context) ([]*models.OptionCatalogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.OptionCatalogEntry
	for _, o := range m.optionCatalog {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OptionCode < out[j].OptionCode })
	return out, nil
}

func (m *Mock) GetOptionCatalogEntry(ctx context.Context, code int16) (*models.OptionCatalogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.optionCatalog[code]
	if !ok {
		return nil, fmt.Errorf("mock: get option catalog entry %d: %w", code, models.ErrNotFound)
	}
	return o, nil
}

func (m *Mock) CreateOptionCatalogEntry(ctx context.Context, entry *models.OptionCatalogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.optionCatalog[entry.OptionCode]; ok {
		return fmt.Errorf("mock: create option catalog entry %d: %w", entry.OptionCode, models.ErrConflict)
	}
	if !entry.ValueType.Valid() {
		return fmt.Errorf("mock: create option catalog entry %d: invalid value_type: %w", entry.OptionCode, models.ErrInvalidInput)
	}
	entry.CreatedAt = time.Now().UTC()
	m.optionCatalog[entry.OptionCode] = entry
	return nil
}

func (m *Mock) DeleteOptionCatalogEntry(ctx context.Context, code int16) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.optionCatalog[code]; !ok {
		return fmt.Errorf("mock: delete option catalog entry %d: %w", code, models.ErrNotFound)
	}
	delete(m.optionCatalog, code)
	return nil
}

func (m *Mock) CreateOptionValue(ctx context.Context, ov *models.DHCPOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !ov.ScopeLevel.Valid() {
		return fmt.Errorf("mock: create option value: invalid scope_level %q: %w", ov.ScopeLevel, models.ErrInvalidInput)
	}
	for _, existing := range m.optionValues {
		if existing.OptionCode == ov.OptionCode && existing.ScopeLevel == ov.ScopeLevel && existing.ScopeEntityID == ov.ScopeEntityID {
			return fmt.Errorf("mock: create option value: %w", models.ErrConflict)
		}
	}
	if ov.ID == "" {
		ov.ID = newUUID()
	}
	now := time.Now().UTC()
	ov.CreatedAt, ov.UpdatedAt = now, now
	m.optionValues[ov.ID] = ov
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) UpdateOptionValue(ctx context.Context, id string, value string, isEnabled bool) (*models.DHCPOption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.optionValues[id]
	if !ok {
		return nil, fmt.Errorf("mock: update option value %s: %w", id, models.ErrNotFound)
	}
	o.Value, o.IsEnabled = value, isEnabled
	o.UpdatedAt = time.Now().UTC()
	m.notifyLocked(ConfigChangeChannel)
	return o, nil
}

func (m *Mock) DeleteOptionValue(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.optionValues[id]; !ok {
		return fmt.Errorf("mock: delete option value %s: %w", id, models.ErrNotFound)
	}
	delete(m.optionValues, id)
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

// ---------------------------------------------------------------------------
// TemplateStore + ConfigStore template reads
// ---------------------------------------------------------------------------

func (m *Mock) GetTemplateByID(ctx context.Context, templateID string) (*models.Template, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.templates[templateID]
	if !ok {
		return nil, fmt.Errorf("mock: get template %s: %w", templateID, models.ErrNotFound)
	}
	return t, nil
}

func (m *Mock) GetTemplatesByScope(ctx context.Context, scopeID string) ([]*models.Template, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type entry struct {
		t        *models.Template
		priority int
	}
	var entries []entry
	for _, a := range m.templateScopes {
		if a.ScopeID != scopeID {
			continue
		}
		if t, ok := m.templates[a.TemplateID]; ok {
			entries = append(entries, entry{t, a.Priority})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].priority > entries[j].priority })
	out := make([]*models.Template, len(entries))
	for i, e := range entries {
		out[i] = e.t
	}
	return out, nil
}

func (m *Mock) ListTemplates(ctx context.Context, params ListParams) (ListResult[*models.Template], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*models.Template
	for _, t := range m.templates {
		if matchStr(t.Name, params.Search) {
			items = append(items, t)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	limit, offset := normalizeListParams(params)
	total := int64(len(items))
	return ListResult[*models.Template]{Items: paginate(items, limit, offset), Total: total, Limit: limit, Offset: offset}, nil
}

func (m *Mock) CreateTemplate(ctx context.Context, t *models.Template) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.templates {
		if strings.EqualFold(existing.Name, t.Name) {
			return fmt.Errorf("mock: create template: name %q: %w", t.Name, models.ErrConflict)
		}
	}
	if t.ID == "" {
		t.ID = newUUID()
	}
	now := time.Now().UTC()
	t.CreatedAt, t.UpdatedAt = now, now
	m.templates[t.ID] = t
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) UpdateTemplate(ctx context.Context, t *models.Template) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.templates[t.ID]; !ok {
		return fmt.Errorf("mock: update template %s: %w", t.ID, models.ErrNotFound)
	}
	t.UpdatedAt = time.Now().UTC()
	m.templates[t.ID] = t
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) DeleteTemplate(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.templates[id]; !ok {
		return fmt.Errorf("mock: delete template %s: %w", id, models.ErrNotFound)
	}
	for aid, a := range m.templateScopes {
		if a.TemplateID == id {
			delete(m.templateScopes, aid)
		}
	}
	for aid, a := range m.templatePools {
		if a.TemplateID == id {
			delete(m.templatePools, aid)
		}
	}
	delete(m.templates, id)
	m.notifyLocked(ConfigChangeChannel)
	return nil
}

func (m *Mock) ListTemplateScopeAssocs(ctx context.Context, templateID string) ([]*models.TemplateScopeAssoc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.TemplateScopeAssoc
	for _, a := range m.templateScopes {
		if a.TemplateID == templateID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *Mock) AssociateTemplateScope(ctx context.Context, templateID, scopeID string, priority int) (*models.TemplateScopeAssoc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := &models.TemplateScopeAssoc{ID: newUUID(), TemplateID: templateID, ScopeID: scopeID, Priority: priority, CreatedAt: time.Now().UTC()}
	m.templateScopes[a.ID] = a
	m.notifyLocked(ConfigChangeChannel)
	return a, nil
}

func (m *Mock) DisassociateTemplateScope(ctx context.Context, templateID, scopeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, a := range m.templateScopes {
		if a.TemplateID == templateID && a.ScopeID == scopeID {
			delete(m.templateScopes, id)
			m.notifyLocked(ConfigChangeChannel)
			return nil
		}
	}
	return fmt.Errorf("mock: disassociate template %s from scope %s: %w", templateID, scopeID, models.ErrNotFound)
}

func (m *Mock) ListTemplatePoolAssocs(ctx context.Context, templateID string) ([]*models.TemplatePoolAssoc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.TemplatePoolAssoc
	for _, a := range m.templatePools {
		if a.TemplateID == templateID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *Mock) AssociateTemplatePool(ctx context.Context, templateID, poolID string, priority int) (*models.TemplatePoolAssoc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := &models.TemplatePoolAssoc{ID: newUUID(), TemplateID: templateID, PoolID: poolID, Priority: priority, CreatedAt: time.Now().UTC()}
	m.templatePools[a.ID] = a
	m.notifyLocked(ConfigChangeChannel)
	return a, nil
}

func (m *Mock) DisassociateTemplatePool(ctx context.Context, templateID, poolID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, a := range m.templatePools {
		if a.TemplateID == templateID && a.PoolID == poolID {
			delete(m.templatePools, id)
			m.notifyLocked(ConfigChangeChannel)
			return nil
		}
	}
	return fmt.Errorf("mock: disassociate template %s from pool %s: %w", templateID, poolID, models.ErrNotFound)
}

// ---------------------------------------------------------------------------
// TagStore
// ---------------------------------------------------------------------------

func (m *Mock) ListTags(ctx context.Context, params ListParams) (ListResult[*models.Tag], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*models.Tag
	for _, t := range m.tags {
		if matchStr(t.Name, params.Search) {
			items = append(items, t)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	limit, offset := normalizeListParams(params)
	total := int64(len(items))
	return ListResult[*models.Tag]{Items: paginate(items, limit, offset), Total: total, Limit: limit, Offset: offset}, nil
}

func (m *Mock) CreateTag(ctx context.Context, t *models.Tag) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.tags {
		if strings.EqualFold(existing.Name, t.Name) {
			return fmt.Errorf("mock: create tag: name %q: %w", t.Name, models.ErrConflict)
		}
	}
	if t.ID == "" {
		t.ID = newUUID()
	}
	t.CreatedAt = time.Now().UTC()
	m.tags[t.ID] = t
	return nil
}

func (m *Mock) DeleteTag(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tags[id]; !ok {
		return fmt.Errorf("mock: delete tag %s: %w", id, models.ErrNotFound)
	}
	for eid, et := range m.entityTags {
		if et.TagID == id {
			delete(m.entityTags, eid)
		}
	}
	delete(m.tags, id)
	return nil
}

func (m *Mock) AddEntityTag(ctx context.Context, tagID string, entityType models.TaggableEntityType, entityID string) (*models.EntityTag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tags[tagID]; !ok {
		return nil, fmt.Errorf("mock: add entity tag: tag %s: %w", tagID, models.ErrNotFound)
	}
	for _, et := range m.entityTags {
		if et.TagID == tagID && et.EntityType == entityType && et.EntityID == entityID {
			return nil, fmt.Errorf("mock: add entity tag: %w", models.ErrConflict)
		}
	}
	et := &models.EntityTag{ID: newUUID(), TagID: tagID, EntityType: entityType, EntityID: entityID, CreatedAt: time.Now().UTC()}
	m.entityTags[et.ID] = et
	return et, nil
}

func (m *Mock) RemoveEntityTag(ctx context.Context, tagID string, entityType models.TaggableEntityType, entityID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, et := range m.entityTags {
		if et.TagID == tagID && et.EntityType == entityType && et.EntityID == entityID {
			delete(m.entityTags, id)
			return nil
		}
	}
	return fmt.Errorf("mock: remove entity tag: %w", models.ErrNotFound)
}

func (m *Mock) ListTagsForEntity(ctx context.Context, entityType models.TaggableEntityType, entityID string) ([]*models.Tag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.Tag
	for _, et := range m.entityTags {
		if et.EntityType == entityType && et.EntityID == entityID {
			if t, ok := m.tags[et.TagID]; ok {
				out = append(out, t)
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// UserStore
// ---------------------------------------------------------------------------

func (m *Mock) GetUserByID(ctx context.Context, userID int64) (*models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return nil, fmt.Errorf("mock: get user %d: %w", userID, models.ErrNotFound)
	}
	return u, nil
}

func (m *Mock) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if strings.EqualFold(u.Username, username) {
			return u, nil
		}
	}
	return nil, fmt.Errorf("mock: get user %q: %w", username, models.ErrNotFound)
}

func (m *Mock) CreateUser(ctx context.Context, user *models.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if strings.EqualFold(u.Username, user.Username) {
			return fmt.Errorf("mock: create user: username %q: %w", user.Username, models.ErrConflict)
		}
	}
	if !user.Role.Valid() {
		user.Role = models.RoleViewer
	}
	m.nextUserID++
	user.ID = m.nextUserID
	now := time.Now().UTC()
	user.CreatedAt, user.UpdatedAt = now, now
	m.users[user.ID] = user
	return nil
}

func (m *Mock) UpdateUser(ctx context.Context, user *models.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.users[user.ID]
	if !ok {
		return fmt.Errorf("mock: update user %d: %w", user.ID, models.ErrNotFound)
	}
	user.PasswordHash = existing.PasswordHash // UserStore.UpdateUser excludes password changes.
	user.UpdatedAt = time.Now().UTC()
	m.users[user.ID] = user
	return nil
}

func (m *Mock) DeleteUser(ctx context.Context, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[userID]; !ok {
		return fmt.Errorf("mock: delete user %d: %w", userID, models.ErrNotFound)
	}
	delete(m.users, userID)
	for tid, t := range m.tokens {
		if t.UserID == userID {
			delete(m.tokens, tid)
		}
	}
	return nil
}

func (m *Mock) ListUsers(ctx context.Context, params ListParams) (ListResult[*models.User], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*models.User
	for _, u := range m.users {
		if matchStr(u.Username, params.Search) {
			items = append(items, u)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Username < items[j].Username })
	limit, offset := normalizeListParams(params)
	total := int64(len(items))
	return ListResult[*models.User]{Items: paginate(items, limit, offset), Total: total, Limit: limit, Offset: offset}, nil
}

func (m *Mock) CreateAPIToken(ctx context.Context, token *models.APIToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[token.UserID]; !ok {
		return fmt.Errorf("mock: create api token: user %d: %w", token.UserID, models.ErrNotFound)
	}
	m.nextTokenID++
	token.ID = m.nextTokenID
	token.CreatedAt = time.Now().UTC()
	m.tokens[token.ID] = token
	return nil
}

func (m *Mock) ValidateAPIToken(ctx context.Context, tokenHash string) (*models.APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if t.TokenHash != tokenHash {
			continue
		}
		if t.ExpiresAt != nil && t.ExpiresAt.Before(time.Now().UTC()) {
			return nil, fmt.Errorf("mock: validate api token: %w", models.ErrUnauthorized)
		}
		now := time.Now().UTC()
		t.LastUsedAt = &now
		return t, nil
	}
	return nil, fmt.Errorf("mock: validate api token: %w", models.ErrUnauthorized)
}

func (m *Mock) RevokeAPIToken(ctx context.Context, tokenID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tokens[tokenID]; !ok {
		return fmt.Errorf("mock: revoke api token %d: %w", tokenID, models.ErrNotFound)
	}
	delete(m.tokens, tokenID)
	return nil
}

func (m *Mock) ListAPITokens(ctx context.Context, userID int64) ([]*models.APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.APIToken
	for _, t := range m.tokens {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ---------------------------------------------------------------------------
// ClusterStore
// ---------------------------------------------------------------------------

func heartbeatKey(clusterID, serverID string) string { return clusterID + "/" + serverID }

func (m *Mock) GetClusterByID(ctx context.Context, clusterID string) (*models.Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.clusters[clusterID]
	if !ok {
		return nil, fmt.Errorf("mock: get cluster %s: %w", clusterID, models.ErrNotFound)
	}
	return c, nil
}

func (m *Mock) WriteHeartbeat(ctx context.Context, clusterID, serverID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for _, s := range m.servers {
		if s.ClusterID == clusterID && s.ID == serverID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("mock: write heartbeat: server %s not registered in cluster %s: %w", serverID, clusterID, models.ErrNotFound)
	}
	key := heartbeatKey(clusterID, serverID)
	hb, ok := m.heartbeats[key]
	now := time.Now().UTC()
	if !ok {
		hb = &models.Heartbeat{ID: newUUID(), ClusterID: clusterID, ServerID: serverID, CreatedAt: now}
		m.heartbeats[key] = hb
	}
	hb.HeartbeatTime = now
	hb.Role = models.ClusterRolePrimary
	hb.UpdatedAt = now
	return nil
}

func (m *Mock) GetHeartbeats(ctx context.Context, clusterID string) ([]*models.Heartbeat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.Heartbeat
	for _, hb := range m.heartbeats {
		if hb.ClusterID == clusterID {
			out = append(out, hb)
		}
	}
	return out, nil
}

func (m *Mock) ElectionQuery(ctx context.Context, clusterID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cluster, ok := m.clusters[clusterID]
	if !ok {
		return "", fmt.Errorf("mock: election query: cluster %s: %w", clusterID, models.ErrNotFound)
	}
	ttl := time.Duration(cluster.HeartbeatTTL) * time.Millisecond
	now := time.Now().UTC()
	var winner *models.ClusterServer
	for _, s := range m.servers {
		if s.ClusterID != clusterID {
			continue
		}
		hb, ok := m.heartbeats[heartbeatKey(clusterID, s.ID)]
		if !ok || now.Sub(hb.HeartbeatTime) > ttl {
			continue
		}
		if winner == nil || s.NodeID < winner.NodeID {
			winner = s
		}
	}
	if winner == nil {
		return "", fmt.Errorf("mock: election query: no healthy server in cluster %s: %w", clusterID, models.ErrNotFound)
	}
	return winner.ID, nil
}

func (m *Mock) RegisterServer(ctx context.Context, server *models.ClusterServer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if server.ID == "" {
		server.ID = newUUID()
	}
	now := time.Now().UTC()
	server.CreatedAt, server.UpdatedAt = now, now
	m.servers[server.ID] = server
	return nil
}

func (m *Mock) ListServers(ctx context.Context, clusterID string) ([]*models.ClusterServer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.ClusterServer
	for _, s := range m.servers {
		if s.ClusterID == clusterID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}

func (m *Mock) UpdateLeaseVersion(ctx context.Context, clusterID, serverID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hb, ok := m.heartbeats[heartbeatKey(clusterID, serverID)]
	if !ok {
		return 0, fmt.Errorf("mock: update lease version: %w", models.ErrNotFound)
	}
	hb.LeaseVersion++
	hb.UpdatedAt = time.Now().UTC()
	return hb.LeaseVersion, nil
}

func (m *Mock) UpdateLastConfigVersion(ctx context.Context, clusterID, serverID string, version int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	hb, ok := m.heartbeats[heartbeatKey(clusterID, serverID)]
	if !ok {
		return fmt.Errorf("mock: update last config version: %w", models.ErrNotFound)
	}
	hb.LastConfigVersion = version
	hb.UpdatedAt = time.Now().UTC()
	return nil
}

func (m *Mock) ListClusters(ctx context.Context, params ListParams) (ListResult[*models.Cluster], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*models.Cluster
	for _, c := range m.clusters {
		if matchStr(c.Name, params.Search) {
			items = append(items, c)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	limit, offset := normalizeListParams(params)
	total := int64(len(items))
	return ListResult[*models.Cluster]{Items: paginate(items, limit, offset), Total: total, Limit: limit, Offset: offset}, nil
}

func (m *Mock) CreateCluster(ctx context.Context, c *models.Cluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.clusters {
		if strings.EqualFold(existing.Name, c.Name) {
			return fmt.Errorf("mock: create cluster: name %q: %w", c.Name, models.ErrConflict)
		}
	}
	if c.ID == "" {
		c.ID = newUUID()
	}
	now := time.Now().UTC()
	c.CreatedAt, c.UpdatedAt = now, now
	m.clusters[c.ID] = c
	return nil
}

func (m *Mock) UpdateCluster(ctx context.Context, c *models.Cluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.clusters[c.ID]; !ok {
		return fmt.Errorf("mock: update cluster %s: %w", c.ID, models.ErrNotFound)
	}
	c.UpdatedAt = time.Now().UTC()
	m.clusters[c.ID] = c
	return nil
}

func (m *Mock) DeleteCluster(ctx context.Context, clusterID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.clusters[clusterID]; !ok {
		return fmt.Errorf("mock: delete cluster %s: %w", clusterID, models.ErrNotFound)
	}
	for _, s := range m.servers {
		if s.ClusterID == clusterID {
			return fmt.Errorf("mock: delete cluster %s: has servers: %w", clusterID, models.ErrHasDependents)
		}
	}
	delete(m.clusters, clusterID)
	return nil
}

func (m *Mock) DeregisterServer(ctx context.Context, clusterID, serverID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.servers[serverID]; !ok {
		return fmt.Errorf("mock: deregister server %s: %w", serverID, models.ErrNotFound)
	}
	delete(m.servers, serverID)
	delete(m.heartbeats, heartbeatKey(clusterID, serverID))
	return nil
}

func (m *Mock) GetClusterHealth(ctx context.Context, clusterID string) (*models.ClusterHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cluster, ok := m.clusters[clusterID]
	if !ok {
		return nil, fmt.Errorf("mock: get cluster health %s: %w", clusterID, models.ErrNotFound)
	}
	ttl := time.Duration(cluster.HeartbeatTTL) * time.Millisecond
	now := time.Now().UTC()
	var servers []*models.ClusterServerHealth
	healthy := 0
	for _, s := range m.servers {
		if s.ClusterID != clusterID {
			continue
		}
		sh := &models.ClusterServerHealth{ServerID: s.ID, NodeName: s.NodeName, NodeID: s.NodeID, APIAddress: s.APIAddress, Role: models.ClusterRoleSecondary}
		if hb, ok := m.heartbeats[heartbeatKey(clusterID, s.ID)]; ok {
			sh.Role = hb.Role
			sh.VIPAssigned = hb.VIPAssigned
			sh.HeartbeatTime = hb.HeartbeatTime
			sh.HeartbeatAgeMs = now.Sub(hb.HeartbeatTime).Milliseconds()
			sh.IsHealthy = now.Sub(hb.HeartbeatTime) <= ttl
			sh.LastConfigApply = hb.LastConfigVersion
		}
		if sh.IsHealthy {
			healthy++
		}
		servers = append(servers, sh)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].NodeID < servers[j].NodeID })
	state := models.ClusterStateDown
	switch {
	case healthy == len(servers) && len(servers) > 0:
		state = models.ClusterStateHealthy
	case healthy > 0:
		state = models.ClusterStateDegraded
	}
	return &models.ClusterHealth{ClusterID: clusterID, Timestamp: now, Servers: servers, ClusterState: state}, nil
}

// ---------------------------------------------------------------------------
// AuditStore
// ---------------------------------------------------------------------------

func (m *Mock) LogAction(ctx context.Context, log *models.AuditLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextAuditID++
	log.ID = m.nextAuditID
	log.Timestamp = time.Now().UTC()
	m.auditLog = append(m.auditLog, log)
	return nil
}

func (m *Mock) ListAuditLog(ctx context.Context, offset, limit int, filters map[string]interface{}) ([]*models.AuditLog, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*models.AuditLog
	for i := len(m.auditLog) - 1; i >= 0; i-- {
		l := m.auditLog[i]
		if v, ok := filters["entity_type"]; ok && l.EntityType != fmt.Sprint(v) {
			continue
		}
		if v, ok := filters["entity_id"]; ok && l.EntityID != fmt.Sprint(v) {
			continue
		}
		if v, ok := filters["action"]; ok && string(l.Action) != fmt.Sprint(v) {
			continue
		}
		if v, ok := filters["user_id"]; ok {
			if l.UserID == nil || strconv.FormatInt(*l.UserID, 10) != fmt.Sprint(v) {
				continue
			}
		}
		items = append(items, l)
	}
	total := int64(len(items))
	if limit <= 0 {
		limit = defaultListLimit
	}
	return paginate(items, limit, offset), total, nil
}

// ---------------------------------------------------------------------------
// StatsRecorder + DashboardStore
// ---------------------------------------------------------------------------

func (m *Mock) recordTxn(scopeID string, inc func(*models.DHCPStats)) {
	if scopeID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.txnStats[scopeID]
	if !ok {
		s = &models.DHCPStats{ScopeID: scopeID}
		m.txnStats[scopeID] = s
	}
	inc(s)
}

func (m *Mock) RecordDiscover(scopeID string) {
	m.recordTxn(scopeID, func(s *models.DHCPStats) { s.DiscoverCount++ })
}
func (m *Mock) RecordOffer(scopeID string) {
	m.recordTxn(scopeID, func(s *models.DHCPStats) { s.OfferCount++ })
}
func (m *Mock) RecordRequest(scopeID string) {
	m.recordTxn(scopeID, func(s *models.DHCPStats) { s.RequestCount++ })
}
func (m *Mock) RecordAck(scopeID string) {
	m.recordTxn(scopeID, func(s *models.DHCPStats) { s.AckCount++ })
}
func (m *Mock) RecordNak(scopeID string) {
	m.recordTxn(scopeID, func(s *models.DHCPStats) { s.NakCount++ })
}
func (m *Mock) RecordDecline(scopeID string) {
	m.recordTxn(scopeID, func(s *models.DHCPStats) { s.DeclineCount++ })
}
func (m *Mock) RecordRelease(scopeID string) {
	m.recordTxn(scopeID, func(s *models.DHCPStats) { s.ReleaseCount++ })
}
func (m *Mock) RecordNoLeases(scopeID string) {
	m.recordTxn(scopeID, func(s *models.DHCPStats) { s.NoLeasesCount++ })
}

func (m *Mock) GetStats(ctx context.Context, scopeID string, window time.Duration) (*models.DHCPStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.txnStats[scopeID]; ok {
		cp := *s
		return &cp, nil
	}
	return &models.DHCPStats{ScopeID: scopeID}, nil
}

// SampleUtilization is a no-op for the mock: GetScopeUtilization/
// GetPoolUtilization always recompute live from current state (there is no
// separate stats_utilization_samples-equivalent store to populate), so
// there is nothing to reconcile.
func (m *Mock) SampleUtilization(ctx context.Context) error { return nil }

func (m *Mock) GetDashboardStats(ctx context.Context, window time.Duration) (*models.DashboardStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := &models.DashboardStats{Timestamp: time.Now().UTC()}
	s.TotalVRFs = int64(len(m.vrfs))
	s.TotalScopes = int64(len(m.scopes))
	s.TotalPools = int64(len(m.pools))

	var total, active, reserved, reservedUnleased int64
	for _, p := range m.pools {
		t, a, r, ru := m.poolTotalsLocked(p)
		total += t
		active += a
		reserved += r
		reservedUnleased += ru
	}
	u := utilizationTotals{total: total, active: active, reservedTotal: reserved, reservedUnleased: reservedUnleased}.toUtilization("", "")
	s.TotalAddresses, s.TotalActiveLeases, s.TotalReservedAddresses, s.TotalAvailableAddresses, s.OverallUtilizationPercentage =
		u.Total, u.Active, u.Reserved, u.Available, u.UtilizationPercentage

	agg := &models.DHCPStats{}
	for _, st := range m.txnStats {
		agg.DiscoverCount += st.DiscoverCount
		agg.OfferCount += st.OfferCount
		agg.RequestCount += st.RequestCount
		agg.AckCount += st.AckCount
		agg.NakCount += st.NakCount
		agg.DeclineCount += st.DeclineCount
		agg.ReleaseCount += st.ReleaseCount
		agg.NoLeasesCount += st.NoLeasesCount
	}
	s.DHCPStatsWindow = agg

	for id := range m.clusters {
		m.mu.Unlock()
		health, err := m.GetClusterHealth(ctx, id)
		m.mu.Lock()
		if err == nil {
			s.ClusterHealth = append(s.ClusterHealth, health)
		}
	}

	type summary struct {
		id, name       string
		active, total  int64
		utilizationPct float64
	}
	var summaries []summary
	for scopeID, sc := range m.scopes {
		var t, a int64
		for _, p := range m.pools {
			if p.ScopeID != scopeID {
				continue
			}
			pt, pa, _, _ := m.poolTotalsLocked(p)
			t += pt
			a += pa
		}
		var pct float64
		if t > 0 {
			pct = float64(a) / float64(t) * 100
		}
		summaries = append(summaries, summary{scopeID, sc.Name, a, t, pct})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].utilizationPct > summaries[j].utilizationPct })
	limit := dashboardTopScopesLimit
	if limit > len(summaries) {
		limit = len(summaries)
	}
	for _, su := range summaries[:limit] {
		s.TopUtilizedScopes = append(s.TopUtilizedScopes, &models.ScopeUtilizationSummary{
			ScopeID: su.id, ScopeName: su.name, UtilizationPercentage: su.utilizationPct, Active: su.active, Total: su.total,
		})
	}

	return s, nil
}

// GetUtilizationChart returns no history for the mock: it keeps no
// hourly-rollup-equivalent store (see SampleUtilization's doc comment).
// Tests asserting chart behavior should seed data through a real Postgres
// instance instead.
func (m *Mock) GetUtilizationChart(ctx context.Context, entityType models.UtilizationEntityType, entityID string, days int) ([]*models.ChartPoint, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// SearchStore
// ---------------------------------------------------------------------------

func (m *Mock) GlobalSearch(ctx context.Context, query string, entityTypes []string, limit int) ([]*models.SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = defaultListLimit
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []*models.SearchResult{}, nil
	}
	wants := func(t models.SearchResultType) bool {
		if len(entityTypes) == 0 {
			return true
		}
		for _, want := range entityTypes {
			if want == string(t) {
				return true
			}
		}
		return false
	}

	out := []*models.SearchResult{}
	add := func(t models.SearchResultType, id, title, subtitle, url string) {
		if len(out) >= limit || !wants(t) {
			return
		}
		out = append(out, &models.SearchResult{Type: t, EntityID: id, Title: title, Subtitle: subtitle, URL: url})
	}

	for _, v := range m.vrfs {
		if matchStr(v.Name, query) {
			add(models.SearchResultVRF, v.ID, v.Name, "", "/ipam/vrfs/"+v.ID)
		}
	}
	for _, p := range m.prefixes {
		if p.Prefix != nil && (matchStr(p.Prefix.String(), query) || (p.Name != nil && matchStr(*p.Name, query))) {
			add(models.SearchResultPrefix, p.ID, p.Prefix.String(), "", "/ipam/prefixes/"+p.ID)
		}
	}
	for _, vl := range m.vlans {
		if matchStr(vl.Name, query) || matchStr(strconv.Itoa(vl.VID), query) {
			add(models.SearchResultVLAN, vl.ID, vl.Name, "VID "+strconv.Itoa(vl.VID), "/ipam/vlans/"+vl.ID)
		}
	}
	for _, vg := range m.vlanGroups {
		if matchStr(vg.Name, query) {
			add(models.SearchResultVLANGroup, vg.ID, vg.Name, "", "/ipam/vlans?group="+vg.ID)
		}
	}
	for _, sc := range m.scopes {
		if matchStr(sc.Name, query) {
			add(models.SearchResultScope, sc.ID, sc.Name, "", "/dhcp/scopes/"+sc.ID)
		}
	}
	for _, p := range m.pools {
		if matchStr(p.Name, query) {
			add(models.SearchResultPool, p.ID, p.Name, "", "/dhcp/pools/"+p.ID)
		}
	}
	for _, r := range m.reservations {
		hostname := ""
		if r.Hostname != nil {
			hostname = *r.Hostname
		}
		if matchStr(hostname, query) || matchStr(r.ReservedIP.String(), query) || matchStr(r.BindingKey, query) {
			add(models.SearchResultReservation, r.ID, r.ReservedIP.String(), hostname, "/dhcp/pools/"+r.PoolID+"?tab=reservations")
		}
	}
	for _, l := range m.leases {
		hostname := ""
		if l.ClientHostname != nil {
			hostname = *l.ClientHostname
		}
		if matchStr(l.IPAddress.String(), query) || matchStr(hostname, query) || (l.ClientMAC != nil && matchStr(l.ClientMAC.String(), query)) {
			add(models.SearchResultLease, l.ID, l.IPAddress.String(), hostname, "/dhcp/leases/"+l.ID)
		}
	}
	for _, t := range m.templates {
		if matchStr(t.Name, query) {
			add(models.SearchResultTemplate, t.ID, t.Name, "", "/dhcp/templates/"+t.ID)
		}
	}
	for _, o := range m.optionCatalog {
		code := strconv.Itoa(int(o.OptionCode))
		if matchStr(o.Name, query) || matchStr(code, query) {
			add(models.SearchResultOption, code, o.Name, "Option "+code, "/dhcp/options?q="+code)
		}
	}
	for _, c := range m.clusters {
		if matchStr(c.Name, query) {
			add(models.SearchResultCluster, c.ID, c.Name, "", "/ha/clusters/"+c.ID)
		}
	}
	// Deliberately no 'server' or 'user' results: matches
	// postgres_admin.go's globalSearchSQL, which drops both so this
	// viewer-accessible endpoint can't be used to bypass the admin-only
	// gate on the user directory (see that file's comment for the full
	// rationale).

	return out, nil
}

// ---------------------------------------------------------------------------
// ConfigChangeStore — in-memory pub/sub standing in for LISTEN/NOTIFY.
// ---------------------------------------------------------------------------

func (m *Mock) notifyLocked(channel string) {
	for _, ch := range m.listeners[channel] {
		select {
		case ch <- time.Now().UTC().Format(time.RFC3339Nano):
		default: // Slow/absent listener: drop rather than block the writer.
		}
	}
}

func (m *Mock) NotifyConfigChange(ctx context.Context, channel string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifyLocked(channel)
	return nil
}

func (m *Mock) ListenConfigChanges(ctx context.Context, channel string) (<-chan string, func() error, error) {
	m.mu.Lock()
	ch := make(chan string, 16)
	m.listeners[channel] = append(m.listeners[channel], ch)
	m.mu.Unlock()

	var closeOnce sync.Once
	closeFn := func() error {
		closeOnce.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			subs := m.listeners[channel]
			for i, c := range subs {
				if c == ch {
					m.listeners[channel] = append(subs[:i], subs[i+1:]...)
					break
				}
			}
			close(ch)
		})
		return nil
	}
	return ch, closeFn, nil
}

// ---------------------------------------------------------------------------
// VLANStore
// ---------------------------------------------------------------------------

func (m *Mock) ListVLANGroups(ctx context.Context, params ListParams) (ListResult[*models.VLANGroup], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]*models.VLANGroup, 0, len(m.vlanGroups))
	for _, g := range m.vlanGroups {
		if params.Search != "" && !strings.Contains(strings.ToLower(g.Name), strings.ToLower(params.Search)) {
			continue
		}
		items = append(items, g)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return ListResult[*models.VLANGroup]{Items: items, Total: int64(len(items)), Limit: params.Limit, Offset: params.Offset}, nil
}

func (m *Mock) GetVLANGroupByID(ctx context.Context, id string) (*models.VLANGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.vlanGroups[id]
	if !ok {
		return nil, fmt.Errorf("mock: get vlan group %s: %w", id, models.ErrNotFound)
	}
	return g, nil
}

func (m *Mock) CreateVLANGroup(ctx context.Context, g *models.VLANGroup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g.ID == "" {
		g.ID = newUUID()
	}
	for _, existing := range m.vlanGroups {
		if existing.Name == g.Name {
			return fmt.Errorf("mock: create vlan group %s: %w", g.Name, models.ErrConflict)
		}
	}
	g.CreatedAt, g.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	m.vlanGroups[g.ID] = g
	return nil
}

func (m *Mock) UpdateVLANGroup(ctx context.Context, g *models.VLANGroup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.vlanGroups[g.ID]; !ok {
		return fmt.Errorf("mock: update vlan group %s: %w", g.ID, models.ErrNotFound)
	}
	g.UpdatedAt = time.Now().UTC()
	m.vlanGroups[g.ID] = g
	return nil
}

func (m *Mock) DeleteVLANGroup(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.vlanGroups[id]; !ok {
		return fmt.Errorf("mock: delete vlan group %s: %w", id, models.ErrNotFound)
	}
	delete(m.vlanGroups, id)
	// Mirrors ON DELETE SET NULL: members survive, ungrouped.
	for _, v := range m.vlans {
		if v.GroupID != nil && *v.GroupID == id {
			v.GroupID = nil
		}
	}
	return nil
}

func (m *Mock) ListVLANs(ctx context.Context, params ListParams) (ListResult[*models.VLAN], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	groupFilter := params.Filters["group_id"]
	statusFilter := params.Filters["status"]

	items := make([]*models.VLAN, 0, len(m.vlans))
	for _, v := range m.vlans {
		switch {
		case groupFilter == "none":
			if v.GroupID != nil {
				continue
			}
		case groupFilter != "":
			if v.GroupID == nil || *v.GroupID != groupFilter {
				continue
			}
		}
		if statusFilter != "" && string(v.Status) != statusFilter {
			continue
		}
		if params.Search != "" &&
			!strings.Contains(strings.ToLower(v.Name), strings.ToLower(params.Search)) &&
			strconv.Itoa(v.VID) != params.Search {
			continue
		}
		items = append(items, m.decorateVLANLocked(v))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].VID < items[j].VID })
	return ListResult[*models.VLAN]{Items: items, Total: int64(len(items)), Limit: params.Limit, Offset: params.Offset}, nil
}

// decorateVLANLocked fills the read-only group_name/prefix_count joins the
// Postgres queries compute in SQL. Caller must hold m.mu.
func (m *Mock) decorateVLANLocked(v *models.VLAN) *models.VLAN {
	out := *v
	out.GroupName = nil
	if v.GroupID != nil {
		if g, ok := m.vlanGroups[*v.GroupID]; ok {
			name := g.Name
			out.GroupName = &name
		}
	}
	out.PrefixCount = 0
	for _, p := range m.prefixes {
		if p.VLANID != nil && *p.VLANID == v.ID {
			out.PrefixCount++
		}
	}
	return &out
}

func (m *Mock) GetVLANByID(ctx context.Context, id string) (*models.VLAN, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vlans[id]
	if !ok {
		return nil, fmt.Errorf("mock: get vlan %s: %w", id, models.ErrNotFound)
	}
	return m.decorateVLANLocked(v), nil
}

// vidConflictLocked enforces the two partial unique indexes from migration
// 0004: a VID is unique within its group, and unique among ungrouped VLANs.
func (m *Mock) vidConflictLocked(v *models.VLAN) bool {
	for _, existing := range m.vlans {
		if existing.ID == v.ID || existing.VID != v.VID {
			continue
		}
		switch {
		case existing.GroupID == nil && v.GroupID == nil:
			return true
		case existing.GroupID != nil && v.GroupID != nil && *existing.GroupID == *v.GroupID:
			return true
		}
	}
	return false
}

func (m *Mock) CreateVLAN(ctx context.Context, v *models.VLAN) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v.ID == "" {
		v.ID = newUUID()
	}
	if v.Status == "" {
		v.Status = models.VLANStatusActive
	}
	if m.vidConflictLocked(v) {
		return fmt.Errorf("mock: create vlan %d: %w", v.VID, models.ErrConflict)
	}
	v.CreatedAt, v.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	m.vlans[v.ID] = v
	return nil
}

func (m *Mock) UpdateVLAN(ctx context.Context, v *models.VLAN) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.vlans[v.ID]; !ok {
		return fmt.Errorf("mock: update vlan %s: %w", v.ID, models.ErrNotFound)
	}
	if m.vidConflictLocked(v) {
		return fmt.Errorf("mock: update vlan %d: %w", v.VID, models.ErrConflict)
	}
	v.UpdatedAt = time.Now().UTC()
	m.vlans[v.ID] = v
	return nil
}

func (m *Mock) DeleteVLAN(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.vlans[id]; !ok {
		return fmt.Errorf("mock: delete vlan %s: %w", id, models.ErrNotFound)
	}
	delete(m.vlans, id)
	// Mirrors ON DELETE SET NULL on ipam_prefixes.vlan_id.
	for _, p := range m.prefixes {
		if p.VLANID != nil && *p.VLANID == id {
			p.VLANID = nil
		}
	}
	return nil
}

func (m *Mock) ListPrefixesByVLAN(ctx context.Context, vlanID string) ([]*models.IPAMPrefix, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]*models.IPAMPrefix, 0)
	for _, p := range m.prefixes {
		if p.VLANID != nil && *p.VLANID == vlanID {
			items = append(items, p)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Prefix.String() < items[j].Prefix.String() })
	return items, nil
}

// ListClientsUnderPrefix returns DHCP clients (leases and reservations)
// under prefixID and its descendants, supporting filters and sorting.
// This is the mock implementation of the store interface method.
// ListClientsUnderPrefix mirrors the Postgres implementation: it walks the
// prefix tree down from prefixID, gathers the leases and enabled reservations
// of every scope hanging off a prefix in that subtree, then filters, sorts and
// paginates them. The semantics must match postgres_ipam.go exactly, since the
// API tests run against this mock.
func (m *Mock) ListClientsUnderPrefix(ctx context.Context, prefixID string, params ListParams) (ListResult[*models.DHCPClient], error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if params.Limit <= 0 {
		params.Limit = defaultListLimit
	}

	// Descend the prefix tree by parent_id, the same walk the Postgres
	// recursive CTE performs. Iterating to a fixed point rather than
	// recursing keeps a malformed parent chain from blowing the stack.
	descendants := map[string]bool{prefixID: true}
	for changed := true; changed; {
		changed = false
		for _, p := range m.prefixes {
			if p.ParentID != nil && descendants[*p.ParentID] && !descendants[p.ID] {
				descendants[p.ID] = true
				changed = true
			}
		}
	}

	poolOwner := make(map[string]*models.DHCPPool, len(m.pools))
	scopeOf := make(map[string]*models.DHCPScope, len(m.pools))
	for _, pool := range m.pools {
		for _, scope := range m.scopes {
			if scope.ID == pool.ScopeID && descendants[scope.IPAMPrefixID] {
				poolOwner[pool.ID] = pool
				scopeOf[pool.ID] = scope
			}
		}
	}

	var all []*models.DHCPClient
	for _, lease := range m.leases {
		pool, ok := poolOwner[lease.PoolID]
		if !ok {
			continue
		}
		scope := scopeOf[lease.PoolID]
		state := lease.BindingState
		start, end := lease.LeaseStart, lease.LeaseEnd
		// Annotate with reservation state (amendment A19): find the reservation
		// that concerns this lease, applying the same precedence as the lease
		// read path.
		leaseRes := m.leaseReservationLocked(lease)
		leaseState := models.ClassifyLeaseReservation(leaseRes)
		all = append(all, &models.DHCPClient{
			ID:               lease.ID,
			Kind:             models.ClientKindLease,
			IPAddress:        lease.IPAddress,
			PoolID:           pool.ID,
			PoolName:         pool.Name,
			ScopeID:          scope.ID,
			ScopeName:        scope.Name,
			PrefixID:         scope.IPAMPrefixID,
			ClientMAC:        lease.ClientMAC,
			ClientID:         lease.ClientID,
			ClientHostname:   lease.ClientHostname,
			BindingState:     &state,
			LeaseStart:       &start,
			LeaseEnd:         &end,
			ReservationState: leaseState,
			Reservation:      leaseRes,
			CreatedAt:        lease.CreatedAt,
			UpdatedAt:        lease.UpdatedAt,
		})
	}
	for _, res := range m.reservations {
		pool, ok := poolOwner[res.PoolID]
		if !ok || !res.IsEnabled {
			continue
		}
		scope := scopeOf[res.PoolID]
		key, bindingType, enabled := res.BindingKey, res.BindingType, res.IsEnabled
		// Annotate with reservation state (amendment A19): a reservation row
		// describes itself, so Reservation is populated from itself with
		// MatchesClient and MatchesAddress both true. ReservationState is
		// LeasePinned (enabled) or LeasePinnedDisabled (disabled).
		resState := models.LeaseReservation{
			ID:             res.ID,
			ReservedIP:     res.ReservedIP,
			BindingKey:     res.BindingKey,
			BindingType:    res.BindingType,
			IsEnabled:      res.IsEnabled,
			MatchesClient:  true,
			MatchesAddress: true,
		}
		reservationState := models.ClassifyLeaseReservation(&resState)
		client := &models.DHCPClient{
			ID:               res.ID,
			Kind:             models.ClientKindReservation,
			IPAddress:        res.ReservedIP,
			PoolID:           pool.ID,
			PoolName:         pool.Name,
			ScopeID:          scope.ID,
			ScopeName:        scope.Name,
			PrefixID:         scope.IPAMPrefixID,
			ClientHostname:   res.Hostname,
			BindingKey:       &key,
			BindingType:      &bindingType,
			IsEnabled:        &enabled,
			ReservationState: reservationState,
			Reservation:      &resState,
			CreatedAt:        res.CreatedAt,
			UpdatedAt:        res.UpdatedAt,
		}
		// A MAC-bound reservation identifies a hardware address just as a
		// lease does; surface it in the same column so the UI can show one
		// coherent table.
		if t, value, ok := models.ParseBindingKey(res.BindingKey); ok && t == models.BindingTypeMAC {
			if hw, err := net.ParseMAC(value); err == nil {
				ha := models.HardwareAddr(hw)
				client.ClientMAC = &ha
			}
		}
		all = append(all, client)
	}

	// Filters are conjunctive: each one narrows the previous result.
	filtered := all
	if kind := params.Filters["kind"]; kind != "" {
		var next []*models.DHCPClient
		for _, c := range filtered {
			if string(c.Kind) == kind {
				next = append(next, c)
			}
		}
		filtered = next
	}
	if state := params.Filters["state"]; state != "" {
		// A lease state filter cannot match a reservation, which has no
		// binding state, so reservations drop out entirely.
		var next []*models.DHCPClient
		for _, c := range filtered {
			if c.BindingState != nil && string(*c.BindingState) == state {
				next = append(next, c)
			}
		}
		filtered = next
	}
	// Accept either the "q" filter the handler registers or the "search"
	// param ParseListParams fills, exactly as the Postgres side does.
	search := params.Filters["q"]
	if search == "" {
		search = params.Search
	}
	if q := strings.ToLower(strings.TrimSpace(search)); q != "" {
		var next []*models.DHCPClient
		for _, c := range filtered {
			mac := ""
			if c.ClientMAC != nil {
				mac = strings.ToLower(c.ClientMAC.String())
			}
			host := ""
			if c.ClientHostname != nil {
				host = strings.ToLower(*c.ClientHostname)
			}
			if strings.Contains(strings.ToLower(c.IPAddress.String()), q) ||
				strings.Contains(mac, q) || strings.Contains(host, q) {
				next = append(next, c)
			}
		}
		filtered = next
	}

	total := int64(len(filtered))

	field, desc := params.Sort, false
	if strings.HasPrefix(field, "-") {
		field, desc = field[1:], true
	}
	less := func(i, j int) bool { return compareIP(filtered[i].IPAddress, filtered[j].IPAddress) < 0 }
	switch field {
	case "lease_end":
		less = func(i, j int) bool {
			a, b := filtered[i].LeaseEnd, filtered[j].LeaseEnd
			switch {
			case a == nil && b == nil:
				return false
			case a == nil:
				return false // reservations sort after leases
			case b == nil:
				return true
			default:
				return a.Before(*b)
			}
		}
	case "client_mac":
		less = func(i, j int) bool { return macKey(filtered[i]) < macKey(filtered[j]) }
	case "client_hostname":
		less = func(i, j int) bool { return hostnameKey(filtered[i]) < hostnameKey(filtered[j]) }
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if desc {
			return less(j, i)
		}
		return less(i, j)
	})

	start := params.Offset
	if start < 0 {
		start = 0
	}
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + params.Limit
	if end > len(filtered) {
		end = len(filtered)
	}

	return ListResult[*models.DHCPClient]{
		Items:  filtered[start:end],
		Total:  total,
		Limit:  params.Limit,
		Offset: params.Offset,
	}, nil
}

// macKey and hostnameKey give the sort comparators a total order over rows
// whose MAC or hostname is absent.
func macKey(c *models.DHCPClient) string {
	if c.ClientMAC == nil {
		return ""
	}
	return c.ClientMAC.String()
}

func hostnameKey(c *models.DHCPClient) string {
	if c.ClientHostname == nil {
		return ""
	}
	return *c.ClientHostname
}
