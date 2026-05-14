// Package projectlink reads and writes the per-project link file
// .weknora/project.yaml that anchors a working directory to a context+KB.
//
// Discovery walks up from the start directory until either (a) a
// .weknora/project.yaml is found, (b) the filesystem root is reached, or
// (c) the walk exceeds a 64-level depth limit (cycle protection for
// pathological symlink setups). Pattern matches `cargo`, `npm`, and `git`
// - find-the-project's-root walks; mount-boundary crossing is allowed
// (npm/cargo behave the same - a project may straddle a bind-mount).
package projectlink

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/xdg"
	"gopkg.in/yaml.v3"
)

// Project is the on-disk schema of .weknora/project.yaml.
type Project struct {
	Context   string    `yaml:"context,omitempty"`
	KBID      string    `yaml:"kb_id"`
	CreatedAt time.Time `yaml:"created_at"`
}

// FileName is the project link file basename relative to .weknora/.
const FileName = "project.yaml"

// DirName is the project-link directory name.
const DirName = ".weknora"

const maxWalkDepth = 64

// Discover walks up from startDir looking for <DirName>/<FileName>.
// Returns (path, true, nil) when found, (root, false, nil) when none is
// found within depth limit, or ("", false, err) on filesystem errors.
func Discover(startDir string) (string, bool, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", false, fmt.Errorf("resolve start dir: %w", err)
	}
	for range maxWalkDepth {
		candidate := filepath.Join(dir, DirName, FileName)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, true, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			// Permission errors etc. - keep walking up; missing read access
			// to a parent dir doesn't necessarily mean the project starts
			// here. Cargo/npm have the same behavior.
			// Fall through to parent walk.
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Hit filesystem root
			return "", false, nil
		}
		dir = parent
	}
	return "", false, nil
}

// Load reads and parses the project link file at path.
func Load(path string) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read project link: %w", err)
	}
	var p Project
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("project link corrupt: %w", err)
	}
	return &p, nil
}

// Save writes the project link to path atomically (0600 perms).
// Creates parent .weknora/ directory if missing.
func Save(path string, p *Project) error {
	return xdg.WriteAtomicYAML(path, p)
}

// Remove deletes the project link at path. A missing file is reported as
// success so callers can stay idempotent under concurrent-removal races
// - the post-condition (no file at path) holds in either case.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove project link: %w", err)
	}
	return nil
}
