package dhcp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"

	"github.com/shamalawy/gdhcp/server/internal/config"
	"github.com/shamalawy/gdhcp/server/internal/metrics"
	"github.com/shamalawy/gdhcp/server/internal/models"
	"github.com/shamalawy/gdhcp/server/internal/store"
)

// maxConcurrentDispatch caps how many received packets are handled at once
// across all interfaces. It sits above the pgx pool's default max
// connections so ordinary bursts never queue on the semaphore -- the pool,
// not this, is the steady-state limiter -- while still bounding the
// goroutine and memory cost of a broadcast storm to something fixed.
const maxConcurrentDispatch = 256

// ServingGate lets the HA cluster manager decide whether this node should
// currently answer DHCP traffic, without the DHCP engine importing the ha
// package (amendment A10's decoupling rule: the DHCP engine defines this
// small consumer-side interface against its own needs; server/app.go wires
// the concrete ha.Manager implementation in).
type ServingGate interface {
	// ShouldServe reports whether this node should process and reply to
	// DHCP requests right now. When false, the engine still reads packets
	// off the wire (so sockets don't back up) but drops them silently
	// rather than replying.
	//
	// False means this node cannot safely answer at all — it is still
	// electing (INITIALIZING), fenced off from the database (ERROR), or
	// shutting down. It does NOT mean "another node is primary": per
	// amendment A11 and docs/design/ha-cluster.md's "Concurrent DHCP
	// Serving & Load-Sharing", PRIMARY and SECONDARY both answer, and a
	// client receiving two OFFERs is expected — RFC 2131 §4.4.1 has it pick
	// one, and the REQUEST that follows is settled by the atomic INSERT in
	// AllocateLease. Do not narrow this gate to primary-only; that demotes
	// the secondary to a passive standby and gives up load-sharing.
	ShouldServe() bool
}

// alwaysServe is the default gate used when the caller passes gate=nil to
// New (standalone/dev/test usage that has no HA manager to wire in).
type alwaysServe struct{}

func (alwaysServe) ShouldServe() bool { return true }

// storeDeps is the narrow slice of store.Store the engine actually calls,
// following amendment A10's preference for depending on narrower interfaces
// over the full aggregate where practical. New's public signature still
// accepts the full store.Store (as the task specifies), which satisfies
// storeDeps structurally; tests can supply a much smaller fake.
type storeDeps interface {
	store.LeaseStore
	store.ConfigStore
	store.StatsRecorder
	store.OptionCatalogStore

	// ListServers is the one ClusterStore method the engine needs, to size
	// the cluster for the T1 50%/40% split (docs/CONTRACTS.md Part 3).
	ListServers(ctx context.Context, clusterID string) ([]*models.ClusterServer, error)
}

// Engine is the DHCPv4 server: it owns one listener per configured
// interface, the DORA/DRAD state machine, and the background lease-expiry
// reaper.
type Engine struct {
	cfg   *config.Config
	store storeDeps
	gate  ServingGate
	log   *slog.Logger

	// metricsReg is optional (nil-safe): the constructor's fixed 4-argument
	// signature has no room for it, so integration wires it in after
	// construction via UseMetrics. See notes_for_integration in the task
	// hand-off for why.
	metricsReg *metrics.Registry

	// pingCheckEnabled/pingCheckTimeout gate the optional pre-OFFER
	// ICMP-echo conflict check (design doc "Optional Ping-Check Before
	// OFFER"). config.DHCPConfig has no field for this yet, so it defaults
	// off and is toggled via SetPingCheck; see notes_for_integration.
	pingCheckEnabled bool
	pingCheckTimeout time.Duration

	// expiryReaperInterval controls lease.go's background reaper. Same gap
	// as above: no config field yet, defaults to 60s, override via
	// SetExpiryReaperInterval.
	expiryReaperInterval time.Duration

	vrfCacheMu sync.RWMutex
	vrfCache   map[string]vrfCacheEntry

	clusterSizeMu sync.Mutex
	clusterSize   int
	clusterSizeAt time.Time

	listenersMu sync.Mutex
	listeners   map[string]*InterfaceListener

	// rawSenders holds one AF_PACKET sender per interface, used to address
	// a reply to a client that has no IP yet (see rawsend.go). An interface
	// is absent from the map when the socket could not be opened (no
	// CAP_NET_RAW), in which case sendReply falls back to the UDP path.
	rawSendersMu sync.Mutex
	rawSenders   map[string]*rawSender

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// dispatchSem bounds how many packets are being handled concurrently.
	// Every dispatch goroutine holds a slot for its lifetime, so a burst
	// (or a flood) parks in the kernel's socket buffer instead of turning
	// into unbounded goroutines each waiting on a pgx pool connection.
	// Acquiring it in serveListener rather than inside the goroutine is
	// what makes that backpressure real: the read loop stops reading while
	// saturated, and the kernel drops the excess -- the correct failure
	// mode for a broadcast protocol clients retry anyway.
	dispatchSem chan struct{}

	// serverID identifies this node in logs (HA server_id, else hostname).
	serverID string
}

