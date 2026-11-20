package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWikiPageWriteSnapshotIgnoresProgressReadCache(t *testing.T) {
	page := &WikiPage{
		ID: "page", TenantID: 1, KnowledgeBaseID: "kb", Slug: "entity/shared", Version: 1,
		Content: "stored body", SourceRefs: StringArray{"source"}, ChunkRefs: StringArray{"chunk"},
	}
	snapshot, err := WikiPageProgressSnapshot(page)
	require.NoError(t, err)
	page.ProgressSnapshot = snapshot
	ctx := WithWikiPageWriteSnapshot(context.Background(), page)
	expected, guarded := WikiPageWriteSnapshotFromContext(ctx)
	require.True(t, guarded)

	// 仓储公开读取填充进度快照，事务内直接读取不填充；同一数据库行仍应匹配。
	stored := cloneWikiPageWriteSnapshot(page)
	stored.ProgressSnapshot = ""
	require.True(t, WikiPageWriteSnapshotMatches(expected, stored))
	require.True(t, WikiPageWriteSnapshotMatches(stored, expected))

	// 匹配只规范化副本，不能清掉后续进度提交仍需使用的原始读取快照。
	require.Equal(t, snapshot, page.ProgressSnapshot)
	require.Equal(t, snapshot, expected.ProgressSnapshot)
	require.Empty(t, stored.ProgressSnapshot)
}

func TestWikiPageWriteSnapshotProgressCacheDoesNotHidePersistentChanges(t *testing.T) {
	page := &WikiPage{
		ID: "page", TenantID: 1, KnowledgeBaseID: "kb", Slug: "entity/shared", Version: 1,
		Content: "stored body", Status: WikiPageStatusDraft,
		SourceRefs: StringArray{"source"}, ChunkRefs: StringArray{"chunk"},
		PageMetadata: JSON(`{"category":"old"}`),
	}
	snapshot, err := WikiPageProgressSnapshot(page)
	require.NoError(t, err)
	page.ProgressSnapshot = snapshot
	cases := []struct {
		name   string
		change func(*WikiPage)
	}{
		{"content", func(p *WikiPage) { p.Content = "concurrent body" }},
		{"source references", func(p *WikiPage) { p.SourceRefs = append(p.SourceRefs, "other-source") }},
		{"chunk references", func(p *WikiPage) { p.ChunkRefs = append(p.ChunkRefs, "other-chunk") }},
		{"publication status", func(p *WikiPage) { p.Status = WikiPageStatusPublished }},
		{"metadata", func(p *WikiPage) { p.PageMetadata = JSON(`{"category":"new"}`) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := cloneWikiPageWriteSnapshot(page)
			tc.change(current)
			// 非持久缓存即使相同，也不能掩盖正文或同版本元数据的实际变化。
			require.Equal(t, page.Version, current.Version)
			require.Equal(t, page.ProgressSnapshot, current.ProgressSnapshot)
			require.False(t, WikiPageWriteSnapshotMatches(page, current))
			freshSnapshot, err := WikiPageProgressSnapshot(current)
			require.NoError(t, err)
			require.NotEqual(t, snapshot, freshSnapshot)
		})
	}
}
