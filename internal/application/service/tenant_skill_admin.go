package service

import (
	"context"
	"encoding/json"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// ListUsableSkills returns the installed skills a chat turn can actually
// invoke on this config: ready, enabled, and carried by the live snapshot.
// Failures and a missing config yield an empty list so @ mention never 500s.
func (s *TenantSkillService) ListUsableSkills(
	ctx context.Context, tenantID uint64, configID string,
) []*types.TenantSkillEntity {
	return effectiveTenantSkills(ctx, s.configs, s.skills, tenantID, configID)
}

// ListSkills returns the skills installed onto one sandbox config.
//
// The config is read first so a config that does not belong to this workspace
// is reported as missing rather than as a config that simply has no skills.
func (s *TenantSkillService) ListSkills(
	ctx context.Context, tenantID uint64, configID string,
) ([]*types.TenantSkillEntity, error) {
	cfgEntity, err := s.configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return nil, err
	}
	if cfgEntity == nil {
		return nil, apperrors.NewNotFoundError("sandbox config not found")
	}
	return s.skills.ListSkillsByConfig(ctx, tenantID, configID)
}

// GetSkill returns one installed skill, or nil when this workspace's config
// does not carry it. The repository scopes the lookup by workspace and config,
// so a skill ID from another workspace is indistinguishable from a missing one.
func (s *TenantSkillService) GetSkill(
	ctx context.Context, tenantID uint64, configID, skillID string,
) (*types.TenantSkillEntity, error) {
	return s.skills.GetSkill(ctx, tenantID, configID, skillID)
}

// SetSkillEnabled hides or reveals an installed skill.
//
// This is metadata only: the files stay in the image either way, which is what
// makes it safe to toggle while the image is otherwise untouched. Removing the
// files is RemoveSkill's job and needs a new snapshot. It returns nil when the
// skill is not reachable for this workspace and config.
func (s *TenantSkillService) SetSkillEnabled(
	ctx context.Context, tenantID uint64, configID, skillID string, enabled bool,
) (*types.TenantSkillEntity, error) {
	skill, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return nil, err
	}
	if skill == nil {
		return nil, nil
	}
	if skill.Enabled == enabled {
		return skill, nil
	}
	skill.Enabled = enabled
	if err := s.skills.UpdateSkill(ctx, skill); err != nil {
		return nil, err
	}
	return skill, nil
}

// SubscribeProgress follows one install or removal.
//
// A nil channel means there is no live stream to follow — without Redis
// nothing is published at all — and the caller is expected to fall back to the
// durable status. The returned closer must always be called: it releases the
// Redis subscription, which would otherwise outlive the request.
func (s *TenantSkillService) SubscribeProgress(
	ctx context.Context, tenantID uint64, configID, skillID string,
) (<-chan SkillProgress, func(), error) {
	if s.redis == nil {
		return nil, func() {}, nil
	}
	sub := s.redis.Subscribe(ctx, skillProgressKey(tenantID, configID, skillID))
	out := make(chan SkillProgress, skillProgressBuffer)
	go func() {
		defer close(out)
		for msg := range sub.Channel() {
			var p SkillProgress
			if err := json.Unmarshal([]byte(msg.Payload), &p); err != nil {
				continue
			}
			select {
			case out <- p:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, func() { _ = sub.Close() }, nil
}

// skillProgressBuffer absorbs the burst a fast stage transition produces so a
// subscriber that is momentarily busy writing to its client does not stall the
// pub/sub reader.
const skillProgressBuffer = 16
