package im

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Kept separate from channel settings: a concurrent rename must never resurrect
// a revoked delegation. Only the explicit admin operation can change this row.
type originalSharing struct {
	ChannelID string `gorm:"primaryKey"`
	AgentID   string
	KBID      string `gorm:"column:kb_id"`
	GrantorID string
	Revision  string
}

func (originalSharing) TableName() string { return "im_original_shares" }

// OriginalSharingKB returns the explicit sharing destination of this channel.
func (s *Service) OriginalSharingKB(ctx context.Context, channel *IMChannel) (string, error) {
	var scope originalSharing
	err := s.db.WithContext(ctx).Where("channel_id = ? AND agent_id = ?", channel.ID, channel.AgentID).
		Take(&scope).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return scope.KBID, err
}

// ConfigureOriginalSharing grants or revokes sharing for one workspace-owned KB.
func (s *Service) ConfigureOriginalSharing(ctx context.Context, channel *IMChannel, kbID string) error {
	caller := types.CallerFromContext(ctx)
	_, apiKey := types.TenantAPIKeyScopeFromContext(ctx)
	if channel.Platform != string(PlatformFeishu) || caller.TenantID != channel.TenantID || apiKey ||
		caller.UserID == "" || types.IsSyntheticUserID(caller.UserID) ||
		!caller.Role.HasPermission(types.TenantRoleAdmin) {
		return access.ErrForbidden
	}
	kbID = strings.TrimSpace(kbID)
	if kbID == "" {
		return s.db.WithContext(ctx).Where("channel_id = ?", channel.ID).Delete(&originalSharing{}).Error
	}
	if s.redis == nil || originalDownloadBase() == "" {
		return errors.New("original sharing requires Redis and an HTTPS APP_EXTERNAL_URL")
	}
	scope := originalSharing{
		ChannelID: channel.ID, AgentID: channel.AgentID, KBID: kbID,
		GrantorID: caller.UserID, Revision: uuid.NewString(),
	}
	if _, err := s.authorizeOriginalSharing(ctx, channel, &scope); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "channel_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"agent_id", "kb_id", "grantor_id", "revision"}),
	}).Create(&scope).Error
}

// Both issuance and redemption need live authority from the human who shared
// this KB. The IM user and the anonymous recipient never acquire that role.
func (s *Service) authorizeOriginalSharing(
	ctx context.Context, channel *IMChannel, scope *originalSharing,
) (*types.CustomAgent, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&types.TenantMember{}).
		Joins("JOIN users ON users.id = tenant_members.user_id").
		Where("users.deleted_at IS NULL AND users.is_active = ?", true).
		Where("tenant_members.user_id = ? AND tenant_members.tenant_id = ?", scope.GrantorID, channel.TenantID).
		Where("tenant_members.status = ? AND tenant_members.role IN ?",
			types.TenantMemberStatusActive, []types.TenantRole{types.TenantRoleAdmin, types.TenantRoleOwner}).
		Count(&n).Error
	if err != nil || n != 1 {
		return nil, access.ErrForbidden
	}
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, scope.KBID)
	if err != nil || kb == nil || kb.DeletedAt.Valid || kb.TenantID != channel.TenantID {
		return nil, access.ErrForbidden
	}
	agent, err := s.agentService.GetAgentByID(ctx, channel.AgentID)
	if err != nil || agent == nil || agent.TenantID != channel.TenantID || agent.ID != scope.AgentID ||
		!outboundKBInScope(agent, nil, scope.KBID) {
		return nil, access.ErrForbidden
	}
	return agent, nil
}
