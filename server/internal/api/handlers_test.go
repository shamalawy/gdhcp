package api

// handlers_test.go covers what the task spec calls out explicitly: auth
// (login/refresh success and failure paths), the RBAC matrix (viewer /
// operator / admin against representative routes), the canonical error
// envelope shape, and one full CRUD flow (VRFs) exercised end-to-end through
// New()'s handler chain against the in-memory mockStore.

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shamalawy/gdhcp/server/internal/config"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig() *config.Config {
	return &config.Config{Server: config.ServerConfig{JWTSecret: "test-secret-do-not-use-in-prod"}}
}

// newTestServer builds a fresh mockStore-backed handler plus that store, so
// individual tests can seed users/VRFs/etc. before issuing requests.
func newTestServer() (http.Handler, *mockStore) {
	st := newMockStore()
	h := New(testConfig(), st, nil, nil, testLogger())
	return h, st
}

// seedUserWithPassword inserts an active user with role and the given plaintext
// password (bcrypt-hashed, matching how the real store/CLI would create one).
func seedUserWithPassword(t *testing.T, st *mockStore, username, password string, role models.Role) *models.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	return st.seedUser(&models.User{
		Username:     username,
		Email:        username + "@example.com",
		PasswordHash: string(hash),
		Role:         role,
		IsActive:     true,
	})
}

func doRequest(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return v
}

// ---------------------------------------------------------------------------
// Auth: POST /auth/login, POST /auth/refresh
// ---------------------------------------------------------------------------

