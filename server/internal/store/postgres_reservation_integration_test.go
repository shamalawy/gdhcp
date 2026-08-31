package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// TestPostgresReservationBindingsAreHonoured is an integration test against a
// real database: it creates one reservation per binding type in a scratch
// scope, then allocates as a client presenting that identifier and asserts the
// reserved address comes back.
//
// This is the regression guard for the defect where reservations were stored
// with a bare binding_key ("aa:bb:cc:dd:ee:ff") while the allocator looked up
// type-prefixed candidates ("mac:aa:bb:cc:dd:ee:ff"). Nothing failed loudly --
// clients simply got dynamic addresses -- so only an end-to-end allocation
// check catches a regression here.
//
// Skipped unless GDHCP_TEST_DSN names a database that has been migrated;
// everything it writes is rolled back into a scratch VRF that it deletes.
func TestPostgresReservationBindingsAreHonoured(t *testing.T) {
	dsn := os.Getenv("GDHCP_TEST_DSN")
	if dsn == "" {
		t.Skip("set GDHCP_TEST_DSN to run reservation binding integration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// Registered before the fixture cleanup so that, cleanups running LIFO,
	// the pool is closed last -- a deferred Close() here would run first and
	// leave every cleanup statement executing against a closed pool.
	t.Cleanup(pool.Close)
	st := NewPostgres(pool)

	vrfID, prefixID, scopeID, poolID := seedReservationFixture(ctx, t, pool)
	t.Cleanup(func() {
		// dhcp_pools, reservations and leases all cascade from the scope.
		for _, stmt := range []struct {
			sql string
			id  string
		}{
			{`DELETE FROM dhcp_scopes WHERE id = $1`, scopeID},
			{`DELETE FROM ipam_prefixes WHERE id = $1`, prefixID},
			{`DELETE FROM vrfs WHERE id = $1`, vrfID},
		} {
			if _, err := pool.Exec(ctx, stmt.sql, stmt.id); err != nil {
				t.Errorf("cleanup %q: %v", stmt.sql, err)
			}
		}
	})

	// One reservation per binding type, each on its own address.
	cases := []struct {
		name  string
		typ   models.BindingType
		raw   string
		ip    string
		ident models.ClientIdentity
	}{
		{"mac", models.BindingTypeMAC, "AA:BB:CC:DD:EE:01", "10.99.0.10",
			models.ClientIdentity{MAC: "aa:bb:cc:dd:ee:01"}},
		{"client_id", models.BindingTypeClientID, "01AABBCCDD", "10.99.0.11",
			models.ClientIdentity{MAC: "00:00:00:00:11:01", ClientID: "01aabbccdd"}},
		{"hostname", models.BindingTypeHostname, "Web-SRV-01", "10.99.0.12",
			models.ClientIdentity{MAC: "00:00:00:00:11:02", Hostname: "web-srv-01"}},
		{"circuit_id", models.BindingTypeCircuitID, "eth 1/0/24", "10.99.0.13",
			models.ClientIdentity{MAC: "00:00:00:00:11:03", CircuitID: "65746820312f302f3234"}},
		{"remote_id", models.BindingTypeRemoteID, "DEAD", "10.99.0.14",
			models.ClientIdentity{MAC: "00:00:00:00:11:04", RemoteID: "dead"}},
		{"vendor_class", models.BindingTypeVendorClass, "MSFT 5.0", "10.99.0.15",
			models.ClientIdentity{MAC: "00:00:00:00:11:05", VendorClass: "4d53465420352e30"}},
		{"user_class", models.BindingTypeUserClass, "iPXE", "10.99.0.16",
			models.ClientIdentity{MAC: "00:00:00:00:11:06", UserClass: "69505845"}},
	}

	for _, tc := range cases {
		key, err := models.BuildBindingKey(tc.typ, tc.raw)
		if err != nil {
			t.Fatalf("%s: BuildBindingKey: %v", tc.name, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO dhcp_reservations (pool_id, reserved_ip, binding_key, binding_type, is_enabled)
			VALUES ($1, $2, $3, $4, true)`, poolID, tc.ip, key, string(tc.typ)); err != nil {
			t.Fatalf("%s: insert reservation: %v", tc.name, err)
		}
	}

	hostname := "integration"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lease, err := st.AllocateLease(ctx, poolID, tc.ident, &hostname, 0)
			if err != nil {
				t.Fatalf("AllocateLease: %v", err)
			}
			defer pool.Exec(ctx, `DELETE FROM dhcp_leases WHERE id = $1`, lease.ID)
			if got := lease.IPAddress.String(); got != tc.ip {
				t.Errorf("allocated %s, want the reserved %s -- the %s reservation did not match",
					got, tc.ip, tc.name)
			}
			if lease.ClientHostname == nil || *lease.ClientHostname != hostname {
				t.Errorf("lease client_hostname = %v, want %q", lease.ClientHostname, hostname)
			}
		})
	}

	// A client presenting both a client_id and a MAC that each have their own
	// reservation must get the client_id one: option 61 outranks the hardware
	// address in models.BindingTypePrecedence.
	t.Run("client_id outranks mac", func(t *testing.T) {
		lease, err := st.AllocateLease(ctx, poolID,
			models.ClientIdentity{MAC: "aa:bb:cc:dd:ee:01", ClientID: "01aabbccdd"}, &hostname, 0)
		if err != nil {
			t.Fatalf("AllocateLease: %v", err)
		}
		defer pool.Exec(ctx, `DELETE FROM dhcp_leases WHERE id = $1`, lease.ID)
		if got := lease.IPAddress.String(); got != "10.99.0.11" {
			t.Errorf("allocated %s, want 10.99.0.11 (the client_id reservation)", got)
		}
	})
}

// seedReservationFixture creates an isolated VRF / prefix / scope / pool for
// the integration test to allocate inside, so it never touches real data.
func seedReservationFixture(ctx context.Context, t *testing.T, pool *pgxpool.Pool) (vrfID, prefixID, scopeID, poolID string) {
	t.Helper()
	// Drop anything a previously interrupted run left behind, so the test is
	// re-runnable against a long-lived dev database.
	pool.Exec(ctx, `DELETE FROM dhcp_scopes WHERE name = 'itest-reservations'`)
	pool.Exec(ctx, `DELETE FROM ipam_prefixes WHERE name = 'itest-reservations'`)
	pool.Exec(ctx, `DELETE FROM vrfs WHERE name = 'itest-reservations'`)
	if err := pool.QueryRow(ctx, `
		INSERT INTO vrfs (name, description) VALUES ('itest-reservations', 'integration test')
		RETURNING id`).Scan(&vrfID); err != nil {
		t.Fatalf("seed vrf: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO ipam_prefixes (vrf_id, prefix, prefix_type, name)
		VALUES ($1, '10.99.0.0/24', 'subnet', 'itest-reservations')
		RETURNING id`, vrfID).Scan(&prefixID); err != nil {
		t.Fatalf("seed prefix: %v", err)
	}
	var clusterID string
	if err := pool.QueryRow(ctx, `SELECT id FROM clusters LIMIT 1`).Scan(&clusterID); err != nil {
		t.Fatalf("seed: no cluster to attach a scope to: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO dhcp_scopes (vrf_id, cluster_id, ipam_prefix_id, name, network, is_enabled)
		VALUES ($1, $2, $3, 'itest-reservations', '10.99.0.0/24', true)
		RETURNING id`, vrfID, clusterID, prefixID).Scan(&scopeID); err != nil {
		t.Fatalf("seed scope: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO dhcp_pools (scope_id, name, range_start, range_end, is_enabled)
		VALUES ($1, 'itest-pool', '10.99.0.10', '10.99.0.200', true)
		RETURNING id`, scopeID).Scan(&poolID); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	return vrfID, prefixID, scopeID, poolID
}
