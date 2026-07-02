package work

import (
	"runtime"
	"time"

	"github.com/kitwork/engine/value"
)

// CacheItem represents a general-purpose cached value inside the VM
type CacheItem struct {
	Value    value.Value
	ExpireAt time.Time
}

// GeneralCache wraps the tenant general-purpose cache namespace
type GeneralCache struct {
	tenant *Tenant
}

func (c *GeneralCache) GetCache(key string) (value.Value, bool) {
	c.tenant.lruCacheLock.RLock()
	defer c.tenant.lruCacheLock.RUnlock()
	item, ok := c.tenant.lruCache[key]
	if ok && (item.ExpireAt.IsZero() || time.Now().Before(item.ExpireAt)) {
		return item.Value, true
	}
	return value.Value{K: value.Nil}, false
}

func (c *GeneralCache) SetCache(key string, val value.Value, ttlVal value.Value) {
	var ttl time.Duration
	if !ttlVal.IsBlank() {
		if ttlVal.IsNumeric() {
			ttl = time.Duration(ttlVal.N) * time.Millisecond
		} else {
			ttl, _ = ParseDuration(ttlVal.Text())
		}
	}

	c.tenant.lruCacheLock.Lock()
	defer c.tenant.lruCacheLock.Unlock()

	// 1. Eviction Policy: cap size per tenant to prevent unbounded growth
	if len(c.tenant.lruCache) >= 1000 {
		// Evict the first key we iterate over (simple pseudo-random/FIFO approximation)
		for k := range c.tenant.lruCache {
			delete(c.tenant.lruCache, k)
			break
		}
	}

	// 2. Global Memory Defense: if host heap allocation is under high pressure, clear cache
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if m.Alloc > 512*1024*1024 { // 512 MB
		c.tenant.lruCache = make(map[string]*CacheItem)
	}

	var expireAt time.Time
	if ttl > 0 {
		expireAt = time.Now().Add(ttl)
	}

	c.tenant.lruCache[key] = &CacheItem{
		Value:    val,
		ExpireAt: expireAt,
	}
}

func (c *GeneralCache) DeleteCache(key string) {
	c.tenant.lruCacheLock.Lock()
	defer c.tenant.lruCacheLock.Unlock()
	delete(c.tenant.lruCache, key)
}

func (c *GeneralCache) ClearCache() {
	c.tenant.lruCacheLock.Lock()
	defer c.tenant.lruCacheLock.Unlock()
	c.tenant.lruCache = make(map[string]*CacheItem)
}

// VM-callable / reflection-based API

func (c *GeneralCache) Get(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Nil}
	}
	val, ok := c.GetCache(args[0].Text())
	if ok {
		return val
	}
	return value.Value{K: value.Nil}
}

func (c *GeneralCache) Set(args ...value.Value) value.Value {
	if len(args) < 2 {
		return value.Value{K: value.Nil}
	}
	key := args[0].Text()
	val := args[1]

	var ttlVal value.Value
	if len(args) > 2 {
		ttlVal = args[2]
	}

	c.SetCache(key, val, ttlVal)
	return val
}

func (c *GeneralCache) Delete(args ...value.Value) value.Value {
	if len(args) > 0 {
		c.DeleteCache(args[0].Text())
	}
	return value.Value{K: value.Nil}
}

func (c *GeneralCache) Clear(_ ...value.Value) value.Value {
	c.ClearCache()
	return value.Value{K: value.Nil}
}
