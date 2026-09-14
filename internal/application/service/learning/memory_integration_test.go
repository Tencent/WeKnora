package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	memoryservice "github.com/Tencent/WeKnora/internal/application/service/memory"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type learningMemoryTenant struct {
	interfaces.TenantRepository
	config *types.MemoryConfig
}

func (r *learningMemoryTenant) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id, MemoryConfig: r.config}, nil
}

func learningPersistentMemory(t *testing.T, f *fixture) (interfaces.MemoryService, *learningMemoryTenant) {
	t.Helper()
	require.NoError(t, f.db.AutoMigrate(
		&types.MemorySubject{}, &types.MemoryItem{}, &types.MemoryTombstone{},
		&types.MemoryTopicStat{}, &types.MemoryDocAffinity{}, &types.MemoryItemEmbedding{},
	))
	tenant := &learningMemoryTenant{config: &types.MemoryConfig{
		Enabled: true, WriteMode: types.MemoryWriteAuto, InterestThreshold: 3,
	}}
	memory := memoryservice.NewMemoryService(repository.NewMemoryRepository(f.db), tenant, nil, nil, nil, nil)
	f.svc = NewService(f.repo, fakeModels{client: f.model}, f.tasks, memory)
	_, err := f.svc.SetEnabled(f.ctx, true)
	require.NoError(t, err)
	return memory, tenant
}

func TestLearningPersistedMemoryChangesCandidateRecallAndRanking(t *testing.T) {
	f := newFixture(t)
	memory, tenant := learningPersistentMemory(t, f)
	// Put the relevant page beyond the cold-start candidate limit. Merely
	// passing an interest to the scorer cannot make this test succeed.
	require.NoError(t, f.db.Model(f.page).Update("id", "ffffffff-ffff-4fff-bfff-ffffffffffff").Error)
	for i := range 64 {
		page := *f.page
		page.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		page.Slug, page.Title = fmt.Sprintf("concept/other-%d", i), fmt.Sprintf("Other topic %d", i)
		require.NoError(t, f.db.Create(&page).Error)
	}
	before, err := f.svc.Recommend(f.ctx, f.kb.ID, 1)
	require.NoError(t, err)
	require.Len(t, before, 1)
	require.NotEqual(t, f.page.ID, before[0].PageID)
	for range 2 {
		require.Empty(t, memory.ObserveQuestionTopics(f.ctx, []string{"Transactions"}))
	}
	require.Equal(t, []string{"Transactions"}, memory.ObserveQuestionTopics(f.ctx, []string{"Transactions"}))
	sources := []types.MemoryDocAffinity{{
		KnowledgeID: f.chunk.KnowledgeID, KnowledgeBaseID: f.kb.ID, Title: "Transaction source",
	}}
	memory.RecordAnswerSources(f.ctx, sources)
	node, err := f.svc.Node(f.ctx, f.page.ID)
	require.NoError(t, err)
	require.False(t, node.Familiar, "one source use is not familiarity")
	memory.RecordAnswerSources(f.ctx, sources)

	// Reconstruct both services to prove the signals came from persisted
	// memory rather than fixture state or a process-local cache.
	memory = memoryservice.NewMemoryService(repository.NewMemoryRepository(f.db), tenant, nil, nil, nil, nil)
	f.svc = NewService(f.repo, fakeModels{client: f.model}, f.tasks, memory)
	after, err := f.svc.Recommend(f.ctx, f.kb.ID, 1)
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.Equal(t, f.page.ID, after[0].PageID)
	require.Equal(t, 1., after[0].Components.InterestMatch)
	require.Contains(t, after[0].ReasonCodes, "interest_match")
	require.True(t, after[0].Familiar)
	require.Equal(t, "unseen", after[0].Mastery.State)
	require.Equal(t, types.LearningInitialMastery, after[0].Mastery.PMastery)
	require.Zero(t, after[0].Mastery.Attempts)
	overlay, err := f.svc.Overlay(f.ctx, f.kb.ID, []string{f.page.Slug})
	require.NoError(t, err)
	require.Len(t, overlay, 1)
	require.True(t, overlay[0].Familiar)
	require.Equal(t, after[0].Mastery, overlay[0].Mastery)
	var count int64
	require.NoError(t, f.db.Model(&types.LearningMastery{}).Count(&count).Error)
	require.Zero(t, count, "memory must not create assessed mastery")
	require.Zero(t, f.model.calls)

	bob := webContext(f.scope.TenantID, "bob")
	_, err = f.svc.SetEnabled(bob, true)
	require.NoError(t, err)
	other, err := f.svc.Recommend(bob, f.kb.ID, 1)
	require.NoError(t, err)
	require.NotEqual(t, f.page.ID, other[0].PageID)
	require.False(t, other[0].Familiar)
	require.Zero(t, other[0].Components.InterestMatch)
	foreign := webContext(f.scope.TenantID+1, "alice")
	require.Empty(t, memory.RetrievalContextFor(foreign).Interests)
	require.Empty(t, memory.DocumentAffinity(foreign, []string{f.chunk.KnowledgeID}))
	_, err = f.svc.SetEnabled(foreign, true)
	require.NoError(t, err)
	_, err = f.svc.Recommend(foreign, f.kb.ID, 1)
	require.ErrorIs(t, err, types.ErrLearningNotFound)
}

