package dhcp

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"

	"github.com/mdlayher/packet"
	"golang.org/x/sys/unix"
)

// rawSender delivers a reply straight to a client's hardware address over an
// AF_PACKET socket, bypassing the host's routing and ARP.
//
// This exists because of RFC 2131 §4.1's yiaddr case. When a client in the
// INIT state does not set the BOOTP broadcast flag -- which is the default
// for ISC dhclient, busybox udhcpc, Windows and macOS -- the server is
// expected to address the reply to the address it is *about to* give the
// client. Sending that through the ordinary UDP socket makes the kernel ARP
// for yiaddr first, and nothing answers: the client cannot ARP for an address
// it has not been given yet. The datagram is then dropped in the queue and
// the client never sees an OFFER at all.
//
// Real DHCP servers (ISC dhcpd, dnsmasq, Kea) solve this by writing the
// frame themselves, addressed to the client's chaddr. So does this. The
// socket is opened in packet.Datagram mode, so the kernel still builds the
// Ethernet header from the destination hardware address we hand it; the IPv4
// and UDP headers below are ours because no routing decision may be involved.
//
// Requires CAP_NET_RAW, which deploy/docker/Dockerfile's setcap and
// deploy/systemd/gdhcp.service's AmbientCapabilities already grant. When the
// socket cannot be opened the engine logs once and falls back to the plain
// UDP path, which still works for every client that does set the broadcast
// flag.
type rawSender struct {
	ifname string

	mu   sync.Mutex // serializes WriteTo; one sender is shared per interface
	conn *packet.Conn
}

func newRawSender(ifname string) (*rawSender, error) {
	ifi, err := net.InterfaceByName(ifname)
	if err != nil {
		return nil, fmt.Errorf("dhcp: raw sender: lookup interface %s: %w", ifname, err)
	}
	conn, err := packet.Listen(ifi, packet.Datagram, unix.ETH_P_IP, nil)
	if err != nil {
		return nil, fmt.Errorf("dhcp: raw sender: open AF_PACKET socket on %s: %w", ifname, err)
	}
	return &rawSender{ifname: ifname, conn: conn}, nil
}

func (r *rawSender) Close() error {
	if r == nil || r.conn == nil {
		return nil
	}
	return r.conn.Close()
}

// sendUDP writes one IPv4/UDP datagram to dstMAC.
func (r *rawSender) sendUDP(src, dst net.IP, srcPort, dstPort uint16, payload []byte, dstMAC net.HardwareAddr) error {
	if len(dstMAC) == 0 {
		return fmt.Errorf("dhcp: raw sender: empty destination hardware address")
	}
	frame, err := buildIPv4UDP(src, dst, srcPort, dstPort, payload)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.conn.WriteTo(frame, &packet.Addr{HardwareAddr: dstMAC}); err != nil {
		return fmt.Errorf("dhcp: raw sender: write to %s on %s: %w", dstMAC, r.ifname, err)
	}
	return nil
}

// buildIPv4UDP assembles an IPv4 header plus a UDP header around payload.
func buildIPv4UDP(src, dst net.IP, srcPort, dstPort uint16, payload []byte) ([]byte, error) {
	src4, dst4 := src.To4(), dst.To4()
	if src4 == nil {
		return nil, fmt.Errorf("dhcp: raw sender: source %v is not IPv4", src)
	}
	if dst4 == nil {
		return nil, fmt.Errorf("dhcp: raw sender: destination %v is not IPv4", dst)
	}

	const ipHeaderLen, udpHeaderLen = 20, 8
	udpLen := udpHeaderLen + len(payload)
	total := ipHeaderLen + udpLen
	if total > 65535 {
		return nil, fmt.Errorf("dhcp: raw sender: datagram too large (%d bytes)", total)
	}

	buf := make([]byte, total)

	// IPv4 header. No options, so IHL is 5 words; DF is not set and the
	// datagram is small enough never to need fragmenting.
	buf[0] = 0x45
	buf[1] = 0x00 // DSCP/ECN
	binary.BigEndian.PutUint16(buf[2:4], uint16(total))
	binary.BigEndian.PutUint16(buf[4:6], 0) // identification
	binary.BigEndian.PutUint16(buf[6:8], 0) // flags + fragment offset
	buf[8] = 64                             // TTL
	buf[9] = unix.IPPROTO_UDP
	copy(buf[12:16], src4)
	copy(buf[16:20], dst4)
	binary.BigEndian.PutUint16(buf[10:12], onesComplementChecksum(buf[:ipHeaderLen]))

	// UDP header, then the payload it covers.
	udp := buf[ipHeaderLen:]
	binary.BigEndian.PutUint16(udp[0:2], srcPort)
	binary.BigEndian.PutUint16(udp[2:4], dstPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(udpLen))
	copy(udp[udpHeaderLen:], payload)
	binary.BigEndian.PutUint16(udp[6:8], udpChecksum(src4, dst4, udp))

	return buf, nil
}

// udpChecksum computes the UDP checksum over the IPv4 pseudo-header and the
// UDP datagram. A computed zero is transmitted as 0xffff, since zero is
// reserved to mean "no checksum" (RFC 768).
func udpChecksum(src, dst net.IP, udp []byte) uint16 {
	pseudo := make([]byte, 12+len(udp))
	copy(pseudo[0:4], src)
	copy(pseudo[4:8], dst)
	pseudo[8] = 0
	pseudo[9] = unix.IPPROTO_UDP
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(udp)))
	copy(pseudo[12:], udp)

	if sum := onesComplementChecksum(pseudo); sum != 0 {
		return sum
	}
	return 0xffff
}

// onesComplementChecksum is the standard Internet checksum (RFC 1071).
func onesComplementChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i : i+2]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
