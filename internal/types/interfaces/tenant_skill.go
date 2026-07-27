package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type TenantSkillRepository interface {
	CreateStaging(ctx context.Context, skill *types.TenantSkill, version *types.TenantSkillVersion) error
	CreateVersion(ctx context.Context, version *types.TenantSkillVersion) error
	GetByID(ctx context.Context, tenantID uint64, skillID string) (*types.TenantSkill, error)
	GetCurrentVersion(ctx context.Context, tenantID uint64, skillID string) (*types.TenantSkillVersion, error)
	List(ctx context.Context, tenantID uint64, includeDisabled bool) ([]*types.TenantSkill, error)
	SetVersionReady(ctx context.Context, tenantID uint64, skillID, versionID, storagePath, contentHash string, manifestJSON []byte) error
	SwitchCurrentVersion(ctx context.Context, tenantID uint64, skillID, oldVersionID, newVersionID string) error
	SetStatus(ctx context.Context, tenantID uint64, skillID string, status types.TenantSkillStatus) error
	SoftDelete(ctx context.Context, tenantID uint64, skillID string) error
	ListReconciliationCandidates(ctx context.Context, olderThan time.Time) ([]*types.TenantSkillVersion, error)
	CreateExecutionAudit(ctx context.Context, audit *types.SkillExecutionAudit) error
	FinishExecutionAudit(ctx context.Context, tenantID uint64, auditID string, finish types.ExecutionAuditFinish) error
}
