package tools

// Serialize mutations that target the same sandbox file.
//
// write_sandbox_file in append mode and edit_sandbox_file are both
// read-modify-write against a remote filesystem that exposes no atomic
// primitive. When the model emits several tool calls in one response the
// engine may run them concurrently, and two calls touching one path then read
// the same bytes and the second write silently discards the first — the failure
// looks like the model's own output going missing, which is unfalsifiable from
// the logs.
//
// Serialize per path, so unrelated files still run concurrently. A global lock
// would be correct too, but it would erase the point of parallel tool calls
// for the workload that uses them most.

import (
	"sync"
	"sync/atomic"
)

// sandboxFileMutationEpoch counts completed sandbox file mutations across the
// process. read_sandbox_file caches a downloaded file to serve pages from, and
// this is what lets that cache notice a write.
//
// Stat alone is not enough to detect a change: a same-length replacement
// (flipping a digit, say) leaves the size identical, and mtime resolution is
// one second on some backends, so an edit and a re-read in the same second can
// look untouched. A counter cannot miss that.
//
// It is global rather than per path, which over-invalidates — writing file B
// drops a cached file A. That costs one re-download in a case that barely
// happens (paging a file while writing another) and buys a counter that needs
// no bookkeeping and cannot grow.
var sandboxFileMutationEpoch atomic.Uint64

// sandboxMutationEpoch reads the current value, for a cache to record and
// later compare against.
func sandboxMutationEpoch() uint64 { return sandboxFileMutationEpoch.Load() }

type fileMutationQueue struct {
	mu    sync.Mutex
	locks map[string]*fileMutationLock
}

type fileMutationLock struct {
	mu sync.Mutex
	// refs keeps the entry alive while callers are queued on it, so the map
	// does not accumulate one lock per file ever written in the process.
	refs int
}

var sandboxFileMutations = &fileMutationQueue{locks: map[string]*fileMutationLock{}}

// lockFile blocks until this key is free and returns the release function.
func (q *fileMutationQueue) lockFile(key string) func() {
	q.mu.Lock()
	entry, ok := q.locks[key]
	if !ok {
		entry = &fileMutationLock{}
		q.locks[key] = entry
	}
	entry.refs++
	q.mu.Unlock()

	entry.mu.Lock()

	return func() {
		entry.mu.Unlock()
		q.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(q.locks, key)
		}
		q.mu.Unlock()
	}
}

// lockSandboxFile serializes mutations to one file within one session. Paths
// are already cleaned and absolute by the time callers reach this. Releasing
// the lock marks the mutation done, which is what invalidates read caches.
func lockSandboxFile(sessionID, filePath string) func() {
	release := sandboxFileMutations.lockFile(sessionID + "\x00" + filePath)
	return func() {
		sandboxFileMutationEpoch.Add(1)
		release()
	}
}
