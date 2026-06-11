package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// EmbedChannelRepository persists web embed channel rows.
type EmbedChannelRepository interface {
	Create(ctx context.Context, ch *types.EmbedChannel) error
	GetByID(ctx context.Context, id string) (*types.EmbedChannel, error)
	GetByPublishToken(ctx context.Context, token string) (*types.EmbedChannel, error)
	ListByKnowledgeBase(ctx context.Context, tenantID uint64, kbID string) ([]*types.EmbedChannel, error)
	Update(ctx context.Context, ch *types.EmbedChannel) error
	Delete(ctx context.Context, tenantID uint64, id string) error
}

// EmbedChannelService manages web embed channel lifecycle.
type EmbedChannelService interface {
	Create(ctx context.Context, tenantID uint64, kbID string, req *types.EmbedChannel) (*types.EmbedChannel, string, error)
	ListByKnowledgeBase(ctx context.Context, tenantID uint64, kbID string) ([]*types.EmbedChannel, error)
	Update(ctx context.Context, tenantID uint64, id string, req *types.EmbedChannel, enabled *bool) (*types.EmbedChannel, error)
	Delete(ctx context.Context, tenantID uint64, id string) error
	RotateToken(ctx context.Context, tenantID uint64, id string) (*types.EmbedChannel, string, error)
	LookupForEmbed(ctx context.Context, channelID, token string) (*types.EmbedChannel, error)
	PublicConfig(ch *types.EmbedChannel) types.EmbedChannelPublicConfig
}
