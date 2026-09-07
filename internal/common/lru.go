package common

import (
	"container/list"
	"sync"
	"time"
)

// LRUCache is a minimal thread-safe LRU cache for in-process use.
// It supports bounded capacity and optional item TTL (checked lazily on Get).
type LRUCache struct {
	mu       sync.Mutex
	maxItems int
	items    map[string]*list.Element
	order    *list.List
	ttl      time.Duration
}

type lruEntry struct {
	key       string
	value     any
	expiresAt time.Time
}

// NewLRUCache creates an LRU cache holding up to maxItems entries.
// If ttl > 0, entries older than ttl are treated as missing on Get.
func NewLRUCache(maxItems int, ttl time.Duration) *LRUCache {
	return &LRUCache{
		maxItems: maxItems,
		items:    make(map[string]*list.Element),
		order:    list.New(),
		ttl:      ttl,
	}
}

// Get returns the cached value for key, or nil if absent/expired.
func (c *LRUCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	entry := el.Value.(*lruEntry)
	if c.ttl > 0 && time.Now().After(entry.expiresAt) {
		c.removeLocked(el)
		return nil, false
	}
	c.order.MoveToFront(el)
	return entry.value, true
}

// Set inserts or updates key with value.
func (c *LRUCache) Set(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value.(*lruEntry).value = value
		if c.ttl > 0 {
			el.Value.(*lruEntry).expiresAt = time.Now().Add(c.ttl)
		}
		c.order.MoveToFront(el)
		return
	}
	entry := &lruEntry{key: key, value: value}
	if c.ttl > 0 {
		entry.expiresAt = time.Now().Add(c.ttl)
	}
	el := c.order.PushFront(entry)
	c.items[key] = el
	if c.order.Len() > c.maxItems {
		c.removeLocked(c.order.Back())
	}
}

func (c *LRUCache) removeLocked(el *list.Element) {
	entry := el.Value.(*lruEntry)
	delete(c.items, entry.key)
	c.order.Remove(el)
}

// Len returns the current number of cached entries.
func (c *LRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
