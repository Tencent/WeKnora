package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/skillpkg"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	ErrSkillDisabled       = errors.New("skill is disabled")
	ErrSkillNotAllowed     = errors.New("skill is not allowed")
	ErrTenantSkillNotFound = errors.New("tenant skill not found")
)

type RuntimeScope struct {
	TenantID uint64
	UserID   string
	Allowed  []types.SkillReference
}

type RuntimeResolver interface {
	LoadInstructions(context.Context, RuntimeScope, types.SkillReference) (*Skill, error)
	ReadFile(context.Context, RuntimeScope, types.SkillReference, string) (string, error)
	Execute(context.Context, RuntimeScope, types.SkillReference, string, []string, string) (*sandbox.ExecuteResult, error)
}

type TenantSkillRepository interface {
	GetByID(context.Context, uint64, string) (*types.TenantSkill, error)
	GetCurrentVersion(context.Context, uint64, string) (*types.TenantSkillVersion, error)
	CreateExecutionAudit(context.Context, *types.SkillExecutionAudit) error
	FinishExecutionAudit(context.Context, uint64, string, types.ExecutionAuditFinish) error
}

type TenantLoader struct {
	repo      TenantSkillRepository
	storage   skillpkg.Storage
	root      string
	preloaded *Loader
}

func NewTenantLoader(repo TenantSkillRepository, storage skillpkg.Storage, root string, preloaded *Loader) *TenantLoader {
	return &TenantLoader{repo: repo, storage: storage, root: filepath.Clean(root), preloaded: preloaded}
}

func (loader *TenantLoader) LoadInstructions(ctx context.Context, scope RuntimeScope, ref types.SkillReference) (*Skill, error) {
	if !referenceAllowed(scope.Allowed, ref) {
		return nil, ErrSkillNotAllowed
	}
	if ref.Source == types.SkillSourcePreloaded {
		return loader.preloaded.LoadSkillInstructions(ref.SkillID)
	}
	version, skill, err := loader.authorize(ctx, scope.TenantID, ref)
	if err != nil {
		return nil, err
	}
	content, err := loader.readVersionFile(ctx, scope.TenantID, skill.ID, version, SkillFileName)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseSkillFile(content)
	if err != nil {
		return nil, err
	}
	parsed.BasePath = filepath.Join(loader.root, fmt.Sprint(scope.TenantID), skill.ID, version.ID)
	parsed.FilePath = filepath.Join(parsed.BasePath, SkillFileName)
	return parsed, nil
}

func (loader *TenantLoader) ReadFile(ctx context.Context, scope RuntimeScope, ref types.SkillReference, relativePath string) (string, error) {
	if !referenceAllowed(scope.Allowed, ref) {
		return "", ErrSkillNotAllowed
	}
	if ref.Source == types.SkillSourcePreloaded {
		file, err := loader.preloaded.LoadSkillFile(ref.SkillID, relativePath)
		if err != nil {
			return "", err
		}
		return file.Content, nil
	}
	version, skill, err := loader.authorize(ctx, scope.TenantID, ref)
	if err != nil {
		return "", err
	}
	return loader.readVersionFile(ctx, scope.TenantID, skill.ID, version, relativePath)
}

func (loader *TenantLoader) authorize(ctx context.Context, tenantID uint64, ref types.SkillReference) (*types.TenantSkillVersion, *types.TenantSkill, error) {
	if ref.Source != types.SkillSourceTenant || loader.repo == nil || loader.storage == nil {
		return nil, nil, ErrTenantSkillNotFound
	}
	skill, err := loader.repo.GetByID(ctx, tenantID, ref.SkillID)
	if err != nil {
		return nil, nil, err
	}
	if skill.Status != types.TenantSkillEnabled {
		return nil, nil, ErrSkillDisabled
	}
	version, err := loader.repo.GetCurrentVersion(ctx, tenantID, skill.ID)
	if err != nil {
		return nil, nil, err
	}
	if err := loader.storage.VerifyVersion(ctx, tenantID, version); err != nil {
		return nil, nil, err
	}
	return version, skill, nil
}

func (loader *TenantLoader) readVersionFile(ctx context.Context, tenantID uint64, skillID string, version *types.TenantSkillVersion, relativePath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relativePath))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrSkillNotAllowed
	}
	base := filepath.Join(loader.root, fmt.Sprint(tenantID), skillID, version.ID)
	target := filepath.Join(base, clean)
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrSkillNotAllowed
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func referenceAllowed(allowed []types.SkillReference, ref types.SkillReference) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if item == ref {
			return true
		}
	}
	return false
}

func MigrateLegacySkillRefs(selected []string, preloaded []*SkillMetadata) ([]types.SkillReference, []string) {
	known := make(map[string]struct{}, len(preloaded))
	for _, item := range preloaded {
		known[item.Name] = struct{}{}
	}
	refs, invalid := make([]types.SkillReference, 0, len(selected)), make([]string, 0)
	for _, name := range selected {
		if _, ok := known[name]; ok {
			refs = append(refs, types.SkillReference{Source: types.SkillSourcePreloaded, SkillID: name})
		} else {
			invalid = append(invalid, name)
		}
	}
	return refs, invalid
}
