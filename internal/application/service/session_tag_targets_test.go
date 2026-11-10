package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tagTargetKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	kbs map[string]*types.KnowledgeBase
}

func (s *tagTargetKnowledgeBaseService) GetKnowledgeBasesByIDsOnly(
	_ context.Context,
	ids []string,
) ([]*types.KnowledgeBase, error) {
	out := make([]*types.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		if kb := s.kbs[id]; kb != nil {
			out = append(out, kb)
		}
	}
	return out, nil
}

type tagTargetKnowledgeService struct {
	interfaces.KnowledgeService
	knowledges []*types.Knowledge
	tagIDs     map[string][]string
}

func (s *tagTargetKnowledgeService) GetKnowledgeBatchWithSharedAccess(
	_ context.Context,
	_ uint64,
	ids []string,
) ([]*types.Knowledge, error) {
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	out := make([]*types.Knowledge, 0)
	for _, knowledge := range s.knowledges {
		if allowed[knowledge.ID] {
			out = append(out, knowledge)
		}
	}
	return out, nil
}

func (s *tagTargetKnowledgeService) ListKnowledgeIDsByTagIDs(
	_ context.Context,
	_ uint64,
	kbID string,
	tagIDs []string,
) ([]string, error) {
	allowedTags := make(map[string]bool, len(tagIDs))
	for _, tagID := range tagIDs {
		allowedTags[tagID] = true
	}
	out := make([]string, 0)
	for knowledgeID, tags := range s.tagIDs {
		if !knowledgeBelongsToKB(s.knowledges, knowledgeID, kbID) {
			continue
		}
		for _, tagID := range tags {
			if allowedTags[tagID] {
				out = append(out, knowledgeID)
				break
			}
		}
	}
	return out, nil
}

func knowledgeBelongsToKB(knowledges []*types.Knowledge, knowledgeID string, kbID string) bool {
	for _, knowledge := range knowledges {
		if knowledge.ID == knowledgeID && knowledge.KnowledgeBaseID == kbID {
			return true
		}
	}
	return false
}

func newTagTargetSessionService() *sessionService {
	return &sessionService{
		cfg: &config.Config{},
		knowledgeBaseService: &tagTargetKnowledgeBaseService{
			kbs: map[string]*types.KnowledgeBase{
				"doc-kb": {ID: "doc-kb", TenantID: 100, Type: types.KnowledgeBaseTypeDocument},
				"faq-kb": {ID: "faq-kb", TenantID: 100, Type: types.KnowledgeBaseTypeFAQ},
			},
		},
		knowledgeService: &tagTargetKnowledgeService{
			knowledges: []*types.Knowledge{
				{ID: "doc-1", TenantID: 100, KnowledgeBaseID: "doc-kb"},
				{ID: "doc-2", TenantID: 100, KnowledgeBaseID: "doc-kb"},
				{ID: "doc-3", TenantID: 100, KnowledgeBaseID: "doc-kb"},
			},
			tagIDs: map[string][]string{
				"doc-1": {"tag-a"},
				"doc-2": {"tag-b"},
				"doc-3": {"tag-a", "tag-b"},
			},
		},
	}
}

func TestBuildAgentConfig_TagOnlyScopePreservesRetrievalTarget(t *testing.T) {
	svc := newTagTargetSessionService()
	agent := &types.CustomAgent{
		ID:       "agent-1",
		TenantID: 100,
		Config: types.CustomAgentConfig{
			AgentMode:           types.AgentModeSmartReasoning,
			KBSelectionMode:     "all",
			WebSearchProviderID: "provider-1",
		},
	}
	req := &types.QARequest{
		Session:     &types.Session{ID: "session-1", TenantID: 100},
		CustomAgent: agent,
		TagScopes: []types.TagScope{
			{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}},
		},
	}

	agentConfig, err := svc.buildAgentConfig(
		tagTargetContext(),
		req,
		&types.Tenant{ID: 100},
		100,
	)

	require.NoError(t, err)
	assert.Empty(t, agentConfig.KnowledgeBases)
	require.Len(t, agentConfig.SearchTargets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledge, agentConfig.SearchTargets[0].Type)
	assert.ElementsMatch(t, []string{"doc-1", "doc-3"}, agentConfig.SearchTargets[0].KnowledgeIDs)
	assert.ElementsMatch(t, []string{"doc-1", "doc-3"}, agentConfig.KnowledgeIDs)
	assert.True(t, agentHasKnowledgeScope(agentConfig))
}

func tagTargetContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(100))
}

