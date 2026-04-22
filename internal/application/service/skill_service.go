package service

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// DefaultPreloadedSkillsDir default preloaded skills directory
const DefaultPreloadedSkillsDir = "skills/preloaded"

// DefaultInstalledSkillsDir default installed skills directory
const DefaultInstalledSkillsDir = "skills/installed"

// skillService implements SkillService
type skillService struct {
	repo            *skills.UserInstallableRepository
	artifactService skills.ArtifactService
	loader          *skills.Loader
	preloadedDir    string
	installedDir    string
	mu              sync.RWMutex
	initialized     bool
}

// NewSkillService creates a new skill service
func NewSkillService() interfaces.SkillService {
	preloadedDir := getPreloadedSkillsDir()
	installedDir := getInstalledSkillsDir()

	return &skillService{
		preloadedDir:    preloadedDir,
		installedDir:    installedDir,
		artifactService: skills.NewInMemoryArtifactService(),
		initialized:     false,
	}
}

// getPreloadedSkillsDir returns the path to the preloaded skills directory
func getPreloadedSkillsDir() string {
	if dir := os.Getenv("WEKNORA_SKILLS_DIR"); dir != "" {
		return dir
	}

	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		skillsDir := filepath.Join(execDir, DefaultPreloadedSkillsDir)
		if _, err := os.Stat(skillsDir); err == nil {
			return skillsDir
		}
	}

	cwd, err := os.Getwd()
	if err == nil {
		skillsDir := filepath.Join(cwd, DefaultPreloadedSkillsDir)
		if _, err := os.Stat(skillsDir); err == nil {
			return skillsDir
		}
	}

	return DefaultPreloadedSkillsDir
}

// getInstalledSkillsDir returns the path to the installed skills directory
func getInstalledSkillsDir() string {
	if dir := os.Getenv("WEKNORA_SKILLS_INSTALLED_DIR"); dir != "" {
		return dir
	}

	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		return filepath.Join(execDir, DefaultInstalledSkillsDir)
	}

	cwd, err := os.Getwd()
	if err == nil {
		return filepath.Join(cwd, DefaultInstalledSkillsDir)
	}

	return DefaultInstalledSkillsDir
}

// ensureInitialized ensures the skill service is initialized
func (s *skillService) ensureInitialized(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.initialized {
		return nil
	}

	// ensure preloaded skills directory exists
	if _, err := os.Stat(s.preloadedDir); os.IsNotExist(err) {
		logger.Warnf(ctx, "Preloaded skills directory does not exist: %s", s.preloadedDir)
		if err := os.MkdirAll(s.preloadedDir, 0755); err != nil {
			logger.Warnf(ctx, "Failed to create preloaded skills directory: %v", err)
		}
	}

	// create installable repository
	repo, err := skills.NewUserInstallableRepository(
		[]string{s.preloadedDir},
		s.installedDir,
	)
	if err != nil {
		// 降级：只使用 Loader
		logger.Warnf(ctx, "Failed to create installable repository, falling back to loader: %v", err)
		s.loader = skills.NewLoader([]string{s.preloadedDir})
		s.initialized = true
		return nil
	}

	s.repo = repo

	s.loader = skills.NewLoader([]string{s.preloadedDir, s.installedDir})

	s.initialized = true
	logger.Infof(ctx, "Skill service initialized: preloaded=%s, installed=%s", s.preloadedDir, s.installedDir)

	return nil
}

// ListPreloadedSkills returns metadata for all preloaded and installed skills
func (s *skillService) ListPreloadedSkills(ctx context.Context) ([]*skills.SkillMetadata, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	metadata, err := s.loader.DiscoverSkills()
	if err != nil {
		logger.Errorf(ctx, "Failed to discover preloaded skills: %v", err)
		return nil, fmt.Errorf("failed to discover skills: %w", err)
	}

	logger.Infof(ctx, "Discovered %d preloaded skills", len(metadata))
	return metadata, nil
}

// GetSkillByName returns the skill with the given name
func (s *skillService) GetSkillByName(ctx context.Context, name string) (*skills.Skill, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	skill, err := s.loader.LoadSkillInstructions(name)
	if err != nil {
		logger.Errorf(ctx, "Failed to load skill %s: %v", name, err)
		return nil, fmt.Errorf("failed to load skill: %w", err)
	}

	return skill, nil
}

// ListAllSkills returns metadata for all skills
func (s *skillService) ListAllSkills(ctx context.Context) ([]skills.Summary, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.repo != nil {
		return s.repo.Summaries(), nil
	}

	metadata, err := s.loader.DiscoverSkills()
	if err != nil {
		return nil, err
	}

	summaries := make([]skills.Summary, 0, len(metadata))
	for _, m := range metadata {
		summaries = append(summaries, skills.Summary{
			Name:        m.Name,
			Description: m.Description,
			Source:      "preloaded",
		})
	}
	return summaries, nil
}

// GetSkillDetail returns the detail of the skill with the given name
func (s *skillService) GetSkillDetail(ctx context.Context, name string) (*skills.FullSkill, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.repo != nil {
		return s.repo.Get(name)
	}

	// 降级：使用 Loader
	skill, err := s.loader.LoadSkillInstructions(name)
	if err != nil {
		return nil, err
	}

	files, _ := s.loader.ListSkillFiles(name)
	var docs []skills.Doc
	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f), ".md") || strings.HasSuffix(strings.ToLower(f), ".txt") {
			content, err := os.ReadFile(filepath.Join(skill.BasePath, f))
			if err == nil {
				docs = append(docs, skills.Doc{Path: f, Content: string(content)})
			}
		}
	}

	return &skills.FullSkill{
		Summary: skills.Summary{
			Name:        skill.Name,
			Description: skill.Description,
			Source:      "preloaded",
		},
		Body:         skill.Instructions,
		Docs:         docs,
		Instructions: skill.Instructions,
		BasePath:     skill.BasePath,
	}, nil
}

