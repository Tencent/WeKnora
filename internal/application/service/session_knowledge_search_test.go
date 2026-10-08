package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type searchTenantService struct {
	interfaces.TenantService
	rc *types.RetrievalConfig
}

func (s *searchTenantService) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: 1, RetrievalConfig: s.rc}, nil
}

type searchKBService struct {
	interfaces.KnowledgeBaseService
}

func (s *searchKBService) GetKnowledgeBasesByIDsOnly(_ context.Context, ids []string) ([]*types.KnowledgeBase, error) {
	kbs := make([]*types.KnowledgeBase, len(ids))
	for i, id := range ids {
		kbs[i] = &types.KnowledgeBase{ID: id, TenantID: 1}
	}
	return kbs, nil
}

// fakeRetrieval stands in for CHUNK_SEARCH and CHUNK_MERGE: it records the
// pipeline request it saw and serves canned retrieval results.
type fakeRetrieval struct {
	results []*types.SearchResult
	seen    types.PipelineRequest
}

func (f *fakeRetrieval) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_SEARCH, types.CHUNK_MERGE}
}

func (f *fakeRetrieval) OnEvent(
	_ context.Context, event types.EventType, cm *types.ChatManage, next func() *chatpipeline.PluginError,
) *chatpipeline.PluginError {
	switch event {
	case types.CHUNK_SEARCH:
		f.seen = cm.PipelineRequest
		if len(f.results) == 0 {
			return chatpipeline.ErrSearchNothing
		}
		cm.SearchResult = f.results
	case types.CHUNK_MERGE:
		cm.MergeResult = cm.RerankResult
		if len(cm.MergeResult) == 0 {
			cm.MergeResult = cm.SearchResult
		}
	}
	return next()
}

func newSearchKnowledgeService(
	t *testing.T, rc *types.RetrievalConfig, models *rerankModelService, retrieval *fakeRetrieval,
) *sessionService {
	t.Helper()
	events := chatpipeline.NewEventManager()
	events.Register(retrieval)
	chatpipeline.NewPluginRerank(events, models)
	chatpipeline.NewPluginFilterTopK(events)
	return &sessionService{
		cfg:                  &config.Config{Conversation: &config.ConversationConfig{}},
		tenantService:        &searchTenantService{rc: rc},
		knowledgeBaseService: &searchKBService{},
		modelService:         models,
		eventManager:         events,
	}
}

func searchKnowledgeCtx() context.Context {
	return types.WithCaller(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1)),
		types.Caller{TenantID: 1, UserID: "u-1"})
}

func threeHits() []*types.SearchResult {
	return []*types.SearchResult{
		{ID: "c1", Content: "first", Score: 0.9, KnowledgeID: "k1"},
		{ID: "c2", Content: "second", Score: 0.8, KnowledgeID: "k2"},
		{ID: "c3", Content: "third", Score: 0.7, KnowledgeID: "k3"},
	}
}

func TestSearchKnowledge_defaultRerankReportsTenantModel(t *testing.T) {
	models := newRerankModelService()
	models.reranker = &scoredReranker{scores: []float64{0.1, 0.9, 0.6}}
	retrieval := &fakeRetrieval{results: threeHits()}
	svc := newSearchKnowledgeService(t, &types.RetrievalConfig{RerankModelID: "rr-1", RerankThreshold: 0.3},
		models, retrieval)

	got, err := svc.SearchKnowledge(searchKnowledgeCtx(), []string{"kb-1"}, nil, nil, "q", nil)
	require.NoError(t, err)

	require.Len(t, got.Results, 2)
	assert.Equal(t, "c2", got.Results[0].ID)
	d := got.Meta.Rerank
	require.NotNil(t, d)
	assert.Equal(t, types.RerankOutcomeOK, d.Outcome)
	assert.Equal(t, "rr-1", d.ModelID)
	assert.Equal(t, types.RerankModelSourceTenant, d.ModelSource)
	assert.Equal(t, 3, d.CandidateCount)
	assert.Equal(t, 2, d.ResultCount)
}

