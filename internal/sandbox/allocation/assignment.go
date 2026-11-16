package allocation

import (
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox/workspace"
)

// Assignment is the persisted placement of one session. Only the config
// address, the shared machine (if any) and the release time are stored; the
// mode and the workspace layout follow from whether there is a machine, and
// the session keeps both for life whatever the config switch says later.
type Assignment struct {
	SessionID      string
	ConfigTenantID uint64
	ConfigID       string
	// Machine is the shared machine of a user-mode session; zero for a
	// session with a machine of its own.
	Machine    UserMachine
	ReleasedAt *time.Time
}

// Released reports a session whose workspace has been given back.
func (a Assignment) Released() bool { return a.ReleasedAt != nil }

// Mode is ModeUser for a session on a shared machine, ModeSession otherwise.
func (a Assignment) Mode() Mode {
	if a.Machine != (UserMachine{}) {
		return ModeUser
	}
	return ModeSession
}

// IsUser reports a session on a shared machine.
func (a Assignment) IsUser() bool { return a.Mode() == ModeUser }

// UserMachine returns the shared machine a user-mode session lives on.
func (a Assignment) UserMachine() (UserMachine, bool) {
	return a.Machine, a.IsUser()
}

// Layout is the workspace the session was assigned, independent of the
// config's current switch.
func (a Assignment) Layout() (workspace.Layout, error) {
	if a.IsUser() {
		return workspace.UserSession(a.SessionID)
	}
	return workspace.Remote(), nil
}
