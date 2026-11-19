// Package container - background-work wiring.
package container

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/application/service"
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

// redisNamespace matches the session-binding store: WEKNORA_REDIS_NAMESPACE,
// or "weknora" when that variable is empty.
func redisNamespace() string {
	namespace := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE"))
	if namespace == "" {
		return "weknora"
	}
	return namespace
}

// newRemoteShellJobService wires the shell feature into BackgroundWork:
// Handler, the throttled keep-aliver, and (when Redis is configured) the job
// index. Dig provides the build-tagged newShellJobService; the web stub
// forwards here. The agent service depends on that constructor, so dig builds
// it first.
func newRemoteShellJobService(
	rdb *redis.Client,
	pinned *service.PinnedSessionSandbox,
	bg *service.BackgroundWork,
	streams interfaces.StreamManager,
) *service.ShellJobService {
	var index *service.ShellJobIndex
	if rdb != nil {
		index = service.NewShellJobIndex(rdb, redisNamespace())
	}
	// A nil stream manager must stay a nil pointer. Storing (*Emitter)(nil)
	// in the lifecycle's event interface panics on the first call.
	var events *service.BackgroundEventEmitter
	if streams != nil {
		events = service.NewBackgroundEventEmitter(streams)
	}
	svc := service.NewShellJobService(pinned, bg, index, events, service.NewBackgroundKeepAliver(pinned))
	if bg != nil {
		bg.Handler = svc
	}
	return svc
}

// startShellJobSweeper runs the remote handoff loop. Lite has no Redis index,
// so RemoteAvailable is false and nothing is started.
func startShellJobSweeper(svc *service.ShellJobService, cleaner interfaces.ResourceCleaner) {
	if svc == nil || !svc.RemoteAvailable() || cleaner == nil {
		return
	}
	sweeperCtx, stop := context.WithCancel(context.Background())
	svc.StartSweeper(sweeperCtx, 5*time.Second)
	cleaner.RegisterWithName("ShellJobSweeper", func() error {
		stop()
		return nil
	})
}
