package service

import (
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// BackgroundWork bundles the shared background-work parts the agent, the
// session handlers and features use. Handler is nil until a feature that
// submits background work is wired; until then Enabled is false and nothing
// background-related reaches a run.
type BackgroundWork struct {
	Registry  interfaces.ActivityRegistry
	Inbox     interfaces.NoticeInbox
	Listeners *background.Listeners
	Handler   interfaces.WorkHandler
}

// NewBackgroundWork returns the bundle with an empty listener set.
func NewBackgroundWork(registry interfaces.ActivityRegistry, inbox interfaces.NoticeInbox) *BackgroundWork {
	return &BackgroundWork{
		Registry:  registry,
		Inbox:     inbox,
		Listeners: background.NewListeners(registry, inbox, logger.Warnf),
	}
}

// Enabled reports whether background work can exist at all.
func (b *BackgroundWork) Enabled() bool {
	return b != nil && b.Handler != nil && b.Registry != nil && b.Inbox != nil
}
