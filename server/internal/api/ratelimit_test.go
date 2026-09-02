package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

func TestLoginLimiter_BurstThenRefill(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := newLoginLimiter()
	l.now = func() time.Time { return now }

	for i := 0; i < loginBurst; i++ {
		if !l.allow("10.0.0.1") {
			t.Fatalf("attempt %d of %d denied inside the burst", i+1, loginBurst)
		}
	}
	if l.allow("10.0.0.1") {
		t.Fatal("attempt past the burst was allowed")
	}

	// Buckets are per address: one noisy client must not lock everyone out.
	if !l.allow("10.0.0.2") {
		t.Fatal("a second address was throttled by the first address's attempts")
	}

	// One refill period buys exactly one attempt back, not a fresh burst.
	now = now.Add(loginRefill)
	if !l.allow("10.0.0.1") {
		t.Fatal("no token available after one full refill period")
	}
	if l.allow("10.0.0.1") {
		t.Fatal("one refill period granted more than one token")
	}

	// A long idle refills to the cap and no further.
	now = now.Add(loginRefill * loginBurst * 10)
	for i := 0; i < loginBurst; i++ {
		if !l.allow("10.0.0.1") {
			t.Fatalf("attempt %d denied after a long idle should have refilled the bucket", i+1)
		}
	}
	if l.allow("10.0.0.1") {
		t.Fatal("bucket refilled past its burst cap")
	}
}

// TestLimiterKey_IgnoresPortAndForwardedHeader pins the security property:
// the bucket is keyed on the TCP peer, so a caller cannot mint a fresh
// allowance by setting a header it controls.
func TestLimiterKey_IgnoresPortAndForwardedHeader(t *testing.T) {
	r1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r1.RemoteAddr = "203.0.113.9:51000"

	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r2.RemoteAddr = "203.0.113.9:51001" // same client, new connection
	r2.Header.Set("X-Forwarded-For", "198.51.100.7")

	if got := limiterKey(r1); got != "203.0.113.9" {
		t.Fatalf("limiterKey = %q, want the host without the port", got)
	}
	if limiterKey(r1) != limiterKey(r2) {
		t.Fatal("a new source port or an X-Forwarded-For header produced a different bucket; " +
			"the limiter can be bypassed")
	}
}

func TestLogin_RateLimited(t *testing.T) {
	h, st := newTestServer()
	seedUserWithPassword(t, st, "alice", "correct-horse", models.RoleAdmin)

	body := loginRequest{Username: "alice", Password: "wrong"}
	for i := 0; i < loginBurst; i++ {
		rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/login", "", body)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401 inside the burst", i+1, rec.Code)
		}
	}

	rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/login", "", body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt past the burst: status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("429 response carries no Retry-After header")
	}

	// A throttled attempt must not reach the handler at all — that is what
	// stops the audit log from becoming an unauthenticated write amplifier.
	if n := st.failedLoginCount(); n != loginBurst {
		t.Errorf("audit recorded %d failed logins, want %d (the throttled one must not be logged)", n, loginBurst)
	}
}

// TestLogin_FailedAttemptsAreAudited: a brute-force run that never succeeds
// used to leave no trace whatsoever.
func TestLogin_FailedAttemptsAreAudited(t *testing.T) {
	h, st := newTestServer()
	seedUserWithPassword(t, st, "alice", "correct-horse", models.RoleAdmin)

	// Wrong password for a real account.
	if rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/login", "",
		loginRequest{Username: "alice", Password: "wrong"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	// Unknown account: no user to attribute it to, but it must still be recorded.
	if rec := doRequest(t, h, http.MethodPost, "/api/v1/auth/login", "",
		loginRequest{Username: "nobody", Password: "x"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	entries := st.failedLogins()
	if len(entries) != 2 {
		t.Fatalf("audit holds %d failed-login entries, want 2", len(entries))
	}
	if entries[0].EntityID != "alice" || entries[1].EntityID != "nobody" {
		t.Errorf("attempted usernames recorded as %q and %q, want \"alice\" and \"nobody\"",
			entries[0].EntityID, entries[1].EntityID)
	}
	// Both rows must have the same shape. Attributing only the known-user
	// row to a user id would make that path pay a foreign-key check the
	// unknown-user path skips — a timing signal that undoes the whole point
	// of handleLogin's dummy bcrypt compare.
	for i, e := range entries {
		if e.UserID != nil {
			t.Errorf("entry %d attributes a failed login to a user id; both paths must write "+
				"an identical row or the audit insert leaks which usernames exist", i)
		}
	}
}

// TestLoginLimiter_NeverFailsOpenAtCapacity: an attacker who can fill the
// bucket map must not thereby switch the limiter off for everyone else. At
// capacity the coldest bucket is evicted; a new client is still throttled.
func TestLoginLimiter_NeverFailsOpenAtCapacity(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := newLoginLimiter()
	l.now = func() time.Time { return now }

	// Fill the map with buckets that are all too recent to sweep.
	for i := 0; i < maxLoginBuckets; i++ {
		l.buckets[keyFor(i)] = &loginBucket{tokens: 0, last: now}
		now = now.Add(time.Nanosecond)
	}
	if len(l.buckets) != maxLoginBuckets {
		t.Fatalf("setup: %d buckets, want %d", len(l.buckets), maxLoginBuckets)
	}

	// A brand-new address gets a real bucket and is throttled normally:
	// burst attempts allowed, then denied.
	const fresh = "198.51.100.200"
	for i := 0; i < loginBurst; i++ {
		if !l.allow(fresh) {
			t.Fatalf("attempt %d from a new address denied inside the burst", i+1)
		}
	}
	if l.allow(fresh) {
		t.Fatal("limiter failed open at capacity: a new address was never throttled")
	}
	if len(l.buckets) > maxLoginBuckets {
		t.Fatalf("bucket map grew past its cap: %d", len(l.buckets))
	}
}

func keyFor(i int) string {
	return "10." + itoa(i/65536%256) + "." + itoa(i/256%256) + "." + itoa(i%256)
}

func itoa(i int) string { return string(rune('0'+i/100%10)) + string(rune('0'+i/10%10)) + string(rune('0'+i%10)) }

// TestDecodeJSON_RejectsOversizedBody covers maxBodyMiddleware end-to-end:
// an unauthenticated caller must not be able to make the server buffer an
// arbitrary amount of memory, and the refusal must not be reported as a
// syntax error.
func TestDecodeJSON_RejectsOversizedBody(t *testing.T) {
	h, st := newTestServer()
	seedUserWithPassword(t, st, "alice", "correct-horse", models.RoleAdmin)

	// Valid JSON, just far too much of it.
	var buf bytes.Buffer
	buf.WriteString(`{"username":"alice","password":"`)
	buf.WriteString(strings.Repeat("A", maxRequestBodyBytes+1024))
	buf.WriteString(`"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 for a body past the %d byte cap", rec.Code, maxRequestBodyBytes)
	}
}