func TestLearningPersistedMemorySwitchesReachAgentRecommendations(t *testing.T) {
	f := newFixture(t)
	memory, tenant := learningPersistentMemory(t, f)
	_, err := memory.Remember(f.ctx, types.MemoryItem{
		Kind: types.MemoryKindInterest, Content: "Transactions", Origin: types.MemoryOriginExplicit,
	})
	require.NoError(t, err)
	for range types.MemoryDocAffinityMinHits {
		memory.RecordAnswerSources(f.ctx, []types.MemoryDocAffinity{{
			KnowledgeID: f.chunk.KnowledgeID, KnowledgeBaseID: f.kb.ID, Title: "Transaction source",
		}})
	}
	args, err := json.Marshal(map[string]any{"knowledge_base_id": f.kb.ID, "limit": 1})
	require.NoError(t, err)
	check := func(t *testing.T, ctx context.Context, preference *bool, personalized bool) {
		t.Helper()
		tool := tools.NewLearningTool(tools.ToolRecommendLearningTopics, f.svc, nil, []string{f.kb.ID}, preference)
		result, err := tool.Execute(ctx, args)
		require.NoError(t, err)
		require.True(t, result.Success, result.Error)
		var output struct {
			Recommendations []*types.LearningRecommendation `json:"recommendations"`
		}
		require.NoError(t, json.Unmarshal([]byte(result.Output), &output))
		require.Len(t, output.Recommendations, 1)
		recommendation := output.Recommendations[0]
		require.Equal(t, f.page.ID, recommendation.PageID)
		require.Equal(t, personalized, recommendation.Familiar)
		require.Equal(t, personalized, recommendation.Components.InterestMatch == 1)
		require.Equal(t, "unseen", recommendation.Mastery.State)
		require.Zero(t, recommendation.Mastery.Attempts)
	}
	on, off := true, false
	check(t, f.ctx, nil, true)
	t.Run("agent_opt_out", func(t *testing.T) { check(t, f.ctx, &off, false) })
	t.Run("request_opt_out_cannot_be_overridden", func(t *testing.T) {
		check(t, types.WithMemoryDisabled(f.ctx), &on, false)
	})
	t.Run("retrieval_conditioning_off", func(t *testing.T) {
		tenant.config.RetrievalConditioning = &off
		defer func() { tenant.config.RetrievalConditioning = nil }()
		require.True(t, memory.MemoryAvailable(f.ctx))
		check(t, f.ctx, nil, false)
	})
	t.Run("workspace_off", func(t *testing.T) {
		tenant.config.Enabled = false
		defer func() { tenant.config.Enabled = true }()
		check(t, f.ctx, nil, false)
	})
	t.Run("user_off", func(t *testing.T) {
		require.NoError(t, memory.SetEnabled(f.ctx, false))
		check(t, f.ctx, nil, false)
		require.NoError(t, memory.SetEnabled(f.ctx, true))
	})
	check(t, f.ctx, nil, true)
	t.Run("out_of_scope_tool", func(t *testing.T) {
		tool := tools.NewLearningTool(tools.ToolRecommendLearningTopics, f.svc, nil, []string{"other-kb"}, nil)
		result, err := tool.Execute(f.ctx, args)
		require.NoError(t, err)
		require.False(t, result.Success)
		require.Empty(t, result.Data)
	})
	t.Run("memory_clear", func(t *testing.T) {
		_, err := memory.Clear(f.ctx)
		require.NoError(t, err)
		check(t, f.ctx, nil, false)
		settings, err := f.svc.GetSettings(f.ctx)
		require.NoError(t, err)
		require.True(t, settings.Enabled, "memory and learning consent are independent")
	})
	require.Zero(t, f.model.calls)
}
