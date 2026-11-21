package memory

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// ErrMemoryAdminForbidden 表示调用者不是具有用户身份的租户管理员。
var ErrMemoryAdminForbidden = errors.New("memory: administrator access required")

// ErrSubjectNotFound 对缺失和其他租户的主体返回相同错误。
var ErrSubjectNotFound = errors.New("memory: subject not found")

func memoryAdminCaller(ctx context.Context) (types.Caller, error) {
	caller := types.CallerFromContext(ctx)
	_, apiKey := types.TenantAPIKeyScopeFromContext(ctx)
	if apiKey || caller.TenantID == 0 || strings.TrimSpace(caller.UserID) == "" ||
		(caller.Role != types.TenantRoleAdmin && caller.Role != types.TenantRoleOwner) {
		return types.Caller{}, ErrMemoryAdminForbidden
	}
	return caller, nil
}

// adminScope 通过调用者租户和主体 UUID 解析显式作用域，不修改请求身份。
func (s *Service) adminScope(ctx context.Context, subjectID string) (interfaces.MemoryScope, error) {
	caller, err := memoryAdminCaller(ctx)
	if err != nil {
		return interfaces.MemoryScope{}, err
	}
	id, err := uuid.Parse(subjectID)
	if err != nil {
		return interfaces.MemoryScope{}, ErrSubjectNotFound
	}
	subject, err := s.repo.GetSubjectByID(ctx, caller.TenantID, id.String())
	if err != nil {
		return interfaces.MemoryScope{}, err
	}
	if subject == nil {
		return interfaces.MemoryScope{}, ErrSubjectNotFound
	}
	return (interfaces.MemoryScope{TenantID: caller.TenantID, SubjectID: subject.SubjectID}).
		WithGeneration(subject.ExtractionState.Generation), nil
}

func memoryAdminPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// AdminListSubjects 分页列出调用者租户内的主体及不含记忆内容的统计。
func (s *Service) AdminListSubjects(
	ctx context.Context, limit, offset int,
) ([]*types.MemorySubjectSummary, int64, error) {
	caller, err := memoryAdminCaller(ctx)
	if err != nil {
		return nil, 0, err
	}
	limit, offset = memoryAdminPage(limit, offset)
	return s.repo.ListSubjects(ctx, caller.TenantID, limit, offset)
}

// AdminListItems 查询指定主体的记忆，即使工作区或主体已禁用记忆。
func (s *Service) AdminListItems(
	ctx context.Context, subjectID, status string, limit, offset int,
) ([]*types.MemoryItem, int64, error) {
	scope, err := s.adminScope(ctx, subjectID)
	if err != nil {
		return nil, 0, err
	}
	limit, offset = memoryAdminPage(limit, offset)
	return s.repo.ListItems(ctx, scope, status, limit, offset)
}

// AdminConfirmItem 确认指定主体的待确认记忆，复用个人记忆的生命周期。
func (s *Service) AdminConfirmItem(ctx context.Context, subjectID, itemID string) (*types.MemoryItem, error) {
	scope, err := s.adminScope(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	return s.confirmItemInScope(ctx, scope, itemID)
}

// AdminRejectItem 仅拒绝待确认记忆，并记录拒绝指纹及更新主体摘要。
func (s *Service) AdminRejectItem(ctx context.Context, subjectID, itemID string) error {
	scope, err := s.adminScope(ctx, subjectID)
	if err != nil {
		return err
	}
	return s.deleteItemInScope(ctx, scope, itemID, true)
}

// AdminClear 原子清除指定主体当前的记忆快照，并保留主体开关和提取游标。
func (s *Service) AdminClear(ctx context.Context, subjectID string) (int64, error) {
	scope, err := s.adminScope(ctx, subjectID)
	if err != nil {
		return 0, err
	}
	return s.repo.ClearSubjectMemory(ctx, scope)
}

// AdminSetEnabled 修改指定主体的记忆开关，不改变管理员自己的记忆设置。
func (s *Service) AdminSetEnabled(ctx context.Context, subjectID string, enabled bool) error {
	scope, err := s.adminScope(ctx, subjectID)
	if err != nil {
		return err
	}
	return s.repo.UpdateSubjectEnabled(ctx, scope, enabled)
}
