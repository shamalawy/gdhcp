package dhcp

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"

	"github.com/shamalawy/gdhcp/server/internal/config"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

// ---------------------------------------------------------------------------
// encodeOptionValue: table test covering every catalog value_type.
// ---------------------------------------------------------------------------

func TestEncodeOptionValue(t *testing.T) {
	cases := []struct {
		name    string
		vt      models.OptionValueType
		raw     string
		want    []byte
		wantErr bool
	}{
		{"string", models.OptionValueTypeString, "example.org", []byte("example.org"), false},
		{"uint8", models.OptionValueTypeUint8, "200", []byte{200}, false},
		{"uint8 out of range", models.OptionValueTypeUint8, "300", nil, true},
		{"uint16", models.OptionValueTypeUint16, "1500", []byte{0x05, 0xDC}, false},
		{"uint32", models.OptionValueTypeUint32, "86400", []byte{0x00, 0x01, 0x51, 0x80}, false},
		{"boolean true", models.OptionValueTypeBoolean, "true", []byte{1}, false},
		{"boolean false", models.OptionValueTypeBoolean, "false", []byte{0}, false},
		{"boolean 1", models.OptionValueTypeBoolean, "1", []byte{1}, false},
		{"boolean invalid", models.OptionValueTypeBoolean, "maybe", nil, true},
		{"ip", models.OptionValueTypeIP, "10.0.0.1", []byte{10, 0, 0, 1}, false},
		{"ip invalid", models.OptionValueTypeIP, "not-an-ip", nil, true},
		{"ip_list", models.OptionValueTypeIPList, "10.0.0.1, 10.0.0.2", []byte{10, 0, 0, 1, 10, 0, 0, 2}, false},
		{"bytes hex", models.OptionValueTypeBytes, "de:ad:be:ef", []byte{0xde, 0xad, 0xbe, 0xef}, false},
		{"bytes hex 0x prefix", models.OptionValueTypeBytes, "0xCAFE", []byte{0xca, 0xfe}, false},
		{"fqdn", models.OptionValueTypeFQDN, "host.example.com", nil, false}, // checked structurally below
		{"route_list", models.OptionValueTypeRouteList, "10.1.0.0/24:10.0.0.1,0.0.0.0/0:10.0.0.1", nil, false},
		{"unsupported type", models.OptionValueType("nonsense"), "x", nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encodeOptionValue(tc.vt, tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("encodeOptionValue(%s, %q) = %v, want error", tc.vt, tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("encodeOptionValue(%s, %q) unexpected error: %v", tc.vt, tc.raw, err)
			}

			switch tc.vt {
			case models.OptionValueTypeFQDN:
				// RFC 1035 wire format: length-prefixed labels, zero-terminated.
				want := []byte{4, 'h', 'o', 's', 't', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0}
				if !bytes.Equal(got, want) {
					t.Fatalf("fqdn encode = %v, want %v", got, want)
				}
			case models.OptionValueTypeRouteList:
				routes := dhcpv4.Routes{}
				if err := (&routes).FromBytes(got); err != nil {
					t.Fatalf("route_list output did not round-trip: %v", err)
				}
				if len(routes) != 2 {
					t.Fatalf("route_list decoded %d routes, want 2", len(routes))
				}
			default:
				if !bytes.Equal(got, tc.want) {
					t.Fatalf("encodeOptionValue(%s, %q) = %v, want %v", tc.vt, tc.raw, got, tc.want)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// computeTimers: T1 50%/40% split, T2 87.5%.
// ---------------------------------------------------------------------------

func TestComputeTimers(t *testing.T) {
	e := &Engine{cfg: &config.Config{}, store: newMockStore(), log: slog.New(slog.NewTextHandler(bytesDiscard{}, nil))}

	e.cfg.HA.Enabled = false
	t1, t2 := e.computeTimers(context.Background(), 86400)
	if t1 != 43200 {
		t.Errorf("standalone T1 = %d, want 43200 (50%%)", t1)
	}
	if t2 != 75600 {
		t.Errorf("T2 = %d, want 75600 (87.5%%)", t2)
	}

	e.cfg.HA.Enabled = true
	e.cfg.HA.ClusterID = "cluster1"
	ms := e.store.(*mockStore)
	ms.servers = []*models.ClusterServer{{ID: "s1"}, {ID: "s2"}}
	t1, t2 = e.computeTimers(context.Background(), 86400)
	if t1 != 34560 {
		t.Errorf("HA (2+ servers) T1 = %d, want 34560 (40%%)", t1)
	}
	if t2 != 75600 {
		t.Errorf("T2 = %d, want 75600 (87.5%%)", t2)
	}

	ms.servers = []*models.ClusterServer{{ID: "s1"}}
	ms.clusterSizeCallCount = 0
	e.clusterSizeAt = time.Time{} // bust the cache so the single-server count is re-read
	t1, _ = e.computeTimers(context.Background(), 86400)
	if t1 != 43200 {
		t.Errorf("HA (1 server) T1 = %d, want 43200 (50%%)", t1)
	}
}

type bytesDiscard struct{}

func (bytesDiscard) Write(p []byte) (int, error) { return len(p), nil }

// ---------------------------------------------------------------------------
// DORA handling against a local store mock.
// ---------------------------------------------------------------------------

func TestEngine_HandleDiscover_Success(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01}
	req, err := dhcpv4.NewDiscovery(mac)
	if err != nil {
		t.Fatalf("NewDiscovery: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, req, "testif0", nil)

	if conn.lastPacket == nil {
		t.Fatal("no reply was sent")
	}
	resp, err := dhcpv4.FromBytes(conn.lastPacket)
	if err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if resp.MessageType() != dhcpv4.MessageTypeOffer {
		t.Fatalf("message type = %s, want OFFER", resp.MessageType())
	}
	if !resp.YourIPAddr.Equal(net.IPv4(10, 0, 0, 10)) {
		t.Fatalf("offered ip = %s, want 10.0.0.10", resp.YourIPAddr)
	}
	if lt := resp.IPAddressLeaseTime(0); lt != 3600*time.Second {
		t.Fatalf("lease time = %s, want 3600s", lt)
	}
	dns := resp.DNS()
	if len(dns) != 2 || !dns[0].Equal(net.IPv4(10, 0, 0, 1)) || !dns[1].Equal(net.IPv4(10, 0, 0, 2)) {
		t.Fatalf("dns servers = %v, want [10.0.0.1 10.0.0.2] (from resolved scope option 6)", dns)
	}
	if mask := resp.SubnetMask(); mask == nil || net.IP(mask).String() != "255.255.255.0" {
		t.Fatalf("subnet mask = %v, want 255.255.255.0 (derived from scope network)", mask)
	}
	if sid := resp.ServerIdentifier(); !sid.Equal(net.IPv4(10, 0, 0, 254)) {
		t.Fatalf("server identifier = %s, want 10.0.0.254 (HA VIP)", sid)
	}

	if env.store.discoverCount["scope1"] != 1 || env.store.offerCount["scope1"] != 1 {
		t.Fatalf("expected one DISCOVER and one OFFER recorded for scope1, got discover=%d offer=%d",
			env.store.discoverCount["scope1"], env.store.offerCount["scope1"])
	}
}

func TestEngine_HandleDiscover_NoLeasesAvailable(t *testing.T) {
	env := newTestEnv(t)
	env.store.allocateErr = models.ErrNoLeases

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x02}
	req, err := dhcpv4.NewDiscovery(mac)
	if err != nil {
		t.Fatalf("NewDiscovery: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, req, "testif0", nil)

	if conn.lastPacket != nil {
		t.Fatalf("expected no reply when the pool is exhausted, got %d bytes", len(conn.lastPacket))
	}
	if env.store.noLeasesCount["scope1"] != 1 {
		t.Fatalf("expected RecordNoLeases(scope1) once, got %d", env.store.noLeasesCount["scope1"])
	}
}

func TestEngine_HandleRequestRenew_Success(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x03}
	leaseIP := net.IPv4(10, 0, 0, 11).To4()
	env.store.leasesByPoolIP["pool1|10.0.0.11"] = &models.DHCPLease{
		ID: "lease-renew", PoolID: "pool1", IPAddress: leaseIP,
		ClientMAC: hwAddrPtr(mac), BindingState: models.LeaseStateActive,
		LeaseStart: time.Now().Add(-time.Hour), LeaseEnd: time.Now().Add(time.Hour),
	}

	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeRequest),
		dhcpv4.WithClientIP(leaseIP),
		dhcpv4.WithBroadcast(false),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	conn := &fakePacketConn{}
	// dst = the HA VIP: a RENEWING client unicasts straight to the server
	// address it was given, which is what pkt.Dst now captures.
	env.engine.dispatch(conn, &net.UDPAddr{IP: leaseIP, Port: 68}, req, "testif0", net.IPv4(10, 0, 0, 254))

	if conn.lastPacket == nil {
		t.Fatal("no reply was sent for renew")
	}
	resp, err := dhcpv4.FromBytes(conn.lastPacket)
	if err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if resp.MessageType() != dhcpv4.MessageTypeAck {
		t.Fatalf("message type = %s, want ACK", resp.MessageType())
	}
	if !resp.YourIPAddr.Equal(leaseIP) {
		t.Fatalf("acked ip = %s, want %s", resp.YourIPAddr, leaseIP)
	}
	if !env.store.renewCalled {
		t.Fatal("expected RenewLease to be called")
	}
}

// TestEngine_HandleRequestRebind_Relayed_NoLease_StaysSilent guards the
// giaddr-based REBINDING signal: a relayed REQUEST (ciaddr set, giaddr set,
// no server-id, BROADCAST flag unset as any compliant client leaves it) for
// a lease this server has no record of must stay silent per RFC 2131
// §4.3.2, not NAK — a relay agent only ever forwards broadcast REBINDING
// traffic, never a unicast RENEW, so a nonzero giaddr rules RENEWING out
// even when the packet's destination address (dst, below) looks renew-like
// on its own.
func TestEngine_HandleRequestRebind_Relayed_NoLease_StaysSilent(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x04}
	ciaddr := net.IPv4(10, 0, 0, 15).To4()
	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeRequest),
		dhcpv4.WithClientIP(ciaddr),
		dhcpv4.WithGatewayIP(net.IPv4(10, 0, 0, 1).To4()),
		dhcpv4.WithBroadcast(false),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 67}, req, "testif0", net.IPv4(10, 0, 0, 254))

	if conn.lastPacket != nil {
		t.Fatalf("expected silence for a relayed REBINDING with no lease record, got a reply")
	}
}

// TestEngine_HandleRequestRebind_Unrelayed_NoLease_StaysSilent guards the
// bug this replaced: an unrelayed REBINDING client (ciaddr set, no giaddr,
// no server-id, BROADCAST flag unset as any compliant client leaves it —
// indistinguishable from RENEWING by IsUnicast() alone) broadcasts its
// REQUEST to 255.255.255.255. dst reflects that real destination, so a
// lease this server has no record of must stay silent per RFC 2131 §4.3.2,
// not NAK the whole segment.
func TestEngine_HandleRequestRebind_Unrelayed_NoLease_StaysSilent(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x06}
	ciaddr := net.IPv4(10, 0, 0, 17).To4()
	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeRequest),
		dhcpv4.WithClientIP(ciaddr),
		dhcpv4.WithBroadcast(false),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: ciaddr, Port: 68}, req, "testif0", net.IPv4bcast)

	if conn.lastPacket != nil {
		t.Fatalf("expected silence for an unrelayed REBINDING with no lease record, got a reply")
	}
}

