package skills

import (
	"context"
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

// fileSystemRuntime is the default SkillRuntime implementation. It
// composes a SkillRepository (for storage) and a sandbox.Manager (for
// script execution), and adds the cross-cutting concerns shared by all
// runtimes: enable flag, allow-list, metadata cache.
type fileSystemRuntime struct {
	repo     SkillRepository
	sandbox  sandbox.Manager
	enabled  bool
	hasSbx   bool // true when sandbox mode is "docker" or "local"
	allowSet map[string]struct{}

	mu       sync.RWMutex
	metaList []*SkillMetadata
	ready    bool

	// initOnce guards Initialize so concurrent first-time callers do not
	// each trigger a redundant filesystem scan. Reload bypasses it
	// deliberately — operators use Reload precisely to re-scan, and if
	// the initial Initialize failed (e.g. ctx cancelled) Reload is the
	// intended retry entry point.
	initOnce sync.Once
	initErr  error
}

// newFileSystemRuntime is the internal constructor. Callers should use
// NewRuntime / NewRuntimeFromEnv from factory.go instead.
func newFileSystemRuntime(
	repo SkillRepository,
	sbx sandbox.Manager,
	enabled, sandboxAvailable bool,
	allowed []string,
) *fileSystemRuntime {
	rt := &fileSystemRuntime{
		repo:    repo,
		sandbox: sbx,
		enabled: enabled,
		hasSbx:  sandboxAvailable,
	}
	if len(allowed) > 0 {
		rt.allowSet = make(map[string]struct{}, len(allowed))
		for _, n := range allowed {
			rt.allowSet[n] = struct{}{}
		}
	}
	return rt
}

// IsEnabled implements SkillRuntime.
func (r *fileSystemRuntime) IsEnabled() bool { return r.enabled }

// SandboxAvailable implements SkillRuntime.
func (r *fileSystemRuntime) SandboxAvailable() bool { return r.hasSbx }

// Initialize discovers skills at most once. Subsequent calls return the
// result of the first call; use Reload to force a re-scan.
func (r *fileSystemRuntime) Initialize(ctx context.Context) error {
	if !r.enabled {
		return nil
	}
	r.initOnce.Do(func() {
		r.initErr = r.refresh(ctx)
	})
	return r.initErr
}

// Reload re-scans every directory and replaces the metadata cache.
func (r *fileSystemRuntime) Reload(ctx context.Context) error {
	if !r.enabled {
		return nil
	}
	return r.refresh(ctx)
}

func (r *fileSystemRuntime) refresh(ctx context.Context) error {
	skills, err := r.repo.Discover(ctx)
	if err != nil {
		return fmt.Errorf("discover skills: %w", err)
	}
	metas := make([]*SkillMetadata, 0, len(skills))
	for _, s := range skills {
		if !r.allowed(s.Name) {
			continue
		}
		metas = append(metas, s.ToMetadata())
	}
	r.mu.Lock()
	r.metaList = metas
	r.ready = true
	r.mu.Unlock()
	return nil
}

// allowed returns true when skillName passes the optional allow-list.
// Empty allow-list = allow everything (preserves legacy behaviour).
func (r *fileSystemRuntime) allowed(skillName string) bool {
	if r.allowSet == nil {
		return true
	}
	_, ok := r.allowSet[skillName]
	return ok
}

// ListMetadata implements SkillRuntime. Returns a defensive copy.
func (r *fileSystemRuntime) ListMetadata(ctx context.Context) []*SkillMetadata {
	if !r.enabled {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*SkillMetadata, len(r.metaList))
	copy(out, r.metaList)
	return out
}

// Load implements SkillRuntime.
func (r *fileSystemRuntime) Load(ctx context.Context, name string) (*Skill, error) {
	if err := r.guard(name); err != nil {
		return nil, err
	}
	return r.repo.GetByName(ctx, name)
}

// GetInfo implements SkillRuntime.
func (r *fileSystemRuntime) GetInfo(ctx context.Context, name string) (*SkillInfo, error) {
	if err := r.guard(name); err != nil {
		return nil, err
	}
	skill, err := r.repo.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	files, err := r.repo.ListFiles(ctx, name)
	if err != nil {
		files = []string{} // non-fatal
	}
	return &SkillInfo{
		Name:         skill.Name,
		Description:  skill.Description,
		BasePath:     skill.BasePath,
		Instructions: skill.Instructions,
		Files:        files,
	}, nil
}

// ReadFile implements SkillRuntime.
func (r *fileSystemRuntime) ReadFile(ctx context.Context, name, relPath string) (string, error) {
	if err := r.guard(name); err != nil {
		return "", err
	}
	f, err := r.repo.ReadFile(ctx, name, relPath)
	if err != nil {
		return "", err
	}
	return f.Content, nil
}

// ListFiles implements SkillRuntime.
func (r *fileSystemRuntime) ListFiles(ctx context.Context, name string) ([]string, error) {
	if err := r.guard(name); err != nil {
		return nil, err
	}
	return r.repo.ListFiles(ctx, name)
}

// ExecuteScript implements SkillRuntime.
func (r *fileSystemRuntime) ExecuteScript(ctx context.Context, req ExecuteRequest) (*sandbox.ExecuteResult, error) {
	if err := r.guard(req.SkillName); err != nil {
		return nil, err
	}
	if r.sandbox == nil {
		return nil, fmt.Errorf("sandbox is not configured")
	}

	basePath, err := r.repo.BasePath(ctx, req.SkillName)
	if err != nil {
		return nil, err
	}
	file, err := r.repo.ReadFile(ctx, req.SkillName, req.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("load script: %w", err)
	}
	if !file.IsScript {
		return nil, fmt.Errorf("file is not an executable script: %s", req.ScriptPath)
	}

	cfg := &sandbox.ExecuteConfig{
		Script:  file.Path,
		Args:    req.Args,
		WorkDir: basePath,
		Stdin:   req.Stdin,
	}
	return r.sandbox.Execute(ctx, cfg)
}

// Cleanup implements SkillRuntime.
func (r *fileSystemRuntime) Cleanup(ctx context.Context) error {
	if r.sandbox == nil {
		return nil
	}
	return r.sandbox.Cleanup(ctx)
}

// guard centralises the enabled + allow-list checks performed by every
// non-discovery method. Returning a single error keeps call-sites short.
func (r *fileSystemRuntime) guard(skillName string) error {
	if !r.enabled {
		return fmt.Errorf("skills are not enabled")
	}
	if !r.allowed(skillName) {
		return fmt.Errorf("skill not allowed: %s", skillName)
	}
	return nil
}
