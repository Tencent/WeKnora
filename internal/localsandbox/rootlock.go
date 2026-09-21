package localsandbox

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type rootLocks struct {
	mu    sync.Mutex
	locks map[string]*rootLease
}

type rootLease struct {
	mu   sync.Mutex
	refs int
}

func newRootLocks() *rootLocks {
	return &rootLocks{locks: make(map[string]*rootLease)}
}

func rootLockKey(root string) string {
	key := filepath.Clean(root)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

// LockRoot serializes mutating work against one workspace directory.
// Sessions that share a project share the lock; different directories do not
// block each other. The returned function always unlocks; call it with defer.
func (s *Service) LockRoot(root string) func() {
	if s == nil || s.roots == nil || strings.TrimSpace(root) == "" {
		return func() {}
	}
	return s.roots.lock(root)
}

func (r *rootLocks) lock(root string) func() {
	key := rootLockKey(root)
	r.mu.Lock()
	lease := r.locks[key]
	if lease == nil {
		lease = &rootLease{}
		r.locks[key] = lease
	}
	lease.refs++
	r.mu.Unlock()
	lease.mu.Lock()
	return func() {
		lease.mu.Unlock()
		r.mu.Lock()
		lease.refs--
		if lease.refs == 0 {
			delete(r.locks, key)
		}
		r.mu.Unlock()
	}
}