// New constructs a DHCPv4 Engine. cfg supplies interface/VRF bindings and
// lease-time defaults (docs/CONTRACTS.md Part 4); st is the backing store
// (only the LeaseStore/ConfigStore/StatsRecorder/OptionCatalogStore slices
// and ClusterStore.ListServers are used — see storeDeps); gate lets HA
// silence this node when it is not the one that should answer (amendment
// A10); log receives one structured line per transaction (logging.go) plus
// engine lifecycle/error events. gate and log may be nil (a permissive
// always-serve gate and slog.Default() are used respectively) for
// standalone/dev/test use; cfg and st must not be nil.
func New(cfg *config.Config, st store.Store, gate ServingGate, log *slog.Logger) (*Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("dhcp: New: nil config")
	}
	if st == nil {
		return nil, fmt.Errorf("dhcp: New: nil store")
	}
	if gate == nil {
		gate = alwaysServe{}
	}
	if log == nil {
		log = slog.Default()
	}

	serverID := cfg.HA.ServerID
	if serverID == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			serverID = h
		} else {
			serverID = "gdhcp"
		}
	}

	return &Engine{
		cfg:                  cfg,
		store:                st,
		gate:                 gate,
		log:                  log,
		pingCheckEnabled:     false,
		pingCheckTimeout:     100 * time.Millisecond,
		expiryReaperInterval: 60 * time.Second,
		vrfCache:             make(map[string]vrfCacheEntry),
		listeners:            make(map[string]*InterfaceListener),
		rawSenders:           make(map[string]*rawSender),
		dispatchSem:          make(chan struct{}, maxConcurrentDispatch),
		serverID:             serverID,
	}, nil
}

// UseMetrics wires a Prometheus registry in for stats.go's Record* wrappers
// to publish gdhcp_dhcp_* counters (docs/CONTRACTS.md Part 8) alongside the
// store's own transaction counters. Optional; nil-safe if never called.
func (e *Engine) UseMetrics(r *metrics.Registry) { e.metricsReg = r }

// SetPingCheck enables/disables the pre-OFFER ICMP-echo conflict check and
// its per-attempt timeout (design doc "Optional Ping-Check Before OFFER").
// Disabled with a 100ms timeout by default.
func (e *Engine) SetPingCheck(enabled bool, timeout time.Duration) {
	e.pingCheckEnabled = enabled
	if timeout > 0 {
		e.pingCheckTimeout = timeout
	}
}

// SetExpiryReaperInterval overrides the default 60s interval between
// ExpireLeasesOlderThan sweeps. Must be called before Start.
func (e *Engine) SetExpiryReaperInterval(d time.Duration) {
	if d > 0 {
		e.expiryReaperInterval = d
	}
}

// Start opens a listener on every configured interface (config.DHCP.
// Interfaces) and starts the background expiry reaper. An empty interface
// list is valid (amendment A11: API/UI-only mode) and Start returns nil
// without opening any sockets. Start returns once every listener socket is
// open; packet processing continues on background goroutines until Stop.
func (e *Engine) Start(ctx context.Context) error {
	if len(e.cfg.DHCP.Interfaces) == 0 {
		e.log.Info("dhcp: no interfaces configured, running in API/UI-only mode (amendment A11)")
		return nil
	}

	e.ctx, e.cancel = context.WithCancel(ctx)

	e.listenersMu.Lock()
	for _, iface := range e.cfg.DHCP.Interfaces {
		l, err := e.newListener(iface)
		if err != nil {
			e.listenersMu.Unlock()
			_ = e.Stop()
			return fmt.Errorf("dhcp: start listener on %s: %w", iface.Name, err)
		}
		e.listeners[iface.Name] = l
		e.wg.Add(1)
		go e.serveListener(l)

		// Best-effort: without this, clients that leave the broadcast flag
		// clear (the Linux/Windows/macOS default) never receive an OFFER.
		// Not fatal -- the engine still serves broadcast-flag clients.
		rawOK := true
		rs, rawErr := newRawSender(iface.Name)
		if rawErr != nil {
			rawOK = false
			e.log.Warn("dhcp: no raw sender; clients that do not set the broadcast flag will not be answered on this interface",
				"interface", iface.Name, "error", rawErr, "hint", "grant CAP_NET_RAW")
		} else {
			e.rawSendersMu.Lock()
			e.rawSenders[iface.Name] = rs
			e.rawSendersMu.Unlock()
		}

		e.log.Info("dhcp: listening", "interface", iface.Name, "vrf_id_hint", iface.VRFID, "raw_send", rawOK)
	}
	e.listenersMu.Unlock()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.runExpiryReaper(e.ctx)
	}()

	return nil
}

