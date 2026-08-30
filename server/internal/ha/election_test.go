package ha

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/config"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig(enabled bool) *config.Config {
	cfg := config.Default()
	cfg.HA.Enabled = enabled
	cfg.HA.ClusterID = "test-cluster"
	cfg.HA.ServerName = "node-a"
	cfg.HA.NodeID = 1
	cfg.HA.HeartbeatIntervalMs = 1000
	cfg.HA.HeartbeatTTLMs = 3000
	cfg.HA.ElectionIntervalMs = 2000
	return cfg
}

// newTestManager builds a Manager against a fresh mockStore, bypassing
// Start's I/O (ensureCluster/ensureServerRegistered) by setting the
// resolved cluster_id/server_id directly — the tests below exercise
// election/heartbeat/failover behavior given a resolved identity, which
// ensureCluster/ensureServerRegistered are covered separately by
// TestEnsureCluster_* and TestEnsureServerRegistered_*.
func newTestManager(t *testing.T, cfg *config.Config) (*Manager, *mockStore) {
	t.Helper()
	st := newMockStore()
	m := newManager(cfg, st, testLogger())
	m.clusterID = "test-cluster-id"
	m.serverID = "self"
	m.lastDBSuccess = time.Now()
	return m, st
}

func TestNew_RejectsNilArgs(t *testing.T) {
	if _, err := New(nil, nil, testLogger()); err == nil {
		t.Error("New with nil config: want error, got nil")
	}
	if _, err := New(testConfig(true), nil, testLogger()); err == nil {
		t.Error("New with nil store: want error, got nil")
	}
}

func TestElection_WinBecomesPrimaryAndWritesHeartbeat(t *testing.T) {
	m, st := newTestManager(t, testConfig(true))
	st.electionResult = m.serverID

	m.runElection(context.Background())

	if got := m.sm.Current(); got != StatePrimary {
		t.Fatalf("state = %s, want PRIMARY", got)
	}
	if n := st.heartbeatCount(m.serverID); n != 1 {
		t.Errorf("heartbeat writes = %d, want 1 (immediate write on promotion)", n)
	}
	if !m.ShouldServe() {
		t.Error("ShouldServe() = false for PRIMARY, want true")
	}
}

func TestElection_LoseBecomesSecondary(t *testing.T) {
	m, st := newTestManager(t, testConfig(true))
	st.electionResult = "some-other-server"

	m.runElection(context.Background())

	if got := m.sm.Current(); got != StateSecondary {
		t.Fatalf("state = %s, want SECONDARY", got)
	}
	if !m.ShouldServe() {
		t.Error("ShouldServe() = false for SECONDARY, want true (secondaries serve concurrently)")
	}
}

func TestElection_BootstrapFallback_LocalWins(t *testing.T) {
	m, st := newTestManager(t, testConfig(true))
	st.servers = []*models.ClusterServer{
		{ID: m.serverID, NodeID: 1},
		{ID: "peer", NodeID: 2},
	}
	// electionResult left empty: nobody has a fresh heartbeat yet.

	m.runElection(context.Background())

	if got := m.sm.Current(); got != StatePrimary {
		t.Fatalf("state = %s, want PRIMARY (lowest node_id wins bootstrap)", got)
	}
}

func TestElection_BootstrapFallback_PeerWins(t *testing.T) {
	m, st := newTestManager(t, testConfig(true))
	st.servers = []*models.ClusterServer{
		{ID: m.serverID, NodeID: 2},
		{ID: "peer", NodeID: 1},
	}

	m.runElection(context.Background())

	if got := m.sm.Current(); got != StateSecondary {
		t.Fatalf("state = %s, want SECONDARY (peer has the lower node_id)", got)
	}
}

func TestElection_NoServersYet_StaysInitializing(t *testing.T) {
	m, _ := newTestManager(t, testConfig(true))
	// No election result, no registered servers: nothing to decide yet.

	m.runElection(context.Background())

	if got := m.sm.Current(); got != StateInitializing {
		t.Fatalf("state = %s, want unchanged INITIALIZING", got)
	}
}

func TestFencing_DemotesAfterGracePeriodExceeded(t *testing.T) {
	m, st := newTestManager(t, testConfig(true))
	m.sm.current = StatePrimary
	st.electionErr = errors.New("connection refused")

	// heartbeat_ttl_ms=3000 -> grace = 3s; back-date last success beyond it.
	m.lastDBSuccess = time.Now().Add(-7 * time.Second)

	m.runElection(context.Background())

	if got := m.sm.Current(); got != StateError {
		t.Fatalf("state = %s, want ERROR (fenced off after exceeding heartbeat_ttl_ms)", got)
	}
	if m.ShouldServe() {
		t.Error("ShouldServe() = true while fenced (ERROR), want false")
	}
}

