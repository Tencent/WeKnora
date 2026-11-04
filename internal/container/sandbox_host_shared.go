package container

import (
	"sync"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/handler/session"
)

// HostApprovalModeLoader reads the desktop approval-mode preference.
// An empty result means ModeAsk.
type HostApprovalModeLoader func() string

// HostProjectDirsLoader is the approved-directory list from desktop prefs.
// Alias of the session-handler type so dig has one provider for both.
type HostProjectDirsLoader = session.HostProjectDirsLoader

// The host sandbox is constructed inside BuildContainer. dig.Decorate after
// that returns does not rebuild it, so the providers close over these slots
// and read them on each call. The desktop process installs the prefs readers
// before BuildContainer; a server process leaves them unset.
var (
	hostLoaderMu     sync.RWMutex
	hostApprovalMode HostApprovalModeLoader
	hostProjectDirs  session.HostProjectDirsLoader
)

// SetHostApprovalModeLoader installs the process-wide default approval mode.
func SetHostApprovalModeLoader(fn HostApprovalModeLoader) {
	hostLoaderMu.Lock()
	hostApprovalMode = fn
	hostLoaderMu.Unlock()
}

// SetHostProjectDirsLoader installs the approved project-directory list.
func SetHostProjectDirsLoader(fn session.HostProjectDirsLoader) {
	hostLoaderMu.Lock()
	hostProjectDirs = fn
	hostLoaderMu.Unlock()
}

func provideHostApprovalModeLoader() HostApprovalModeLoader {
	return func() string {
		hostLoaderMu.RLock()
		fn := hostApprovalMode
		hostLoaderMu.RUnlock()
		if fn == nil {
			return ""
		}
		return fn()
	}
}

func provideHostProjectDirsLoader() session.HostProjectDirsLoader {
	return func() []string {
		hostLoaderMu.RLock()
		fn := hostProjectDirs
		hostLoaderMu.RUnlock()
		if fn == nil {
			return nil
		}
		return fn()
	}
}

// provideHostApprovalModes exposes the host sandbox's per-session approval
// mode to the session handler. Outside Lite the manager is absent and the
// handler answers 404.
func provideHostApprovalModes(host service.HostSandboxManager) session.HostApprovalModes {
	if modes, ok := host.Manager.(session.HostApprovalModes); ok {
		return modes
	}
	return nil
}
