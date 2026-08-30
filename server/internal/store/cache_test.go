package store

import (
	"testing"
	"time"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

func TestOptionCacheGetSetMiss(t *testing.T) {
	c := newOptionCache(time.Minute)
	key := optionCacheKey{code: 3, scopeID: "scope-1"}

	if _, ok := c.get(key); ok {
		t.Fatalf("expected miss on empty cache")
	}

	c.set(key, &models.DHCPOption{OptionCode: 3, Value: "10.0.0.1"})
	v, ok := c.get(key)
	if !ok {
		t.Fatalf("expected hit after set")
	}
	if v == nil || v.Value != "10.0.0.1" {
		t.Fatalf("unexpected cached value: %+v", v)
	}
}

func TestOptionCacheCachesNilAsFound(t *testing.T) {
	c := newOptionCache(time.Minute)
	key := optionCacheKey{code: 6, scopeID: "scope-1"}

	c.set(key, nil) // a resolved-to-not-found result is itself worth caching.
	v, ok := c.get(key)
	if !ok {
		t.Fatalf("expected hit for cached nil result")
	}
	if v != nil {
		t.Fatalf("expected nil cached value, got %+v", v)
	}
}

func TestOptionCacheExpires(t *testing.T) {
	c := newOptionCache(time.Millisecond)
	key := optionCacheKey{code: 1, scopeID: "scope-1"}
	c.set(key, &models.DHCPOption{OptionCode: 1, Value: "255.255.255.0"})

	time.Sleep(5 * time.Millisecond)

	if _, ok := c.get(key); ok {
		t.Fatalf("expected cache entry to have expired")
	}
}

func TestOptionCacheDifferentKeysDontCollide(t *testing.T) {
	c := newOptionCache(time.Minute)
	keyScope := optionCacheKey{code: 3, scopeID: "scope-1"}
	keyPool := optionCacheKey{code: 3, poolID: "pool-1"}

	c.set(keyScope, &models.DHCPOption{Value: "scope-value"})
	c.set(keyPool, &models.DHCPOption{Value: "pool-value"})

	v, ok := c.get(keyScope)
	if !ok || v.Value != "scope-value" {
		t.Fatalf("scope key returned wrong value: %+v", v)
	}
	v, ok = c.get(keyPool)
	if !ok || v.Value != "pool-value" {
		t.Fatalf("pool key returned wrong value: %+v", v)
	}
}

func TestOptionCacheInvalidateAll(t *testing.T) {
	c := newOptionCache(time.Minute)
	key := optionCacheKey{code: 3, scopeID: "scope-1"}
	c.set(key, &models.DHCPOption{Value: "10.0.0.1"})

	c.invalidateAll()

	if _, ok := c.get(key); ok {
		t.Fatalf("expected cache to be empty after invalidateAll")
	}
}
