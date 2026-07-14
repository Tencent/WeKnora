package skills

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// artifactOutputEnvVar is the name of the environment variable that WeKnora
// injects into every skill script execution. The value points to the
// convention-driven directory where the script should drop artifacts the user
// will be able to download after the turn completes.
//
// The name is stable across releases; skills reference it via os.getenv(...)
// so they never hard-code the path.
const artifactOutputEnvVar = "WEKNORA_SKILL_OUTPUT_DIR"

// sessionInputEnvVar points skill scripts at user-uploaded files restored into
// the current session's Cube. Inputs are separate from generated artifacts.
const sessionInputEnvVar = "WEKNORA_SESSION_INPUT_DIR"

// artifactHistoryEnvVar is the name of the environment variable that points
// to the root artifact output directory (/workspace/output). Skill scripts
// can use this to self-discover artifacts from prior runs when they need to
// chain without LLM mediation.
const artifactHistoryEnvVar = "WEKNORA_SKILL_HISTORY_ROOT"

// defaultArtifactOutputDir is used when neither the environment variable
// (WEKNORA_SKILL_OUTPUT_DIR) nor the ExecuteConfig.Env has an override.
// /workspace/output sits inside the base sandbox image's writable tree and
// is guaranteed to survive across Execute calls for the same session (Cube
// SessionBoundManager keeps the MicroVM alive between calls).
const defaultArtifactOutputDir = "/workspace/output"

// ArtifactOutputDir returns the absolute path (inside the sandbox) where
// skill scripts should write artifacts for this turn. It is exported so
// callers such as ArtifactCollector can list the same directory when
// draining artifacts after Execute returns.
//
// Resolution order (first non-empty wins):
//  1. WEKNORA_SKILL_OUTPUT_DIR from the host environment (ops override).
//  2. defaultArtifactOutputDir.
//
// Callers are expected to treat the returned string as read-only: the path
// is normalised (no trailing slash) so it can be joined safely.
func ArtifactOutputDir() string {
	if v := strings.TrimSpace(os.Getenv(artifactOutputEnvVar)); v != "" {
		return path.Clean(v)
	}
	return defaultArtifactOutputDir
}

// SkillOutputDir returns the artifact output directory for skill executions.
// All skills write to the same root directory (/workspace/output/) to enable
// collaboration and file sharing between different skill executions.
func (m *Manager) SkillOutputDir(sessionID, skillName string) string {
	return ArtifactOutputDir()
}

// Manager manages skills lifecycle including discovery, loading, and script execution
// It coordinates between the Loader (filesystem operations) and Sandbox (script execution)
type Manager struct {
	loader     *Loader
	sandboxMgr sandbox.Manager

	// Configuration
	skillDirs     []string
	allowedSkills []string // Empty means all skills are allowed
	enabled       bool

	// Cache
	metadataCache []*SkillMetadata
	mu            sync.RWMutex
}

// ManagerConfig holds configuration for the skill manager
type ManagerConfig struct {
	SkillDirs     []string // Directories to search for skills
	AllowedSkills []string // Skill names whitelist (empty = allow all)
	Enabled       bool     // Whether skills are enabled
}

// NewManager creates a new skill manager with the given configuration
func NewManager(config *ManagerConfig, sandboxMgr sandbox.Manager) *Manager {
	if config == nil {
		config = &ManagerConfig{
			Enabled: false,
		}
	}

	return &Manager{
		loader:        NewLoader(config.SkillDirs),
		sandboxMgr:    sandboxMgr,
		skillDirs:     config.SkillDirs,
		allowedSkills: config.AllowedSkills,
		enabled:       config.Enabled,
	}
}

// IsEnabled returns whether skills are enabled
func (m *Manager) IsEnabled() bool {
	return m.enabled
}

// Initialize discovers all skills and caches their metadata
// This should be called at startup
func (m *Manager) Initialize(ctx context.Context) error {
	if !m.enabled {
		return nil
	}

	metadata, err := m.loader.DiscoverSkills()
	if err != nil {
		return fmt.Errorf("failed to discover skills: %w", err)
	}

	// Filter by allowed skills if specified
	if len(m.allowedSkills) > 0 {
		metadata = m.filterAllowedSkills(metadata)
	}

	m.mu.Lock()
	m.metadataCache = metadata
	m.mu.Unlock()

	return nil
}

// filterAllowedSkills filters metadata to only include allowed skills
func (m *Manager) filterAllowedSkills(metadata []*SkillMetadata) []*SkillMetadata {
	if len(m.allowedSkills) == 0 {
		return metadata
	}

	allowedSet := make(map[string]bool)
	for _, name := range m.allowedSkills {
		allowedSet[name] = true
	}

	var filtered []*SkillMetadata
	for _, meta := range metadata {
		if allowedSet[meta.Name] {
			filtered = append(filtered, meta)
		}
	}
	return filtered
}

