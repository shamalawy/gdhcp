package dhcp

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// vrfCacheTTL bounds how long a resolved interface->VRF mapping is reused
// before being re-queried from the store, so config changes (a VRF
// reassigned to an interface via server_interfaces) take effect without a
// restart, while the hot packet path doesn't hit the database per-packet.
const vrfCacheTTL = 10 * time.Second

type vrfCacheEntry struct {
	vrf     *models.VRF
	expires time.Time
}

// servingContext is the result of VRF/scope selection for one request: the
// VRF the request belongs to, and the ordered list of candidate scopes to
// try allocation against. More than one scope means a shared-network wire
// (design doc: "multiple scopes on one wire ... trying scopes in the VRF
// containing the interface address").
type servingContext struct {
	VRF     *models.VRF
	Scopes  []*models.DHCPScope
	Relayed bool
	Giaddr  net.IP
}

// selectServingContext implements vrf.go's selection algorithm:
//  1. Relayed (giaddr set): match giaddr against a scope directly.
//  2. Renewing/rebinding (ciaddr set, no giaddr): resolve the interface's
//     VRF, then find scope(s) in that VRF whose network contains ciaddr.
//  3. Direct (neither set): resolve the interface's VRF, then find scope(s)
//     in that VRF whose network contains one of the interface's own
//     addresses (shared-network case: all matches are returned as
//     candidates, tried in order by allocation.go).
func (e *Engine) selectServingContext(ctx context.Context, pkt *DHCPPacket) (*servingContext, error) {
	req := pkt.Packet

	if !isZeroIP(req.GatewayIPAddr) {
		return e.selectByGiaddr(ctx, req.GatewayIPAddr)
	}

	vrf, err := e.vrfForInterface(ctx, pkt.Interface)
	if err != nil {
		return nil, err
	}

	if !isZeroIP(req.ClientIPAddr) {
		scopes, err := e.scopesContainingIP(ctx, vrf.ID, req.ClientIPAddr)
		if err != nil {
			return nil, err
		}
		if len(scopes) == 0 {
			return nil, fmt.Errorf("%w: ciaddr %s not in any scope of vrf %s", ErrScopeNotFound, req.ClientIPAddr, vrf.ID)
		}
		return &servingContext{VRF: vrf, Scopes: scopes}, nil
	}

	scopes, err := e.directScopes(ctx, pkt.Interface, vrf, req)
	if err != nil {
		return nil, err
	}
	if len(scopes) == 0 {
		return nil, fmt.Errorf("%w: interface %s vrf %s", ErrScopeNotFound, pkt.Interface, vrf.ID)
	}
	return &servingContext{VRF: vrf, Scopes: scopes}, nil
}

// directScopes handles the no-giaddr, no-ciaddr case (DISCOVER, INIT-REBOOT):
// match the receiving interface's local addresses against scope networks in
// its VRF (shared-network aware), falling back to the client's requested-IP
// option (INIT-REBOOT) and finally to "every enabled scope in this VRF" for
// single-scope deployments where the interface has no matching local address
// configured yet (e.g. relay-less bring-up).
func (e *Engine) directScopes(ctx context.Context, ifname string, vrf *models.VRF, req interface {
	RequestedIPAddress() net.IP
}) ([]*models.DHCPScope, error) {
	seen := map[string]bool{}
	var scopes []*models.DHCPScope
	addToSet := func(cands []*models.DHCPScope) {
		for _, s := range cands {
			if !seen[s.ID] {
				seen[s.ID] = true
				scopes = append(scopes, s)
			}
		}
	}

	if localAddrs, err := interfaceLocalAddrs(ifname); err == nil {
		for _, addr := range localAddrs {
			cand, err := e.scopesContainingIP(ctx, vrf.ID, addr)
			if err == nil {
				addToSet(cand)
			}
		}
	}

	if len(scopes) == 0 {
		if reqIP := req.RequestedIPAddress(); reqIP != nil && !isZeroIP(reqIP) {
			if cand, err := e.scopesContainingIP(ctx, vrf.ID, reqIP); err == nil {
				addToSet(cand)
			}
		}
	}

	if len(scopes) == 0 {
		all, err := e.store.GetScopesByVRF(ctx, vrf.ID)
		if err != nil {
			return nil, fmt.Errorf("dhcp: list scopes for vrf %s: %w", vrf.ID, err)
		}
		for _, s := range all {
			if s.IsEnabled {
				scopes = append(scopes, s)
			}
		}
	}

	return scopes, nil
}

// networkScopes resolves the scopes ifname's VRF serves from network
// topology alone: the interface's own local addresses (directScopes' tier
// 1), or — if it has none configured yet (e.g. relay-less bring-up) —
// every enabled scope in the VRF (directScopes' tier 3). Unlike
// directScopes' own result, this never consults anything the client sent
// (directScopes' tier 2 falls back to matching scopes by the client's own
// requested-IP option), so it is safe to validate a client-supplied
// requested IP against without that check becoming tautological — see
// handleRequestInitReboot's wrong-network check, the one caller of this.
func (e *Engine) networkScopes(ctx context.Context, ifname string, vrf *models.VRF) ([]*models.DHCPScope, error) {
	if localAddrs, err := interfaceLocalAddrs(ifname); err == nil {
		seen := map[string]bool{}
		var scopes []*models.DHCPScope
		for _, addr := range localAddrs {
			cand, err := e.scopesContainingIP(ctx, vrf.ID, addr)
			if err != nil {
				continue
			}
			for _, s := range cand {
				if !seen[s.ID] {
					seen[s.ID] = true
					scopes = append(scopes, s)
				}
			}
		}
		if len(scopes) > 0 {
			return scopes, nil
		}
	}

	all, err := e.store.GetScopesByVRF(ctx, vrf.ID)
	if err != nil {
		return nil, fmt.Errorf("dhcp: list scopes for vrf %s: %w", vrf.ID, err)
	}
	var scopes []*models.DHCPScope
	for _, s := range all {
		if s.IsEnabled {
			scopes = append(scopes, s)
		}
	}
	return scopes, nil
}

