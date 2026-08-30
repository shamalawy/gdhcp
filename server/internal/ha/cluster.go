// Package ha implements gdhcp's HA cluster manager: PostgreSQL-lease-based
// primary/secondary election, heartbeats, VIP failover (netlink + gratuitous
// ARP), config-change propagation (LISTEN/NOTIFY + poll fallback), manual
// failover, and cluster health reporting, per docs/design/ha-cluster.md and
// its binding overrides in docs/AMENDMENTS.md.
//
// Manager is the package's single exported entry point: construct one with
// New, call Start once, and Stop for graceful shutdown. Everything else in
// this package (election, heartbeat, vip, arp, config_watcher, failover,
// state_machine, metrics) is Manager's internal implementation, split across
// files by concern rather than by exported surface.
package ha

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"log/slog"

	"github.com/google/uuid"

	"github.com/shamalawy/gdhcp/server/internal/config"
	"github.com/shamalawy/gdhcp/server/internal/metrics"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// configChannel is the PostgreSQL NOTIFY channel this package LISTENs on for
// near-instant config propagation (docs/design/ha-cluster.md "PostgreSQL
// LISTEN/NOTIFY"). The API/config-write layer is expected to call
// store.ConfigChangeStore.NotifyConfigChange(ctx, configChannel) whenever it
// writes a config change other units should pick up.
const configChannel = "gdhcp_config"

// clusterNamespace is a fixed namespace UUID used to deterministically
// derive a cluster row's primary key from an operator-facing, non-UUID
// ha.cluster_id string. See resolveClusterRowID's doc comment for why.
var clusterNamespace = uuid.MustParse("6ea1c2c4-6e79-4b6a-9f1a-9b2b6a9c9c31")

// dataStore is the narrow slice of store.Store this package actually calls.
// Per amendment A10 ("prefer depending on the narrower interfaces above ...
// rather than the full Store where practical"), Manager is built against
// this small interface internally even though its exported constructor
// accepts the full store.Store (which satisfies dataStore structurally) —
// that keeps New's signature exactly what the integration agent wires
// store.NewPostgres(pool) into, while letting tests substitute an
// eight-method mock instead of implementing all ~20 embedded Store
// interfaces.
type dataStore interface {
	GetClusterByID(ctx context.Context, clusterID string) (*models.Cluster, error)
	CreateCluster(ctx context.Context, c *models.Cluster) error
	RegisterServer(ctx context.Context, server *models.ClusterServer) error
	ListServers(ctx context.Context, clusterID string) ([]*models.ClusterServer, error)
	WriteHeartbeat(ctx context.Context, clusterID, serverID string) error
	ElectionQuery(ctx context.Context, clusterID string) (string, error)
	GetClusterHealth(ctx context.Context, clusterID string) (*models.ClusterHealth, error)
	ListenConfigChanges(ctx context.Context, channel string) (notifications <-chan string, closeFn func() error, err error)
}

