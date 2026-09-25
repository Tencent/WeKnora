package im

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRelocalizeBuiltinChannelAgentNames(t *testing.T) {
	restore := types.OverrideBuiltinAgentEntriesForTest(map[string]*types.BuiltinAgentEntry{
		types.BuiltinQuickAnswerID: {
			ID:     types.BuiltinQuickAnswerID,
			Avatar: "quick.png",
			I18n: map[string]types.BuiltinAgentI18n{
				"default": {Name: "快速问答"},
				"en-US":   {Name: "Quick Answer"},
				"zh-CN":   {Name: "快速问答"},
			},
		},
	})
	t.Cleanup(restore)

	rows := []ChannelWithAgent{
		{AgentID: "custom-1", TenantID: 1, AgentName: "Mine"},
		{AgentID: types.BuiltinQuickAnswerID, TenantID: 1, AgentName: ""},
		{AgentID: types.BuiltinQuickAnswerID, TenantID: 1, AgentName: "frozen-zh"},
	}
	ctx := context.WithValue(context.Background(), types.LanguageContextKey, "en-US")
	relocalizeBuiltinChannelAgentNames(ctx, rows)

	require.Equal(t, "Mine", rows[0].AgentName)
	require.Equal(t, "Quick Answer", rows[1].AgentName)
	require.Equal(t, "Quick Answer", rows[2].AgentName)
}