// selectByGiaddr resolves the scope (and its VRF) that owns a relay agent's
// giaddr, per RFC 2131 §4.1: the relay's IP identifies which subnet/VRF the
// client is on.
func (e *Engine) selectByGiaddr(ctx context.Context, giaddr net.IP) (*servingContext, error) {
	scope, err := e.store.FindScopeByGiaddr(ctx, giaddr)
	if err != nil {
		return nil, fmt.Errorf("dhcp: find scope by giaddr %s: %w", giaddr, err)
	}
	if scope == nil {
		return nil, fmt.Errorf("%w: giaddr %s", ErrRelayNotSupported, giaddr)
	}
	vrf, err := e.store.GetVRFByID(ctx, scope.VRFID)
	if err != nil {
		return nil, fmt.Errorf("dhcp: get vrf %s: %w", scope.VRFID, err)
	}
	return &servingContext{VRF: vrf, Scopes: []*models.DHCPScope{scope}, Relayed: true, Giaddr: giaddr}, nil
}

// scopesContainingIP lists the enabled scopes in vrfID whose cached network
// (dhcp_scopes.network) contains ip.
func (e *Engine) scopesContainingIP(ctx context.Context, vrfID string, ip net.IP) ([]*models.DHCPScope, error) {
	scopes, err := e.store.GetScopesByVRF(ctx, vrfID)
	if err != nil {
		return nil, fmt.Errorf("dhcp: list scopes for vrf %s: %w", vrfID, err)
	}
	var out []*models.DHCPScope
	for _, s := range scopes {
		if !s.IsEnabled || s.Network == nil {
			continue
		}
		n := net.IPNet(*s.Network)
		if n.Contains(ip) {
			out = append(out, s)
		}
	}
	return out, nil
}

// vrfForInterface resolves the VRF for a receiving interface: store lookup
// first (server_interfaces, refreshed on every call subject to vrfCacheTTL),
// falling back to the VRF ID configured for that interface in config.DHCP.
// Interfaces (amendment: "config-file fallback") if the store lookup fails
// or returns nothing.
func (e *Engine) vrfForInterface(ctx context.Context, ifname string) (*models.VRF, error) {
	if v := e.cachedVRF(ifname); v != nil {
		return v, nil
	}

	vrf, storeErr := e.store.GetVRFByInterface(ctx, ifname)
	if storeErr == nil && vrf != nil {
		e.cacheVRF(ifname, vrf)
		return vrf, nil
	}

	if fallbackID := e.configuredVRFID(ifname); fallbackID != "" {
		if v2, err2 := e.store.GetVRFByID(ctx, fallbackID); err2 == nil && v2 != nil {
			e.cacheVRF(ifname, v2)
			return v2, nil
		}
	}

	if storeErr != nil {
		return nil, fmt.Errorf("dhcp: resolve vrf for interface %s: %w", ifname, storeErr)
	}
	return nil, fmt.Errorf("%w: no vrf resolvable for interface %s", ErrScopeNotFound, ifname)
}

// configuredVRFID returns the vrf_id configured in config.DHCP.Interfaces
// for ifname, or "" if not configured there.
func (e *Engine) configuredVRFID(ifname string) string {
	for _, i := range e.cfg.DHCP.Interfaces {
		if i.Name == ifname {
			return i.VRFID
		}
	}
	return ""
}

func (e *Engine) cachedVRF(ifname string) *models.VRF {
	e.vrfCacheMu.RLock()
	defer e.vrfCacheMu.RUnlock()
	entry, ok := e.vrfCache[ifname]
	if !ok || time.Now().After(entry.expires) {
		return nil
	}
	return entry.vrf
}

func (e *Engine) cacheVRF(ifname string, vrf *models.VRF) {
	e.vrfCacheMu.Lock()
	defer e.vrfCacheMu.Unlock()
	e.vrfCache[ifname] = vrfCacheEntry{vrf: vrf, expires: time.Now().Add(vrfCacheTTL)}
}

// isZeroIP reports whether ip is unset/unspecified (0.0.0.0), as used
// throughout RFC 2131 §4.1's giaddr/ciaddr branching.
func isZeroIP(ip net.IP) bool {
	return ip == nil || ip.IsUnspecified()
}

// serverIdentifierFor returns the IPv4 address this server should present
// as Option 54 (Server Identifier) and as the source when replying on
// ifname: the HA cluster VIP when HA is enabled and configured (the
// ServingGate having already confirmed this node holds it), else the
// interface's own local address.
func (e *Engine) serverIdentifierFor(ifname string) net.IP {
	if e.cfg.HA.Enabled && e.cfg.HA.VIPAddress != "" {
		if ip, _, err := net.ParseCIDR(e.cfg.HA.VIPAddress); err == nil && ip.To4() != nil {
			return ip.To4()
		}
		if ip := net.ParseIP(e.cfg.HA.VIPAddress); ip != nil && ip.To4() != nil {
			return ip.To4()
		}
	}
	if addrs, err := interfaceLocalAddrs(ifname); err == nil && len(addrs) > 0 {
		return addrs[0]
	}
	return net.IPv4zero
}