func TestBuildSearchTargets_DocumentTagScopeResolvesKnowledgeIDs(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		nil,
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
	)

	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledge, targets[0].Type)
	assert.Equal(t, "doc-kb", targets[0].KnowledgeBaseID)
	assert.ElementsMatch(t, []string{"doc-1", "doc-3"}, targets[0].KnowledgeIDs)
	assert.Empty(t, targets[0].TagIDs)
	assert.ElementsMatch(t, []string{"tag-a"}, targets[0].ScopeTagIDs)
	assert.True(t, targets[0].DisableRecallThresholds)
}

func TestBuildSearchTargets_ExplicitKnowledgeScopeDisablesRecallThresholds(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		nil,
		[]string{"doc-1"},
		nil,
	)

	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledge, targets[0].Type)
	assert.Equal(t, []string{"doc-1"}, targets[0].KnowledgeIDs)
	assert.True(t, targets[0].DisableRecallThresholds)
}

func TestBuildSearchTargets_DocumentTagScopeIntersectsExplicitKnowledgeIDs(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		[]string{"doc-2", "doc-3"},
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
	)

	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledge, targets[0].Type)
	assert.Equal(t, []string{"doc-3"}, targets[0].KnowledgeIDs)
	assert.ElementsMatch(t, []string{"tag-a"}, targets[0].ScopeTagIDs)
	assert.True(t, targets[0].DisableRecallThresholds)
}

func TestBuildSearchTargets_FAQTagScopeKeepsIndexTagFilter(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"faq-kb"},
		nil,
		[]types.TagScope{{KnowledgeBaseID: "faq-kb", TagIDs: []string{"tag-a", "tag-b"}}},
	)

	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledgeBase, targets[0].Type)
	assert.Equal(t, "faq-kb", targets[0].KnowledgeBaseID)
	assert.ElementsMatch(t, []string{"tag-a", "tag-b"}, targets[0].TagIDs)
	assert.ElementsMatch(t, []string{"tag-a", "tag-b"}, targets[0].ScopeTagIDs)
	assert.True(t, targets[0].DisableRecallThresholds)
}

func TestBuildSearchTargets_FullKBWithTagScopeSkipsFullKBTarget(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		nil,
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
	)

	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledge, targets[0].Type)
	assert.NotEqual(t, types.SearchTargetTypeKnowledgeBase, targets[0].Type)
}

func TestBuildSearchTargets_DocumentTagScopeWithMissingKBMetadata(t *testing.T) {
	svc := &sessionService{
		knowledgeBaseService: &tagTargetKnowledgeBaseService{kbs: map[string]*types.KnowledgeBase{}},
		knowledgeService: &tagTargetKnowledgeService{
			knowledges: []*types.Knowledge{
				{ID: "doc-1", TenantID: 100, KnowledgeBaseID: "doc-kb"},
				{ID: "doc-3", TenantID: 100, KnowledgeBaseID: "doc-kb"},
			},
			tagIDs: map[string][]string{
				"doc-1": {"tag-a"},
				"doc-3": {"tag-a"},
			},
		},
	}

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		nil,
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
	)

	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledge, targets[0].Type)
	assert.ElementsMatch(t, []string{"doc-1", "doc-3"}, targets[0].KnowledgeIDs)
	assert.True(t, targets[0].DisableRecallThresholds)
}

func TestMergeResolvedTagKnowledgeIDs_OnlyIncludesTagScopedTargets(t *testing.T) {
	got := mergeResolvedTagKnowledgeIDs(
		[]string{"existing-doc"},
		types.SearchTargets{
			{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "tag-kb", KnowledgeIDs: []string{"tag-doc-1", "tag-doc-2"}},
			{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "other-kb", KnowledgeIDs: []string{"other-doc"}},
			{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "faq-kb", TagIDs: []string{"faq-tag"}},
		},
		[]types.TagScope{
			{KnowledgeBaseID: "tag-kb", TagIDs: []string{"tag-a"}},
			{KnowledgeBaseID: "faq-kb", TagIDs: []string{"faq-tag"}},
		},
	)

	assert.ElementsMatch(t, []string{"existing-doc", "tag-doc-1", "tag-doc-2"}, got)
}

type tagTargetKnowledgeServiceWithError struct {
	tagTargetKnowledgeService
	listErr error
}

func (s *tagTargetKnowledgeServiceWithError) ListKnowledgeIDsByTagIDs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagIDs []string,
) ([]string, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.tagTargetKnowledgeService.ListKnowledgeIDsByTagIDs(ctx, tenantID, kbID, tagIDs)
}

func TestBuildSearchTargets_DocumentTagScopeResolutionError(t *testing.T) {
	base := newTagTargetSessionService()
	base.knowledgeService = &tagTargetKnowledgeServiceWithError{
		tagTargetKnowledgeService: *base.knowledgeService.(*tagTargetKnowledgeService),
		listErr:                   fmt.Errorf("database unavailable"),
	}

	_, err := base.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		nil,
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "database unavailable")
}
