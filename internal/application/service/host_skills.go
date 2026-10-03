package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

// HostSkillTree is the on-disk skill version tree used by Lite local installs.
// Implemented by internal/localsandbox/skilltree.Tree.
type HostSkillTree interface {
	Root() string
	VersionsRoot() string
	NewVersion(name string) (string, error)
	Activate(name, versionDir string) (string, error)
	Discard(versionDir string) error
	Prune(name string, keep ...string) error
	Remove(name string) error
	Installed(name string) bool
	Sweep() error
	Lock(name string) (func(), error)
}

// HostSkillInstaller is the install-only sandbox used to materialize skill
// packages. Implemented by adapter.InstallManager.
type HostSkillInstaller interface {
	sandbox.Manager
	sandbox.SessionInstallCapabilityProvider
	sandbox.SessionFileReader
	StatSessionFile(ctx context.Context, sessionID, filePath string) (*sandbox.RemoteStatEntry, error)
	WriteSessionFile(ctx context.Context, sessionID, filePath string, content []byte) error
	Bind(sessionID, dir string) (release func())
}
