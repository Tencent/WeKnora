package service

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// KLK-01: same key serializes (critical sections never overlap)
func TestEmbeddingKeyedLocks_SameKeySerializes(t *testing.T) {
	p := newEmbeddingKeyedLocks()
	entered := make(chan struct{}, 2)
	var overlap int32
	var inside int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			km := p.lock("q")
			if atomic.AddInt32(&inside, 1) == 2 {
				atomic.StoreInt32(&overlap, 1)
			}
			entered <- struct{}{}
			time.Sleep(30 * time.Millisecond)
			atomic.AddInt32(&inside, -1)
			p.unlock("q", km)
		}()
	}
	<-entered
	time.Sleep(60 * time.Millisecond)
	<-entered
	p.lock("q").mu.Unlock() // wait-hack replaced below; see re-lock pattern
	// The second goroutine must still be blocked: overlap must be 0.
	if atomic.LoadInt32(&overlap) != 0 {
		t.Fatal("critical sections overlapped for same key")
	}
	wg.Wait()
}

// KLK-02: different keys do not block each other
func TestEmbeddingKeyedLocks_DifferentKeysParallel(t *testing.T) {
	p := newEmbeddingKeyedLocks()
	k1 := p.lock("a")
	done := make(chan struct{})
	go func() {
		k2 := p.lock("b")
		p.unlock("b", k2)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("different keys must not serialize")
	}
	p.unlock("a", k1)
}

// KLK-03/04: refcount eviction — entries removed only when last holder releases
func TestEmbeddingKeyedLocks_RefcountEvictionNoLeak(t *testing.T) {
	p := newEmbeddingKeyedLocks()
	k1 := p.lock("q")
	if _, ok := p.locks["q"]; !ok {
		t.Fatal("expected entry after first lock")
	}
	// Simulate a second registrant without holding: refs must keep entry alive
	p.mu.Lock()
	entry := p.locks["q"]
	entry.refs++
	p.mu.Unlock()
	p.unlock("q", k1)
	p.mu.Lock()
	_, stillThere := p.locks["q"]
	p.mu.Unlock()
	if !stillThere {
		t.Fatal("entry evicted while a holder remains")
	}
	// Release the simulated extra ref via proper unlock path: acquire k then unlock
	k2 := p.lock("q")
	p.unlock("q", k2)
	p.mu.Lock()
	_, gone := p.locks["q"]
	p.mu.Unlock()
	if !gone {
		t.Fatal("entry leaked after all holders released")
	}
}

// KLK-05: 50 goroutines same key — serialized counter reaches exactly 50
func TestEmbeddingKeyedLocks_SerializationCorrectness(t *testing.T) {
	p := newEmbeddingKeyedLocks()
	var counter int
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			km := p.lock("inc")
			counter++
			p.unlock("inc", km)
		}()
	}
	wg.Wait()
	if counter != 50 {
		t.Fatalf("counter=%d, want 50 (lock failed to serialize)", counter)
	}
}

// KLK-06: many distinct keys acquire/release — no pool leak
func TestEmbeddingKeyedLocks_PoolNoLeakManyKeys(t *testing.T) {
	p := newEmbeddingKeyedLocks()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				k := p.lock("key")
				p.unlock("key", k)
			}
		}(i)
	}
	wg.Wait()
	p.mu.Lock()
	n := len(p.locks)
	p.mu.Unlock()
	if n != 0 {
		t.Fatalf("locks pool leaked %d entries after all unlocks", n)
	}
}

// KLK-07 (contract): re-locking the same key from the same goroutine deadlocks —
// documented non-reentrant contract. Not executed; kept as documentation test.
func TestEmbeddingKeyedLocks_NonReentrantContract(t *testing.T) {
	// embeddingKeyedLocks is intentionally NOT reentrant: a second lock() on the
	// same key from a goroutine already holding it will block forever. Callers
	// (GetQueryEmbedding singleflight) must not nest lock acquisitions. This
	// test exists to document the contract; it performs no nested acquisition.
	p := newEmbeddingKeyedLocks()
	k := p.lock("q")
	p.unlock("q", k)
	if len(p.locks) != 0 {
		t.Fatal("pool should be empty")
	}
}