// TestFencing_SelfFencesNoLaterThanPeerFreshnessThreshold guards the
// split-brain fix: the self-fencing grace period must never exceed the
// heartbeat_ttl_ms freshness threshold ElectionQuery uses to decide a peer's
// heartbeat is stale (postgres_cluster.go), or an isolated primary could
// still be holding the VIP when a peer bootstrap-elects a replacement
// (election.go's bootstrapElect). Back-dating lastDBSuccess to just over 1x
// heartbeat_ttl_ms — the exact point a peer would already treat this node as
// stale — must be enough to fence, not just an outage of 2x heartbeat_ttl_ms.
func TestFencing_SelfFencesNoLaterThanPeerFreshnessThreshold(t *testing.T) {
	m, st := newTestManager(t, testConfig(true))
	m.sm.current = StatePrimary
	st.electionErr = errors.New("connection refused")

	// heartbeat_ttl_ms=3000; back-date last success to just past 1x it,
	// which is exactly when a peer's ElectionQuery would already see this
	// node's heartbeat as stale.
	m.lastDBSuccess = time.Now().Add(-3100 * time.Millisecond)

	m.runElection(context.Background())

	if got := m.sm.Current(); got != StateError {
		t.Fatalf("state = %s, want ERROR (must fence itself by 1x heartbeat_ttl_ms, matching the peer freshness threshold, to avoid split-brain)", got)
	}
}

func TestFencing_ToleratesShortOutage(t *testing.T) {
	m, st := newTestManager(t, testConfig(true))
	m.sm.current = StatePrimary
	st.electionErr = errors.New("connection refused")
	m.lastDBSuccess = time.Now() // well within the grace period

	m.runElection(context.Background())

	if got := m.sm.Current(); got != StatePrimary {
		t.Fatalf("state = %s, want unchanged PRIMARY (outage still within fencing grace)", got)
	}
}

func TestFencing_RecoveryRejoinsAsSecondaryFirst(t *testing.T) {
	m, st := newTestManager(t, testConfig(true))
	m.sm.current = StateError
	// Even though the election query would name this node the winner...
	st.electionResult = m.serverID

	m.runElection(context.Background())

	// ...a node recovering from ERROR must spend at least one cycle as
	// SECONDARY before it can be promoted again.
	if got := m.sm.Current(); got != StateSecondary {
		t.Fatalf("state = %s, want SECONDARY (must rejoin as SECONDARY before re-promotion)", got)
	}

	// The next cycle, with the DB healthy, is free to promote it.
	m.runElection(context.Background())
	if got := m.sm.Current(); got != StatePrimary {
		t.Fatalf("state = %s, want PRIMARY on the following cycle", got)
	}
}

func TestFailover_DemotesPrimaryAndSuppressesReelection(t *testing.T) {
	m, st := newTestManager(t, testConfig(true))
	m.sm.current = StatePrimary
	st.electionResult = m.serverID

	if err := m.Failover(context.Background(), ""); err != nil {
		t.Fatalf("Failover returned error: %v", err)
	}
	if got := m.sm.Current(); got != StateSecondary {
		t.Fatalf("state after Failover = %s, want SECONDARY", got)
	}

	// The DB still names this node the election winner, but the cool-down
	// must suppress re-promotion.
	m.runElection(context.Background())
	if got := m.sm.Current(); got != StateSecondary {
		t.Fatalf("state after post-failover election = %s, want SECONDARY (cool-down should suppress re-election)", got)
	}

	// Once the cool-down has elapsed, normal election resumes.
	m.mu.Lock()
	m.steppedDownUntil = time.Now().Add(-time.Second)
	m.mu.Unlock()
	m.runElection(context.Background())
	if got := m.sm.Current(); got != StatePrimary {
		t.Fatalf("state after cool-down elapsed = %s, want PRIMARY", got)
	}
}

func TestFailover_RejectsWhenNotPrimary(t *testing.T) {
	m, _ := newTestManager(t, testConfig(true))
	m.sm.current = StateSecondary

	if err := m.Failover(context.Background(), ""); err == nil {
		t.Error("Failover on a non-primary node: want error, got nil")
	}
}

