package im

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type channelKBLookup struct {
	interfaces.KnowledgeBaseService
}

func (channelKBLookup) GetKnowledgeBaseByIDOnly(_ context.Context, id string) (*types.KnowledgeBase, error) {
	switch id {
	case "own-kb":
		return &types.KnowledgeBase{ID: id, TenantID: 7}, nil
	case "foreign-kb":
		return &types.KnowledgeBase{ID: id, TenantID: 84}, nil
	}
	return nil, errors.New("not found")
}

// IM files are saved into the channel's KB as the channel's workspace, so the
// KB must belong to that workspace (and to a restricted key's allow-list).
func TestSetChannelKnowledgeBaseIDRequiresOwnKB(t *testing.T) {
	svc := &Service{kbService: channelKBLookup{}}
	channel := &IMChannel{TenantID: 7, KnowledgeBaseID: "own-kb"}

	for _, id := range []string{"foreign-kb", "missing-kb"} {
		require.Error(t, svc.SetChannelKnowledgeBaseID(context.Background(), channel, id), id)
		require.Equal(t, "own-kb", channel.KnowledgeBaseID, "a rejected ID leaves the binding unchanged")
	}

	restricted := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"other-kb"},
		Capabilities:     types.StringArray{string(types.APIKeyCapabilityManageChannels)},
	})
	require.Error(t, svc.SetChannelKnowledgeBaseID(restricted, channel, "own-kb"))

	require.NoError(t, svc.SetChannelKnowledgeBaseID(context.Background(), channel, ""))
	require.Empty(t, channel.KnowledgeBaseID)
	require.NoError(t, svc.SetChannelKnowledgeBaseID(context.Background(), channel, "own-kb"))
	require.Equal(t, "own-kb", channel.KnowledgeBaseID)
}