// TestEngine_HandleRequestRenew_LeaseNotFound_NaksWithServerIdentifier
// guards two things together: a direct (unrelayed) unicast RENEW -- dst set
// to this server's own address, per the isRenewing classifier in dora.go --
// for a lease this server has no record of must NAK (RFC 2131 §4.3.2), and
// that NAK must carry option 54 (server identifier) on the wire, which
// every NAK this server sends is required to include.
func TestEngine_HandleRequestRenew_LeaseNotFound_NaksWithServerIdentifier(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x05}
	ciaddr := net.IPv4(10, 0, 0, 16).To4()
	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeRequest),
		dhcpv4.WithClientIP(ciaddr),
		dhcpv4.WithBroadcast(false),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: ciaddr, Port: 68}, req, "testif0", net.IPv4(10, 0, 0, 254))

	if conn.lastPacket == nil {
		t.Fatal("expected a NAK for a direct RENEW with no lease record, got no reply")
	}
	resp, err := dhcpv4.FromBytes(conn.lastPacket)
	if err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if resp.MessageType() != dhcpv4.MessageTypeNak {
		t.Fatalf("message type = %s, want NAK", resp.MessageType())
	}
	if sid := resp.ServerIdentifier(); sid == nil || !sid.Equal(net.IPv4(10, 0, 0, 254)) {
		t.Fatalf("NAK server identifier = %v, want 10.0.0.254 (RFC 2131 Table 3: MUST be present)", sid)
	}
}

