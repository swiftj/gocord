package media

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Cache defaults: encoded payloads are kept 10 minutes, at most 64 entries
// and 200 MB, so chunked reads of one payload see identical bytes.
const (
	DefaultCacheEntries = 64
	DefaultCacheBytes   = 200 << 20
	DefaultCacheTTL     = 10 * time.Minute
)

// CacheKey is sha256(source key + options).
func CacheKey(s Source, o FetchOptions) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%d|%t", s.Key(), o.MaxDimension, o.Format, o.Quality, o.StripGIFToFirstFrame)))
	return hex.EncodeToString(sum[:])
}

// Cache is a size- and count-bounded LRU with a TTL.
type Cache struct {
	mu         sync.Mutex
	ll         *list.List
	items      map[string]*list.Element
	maxEntries int
	maxBytes   int
	bytes      int
	ttl        time.Duration
	now        func() time.Time
}

type cacheEntry struct {
	key     string
	val     *Fetched
	expires time.Time
}

// NewCache returns an empty cache.
func NewCache(maxEntries, maxBytes int, ttl time.Duration) *Cache {
	return &Cache{ll: list.New(), items: map[string]*list.Element{}, maxEntries: maxEntries, maxBytes: maxBytes, ttl: ttl, now: time.Now}
}

// Get returns a live entry and marks it recently used.
func (c *Cache) Get(key string) (*Fetched, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*cacheEntry)
	if c.now().After(e.expires) {
		c.removeLocked(el)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

// Put stores v, evicting expired and least recently used entries to stay in
// bounds. A value larger than the byte bound is not cached.
func (c *Cache) Put(key string, v *Fetched) {
	if v == nil || len(v.Bytes) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.removeLocked(el)
	}
	c.items[key] = c.ll.PushFront(&cacheEntry{key: key, val: v, expires: c.now().Add(c.ttl)})
	c.bytes += len(v.Bytes)
	now := c.now()
	for el := c.ll.Back(); el != nil; {
		prev := el.Prev()
		if now.After(el.Value.(*cacheEntry).expires) {
			c.removeLocked(el)
		}
		el = prev
	}
	for c.ll.Len() > c.maxEntries || c.bytes > c.maxBytes {
		c.removeLocked(c.ll.Back())
	}
}

// Len returns the number of entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

func (c *Cache) removeLocked(el *list.Element) {
	e := el.Value.(*cacheEntry)
	c.ll.Remove(el)
	delete(c.items, e.key)
	c.bytes -= len(e.val.Bytes)
}
