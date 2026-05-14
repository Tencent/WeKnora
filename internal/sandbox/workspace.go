package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// workspace directory names
const (
	// DirSkills storage skills
	DirSkills = "skills"
	// DirWork storage writable intermediate files
	DirWork = "work"
	// DirRuns storage run directories
	DirRuns = "runs"
	// DirOut storage collected output artifacts
	DirOut = "out"
)

// injected environment variable names
const (
	EnvWorkspaceDir = "WORKSPACE_DIR"
	EnvSkillsDir    = "SKILLS_DIR"
	EnvWorkDir      = "WORK_DIR"
	EnvRunDir       = "RUN_DIR"
	EnvSkillName    = "SKILL_NAME"
)

// Workspace is a isolated workspace for a skill
type Workspace struct {
	// ID is the unique identifier of the workspace
	ID string
	// Path is the absolute path of the workspace on the host
	Path string
	// SkillName is the skill name associated with the workspace
	SkillName string
	// CreatedAt is the workspace creation time
	CreatedAt time.Time
}

// EnsureLayout creates the standard subdirectory structure for the workspace
// Returns the absolute paths of the subdirectories
func EnsureLayout(root string) (map[string]string, error) {
	paths := map[string]string{
		DirSkills: filepath.Join(root, DirSkills),
		DirWork:   filepath.Join(root, DirWork),
		DirRuns:   filepath.Join(root, DirRuns),
		DirOut:    filepath.Join(root, DirOut),
	}
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create workspace dir %s: %w", p, err)
		}
	}
	return paths, nil
}

// CreateWorkspace creates a new workspace for a skill
// workRoot is the root directory for the workspace, empty uses system temp directory
func CreateWorkspace(workRoot string, skillName string) (*Workspace, error) {
	var base string
	if workRoot != "" {
		base = workRoot
	} else {
		base = os.TempDir()
	}

	// ensure base dir exists
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create workspace base: %w", err)
	}

	// generate unique workspace path
	safe := sanitizeForPath(skillName)
	suf := time.Now().UnixNano()
	wsPath := filepath.Join(base, fmt.Sprintf("ws_%s_%d", safe, suf))

	if err := os.MkdirAll(wsPath, 0o777); err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	_ = os.Chmod(wsPath, 0o777)

	// ensure workspace layout
	if _, err := EnsureLayout(wsPath); err != nil {
		os.RemoveAll(wsPath)
		return nil, err
	}

	return &Workspace{
		ID:        fmt.Sprintf("%s_%d", safe, suf),
		Path:      wsPath,
		SkillName: skillName,
		CreatedAt: time.Now(),
	}, nil
}

// CleanupWorkspace cleans up a workspace
func CleanupWorkspace(ws *Workspace) error {
	if ws == nil || ws.Path == "" {
		return nil
	}
	return os.RemoveAll(ws.Path)
}

// StageSkill stages a skill to a workspace
// returns the relative path of the skill within the workspace
func StageSkill(ws *Workspace, skillSourceDir string, skillName string) (string, error) {
	if ws == nil {
		return "", fmt.Errorf("workspace is nil")
	}

	destDir := filepath.Join(ws.Path, DirSkills, skillName)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create skill staging dir: %w", err)
	}

	if err := copyDir(skillSourceDir, destDir); err != nil {
		return "", fmt.Errorf("failed to stage skill: %w", err)
	}

	return filepath.Join(DirSkills, skillName), nil
}

// BuildWorkspaceEnv builds the environment for a workspace
// These environment variables will be injected into the script execution environment
func BuildWorkspaceEnv(ws *Workspace, skillName string) map[string]string {
	if ws == nil {
		return nil
	}

	runDir := filepath.Join(ws.Path, DirRuns,
		"run_"+time.Now().Format("20060102T150405.000"))
	_ = os.MkdirAll(runDir, 0o755)

	env := map[string]string{
		EnvWorkspaceDir: ws.Path,
		EnvSkillsDir:    filepath.Join(ws.Path, DirSkills),
		EnvWorkDir:      filepath.Join(ws.Path, DirWork),
		OutputDirEnvVar: filepath.Join(ws.Path, DirOut),
		EnvRunDir:       runDir,
	}
	if skillName != "" {
		env[EnvSkillName] = skillName
	}
	return env
}

// BuildDockerWorkspaceEnv builds the environment for a workspace in docker
func BuildDockerWorkspaceEnv(containerWorkDir string, skillName string) map[string]string {
	env := map[string]string{
		EnvWorkspaceDir: containerWorkDir,
		EnvSkillsDir:    containerWorkDir + "/" + DirSkills,
		EnvWorkDir:      containerWorkDir + "/" + DirWork,
		OutputDirEnvVar: containerWorkDir + "/" + DirOut,
		EnvRunDir:       containerWorkDir + "/" + DirRuns,
	}
	if skillName != "" {
		env[EnvSkillName] = skillName
	}
	return env
}

// GetWorkspaceOutputDir returns the output dir of a workspace
func GetWorkspaceOutputDir(ws *Workspace) string {
	if ws == nil {
		return ""
	}
	return filepath.Join(ws.Path, DirOut)
}

// GetWorkspaceSkillDir returns the skill dir of a workspace
func GetWorkspaceSkillDir(ws *Workspace, skillName string) string {
	if ws == nil {
		return ""
	}
	return filepath.Join(ws.Path, DirSkills, skillName)
}

// DirDigest calculates the digest of a directory
func DirDigest(root string) (string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		k := strings.ReplaceAll(rel, string(os.PathSeparator), "/")
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return "", err
		}
		_, _ = h.Write(b)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyDir copy dir
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// sanitizeForPath sanitize string for path
func sanitizeForPath(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, s)
}
