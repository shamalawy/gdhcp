package store

import (
	"net"
	"testing"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

func TestIPToBigIntRoundTrip(t *testing.T) {
	cases := []string{"10.0.0.1", "192.168.1.255", "0.0.0.0", "255.255.255.255", "2001:db8::1"}
	for _, s := range cases {
		ip := net.ParseIP(s)
		byteLen := 4
		if ip.To4() == nil {
			byteLen = 16
		}
		got := bigIntToIP(ipToBigInt(ip), byteLen)
		if !got.Equal(ip) {
			t.Errorf("round-trip %s: got %s", s, got)
		}
	}
}

func TestIPToBigIntOrdering(t *testing.T) {
	a := ipToBigInt(net.ParseIP("10.0.0.1"))
	b := ipToBigInt(net.ParseIP("10.0.0.2"))
	if a.Cmp(b) >= 0 {
		t.Fatalf("expected 10.0.0.1 < 10.0.0.2 as big.Int, got a=%s b=%s", a, b)
	}
}

func TestIPInRange(t *testing.T) {
	start := net.ParseIP("10.0.0.10")
	end := net.ParseIP("10.0.0.20")

	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.9", false},
		{"10.0.0.10", true},
		{"10.0.0.15", true},
		{"10.0.0.20", true},
		{"10.0.0.21", false},
	}
	for _, c := range cases {
		got := ipInRange(net.ParseIP(c.ip), start, end)
		if got != c.want {
			t.Errorf("ipInRange(%s, [%s,%s]) = %v, want %v", c.ip, start, end, got, c.want)
		}
	}
}

func TestCompareIP(t *testing.T) {
	a := net.ParseIP("10.0.0.1").To16()
	b := net.ParseIP("10.0.0.2").To16()
	if compareIP(a, b) >= 0 {
		t.Fatalf("expected a < b")
	}
	if compareIP(b, a) <= 0 {
		t.Fatalf("expected b > a")
	}
	if compareIP(a, a) != 0 {
		t.Fatalf("expected a == a")
	}
}

func TestNetworksOverlap(t *testing.T) {
	_, a, _ := net.ParseCIDR("10.0.0.0/24")
	_, b, _ := net.ParseCIDR("10.0.0.128/25")
	_, c, _ := net.ParseCIDR("10.0.1.0/24")

	if !networksOverlap(a, b) {
		t.Errorf("expected %s and %s to overlap", a, b)
	}
	if networksOverlap(a, c) {
		t.Errorf("expected %s and %s not to overlap", a, c)
	}
}

func TestOverlapsAny(t *testing.T) {
	_, candidate, _ := net.ParseCIDR("10.0.0.0/25")
	_, existing1, _ := net.ParseCIDR("10.0.1.0/25")
	_, existing2, _ := net.ParseCIDR("10.0.0.64/26")

	if overlapsAny(candidate, []*net.IPNet{existing1}) {
		t.Errorf("expected no overlap against %s", existing1)
	}
	if !overlapsAny(candidate, []*net.IPNet{existing1, existing2}) {
		t.Errorf("expected overlap against %s", existing2)
	}
}

// cidrNetworkAndBroadcast and poolTotalAddresses back GetPoolUtilization's
// "range size minus network/broadcast handling" requirement (mock.go); this
// exercises the pure math without a DHCPPool/DHCPScope fixture.
func TestCidrNetworkAndBroadcast(t *testing.T) {
	_, n, _ := net.ParseCIDR("10.0.0.0/24")
	network, broadcast := cidrNetworkAndBroadcast(n)
	if !network.Equal(net.ParseIP("10.0.0.0")) {
		t.Errorf("network = %s, want 10.0.0.0", network)
	}
	if !broadcast.Equal(net.ParseIP("10.0.0.255")) {
		t.Errorf("broadcast = %s, want 10.0.0.255", broadcast)
	}
}

func TestPoolTotalAddressesExcludesNetworkAndBroadcast(t *testing.T) {
	_, network, _ := net.ParseCIDR("10.0.0.0/24")
	n := models.IPNet(*network)
	scope := &models.DHCPScope{Network: &n}

	// Pool spans the whole /24, so both the network and broadcast address
	// fall inside its range and must be excluded from the usable total.
	pool := &models.DHCPPool{
		RangeStart: net.ParseIP("10.0.0.0"),
		RangeEnd:   net.ParseIP("10.0.0.255"),
	}
	got := poolTotalAddresses(pool, scope)
	want := int64(254) // 256 addresses minus network (.0) and broadcast (.255).
	if got != want {
		t.Errorf("poolTotalAddresses = %d, want %d", got, want)
	}

	// A pool range that excludes the network/broadcast entirely (typical
	// "usable range" convention) should not subtract anything further.
	poolInner := &models.DHCPPool{
		RangeStart: net.ParseIP("10.0.0.10"),
		RangeEnd:   net.ParseIP("10.0.0.20"),
	}
	got = poolTotalAddresses(poolInner, scope)
	want = 11 // 10..20 inclusive.
	if got != want {
		t.Errorf("poolTotalAddresses (inner range) = %d, want %d", got, want)
	}
}

func TestPoolTotalAddressesNoScope(t *testing.T) {
	pool := &models.DHCPPool{
		RangeStart: net.ParseIP("10.0.0.10"),
		RangeEnd:   net.ParseIP("10.0.0.10"),
	}
	got := poolTotalAddresses(pool, nil)
	if got != 1 {
		t.Errorf("poolTotalAddresses with nil scope = %d, want 1", got)
	}
}
