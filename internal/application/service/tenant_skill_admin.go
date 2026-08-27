package service

import (
	"context"
	"encoding/json"
	"fmt"

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

// SkillAdminUpdate is everything one admin request may change about an
// installed skill. Both fields are optional: a request carrying only values
// must leave the visibility alone, and one carrying only the toggle must leave
// every stored value alone.
type SkillAdminUpdate struct {
	// Enabled is nil when the request did not mention visibility.
	Enabled *bool
	// EnvValues holds only the names the request sent. A name it does not
	// carry keeps whatever is stored.
	EnvValues map[string]string
}

// UpdateSkillAdmin applies every admin-owned change of one request in a single
// read-modify-write.
//
// Doing both in one cycle is what makes a request all-or-nothing: applying the
// toggle and the values as two updates can persist the first and then fail the
// second, leaving a credential rotation (disable → clear → re-enable → set)
// half applied while the admin is told it failed.
//
// Visibility is metadata only: the files stay in the image either way, which
// is what makes it safe to toggle while the image is otherwise untouched.
// Removing the files is RemoveSkill's job and needs a new snapshot.
//
// Of the values, only declared names are written, and a name outside the
// declaration is ignored rather than refused: a stale settings tab must not
// fail an otherwise valid save, and an invented variable would store a value
// nothing reads while making the declaration — the record of what this skill
// consumes — untrustworthy. An empty string clears the value and keeps the
// declaration, because "nobody filled this in" and "this is not needed" are
// different states.
//
// It returns nil when the skill is not reachable for this workspace and
// config, so the handler renders the usual 404.
func (s *TenantSkillService) UpdateSkillAdmin(
	ctx context.Context, tenantID uint64, configID, skillID string, update SkillAdminUpdate,
) (*types.TenantSkillEntity, error) {
	skill, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return nil, err
	}
	if skill == nil {
		return nil, nil
	}

	// Every value is checked before any part of the request is applied, so a
	// rejected entry cannot leave the declaration — or the toggle — half
	// written.
	for name, value := range update.EnvValues {
		if _, declared := skill.Envs.Get(name); !declared {
			continue
		}
		if len(value) > MaxEnvValueBytes {
			return nil, apperrors.NewBadRequestError(fmt.Sprintf(
				"value of %s cannot exceed %d bytes", name, MaxEnvValueBytes))
		}
	}

	changed := false
	if update.Enabled != nil && skill.Enabled != *update.Enabled {
		skill.Enabled = *update.Enabled
		changed = true
	}
	for i := range skill.Envs {
		value, sent := update.EnvValues[skill.Envs[i].Name]
		if !sent || skill.Envs[i].Value == value {
			continue
		}
		skill.Envs[i].Value = value
		changed = true
	}
	if !changed {
		return skill, nil
	}
	if err := s.skills.UpdateSkill(ctx, skill); err != nil {
		return nil, err
	}
	return skill, nil
}

// SetSkillEnabled hides or reveals an installed skill.
func (s *TenantSkillService) SetSkillEnabled(
	ctx context.Context, tenantID uint64, configID, skillID string, enabled bool,
) (*types.TenantSkillEntity, error) {
	return s.UpdateSkillAdmin(ctx, tenantID, configID, skillID,
		SkillAdminUpdate{Enabled: &enabled})
}

// SetSkillEnvValues stores the workspace-wide values for one skill.
func (s *TenantSkillService) SetSkillEnvValues(
	ctx context.Context, tenantID uint64, configID, skillID string, values map[string]string,
) (*types.TenantSkillEntity, error) {
	return s.UpdateSkillAdmin(ctx, tenantID, configID, skillID,
		SkillAdminUpdate{EnvValues: values})
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
