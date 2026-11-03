package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The suite covers the shared-agent read path for starter questions
// (issue #2957): tenant 2 owns the agent and shares it with tenant 1;
// tenant 1 has no own copy of the agent.

// sharedSuggestionAgentRepo mimics the tenant-scoped custom agent
// repository: GetAgentByID only resolves the agent under its own tenant.
type sharedSuggestionAgentRepo struct {
	interfaces.CustomAgentRepository
	agent          *types.CustomAgent
	lookupByTenant []uint64
}

func (r *sharedSuggestionAgentRepo) GetAgentByID(
	_ context.Context, _ string, tenantID uint64,
) (*types.CustomAgent, error) {
	r.lookupByTenant = append(r.lookupByTenant, tenantID)
	if r.agent != nil && r.agent.TenantID == tenantID {
		return r.agent, nil
	}
	return nil, repository.ErrCustomAgentNotFound
}

// sharedSuggestionShareRepo returns the share that tenant 1 can access.
type sharedSuggestionShareRepo struct {
	interfaces.AgentShareRepository
	share *types.AgentShare
}

func (r *sharedSuggestionShareRepo) GetShareByAgentIDForTenant(
	_ context.Context, tenantID uint64, _ string, excludeTenantID uint64,
) (*types.AgentShare, error) {
	if r.share == nil ||
		r.share.SourceTenantID == tenantID ||
		r.share.SourceTenantID == excludeTenantID {
		return nil, repository.ErrAgentShareNotFound
	}
	return r.share, nil
}

// sharedSuggestionChunkRepo records how the suggestion pipeline queries
// chunks so tests can assert the effective tenant and KB scope.
type sharedSuggestionChunkRepo struct {
	interfaces.ChunkRepository
	faqTenants []uint64
	faqKBIDs   [][]string
	docTenants []uint64
	docKBIDs   [][]string
}

func (r *sharedSuggestionChunkRepo) ListRecommendedFAQChunks(
	_ context.Context, tenantID uint64, kbIDs []string, _ []string, _ []string, _ int,
) ([]*types.Chunk, error) {
	r.faqTenants = append(r.faqTenants, tenantID)
	r.faqKBIDs = append(r.faqKBIDs, kbIDs)
	return nil, nil
}

func (r *sharedSuggestionChunkRepo) ListRecentDocumentChunksWithQuestions(
	_ context.Context, tenantID uint64, kbIDs []string, _ []string, _ int,
) ([]*types.Chunk, error) {
	r.docTenants = append(r.docTenants, tenantID)
	r.docKBIDs = append(r.docKBIDs, kbIDs)
	return nil, nil
}

// sharedSuggestionKBService resolves KBs without a tenant filter, as the
// real batch lookup does.
type sharedSuggestionKBService struct {
	interfaces.KnowledgeBaseService
	kbs []*types.KnowledgeBase
}

func (s *sharedSuggestionKBService) GetKnowledgeBasesByIDsOnly(
	_ context.Context, ids []string,
) ([]*types.KnowledgeBase, error) {
	byID := make(map[string]*types.KnowledgeBase, len(s.kbs))
	for _, kb := range s.kbs {
		byID[kb.ID] = kb
	}
	out := make([]*types.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		if kb, ok := byID[id]; ok {
			out = append(out, kb)
		}
	}
	return out, nil
}

func sharedAgentWithCuratedStarters() *types.CustomAgent {
	return &types.CustomAgent{
		ID:       "agent-shared",
		TenantID: 2,
		Config: types.CustomAgentConfig{
			QuestionSuggestions: &types.QuestionSuggestionConfig{
				Starters: types.StarterSuggestionConfig{
					Enabled: true,
					Mode:    types.SuggestionModeCurated,
					Count:   3,
					Items:   []string{"s1", "s2", "s3"},
				},
			},
		},
	}
}

// TestGetSuggestedQuestions_SharedAgentResolvesThroughShare is the
// regression test for issue #2957: a tenant an agent was shared with must
// get starter questions instead of a 404 agent-not-found.
func TestGetSuggestedQuestions_SharedAgentResolvesThroughShare(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	agent := sharedAgentWithCuratedStarters()
	svc := &customAgentService{
		repo:           &sharedSuggestionAgentRepo{agent: agent},
		agentShareRepo: &sharedSuggestionShareRepo{share: &types.AgentShare{SourceTenantID: 2}},
	}

	got, err := svc.GetSuggestedQuestions(ctx, "agent-shared", nil, nil, nil, 0)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "s1", got[0].Question)
}

// TestGetSuggestedQuestions_SharedAgentWithoutShareStaysNotFound keeps the
// original behavior for agents that are not shared with the caller.
func TestGetSuggestedQuestions_SharedAgentWithoutShareStaysNotFound(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	svc := &customAgentService{
		repo:           &sharedSuggestionAgentRepo{agent: sharedAgentWithCuratedStarters()},
		agentShareRepo: &sharedSuggestionShareRepo{},
	}

	_, err := svc.GetSuggestedQuestions(ctx, "agent-shared", nil, nil, nil, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

// TestGetSuggestedQuestions_NilShareRepoStaysNotFound pins the nil-safe
// fallback for unit constructions without a share repository.
func TestGetSuggestedQuestions_NilShareRepoStaysNotFound(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	svc := &customAgentService{
		repo: &sharedSuggestionAgentRepo{agent: sharedAgentWithCuratedStarters()},
	}

	_, err := svc.GetSuggestedQuestions(ctx, "agent-shared", nil, nil, nil, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

// TestGetSuggestedQuestions_SharedAgentIgnoresCallerKBOverrides ensures a
// shared-agent consumer cannot widen the suggestion scope: caller-supplied
// KB targets are dropped and only the agent's own KB configuration is
// queried, under the source tenant.
func TestGetSuggestedQuestions_SharedAgentIgnoresCallerKBOverrides(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	agent := sharedAgentWithCuratedStarters()
	agent.Config.QuestionSuggestions = nil // force the KB pipeline
	agent.Config.KBSelectionMode = "selected"
	agent.Config.KnowledgeBases = []string{"kb-owner"}
	chunks := &sharedSuggestionChunkRepo{}
	svc := &customAgentService{
		repo:           &sharedSuggestionAgentRepo{agent: agent},
		agentShareRepo: &sharedSuggestionShareRepo{share: &types.AgentShare{SourceTenantID: 2}},
		chunkRepo:      chunks,
		kbService: &sharedSuggestionKBService{kbs: []*types.KnowledgeBase{
			{ID: "kb-owner", TenantID: 2},
		}},
	}

	got, err := svc.GetSuggestedQuestions(ctx, "agent-shared", []string{"kb-caller"}, nil, nil, 5)
	require.NoError(t, err)
	assert.Empty(t, got)
	// Every chunk query must run under the source tenant (2) with only the
	// agent's configured KB — never the caller's kb-caller target.
	for _, tenantID := range append(append([]uint64{}, chunks.faqTenants...), chunks.docTenants...) {
		assert.Equal(t, uint64(2), tenantID)
	}
	for _, kbIDs := range append(append([][]string{}, chunks.faqKBIDs...), chunks.docKBIDs...) {
		for _, id := range kbIDs {
			assert.Equal(t, "kb-owner", id)
		}
	}
	require.NotEmpty(t, chunks.faqTenants, "expected the pipeline to reach the chunk queries")
}
