package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/skillpkg"
	"github.com/Tencent/WeKnora/internal/skillrunner"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// DefaultPreloadedSkillsDir is the default directory for preloaded skills
const DefaultPreloadedSkillsDir = "skills/preloaded"

// skillService implements SkillService interface
type skillService struct {
	loader                   *skills.Loader
	preloadedDir             string
	mu                       sync.RWMutex
	initialized              bool
	tenantRepo               interfaces.TenantSkillRepository
	storage                  skillpkg.Storage
	uploadEnabled            bool
	scriptExecutionAvailable bool
	runnerHealth interface{ Healthy(context.Context) bool }
}

type SkillServiceOption func(*skillService)

func WithTenantSkillRepository(repo interfaces.TenantSkillRepository) SkillServiceOption {
	return func(service *skillService) { service.tenantRepo = repo }
}

func WithTenantSkillStorage(storage skillpkg.Storage) SkillServiceOption {
	return func(service *skillService) { service.storage = storage }
}

func WithTenantUploadEnabled(enabled bool) SkillServiceOption {
	return func(service *skillService) { service.uploadEnabled = enabled }
}

func WithScriptExecutionAvailable(available bool) SkillServiceOption {
	return func(service *skillService) { service.scriptExecutionAvailable = available }
}

// NewSkillService creates a new skill service
func NewSkillService(options ...SkillServiceOption) interfaces.SkillService {
	// Determine the preloaded skills directory
	preloadedDir := getPreloadedSkillsDir()

	service := &skillService{
		preloadedDir: preloadedDir,
		initialized:  false,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// NewConfiguredSkillService wires tenant uploads only for the PostgreSQL edition.
// Lite/SQLite keeps preloaded skills available without touching tenant skill tables.
func NewConfiguredSkillService(repo interfaces.TenantSkillRepository) interfaces.SkillService {
	root := os.Getenv("WEKNORA_TENANT_SKILLS_DIR")
	if root == "" {
		root = "data/tenant-skills"
	}
	uploadEnabled := strings.EqualFold(os.Getenv("DB_DRIVER"), "postgres")
	storage := skillpkg.NewFileStorage(root, skillpkg.NewValidator(skillpkg.DefaultLimits()))
	configured := NewSkillService(
		WithTenantSkillRepository(repo),
		WithTenantSkillStorage(storage),
		WithTenantUploadEnabled(uploadEnabled),
	)
	service := configured.(*skillService)
	if runnerURL, credential := os.Getenv("WEKNORA_SKILL_RUNNER_URL"), os.Getenv("WEKNORA_SKILL_RUNNER_CREDENTIAL"); runnerURL != "" && credential != "" {
		service.runnerHealth = skillrunner.NewClient(runnerURL, credential, 2*time.Second)
	}
	return configured
}

// getPreloadedSkillsDir returns the path to the preloaded skills directory
func getPreloadedSkillsDir() string {
	// Check if SKILLS_DIR environment variable is set
	if dir := os.Getenv("WEKNORA_SKILLS_DIR"); dir != "" {
		return dir
	}

	// Try to find the skills directory relative to the executable
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		skillsDir := filepath.Join(execDir, DefaultPreloadedSkillsDir)
		if _, err := os.Stat(skillsDir); err == nil {
			return skillsDir
		}
	}

	// Try current working directory
	cwd, err := os.Getwd()
	if err == nil {
		skillsDir := filepath.Join(cwd, DefaultPreloadedSkillsDir)
		if _, err := os.Stat(skillsDir); err == nil {
			return skillsDir
		}
	}

	// Default to relative path (will be created if needed)
	return DefaultPreloadedSkillsDir
}

// ensureInitialized initializes the loader if not already done
func (s *skillService) ensureInitialized(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.initialized {
		return nil
	}

	// Check if preloaded directory exists
	if _, err := os.Stat(s.preloadedDir); os.IsNotExist(err) {
		logger.Warnf(ctx, "Preloaded skills directory does not exist: %s", s.preloadedDir)
		// Create the directory to avoid repeated warnings
		if err := os.MkdirAll(s.preloadedDir, 0755); err != nil {
			logger.Warnf(ctx, "Failed to create preloaded skills directory: %v", err)
		}
	}

	// Create loader with preloaded directory
	s.loader = skills.NewLoader([]string{s.preloadedDir})
	s.initialized = true

	logger.Infof(ctx, "Skill service initialized with preloaded directory: %s", s.preloadedDir)

	return nil
}

// ListPreloadedSkills returns metadata for all preloaded skills
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

// GetSkillByName retrieves a skill by its name
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

// GetPreloadedDir returns the configured preloaded skills directory
func (s *skillService) GetPreloadedDir() string {
	return s.preloadedDir
}

func (s *skillService) TenantUploadAvailable() bool {
	return s.uploadEnabled && s.tenantRepo != nil && s.storage != nil
}

func (s *skillService) ScriptExecutionAvailable() bool {
	if s.runnerHealth == nil { return s.scriptExecutionAvailable }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.runnerHealth.Healthy(ctx)
}

func (s *skillService) Upload(
	ctx context.Context,
	tenantID uint64,
	userID string,
	archive io.Reader,
	size int64,
) (*types.TenantSkill, error) {
	if !s.TenantUploadAvailable() {
		return nil, fmt.Errorf("tenant skill upload is unavailable")
	}
	uploadID, skillID, versionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	pkg, err := s.storage.Stage(ctx, tenantID, uploadID, archive, size)
	if err != nil {
		return nil, err
	}
	manifestJSON, err := json.Marshal(pkg.Manifest)
	if err != nil {
		return nil, err
	}
	skill := &types.TenantSkill{
		ID: skillID, TenantID: tenantID, Name: pkg.Manifest.Name,
		Description: pkg.Manifest.Description, Category: skillCategory(pkg.Manifest.Category),
		Status: types.TenantSkillEnabled, HasScripts: pkg.HasScripts, UploadedBy: userID,
	}
	version := &types.TenantSkillVersion{
		ID: versionID, TenantID: tenantID, SkillID: skillID, Version: 1,
		State: types.SkillVersionStaging, StoragePath: ".staging/" + uploadID,
		ContentHash: strings.Repeat("0", 64), ManifestJSON: manifestJSON, CreatedBy: userID,
	}
	if err := s.tenantRepo.CreateStaging(ctx, skill, version); err != nil {
		return nil, err
	}
	storagePath, contentHash, err := s.storage.Materialize(ctx, tenantID, skillID, versionID, pkg)
	if err != nil {
		return nil, err
	}
	if err := s.tenantRepo.SetVersionReady(
		ctx, tenantID, skillID, versionID, storagePath, contentHash, manifestJSON,
	); err != nil {
		return nil, err
	}
	if err := s.tenantRepo.SwitchCurrentVersion(ctx, tenantID, skillID, "", versionID); err != nil {
		return nil, err
	}
	return s.tenantRepo.GetByID(ctx, tenantID, skillID)
}

func (s *skillService) ListVisible(
	ctx context.Context,
	tenantID uint64,
	manager bool,
) ([]*types.SkillSummary, error) {
	metadata, err := s.ListPreloadedSkills(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*types.SkillSummary, 0, len(metadata))
	for _, item := range metadata {
		result = append(result, &types.SkillSummary{
			Source: types.SkillSourcePreloaded, SkillID: item.Name, Name: item.Name,
			Description: item.Description, Category: types.SkillCategoryOther,
			Status: types.TenantSkillEnabled, ReadOnly: true,
		})
	}
	if !s.TenantUploadAvailable() {
		return result, nil
	}
	tenantSkills, err := s.tenantRepo.List(ctx, tenantID, manager)
	if err != nil {
		return nil, err
	}
	for _, item := range tenantSkills {
		summary := tenantSkillSummary(item)
		if version, versionErr := s.tenantRepo.GetCurrentVersion(ctx, tenantID, item.ID); versionErr == nil {
			summary.Version = version.Version
		}
		result = append(result, summary)
	}
	return result, nil
}

func (s *skillService) UpdatePackage(
	ctx context.Context,
	tenantID uint64,
	userID string,
	skillID string,
	archive io.Reader,
	size int64,
	expectedVersion int64,
) (*types.SkillDetail, error) {
	if !s.TenantUploadAvailable() {
		return nil, fmt.Errorf("tenant skill upload is unavailable")
	}
	skill, err := s.tenantRepo.GetByID(ctx, tenantID, skillID)
	if err != nil {
		return nil, err
	}
	current, err := s.tenantRepo.GetCurrentVersion(ctx, tenantID, skillID)
	if err != nil {
		return nil, err
	}
	if current.Version != expectedVersion {
		return nil, fmt.Errorf("skill version conflict: expected %d, current %d", expectedVersion, current.Version)
	}
	uploadID, versionID := uuid.NewString(), uuid.NewString()
	pkg, err := s.storage.Stage(ctx, tenantID, uploadID, archive, size)
	if err != nil {
		return nil, err
	}
	if pkg.Manifest.Name != skill.Name {
		return nil, fmt.Errorf("skill package name cannot change")
	}
	manifestJSON, err := json.Marshal(pkg.Manifest)
	if err != nil {
		return nil, err
	}
	version := &types.TenantSkillVersion{
		ID: versionID, TenantID: tenantID, SkillID: skillID, Version: current.Version + 1,
		State: types.SkillVersionStaging, StoragePath: ".staging/" + uploadID,
		ContentHash: strings.Repeat("0", 64), ManifestJSON: manifestJSON, CreatedBy: userID,
	}
	if err := s.tenantRepo.CreateVersion(ctx, version); err != nil {
		return nil, err
	}
	storagePath, contentHash, err := s.storage.Materialize(ctx, tenantID, skillID, versionID, pkg)
	if err != nil {
		return nil, err
	}
	if err := s.tenantRepo.SetVersionReady(
		ctx, tenantID, skillID, versionID, storagePath, contentHash, manifestJSON,
	); err != nil {
		return nil, err
	}
	if err := s.tenantRepo.SwitchCurrentVersion(ctx, tenantID, skillID, current.ID, versionID); err != nil {
		return nil, err
	}
	return s.GetVisible(ctx, tenantID, types.SkillReference{Source: types.SkillSourceTenant, SkillID: skillID}, true)
}

func (s *skillService) GetVisible(
	ctx context.Context,
	tenantID uint64,
	ref types.SkillReference,
	manager bool,
) (*types.SkillDetail, error) {
	if ref.Source == types.SkillSourcePreloaded {
		item, err := s.GetSkillByName(ctx, ref.SkillID)
		if err != nil {
			return nil, err
		}
		return &types.SkillDetail{SkillSummary: types.SkillSummary{
			Source: types.SkillSourcePreloaded, SkillID: item.Name, Name: item.Name,
			Description: item.Description, Category: types.SkillCategoryOther,
			Status: types.TenantSkillEnabled, ReadOnly: true,
		}}, nil
	}
	if ref.Source != types.SkillSourceTenant || !s.TenantUploadAvailable() {
		return nil, repository.ErrTenantSkillNotFound
	}
	skill, err := s.tenantRepo.GetByID(ctx, tenantID, ref.SkillID)
	if err != nil {
		return nil, err
	}
	if !manager && skill.Status != types.TenantSkillEnabled {
		return nil, repository.ErrTenantSkillNotFound
	}
	version, err := s.tenantRepo.GetCurrentVersion(ctx, tenantID, skill.ID)
	if err != nil {
		return nil, err
	}
	detail := &types.SkillDetail{
		SkillSummary: *tenantSkillSummary(skill), VersionID: version.ID,
		ContentHash: version.ContentHash,
	}
	detail.Version = version.Version
	return detail, nil
}

func (s *skillService) SetStatuses(
	ctx context.Context,
	tenantID uint64,
	updates []types.SkillStatusUpdate,
) []types.SkillStatusResult {
	results := make([]types.SkillStatusResult, 0, len(updates))
	for _, update := range updates {
		result := types.SkillStatusResult{SkillID: update.SkillID}
		if update.Status != types.TenantSkillEnabled && update.Status != types.TenantSkillDisabled {
			result.Code = "invalid_status"
		} else if err := s.tenantRepo.SetStatus(ctx, tenantID, update.SkillID, update.Status); err != nil {
			result.Code = "not_found"
		} else {
			result.Success = true
		}
		results = append(results, result)
	}
	return results
}

func (s *skillService) Delete(
	ctx context.Context,
	tenantID uint64,
	_ string,
	skillID string,
) error {
	if !s.TenantUploadAvailable() {
		return fmt.Errorf("tenant skill upload is unavailable")
	}
	return s.tenantRepo.SoftDelete(ctx, tenantID, skillID)
}

func tenantSkillSummary(skill *types.TenantSkill) *types.SkillSummary {
	return &types.SkillSummary{
		Source: types.SkillSourceTenant, SkillID: skill.ID, Name: skill.Name,
		Description: skill.Description, Category: skill.Category, Status: skill.Status,
		HasScripts: skill.HasScripts, UploadedBy: skill.UploadedBy, UpdatedAt: skill.UpdatedAt,
	}
}

func skillCategory(value string) types.SkillCategory {
	switch types.SkillCategory(value) {
	case types.SkillCategoryContent, types.SkillCategoryData, types.SkillCategoryDevelopment,
		types.SkillCategoryWorkflow, types.SkillCategoryOther:
		return types.SkillCategory(value)
	default:
		return types.SkillCategoryOther
	}
}
