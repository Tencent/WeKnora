package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type reparseChunkRepo struct {
	interfaces.ChunkRepository
	chunks []*types.Chunk
}

func (r *reparseChunkRepo) ListChunksByKnowledgeID(
	context.Context, uint64, string,
) ([]*types.Chunk, error) {
	return r.chunks, nil
}

type reparseWikiRepo struct {
	interfaces.WikiPageRepository
	pages []*types.WikiPage
}

func (r *reparseWikiRepo) ListBySourceRef(
	context.Context, string, string,
) ([]*types.WikiPage, error) {
	return r.pages, nil
}

type reparseWikiService struct {
	interfaces.WikiPageService
	updated []*types.WikiPage
}

func (s *reparseWikiService) UpdatePageMeta(_ context.Context, page *types.WikiPage) error {
	snapshot := *page
	snapshot.SourceRefs = append(types.StringArray(nil), page.SourceRefs...)
	snapshot.ChunkRefs = append(types.StringArray(nil), page.ChunkRefs...)
	s.updated = append(s.updated, &snapshot)
	return nil
}

func TestPrepareWikiForReparse_RemovesOnlyReparsedKnowledgeChunkRefs(t *testing.T) {
	page := &types.WikiPage{
		Slug:       "entity/example",
		SourceRefs: types.StringArray{"knowledge-1", "knowledge-2"},
		ChunkRefs:  types.StringArray{"chunk-old-1", "chunk-other-source"},
	}
	wikiService := &reparseWikiService{}
	svc := &knowledgeService{
		chunkRepo: &reparseChunkRepo{chunks: []*types.Chunk{
			{ID: "chunk-old-1", KnowledgeID: "knowledge-1"},
		}},
		wikiRepo:    &reparseWikiRepo{pages: []*types.WikiPage{page}},
		wikiService: wikiService,
	}

	svc.prepareWikiForReparse(context.Background(), &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
	})

	if len(wikiService.updated) != 1 {
		t.Fatalf("expected one wiki page metadata update, got %d", len(wikiService.updated))
	}
	got := wikiService.updated[0]
	wantChunkRefs := types.StringArray{"chunk-other-source"}
	if !reflect.DeepEqual(got.ChunkRefs, wantChunkRefs) {
		t.Fatalf("unexpected chunk refs: got %v, want %v", got.ChunkRefs, wantChunkRefs)
	}
	wantSourceRefs := types.StringArray{"knowledge-1", "knowledge-2"}
	if !reflect.DeepEqual(got.SourceRefs, wantSourceRefs) {
		t.Fatalf("source refs changed during reparse: got %v, want %v", got.SourceRefs, wantSourceRefs)
	}
}
