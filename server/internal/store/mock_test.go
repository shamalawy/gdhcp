package store

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// newTestPool seeds a Mock with one VRF/scope/pool and returns the pool ID.
// The scope's /29 network keeps the .0/.7 network/broadcast addresses
// outside rangeStart/rangeEnd so callers can reason about exact counts.
func newTestPool(t *testing.T, m *Mock, rangeStart, rangeEnd string) string {
	t.Helper()
	ctx := context.Background()

	vrf := &models.VRF{Name: "test-vrf-" + rangeStart, IsDefault: false}
	if err := m.CreateVRF(ctx, vrf); err != nil {
		t.Fatalf("create vrf: %v", err)
	}

	_, network, err := net.ParseCIDR("10.0.0.0/29")
	if err != nil {
		t.Fatalf("parse cidr: %v", err)
	}
	n := models.IPNet(*network)
	scope := &models.DHCPScope{VRFID: vrf.ID, Name: "scope-" + rangeStart, Network: &n, IsEnabled: true}
	if err := m.CreateScope(ctx, scope); err != nil {
		t.Fatalf("create scope: %v", err)
	}

	pool := &models.DHCPPool{
		ScopeID:    scope.ID,
		Name:       "pool-" + rangeStart,
		RangeStart: net.ParseIP(rangeStart),
		RangeEnd:   net.ParseIP(rangeEnd),
		IsEnabled:  true,
	}
	if err := m.CreatePool(ctx, pool); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	return pool.ID
}

func TestMockAllocateLeaseScansAscendingAndReusesAffinity(t *testing.T) {
	ctx := context.Background()
	m := NewMock()
	poolID := newTestPool(t, m, "10.0.0.1", "10.0.0.3") // 3 usable addresses.

	leaseA, err := m.AllocateLease(ctx, poolID, models.ClientIdentity{MAC: "aa:aa:aa:aa:aa:aa"}, nil)
	if err != nil {
		t.Fatalf("allocate for A: %v", err)
	}
	if !leaseA.IPAddress.Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("expected lowest free IP 10.0.0.1 first, got %s", leaseA.IPAddress)
	}

	leaseB, err := m.AllocateLease(ctx, poolID, models.ClientIdentity{MAC: "bb:bb:bb:bb:bb:bb"}, nil)
	if err != nil {
		t.Fatalf("allocate for B: %v", err)
	}
	if !leaseB.IPAddress.Equal(net.ParseIP("10.0.0.2")) {
		t.Errorf("expected next free IP 10.0.0.2, got %s", leaseB.IPAddress)
	}

	// Renew affinity: re-allocating for A must return the same IP rather
	// than scanning for a new one (amendment A6 step 2).
	leaseAAgain, err := m.AllocateLease(ctx, poolID, models.ClientIdentity{MAC: "aa:aa:aa:aa:aa:aa"}, nil)
	if err != nil {
		t.Fatalf("re-allocate for A: %v", err)
	}
	if !leaseAAgain.IPAddress.Equal(leaseA.IPAddress) {
		t.Errorf("expected renew affinity to keep %s, got %s", leaseA.IPAddress, leaseAAgain.IPAddress)
	}
	if leaseAAgain.ID != leaseA.ID {
		t.Errorf("expected renew affinity to reuse the same lease row")
	}

	leaseC, err := m.AllocateLease(ctx, poolID, models.ClientIdentity{MAC: "cc:cc:cc:cc:cc:cc"}, nil)
	if err != nil {
		t.Fatalf("allocate for C: %v", err)
	}
	if !leaseC.IPAddress.Equal(net.ParseIP("10.0.0.3")) {
		t.Errorf("expected 10.0.0.3, got %s", leaseC.IPAddress)
	}

	// Pool is now exhausted: a fourth distinct client must get ErrNoLeases.
	_, err = m.AllocateLease(ctx, poolID, models.ClientIdentity{MAC: "dd:dd:dd:dd:dd:dd"}, nil)
	if !errors.Is(err, models.ErrNoLeases) {
		t.Fatalf("expected ErrNoLeases on exhausted pool, got %v", err)
	}
}

