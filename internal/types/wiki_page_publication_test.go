package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 发布边界按值传递；覆盖子上下文不能改变父上下文或原始目标。
func TestWikiPagePublicationContextKeepsValue(t *testing.T) {
	ctx := context.Background()
	_, ok := WikiPagePublicationFromContext(ctx)
	require.False(t, ok)

	target := WikiPagePublication{
		TenantID: 1, KnowledgeBaseID: "kb", PageID: "page", Slug: "entity/page", PageVersion: 1,
	}
	parent := WithWikiPagePublication(ctx, target)
	target.PageVersion = 2
	stored, ok := WikiPagePublicationFromContext(parent)
	require.True(t, ok)
	require.Equal(t, 1, stored.PageVersion)

	child := WithWikiPagePublication(parent, target)
	newer, ok := WikiPagePublicationFromContext(child)
	require.True(t, ok)
	require.Equal(t, 2, newer.PageVersion)
	stored, ok = WikiPagePublicationFromContext(parent)
	require.True(t, ok)
	require.Equal(t, 1, stored.PageVersion)
}
