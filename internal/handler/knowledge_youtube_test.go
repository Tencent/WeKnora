package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type youtubeHandlerKnowledge struct {
	interfaces.KnowledgeService
	result  *types.YoutubeIngestResult
	err     error
	gotURLs []string
}

func (s *youtubeHandlerKnowledge) CreateKnowledgeFromYoutube(
	_ context.Context, _ string, urls []string, _ []string, _ string,
) (*types.YoutubeIngestResult, error) {
	s.gotURLs = urls
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

func TestCreateKnowledgeFromYoutubeHandlerSuccess(t *testing.T) {
	kg := &youtubeHandlerKnowledge{result: &types.YoutubeIngestResult{
		SuccessCount: 2,
		Knowledge:    []*types.Knowledge{{ID: "k1"}, {ID: "k2"}},
		Failed:       []types.YoutubeIngestFailure{},
	}}
	h := &KnowledgeHandler{
		kgService: kg,
		kbService: &stubKBService{get: func(context.Context, string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb", TenantID: 7, CreatorID: "user"}, nil
		}},
	}
	r := documentHandlerRouter()
	r.POST("/knowledge-bases/:id/knowledge/youtube", h.CreateKnowledgeFromYoutube)

	w := mutationRequest(r, http.MethodPost, "/knowledge-bases/kb/knowledge/youtube",
		`{"urls":["https://www.youtube.com/watch?v=abc123"]}`)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, []string{"https://www.youtube.com/watch?v=abc123"}, kg.gotURLs)
}

func TestCreateKnowledgeFromYoutubeHandlerRequiresURLs(t *testing.T) {
	kg := &youtubeHandlerKnowledge{result: &types.YoutubeIngestResult{}}
	h := &KnowledgeHandler{
		kgService: kg,
		kbService: &stubKBService{get: func(context.Context, string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb", TenantID: 7, CreatorID: "user"}, nil
		}},
	}
	r := documentHandlerRouter()
	r.POST("/knowledge-bases/:id/knowledge/youtube", h.CreateKnowledgeFromYoutube)

	w := mutationRequest(r, http.MethodPost, "/knowledge-bases/kb/knowledge/youtube", `{"urls":[]}`)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}
