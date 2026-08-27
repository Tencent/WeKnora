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

// skillDirEnvVar points a script at its own directory inside the sandbox
// image. Installed skills run with /workspace as WorkDir, so this is how a
// script reaches the data and helpers that were installed beside it. The
// install-time verification pass exports the same name.
const skillDirEnvVar = "WEKNORA_SKILL_DIR"

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

	// tenantSource holds the skills installed into this run's sandbox image.
	// When set it is the only source the model is told about: the host
	// skills/preloaded directory is not what execute_skill_script would find
	// inside the sandbox.
	tenantSource SkillSource

	// envResolver supplies the per-caller environment for one execution. It
	// is nil when the run has no installed skills, in which case execution
	// keeps exactly its previous behaviour.
	envResolver SkillEnvResolver

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

// WithTenantSource attaches the skills an administrator installed into the
// sandbox config this run booted from. It is part of construction - callers
// must invoke it before Initialize, i.e. before the engine can reach the
// manager - so it takes no lock.
func (m *Manager) WithTenantSource(source SkillSource) *Manager {
	m.tenantSource = source
	return m
}

// WithEnvResolver attaches the per-caller environment resolver. Like
// WithTenantSource it is part of construction and must be invoked before
// Initialize, so it takes no lock.
func (m *Manager) WithEnvResolver(resolver SkillEnvResolver) *Manager {
	m.envResolver = resolver
	return m
}

// resolveSource decides which source owns one skill name. An installed image
// is the only copy the sandbox can run: falling back to a host preloaded
// skill would advertise files that are not in the image.
func (m *Manager) resolveSource(skillName string) SkillSource {
	if m.tenantSource != nil {
		return m.tenantSource
	}
	return m.loader
}

// discoverAllSkills returns the set the model is told about. When skills are
// installed into the sandbox image, that image is the source of truth; the
// deployment's skills/preloaded directory is not what execute_skill_script
// would find inside the sandbox.
func (m *Manager) discoverAllSkills() ([]*SkillMetadata, error) {
	if m.tenantSource != nil {
		return m.tenantSource.DiscoverSkills()
	}
	return m.loader.Reload()
}

// Initialize discovers all skills and caches their metadata
// This should be called at startup
func (m *Manager) Initialize(ctx context.Context) error {
	if !m.enabled {
		return nil
	}

	metadata, err := m.discoverAllSkills()
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

	return m.resolveSource(skillName).LoadSkillInstructions(skillName)
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

	file, err := m.resolveSource(skillName).LoadSkillFile(skillName, filePath)
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

	return m.resolveSource(skillName).ListSkillFiles(skillName)
}

