package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMoveKnowledgeReuseVectors_CrossStoreRejected verifies the defense-in-depth
// guard: a reuse_vectors move whose source and target KBs are bound to different
// vector stores must be rejected before any index copy runs. The guard fires at
// the top of moveKnowledgeReuseVectors, so an empty knowledgeService (no repos)
// is sufficient — the function returns before touching any dependency.
func TestMoveKnowledgeReuseVectors_CrossStoreRejected(t *testing.T) {
	s := &knowledgeService{}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	storeA, storeB := "store-a", "store-b"
	tests := []struct {
		name     string
		srcStore *string
		dstStore *string
	}{
		{"both bound, different stores", &storeA, &storeB},
		{"source bound, target env-store", &storeA, nil},
		{"source env-store, target bound", nil, &storeB},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &types.KnowledgeBase{ID: "kb-src", VectorStoreID: tt.srcStore}
			dst := &types.KnowledgeBase{ID: "kb-dst", VectorStoreID: tt.dstStore}
			kn := &types.Knowledge{ID: "k1", EmbeddingModelID: "m1"}

			err := s.moveKnowledgeReuseVectors(ctx, kn, src, dst)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "different vector stores")
		})
	}
}

// TestSharesStoreWith_GuardSemantics pins the same/cross-store classification the
// move gate relies on, including the env-store (nil) normalization, so a change
// to SharesStoreWith that would silently re-open the cross-store reuse_vectors
// hole fails here.
func TestSharesStoreWith_GuardSemantics(t *testing.T) {
	storeA, storeB, empty := "store-a", "store-b", ""

	same := func(a, b *string) bool {
		return (&types.KnowledgeBase{VectorStoreID: a}).
			SharesStoreWith(&types.KnowledgeBase{VectorStoreID: b})
	}

	assert.True(t, same(nil, nil), "both env-store → same")
	assert.True(t, same(&empty, nil), "empty string normalizes to env-store")
	assert.True(t, same(&storeA, &storeA), "same UUID → same")
	assert.False(t, same(&storeA, &storeB), "different UUID → cross-store")
	assert.False(t, same(&storeA, nil), "bound vs env-store → cross-store")
}
