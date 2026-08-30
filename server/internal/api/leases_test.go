package api

// leases_test.go covers the lease action endpoints added alongside the
// leases page: the single-lease routes, the bulk route's partial-result
// contract, and the RBAC line between reading leases (viewer) and acting on
// them (operator).

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

func seedActiveLease(st *mockStore, ip string) *models.DHCPLease {
	start := time.Now().UTC().Add(-time.Hour)
	return st.seedLease(&models.DHCPLease{
		PoolID:       "pool-1",
		IPAddress:    net.ParseIP(ip),
		BindingState: models.LeaseStateActive,
		LeaseStart:   start,
		LeaseEnd:     start.Add(2 * time.Hour),
	})
}

func TestLeaseActions_SingleLease(t *testing.T) {
	t.Run("release marks the lease released", func(t *testing.T) {
		h, st := newTestServer()
		token := loginAs(t, h, st, "op", models.RoleOperator)
		lease := seedActiveLease(st, "10.0.0.10")

		rec := doRequest(t, h, http.MethodPost, "/api/v1/leases/"+lease.ID+"/release", token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if lease.BindingState != models.LeaseStateReleased {
			t.Errorf("binding_state = %q, want released", lease.BindingState)
		}
	})

	t.Run("abandon marks the lease abandoned", func(t *testing.T) {
		h, st := newTestServer()
		token := loginAs(t, h, st, "op", models.RoleOperator)
		lease := seedActiveLease(st, "10.0.0.11")

		rec := doRequest(t, h, http.MethodPost, "/api/v1/leases/"+lease.ID+"/abandon", token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if lease.BindingState != models.LeaseStateAbandoned {
			t.Errorf("binding_state = %q, want abandoned", lease.BindingState)
		}
	})

	t.Run("renew with no body reuses the lease's own duration", func(t *testing.T) {
		h, st := newTestServer()
		token := loginAs(t, h, st, "op", models.RoleOperator)
		lease := seedActiveLease(st, "10.0.0.12") // seeded with a 2h span

		before := time.Now().UTC()
		rec := doRequest(t, h, http.MethodPost, "/api/v1/leases/"+lease.ID+"/renew", token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		got := lease.LeaseEnd.Sub(before)
		if got < 110*time.Minute || got > 130*time.Minute {
			t.Errorf("new lease_end is %v away, want ~2h (the lease's original span)", got)
		}
	})

	t.Run("renew honours an explicit duration", func(t *testing.T) {
		h, st := newTestServer()
		token := loginAs(t, h, st, "op", models.RoleOperator)
		lease := seedActiveLease(st, "10.0.0.13")

		before := time.Now().UTC()
		rec := doRequest(t, h, http.MethodPost, "/api/v1/leases/"+lease.ID+"/renew", token,
			map[string]any{"duration_seconds": 600})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		got := lease.LeaseEnd.Sub(before)
		if got < 9*time.Minute || got > 11*time.Minute {
			t.Errorf("new lease_end is %v away, want ~10m", got)
		}
	})

	t.Run("renew rejects a negative duration", func(t *testing.T) {
		h, st := newTestServer()
		token := loginAs(t, h, st, "op", models.RoleOperator)
		lease := seedActiveLease(st, "10.0.0.14")

		rec := doRequest(t, h, http.MethodPost, "/api/v1/leases/"+lease.ID+"/renew", token,
			map[string]any{"duration_seconds": -1})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("delete removes the lease", func(t *testing.T) {
		h, st := newTestServer()
		token := loginAs(t, h, st, "op", models.RoleOperator)
		lease := seedActiveLease(st, "10.0.0.15")

		rec := doRequest(t, h, http.MethodDelete, "/api/v1/leases/"+lease.ID, token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if rec := doRequest(t, h, http.MethodGet, "/api/v1/leases/"+lease.ID, token, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET after delete = %d, want 404", rec.Code)
		}
	})

	t.Run("unknown lease is a 404, not a 500", func(t *testing.T) {
		h, st := newTestServer()
		token := loginAs(t, h, st, "op", models.RoleOperator)

		rec := doRequest(t, h, http.MethodPost, "/api/v1/leases/does-not-exist/release", token, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestLeaseActions_Bulk(t *testing.T) {
	t.Run("reports per-lease outcomes instead of failing as a whole", func(t *testing.T) {
		h, st := newTestServer()
		token := loginAs(t, h, st, "op", models.RoleOperator)
		good := seedActiveLease(st, "10.0.1.10")

		rec := doRequest(t, h, http.MethodPost, "/api/v1/leases/bulk", token, map[string]any{
			"action":    "release",
			"lease_ids": []string{good.ID, "no-such-lease"},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		resp := decodeJSON[bulkResponseBody](t, rec)
		if resp.Succeeded != 1 || resp.Failed != 1 {
			t.Fatalf("succeeded/failed = %d/%d, want 1/1; body=%s", resp.Succeeded, resp.Failed, rec.Body.String())
		}
		if len(resp.Results) != 2 {
			t.Fatalf("got %d results, want 2", len(resp.Results))
		}
		if !resp.Results[0].OK {
			t.Errorf("first result should have succeeded, got error %q", resp.Results[0].Error)
		}
		if resp.Results[1].OK || resp.Results[1].Error != "lease not found" {
			t.Errorf("second result = %+v, want a 'lease not found' failure", resp.Results[1])
		}
		// The one good lease must still have been acted on.
		if good.BindingState != models.LeaseStateReleased {
			t.Errorf("binding_state = %q, want released", good.BindingState)
		}
	})

	t.Run("rejects an unknown action", func(t *testing.T) {
		h, st := newTestServer()
		token := loginAs(t, h, st, "op", models.RoleOperator)

		rec := doRequest(t, h, http.MethodPost, "/api/v1/leases/bulk", token, map[string]any{
			"action": "nuke", "lease_ids": []string{"x"},
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("rejects an empty selection", func(t *testing.T) {
		h, st := newTestServer()
		token := loginAs(t, h, st, "op", models.RoleOperator)

		rec := doRequest(t, h, http.MethodPost, "/api/v1/leases/bulk", token, map[string]any{
			"action": "release", "lease_ids": []string{},
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("caps the number of leases in one request", func(t *testing.T) {
		h, st := newTestServer()
		token := loginAs(t, h, st, "op", models.RoleOperator)

		ids := make([]string, 501)
		for i := range ids {
			ids[i] = "id"
		}
		rec := doRequest(t, h, http.MethodPost, "/api/v1/leases/bulk", token, map[string]any{
			"action": "release", "lease_ids": ids,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
	})
}

// TestLeaseActions_RBAC pins the line this feature moved: leases were a
// read-only resource, so every mutation route here is new attack surface
// and a viewer must not reach any of it.
func TestLeaseActions_RBAC(t *testing.T) {
	routes := []struct {
		name, method, path string
		body               any
	}{
		{"release", http.MethodPost, "/api/v1/leases/LEASE/release", nil},
		{"abandon", http.MethodPost, "/api/v1/leases/LEASE/abandon", nil},
		{"renew", http.MethodPost, "/api/v1/leases/LEASE/renew", map[string]any{}},
		{"delete", http.MethodDelete, "/api/v1/leases/LEASE", nil},
		{"bulk", http.MethodPost, "/api/v1/leases/bulk", map[string]any{"action": "release", "lease_ids": []string{"LEASE"}}},
	}

	for _, route := range routes {
		t.Run(route.name+" is forbidden for a viewer", func(t *testing.T) {
			h, st := newTestServer()
			token := loginAs(t, h, st, "viewer", models.RoleViewer)
			lease := seedActiveLease(st, "10.0.2.10")
			path := replaceLeaseID(route.path, lease.ID)

			rec := doRequest(t, h, route.method, path, token, route.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
			}
			if lease.BindingState != models.LeaseStateActive {
				t.Errorf("lease was mutated despite the 403: state = %q", lease.BindingState)
			}
		})
	}

	t.Run("a viewer can still read leases", func(t *testing.T) {
		h, st := newTestServer()
		token := loginAs(t, h, st, "viewer", models.RoleViewer)
		lease := seedActiveLease(st, "10.0.2.11")

		rec := doRequest(t, h, http.MethodGet, "/api/v1/leases/"+lease.ID, token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
	})
}

// bulkResponseBody mirrors the leases package's unexported response shape so
// the test can assert on it across the package boundary.
type bulkResponseBody struct {
	Action    string `json:"action"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
	Results   []struct {
		LeaseID string `json:"lease_id"`
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
	} `json:"results"`
}

func replaceLeaseID(path, id string) string {
	const placeholder = "LEASE"
	for i := 0; i+len(placeholder) <= len(path); i++ {
		if path[i:i+len(placeholder)] == placeholder {
			return path[:i] + id + path[i+len(placeholder):]
		}
	}
	return path
}
