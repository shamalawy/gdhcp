package dashboard

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// fakeHubStore counts calls so a test can assert that N subscribers cost one
// round of queries rather than N.
type fakeHubStore struct {
	mu sync.Mutex

	statsCalls  int
	scopesCalls int
	utilCalls   int

	scopes []*models.DHCPScope
	active map[string]int64
}

func newFakeHubStore(scopeIDs ...string) *fakeHubStore {
	f := &fakeHubStore{active: make(map[string]int64)}
	for _, id := range scopeIDs {
		f.scopes = append(f.scopes, &models.DHCPScope{ID: id})
		f.active[id] = 0
	}
	return f
}

func (f *fakeHubStore) GetDashboardStats(context.Context, time.Duration) (*models.DashboardStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statsCalls++
	return &models.DashboardStats{}, nil
}

func (f *fakeHubStore) ListScopes(context.Context, store.ListParams) (store.ListResult[*models.DHCPScope], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopesCalls++
	return store.ListResult[*models.DHCPScope]{Items: f.scopes, Total: int64(len(f.scopes))}, nil
}

func (f *fakeHubStore) GetScopeUtilization(_ context.Context, scopeID string) (*models.Utilization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.utilCalls++
	return &models.Utilization{EntityID: scopeID, Active: f.active[scopeID], Total: 100}, nil
}

func (f *fakeHubStore) counts() (stats, scopes, util int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statsCalls, f.scopesCalls, f.utilCalls
}

func testHubLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// drain reads whatever is already buffered for a subscriber without blocking.
func drain(ch <-chan sseMessage) []sseMessage {
	var out []sseMessage
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, msg)
		default:
			return out
		}
	}
}

// waitFor polls cond until it holds or the deadline passes, so the test does
// not depend on how quickly the hub's poller goroutine gets scheduled.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestEventHub_PollsOncePerTickRegardlessOfSubscribers is the point of the
// hub: query cost must track the size of the deployment, not the number of
// open dashboards. Three subscribers must not produce three ListScopes
// sweeps.
func TestEventHub_PollsOncePerTickRegardlessOfSubscribers(t *testing.T) {
	st := newFakeHubStore("scope-a", "scope-b")
	h := newEventHub(st, testHubLogger())

	chA, releaseA := h.subscribe()
	defer releaseA()
	waitFor(t, "first poll", func() bool { s, _, _ := st.counts(); return s >= 1 })

	chB, releaseB := h.subscribe()
	defer releaseB()
	chC, releaseC := h.subscribe()
	defer releaseC()

	stats, scopes, util := st.counts()
	if stats != 1 || scopes != 1 {
		t.Fatalf("after 3 subscribers: stats=%d scopes=%d, want 1 and 1 (one shared poll)", stats, scopes)
	}
	if util != 2 {
		t.Fatalf("GetScopeUtilization called %d times for 2 scopes, want 2", util)
	}

	// Every subscriber must still have received that one poll's events: the
	// first from the broadcast, the later two replayed on attach.
	for name, ch := range map[string]<-chan sseMessage{"A": chA, "B": chB, "C": chC} {
		got := drain(ch)
		var sawStats, sawLeases bool
		for _, m := range got {
			switch m.event {
			case "stats":
				sawStats = true
			case "leases":
				sawLeases = true
			}
		}
		if !sawStats || !sawLeases {
			t.Errorf("subscriber %s got %d events (stats=%v leases=%v), want both", name, len(got), sawStats, sawLeases)
		}
	}
}

// TestEventHub_StopsPollingWhenLastSubscriberLeaves covers the
// reference-counting: with no dashboard open the server must issue no
// queries at all.
func TestEventHub_StopsPollingWhenLastSubscriberLeaves(t *testing.T) {
	st := newFakeHubStore("scope-a")
	h := newEventHub(st, testHubLogger())

	_, release := h.subscribe()
	waitFor(t, "first poll", func() bool { s, _, _ := st.counts(); return s >= 1 })
	release()

	h.mu.Lock()
	running := h.cancel != nil
	h.mu.Unlock()
	if running {
		t.Fatal("poller still running after the last subscriber left")
	}

	before, _, _ := st.counts()
	time.Sleep(20 * time.Millisecond)
	after, _, _ := st.counts()
	if after != before {
		t.Fatalf("poller kept querying after release: %d -> %d", before, after)
	}
}

// TestEventHub_ChurnEmitsOnlyChangedScopes keeps the `leases` payload small
// on a quiet system: a scope whose active count did not move must not be
// re-sent every ten seconds.
func TestEventHub_ChurnEmitsOnlyChangedScopes(t *testing.T) {
	st := newFakeHubStore("scope-a", "scope-b")
	h := newEventHub(st, testHubLogger())
	ctx := context.Background()

	first := h.pollChurn(ctx)
	if len(first) != 2 {
		t.Fatalf("first poll returned %d scopes, want both (nothing is known yet)", len(first))
	}

	if steady := h.pollChurn(ctx); len(steady) != 0 {
		t.Fatalf("unchanged poll returned %d scopes, want none", len(steady))
	}

	st.mu.Lock()
	st.active["scope-b"] = 7
	st.mu.Unlock()

	changed := h.pollChurn(ctx)
	if len(changed) != 1 || changed[0].ScopeID != "scope-b" || changed[0].Active != 7 {
		t.Fatalf("after one scope moved, got %+v, want just scope-b at 7", changed)
	}
}

// TestEventHub_SlowSubscriberDoesNotBlockBroadcast: a connection that has
// stopped reading must be dropped messages, never allowed to stall the
// shared poller and with it every other dashboard.
func TestEventHub_SlowSubscriberDoesNotBlockBroadcast(t *testing.T) {
	h := newEventHub(newFakeHubStore(), testHubLogger())

	ch := make(chan sseMessage, subscriberBuffer)
	h.mu.Lock()
	h.subs[99] = ch
	h.mu.Unlock()

	// Overfill well past the buffer; broadcast must return regardless.
	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberBuffer*4; i++ {
			h.broadcast(sseMessage{event: "stats", data: i})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast blocked on a subscriber that stopped reading")
	}

	if got := len(ch); got != subscriberBuffer {
		t.Fatalf("slow subscriber holds %d events, want it capped at %d", got, subscriberBuffer)
	}
}
