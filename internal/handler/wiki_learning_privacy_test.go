package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type learningGraphWikiStub struct {
	interfaces.WikiPageService
	request *types.WikiGraphRequest
}

func (s *learningGraphWikiStub) GetGraph(
	_ context.Context, request *types.WikiGraphRequest,
) (*types.WikiGraphData, error) {
	s.request = request
	return &types.WikiGraphData{}, nil
}

type learningGraphKBStub struct {
	interfaces.KnowledgeBaseService
}

func (learningGraphKBStub) GetKnowledgeBaseByID(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID:               "kb-1",
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
	}, nil
}

type learningGraphMemoryStub struct {
	interfaces.MemoryService
	available     bool
	learningCalls int
}

func (s *learningGraphMemoryStub) MemoryAvailable(context.Context) bool { return s.available }

func (s *learningGraphMemoryStub) LearningDocuments(
	context.Context, int,
) ([]*types.MemoryDocView, error) {
	s.learningCalls++
	return []*types.MemoryDocView{{KnowledgeID: "doc-1", Hits: 2}}, nil
}

func invokeLearningGraph(t *testing.T, memory *learningGraphMemoryStub) *learningGraphWikiStub {
	t.Helper()
	gin.SetMode(gin.TestMode)
	wiki := &learningGraphWikiStub{}
	handler := NewWikiPageHandler(wiki, learningGraphKBStub{}, nil, nil, memory)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "kb_id", Value: "kb-1"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/wiki/graph", nil)

	handler.GetGraph(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, wiki.request)
	return wiki
}

func TestWikiGraphDoesNotLoadLearningProfileAfterPersonalOptOut(t *testing.T) {
	memory := &learningGraphMemoryStub{available: false}
	wiki := invokeLearningGraph(t, memory)

	require.Zero(t, memory.learningCalls)
	require.Nil(t, wiki.request.LearningDocuments)
	require.Empty(t, wiki.request.FamiliarKnowledgeIDs)
}

func TestWikiGraphLoadsLearningProfileWhenMemoryIsAvailable(t *testing.T) {
	memory := &learningGraphMemoryStub{available: true}
	wiki := invokeLearningGraph(t, memory)

	require.Equal(t, 1, memory.learningCalls)
	require.Len(t, wiki.request.LearningDocuments, 1)
	require.Equal(t, []string{"doc-1"}, wiki.request.FamiliarKnowledgeIDs)
}
