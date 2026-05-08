package skills

import (
	"context"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

// SkillRuntime is the unified abstraction for all Skill capabilities.
// It is consumed by AgentEngine, HTTP handlers, workflows and any future
// module that needs to discover, load or execute skills.
//
// Implementations MUST be safe for concurrent use.
type SkillRuntime interface {
	// Lifecycle ----------------------------------------------------------

	// Initialize discovers skills and prepares internal caches.
	// Safe to call multiple times; subsequent calls are no-ops once ready.
	Initialize(ctx context.Context) error

	// Reload re-discovers skills, refreshing all caches.
	Reload(ctx context.Context) error

	// Cleanup releases underlying resources (sandbox, etc.).
	Cleanup(ctx context.Context) error

	// State --------------------------------------------------------------

	// IsEnabled reports whether the runtime is active.
	// Disabled runtimes still satisfy the interface but reject operations.
	IsEnabled() bool

	// SandboxAvailable reports whether script execution is possible
	// (i.e. WEKNORA_SANDBOX_MODE is "docker" or "local").
	SandboxAvailable() bool

	// Discovery (Level 1) ------------------------------------------------

	// ListMetadata returns the lightweight metadata for all discovered
	// skills. The returned slice is a defensive copy.
	ListMetadata(ctx context.Context) []*SkillMetadata

	// Instructions (Level 2) ---------------------------------------------

	// Load returns the full skill (frontmatter + instructions).
	Load(ctx context.Context, name string) (*Skill, error)

	// GetInfo returns a Skill plus the list of associated files.
	GetInfo(ctx context.Context, name string) (*SkillInfo, error)

	// Resources (Level 3) ------------------------------------------------

	// ReadFile returns the textual content of a file inside a skill.
	// `relPath` is resolved relative to the skill's base directory and
	// must not escape it (path-traversal is rejected).
	ReadFile(ctx context.Context, name, relPath string) (string, error)

	// ListFiles returns all files (relative paths) in a skill directory.
	ListFiles(ctx context.Context, name string) ([]string, error)

	// Execution ----------------------------------------------------------

	// ExecuteScript runs a script from a skill in the configured sandbox.
	ExecuteScript(ctx context.Context, req ExecuteRequest) (*sandbox.ExecuteResult, error)
}

// ExecuteRequest is the structured input for script execution.
// Centralising it (rather than passing many positional args) makes it
// trivial to extend later (timeout overrides, env vars, etc.) without
// breaking the interface.
type ExecuteRequest struct {
	SkillName  string
	ScriptPath string
	Args       []string
	Stdin      string
}

// SkillInfo provides detailed information about a skill, including the
// list of files in its directory. It is the unit returned by GetInfo.
type SkillInfo struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	BasePath     string   `json:"base_path"`
	Instructions string   `json:"instructions"`
	Files        []string `json:"files"`
}
