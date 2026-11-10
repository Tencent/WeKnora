package chatpipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// tenantScopedChunkRepo makes the tenant-scoped lookups behave like the SQL
// repository (WHERE tenant_id = ?), while the *Only lookups stay unscoped.
// With the pre-#3342 code every parent fetch went through the scoped variant,
// so shared-KB parents (owned by the sharing workspace) never resolved.
type tenantScopedChunkRepo struct {
	*expandChunkRepo
}

func filterByTenant(chunks []*types.Chunk, tenantID uint64) []*types.Chunk {
	out := make([]*types.Chunk, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk.TenantID == tenantID {
			out = append(out, chunk)
		}
	}
	return out
}

func (r *tenantScopedChunkRepo) ListChunksByID(
	ctx context.Context, tenantID uint64, ids []string,
) ([]*types.Chunk, error) {
	all, err := r.expandChunkRepo.ListChunksByID(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	return filterByTenant(all, tenantID), nil
}

func (r *tenantScopedChunkRepo) ListChunksByParentIDs(
	ctx context.Context, tenantID uint64, parentIDs []string,
) ([]*types.Chunk, error) {
	all, err := r.expandChunkRepo.ListChunksByParentIDs(ctx, tenantID, parentIDs)
	if err != nil {
		return nil, err
	}
	return filterByTenant(all, tenantID), nil
}

// TestResolveParentChunksResolvesSharedKBParentsAcrossTenants pins the merge
// half of #3342: retrieval hits from an org-shared KB belong to the sharing
// workspace, and the caller-tenant filter made both the parent_text context
// and the image_info enrichment invisible for those hits.
func TestResolveParentChunksResolvesSharedKBParentsAcrossTenants(t *testing.T) {
	imageInfo, err := json.Marshal([]types.ImageInfo{{URL: "resource://sharedImage", OCRText: "shared image text"}})
	if err != nil {
		t.Fatal(err)
	}
	// All chunks belong to workspace 2; the caller is workspace 1.
	repo := &tenantScopedChunkRepo{expandChunkRepo: &expandChunkRepo{
		chunks: map[string]*types.Chunk{
			"parent": {
				ID: "parent", TenantID: 2, ChunkType: types.ChunkTypeParentText,
				Content: "shared parent body with ![img](resource://sharedImage)",
			},
			"child": {
				ID: "child", TenantID: 2, ChunkType: types.ChunkTypeText,
				ParentChunkID: "parent", Content: "shared child body",
			},
		},
		children: map[string][]*types.Chunk{
			"child": {
				{
					ID: "imageChild", TenantID: 2, ParentChunkID: "child",
					ChunkType: types.ChunkTypeImageOCR, ImageInfo: string(imageInfo), IsEnabled: true,
				},
			},
		},
	}}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{
		ID: "child", KnowledgeID: "doc", ChunkType: string(types.ChunkTypeText),
		ParentChunkID: "parent", Content: "shared child body",
	}

	got := plugin.resolveParentChunks(ctx, &types.ChatManage{}, []*types.SearchResult{result})
	if len(got) != 1 {
		t.Fatalf("result count = %d, want 1", len(got))
	}
	if !strings.Contains(got[0].Content, "shared parent body") {
		t.Fatalf("cross-tenant parent content was not resolved: %q", got[0].Content)
	}
	if !strings.Contains(got[0].ImageInfo, "resource://sharedImage") {
		t.Fatalf("cross-tenant image_info was not enriched: %q", got[0].ImageInfo)
	}
}
