package ha

import (
	"context"
	"testing"
	"time"
)

// TestLifecycle_StandaloneStartStop exercises the real Start/Stop goroutine
// wiring (runLoop, watchConfigLoop, ticker selects, WaitGroup shutdown) end
// to end, rather than calling runElection/tickHeartbeat directly as the
// rest of this package's tests do.
func TestLifecycle_StandaloneStartStop(t *testing.T) {
	cfg := testConfig(false)
	cfg.HA.HeartbeatIntervalMs = 20 // fast tick so the test doesn't need to sleep long
	st := newMockStore()
	m := newManager(cfg, st, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if m.clusterID == "" || m.serverID == "" {
		t.Fatal("Start did not resolve cluster/server identity")
	}

	// Standalone self-promotes to PRIMARY without waiting on an election.
	deadline := time.Now().Add(time.Second)
	for m.sm.Current() != StatePrimary && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.sm.Current(); got != StatePrimary {
		t.Fatalf("state after Start = %s, want PRIMARY", got)
	}
	if !m.ShouldServe() {
		t.Error("ShouldServe() = false, want true")
	}

	// Let at least one heartbeat tick land.
	deadline = time.Now().Add(time.Second)
	for st.heartbeatCount(m.serverID) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := st.heartbeatCount(m.serverID); n == 0 {
		t.Error("no heartbeat was written by the running loop")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := m.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := m.sm.Current(); got != StateShutdown {
		t.Fatalf("state after Stop = %s, want SHUTDOWN", got)
	}
}

// TestLifecycle_ClusteredStartStop is the same shape as
// TestLifecycle_StandaloneStartStop but with HA enabled, so the election
// loop and its interaction with runLoop's other tickers are exercised too.
func TestLifecycle_ClusteredStartStop(t *testing.T) {
	cfg := testConfig(true)
	cfg.HA.HeartbeatIntervalMs = 20
	cfg.HA.ElectionIntervalMs = 20
	cfg.HA.HeartbeatTTLMs = 100
	st := newMockStore()
	m := newManager(cfg, st, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Sole registered server with the lowest (only) node_id: bootstrap
	// election should promote it to PRIMARY.
	deadline := time.Now().Add(time.Second)
	for m.sm.Current() != StatePrimary && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.sm.Current(); got != StatePrimary {
		t.Fatalf("state after Start = %s, want PRIMARY", got)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := m.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := m.sm.Current(); got != StateShutdown {
		t.Fatalf("state after Stop = %s, want SHUTDOWN", got)
	}
}
