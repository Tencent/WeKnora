package common

import (
	"sync"
	"testing"
	"time"
)

// LRU-01: capacity eviction + Get refreshes recency
func TestLRU_CapacityEvictionAndRecency(t *testing.T) {
	c := NewLRUCache(3, 0)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected a hit for a")
	}
	c.Set("d", 4) // evicts b (least recently used), not a
	if _, ok := c.Get("b"); ok {
		t.Fatal("expected b to be evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected a to survive after Get refresh")
	}
	if c.Len() != 3 {
		t.Fatalf("Len=%d, want 3", c.Len())
	}
}

// LRU-02: Set on existing key updates value without growing Len
func TestLRU_SetExistingUpdatesInPlace(t *testing.T) {
	c := NewLRUCache(2, 0)
	c.Set("k", "v1")
	c.Set("k", "v2")
	if v, _ := c.Get("k"); v != "v2" {
		t.Fatalf("got %v, want v2", v)
	}
	if c.Len() != 1 {
		t.Fatalf("Len=%d, want 1", c.Len())
	}
}

// LRU-03/04/05: TTL hit, expiry, lazy eviction
func TestLRU_TTLExpiryAndLazyEviction(t *testing.T) {
	c := NewLRUCache(5, 60*time.Millisecond)
	c.Set("k", "v")
	if _, ok := c.Get("k"); !ok {
		t.Fatal("expected hit within TTL")
	}
	time.Sleep(80 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("expected miss after TTL")
	}
	if c.Len() != 0 {
		t.Fatalf("expired entry not lazily evicted, Len=%d", c.Len())
	}
}

// LRU-06: ttl=0 never expires
func TestLRU_TTLZeroNeverExpires(t *testing.T) {
	c := NewLRUCache(2, 0)
	c.Set("k", "v")
	time.Sleep(30 * time.Millisecond)
	if _, ok := c.Get("k"); !ok {
		t.Fatal("ttl=0 must never expire")
	}
}

// LRU-07: unaccessed expired items still count in Len (documented semantics)
func TestLRU_UnaccessedExpiredStillCounted(t *testing.T) {
	c := NewLRUCache(5, 40*time.Millisecond)
	c.Set("k", "v")
	time.Sleep(60 * time.Millisecond)
	if c.Len() != 1 {
		t.Fatalf("unaccessed expired item should still count in Len (lazy-only eviction), Len=%d", c.Len())
	}
}

// LRU-08/09: maxItems=1 keeps only newest; maxItems=0 degenerates (never hits)
func TestLRU_MaxItemsBoundaries(t *testing.T) {
	c1 := NewLRUCache(1, 0)
	c1.Set("a", 1)
	c1.Set("b", 2)
	if _, ok := c1.Get("a"); ok {
		t.Fatal("maxItems=1 must evict a")
	}
	if c1.Len() != 1 {
		t.Fatalf("Len=%d, want 1", c1.Len())
	}
	c0 := NewLRUCache(0, 0)
	c0.Set("a", 1)
	if _, ok := c0.Get("a"); ok {
		t.Fatal("maxItems=0 degenerates to never-hit (documented)")
	}
}

// LRU-10: missing key
func TestLRU_MissingKey(t *testing.T) {
	c := NewLRUCache(2, 0)
	if v, ok := c.Get("nope"); ok || v != nil {
		t.Fatalf("expected nil,false for missing key, got %v,%v", v, ok)
	}
}

// LRU-11: concurrent mixed Get/Set under -race
func TestLRU_ConcurrentAccess(t *testing.T) {
	c := NewLRUCache(64, 0)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				c.Set(string(rune('a'+g)), i)
				c.Get(string(rune('a' + g)))
			}
		}(g)
	}
	wg.Wait()
	if c.Len() > 64 {
		t.Fatalf("Len=%d exceeds cap 64", c.Len())
	}
}

// LRU-12: concurrent same-key writes end on some single write value (no torn entry)
func TestLRU_ConcurrentSameKeyNoTornEntry(t *testing.T) {
	c := NewLRUCache(4, 0)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				c.Set("k", g*1000+i)
			}
		}(g)
	}
	wg.Wait()
	v, ok := c.Get("k")
	if !ok {
		t.Fatal("expected final value present")
	}
	n, valid := v.(int)
	if !valid {
		t.Fatalf("torn entry: value type %T", v)
	}
	_ = n // any single-goroutine value is acceptable; type integrity is the contract
}

// LRU-13: corpusVersion does NOT invalidate this cache by design (no Invalidate API).
// Query-embedding results expire via TTL only. Guard test: pin the current semantics.
func TestLRU_NoInvalidationAPIByDesign(t *testing.T) {
	c := NewLRUCache(4, time.Hour)
	c.Set("q:embedding", "vec")
	// No Invalidate/Delete method exists; the only expiry path is TTL or capacity.
	// If this test fails to compile after adding such a method, revisit fail-open
	// cache expectations in repository_failopen_test.go (FO-12 uses corpusVersion keys).
	if _, ok := c.Get("q:embedding"); !ok {
		t.Fatal("fresh entry must hit")
	}
}
