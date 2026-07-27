package interfaces

import (
	"context"
	"io"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/types"
)

// SkillService defines the interface for skill business logic
type SkillService interface {
	// ListPreloadedSkills returns metadata for all preloaded skills
	ListPreloadedSkills(ctx context.Context) ([]*skills.SkillMetadata, error)

	// GetSkillByName retrieves a skill by its name
	GetSkillByName(ctx context.Context, name string) (*skills.Skill, error)

	TenantUploadAvailable() bool
	ScriptExecutionAvailable() bool
	Upload(ctx context.Context, tenantID uint64, userID string, archive io.Reader, size int64) (*types.TenantSkill, error)
	UpdatePackage(ctx context.Context, tenantID uint64, userID, skillID string, archive io.Reader, size int64, expectedVersion int64) (*types.SkillDetail, error)
	ListVisible(ctx context.Context, tenantID uint64, manager bool) ([]*types.SkillSummary, error)
	GetVisible(ctx context.Context, tenantID uint64, ref types.SkillReference, manager bool) (*types.SkillDetail, error)
	SetStatuses(ctx context.Context, tenantID uint64, updates []types.SkillStatusUpdate) []types.SkillStatusResult
	Delete(ctx context.Context, tenantID uint64, userID, skillID string) error
}