// SandboxSkillDir reports where a skill lives inside the sandbox image, and
// whether that path means anything to say out loud.
//
// Only an installed skill has one. A preloaded skill is uploaded from the host
// for the duration of a single call, so its base path names a directory on the
// WeKnora machine that no shell command in the sandbox can reach — telling the
// model about it would be worse than saying nothing.
func (m *Manager) SandboxSkillDir(skillName string) (string, bool) {
	if m == nil || !m.enabled || !m.isSkillAllowed(skillName) {
		return "", false
	}
	image, ok := m.resolveSource(skillName).(imageSkillSource)
	if !ok {
		return "", false
	}
	dir, err := image.GetSkillBasePath(skillName)
	if err != nil {
		return "", false
	}
	dir = strings.TrimSpace(dir)
	return dir, dir != ""
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

	source := m.resolveSource(skillName)

	// Get the skill base path
	basePath, err := source.GetSkillBasePath(skillName)
	if err != nil {
		return nil, err
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
	// SessionFileStore advertises the "sandbox provides per-session file
	// storage" capability. When present we can safely expose the input
	// staging directory and pre-materialise the output directory; when
	// absent, directories are materialised during script execution.
	fileStore := sessionFileStoreFromManager(m.sandboxMgr)
	if fileStore != nil {
		env[sessionInputEnvVar] = sandbox.SessionInputRoot
		if sessionID != "" {
			if err := fileStore.EnsureSessionDir(ctx, sessionID, outputDir); err != nil {
				logger.Warnf(ctx, "[Tool][ExecuteScript] pre-create output dir %s failed: %v", outputDir, err)
			}
		}
	}

	// Per-caller values are resolved here rather than baked into the sandbox
	// at creation: an IM thread can have several people sharing one sandbox,
	// so each turn's values must belong to that turn's speaker and must not
	// linger where the next person could read them with `env`.
	//
	// The resolver runs after the two artifact keys above are seeded, so
	// applyResolvedEnv's skip-existing rule protects exactly those two. It is
	// NOT a blanket guarantee over every WEKNORA_ key: sessionInputEnvVar is
	// only seeded when a session file store exists, and skillDirEnvVar is set
	// later inside buildSkillExecuteConfig and so relies on that unconditional
	// write instead. The write-time WEKNORA_ prefix blacklist
	// (service.validateUserEnvName) is what covers those two.
	if m.envResolver != nil {
		resolved, missing, err := m.envResolver.ResolveEnv(ctx, skillName)
		if err != nil {
			return nil, err
		}
		if len(missing) > 0 {
			return nil, &MissingSkillEnvError{SkillName: skillName, Names: missing}
		}
		applyResolvedEnv(env, resolved)
	}

	config, err := buildSkillExecuteConfig(
		source, skillName, scriptPath, basePath, args, stdin, env, sessionID,
	)
	if err != nil {
		return nil, err
	}

	// Execute in sandbox
	return m.sandboxMgr.Execute(ctx, config)
}

// buildSkillExecuteConfig turns one skill script into an execution request.
//
// The two sources diverge here and nowhere else. A skill installed into the
// image is run in place: there is no host-side copy to upload, and the
// executor pins WorkDir to the session workspace and runs it as the ordinary
// sandbox user. A preloaded skill keeps its existing behaviour exactly -
// uploaded from the host, executed with the skill directory as WorkDir.
func buildSkillExecuteConfig(
	source SkillSource,
	skillName, scriptPath, basePath string,
	args []string,
	stdin string,
	env map[string]string,
	sessionID string,
) (*sandbox.ExecuteConfig, error) {
	image, installed := source.(imageSkillSource)
	if !installed {
		// Load the script file to verify it exists and is a script
		file, err := source.LoadSkillFile(skillName, scriptPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load script: %w", err)
		}
		if !file.IsScript {
			return nil, fmt.Errorf("file is not an executable script: %s", scriptPath)
		}
		return &sandbox.ExecuteConfig{
			Script:    file.Path,
			Args:      args,
			WorkDir:   basePath,
			Stdin:     stdin,
			Env:       env,
			SessionID: sessionID,
		}, nil
	}

	// The archive is deliberately not consulted: the image is what executes,
	// and a skill whose archive failed to store is still installed and
	// runnable. That leaves the extension as the only check available here,
	// which is also the one the executor's interpreter choice depends on.
	if !IsScript(scriptPath) {
		return nil, fmt.Errorf("file is not an executable script: %s", scriptPath)
	}
	remoteScript, err := image.RemoteScriptPath(skillName, scriptPath)
	if err != nil {
		return nil, err
	}
	// The install-time verification pass exports the skill directory under
	// this name, so the environment a script is checked in is the environment
	// it is later called in.
	env[skillDirEnvVar] = basePath
	return &sandbox.ExecuteConfig{
		RemoteScriptPath: remoteScript,
		Args:             args,
		Stdin:            stdin,
		Env:              env,
		SessionID:        sessionID,
	}, nil
}

// sessionFileStoreFromManager returns the sandbox manager's effective
// session filesystem capability, or nil when the backend cannot expose one.
// Isolated in a helper so callers stay free of provider-specific branches.
func sessionFileStoreFromManager(mgr sandbox.Manager) sandbox.SessionFileStore {
	provider, ok := mgr.(sandbox.SessionCapabilityProvider)
	if !ok || provider == nil {
		return nil
	}
	return provider.SessionFileStore()
}

// GetSkillInfo returns detailed information about a skill
func (m *Manager) GetSkillInfo(ctx context.Context, skillName string) (*SkillInfo, error) {
	if !m.enabled {
		return nil, fmt.Errorf("skills are not enabled")
	}

	if !m.isSkillAllowed(skillName) {
		return nil, fmt.Errorf("skill not allowed: %s", skillName)
	}

	source := m.resolveSource(skillName)
	skill, err := source.LoadSkillInstructions(skillName)
	if err != nil {
		return nil, err
	}

	files, err := source.ListSkillFiles(skillName)
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

	metadata, err := m.discoverAllSkills()
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
