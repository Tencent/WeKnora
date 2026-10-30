package memory

import (
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRecallWordSegmentation(t *testing.T) {
	for _, text := range []string{"数，据", "数,据", "数 据", "数\n据", "数1据", "数🙂据", "", "数"} {
		t.Run(text, func(t *testing.T) {
			require.Empty(t, recallTokenSet(text))
		})
	}
	require.Contains(t, recallTokenSet("数据库"), "数据库")
	require.Contains(t, recallTokenSet("连接池配置"), "连接池")
	require.Contains(t, recallTokenSet("数Go据"), "go")
	require.Contains(t, recallTokenSet("POSTGRESQL 17"), "postgresql")
}

func TestRecallSegmentationRejectsCharacterNoise(t *testing.T) {
	cases := []struct {
		name, query, content, topic string
		want                        bool
	}{
		{"continuous_control", "数据", "数据", "", true},
		{"content_boundary", "数据", "数，据", "", false},
		{"topic_boundary", "数据", "", "数，据", false},
		{"field_boundary", "数据", "数", "据", false},
		{"query_boundary", "数，据", "数据", "", false},
		{"shared_characters", "数据库", "用户数量参数配置保存在仓库中", "", false},
		{"partial_term", "连接池", "用户连接公司游泳池", "", false},
		{"technical_term", "连接池", "连接池配置为20", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := &types.MemoryItem{Content: tc.content, Topic: tc.topic, Importance: 3}
			selected := selectRecallItems(tc.query, []*types.MemoryItem{entry}, 8, 2000)
			require.Equal(t, tc.want, len(selected) > 0,
				"query tokens=%v memory tokens=%v", recallTokenSet(tc.query), recallTokenSet(tc.content))
		})
	}
}

func BenchmarkRecallWordSegmentation(b *testing.B) {
	for _, count := range []int{100, 300, 1000} {
		b.Run(fmt.Sprintf("items_%d", count), func(b *testing.B) {
			items := make([]*types.MemoryItem, count)
			for i := range items {
				items[i] = &types.MemoryItem{
					Content: fmt.Sprintf("生产数据库连接池配置为20，服务编号%d", i),
					Topic:   "数据库配置", Importance: 3,
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				selectRecallItems("数据库连接池", items, 8, 2000)
			}
		})
	}
}