// TestEngine_BuildClientIdentity_WithHostname verifies that option 12 (hostname)
// is extracted from a DHCP packet and passed to AllocateLease via ClientIdentity.
func TestEngine_BuildClientIdentity_WithHostname(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x07}
	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeDiscover),
		dhcpv4.WithOption(dhcpv4.OptHostName("MyDevice")),
	)
	if err != nil {
		t.Fatalf("build discover: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, req, "testif0", nil)

	// Verify that AllocateLease was called with the hostname
	if env.store.allocateLastHostname == nil {
		t.Fatal("expected hostname to be passed to AllocateLease, got nil")
	}
	if *env.store.allocateLastHostname != "mydevice" {
		t.Errorf("hostname = %q, want %q (lowercased)", *env.store.allocateLastHostname, "mydevice")
	}
	if env.store.allocateLastIdent.Hostname != "mydevice" {
		t.Errorf("ClientIdentity.Hostname = %q, want %q", env.store.allocateLastIdent.Hostname, "mydevice")
	}
}

// TestEngine_BuildClientIdentity_WithClientID verifies that option 61
// (client identifier) is extracted and included in ClientIdentity.
func TestEngine_BuildClientIdentity_WithClientID(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x08}
	clientID := []byte{0x01, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeDiscover),
		dhcpv4.WithOption(dhcpv4.OptClientIdentifier(clientID)),
	)
	if err != nil {
		t.Fatalf("build discover: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, req, "testif0", nil)

	expectedHex := "01aabbccddeeff"
	if env.store.allocateLastIdent.ClientID != expectedHex {
		t.Errorf("ClientIdentity.ClientID = %q, want %q", env.store.allocateLastIdent.ClientID, expectedHex)
	}
}

// TestEngine_BuildClientIdentity_WithVendorClass verifies that option 60
// (vendor class identifier) is extracted as hex and included in ClientIdentity.
func TestEngine_BuildClientIdentity_WithVendorClass(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x09}
	vendorClass := []byte{0xde, 0xad, 0xbe, 0xef}
	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeDiscover),
		dhcpv4.WithOption(dhcpv4.OptGeneric(dhcpv4.GenericOptionCode(60), vendorClass)),
	)
	if err != nil {
		t.Fatalf("build discover: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, req, "testif0", nil)

	expectedHex := "deadbeef"
	if env.store.allocateLastIdent.VendorClass != expectedHex {
		t.Errorf("ClientIdentity.VendorClass = %q, want %q", env.store.allocateLastIdent.VendorClass, expectedHex)
	}
}

// TestEngine_BuildClientIdentity_WithUserClass verifies that option 77
// (user class) is extracted as hex and included in ClientIdentity.
func TestEngine_BuildClientIdentity_WithUserClass(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x0a}
	userClass := []byte{0xca, 0xfe}
	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeDiscover),
		dhcpv4.WithOption(dhcpv4.OptGeneric(dhcpv4.GenericOptionCode(77), userClass)),
	)
	if err != nil {
		t.Fatalf("build discover: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, req, "testif0", nil)

	expectedHex := "cafe"
	if env.store.allocateLastIdent.UserClass != expectedHex {
		t.Errorf("ClientIdentity.UserClass = %q, want %q", env.store.allocateLastIdent.UserClass, expectedHex)
	}
}

// TestEngine_ClientIdentity_ContainsMAC verifies that the client's hardware
// address (chaddr) is always included in ClientIdentity.
func TestEngine_ClientIdentity_ContainsMAC(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeDiscover),
	)
	if err != nil {
		t.Fatalf("build discover: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, req, "testif0", nil)

	expectedMAC := "aa:bb:cc:dd:ee:ff"
	if env.store.allocateLastIdent.MAC != expectedMAC {
		t.Errorf("ClientIdentity.MAC = %q, want %q", env.store.allocateLastIdent.MAC, expectedMAC)
	}
}

// TestEngine_BindingKeys_CorrectPrecedence verifies that ClientIdentity.BindingKeys()
// returns keys in the correct match precedence order per the design decision.
func TestEngine_BindingKeys_CorrectPrecedence(t *testing.T) {
	ident := models.ClientIdentity{
		MAC:         "aa:bb:cc:dd:ee:ff",
		ClientID:    "01aabbcc",
		Hostname:    "myhost",
		CircuitID:   "deadbeef",
		RemoteID:    "cafebabe",
		VendorClass: "1234",
		UserClass:   "5678",
	}

	keys := ident.BindingKeys()
	expectedOrder := []string{
		"client_id:01aabbcc",
		"mac:aa:bb:cc:dd:ee:ff",
		"circuit_id:deadbeef",
		"remote_id:cafebabe",
		"vendor_class:1234",
		"user_class:5678",
		"hostname:myhost",
	}

	if len(keys) != len(expectedOrder) {
		t.Fatalf("BindingKeys() returned %d keys, want %d", len(keys), len(expectedOrder))
	}
	for i, key := range keys {
		if key != expectedOrder[i] {
			t.Errorf("key[%d] = %q, want %q", i, key, expectedOrder[i])
		}
	}
}

func hwAddrPtr(mac net.HardwareAddr) *models.HardwareAddr {
	h := models.HardwareAddr(mac)
	return &h
}

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

type testEnv struct {
	engine *Engine
	store  *mockStore
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	_, network, err := net.ParseCIDR("10.0.0.0/24")
	if err != nil {
		t.Fatalf("parse cidr: %v", err)
	}
	scopeNet := models.IPNet(*network)

	vrf := &models.VRF{ID: "vrf1", Name: "default", IsDefault: true}
	scope := &models.DHCPScope{ID: "scope1", VRFID: "vrf1", Name: "s1", Network: &scopeNet, IsEnabled: true}
	pool := &models.DHCPPool{
		ID: "pool1", ScopeID: "scope1", Name: "p1",
		RangeStart: net.IPv4(10, 0, 0, 10).To4(), RangeEnd: net.IPv4(10, 0, 0, 20).To4(),
		IsEnabled: true, Priority: 0,
	}

	ms := newMockStore()
	ms.vrf = vrf
	ms.scopesByVRF[vrf.ID] = []*models.DHCPScope{scope}
	ms.poolsByScope[scope.ID] = []*models.DHCPPool{pool}
	ms.scopeOptions["scope1"] = []*models.DHCPOption{
		{
			ID: "opt-dns", OptionCode: 6, ScopeLevel: models.OptionScopeScope, ScopeEntityID: "scope1",
			Value: "10.0.0.1,10.0.0.2", IsEnabled: true,
			Catalog: &models.OptionCatalogEntry{OptionCode: 6, Name: "Domain Name Server", ValueType: models.OptionValueTypeIPList},
		},
		{
			ID: "opt-lease", OptionCode: 51, ScopeLevel: models.OptionScopeScope, ScopeEntityID: "scope1",
			Value: "3600", IsEnabled: true,
			Catalog: &models.OptionCatalogEntry{OptionCode: 51, Name: "IP Address Lease Time", ValueType: models.OptionValueTypeUint32},
		},
	}
	ms.allocateResult = &models.DHCPLease{
		ID: "lease1", PoolID: "pool1", IPAddress: net.IPv4(10, 0, 0, 10).To4(),
		BindingState: models.LeaseStateActive,
		LeaseStart:   time.Now(), LeaseEnd: time.Now().Add(3600 * time.Second),
	}

	cfg := &config.Config{
		DHCP: config.DHCPConfig{
			Interfaces:       []config.DHCPInterface{{Name: "testif0", VRFID: "vrf1"}},
			DefaultLeaseTime: 86400,
		},
		HA: config.HAConfig{Enabled: true, VIPAddress: "10.0.0.254/24", ClusterID: "cluster1"},
	}

	log := slog.New(slog.NewTextHandler(bytesDiscard{}, nil))
	e, err := New(cfg, ms, nil, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return &testEnv{engine: e, store: ms}
}

// fakePacketConn is a minimal net.PacketConn that records the last WriteTo
// call so tests can decode the reply the engine sent.
type fakePacketConn struct {
	lastPacket []byte
	lastAddr   net.Addr
}

func (f *fakePacketConn) ReadFrom(p []byte) (int, net.Addr, error) { return 0, nil, nil }
func (f *fakePacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	f.lastPacket = append([]byte(nil), p...)
	f.lastAddr = addr
	return len(p), nil
}
func (f *fakePacketConn) Close() error                       { return nil }
func (f *fakePacketConn) LocalAddr() net.Addr                { return &net.UDPAddr{} }
func (f *fakePacketConn) SetDeadline(t time.Time) error      { return nil }
func (f *fakePacketConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakePacketConn) SetWriteDeadline(t time.Time) error { return nil }

// TestEngine_HandleDecline_AbandonsRatherThanReleases guards RFC 2131
// §4.3.3: an address the client reported as already in use must not be
// handed straight to the next client that asks. Only AbandonLease holds the
// row back from claimLeaseIP's reclaim clause — ReleaseLease returns it to
// the pool immediately, which is what this handler used to do.
func TestEngine_HandleDecline_AbandonsRatherThanReleases(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x09}
	declinedIP := net.IPv4(10, 0, 0, 12).To4()
	const key = "pool1|10.0.0.12"
	env.store.leasesByPoolIP[key] = &models.DHCPLease{
		ID: "lease-declined", PoolID: "pool1", IPAddress: declinedIP,
		ClientMAC: hwAddrPtr(mac), BindingState: models.LeaseStateActive,
		LeaseStart: time.Now().Add(-time.Minute), LeaseEnd: time.Now().Add(time.Hour),
	}

	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeDecline),
		dhcpv4.WithOption(dhcpv4.OptRequestedIPAddress(declinedIP)),
		dhcpv4.WithBroadcast(false),
	)
	if err != nil {
		t.Fatalf("build decline: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, req, "testif0", net.IPv4(10, 0, 0, 254))

	if conn.lastPacket != nil {
		t.Fatal("DECLINE must not be answered")
	}
	if len(env.store.releasedLeaseIDs) != 0 {
		t.Fatalf("ReleaseLease called for a declined address: %v", env.store.releasedLeaseIDs)
	}
	if got := env.store.abandonedLeaseIDs; len(got) != 1 || got[0] != "lease-declined" {
		t.Fatalf("abandoned lease IDs = %v, want [lease-declined]", got)
	}
	if st := env.store.leasesByPoolIP[key].BindingState; st != models.LeaseStateAbandoned {
		t.Fatalf("binding state = %s, want %s", st, models.LeaseStateAbandoned)
	}
}

// TestEngine_HandleRelease_ReleasesNotAbandons is the counterpart to the
// DECLINE test: RELEASE means "I am done with this address", so it must go
// back into the pool immediately. Getting these two the same way round is
// the whole point — an address released as abandoned would be withheld from
// reallocation until the reaper swept it, quietly shrinking the pool.
func TestEngine_HandleRelease_ReleasesNotAbandons(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x0a}
	leaseIP := net.IPv4(10, 0, 0, 13).To4()
	const key = "pool1|10.0.0.13"
	env.store.leasesByPoolIP[key] = &models.DHCPLease{
		ID: "lease-released", PoolID: "pool1", IPAddress: leaseIP,
		ClientMAC: hwAddrPtr(mac), BindingState: models.LeaseStateActive,
		LeaseStart: time.Now().Add(-time.Minute), LeaseEnd: time.Now().Add(time.Hour),
	}

	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeRelease),
		dhcpv4.WithClientIP(leaseIP),
		dhcpv4.WithBroadcast(false),
	)
	if err != nil {
		t.Fatalf("build release: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: leaseIP, Port: 68}, req, "testif0", net.IPv4(10, 0, 0, 254))

	if conn.lastPacket != nil {
		t.Fatal("RELEASE must not be answered")
	}
	if len(env.store.abandonedLeaseIDs) != 0 {
		t.Fatalf("AbandonLease called for a released address: %v", env.store.abandonedLeaseIDs)
	}
	if got := env.store.releasedLeaseIDs; len(got) != 1 || got[0] != "lease-released" {
		t.Fatalf("released lease IDs = %v, want [lease-released]", got)
	}
}

// TestEngine_HandleInform_OmitsServerIdentifier guards RFC 2131 §3.4: a
// DHCPACK answering a DHCPINFORM should not carry option 54. A client that
// records one may later unicast RENEW traffic at a server holding no lease
// for it. siaddr is unrestricted by §3.4 and is still expected to be set.
func TestEngine_HandleInform_OmitsServerIdentifier(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x0b}
	staticIP := net.IPv4(10, 0, 0, 14).To4()

	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeInform),
		dhcpv4.WithClientIP(staticIP),
		dhcpv4.WithBroadcast(false),
	)
	if err != nil {
		t.Fatalf("build inform: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: staticIP, Port: 68}, req, "testif0", net.IPv4(10, 0, 0, 254))

	if conn.lastPacket == nil {
		t.Fatal("no reply was sent for INFORM")
	}
	resp, err := dhcpv4.FromBytes(conn.lastPacket)
	if err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if resp.MessageType() != dhcpv4.MessageTypeAck {
		t.Fatalf("message type = %s, want ACK", resp.MessageType())
	}
	if sid := resp.ServerIdentifier(); sid != nil {
		t.Fatalf("INFORM ack carries option 54 (%s); RFC 2131 §3.4 says it should not", sid)
	}
	// The options the client actually asked for must still be there —
	// suppressing option 54 must not suppress the reply's payload.
	if got := resp.DNS(); len(got) == 0 {
		t.Fatal("INFORM ack carries no option 6; suppressing option 54 dropped real options too")
	}
	// No lease may be created for an INFORM (RFC 2131 §3.4).
	if env.store.allocateLastLeaseDur != 0 {
		t.Fatal("INFORM allocated a lease")
	}
}

// TestEngine_HandleRequestInitReboot_NaksOutOfScopeIP covers the INIT-REBOOT
// branch's guard: a client returning with an address from some other network
// must be told to start over, not silently ignored.
func TestEngine_HandleRequestInitReboot_NaksOutOfScopeIP(t *testing.T) {
	env := newTestEnv(t)

	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x0c}
	foreignIP := net.IPv4(192, 168, 50, 7).To4() // outside the 10.0.0.0/24 scope

	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeRequest),
		dhcpv4.WithOption(dhcpv4.OptRequestedIPAddress(foreignIP)),
		dhcpv4.WithBroadcast(false),
	)
	if err != nil {
		t.Fatalf("build init-reboot request: %v", err)
	}

	conn := &fakePacketConn{}
	env.engine.dispatch(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, req, "testif0", net.IPv4bcast)

	if conn.lastPacket == nil {
		t.Fatal("no reply was sent for an out-of-scope INIT-REBOOT request")
	}
	resp, err := dhcpv4.FromBytes(conn.lastPacket)
	if err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if resp.MessageType() != dhcpv4.MessageTypeNak {
		t.Fatalf("message type = %s, want NAK for an address outside every served scope", resp.MessageType())
	}
}