// Manager owns HA cluster membership for this node. It self-registers the
// server (and, once Start()ed, the cluster row it belongs to) in the
// database, runs the election/heartbeat loops, claims or releases the
// cluster VIP as its role changes, and reports cluster health.
type Manager struct {
	cfg   *config.Config
	store dataStore
	log   *slog.Logger

	sm *stateMachine

	nodeName string
	nodeID   int

	clusterID string // resolved clusters.id (see resolveClusterRowID)
	serverID  string // resolved cluster_servers.id for this node

	mu               sync.RWMutex
	cluster          *models.Cluster
	lastDBSuccess    time.Time
	steppedDownUntil time.Time
	metricsReg       *metrics.Registry

	metricsRec metricsRecorder
	subs       *broadcaster

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New constructs a Manager against cfg and st. It performs no I/O and
// cannot fail on a healthy config; call Start to self-register in the
// database and begin the election/heartbeat/config-watch loops.
func New(cfg *config.Config, st store.Store, log *slog.Logger) (*Manager, error) {
	if cfg == nil {
		return nil, errors.New("ha: nil config")
	}
	if st == nil {
		return nil, errors.New("ha: nil store")
	}
	if log == nil {
		log = slog.Default()
	}
	if err := cfg.HA.Validate(); err != nil {
		return nil, fmt.Errorf("ha: invalid ha config: %w", err)
	}
	return newManager(cfg, st, log), nil
}

// newManager is the internal constructor. Tests use it directly with a mock
// implementing only dataStore, rather than the full store.Store New
// requires.
func newManager(cfg *config.Config, st dataStore, log *slog.Logger) *Manager {
	m := &Manager{
		cfg:      cfg,
		store:    st,
		log:      log,
		sm:       newStateMachine(),
		nodeName: resolveNodeName(cfg),
		nodeID:   resolveNodeID(cfg),
		subs:     newBroadcaster(),
	}
	m.metricsRec = metricsRecorder{m: m}
	return m
}

func resolveNodeName(cfg *config.Config) string {
	if cfg.HA.ServerName != "" {
		return cfg.HA.ServerName
	}
	if cfg.HA.ServerID != "" {
		return cfg.HA.ServerID
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "gdhcp-node"
}

func resolveNodeID(cfg *config.Config) int {
	if cfg.HA.NodeID > 0 {
		return cfg.HA.NodeID
	}
	return 1
}

func resolveClusterName(cfg *config.Config) string {
	if cfg.HA.ClusterID != "" {
		return cfg.HA.ClusterID
	}
	return "standalone"
}

// resolveClusterRowID derives the clusters.id primary key to look up or
// create for name. The DB column is UUID (amendment A13's schema), but
// config.yaml.example's ha.cluster_id is illustrative ("prod-cluster", not a
// UUID) and store.ClusterStore exposes no lookup-by-name method — only
// GetClusterByID. A real UUID configured for cluster_id (e.g. one obtained
// from POST /clusters/bootstrap and copied into every node's config) is
// used as-is; any other string is deterministically hashed into a stable
// UUID via uuid.NewSHA1, so every node configured with the same cluster_id
// resolves to the same row on every restart without needing a name-lookup
// store method. See notes_for_integration for the alternative (adding
// ClusterStore.GetClusterByName) if that's preferred long-term.
func resolveClusterRowID(name string) string {
	if id, err := uuid.Parse(name); err == nil {
		return id.String()
	}
	return uuid.NewSHA1(clusterNamespace, []byte(name)).String()
}

// vipHostIP extracts the bare IP from a config value that may carry a CIDR
// prefix ("10.0.0.10/24") or be a plain address ("10.0.0.10"). It returns
// nil for an empty or unparseable input.
func vipHostIP(cidrOrIP string) net.IP {
	if cidrOrIP == "" {
		return nil
	}
	if ip, _, err := net.ParseCIDR(cidrOrIP); err == nil {
		return ip
	}
	return net.ParseIP(cidrOrIP)
}

// Start self-registers this node's cluster and server rows (both
// idempotent — safe to call on every process start), then launches the
// config-watch and election/heartbeat loops in the background and returns.
// Start must be called at most once per Manager.
func (m *Manager) Start(ctx context.Context) error {
	clusterName := resolveClusterName(m.cfg)
	m.clusterID = resolveClusterRowID(clusterName)

	if err := m.ensureCluster(ctx, clusterName); err != nil {
		return fmt.Errorf("ha: ensure cluster: %w", err)
	}
	if err := m.ensureServerRegistered(ctx); err != nil {
		return fmt.Errorf("ha: register server: %w", err)
	}

	m.mu.Lock()
	m.lastDBSuccess = time.Now()
	m.mu.Unlock()

	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	m.wg.Add(2)
	go m.watchConfigLoop(runCtx)
	go m.runLoop(runCtx)

	m.log.Info("ha: manager started",
		"cluster_id", m.clusterID, "server_id", m.serverID,
		"node_id", m.nodeID, "node_name", m.nodeName,
		"ha_enabled", m.cfg.HA.Enabled)
	return nil
}

// Stop cancels the background loops (which drop the VIP as part of their
// shutdown-state transition, best-effort) and waits for them to exit, or
// for ctx to expire, whichever comes first. Stop is a no-op if Start was
// never called.
func (m *Manager) Stop(ctx context.Context) error {
	if m.cancel == nil {
		return nil
	}
	m.cancel()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		m.log.Info("ha: manager stopped", "cluster_id", m.clusterID, "server_id", m.serverID)
		return nil
	case <-ctx.Done():
		m.log.Warn("ha: stop deadline exceeded before background loops exited", "cluster_id", m.clusterID)
		return ctx.Err()
	}
}

// ensureCluster fetches the cluster row this node belongs to, creating it
// from cfg.HA if it doesn't exist yet. Idempotent: concurrent first boots
// of every node in a cluster race harmlessly to CreateCluster, and the
// loser just re-fetches the winner's row.
func (m *Manager) ensureCluster(ctx context.Context, name string) error {
	cl, err := m.store.GetClusterByID(ctx, m.clusterID)
	switch {
	case err == nil:
		m.mu.Lock()
		m.cluster = cl
		m.mu.Unlock()
		return nil
	case errors.Is(err, models.ErrNotFound):
		// fall through to create it below
	default:
		return err
	}

	newCluster := &models.Cluster{
		ID:                m.clusterID,
		Name:              name,
		HeartbeatInterval: m.cfg.HA.HeartbeatIntervalMs,
		HeartbeatTTL:      m.cfg.HA.HeartbeatTTLMs,
		ElectionInterval:  m.cfg.HA.ElectionIntervalMs,
	}
	if m.cfg.HA.VIPInterface != "" {
		iface := m.cfg.HA.VIPInterface
		newCluster.VIPInterface = &iface
	}
	if ip := vipHostIP(m.cfg.HA.VIPAddress); ip != nil {
		newCluster.VIPAddress = &ip
	}

	if err := m.store.CreateCluster(ctx, newCluster); err != nil {
		if errors.Is(err, models.ErrConflict) {
			// Lost a startup race against a peer creating the same row.
			cl, gerr := m.store.GetClusterByID(ctx, m.clusterID)
			if gerr != nil {
				return gerr
			}
			m.mu.Lock()
			m.cluster = cl
			m.mu.Unlock()
			return nil
		}
		return err
	}

	m.mu.Lock()
	m.cluster = newCluster
	m.mu.Unlock()
	return nil
}

// ensureServerRegistered self-registers this node's cluster_servers row and
// resolves m.serverID, the row's primary key. RegisterServer is expected to
// be idempotent on (cluster_id, node_id) — repeated calls across restarts
// must resolve to the same row rather than accumulating duplicates.
//
// store.ClusterStore.RegisterServer's signature (a pointer, error-only
// return) matches the common repository convention of the store filling in
// generated fields — here, srv.ID — on the struct it was handed. This
// method honors that convention if present, but does not depend on it: if
// srv.ID comes back empty, it falls back to discovering the row via
// ListServers, matched on this node's unique node_id.
func (m *Manager) ensureServerRegistered(ctx context.Context) error {
	srv := &models.ClusterServer{
		ClusterID:   m.clusterID,
		NodeName:    m.nodeName,
		NodeID:      m.nodeID,
		APIAddress:  m.cfg.Server.APIAddr,
		DHCPAddress: dhcpBindAddress(m.cfg),
	}
	if err := m.store.RegisterServer(ctx, srv); err != nil {
		return err
	}
	if srv.ID != "" {
		m.serverID = srv.ID
		return nil
	}

	servers, err := m.store.ListServers(ctx, m.clusterID)
	if err != nil {
		return fmt.Errorf("discover registered server id: %w", err)
	}
	for _, s := range servers {
		if s.NodeID == m.nodeID {
			m.serverID = s.ID
			return nil
		}
	}
	return fmt.Errorf("server registered but not found via ListServers (node_id=%d)", m.nodeID)
}

// dhcpBindAddress is the informational dhcp_address recorded for this
// server (docs/design/ha-cluster.md's cluster_servers.dhcp_address, e.g.
// "0.0.0.0:67"). config.DHCPConfig has no bind-address field of its own
// (only per-interface name/vrf_id bindings), so this reports the standard
// DHCP server port whenever this node has any DHCP interfaces configured,
// and an empty string for an API/UI-only node (amendment A11: an empty
// dhcp.interfaces list). See notes_for_integration.
func dhcpBindAddress(cfg *config.Config) string {
	if len(cfg.DHCP.Interfaces) == 0 {
		return ""
	}
	return "0.0.0.0:67"
}

// refreshCluster re-fetches the cluster row (picking up VIP/timing changes
// made through the API) and is called by the config-watch loop whenever it
// observes a config change notification or poll tick.
func (m *Manager) refreshCluster(ctx context.Context) {
	cl, err := m.store.GetClusterByID(ctx, m.clusterID)
	if err != nil {
		m.log.Warn("ha: failed to refresh cluster row", "cluster_id", m.clusterID, "error", err)
		return
	}
	m.mu.Lock()
	m.cluster = cl
	m.mu.Unlock()
}

// runLoop is the single goroutine that owns every state transition for this
// Manager's lifetime: it ticks the election and heartbeat loops and, on
// exit (ctx canceled by Stop or by the caller's ctx), transitions to
// StateShutdown so the VIP is dropped exactly once, from one place.
func (m *Manager) runLoop(ctx context.Context) {
	defer m.wg.Done()
	defer m.shutdownCleanup()

	heartbeatInterval := time.Duration(m.cfg.HA.HeartbeatIntervalMs) * time.Millisecond
	if heartbeatInterval <= 0 {
		heartbeatInterval = time.Second
	}
	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer heartbeatTicker.Stop()

	var electionC <-chan time.Time
	if m.cfg.HA.Enabled {
		electionInterval := time.Duration(m.cfg.HA.ElectionIntervalMs) * time.Millisecond
		if electionInterval <= 0 {
			electionInterval = 2 * time.Second
		}
		electionTicker := time.NewTicker(electionInterval)
		defer electionTicker.Stop()
		electionC = electionTicker.C

		// Resolve a role immediately rather than waiting a full
		// election_interval_ms after boot.
		m.runElection(ctx)
	} else {
		// Standalone (amendment A11): no election, no VIP contention. This
		// node is unconditionally "PRIMARY" so ShouldServe()/heartbeat
		// behave uniformly with clustered mode; onEnterPrimary skips all
		// VIP/ARP work whenever !cfg.HA.Enabled.
		m.transitionTo(ctx, StatePrimary, "standalone")
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-electionC:
			m.runElection(ctx)
		case <-heartbeatTicker.C:
			m.tickHeartbeat(ctx)
		}
	}
}