func TestMockAllocateLeasePrefersReservation(t *testing.T) {
	ctx := context.Background()
	m := NewMock()
	poolID := newTestPool(t, m, "10.0.0.1", "10.0.0.4")

	const mac = "aa:bb:cc:dd:ee:ff"
	res := &models.DHCPReservation{
		PoolID:      poolID,
		ReservedIP:  net.ParseIP("10.0.0.3"),
		BindingKey:  "mac:" + mac,
		BindingType: models.BindingTypeMAC,
		IsEnabled:   true,
	}
	if err := m.CreateReservation(ctx, res); err != nil {
		t.Fatalf("create reservation: %v", err)
	}

	lease, err := m.AllocateLease(ctx, poolID, models.ClientIdentity{MAC: mac}, nil)
	if err != nil {
		t.Fatalf("allocate lease: %v", err)
	}
	if !lease.IPAddress.Equal(net.ParseIP("10.0.0.3")) {
		t.Errorf("expected reserved IP 10.0.0.3, got %s", lease.IPAddress)
	}
}

func TestMockCanAllocateInPoolRejectsReservedAndOutOfRange(t *testing.T) {
	ctx := context.Background()
	m := NewMock()
	poolID := newTestPool(t, m, "10.0.0.1", "10.0.0.6")

	res := &models.DHCPReservation{
		PoolID: poolID, ReservedIP: net.ParseIP("10.0.0.4"),
		BindingKey: "mac:11:22:33:44:55:66", BindingType: models.BindingTypeMAC, IsEnabled: true,
	}
	if err := m.CreateReservation(ctx, res); err != nil {
		t.Fatalf("create reservation: %v", err)
	}

	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.2", true},   // free, in range
		{"10.0.0.4", false},  // reserved
		{"10.0.0.0", false},  // scope network address, outside pool range anyway
		{"10.0.0.10", false}, // outside pool range
	}
	for _, c := range cases {
		got, err := m.CanAllocateInPool(ctx, poolID, net.ParseIP(c.ip))
		if err != nil {
			t.Fatalf("CanAllocateInPool(%s): %v", c.ip, err)
		}
		if got != c.want {
			t.Errorf("CanAllocateInPool(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestMockResolveOptionInheritancePrecedence(t *testing.T) {
	ctx := context.Background()
	m := NewMock()
	poolID := newTestPool(t, m, "10.0.0.1", "10.0.0.6")
	pool, _ := m.GetPoolByID(ctx, poolID)
	scopeID := pool.ScopeID

	res := &models.DHCPReservation{
		PoolID: poolID, ReservedIP: net.ParseIP("10.0.0.2"),
		BindingKey: "mac:aa:aa:aa:aa:aa:aa", BindingType: models.BindingTypeMAC, IsEnabled: true,
	}
	if err := m.CreateReservation(ctx, res); err != nil {
		t.Fatalf("create reservation: %v", err)
	}

	tmpl := &models.Template{Name: "tmpl-1", IsEnabled: true}
	if err := m.CreateTemplate(ctx, tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if _, err := m.AssociateTemplateScope(ctx, tmpl.ID, scopeID, 10); err != nil {
		t.Fatalf("associate template: %v", err)
	}

	const code = int16(3) // routers
	setValue := func(level models.OptionScopeLevel, entityID, value string) {
		t.Helper()
		if err := m.CreateOptionValue(ctx, &models.DHCPOption{
			OptionCode: code, ScopeLevel: level, ScopeEntityID: entityID, Value: value, IsEnabled: true,
		}); err != nil {
			t.Fatalf("create option value (%s/%s): %v", level, entityID, err)
		}
	}

	// Nothing set anywhere: resolves to nil.
	v, err := m.ResolveOption(ctx, code, scopeID, poolID, res.ID)
	if err != nil {
		t.Fatalf("resolve (empty): %v", err)
	}
	if v != nil {
		t.Fatalf("expected nil before any value is set, got %+v", v)
	}

	setValue(models.OptionScopeGlobal, globalScopeEntityID, "global-value")
	if v, _ := m.ResolveOption(ctx, code, scopeID, poolID, res.ID); v == nil || v.Value != "global-value" {
		t.Fatalf("expected global-value, got %+v", v)
	}

	setValue(models.OptionScopeTemplate, tmpl.ID, "template-value")
	if v, _ := m.ResolveOption(ctx, code, scopeID, poolID, res.ID); v == nil || v.Value != "template-value" {
		t.Fatalf("expected template to beat global, got %+v", v)
	}

	setValue(models.OptionScopeScope, scopeID, "scope-value")
	if v, _ := m.ResolveOption(ctx, code, scopeID, poolID, res.ID); v == nil || v.Value != "scope-value" {
		t.Fatalf("expected scope to beat template, got %+v", v)
	}

	setValue(models.OptionScopePool, poolID, "pool-value")
	if v, _ := m.ResolveOption(ctx, code, scopeID, poolID, res.ID); v == nil || v.Value != "pool-value" {
		t.Fatalf("expected pool to beat scope, got %+v", v)
	}

	setValue(models.OptionScopeReservation, res.ID, "reservation-value")
	if v, _ := m.ResolveOption(ctx, code, scopeID, poolID, res.ID); v == nil || v.Value != "reservation-value" {
		t.Fatalf("expected reservation to beat pool, got %+v", v)
	}
}

func TestMockConfigChangeNotify(t *testing.T) {
	ctx := context.Background()
	m := NewMock()

	notifications, closeFn, err := m.ListenConfigChanges(ctx, ConfigChangeChannel)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer closeFn()

	vrf := &models.VRF{Name: "notify-vrf"}
	if err := m.CreateVRF(ctx, vrf); err != nil {
		t.Fatalf("create vrf: %v", err)
	}

	select {
	case <-notifications:
	case <-time.After(time.Second):
		t.Fatal("expected a config-change notification after CreateVRF")
	}

	if err := closeFn(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, ok := <-notifications; ok {
		t.Fatal("expected channel to be closed after closeFn")
	}
}

func TestMockGlobalSearchMatchesAcrossEntityTypes(t *testing.T) {
	ctx := context.Background()
	m := NewMock()
	poolID := newTestPool(t, m, "10.0.0.1", "10.0.0.6")

	res := &models.DHCPReservation{
		PoolID: poolID, ReservedIP: net.ParseIP("10.0.0.5"),
		BindingKey: "mac:aa:bb:cc:dd:ee:ff", BindingType: models.BindingTypeMAC, IsEnabled: true,
		Hostname: strPtr("printer-lobby"),
	}
	if err := m.CreateReservation(ctx, res); err != nil {
		t.Fatalf("create reservation: %v", err)
	}

	results, err := m.GlobalSearch(ctx, "printer", nil, 10)
	if err != nil {
		t.Fatalf("global search: %v", err)
	}
	found := false
	for _, r := range results {
		if r.Type == models.SearchResultReservation && r.EntityID == res.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected search for %q to find reservation %s, got %+v", "printer", res.ID, results)
	}

	// entityTypes filter excludes non-matching kinds.
	filtered, err := m.GlobalSearch(ctx, "printer", []string{"vrf"}, 10)
	if err != nil {
		t.Fatalf("global search (filtered): %v", err)
	}
	if len(filtered) != 0 {
		t.Fatalf("expected no results when filtering to vrf type, got %+v", filtered)
	}
}

// TestMockGlobalSearchNeverReturnsUserOrServer guards the fix for the
// user-directory leak: GET /api/v1/search is viewer-accessible (any
// authenticated principal, per router.go's reg.get), but the user
// directory (username/email) is meant to be admin-only, gated on /users.
// GlobalSearch must never surface 'user' (or 'server', dropped alongside it
// for consistency) results — not even when a caller explicitly asks for
// them via entityTypes, and not only when entityTypes is left unfiltered.
func TestMockGlobalSearchNeverReturnsUserOrServer(t *testing.T) {
	ctx := context.Background()
	m := NewMock()

	if err := m.CreateUser(ctx, &models.User{Username: "admin", Email: "admin@example.com", Role: models.RoleAdmin, IsActive: true}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := m.RegisterServer(ctx, &models.ClusterServer{NodeName: "admin-node", APIAddress: "10.0.0.9:8443"}); err != nil {
		t.Fatalf("register server: %v", err)
	}

	for _, tc := range []struct {
		name        string
		entityTypes []string
	}{
		{"unfiltered", nil},
		{"explicit user", []string{"user"}},
		{"explicit server", []string{"server"}},
		{"explicit both", []string{"user", "server"}},
	} {
		results, err := m.GlobalSearch(ctx, "admin", tc.entityTypes, 10)
		if err != nil {
			t.Fatalf("%s: global search: %v", tc.name, err)
		}
		for _, r := range results {
			if r.Type == models.SearchResultUser || r.Type == models.SearchResultServer {
				t.Fatalf("%s: search leaked a %q result (%+v) to a non-admin-gated caller", tc.name, r.Type, r)
			}
		}
	}
}

func strPtr(s string) *string { return &s }

// TestMockGlobalSearchEmptyQueryReturnsEmptySlice guards the fix for the
// "search says something went wrong" defect: GlobalSearch used to return a
// nil slice, which the API encodes as JSON `null`, and the frontend then
// iterated that and threw. Both stores must answer with an empty slice so
// the wire form is `[]`.
func TestMockGlobalSearchEmptyQueryReturnsEmptySlice(t *testing.T) {
	ctx := context.Background()
	m := NewMock()

	for _, query := range []string{"", "   ", "no-such-entity-anywhere"} {
		results, err := m.GlobalSearch(ctx, query, nil, 10)
		if err != nil {
			t.Fatalf("global search %q: %v", query, err)
		}
		if results == nil {
			t.Errorf("global search %q returned nil; want an empty slice so the API encodes [] not null", query)
		}
		if len(results) != 0 {
			t.Errorf("global search %q returned %d results, want 0", query, len(results))
		}
	}
}

// TestMockGlobalSearchURLsAreFrontendRoutes pins the other half of that
// defect: a hit whose `url` doesn't resolve in the Angular router is as
// useless as no hit at all. Every URL must start with the feature prefix
// the SPA actually mounts (/ipam, /dhcp, /ha) — the old SQL emitted bare
// /scopes/<id>, /leases/<id> and friends, all of which 404'd.
func TestMockGlobalSearchURLsAreFrontendRoutes(t *testing.T) {
	ctx := context.Background()
	m := NewMock()
	poolID := newTestPool(t, m, "10.0.0.1", "10.0.0.6")

	if err := m.CreateReservation(ctx, &models.DHCPReservation{
		PoolID: poolID, ReservedIP: net.ParseIP("10.0.0.5"),
		BindingKey: "mac:aa:bb:cc:dd:ee:ff", BindingType: models.BindingTypeMAC, IsEnabled: true,
		Hostname: strPtr("findme-printer"),
	}); err != nil {
		t.Fatalf("create reservation: %v", err)
	}
	if err := m.CreateVLAN(ctx, &models.VLAN{VID: 4001, Name: "findme-vlan", Status: models.VLANStatusActive}); err != nil {
		t.Fatalf("create vlan: %v", err)
	}

	prefixes := map[models.SearchResultType]string{
		models.SearchResultVRF:         "/ipam/vrfs/",
		models.SearchResultPrefix:      "/ipam/prefixes/",
		models.SearchResultVLAN:        "/ipam/vlans/",
		models.SearchResultVLANGroup:   "/ipam/vlans?group=",
		models.SearchResultScope:       "/dhcp/scopes/",
		models.SearchResultPool:        "/dhcp/pools/",
		models.SearchResultReservation: "/dhcp/pools/",
		models.SearchResultLease:       "/dhcp/leases/",
		models.SearchResultTemplate:    "/dhcp/templates/",
		models.SearchResultOption:      "/dhcp/options?q=",
		models.SearchResultCluster:     "/ha/clusters/",
	}

	results, err := m.GlobalSearch(ctx, "findme", nil, 50)
	if err != nil {
		t.Fatalf("global search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result for \"findme\"")
	}
	for _, r := range results {
		want, ok := prefixes[r.Type]
		if !ok {
			t.Errorf("result type %q has no known frontend route", r.Type)
			continue
		}
		if !strings.HasPrefix(r.URL, want) {
			t.Errorf("%s result url = %q, want it to start with %q", r.Type, r.URL, want)
		}
	}
}

func TestMockLeaseAdminActions(t *testing.T) {
	ctx := context.Background()
	m := NewMock()
	poolID := newTestPool(t, m, "10.0.0.1", "10.0.0.6")

	lease, err := m.AllocateLease(ctx, poolID, models.ClientIdentity{MAC: "aa:bb:cc:dd:ee:01"}, strPtr("box-1"))
	if err != nil {
		t.Fatalf("allocate lease: %v", err)
	}

	t.Run("force renew reactivates a released lease", func(t *testing.T) {
		if err := m.ReleaseLease(ctx, lease.ID, "aa:bb:cc:dd:ee:01"); err != nil {
			t.Fatalf("release: %v", err)
		}
		expiry := time.Now().UTC().Add(30 * time.Minute)
		renewed, err := m.ForceRenewLease(ctx, lease.ID, expiry)
		if err != nil {
			t.Fatalf("force renew: %v", err)
		}
		if renewed.BindingState != models.LeaseStateActive {
			t.Errorf("binding_state = %q, want active", renewed.BindingState)
		}
		if !renewed.LeaseEnd.Equal(expiry) {
			t.Errorf("lease_end = %v, want %v", renewed.LeaseEnd, expiry)
		}
	})

	t.Run("force renew rejects an expiry in the past", func(t *testing.T) {
		if _, err := m.ForceRenewLease(ctx, lease.ID, time.Now().UTC().Add(-time.Minute)); !errors.Is(err, models.ErrInvalidInput) {
			t.Errorf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("delete removes the lease", func(t *testing.T) {
		if err := m.DeleteLease(ctx, lease.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := m.GetLease(ctx, lease.ID); !errors.Is(err, models.ErrNotFound) {
			t.Errorf("get after delete: err = %v, want ErrNotFound", err)
		}
		if err := m.DeleteLease(ctx, lease.ID); !errors.Is(err, models.ErrLeaseNotFound) {
			t.Errorf("second delete: err = %v, want ErrLeaseNotFound", err)
		}
	})
}

// TestMockListLeasesSearch covers the leases page's one search box: a single
// query has to reach the IP, the MAC and the hostname, since an operator
// typing "10.0.0" or "aa:bb" or "box" should not have to say which field
// they meant.
func TestMockListLeasesSearch(t *testing.T) {
	ctx := context.Background()
	m := NewMock()
	poolID := newTestPool(t, m, "10.0.0.1", "10.0.0.6")

	if _, err := m.AllocateLease(ctx, poolID, models.ClientIdentity{MAC: "aa:bb:cc:dd:ee:01"}, strPtr("alpha-box")); err != nil {
		t.Fatalf("allocate lease 1: %v", err)
	}
	if _, err := m.AllocateLease(ctx, poolID, models.ClientIdentity{MAC: "11:22:33:44:55:66"}, strPtr("beta-box")); err != nil {
		t.Fatalf("allocate lease 2: %v", err)
	}

	for _, tc := range []struct {
		name, search string
		want         int
	}{
		{"by hostname", "alpha", 1},
		{"by hostname suffix shared by both", "box", 2},
		{"by MAC", "11:22:33", 1},
		{"by IP", "10.0.0.", 2},
		{"no match", "nothing-like-this", 0},
		{"blank matches everything", "", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := m.ListLeases(ctx, ListParams{Search: tc.search, Limit: 50})
			if err != nil {
				t.Fatalf("list leases: %v", err)
			}
			if len(res.Items) != tc.want {
				t.Errorf("search %q matched %d leases, want %d", tc.search, len(res.Items), tc.want)
			}
		})
	}
}