func TestFailover_RejectedInStandaloneMode(t *testing.T) {
	m, _ := newTestManager(t, testConfig(false))
	m.sm.current = StatePrimary

	if err := m.Failover(context.Background(), ""); err == nil {
		t.Error("Failover in standalone mode: want error, got nil")
	}
}

func TestShouldServe_StandaloneAlwaysTrue(t *testing.T) {
	m, _ := newTestManager(t, testConfig(false))
	for _, s := range []State{StateInitializing, StateSecondary, StatePrimary, StateError, StateShutdown} {
		m.sm.current = s
		if !m.ShouldServe() {
			t.Errorf("standalone ShouldServe() = false in state %s, want true", s)
		}
	}
}

func TestShouldServe_ClusteredGatesOnState(t *testing.T) {
	m, _ := newTestManager(t, testConfig(true))
	want := map[State]bool{
		StateInitializing: false,
		StateSecondary:    true,
		StatePrimary:      true,
		StateError:        false,
		StateShutdown:     false,
	}
	for s, exp := range want {
		m.sm.current = s
		if got := m.ShouldServe(); got != exp {
			t.Errorf("ShouldServe() in state %s = %v, want %v", s, got, exp)
		}
	}
}

func TestHealth_FillsInLocalIdentity(t *testing.T) {
	m, st := newTestManager(t, testConfig(true))
	m.sm.current = StatePrimary
	st.health = &models.ClusterHealth{
		ClusterID:    m.clusterID,
		ClusterState: models.ClusterStateHealthy,
	}

	h, err := m.Health(context.Background())
	if err != nil {
		t.Fatalf("Health returned error: %v", err)
	}
	if h.LocalServerID != m.serverID {
		t.Errorf("LocalServerID = %q, want %q", h.LocalServerID, m.serverID)
	}
	if h.LocalRole != models.ClusterRolePrimary {
		t.Errorf("LocalRole = %q, want PRIMARY", h.LocalRole)
	}
}

func TestEnsureCluster_CreatesIfMissingThenIsIdempotent(t *testing.T) {
	cfg := testConfig(true)
	st := newMockStore()
	m := newManager(cfg, st, testLogger())
	m.clusterID = resolveClusterRowID(resolveClusterName(cfg))

	if err := m.ensureCluster(context.Background(), "test-cluster"); err != nil {
		t.Fatalf("first ensureCluster: %v", err)
	}
	if st.cluster == nil {
		t.Fatal("expected CreateCluster to have been called")
	}
	if st.cluster.ID != m.clusterID {
		t.Errorf("created cluster ID = %q, want %q", st.cluster.ID, m.clusterID)
	}

	created := st.cluster
	if err := m.ensureCluster(context.Background(), "test-cluster"); err != nil {
		t.Fatalf("second ensureCluster: %v", err)
	}
	if st.cluster != created {
		t.Error("second ensureCluster should have found the existing row, not created another")
	}
}

func TestEnsureServerRegistered_UsesMutatedID(t *testing.T) {
	cfg := testConfig(true)
	st := newMockStore()
	st.registerAssignsID = true
	m := newManager(cfg, st, testLogger())
	m.clusterID = "cluster-x"

	if err := m.ensureServerRegistered(context.Background()); err != nil {
		t.Fatalf("ensureServerRegistered: %v", err)
	}
	if m.serverID == "" {
		t.Fatal("serverID was not resolved")
	}
}

func TestEnsureServerRegistered_FallsBackToListServers(t *testing.T) {
	cfg := testConfig(true)
	st := newMockStore()
	st.registerAssignsID = false // RegisterServer doesn't mutate the struct
	m := newManager(cfg, st, testLogger())
	m.clusterID = "cluster-x"

	if err := m.ensureServerRegistered(context.Background()); err != nil {
		t.Fatalf("ensureServerRegistered: %v", err)
	}
	if m.serverID == "" {
		t.Fatal("serverID was not resolved via the ListServers fallback")
	}
	found := false
	for _, s := range st.servers {
		if s.ID == m.serverID && s.NodeID == cfg.HA.NodeID {
			found = true
		}
	}
	if !found {
		t.Errorf("resolved serverID %q does not match a registered server with node_id %d", m.serverID, cfg.HA.NodeID)
	}
}

func TestBroadcaster_PublishIsNonBlockingAndCoalesces(t *testing.T) {
	b := newBroadcaster()
	ch := b.Subscribe()

	b.publish()
	b.publish() // should not block even though ch isn't drained yet

	select {
	case <-ch:
	default:
		t.Fatal("expected a signal on the subscriber channel")
	}
}
