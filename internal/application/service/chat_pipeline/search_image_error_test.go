package chatpipeline

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// imageQuerySearchKBService serves both text and image query embeddings and
// records which search path ran. The maps are keyed by KB ID so a test can
// make one target fail while another succeeds.
type imageQuerySearchKBService struct {
	interfaces.KnowledgeBaseService

	kbs       map[string]*types.KnowledgeBase
	keys      map[string]string
	imageErrs map[string]error
	results   []*types.SearchResult

	mu          sync.Mutex
	textCalls   int
	imageCalls  map[string]int
	hybridCalls map[string]int
	params      map[string]types.SearchParams
}

func (s *imageQuerySearchKBService) GetKnowledgeBasesByIDsOnly(
	_ context.Context, ids []string,
) ([]*types.KnowledgeBase, error) {
	out := make([]*types.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		if kb := s.kbs[id]; kb != nil {
			out = append(out, kb)
		}
	}
	return out, nil
}

func (s *imageQuerySearchKBService) ResolveEmbeddingModelKeys(
	context.Context, []*types.KnowledgeBase,
) map[string]string {
	return s.keys
}

func (s *imageQuerySearchKBService) GetQueryEmbedding(
	context.Context, string, string,
) ([]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.textCalls++
	return []float32{1, 2, 3}, nil
}

func (s *imageQuerySearchKBService) GetQueryImageEmbedding(
	_ context.Context, kbID string, _ []byte,
) ([]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.imageCalls == nil {
		s.imageCalls = make(map[string]int)
	}
	s.imageCalls[kbID]++
	if err := s.imageErrs[kbID]; err != nil {
		return nil, err
	}
	return []float32{7, 8, 9}, nil
}

func (s *imageQuerySearchKBService) HybridSearch(
	_ context.Context, id string, params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hybridCalls == nil {
		s.hybridCalls = make(map[string]int)
	}
	if s.params == nil {
		s.params = make(map[string]types.SearchParams)
	}
	s.hybridCalls[id]++
	s.params[id] = params
	return s.results, nil
}

func (s *imageQuerySearchKBService) snapshot() (text int, image, hybrid map[string]int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	image = make(map[string]int, len(s.imageCalls))
	for k, v := range s.imageCalls {
		image[k] = v
	}
	hybrid = make(map[string]int, len(s.hybridCalls))
	for k, v := range s.hybridCalls {
		hybrid[k] = v
	}
	return s.textCalls, image, hybrid
}

func imageSearchKB(id string) *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:               id,
		Type:             types.KnowledgeBaseTypeDocument,
		EmbeddingModelID: "embedding-" + id,
		IndexingStrategy: types.DefaultIndexingStrategy(),
	}
}

// An image query whose embedding fails must not run keyword-only retrieval:
// that would report success for a search the picture never took part in.
func TestSearchImageEmbeddingFailureDoesNotDegradeToKeyword(t *testing.T) {
	rootCause := errors.New("image endpoint unavailable")
	svc := &imageQuerySearchKBService{
		kbs:       map[string]*types.KnowledgeBase{"kb-1": imageSearchKB("kb-1")},
		keys:      map[string]string{"kb-1": "model-1"},
		imageErrs: map[string]error{"kb-1": rootCause},
		results:   []*types.SearchResult{{ID: "keyword-hit", KnowledgeID: "knowledge-1"}},
	}
	plugin := &PluginSearch{knowledgeBaseService: svc}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: "kb-1",
			}},
			EmbeddingTopK: 10,
			QueryImage:    []byte{0x89, 'P', 'N', 'G'},
		},
		PipelineState: types.PipelineState{RewriteQuery: "turbine drawing"},
	}

	err := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH, chatManage, func() *PluginError {
		return nil
	})
	if err == nil || err.ErrorType != ErrSearch.ErrorType {
		t.Fatalf("expected search_failed for an unembeddable image, got %#v", err)
	}
	if !errors.Is(err.Err, rootCause) {
		t.Fatalf("expected the image embedding failure, got %v", err.Err)
	}
	textCalls, imageCalls, hybridCalls := svc.snapshot()
	if textCalls != 0 {
		t.Fatalf("text embedding ran %d times for an image query", textCalls)
	}
	if imageCalls["kb-1"] != 1 {
		t.Fatalf("image embedding calls = %v, want one for kb-1", imageCalls)
	}
	if hybridCalls["kb-1"] != 0 {
		t.Fatalf("keyword fallback ran %d times for a failed image query", hybridCalls["kb-1"])
	}
}