func TestSearchKnowledge_overridesReachThePipeline(t *testing.T) {
	models := newRerankModelService()
	retrieval := &fakeRetrieval{results: threeHits()}
	svc := newSearchKnowledgeService(t, nil, models, retrieval)
	vector := 0.42
	disabled := false

	got, err := svc.SearchKnowledge(searchKnowledgeCtx(), []string{"kb-1"}, nil, nil, "q",
		&types.KnowledgeSearchOptions{
			VectorThreshold: &vector, MatchCount: 2, DisableKeywordsMatch: true,
			Rerank: &types.RerankOptions{Enabled: &disabled},
		})
	require.NoError(t, err)

	assert.Equal(t, 0.42, retrieval.seen.VectorThreshold)
	assert.True(t, retrieval.seen.DisableKeywordsMatch)
	assert.Empty(t, retrieval.seen.RerankModelID, "a disabled rerank must not reach the pipeline")
	require.Len(t, got.Results, 2, "match_count cuts the final results")
	assert.Equal(t, types.RerankOutcomeDisabled, got.Meta.Rerank.Outcome)
}

func TestSearchKnowledge_explainsEmptyResults(t *testing.T) {
	models := newRerankModelService()
	models.reranker = &scoredReranker{scores: []float64{0.01, 0.02, 0.03}}
	svc := newSearchKnowledgeService(t, nil, models, &fakeRetrieval{results: threeHits()})

	got, err := svc.SearchKnowledge(searchKnowledgeCtx(), []string{"kb-1"}, nil, nil, "q",
		&types.KnowledgeSearchOptions{Rerank: &types.RerankOptions{ModelID: "rr-1"}})
	require.NoError(t, err)
	assert.Empty(t, got.Results)
	assert.Equal(t, types.RerankOutcomeAllBelowThreshold, got.Meta.Rerank.Outcome)
	assert.Equal(t, types.RerankModelSourceRequest, got.Meta.Rerank.ModelSource)
	assert.InDelta(t, 0.03, got.Meta.Rerank.TopScore, 1e-9)

	svc = newSearchKnowledgeService(t, nil, models, &fakeRetrieval{})
	got, err = svc.SearchKnowledge(searchKnowledgeCtx(), []string{"kb-1"}, nil, nil, "q", nil)
	require.NoError(t, err)
	assert.Empty(t, got.Results)
	assert.Equal(t, types.RerankOutcomeNoCandidates, got.Meta.Rerank.Outcome)
}

func TestSearchKnowledge_unavailableModelDegradesToRetrievalOrder(t *testing.T) {
	models := newRerankModelService()
	models.loadErr = errors.New("bad credentials")
	svc := newSearchKnowledgeService(t, &types.RetrievalConfig{RerankModelID: "rr-1"}, models,
		&fakeRetrieval{results: threeHits()})

	got, err := svc.SearchKnowledge(searchKnowledgeCtx(), []string{"kb-1"}, nil, nil, "q", nil)
	require.NoError(t, err)
	require.Len(t, got.Results, 3)
	assert.Equal(t, "c1", got.Results[0].ID)
	d := got.Meta.Rerank
	assert.Equal(t, types.RerankOutcomeModelUnavailable, d.Outcome)
	assert.Equal(t, "bad credentials", d.Error)
	assert.Equal(t, types.RerankModelSourceTenant, d.ModelSource)
}

func TestSearchKnowledge_rejectsUnknownRequestedModel(t *testing.T) {
	svc := newSearchKnowledgeService(t, nil, newRerankModelService(), &fakeRetrieval{results: threeHits()})
	_, err := svc.SearchKnowledge(searchKnowledgeCtx(), []string{"kb-1"}, nil, nil, "q",
		&types.KnowledgeSearchOptions{Rerank: &types.RerankOptions{ModelID: "missing"}})
	require.Error(t, err)
}
