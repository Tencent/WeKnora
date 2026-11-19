//go:build desktop

package container

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/background"
)

func TestDesktopWithoutHostManagerStaysRemoteOnly(t *testing.T) {
	bg := service.NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	jobs, err := newShellJobService(nil, nil, bg, nil, NewResourceCleaner(),
		service.HostSandboxManager{Desktop: true})
	require.NoError(t, err)
	require.False(t, jobs.LocalAvailable())
}