func (m *Manager) shutdownCleanup() {
	// Use a background context: the one runLoop was passed is already
	// canceled by the time this deferred call runs, but VIP release still
	// needs to happen.
	m.transitionTo(context.Background(), StateShutdown, "shutdown")
}

// transitionTo is the single place every state change and its side effects
// go through: it asks the state machine whether the edge is legal, logs the
// outcome either way, and — only for an edge that actually took effect —
// performs the VIP/heartbeat action associated with the new state.
func (m *Manager) transitionTo(ctx context.Context, to State, reason string) {
	from := m.sm.Current()
	if !m.sm.Transition(to) {
		return
	}

	m.log.Info("ha: state transition",
		"cluster_id", m.clusterID, "server_id", m.serverID, "node_id", m.nodeID,
		"from", string(from), "to", string(to), "reason", reason)
	// docs/design/ha-cluster.md's cluster_election_log table has no
	// corresponding store.ClusterStore method (see notes_for_integration) —
	// the structured log line above is the durable record of this
	// transition until one is added.

	switch to {
	case StatePrimary:
		m.onEnterPrimary(ctx)
	case StateSecondary, StateError, StateShutdown:
		m.onExitPrimary(ctx)
	}
}

func (m *Manager) onEnterPrimary(ctx context.Context) {
	if !m.cfg.HA.Enabled {
		return // standalone: PRIMARY is bookkeeping only, no VIP/ARP.
	}
	m.assignVIP()
	// Publish a fresh heartbeat immediately so this promotion is visible to
	// peers without waiting for the next heartbeat tick.
	m.writeHeartbeat(ctx)
}

