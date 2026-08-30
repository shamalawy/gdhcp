package store

import (
	"sync"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// optionCacheKey identifies one ResolveOption call's inheritance context.
type optionCacheKey struct {
	code          int16
	scopeID       string
	poolID        string
	reservationID string
}

// optionCacheEntry caches the resolved option (nil Value means "resolved to
// not found", which is itself worth caching to avoid repeatedly walking the
// full inheritance chain for options nothing has set).
type optionCacheEntry struct {
	value     *models.DHCPOption
	expiresAt time.Time
}

// optionCache is a TTL cache for ConfigStore.ResolveOption results
// (config.DatabaseConfig.OptionCacheTTL, docs/AMENDMENTS.md store-unit
// spec). It is invalidated wholesale on any option/scope/pool/reservation/
// template write via invalidateAll, and lazily on read via TTL expiry —
// deliberately simple over partial/keyed invalidation, since a full flush is
// cheap (resolution is a handful of indexed point lookups) and correctness
// under concurrent writers is far easier to reason about.
type optionCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[optionCacheKey]optionCacheEntry
}

func newOptionCache(ttl time.Duration) *optionCache {
	return &optionCache{
		ttl:     ttl,
		entries: make(map[optionCacheKey]optionCacheEntry),
	}
}

// get returns (value, true) on a live cache hit. value may itself be nil
// (a cached "not found" result).
func (c *optionCache) get(key optionCacheKey) (*models.DHCPOption, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.value, true
}

func (c *optionCache) set(key optionCacheKey, value *models.DHCPOption) {
	c.mu.Lock()
	c.entries[key] = optionCacheEntry{value: value, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

// invalidateAll drops every cached entry. Called from the invalidation hook
// tied to config-change notifications: every OptionValueStore/OptionCatalog/
// TemplateStore write calls this directly (immediate local consistency),
// and it is also wired to fire on incoming ListenConfigChanges notifications
// from peer servers by the HA config watcher (which holds this store's
// concrete *Postgres type) so a secondary's cache clears promptly when a
// primary elsewhere changes option config.
func (c *optionCache) invalidateAll() {
	c.mu.Lock()
	c.entries = make(map[optionCacheKey]optionCacheEntry)
	c.mu.Unlock()
}

// InvalidateOptionCache exposes optionCache.invalidateAll for callers
// outside this package holding a concrete *Postgres (e.g. the HA config
// watcher reacting to a ListenConfigChanges notification from another
// server in the cluster). Not part of the Store interface — see
// StatsFlusher's doc comment for why narrow additions live outside Store.
func (p *Postgres) InvalidateOptionCache() {
	p.cache.invalidateAll()
}
