package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SkillRepository abstracts the storage layer behind a SkillRuntime.
//
// Today only fsRepository is implemented (filesystem-backed), but having
// the interface lets us add DB / object-storage / marketplace backends
// later without touching SkillRuntime consumers.
type SkillRepository interface {
	// Discover scans the underlying storage and returns every skill
	// found, with metadata only (no instructions body required).
	Discover(ctx context.Context) ([]*Skill, error)

	// GetByName returns the full skill (with Instructions populated).
	GetByName(ctx context.Context, name string) (*Skill, error)

	// ReadFile returns a single file from a skill's directory.
	ReadFile(ctx context.Context, name, relPath string) (*SkillFile, error)

	// ListFiles returns all files (relative paths) under a skill.
	ListFiles(ctx context.Context, name string) ([]string, error)

	// BasePath returns the absolute base path of a skill, useful when
	// the runtime needs to wire the sandbox's working directory.
	BasePath(ctx context.Context, name string) (string, error)
}

// fsRepository is the filesystem-backed SkillRepository implementation.
//
// It mirrors the behaviour of the original Loader but with explicit
// concurrency safety and a lazy cache populated by Discover/GetByName.
type fsRepository struct {
	dirs []string

	mu    sync.RWMutex
	cache map[string]*Skill
}

// NewFSRepository creates a filesystem-backed repository that searches
// the supplied directories (in order) for `<dir>/<skill-name>/SKILL.md`.
func NewFSRepository(dirs []string) SkillRepository {
	return &fsRepository{
		dirs:  dirs,
		cache: make(map[string]*Skill),
	}
}

// Discover walks every configured directory looking for skill folders
// (subdirectories that contain SKILL.md). Errors on individual
// directories are swallowed (matching the previous Loader behaviour) so
// that a missing optional path does not abort discovery.
func (r *fsRepository) Discover(ctx context.Context) ([]*Skill, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var all []*Skill
	for _, dir := range r.dirs {
		found, err := r.discoverIn(dir)
		if err != nil {
			// Non-fatal: continue with other directories.
			continue
		}
		for _, s := range found {
			r.cache[s.Name] = s
			all = append(all, s)
		}
	}
	return all, nil
}

// discoverIn scans a single directory for SKILL.md files.
func (r *fsRepository) discoverIn(dir string) ([]*Skill, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	var out []*Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		basePath := filepath.Join(dir, entry.Name())
		filePath := filepath.Join(basePath, SkillFileName)

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			continue
		}

		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		skill, err := ParseSkillFile(string(content))
		if err != nil {
			continue
		}
		skill.BasePath = basePath
		skill.FilePath = filePath
		out = append(out, skill)
	}
	return out, nil
}

// GetByName returns the requested skill, falling back to a directory
// scan if the cache does not contain it (e.g. when GetByName is the
// first call after construction).
func (r *fsRepository) GetByName(ctx context.Context, name string) (*Skill, error) {
	r.mu.RLock()
	if s, ok := r.cache[name]; ok && s.Loaded {
		r.mu.RUnlock()
		return s, nil
	}
	r.mu.RUnlock()

	for _, dir := range r.dirs {
		skill, err := r.loadFromDir(dir, name)
		if err == nil {
			r.mu.Lock()
			r.cache[name] = skill
			r.mu.Unlock()
			return skill, nil
		}
	}
	return nil, fmt.Errorf("skill not found: %s", name)
}

// loadFromDir attempts to load `<dir>/<name>/SKILL.md`, then falls back
// to a manual scan of subdirectories whose internal `name:` matches.
func (r *fsRepository) loadFromDir(dir, name string) (*Skill, error) {
	directPath := filepath.Join(dir, name)
	directFile := filepath.Join(directPath, SkillFileName)
	if _, err := os.Stat(directFile); err == nil {
		return r.parseFile(directPath, directFile)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		basePath := filepath.Join(dir, entry.Name())
		filePath := filepath.Join(basePath, SkillFileName)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			continue
		}
		skill, err := r.parseFile(basePath, filePath)
		if err == nil && skill.Name == name {
			return skill, nil
		}
	}
	return nil, fmt.Errorf("skill not found in %s: %s", dir, name)
}

func (r *fsRepository) parseFile(basePath, filePath string) (*Skill, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read skill: %w", err)
	}
	skill, err := ParseSkillFile(string(content))
	if err != nil {
		return nil, err
	}
	skill.BasePath = basePath
	skill.FilePath = filePath
	return skill, nil
}

// ReadFile loads a single file inside a skill, with strict path-traversal
// protection (an attacker-controlled relPath cannot escape BasePath).
func (r *fsRepository) ReadFile(ctx context.Context, name, relPath string) (*SkillFile, error) {
	skill, err := r.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}

	cleanPath := filepath.Clean(relPath)
	if strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
		return nil, fmt.Errorf("invalid file path: %s", relPath)
	}

	full := filepath.Join(skill.BasePath, cleanPath)
	absSkill, err := filepath.Abs(skill.BasePath)
	if err != nil {
		return nil, err
	}
	absFile, err := filepath.Abs(full)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(absFile, absSkill) {
		return nil, fmt.Errorf("file path outside skill directory: %s", relPath)
	}

	content, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return &SkillFile{
		Name:     relPath,
		Path:     absFile,
		Content:  string(content),
		IsScript: IsScript(relPath),
	}, nil
}

// ListFiles walks the skill directory and returns every file with its
// path relative to the skill base.
func (r *fsRepository) ListFiles(ctx context.Context, name string) ([]string, error) {
	skill, err := r.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	var files []string
	walkErr := filepath.Walk(skill.BasePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(skill.BasePath, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("list skill files: %w", walkErr)
	}
	return files, nil
}

// BasePath returns the absolute base path of a skill.
func (r *fsRepository) BasePath(ctx context.Context, name string) (string, error) {
	skill, err := r.GetByName(ctx, name)
	if err != nil {
		return "", err
	}
	return filepath.Abs(skill.BasePath)
}
