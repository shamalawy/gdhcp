package dhcp

import (
	"fmt"
	"net"

	"github.com/insomniacslk/dhcp/dhcpv4/server4"
	"golang.org/x/net/ipv4"
)

// InterfaceListener is one per-interface DHCPv4 socket: a broadcast-capable
// UDP socket bound to the interface on port 67 (dhcpv4.ServerPort), opened
// via insomniacslk/dhcp's server4.NewIPv4UDPConn. Engine.serveListener reads
// it directly (rather than through server4.Server.Serve) via the
// golang.org/x/net/ipv4 wrapper below with the IP_PKTINFO control message
// enabled, which is the only way to recover a received datagram's actual
// destination address -- server4.Server.Serve's plain
// net.PacketConn.ReadFrom has no such facility, and dora.go's
// RENEWING-vs-REBINDING classifier needs that address (see
// Engine.handleRequestRenewRebind) to tell a client unicasting straight to
// this server apart from one broadcasting to the whole segment, which the
// DHCP header's client-set BROADCAST flag cannot do.
type InterfaceListener struct {
	ifname string
	conn   *net.UDPConn
	pconn  *ipv4.PacketConn
}

// newInterfaceListener opens the socket described above, bound to ifname on
// addr.
func newInterfaceListener(ifname string, addr *net.UDPAddr) (*InterfaceListener, error) {
	conn, err := server4.NewIPv4UDPConn(ifname, addr)
	if err != nil {
		return nil, err
	}
	pconn := ipv4.NewPacketConn(conn)
	if err := pconn.SetControlMessage(ipv4.FlagDst, true); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("dhcp: enable destination-address capture on %s: %w", ifname, err)
	}
	return &InterfaceListener{ifname: ifname, conn: conn, pconn: pconn}, nil
}

// Close closes the underlying socket, unblocking any in-flight ReadFrom so
// Engine.serveListener's read loop can exit.
func (l *InterfaceListener) Close() error {
	return l.conn.Close()
}

// LocalAddrs returns the interface's currently-configured IPv4 addresses
// (used by vrf.go for direct/shared-network scope matching and by
// serverIdentifierFor for Option 54). It reads the live OS interface state
// rather than a cached value, since these can change (DHCP-assigned
// uplinks, VIP failover) and looking them up is cheap.
func interfaceLocalAddrs(ifname string) ([]net.IP, error) {
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return nil, err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil {
			continue
		}
		if v4 := ip.To4(); v4 != nil {
			ips = append(ips, v4)
		}
	}
	return ips, nil
}
