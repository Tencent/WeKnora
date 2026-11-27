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
		Claim: WikiProgressClaim{PendingOpID: 1, Epoch: 1},
	}
	parent := WithWikiPagePublication(ctx, target)
	target.PageVersion = 2
	target.Claim.Epoch = 2
	stored, ok := WikiPagePublicationFromContext(parent)
	require.True(t, ok)
	require.Equal(t, 1, stored.PageVersion)
	require.Equal(t, int64(1), stored.Claim.Epoch)

	child := WithWikiPagePublication(parent, target)
	newer, ok := WikiPagePublicationFromContext(child)
	require.True(t, ok)
	require.Equal(t, 2, newer.PageVersion)
	stored, ok = WikiPagePublicationFromContext(parent)
	require.True(t, ok)
	require.Equal(t, 1, stored.PageVersion)
}

// 调用方改切片不能替换既定领取；显式空授权必须与普通未提供上下文区分。
func TestWikiProgressHandoffContextKeepsClaimsAndExplicitEmpty(t *testing.T) {
	ctx := context.Background()
	_, guarded := WikiProgressClaimsFromContext(ctx)
	require.False(t, guarded)
	claims := []WikiProgressClaim{{PendingOpID: 1, Epoch: 1}}
	parent := WithWikiProgressClaims(ctx, claims)
	claims[0].Epoch = 2
	stored, guarded := WikiProgressClaimsFromContext(parent)
	require.True(t, guarded)
	require.Equal(t, int64(1), stored[0].Epoch)
	stored, guarded = WikiProgressClaimsFromContext(WithWikiProgressClaims(parent, nil))
	require.True(t, guarded)
	require.Empty(t, stored)
}
