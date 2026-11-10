package service

import (
	"context"
	"errors"
	"fmt"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

const skillInstallStoppedMessage = "安装已停止"

var errSkillInstallStopped = errors.New(skillInstallStoppedMessage)

// skillRunCancel is one in-process install or remove. Pointer identity is what
// lets a superseded run tell itself apart from the retry that replaced it.
type skillRunCancel struct {
	cancel context.CancelFunc
}

func skillRunKey(tenantID uint64, configID, skillID string) string {
	return fmt.Sprintf("%d:%s:%s", tenantID, configID, skillID)
}

// bindSkillRun ties ctx to StopSkill for this skill. The returned unbind must
// run when the goroutine exits, including after StopSkill has already cancelled.
func (s *TenantSkillService) bindSkillRun(
	ctx context.Context, tenantID uint64, configID, skillID string,
) (context.Context, func()) {
	if s == nil {
		return ctx, func() {}
	}
	runCtx, cancel := context.WithCancel(ctx)
	handle := &skillRunCancel{cancel: cancel}
	key := skillRunKey(tenantID, configID, skillID)
	s.runCancelMu.Lock()
	if s.runCancels == nil {
		s.runCancels = map[string]*skillRunCancel{}
	}
	if prev := s.runCancels[key]; prev != nil && prev.cancel != nil {
		prev.cancel()
	}
	s.runCancels[key] = handle
	s.runCancelMu.Unlock()
	return runCtx, func() {
		cancel()
		s.runCancelMu.Lock()
		if s.runCancels[key] == handle {
			delete(s.runCancels, key)
		}
		s.runCancelMu.Unlock()
	}
}

func (s *TenantSkillService) lookupSkillRun(
	tenantID uint64, configID, skillID string,
) *skillRunCancel {
	if s == nil {
		return nil
	}
	s.runCancelMu.Lock()
	defer s.runCancelMu.Unlock()
	if s.runCancels == nil {
		return nil
	}
	return s.runCancels[skillRunKey(tenantID, configID, skillID)]
}

func (s *TenantSkillService) cancelSkillRun(tenantID uint64, configID, skillID string) {
	handle := s.lookupSkillRun(tenantID, configID, skillID)
	if handle != nil && handle.cancel != nil {
		handle.cancel()
	}
}

// skillRunStillBound reports whether handle is still the run registered for
// this skill. A nil handle means the caller never bound one (direct runInstall
// in tests) and is treated as still bound so those paths keep failing the row.
func (s *TenantSkillService) skillRunStillBound(
	tenantID uint64, configID, skillID string, handle *skillRunCancel,
) bool {
	if handle == nil {
		return true
	}
	return s.lookupSkillRun(tenantID, configID, skillID) == handle
}

// withSkillRunLock is withConfigLock plus a StopSkill-cancellable context.
func (s *TenantSkillService) withSkillRunLock(
	ctx context.Context, tenantID uint64, configID, skillID string,
	fn func(context.Context) error,
) error {
	runCtx, unbind := s.bindSkillRun(ctx, tenantID, configID, skillID)
	defer unbind()
	return s.withConfigLock(runCtx, tenantID, configID, fn)
}

// StopSkill aborts an in-flight install so the operator can retry or uninstall.
// After a process restart there is no goroutine to cancel; the row is still
// rewritten, which is what unblocks the UI. Removal is left alone.
func (s *TenantSkillService) StopSkill(
	ctx context.Context, tenantID uint64, configID, skillID string,
) (*types.TenantSkillEntity, error) {
	if s == nil || s.skills == nil {
		return nil, errors.New("skill service is not configured")
	}
	skill, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return nil, err
	}
	if skill == nil {
		return nil, apperrors.NewNotFoundError("skill not found")
	}
	switch skill.Status {
	case types.SkillStatusFailed:
		return skill, nil
	case types.SkillStatusInstalling:
	default:
		return nil, apperrors.NewBadRequestError("skill is not installing")
	}

	s.cancelSkillRun(tenantID, configID, skillID)
	s.failSkill(ctx, tenantID, configID, skillID, nil, errSkillInstallStopped)

	updated, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, apperrors.NewNotFoundError("skill not found")
	}
	return updated, nil
}
