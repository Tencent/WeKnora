//go:build !desktop

package container

import (
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// newShellJobService is the web constructor dig provides. Host wiring and the
// local-job cleaner are desktop-only, so this build ignores them and forwards
// to the remote service.
func newShellJobService(
	rdb *redis.Client,
	pinned *service.PinnedSessionSandbox,
	bg *service.BackgroundWork,
	streams interfaces.StreamManager,
	_ interfaces.ResourceCleaner,
	_ service.HostSandboxManager,
) (*service.ShellJobService, error) {
	return newRemoteShellJobService(rdb, pinned, bg, streams), nil
}
