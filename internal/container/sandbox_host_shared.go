package container

import "github.com/Tencent/WeKnora/internal/handler/session"

// HostApprovalModeLoader reads the desktop approval-mode preference.
// Nil (server binaries, tests that do not decorate) means ModeAuto.
type HostApprovalModeLoader func() string

// HostProjectDirsLoader is the approved-directory list from desktop prefs.
// Alias of the session-handler type so dig has one provider for both.
type HostProjectDirsLoader = session.HostProjectDirsLoader

func provideHostApprovalModeLoader() HostApprovalModeLoader { return nil }

func provideHostProjectDirsLoader() session.HostProjectDirsLoader { return nil }
