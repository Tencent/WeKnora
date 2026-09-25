package memory

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func item(kind, content string, importance int) *types.MemoryItem {
	return &types.MemoryItem{
		Kind:          kind,
		Content:       content,
		NormalizedKey: types.NormalizeMemoryKey("", content),
		Importance:    importance,
		ValidFrom:     time.Now(),
	}
}

func topicItem(kind, topic, content string, importance int) *types.MemoryItem {
	entry := item(kind, content, importance)
	entry.Topic = topic
	entry.NormalizedKey = types.NormalizeMemoryKey(topic, content)
	return entry
}

// TestTopicIsPartOfTheRetrievalHandle covers the common shape of an extracted
// memory: the question names the subject while the statement carries only the
// value, so matching on the statement alone would miss it.
func TestTopicIsPartOfTheRetrievalHandle(t *testing.T) {
	items := []*types.MemoryItem{
		topicItem(types.MemoryKindFact, "在用的数据库", "已经从 MySQL 迁到 PostgreSQL", 3),
		topicItem(types.MemoryKindFact, "前端技术栈", "用的是 Vue 3 加 Vite", 3),
	}
	selected := selectRecallItems("写一段连接数据库的示例代码", items,
		types.MemoryRecallMaxItems, types.MemoryRecallRuneBudget)
	require.NotEmpty(t, selected)
	require.Equal(t, "已经从 MySQL 迁到 PostgreSQL", selected[0].Content)
	require.NotContains(t, contents(selected), "用的是 Vue 3 加 Vite")
}

func contents(items []*types.MemoryItem) []string {
	out := make([]string, 0, len(items))
	for _, entry := range items {
		out = append(out, entry.Content)
	}
	return out
}

func TestSelectRecallItemsRanksChineseByTopic(t *testing.T) {
	items := []*types.MemoryItem{
		item(types.MemoryKindFact, "生产数据库是 PostgreSQL 17", 3),
		item(types.MemoryKindFact, "前端框架用的是 Vue 3", 3),
		item(types.MemoryKindTask, "在重构订单服务的支付流程", 3),
	}
	selected := selectRecallItems("数据库最近连接超时，怎么排查", items,
		types.MemoryRecallMaxItems, types.MemoryRecallRuneBudget)
	require.NotEmpty(t, selected)
	require.Equal(t, "生产数据库是 PostgreSQL 17", selected[0].Content)
	require.NotContains(t, contents(selected), "前端框架用的是 Vue 3")
}

func TestSelectRecallItemsMatchesEnglish(t *testing.T) {
	items := []*types.MemoryItem{
		item(types.MemoryKindFact, "The staging cluster runs in Frankfurt", 3),
		item(types.MemoryKindFact, "CI is GitHub Actions on self-hosted runners", 3),
	}
	selected := selectRecallItems("where is the staging cluster deployed", items,
		types.MemoryRecallMaxItems, types.MemoryRecallRuneBudget)
	require.NotEmpty(t, selected)
	require.Equal(t, "The staging cluster runs in Frankfurt", selected[0].Content)
}

func TestSelectRecallItemsDropsWeakMatches(t *testing.T) {
	items := []*types.MemoryItem{
		item(types.MemoryKindFact, "我平时用 Go 写服务", 3),
	}
	// "的" and similar filler share nothing meaningful with the memory: a
	// weakly related memory must not be injected at all.
	require.Empty(t, selectRecallItems("今天天气怎么样", items,
		types.MemoryRecallMaxItems, types.MemoryRecallRuneBudget))
}

func TestSelectRecallItemsRespectsCountBudget(t *testing.T) {
	var items []*types.MemoryItem
	for i := 0; i < 20; i++ {
		items = append(items, item(types.MemoryKindFact, "数据库相关的事实 "+string(rune('a'+i)), 3))
	}
	selected := selectRecallItems("数据库", items, types.MemoryRecallMaxItems, types.MemoryRecallRuneBudget)
	require.LessOrEqual(t, len(selected), types.MemoryRecallMaxItems)
}

func TestSelectRecallItemsRespectsRuneBudget(t *testing.T) {
	var items []*types.MemoryItem
	for i := 0; i < 5; i++ {
		long := "数据库"
		for j := 0; j < 60; j++ {
			long += "很长的说明"
		}
		items = append(items, item(types.MemoryKindFact, long, 3))
	}
	selected := selectRecallItems("数据库", items, types.MemoryRecallMaxItems, types.MemoryRecallRuneBudget)
	total := 0
	for _, entry := range selected {
		total += len([]rune(entry.Content))
	}
	require.LessOrEqual(t, total, types.MemoryRecallRuneBudget)
}

func TestSelectRecallItemsEmptyQuery(t *testing.T) {
	items := []*types.MemoryItem{item(types.MemoryKindFact, "任何事实", 3)}
	require.Empty(t, selectRecallItems("   ", items,
		types.MemoryRecallMaxItems, types.MemoryRecallRuneBudget))
}

func TestBigramsBeatSingleCharacterNoise(t *testing.T) {
	// "数" alone appears in 数据, 参数, 数量 — a single ideograph must not be
	// enough to pull an unrelated memory into context.
	items := []*types.MemoryItem{
		item(types.MemoryKindFact, "参数校验统一放在 handler 层", 3),
		item(types.MemoryKindFact, "数据迁移脚本放在 migrations 目录", 3),
	}
	selected := selectRecallItems("数据迁移脚本放在哪", items,
		types.MemoryRecallMaxItems, types.MemoryRecallRuneBudget)
	require.NotEmpty(t, selected)
	require.Equal(t, "数据迁移脚本放在 migrations 目录", selected[0].Content)
}
