package container

import (
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/handler/session"
)

// HostApprovalModeLoader reads the desktop approval-mode preference.
// Nil (server binaries, tests that do not decorate) means ModeAsk.
type HostApprovalModeLoader func() string

// HostProjectDirsLoader is the approved-directory list from desktop prefs.
// Alias of the session-handler type so dig has one provider for both.
type HostProjectDirsLoader = session.HostProjectDirsLoader

func provideHostApprovalModeLoader() HostApprovalModeLoader { return nil }

func provideHostProjectDirsLoader() session.HostProjectDirsLoader { return nil }

// provideHostApprovalModes exposes the host sandbox's per-session approval
// mode to the session handler. Outside Lite the manager is absent and the
// handler answers 404.
func provideHostApprovalModes(host service.HostSandboxManager) session.HostApprovalModes {
	if modes, ok := host.Manager.(session.HostApprovalModes); ok {
		return modes
	}
	return nil
}