// A failed image group must not stop another model group from returning its
// image-based results.
func TestSearchImageEmbeddingFailureKeepsOtherGroups(t *testing.T) {
	rootCause := errors.New("image endpoint unavailable for kb-bad")
	svc := &imageQuerySearchKBService{
		kbs: map[string]*types.KnowledgeBase{
			"kb-good": imageSearchKB("kb-good"),
			"kb-bad":  imageSearchKB("kb-bad"),
		},
		keys: map[string]string{
			"kb-good": "model-good",
			"kb-bad":  "model-bad",
		},
		imageErrs: map[string]error{"kb-bad": rootCause},
		results:   []*types.SearchResult{{ID: "image-hit", KnowledgeID: "knowledge-good"}},
	}
	plugin := &PluginSearch{knowledgeBaseService: svc}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{
				{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-good"},
				{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-bad"},
			},
			EmbeddingTopK: 10,
			QueryImage:    []byte{0x89, 'P', 'N', 'G'},
		},
		PipelineState: types.PipelineState{RewriteQuery: "turbine drawing"},
	}
	nextCalled := false

	err := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH, chatManage, func() *PluginError {
		nextCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("expected the healthy image group to keep the pipeline alive, got %#v", err)
	}
	if !nextCalled {
		t.Fatal("expected pipeline to continue with the healthy group's results")
	}
	if len(chatManage.SearchResult) != 1 || chatManage.SearchResult[0].ID != "image-hit" {
		t.Fatalf("results = %#v, want the healthy group's image hit", chatManage.SearchResult)
	}
	textCalls, imageCalls, hybridCalls := svc.snapshot()
	if textCalls != 0 {
		t.Fatalf("text embedding ran %d times for an image query", textCalls)
	}
	if imageCalls["kb-good"] != 1 || imageCalls["kb-bad"] != 1 {
		t.Fatalf("image embedding calls = %v, want one per group", imageCalls)
	}
	if hybridCalls["kb-bad"] != 0 {
		t.Fatalf("failed image group ran %d keyword searches", hybridCalls["kb-bad"])
	}
}

// When the model-group keys are unresolved, each target must still get its own
// image embedding instead of falling through to a text-only search.
func TestSearchImageQueryWithoutResolvedModelEmbedsPerTarget(t *testing.T) {
	rootCause := errors.New("image endpoint unavailable for kb-bad")
	svc := &imageQuerySearchKBService{
		kbs: map[string]*types.KnowledgeBase{
			"kb-good": imageSearchKB("kb-good"),
			"kb-bad":  imageSearchKB("kb-bad"),
		},
		keys:      map[string]string{},
		imageErrs: map[string]error{"kb-bad": rootCause},
		results:   []*types.SearchResult{{ID: "image-hit", KnowledgeID: "knowledge-good"}},
	}
	plugin := &PluginSearch{knowledgeBaseService: svc}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{
				{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-good"},
				{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-bad"},
			},
			EmbeddingTopK: 10,
			QueryImage:    []byte{0x89, 'P', 'N', 'G'},
		},
		PipelineState: types.PipelineState{RewriteQuery: "turbine drawing"},
	}

	err := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH, chatManage, func() *PluginError {
		return nil
	})
	if err != nil {
		t.Fatalf("expected the healthy target to keep the pipeline alive, got %#v", err)
	}
	if len(chatManage.SearchResult) != 1 || chatManage.SearchResult[0].ID != "image-hit" {
		t.Fatalf("results = %#v, want the healthy target's image hit", chatManage.SearchResult)
	}
	textCalls, imageCalls, hybridCalls := svc.snapshot()
	if textCalls != 0 {
		t.Fatalf("text embedding ran %d times for an unresolved image group", textCalls)
	}
	if imageCalls["kb-good"] != 1 || imageCalls["kb-bad"] != 1 {
		t.Fatalf("image embedding calls = %v, want one per target", imageCalls)
	}
	if hybridCalls["kb-bad"] != 0 {
		t.Fatalf("failed image target ran %d keyword searches", hybridCalls["kb-bad"])
	}
	if params := svc.params["kb-good"]; len(params.QueryEmbedding) == 0 || params.DisableVectorMatch {
		t.Fatalf("healthy image target searched without its image embedding: %#v", params)
	}
}
