package ha

import (
	"net"
	"net/netip"
	"time"

	"github.com/mdlayher/arp"
	"github.com/mdlayher/ethernet"
)

// gratuitousARPBurstSize and gratuitousARPSpacing match
// docs/design/ha-cluster.md's "3-5 copies with 100-200 ms spacing" and this
// unit's task scope ("3x 200ms apart").
const (
	gratuitousARPBurstSize = 3
	gratuitousARPSpacing   = 200 * time.Millisecond
)

// sendGratuitousARP announces vip as now belonging to this node's
// cfg.HA.VIPInterface, so peers' ARP caches update without waiting for a
// stale entry to expire. It is entirely best-effort: opening a raw ARP
// socket needs CAP_NET_RAW, which — like CAP_NET_ADMIN for the VIP itself —
// may not be granted, and vip.go's assignVIP has already succeeded (the VIP
// is live) by the time this runs, so any failure here is logged and
// swallowed rather than affecting HA state in any way. Called as its own
// goroutine (not tracked by Manager's shutdown WaitGroup — it is bounded to
// well under a second and losing it during a fast Stop is harmless).
func (m *Manager) sendGratuitousARP(vip net.IP) {
	if m.cfg.HA.VIPInterface == "" {
		return
	}

	ip4 := vip.To4()
	if ip4 == nil {
		m.log.Warn("ha: gratuitous ARP: VIP is not an IPv4 address, skipping", "vip", vip.String())
		return
	}
	addr, ok := netip.AddrFromSlice(ip4)
	if !ok {
		return
	}

	ifi, err := net.InterfaceByName(m.cfg.HA.VIPInterface)
	if err != nil {
		m.log.Warn("ha: gratuitous ARP: interface lookup failed (best-effort, non-fatal)",
			"interface", m.cfg.HA.VIPInterface, "error", err)
		return
	}

	client, err := arp.Dial(ifi)
	if err != nil {
		m.log.Warn("ha: gratuitous ARP: failed to open raw ARP socket — likely missing CAP_NET_RAW (best-effort, non-fatal)",
			"interface", m.cfg.HA.VIPInterface, "error", err)
		return
	}
	defer client.Close()

	// Gratuitous ARP: an ARP request announcing "I have this address",
	// sender protocol address == target protocol address, sent to the
	// broadcast hardware address so every peer on the segment updates its
	// cache unprompted.
	pkt, err := arp.NewPacket(arp.OperationRequest, ifi.HardwareAddr, addr, ethernet.Broadcast, addr)
	if err != nil {
		m.log.Warn("ha: gratuitous ARP: failed to build packet", "error", err)
		return
	}

	for i := 0; i < gratuitousARPBurstSize; i++ {
		if err := client.WriteTo(pkt, ethernet.Broadcast); err != nil {
			m.log.Warn("ha: gratuitous ARP: send failed (best-effort, non-fatal)", "attempt", i+1, "error", err)
		}
		if i < gratuitousARPBurstSize-1 {
			time.Sleep(gratuitousARPSpacing)
		}
	}
	m.log.Info("ha: gratuitous ARP burst sent", "vip", vip.String(), "interface", m.cfg.HA.VIPInterface)
}