// TestComputeTimers_ShortLeaseInvariant pins the clamp at the lengths that
// used to break it: a one-second lease produced T2 = 0 (a rebinding time in
// the past) and a two-second lease produced T1 == T2. Both were sent to
// clients as options 58/59.
func TestComputeTimers_ShortLeaseInvariant(t *testing.T) {
	e := &Engine{cfg: &config.Config{}, store: newMockStore(), log: slog.New(slog.NewTextHandler(bytesDiscard{}, nil))}
	ctx := context.Background()

	for _, lease := range []uint32{1, 2, 3, 4, 5, 10, 60, 600, 3600, 86400} {
		t1, t2 := e.computeTimers(ctx, lease)

		if t1 == 0 {
			t.Errorf("lease=%d: T1 = 0, must be positive", lease)
		}
		if t2 < t1 {
			t.Errorf("lease=%d: T2 (%d) < T1 (%d); rebinding must not precede renewal", lease, t2, t1)
		}
		if t2 > lease {
			t.Errorf("lease=%d: T2 (%d) exceeds the lease itself", lease, t2)
		}
		// T2 == lease leaves the client no REBINDING window at all, so it
		// must hold strictly for every lease long enough to allow it —
		// which is everything but a one-second lease.
		if lease >= 2 && t2 >= lease {
			t.Errorf("lease=%d: T2 (%d) must be strictly less than the lease; "+
				"T2 == lease leaves no rebinding window", lease, t2)
		}
		// Three whole seconds is the minimum needed to place T1 and T2
		// strictly between 0 and the lease; at or above it, the strict
		// ordering the doc comment promises must actually hold.
		if lease >= 3 && !(t1 < t2 && t2 < lease) {
			t.Errorf("lease=%d: want 0 < T1 < T2 < lease, got T1=%d T2=%d", lease, t1, t2)
		}
	}
}
