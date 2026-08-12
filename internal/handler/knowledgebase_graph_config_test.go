package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestValidateExtractConfigUsesGraphEnabledAsSingleSource(t *testing.T) {
	t.Run("新开关关闭时覆盖旧字段", func(t *testing.T) {
		config := &types.ExtractConfig{Enabled: true}
		require.NoError(t, validateExtractConfig(config, false))
		require.False(t, config.Enabled)
	})

	t.Run("新开关开启时不受旧字段关闭影响", func(t *testing.T) {
		config := &types.ExtractConfig{
			Enabled: false,
			Text:    "示例文本",
			Tags:    []string{"关系"},
			Nodes: []*types.GraphNode{
				{Name: "节点一"},
				{Name: "节点二"},
			},
			Relations: []*types.GraphRelation{
				{Node1: "节点一", Node2: "节点二", Type: "关系"},
			},
		}

		require.NoError(t, validateExtractConfig(config, true))
		require.True(t, config.Enabled)
	})

	t.Run("开启图谱时必须提供抽取参数", func(t *testing.T) {
		require.Error(t, validateExtractConfig(nil, true))
	})
}
