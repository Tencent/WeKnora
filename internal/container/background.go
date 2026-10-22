// Package container - background-work wiring.
package container

import (
	"github.com/Tencent/WeKnora/internal/application/service/sandboxalloc"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// newBackgroundActivityRegistry returns the registry of live background work.
// With Redis it lives on the binding store, so turn leases, the rebuild check
// and the rewind lock read it in the same Lua call. The memory store uses
// the same lock for activity registration and lifecycle checks.
func newBackgroundActivityRegistry(
	store sandbox.SessionSandboxBindingStore, allocations *sandboxalloc.Service,
) interfaces.ActivityRegistry {
	switch store := store.(type) {
	case *sandbox.RedisSessionSandboxBindingStore:
		registry := sandbox.NewRedisActivityRegistry(store)
		if allocations != nil {
			registry.WithAllocations(allocations)
		}
		return registry
	case *sandbox.MemorySessionSandboxBindingStore:
		return sandbox.NewMemoryActivityRegistry(store)
	}
	return background.NewMemoryActivityRegistry(nil)
}

// newBackgroundNoticeInbox returns the completion-notice inbox. With Redis it
// lives next to the steer lists; otherwise it is process-local, like the
// in-memory stream manager it follows.
func newBackgroundNoticeInbox(streams interfaces.StreamManager) interfaces.NoticeInbox {
	if redisStreams, ok := streams.(*stream.RedisStreamManager); ok {
		return stream.NewRedisNoticeInbox(redisStreams, nil)
	}
	return background.NewMemoryNoticeInbox(nil)
}