// InstallSkillFromURL install skill from url
func (s *skillService) InstallSkillFromURL(ctx context.Context, name string, url string) error {
	if err := s.ensureInitialized(ctx); err != nil {
		return fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.repo == nil {
		return fmt.Errorf("skill installation is not available")
	}

	logger.Infof(ctx, "Installing skill %q from URL: %s", name, url)
	if err := s.repo.InstallFromURL(name, url); err != nil {
		logger.Errorf(ctx, "Failed to install skill %q from URL: %v", name, err)
		return err
	}

	// 同步刷新 Loader
	s.loader.Reload()

	logger.Infof(ctx, "Successfully installed skill %q", name)
	return nil
}

// InstallSkillFromUpload install skill from upload
func (s *skillService) InstallSkillFromUpload(ctx context.Context, name string, data []byte, filename string) error {
	if err := s.ensureInitialized(ctx); err != nil {
		return fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.repo == nil {
		return fmt.Errorf("skill installation is not available")
	}

	logger.Infof(ctx, "Installing skill %q from upload: %s", name, filename)
	if err := s.repo.InstallFromUpload(name, data, filename); err != nil {
		logger.Errorf(ctx, "Failed to install skill %q from upload: %v", name, err)
		return err
	}

	// 同步刷新 Loader
	s.loader.Reload()

	logger.Infof(ctx, "Successfully installed skill %q", name)
	return nil
}

// UninstallSkill uninstall skill
func (s *skillService) UninstallSkill(ctx context.Context, name string) error {
	if err := s.ensureInitialized(ctx); err != nil {
		return fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.repo == nil {
		return fmt.Errorf("skill uninstallation is not available")
	}

	logger.Infof(ctx, "Uninstalling skill %q", name)
	if err := s.repo.Uninstall(name); err != nil {
		logger.Errorf(ctx, "Failed to uninstall skill %q: %v", name, err)
		return err
	}

	s.loader.Reload()

	logger.Infof(ctx, "Successfully uninstalled skill %q", name)
	return nil
}

// RefreshSkills refresh skills
func (s *skillService) RefreshSkills(ctx context.Context) error {
	if err := s.ensureInitialized(ctx); err != nil {
		return fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.repo != nil {
		if err := s.repo.Refresh(); err != nil {
			return err
		}
	}

	s.loader.Reload()

	logger.Infof(ctx, "Skills refreshed successfully")
	return nil
}

// ExportSkill export skill as zip
func (s *skillService) ExportSkill(ctx context.Context, name string) ([]byte, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.repo == nil {
		return nil, fmt.Errorf("skill export is not available")
	}

	return s.repo.ExportSkill(name)
}

// ListSkillFiles list skill files
func (s *skillService) ListSkillFiles(ctx context.Context, name string) ([]string, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.loader.ListSkillFiles(name)
}

// GetSkillFile get skill file
func (s *skillService) GetSkillFile(ctx context.Context, name string, filePath string) ([]byte, string, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, "", fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	file, err := s.loader.LoadSkillFile(name, filePath)
	if err != nil {
		return nil, "", err
	}

	mimeType := mime.TypeByExtension(filepath.Ext(filePath))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	return []byte(file.Content), mimeType, nil
}

// SaveArtifact save artifact
func (s *skillService) SaveArtifact(ctx context.Context, sessionInfo skills.ArtifactSessionInfo, filename string, artifact *skills.Artifact) (int, error) {
	return s.artifactService.SaveArtifact(ctx, sessionInfo, filename, artifact)
}

// LoadArtifact load artifact
func (s *skillService) LoadArtifact(ctx context.Context, sessionInfo skills.ArtifactSessionInfo, filename string, version *int) (*skills.Artifact, error) {
	return s.artifactService.LoadArtifact(ctx, sessionInfo, filename, version)
}

// ListArtifacts list artifacts
func (s *skillService) ListArtifacts(ctx context.Context, sessionInfo skills.ArtifactSessionInfo) ([]skills.ArtifactMeta, error) {
	return s.artifactService.ListArtifacts(ctx, sessionInfo)
}

// DeleteArtifact delete artifact
func (s *skillService) DeleteArtifact(ctx context.Context, sessionInfo skills.ArtifactSessionInfo, filename string) error {
	return s.artifactService.DeleteArtifact(ctx, sessionInfo, filename)
}

// ExportArtifact export artifact
func (s *skillService) ExportArtifact(ctx context.Context, sessionInfo skills.ArtifactSessionInfo, filename string, version *int) ([]byte, string, error) {
	art, err := s.artifactService.LoadArtifact(ctx, sessionInfo, filename, version)
	if err != nil {
		return nil, "", err
	}
	if art == nil {
		return nil, "", fmt.Errorf("artifact %q not found", filename)
	}

	mimeType := art.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	return art.Data, mimeType, nil
}

// GetPreloadedDir returns the preloaded directory
func (s *skillService) GetPreloadedDir() string {
	return s.preloadedDir
}

// GetArtifactService returns the artifact service
func (s *skillService) GetArtifactService() skills.ArtifactService {
	return s.artifactService
}