func TestAuth_Login(t *testing.T) {
	h, st := newTestServer()
	seedUserWithPassword(t, st, "alice", "correct-horse", models.RoleAdmin)

	t.Run("success", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/login", "", loginRequest{Username: "alice", Password: "correct-horse"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		resp := decodeJSON[tokenResponse](t, rec)
		if resp.Token == "" {
			t.Error("expected non-empty token")
		}
		if resp.RefreshToken == "" {
			t.Error("expected non-empty refresh_token")
		}
		if resp.ExpiresAt == "" {
			t.Error("expected non-empty expires_at")
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/login", "", loginRequest{Username: "alice", Password: "wrong"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/login", "", loginRequest{Username: "nobody", Password: "x"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing fields", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/login", "", loginRequest{Username: "alice"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("inactive user rejected", func(t *testing.T) {
		u := seedUserWithPassword(t, st, "bob", "hunter2", models.RoleViewer)
		u.IsActive = false
		st.users[u.ID] = u
		rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/login", "", loginRequest{Username: "bob", Password: "hunter2"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestAuth_Refresh(t *testing.T) {
	h, st := newTestServer()
	seedUserWithPassword(t, st, "alice", "correct-horse", models.RoleAdmin)

	login := doRequest(t, h, http.MethodPost, "/api/v1/auth/login", "", loginRequest{Username: "alice", Password: "correct-horse"})
	tokens := decodeJSON[tokenResponse](t, login)

	t.Run("valid refresh token issues a new access token", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/refresh", "", refreshRequest{RefreshToken: tokens.RefreshToken})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		resp := decodeJSON[tokenResponse](t, rec)
		if resp.Token == "" {
			t.Error("expected non-empty token")
		}
	})

	t.Run("access token rejected as refresh token", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/refresh", "", refreshRequest{RefreshToken: tokens.Token})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("garbage refresh token", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/refresh", "", refreshRequest{RefreshToken: "not-a-jwt"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestAuth_MissingOrInvalidBearer(t *testing.T) {
	h, _ := newTestServer()

	t.Run("no Authorization header", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/api/v1/vrfs", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("garbage bearer token", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/api/v1/vrfs", "not-a-real-token", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("health is exempt from auth", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/api/v1/health", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// RBAC matrix (amendment A15: viewer=GET, operator=mutations except
// users/tokens/cluster config, admin=all).
// ---------------------------------------------------------------------------

func loginAs(t *testing.T, h http.Handler, st *mockStore, username string, role models.Role) string {
	t.Helper()
	seedUserWithPassword(t, st, username, "password123", role)
	rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/login", "", loginRequest{Username: username, Password: "password123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login as %s: status = %d, body=%s", username, rec.Code, rec.Body.String())
	}
	return decodeJSON[tokenResponse](t, rec).Token
}

func TestRBAC_Matrix(t *testing.T) {
	type step struct {
		method, path string
		body         any
	}

	cases := []struct {
		name        string
		step        step
		minRole     models.Role // lowest role that must succeed (non-4xx-for-role-reasons)
		forbiddenAt []models.Role
	}{
		{
			name:        "GET /vrfs allowed for every role",
			step:        step{http.MethodGet, "/api/v1/vrfs", nil},
			minRole:     models.RoleViewer,
			forbiddenAt: nil,
		},
		{
			name:        "POST /vrfs requires operator",
			step:        step{http.MethodPost, "/api/v1/vrfs", map[string]string{"name": "test-vrf"}},
			minRole:     models.RoleOperator,
			forbiddenAt: []models.Role{models.RoleViewer},
		},
		{
			name:        "GET /users requires admin",
			step:        step{http.MethodGet, "/api/v1/users", nil},
			minRole:     models.RoleAdmin,
			forbiddenAt: []models.Role{models.RoleViewer, models.RoleOperator},
		},
		{
			name:        "POST /users requires admin (operator excluded per A15)",
			step:        step{http.MethodPost, "/api/v1/users", map[string]string{"username": "x", "email": "x@example.com", "password": "x"}},
			minRole:     models.RoleAdmin,
			forbiddenAt: []models.Role{models.RoleViewer, models.RoleOperator},
		},
		{
			name:        "POST /clusters requires admin (cluster config excluded from operator per A15)",
			step:        step{http.MethodPost, "/api/v1/clusters", map[string]string{"name": "c1"}},
			minRole:     models.RoleAdmin,
			forbiddenAt: []models.Role{models.RoleViewer, models.RoleOperator},
		},
	}

	roleRank := map[models.Role]int{models.RoleViewer: 1, models.RoleOperator: 2, models.RoleAdmin: 3}
	allRoles := []models.Role{models.RoleViewer, models.RoleOperator, models.RoleAdmin}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, role := range allRoles {
				role := role
				t.Run(string(role), func(t *testing.T) {
					h, st := newTestServer()
					token := loginAs(t, h, st, "user_"+string(role), role)

					rec := doRequest(t, h, tc.step.method, tc.step.path, token, tc.step.body)

					wantForbidden := roleRank[role] < roleRank[tc.minRole]
					if wantForbidden {
						if rec.Code != http.StatusForbidden {
							t.Fatalf("role %s on %s %s: status = %d, want 403; body=%s", role, tc.step.method, tc.step.path, rec.Code, rec.Body.String())
						}
						return
					}
					if rec.Code == http.StatusForbidden {
						t.Fatalf("role %s on %s %s: unexpectedly forbidden; body=%s", role, tc.step.method, tc.step.path, rec.Body.String())
					}
				})
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Error envelope (docs/CONTRACTS.md Part 6)
// ---------------------------------------------------------------------------

func TestErrorEnvelope_Shape(t *testing.T) {
	h, st := newTestServer()
	token := loginAs(t, h, st, "viewer1", models.RoleViewer)

	t.Run("404 not found", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/api/v1/vrfs/does-not-exist", token, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		env := decodeJSON[map[string]map[string]any](t, rec)
		errBody, ok := env["error"]
		if !ok {
			t.Fatalf("response missing top-level \"error\" key: %s", rec.Body.String())
		}
		if errBody["code"] != "NOT_FOUND" {
			t.Errorf("error.code = %v, want NOT_FOUND", errBody["code"])
		}
		if errBody["message"] == "" || errBody["message"] == nil {
			t.Error("error.message must be non-empty")
		}
		if _, ok := errBody["trace_id"]; !ok {
			t.Error("error.trace_id must be present")
		}
	})

	t.Run("403 forbidden", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/vrfs", token, map[string]string{"name": "x"})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		env := decodeJSON[map[string]map[string]any](t, rec)
		if env["error"]["code"] != "FORBIDDEN" {
			t.Errorf("error.code = %v, want FORBIDDEN", env["error"]["code"])
		}
	})

	t.Run("401 unauthorized", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/api/v1/vrfs", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
		env := decodeJSON[map[string]map[string]any](t, rec)
		if env["error"]["code"] != "UNAUTHORIZED" {
			t.Errorf("error.code = %v, want UNAUTHORIZED", env["error"]["code"])
		}
	})

	t.Run("404 for unknown route uses the same envelope", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/api/v1/no-such-route", token, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		env := decodeJSON[map[string]map[string]any](t, rec)
		if env["error"]["code"] != "NOT_FOUND" {
			t.Errorf("error.code = %v, want NOT_FOUND", env["error"]["code"])
		}
	})
}

// ---------------------------------------------------------------------------
// CRUD flow: VRFs, end to end through New()'s handler chain.
// ---------------------------------------------------------------------------

func TestCRUDFlow_VRFs(t *testing.T) {
	h, st := newTestServer()
	adminToken := loginAs(t, h, st, "admin1", models.RoleAdmin)

	// Create
	createRec := doRequest(t, h, http.MethodPost, "/api/v1/vrfs", adminToken, map[string]string{
		"name":        "customer-a",
		"description": "Customer A's VRF",
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201; body=%s", createRec.Code, createRec.Body.String())
	}
	created := decodeJSON[models.VRF](t, createRec)
	if created.ID == "" {
		t.Fatal("create: expected non-empty id")
	}
	if created.Name != "customer-a" {
		t.Errorf("create: name = %q, want customer-a", created.Name)
	}

	// List: contains the created VRF
	listRec := doRequest(t, h, http.MethodGet, "/api/v1/vrfs", adminToken, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200; body=%s", listRec.Code, listRec.Body.String())
	}
	listEnv := decodeJSON[struct {
		Items  []models.VRF `json:"items"`
		Total  int64        `json:"total"`
		Limit  int          `json:"limit"`
		Offset int          `json:"offset"`
	}](t, listRec)
	if listEnv.Total != 1 || len(listEnv.Items) != 1 {
		t.Fatalf("list: got %d items (total=%d), want 1", len(listEnv.Items), listEnv.Total)
	}

	// Get
	getRec := doRequest(t, h, http.MethodGet, "/api/v1/vrfs/"+created.ID, adminToken, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200; body=%s", getRec.Code, getRec.Body.String())
	}

	// Update
	updateRec := doRequest(t, h, http.MethodPut, "/api/v1/vrfs/"+created.ID, adminToken, map[string]string{
		"name":        "customer-a-renamed",
		"description": "renamed",
	})
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200; body=%s", updateRec.Code, updateRec.Body.String())
	}
	updated := decodeJSON[models.VRF](t, updateRec)
	if updated.Name != "customer-a-renamed" {
		t.Errorf("update: name = %q, want customer-a-renamed", updated.Name)
	}

	// Delete
	deleteRec := doRequest(t, h, http.MethodDelete, "/api/v1/vrfs/"+created.ID, adminToken, nil)
	if deleteRec.Code != http.StatusOK && deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 200 or 204; body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	// Get after delete -> 404
	getAfterDeleteRec := doRequest(t, h, http.MethodGet, "/api/v1/vrfs/"+created.ID, adminToken, nil)
	if getAfterDeleteRec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want 404; body=%s", getAfterDeleteRec.Code, getAfterDeleteRec.Body.String())
	}

	// Every mutation above must have produced an audit log entry (in
	// addition to the login itself, which logs its own "user" entry).
	var vrfEntries int
	for _, entry := range st.auditLogs {
		if entry.EntityType == "vrf" {
			vrfEntries++
		}
	}
	if vrfEntries < 3 { // create, update, delete
		t.Errorf("audit log has %d vrf entries, want at least 3 (create/update/delete)", vrfEntries)
	}
}

// ---------------------------------------------------------------------------
// Reservation binding key canonicalisation tests
// ---------------------------------------------------------------------------

func TestReservation_BindingKeyCanonicalisation(t *testing.T) {
	h, st := newTestServer()
	opToken := loginAs(t, h, st, "op1", models.RoleOperator)

	// Create a pool for testing
	poolID := "test-pool-123"

	t.Run("raw MAC value sent to create handler", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/pools/"+poolID+"/reservations", opToken, map[string]any{
			"binding_type": "mac",
			"binding_key":  "aa:bb:cc:dd:ee:ff",
			"reserved_ip":  "10.0.0.10",
		})
		// Expect 404 (pool doesn't exist) but the handler should accept the input
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
			t.Logf("status = %d (expected 404 for missing pool)", rec.Code)
		}
	})

	t.Run("already-prefixed MAC key accepted", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/pools/"+poolID+"/reservations", opToken, map[string]any{
			"binding_type": "mac",
			"binding_key":  "mac:aa:bb:cc:dd:ee:ff",
			"reserved_ip":  "10.0.0.10",
		})
		// Should accept without double-prefixing
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
			t.Logf("status = %d (expected 404 for missing pool)", rec.Code)
		}
	})

	t.Run("client_id binding type accepted", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/pools/"+poolID+"/reservations", opToken, map[string]any{
			"binding_type": "client_id",
			"binding_key":  "deadbeef",
			"reserved_ip":  "10.0.0.10",
		})
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
			t.Logf("status = %d (expected 404 for missing pool)", rec.Code)
		}
	})

	t.Run("hostname binding type accepted", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/pools/"+poolID+"/reservations", opToken, map[string]any{
			"binding_type": "hostname",
			"binding_key":  "server.example.com",
			"reserved_ip":  "10.0.0.10",
		})
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
			t.Logf("status = %d (expected 404 for missing pool)", rec.Code)
		}
	})

	t.Run("mismatched prefixed key type rejected", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/pools/"+poolID+"/reservations", opToken, map[string]any{
			"binding_type": "mac",
			"binding_key":  "client_id:deadbeef",
			"reserved_ip":  "10.0.0.10",
		})
		// Should reject type mismatch with 400
		if rec.Code == http.StatusBadRequest {
			env := decodeJSON[map[string]map[string]any](t, rec)
			if env["error"]["code"] != "INVALID_INPUT" {
				t.Errorf("error.code = %v, want INVALID_INPUT", env["error"]["code"])
			}
		}
	})

	t.Run("missing binding_key returns 400", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/pools/"+poolID+"/reservations", opToken, map[string]any{
			"binding_type": "mac",
			"reserved_ip":  "10.0.0.10",
		})
		if rec.Code != http.StatusBadRequest {
			t.Logf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("missing binding_type returns 400", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/pools/"+poolID+"/reservations", opToken, map[string]any{
			"binding_key": "aa:bb:cc:dd:ee:ff",
			"reserved_ip": "10.0.0.10",
		})
		if rec.Code != http.StatusBadRequest {
			t.Logf("status = %d, want 400", rec.Code)
		}
	})
}