func (m *Manager) onExitPrimary(ctx context.Context) {
	_ = ctx
	if !m.cfg.HA.Enabled {
		return
	}
	m.removeVIP()
}

// recordDBFailure is called after any failed DB operation in the
// election/heartbeat loops. If the database has been unreachable for at
// least heartbeat_ttl_ms (the fencing grace period), it demotes this node
// into StateError: VIP dropped, ShouldServe() false. A shorter outage is
// logged and retried on the next tick without changing state.
//
// The grace period is intentionally capped at 1x heartbeat_ttl_ms, matching
// (not exceeding) the freshness threshold ElectionQuery uses to decide a
// peer's heartbeat is stale (postgres_cluster.go). If this were longer than
// that threshold, a peer could see this node's heartbeat go stale and
// bootstrap-elect a replacement primary (election.go's bootstrapElect)
// while this node — still within its own, longer grace period — had not
// yet fenced itself and was still holding the VIP: split-brain. Keeping
// grace <= the peer threshold guarantees an isolated primary always fences
// itself no later than a peer could stand up a replacement.
func (m *Manager) recordDBFailure(ctx context.Context, err error) {
	m.mu.RLock()
	last := m.lastDBSuccess
	m.mu.RUnlock()

	grace := time.Duration(m.cfg.HA.HeartbeatTTLMs) * time.Millisecond
	if grace <= 0 {
		grace = 3 * time.Second // matches config.Default()'s heartbeat_ttl_ms
	}
	if time.Since(last) < grace {
		return
	}
	if m.sm.Current() == StateError {
		return
	}
	m.log.Error("ha: database unreachable beyond fencing grace period; demoting and going dark",
		"cluster_id", m.clusterID, "grace_ms", grace.Milliseconds(),
		"last_db_success", last, "cause", err)
	m.metricsRec.dbPartitionDetected(m.clusterID)
	m.transitionTo(ctx, StateError, "db_partition_detected")
}

// recordDBSuccess is called after any successful DB operation. It returns
// true exactly when this call just rejoined the cluster as SECONDARY after
// a fencing episode — callers use that to skip acting on this tick's result
// any further, so a node that regains DB access always spends at least one
// full cycle as SECONDARY before it can be promoted again (see
// state_machine.go's ERROR->PRIMARY prohibition).
func (m *Manager) recordDBSuccess(ctx context.Context) (justRecovered bool) {
	m.mu.Lock()
	m.lastDBSuccess = time.Now()
	wasError := m.sm.Current() == StateError
	m.mu.Unlock()

	if wasError {
		m.log.Info("ha: database reachable again; rejoining cluster as SECONDARY", "cluster_id", m.clusterID)
		m.transitionTo(ctx, StateSecondary, "db_recovered")
		return true
	}
	return false
}
