//go:build desktop

package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/localsandbox/adapter"
	"github.com/Tencent/WeKnora/internal/localsandbox/skilltree"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

// Host adapters must not version the user's workspace. This compile-time check
// keeps WorkspaceCheckpointer from committing a real project if the adapter is
// ever injected as the process-wide shell runner.
var (
	_ service.WorkspaceVersioning = (*adapter.Adapter)(nil)
	_ service.HostSkillTree       = (*skilltree.Tree)(nil)
	_ service.HostSkillInstaller  = (*adapter.InstallManager)(nil)
)

type hostSandboxDeps struct {
	newBackend func() (localsandbox.Backend, error)
	homeDir    string
	appDataDir string
	// sessionRoot is where auto-allocated session workspaces go. Empty falls
	// back to ~/Documents/WeKnoraLite.
	sessionRoot string
	skillsRoot  string
	// projects maps a session to a user-approved project directory.
	// Nil means every session gets an auto-allocated workspace.
	projects localsandbox.ProjectLookup
	// modes reports the approval mode. nil means ModeAuto.
	modes localsandbox.ModeLookup
}

type hostSandboxParts struct {
	manager sandbox.Manager
	service *localsandbox.Service
	builder *localsandbox.PolicyBuilder
}

// buildHostSandbox returns the process-wide host backend parts, or nil when
// this machine cannot enforce one. Web/server binaries never compile this
// file (see sandbox_host_stub.go); the desktop build tag is the isolation.
// Never returns parts that would run commands unsandboxed.
func buildHostSandbox(deps hostSandboxDeps) *hostSandboxParts {
	if deps.newBackend == nil {
		return nil
	}
	backend, err := deps.newBackend()
	if err != nil || backend == nil {
		return nil
	}
	if err := backend.Available(); err != nil {
		return nil
	}

	homeDir := strings.TrimSpace(deps.homeDir)
	appDataDir := strings.TrimSpace(deps.appDataDir)
	if homeDir == "" || appDataDir == "" {
		return nil
	}

	sessionRoot := strings.TrimSpace(deps.sessionRoot)
	if sessionRoot == "" {
		sessionRoot = defaultHostSessionRoot(homeDir)
	}

	resolver := localsandbox.NewWorkspaceResolver(localsandbox.DirLayout{
		SessionRoot: sessionRoot,
	}, deps.projects)
	builder := localsandbox.NewPolicyBuilder(homeDir, appDataDir).WithSkillsRoot(deps.skillsRoot)
	svc := localsandbox.NewService(backend, resolver, builder, deps.modes)
	return &hostSandboxParts{manager: adapter.New(svc), service: svc, builder: builder}
}

// buildHostSandboxManager returns the process-wide host backend, or nil when
// this machine cannot enforce one.
func buildHostSandboxManager(deps hostSandboxDeps) sandbox.Manager {
	if parts := buildHostSandbox(deps); parts != nil {
		return parts.manager
	}
	return nil
}

// hostModeLookup reads the machine-local approval mode from desktop prefs.
// A nil loader means ModeAuto.
func hostModeLookup(load HostApprovalModeLoader) localsandbox.ModeLookup {
	if load == nil {
		return nil
	}
	return prefsModeLookup{load: load}
}

// prefsModeLookup reads the machine-local approval mode. The session
// argument is unused: approval mode is process-wide, not per session.
//
// The stored value comes from a file the user can hand-edit. Unknown values
// become Auto. Known-but-unshipped modes are left intact so Service can refuse
// them rather than silently widening access.
type prefsModeLookup struct {
	load HostApprovalModeLoader
}

func (p prefsModeLookup) ModeForSession(context.Context, string) localsandbox.ApprovalMode {
	if p.load == nil {
		return localsandbox.ModeAuto
	}
	return localsandbox.ParseApprovalMode(p.load())
}

func provideHostSandboxManager(
	projects localsandbox.ProjectLookup,
	modes localsandbox.ModeLookup,
) service.HostSandboxManager {
	out := service.HostSandboxManager{Desktop: true}
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Warnf(context.Background(),
			"[sandbox] host backend disabled: cannot resolve home directory: %v", err)
		return out
	}
	skillsRoot := defaultHostSkillsRoot(home)
	parts := buildHostSandbox(hostSandboxDeps{
		newBackend:  localsandbox.NewBackend,
		homeDir:     home,
		appDataDir:  hostAppDataDir(home),
		sessionRoot: "",
		skillsRoot:  skillsRoot,
		projects:    projects,
		modes:       modes,
	})
	return finishHostSandbox(parts, skillsRoot)
}

// finishHostSandbox publishes the host backend whenever the OS sandbox is
// available. A skill tree that cannot be created hides local skill installs
// and leaves chat on the host manager.
func finishHostSandbox(parts *hostSandboxParts, skillsRoot string) service.HostSandboxManager {
	out := service.HostSandboxManager{Desktop: true}
	if parts == nil {
		return out
	}
	logger.Infof(context.Background(), "[sandbox] host backend enabled")
	out.Manager = parts.manager
	tree, err := skilltree.New(skillsRoot)
	if err != nil {
		logger.Warnf(context.Background(), "[sandbox] local skills unavailable: %v", err)
		return out
	}
	out.SkillTree = tree
	out.SkillInstaller = adapter.NewInstallManager(parts.service, parts.builder)
	return out
}

func defaultHostSessionRoot(homeDir string) string {
	return filepath.Join(homeDir, "Documents", "WeKnoraLite")
}

func defaultHostSkillsRoot(homeDir string) string {
	return filepath.Join(homeDir, ".weknora", "skills")
}

func hostAppDataDir(home string) string {
	if dir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, "WeKnora Lite")
	}
	return filepath.Join(home, "Library", "Application Support", "WeKnora Lite")
}
