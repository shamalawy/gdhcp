package ha

import (
	"strings"

	"github.com/vishvananda/netlink"
)

// assignVIP claims cfg.HA.VIPAddress on cfg.HA.VIPInterface via
// netlink.AddrReplace (add-or-replace, so promotion is idempotent even if a
// stale address survived an unclean shutdown), brings the interface up, and
// fires a best-effort gratuitous ARP burst.
//
// The VIP CIDR and interface are read from cfg.HA directly rather than the
// DB-backed Cluster row (m.cluster): models.Cluster.VIPAddress is typed
// *net.IP, which cannot round-trip a subnet prefix, so "10.0.0.10/24" from
// config would lose its /24 by the time it came back from the store. See
// notes_for_integration.
//
// Per this unit's task scope, any failure here — a missing interface, or
// (most commonly) a missing CAP_NET_ADMIN — is logged loudly and otherwise
// swallowed: this node keeps running as PRIMARY and keeps serving DHCP
// without owning the VIP, rather than transitioning to StateError. A
// container/binary run without CAP_NET_ADMIN is expected to degrade this
// way, not crash-loop.
func (m *Manager) assignVIP() {
	if m.cfg.HA.VIPAddress == "" || m.cfg.HA.VIPInterface == "" {
		return
	}

	addr, err := netlink.ParseAddr(m.cfg.HA.VIPAddress)
	if err != nil {
		m.log.Error("ha: invalid ha.vip_address in config, cannot assign VIP",
			"vip_address", m.cfg.HA.VIPAddress, "error", err)
		return
	}

	link, err := netlink.LinkByName(m.cfg.HA.VIPInterface)
	if err != nil {
		m.log.Error("ha: VIP interface not found, cannot assign VIP (degrading gracefully; this node keeps serving DHCP without owning the VIP)",
			"interface", m.cfg.HA.VIPInterface, "error", err)
		m.metricsRec.vipAssignmentFailure(m.clusterID)
		m.metricsRec.setVIPAssigned(m.clusterID, false)
		return
	}

	if err := netlink.AddrReplace(link, addr); err != nil {
		m.log.Error("ha: failed to assign VIP — likely missing CAP_NET_ADMIN (degrading gracefully; this node keeps serving DHCP without owning the VIP)",
			"vip_address", m.cfg.HA.VIPAddress, "interface", m.cfg.HA.VIPInterface, "error", err)
		m.metricsRec.vipAssignmentFailure(m.clusterID)
		m.metricsRec.setVIPAssigned(m.clusterID, false)
		return
	}

	if err := netlink.LinkSetUp(link); err != nil {
		m.log.Warn("ha: failed to set VIP interface up (non-fatal)", "interface", m.cfg.HA.VIPInterface, "error", err)
	}

	m.log.Info("ha: VIP assigned", "vip_address", m.cfg.HA.VIPAddress, "interface", m.cfg.HA.VIPInterface)
	m.metricsRec.setVIPAssigned(m.clusterID, true)

	go m.sendGratuitousARP(addr.IPNet.IP)
}

// removeVIP releases the VIP claimed by assignVIP. It is idempotent and
// non-fatal by design (docs/design/ha-cluster.md: "VIP removal is
// non-fatal; if it fails, the server remains reachable but does not serve
// DHCP" — under this unit's task scope that "does not serve DHCP" only
// applies if the demotion itself also drops this node out of
// PRIMARY/SECONDARY; a failed AddrDel never blocks the state transition).
func (m *Manager) removeVIP() {
	if m.cfg.HA.VIPAddress == "" || m.cfg.HA.VIPInterface == "" {
		return
	}

	addr, err := netlink.ParseAddr(m.cfg.HA.VIPAddress)
	if err != nil {
		return
	}

	link, err := netlink.LinkByName(m.cfg.HA.VIPInterface)
	if err != nil {
		m.metricsRec.setVIPAssigned(m.clusterID, false)
		return
	}

	if err := netlink.AddrDel(link, addr); err != nil {
		if !isBenignAddrDelErr(err) {
			m.log.Warn("ha: failed to remove VIP (non-fatal)", "vip_address", m.cfg.HA.VIPAddress, "interface", m.cfg.HA.VIPInterface, "error", err)
		}
	} else {
		m.log.Info("ha: VIP removed", "vip_address", m.cfg.HA.VIPAddress, "interface", m.cfg.HA.VIPInterface)
	}
	m.metricsRec.setVIPAssigned(m.clusterID, false)
}

// isBenignAddrDelErr recognizes the "it just wasn't there" family of
// AddrDel failures (address already gone, interface has no such address,
// or — on the !linux build of netlink — not implemented at all) that
// AddrDel's idempotency contract expects to be silent.
func isBenignAddrDelErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "cannot assign requested address") ||
		strings.Contains(s, "no such") ||
		strings.Contains(s, "not implemented")
}
