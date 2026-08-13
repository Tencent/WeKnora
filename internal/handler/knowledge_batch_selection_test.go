package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubResolveKGService struct {
	interfaces.KnowledgeService

	filterIDs   []string
	filterErr   error
	batchByID   map[string]*types.Knowledge
	lastFilter  types.KnowledgeListFilter
}

func (s *stubResolveKGService) ListKnowledgeIDsByFilter(
	_ context.Context, _ string, filter types.KnowledgeListFilter,
) ([]string, error) {
	s.lastFilter = filter
	if s.filterErr != nil {
		return nil, s.filterErr
	}
	out := make([]string, len(s.filterIDs))
	copy(out, s.filterIDs)
	return out, nil
}

func (s *stubResolveKGService) GetKnowledgeBatch(
	_ context.Context, _ uint64, ids []string,
) ([]*types.Knowledge, error) {
	out := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		if k, ok := s.batchByID[id]; ok {
			out = append(out, k)
		}
	}
	return out, nil
}

func TestResolveBatchKnowledgeIDs_ExplicitMode(t *testing.T) {
	kg := &stubResolveKGService{
		batchByID: map[string]*types.Knowledge{
			"a": {ID: "a", KnowledgeBaseID: "kb-1"},
			"b": {ID: "b", KnowledgeBaseID: "kb-1"},
		},
	}
	h := &KnowledgeHandler{kgService: kg}
	ctx := context.Background()

	resolved, err := h.resolveBatchKnowledgeIDs(
		ctx, 1, "kb-1", knowledgeBatchSelection{}, []string{"a", "b", "a", " "}, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got := strings.Join(resolved.IDs, ","); got != "a,b" {
		t.Fatalf("ids=%q want a,b", got)
	}

	tooMany := make([]string, maxBatchExplicitIDs+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("id-%d", i)
	}
	_, err = h.resolveBatchKnowledgeIDs(ctx, 1, "kb-1", knowledgeBatchSelection{}, tooMany, "")
	if err == nil {
		t.Fatal("expected too-many error")
	}
	if appErr, ok := errors.IsAppError(err); !ok || appErr.Code != errors.ErrBadRequest {
		t.Fatalf("want bad request, got %v", err)
	}
}

func TestResolveBatchKnowledgeIDs_SelectAllExclude(t *testing.T) {
	kg := &stubResolveKGService{filterIDs: []string{"a", "b", "c"}}
	h := &KnowledgeHandler{kgService: kg}
	ctx := context.Background()

	folder := "docs"
	resolved, err := h.resolveBatchKnowledgeIDs(ctx, 1, "kb-1", knowledgeBatchSelection{
		SelectAll:  true,
		ExcludeIDs: []string{"b", "b"},
		Filter: knowledgeBatchFilter{
			Keyword:         "x",
			FolderPath:      &folder,
			FolderRecursive: true,
		},
	}, nil, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got := strings.Join(resolved.IDs, ","); got != "a,c" {
		t.Fatalf("ids=%q want a,c", got)
	}
	if resolved.MatchedCount != 3 || resolved.ExcludedCount != 1 {
		t.Fatalf("matched=%d excluded=%d", resolved.MatchedCount, resolved.ExcludedCount)
	}
	if kg.lastFilter.Keyword != "x" || kg.lastFilter.FolderScope != types.FolderScopeSubtree {
		t.Fatalf("filter not applied: %+v", kg.lastFilter)
	}

	_, err = h.resolveBatchKnowledgeIDs(ctx, 1, "kb-1", knowledgeBatchSelection{
		SelectAll:  true,
		ExcludeIDs: []string{"a", "b", "c"},
	}, nil, "")
	if err == nil {
		t.Fatal("expected empty-match error")
	}
}

func TestResolveBatchKnowledgeIDs_SelectAllCap(t *testing.T) {
	ids := make([]string, maxSelectAllMatched+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("id-%d", i)
	}
	h := &KnowledgeHandler{kgService: &stubResolveKGService{filterIDs: ids}}
	_, err := h.resolveBatchKnowledgeIDs(context.Background(), 1, "kb-1", knowledgeBatchSelection{
		SelectAll: true,
	}, nil, "")
	if err == nil {
		t.Fatal("expected maxSelectAll error")
	}
	if appErr, ok := errors.IsAppError(err); !ok || !strings.Contains(appErr.Message, "select_all") {
		t.Fatalf("want select_all cap error, got %v", err)
	}
}

func TestChunkStringIDs(t *testing.T) {
	chunks := chunkStringIDs([]string{"a", "b", "c", "d", "e"}, 2)
	if len(chunks) != 3 {
		t.Fatalf("len=%d want 3", len(chunks))
	}
	if strings.Join(chunks[2], ",") != "e" {
		t.Fatalf("last chunk=%v", chunks[2])
	}
}