// GetAllMetadata returns metadata for all discovered skills
// This is used for system prompt injection (Level 1)
func (m *Manager) GetAllMetadata() []*SkillMetadata {
	if !m.enabled {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make([]*SkillMetadata, len(m.metadataCache))
	copy(result, m.metadataCache)
	return result
}

// LoadSkill loads the full instructions of a skill (Level 2)
func (m *Manager) LoadSkill(ctx context.Context, skillName string) (*Skill, error) {
	if !m.enabled {
		return nil, fmt.Errorf("skills are not enabled")
	}

	// Check if skill is allowed
	if !m.isSkillAllowed(skillName) {
		return nil, fmt.Errorf("skill not allowed: %s", skillName)
	}

	return m.loader.LoadSkillInstructions(skillName)
}

// isSkillAllowed checks if a skill is in the allowed list
func (m *Manager) isSkillAllowed(skillName string) bool {
	if len(m.allowedSkills) == 0 {
		return true
	}
	for _, name := range m.allowedSkills {
		if name == skillName {
			return true
		}
	}
	return false
}

// ReadSkillFile reads an additional file from a skill directory (Level 3)
func (m *Manager) ReadSkillFile(ctx context.Context, skillName, filePath string) (string, error) {
	if !m.enabled {
		return "", fmt.Errorf("skills are not enabled")
	}

	if !m.isSkillAllowed(skillName) {
		return "", fmt.Errorf("skill not allowed: %s", skillName)
	}

	file, err := m.loader.LoadSkillFile(skillName, filePath)
	if err != nil {
		return "", err
	}

	return file.Content, nil
}

// ListSkillFiles lists all files in a skill directory
func (m *Manager) ListSkillFiles(ctx context.Context, skillName string) ([]string, error) {
	if !m.enabled {
		return nil, fmt.Errorf("skills are not enabled")
	}

	if !m.isSkillAllowed(skillName) {
		return nil, fmt.Errorf("skill not allowed: %s", skillName)
	}

	return m.loader.ListSkillFiles(skillName)
}

// ExecuteScript executes a script from a skill in the sandbox
func (m *Manager) ExecuteScript(ctx context.Context, skillName, scriptPath string, args []string, stdin string) (*sandbox.ExecuteResult, error) {
	if !m.enabled {
		return nil, fmt.Errorf("skills are not enabled")
	}

	if !m.isSkillAllowed(skillName) {
		return nil, fmt.Errorf("skill not allowed: %s", skillName)
	}

	// Verify sandbox manager is available
	if m.sandboxMgr == nil {
		return nil, fmt.Errorf("sandbox is not configured")
	}

	// Get the skill base path
	basePath, err := m.loader.GetSkillBasePath(skillName)
	if err != nil {
		return nil, err
	}

	// Load the script file to verify it exists and is a script
	file, err := m.loader.LoadSkillFile(skillName, scriptPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load script: %w", err)
	}

	if !file.IsScript {
		return nil, fmt.Errorf("file is not an executable script: %s", scriptPath)
	}

	// Prepare execution config
	logger.Info(ctx, "[Tool][ExecuteScript]:Prepare execution config")
	sessionID, _ := types.SessionIDFromContext(ctx)

	// Compute the artifact output directory. All skills share the same root
	// directory (/workspace/output/) to enable collaboration and file sharing
	// between different skill executions in the same session.
	// Skill scripts read the directory via WEKNORA_SKILL_OUTPUT_DIR; the
	// root (for cross-run discovery) is available via WEKNORA_SKILL_HISTORY_ROOT.
	outputDir := m.SkillOutputDir(sessionID, skillName)
	env := map[string]string{
		artifactOutputEnvVar:  outputDir,
		artifactHistoryEnvVar: ArtifactOutputDir(),
	}
	if m.sandboxMgr.GetType() == sandbox.SandboxTypeCube {
		env[sessionInputEnvVar] = sandbox.SessionInputRoot
	}

	// Best-effort pre-create the output directory inside the sandbox. When
	// the underlying manager exposes EnsureSessionDir (Cube backend today),
	// we call it so the script can write to $WEKNORA_SKILL_OUTPUT_DIR
	// unconditionally without a mkdir -p prelude. Errors are logged but
	// never fatal: script execution proceeds regardless, and the script is
	// free to create the directory itself.
	if ensurer, ok := m.sandboxMgr.(interface {
		EnsureSessionDir(context.Context, string, string) error
	}); ok && sessionID != "" {
		if err := ensurer.EnsureSessionDir(ctx, sessionID, outputDir); err != nil {
			logger.Warnf(ctx, "[Tool][ExecuteScript] pre-create output dir %s failed: %v", outputDir, err)
		}
	}

	config := &sandbox.ExecuteConfig{
		Script:    file.Path,
		Args:      args,
		WorkDir:   basePath,
		Stdin:     stdin,
		Env:       env,
		SessionID: sessionID,
	}

	// Execute in sandbox
	return m.sandboxMgr.Execute(ctx, config)
}

// GetSkillInfo returns detailed information about a skill
func (m *Manager) GetSkillInfo(ctx context.Context, skillName string) (*SkillInfo, error) {
	if !m.enabled {
		return nil, fmt.Errorf("skills are not enabled")
	}

	if !m.isSkillAllowed(skillName) {
		return nil, fmt.Errorf("skill not allowed: %s", skillName)
	}

	skill, err := m.loader.LoadSkillInstructions(skillName)
	if err != nil {
		return nil, err
	}

	files, err := m.loader.ListSkillFiles(skillName)
	if err != nil {
		files = []string{} // Non-fatal error
	}

	return &SkillInfo{
		Name:         skill.Name,
		Description:  skill.Description,
		BasePath:     skill.BasePath,
		Instructions: skill.Instructions,
		Files:        files,
	}, nil
}

// SkillInfo provides detailed information about a skill
type SkillInfo struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	BasePath     string   `json:"base_path"`
	Instructions string   `json:"instructions"`
	Files        []string `json:"files"`
}

// Reload refreshes the skill cache by rediscovering all skills
func (m *Manager) Reload(ctx context.Context) error {
	if !m.enabled {
		return nil
	}

	metadata, err := m.loader.Reload()
	if err != nil {
		return err
	}

	if len(m.allowedSkills) > 0 {
		metadata = m.filterAllowedSkills(metadata)
	}

	m.mu.Lock()
	m.metadataCache = metadata
	m.mu.Unlock()

	return nil
}

// Cleanup releases resources
func (m *Manager) Cleanup(ctx context.Context) error {
	if m.sandboxMgr != nil {
		return m.sandboxMgr.Cleanup(ctx)
	}
	return nil
}