func (e *Engine) serveListener(l *InterfaceListener) {
	defer e.wg.Done()
	for {
		rbuf := make([]byte, 4096) // fresh per read: dispatch below keeps using it after this loop moves on
		n, cm, peer, err := l.pconn.ReadFrom(rbuf)
		if err != nil {
			if e.ctx == nil || e.ctx.Err() == nil {
				e.log.Error("dhcp: listener stopped unexpectedly", "interface", l.ifname, "error", err)
			}
			return
		}

		m, err := dhcpv4.FromBytes(rbuf[:n])
		if err != nil {
			continue
		}

		upeer, ok := peer.(*net.UDPAddr)
		if !ok {
			continue
		}
		// Mirrors server4.Server.Serve: a client with no IP yet (e.g.
		// DISCOVER) reports peer 0.0.0.0, which isn't usable as a reply
		// destination, so normalize it to the limited broadcast address.
		if upeer.IP == nil || upeer.IP.To4().Equal(net.IPv4zero) {
			upeer = &net.UDPAddr{IP: net.IPv4bcast, Port: upeer.Port}
		}

		var dst net.IP
		if cm != nil {
			dst = cm.Dst
		}

		// Take a dispatch slot before spawning. If every slot is busy this
		// blocks the read loop, which is the point -- see dispatchSem.
		// Stop() cancels e.ctx, so a saturated engine still shuts down
		// instead of wedging here.
		select {
		case e.dispatchSem <- struct{}{}:
		case <-ctxDone(e.ctx):
			return
		}

		// Tracked in e.wg so Stop's Wait actually covers in-flight
		// transactions. serveListener holds its own wg count for the whole
		// of this loop, so the counter is never at zero when this Add runs.
		e.wg.Add(1)
		go func(conn net.PacketConn, peer *net.UDPAddr, m *dhcpv4.DHCPv4, ifname string, dst net.IP) {
			defer e.wg.Done()
			defer func() { <-e.dispatchSem }()
			e.dispatch(conn, peer, m, ifname, dst)
		}(l.conn, upeer, m, l.ifname, dst)
	}
}

// ctxDone returns ctx's done channel, tolerating the nil context the engine
// carries before Start (tests construct an Engine directly). A nil channel
// blocks forever in a select, which is the correct "never cancelled" case.
func ctxDone(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}

// Stop closes every listener socket, cancels the reaper, and waits for all
// background goroutines to exit.
func (e *Engine) Stop() error {
	if e.cancel != nil {
		e.cancel()
	}

	e.listenersMu.Lock()
	for _, l := range e.listeners {
		_ = l.Close()
	}
	e.listenersMu.Unlock()

	e.rawSendersMu.Lock()
	for name, rs := range e.rawSenders {
		_ = rs.Close()
		delete(e.rawSenders, name)
	}
	e.rawSendersMu.Unlock()

	e.wg.Wait()
	return nil
}

// rawSenderFor returns the AF_PACKET sender for ifname, or nil when one
// could not be opened at Start (see rawsend.go).
func (e *Engine) rawSenderFor(ifname string) *rawSender {
	e.rawSendersMu.Lock()
	defer e.rawSendersMu.Unlock()
	return e.rawSenders[ifname]
}

// dispatch is invoked by serveListener for every parsed DHCPv4 packet
// received on ifname (dst is its captured destination address, or nil if
// that couldn't be captured -- see DHCPPacket.Dst). It applies the
// ServingGate, classifies the message by type, and routes to the matching
// DORA handler in dora.go.
func (e *Engine) dispatch(conn net.PacketConn, peer net.Addr, m *dhcpv4.DHCPv4, ifname string, dst net.IP) {
	if !e.gate.ShouldServe() {
		// This node cannot answer safely right now (still electing, fenced
		// off from the database, or shutting down) -- drop silently rather
		// than reply. Note this is not "another node is primary": per
		// amendment A11 both cluster roles serve. See ServingGate.
		return
	}
	if m == nil {
		return
	}

	start := time.Now()
	ctx := e.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	pkt := &DHCPPacket{ReceivedAt: start, Interface: ifname, Conn: conn, Peer: peer, Packet: m, Dst: dst}

	var err error
	switch m.MessageType() {
	case dhcpv4.MessageTypeDiscover:
		err = e.handleDiscover(ctx, pkt)
	case dhcpv4.MessageTypeRequest:
		err = e.handleRequest(ctx, pkt)
	case dhcpv4.MessageTypeDecline:
		err = e.handleDecline(ctx, pkt)
	case dhcpv4.MessageTypeRelease:
		err = e.handleRelease(ctx, pkt)
	case dhcpv4.MessageTypeInform:
		err = e.handleInform(ctx, pkt)
	default:
		e.log.Debug("dhcp: ignoring unsupported message type", "type", m.MessageType().String(), "interface", ifname)
		return
	}

	if err != nil {
		e.logRequestError(pkt, err, time.Since(start))
	}
}

// newListener opens a server4-backed UDP socket bound to iface.Name (port
// 67, broadcast-capable); serveListener reads it and dispatches every
// packet to this engine.
func (e *Engine) newListener(iface config.DHCPInterface) (*InterfaceListener, error) {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: dhcpv4.ServerPort}
	return newInterfaceListener(iface.Name, addr)
}
