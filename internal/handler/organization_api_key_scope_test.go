package handler

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestFilterSharedKnowledgeBasesForAPIKeyScope(t *testing.T) {
	sharedKBs := []*types.SharedKnowledgeBaseInfo{
		{KnowledgeBase: &types.KnowledgeBase{ID: "kb-allowed"}},
		{KnowledgeBase: &types.KnowledgeBase{ID: "kb-blocked"}},
		nil,
		{},
	}

	t.Run("JWT callers keep the complete list", func(t *testing.T) {
		assert.Equal(t, sharedKBs, filterSharedKnowledgeBasesForAPIKeyScope(context.Background(), sharedKBs))
	})

	t.Run("unrestricted API keys keep the complete list", func(t *testing.T) {
		ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{KeyID: 1})
		assert.Equal(t, sharedKBs, filterSharedKnowledgeBasesForAPIKeyScope(ctx, sharedKBs))
	})

	t.Run("KB-restricted API keys only see allowed shared KBs", func(t *testing.T) {
		ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
			KeyID:            1,
			KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
		})
		assert.Equal(t, sharedKBs[:1], filterSharedKnowledgeBasesForAPIKeyScope(ctx, sharedKBs))
	})
}
