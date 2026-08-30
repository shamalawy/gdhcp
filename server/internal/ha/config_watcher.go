package ha

import (
	"context"
	"sync"
	"time"
)

// broadcaster fans a signal out to any number of Subscribe()rs without ever
// blocking the publisher: each subscriber channel is buffered 1 and sent to
// non-blocking, so a slow/inattentive subscriber just coalesces multiple
// signals into one and misses none of the "something changed, re-check"
// intent.
type broadcaster struct {
	mu   sync.Mutex
	subs []chan struct{}
}

func newBroadcaster() *broadcaster {
	return &broadcaster{}
}

func (b *broadcaster) Subscribe() <-chan struct{} {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

func (b *broadcaster) publish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Subscribe returns a channel that receives a signal whenever this node
// observes a cluster config change (LISTEN/NOTIFY or the poll fallback
// below), for the DHCP engine and API layer to invalidate their in-memory
// caches by (amendment A10's consumer-side-interface pattern). Safe to call
// any number of times, including before Start.
func (m *Manager) Subscribe() <-chan struct{} {
	return m.subs.Subscribe()
}

// watchConfigLoop maintains a LISTEN connection on configChannel (when
// cfg.HA.ConfigListenEnabled) and, regardless, polls every
// config_check_interval_ms as a fallback — docs/design/ha-cluster.md:
// "even if NOTIFY fails, each server re-reads config every
// config_check_interval_ms". Every notification or poll tick re-fetches the
// cluster row (so VIP/timing changes made through the API take effect) and
// fans a signal out to Subscribe()rs.
func (m *Manager) watchConfigLoop(ctx context.Context) {
	defer m.wg.Done()

	interval := time.Duration(m.cfg.HA.ConfigCheckIntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = 5 * time.Second
	}
	pollTicker := time.NewTicker(interval)
	defer pollTicker.Stop()

	var notifications <-chan string
	var closeListen func() error
	if m.cfg.HA.ConfigListenEnabled {
		notifications, closeListen = m.openConfigListen(ctx)
	}
	defer func() {
		if closeListen != nil {
			_ = closeListen()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case payload, ok := <-notifications:
			if !ok {
				// The LISTEN connection dropped. Reconnect eagerly; the
				// poll ticker covers us in the meantime regardless.
				m.log.Warn("ha: config LISTEN connection closed, reconnecting")
				if closeListen != nil {
					_ = closeListen()
				}
				notifications, closeListen = m.openConfigListen(ctx)
				continue
			}
			m.log.Debug("ha: config change notification received", "channel", configChannel, "payload", payload)
			m.onConfigChanged(ctx)

		case <-pollTicker.C:
			if notifications == nil && m.cfg.HA.ConfigListenEnabled {
				// Retry a previously failed/dropped LISTEN at most once per
				// poll interval, rather than busy-looping.
				notifications, closeListen = m.openConfigListen(ctx)
			}
			m.onConfigChanged(ctx)
		}
	}
}

func (m *Manager) openConfigListen(ctx context.Context) (<-chan string, func() error) {
	notifications, closeFn, err := m.store.ListenConfigChanges(ctx, configChannel)
	if err != nil {
		m.log.Warn("ha: failed to open config LISTEN connection, relying on poll fallback", "channel", configChannel, "error", err)
		return nil, nil
	}
	return notifications, closeFn
}

func (m *Manager) onConfigChanged(ctx context.Context) {
	m.refreshCluster(ctx)
	m.subs.publish()
}
