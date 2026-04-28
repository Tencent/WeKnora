package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

// DefaultPreloadedSkillsDir is the conventional location for shipped
// skills relative to the executable / working directory.
const DefaultPreloadedSkillsDir = "skills/preloaded"

// Options configures the construction of a SkillRuntime.
//
// Callers that want full control populate Options directly via NewRuntime.
// The DI container uses NewRuntimeFromEnv which fills Options from the
// existing WEKNORA_SKILLS_DIR / WEKNORA_SANDBOX_* environment variables,
// preserving the legacy configuration surface.
type Options struct {
	// SkillDirs are searched in order. Empty falls back to env-resolved
	// preloaded directory.
	SkillDirs []string

	// AllowedSkills, when non-empty, restricts which skills may be
	// loaded or executed. Empty = allow everything (legacy behaviour).
	AllowedSkills []string

	// Enabled is the master switch. A disabled runtime is still
	// constructed (so callers can hold the dependency) but every
	// operation short-circuits.
	Enabled bool

	// SandboxMode is one of "docker", "local", "disabled" (default).
	SandboxMode string

	// SandboxImage overrides the Docker image when SandboxMode=="docker".
	SandboxImage string

	// SandboxTimeout is the per-script timeout in seconds (0 = default).
	SandboxTimeout int
}

// NewRuntime builds the default fileSystemRuntime from explicit options.
// It is the preferred constructor for tests and embedding scenarios.
func NewRuntime(opts Options) (SkillRuntime, error) {
	dirs := opts.SkillDirs
	if len(dirs) == 0 {
		dirs = []string{resolvePreloadedDir()}
	}

	sbx, sandboxAvail := buildSandbox(opts.SandboxMode, opts.SandboxImage)
	repo := NewFSRepository(dirs)
	rt := newFileSystemRuntime(repo, sbx, opts.Enabled, sandboxAvail, opts.AllowedSkills)
	return rt, nil
}

// NewRuntimeFromEnv is the constructor used by the DI container.
// It reads the existing environment variables so that operators do not
// have to change their deployment configuration:
//
//   WEKNORA_SKILLS_DIR     – override the preloaded skill directory
//   WEKNORA_SANDBOX_MODE   – "docker" | "local" | "disabled" (default)
//   WEKNORA_SANDBOX_DOCKER_IMAGE
//   WEKNORA_SANDBOX_TIMEOUT (seconds)
//
// The runtime is always returned in the "enabled" state; whether
// individual agents take advantage of it is still controlled by
// AgentConfig.SkillsEnabled at request time.
func NewRuntimeFromEnv() (SkillRuntime, error) {
	mode := os.Getenv("WEKNORA_SANDBOX_MODE")
	if mode == "" {
		mode = "disabled"
	}
	image := os.Getenv("WEKNORA_SANDBOX_DOCKER_IMAGE")
	if image == "" {
		image = sandbox.DefaultDockerImage
	}
	timeout := 60
	if v := os.Getenv("WEKNORA_SANDBOX_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeout = n
		}
	}

	opts := Options{
		SkillDirs:      []string{resolvePreloadedDir()},
		Enabled:        true,
		SandboxMode:    mode,
		SandboxImage:   image,
		SandboxTimeout: timeout,
	}
	return NewRuntime(opts)
}

// resolvePreloadedDir mirrors the previous getPreloadedSkillsDir logic
// from application/service/skill_service.go: env > exec dir > cwd >
// fallback. Kept as a free function so it can be tested independently.
func resolvePreloadedDir() string {
	if dir := os.Getenv("WEKNORA_SKILLS_DIR"); dir != "" {
		return dir
	}
	if execPath, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(execPath), DefaultPreloadedSkillsDir)
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		dir := filepath.Join(cwd, DefaultPreloadedSkillsDir)
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}
	return DefaultPreloadedSkillsDir
}

// buildSandbox creates a sandbox.Manager and reports whether script
// execution is actually available. A "disabled" or unrecoverable
// configuration falls back to NewDisabledManager, mirroring the
// behaviour previously living in agent_service.initializeSkillsManager.
func buildSandbox(mode, image string) (sandbox.Manager, bool) {
	switch mode {
	case "docker":
		mgr, err := sandbox.NewManagerFromType("docker", true, image)
		if err != nil {
			logger.Warnf(context.Background(), "skill runtime: docker sandbox init failed, falling back to disabled: %v", err)
			return sandbox.NewDisabledManager(), false
		}
		return mgr, true
	case "local":
		mgr, err := sandbox.NewManagerFromType("local", false, "")
		if err != nil {
			logger.Warnf(context.Background(), "skill runtime: local sandbox init failed: %v", err)
			return sandbox.NewDisabledManager(), false
		}
		return mgr, true
	default:
		return sandbox.NewDisabledManager(), false
	}
}

// FilterMetadata returns a copy of `all` containing only the skills
// whose names appear in `allowed`. An empty `allowed` slice means
// "no filter" and the input is returned unchanged.
//
// This helper exists because AgentConfig.AllowedSkills is enforced at
// request time (since the runtime is a global singleton) rather than at
// construction time. Engine / handlers use it before injecting metadata
// into the prompt.
func FilterMetadata(all []*SkillMetadata, allowed []string) []*SkillMetadata {
	if len(allowed) == 0 {
		return all
	}
	set := make(map[string]struct{}, len(allowed))
	for _, n := range allowed {
		set[n] = struct{}{}
	}
	out := make([]*SkillMetadata, 0, len(all))
	for _, m := range all {
		if _, ok := set[m.Name]; ok {
			out = append(out, m)
		}
	}
	return out
}

// MustBeReady is a small convenience for places (mainly tests) that
// want a one-shot construction + initialisation.
func MustBeReady(opts Options) SkillRuntime {
	rt, err := NewRuntime(opts)
	if err != nil {
		panic(fmt.Errorf("skills.MustBeReady: %w", err))
	}
	if err := rt.Initialize(context.Background()); err != nil {
		panic(fmt.Errorf("skills.MustBeReady: initialize: %w", err))
	}
	return rt
}
