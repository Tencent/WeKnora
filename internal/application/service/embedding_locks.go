package service

import "sync"

// embeddingKeyedLocks provides per-key mutual exclusion (singleflight-ish) for
// deduplicating concurrent identical work, e.g. embedding the same query
// text from multiple goroutines. Locks are refcounted and evicted once
// unused so the pool cannot grow unboundedly over a long-lived process.
type embeddingKeyedLocks struct {
	mu    sync.Mutex
	locks map[string]*embeddingKeyLock
}

type embeddingKeyLock struct {
	mu   sync.Mutex
	refs int
}

func newEmbeddingKeyedLocks() *embeddingKeyedLocks {
	return &embeddingKeyedLocks{locks: make(map[string]*embeddingKeyLock)}
}

// lock returns a held mutex for key, registering the caller as a holder.
func (p *embeddingKeyedLocks) lock(key string) *embeddingKeyLock {
	p.mu.Lock()
	km, ok := p.locks[key]
	if !ok {
		km = &embeddingKeyLock{}
		p.locks[key] = km
	}
	km.refs++
	p.mu.Unlock()
	km.mu.Lock()
	return km
}

// unlock releases key, evicting the entry when no holders remain.
func (p *embeddingKeyedLocks) unlock(key string, km *embeddingKeyLock) {
	km.mu.Unlock()
	p.mu.Lock()
	km.refs--
	if km.refs <= 0 {
		delete(p.locks, key)
	}
	p.mu.Unlock()
}
