// ratelimit.go throttles unauthenticated credential submission. It exists
// for one route: POST /api/v1/auth/login is exempt from the authenticator
// (middleware.go's authExempt), so without a limiter the only thing standing
// between a guesser and the password database is bcrypt's per-attempt cost —
// which slows an attacker down by a constant factor and nothing more.
package api

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/api/httpx"
)

const (
	// loginBurst is how many attempts a single client address may make
	// back-to-back before the bucket empties: enough for a person
	// fat-fingering a password, and for a browser retrying a submit.
	loginBurst = 10

	// loginRefill is how long one token takes to come back, i.e. the
	// sustained rate once the burst is spent — 12 attempts a minute.
	// Against bcrypt's ~100ms floor that is already a ~50x reduction in
	// guesses per second, and it is the difference between a dictionary
	// run finishing in hours and finishing in years.
	loginRefill = 5 * time.Second

	// loginBucketTTL is how long an idle bucket is kept. A bucket that has
	// been idle this long has necessarily refilled to full, so dropping it
	// loses no state a fresh one would not reproduce.
	loginBucketTTL = 15 * time.Minute

	// maxLoginBuckets bounds the map so the limiter cannot itself become
	// the memory-exhaustion vector it is defending against. Reaching it
	// sweeps idle entries first and, if that frees nothing, evicts the
	// least-recently-seen bucket to make room. It must never fail open:
	// letting a request through un-throttled because the map is full hands
	// an attacker who can fill the map the ability to switch the limiter
	// off, which is exactly backwards.
	maxLoginBuckets = 16384
)

// loginBucket is one client address's token bucket, refilled lazily on
// access rather than by a background goroutine.
type loginBucket struct {
	tokens float64
	last   time.Time
}

// loginLimiter is a per-client-address token bucket set.
//
// The key is the TCP peer from r.RemoteAddr, never X-Forwarded-For. That
// distinction is the whole point: clientIPPtr trusts the header because it
// only labels an audit row, but a limiter is an access gate, and a header
// the caller controls would let an attacker mint a fresh bucket per request
// simply by incrementing a number. assets.go's remoteIsPrivate draws the
// same line for the same reason. The cost is that behind a reverse proxy
// every request shares the proxy's address and therefore one bucket — safe
// (it throttles harder), and correctable only by teaching the server which
// proxies it actually trusts, which is a config change this does not assume.
type loginLimiter struct {
	mu      sync.Mutex
	buckets map[string]*loginBucket

	burst  float64
	refill time.Duration

	// now is swappable so tests can advance time without sleeping.
	now func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		buckets: make(map[string]*loginBucket),
		burst:   loginBurst,
		refill:  loginRefill,
		now:     time.Now,
	}
}

// allow consumes one token for key, reporting whether the request may
// proceed.
func (l *loginLimiter) allow(key string) bool {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxLoginBuckets {
			l.sweepLocked(now)
			// Still full: every bucket is recently active, so evict the
			// coldest one rather than admitting this request unthrottled.
			// The scan is O(len(buckets)) but only runs at capacity, and a
			// 16k-entry scan is noise next to the bcrypt compare the
			// request is about to pay for anyway.
			for len(l.buckets) >= maxLoginBuckets {
				if !l.evictOldestLocked() {
					break
				}
			}
		}
		l.buckets[key] = &loginBucket{tokens: l.burst - 1, last: now}
		return true
	}

	// Lazy refill: one token per l.refill of elapsed time, capped at burst.
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += elapsed.Seconds() / l.refill.Seconds()
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweepLocked drops buckets idle beyond loginBucketTTL. Caller holds l.mu.
func (l *loginLimiter) sweepLocked(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.last) > loginBucketTTL {
			delete(l.buckets, k)
		}
	}
}

// evictOldestLocked removes the least-recently-seen bucket, reporting
// whether one was removed. Evicting the coldest entry is the least harmful
// choice available: that client has had the longest to refill, so it loses
// the least accumulated throttling. Caller holds l.mu.
func (l *loginLimiter) evictOldestLocked() bool {
	var oldestKey string
	var oldest time.Time
	found := false
	for k, b := range l.buckets {
		if !found || b.last.Before(oldest) {
			oldestKey, oldest, found = k, b.last, true
		}
	}
	if found {
		delete(l.buckets, oldestKey)
	}
	return found
}

// limiterKey is the rate-limiting identity of a request: the TCP peer
// address with its ephemeral port stripped, so a client's many connections
// share one bucket.
func limiterKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimit wraps h so each client address gets at most loginBurst attempts
// plus one per loginRefill. Over the limit it answers 429 with a Retry-After
// and never calls h — the point being that a throttled attempt costs neither
// a bcrypt comparison nor an audit-log row.
func rateLimit(l *loginLimiter, log *slog.Logger, h http.HandlerFunc) http.HandlerFunc {
	retryAfter := int(loginRefill.Seconds() + 0.5)
	return func(w http.ResponseWriter, r *http.Request) {
		key := limiterKey(r)
		if !l.allow(key) {
			log.Warn("api: login rate limit exceeded", "remote_addr", key, "path", r.URL.Path)
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			httpx.RespondError(w, r, log, httpx.TooManyRequests("too many login attempts, try again shortly"))
			return
		}
		h(w, r)
	}
}
