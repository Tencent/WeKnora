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

func (s *tagTargetKnowledgeBaseService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return s.kbs[id], nil
}

type tagTargetKnowledgeService struct {
	interfaces.KnowledgeService
	knowledges []*types.Knowledge
	tagIDs     map[string][]string
	tagErr     error
	checkedIDs []string
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

// No search target construction may expand a tag into all its documents.
func (s *tagTargetKnowledgeService) ListKnowledgeIDsByTagIDs(context.Context, uint64, string, []string) ([]string, error) {
	panic("document enumeration must not run")
}

func (s *tagTargetKnowledgeService) GetKnowledgeTags(_ context.Context, ids []string) (map[string][]*types.KnowledgeTag, error) {
	s.checkedIDs = append(s.checkedIDs, ids...)
	if s.tagErr != nil {
		return nil, s.tagErr
	}
	out := make(map[string][]*types.KnowledgeTag)
	for _, id := range ids {
		for _, tagID := range s.tagIDs[id] {
			out[id] = append(out[id], &types.KnowledgeTag{ID: tagID})
		}
	}
	return out, nil
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
	assert.Equal(t, types.SearchTargetTypeKnowledgeBase, agentConfig.SearchTargets[0].Type)
	assert.Empty(t, agentConfig.SearchTargets[0].KnowledgeIDs)
	assert.Equal(t, []string{"tag-a"}, agentConfig.SearchTargets[0].TagIDs)
	assert.Empty(t, agentConfig.KnowledgeIDs)
	assert.Empty(t, svc.knowledgeService.(*tagTargetKnowledgeService).checkedIDs)
	assert.True(t, agentHasKnowledgeScope(agentConfig))
}

func tagTargetContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(100))
}

func TestBuildSearchTargets_DocumentTagScopeKeepsIndexTagFilter(t *testing.T) {
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
	assert.Equal(t, types.SearchTargetTypeKnowledgeBase, targets[0].Type)
	assert.Equal(t, "doc-kb", targets[0].KnowledgeBaseID)
	assert.Empty(t, targets[0].KnowledgeIDs)
	assert.Equal(t, []string{"tag-a"}, targets[0].TagIDs)
	assert.Empty(t, svc.knowledgeService.(*tagTargetKnowledgeService).checkedIDs)
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
	assert.Equal(t, []string{"tag-a"}, targets[0].TagIDs)
	assert.ElementsMatch(t, []string{"doc-2", "doc-3"}, svc.knowledgeService.(*tagTargetKnowledgeService).checkedIDs)
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
	assert.Equal(t, types.SearchTargetTypeKnowledgeBase, targets[0].Type)
	assert.Equal(t, []string{"tag-a"}, targets[0].TagIDs)
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
	assert.Equal(t, types.SearchTargetTypeKnowledgeBase, targets[0].Type)
	assert.Empty(t, targets[0].KnowledgeIDs)
	assert.Equal(t, []string{"tag-a"}, targets[0].TagIDs)
	assert.True(t, targets[0].DisableRecallThresholds)
}

func TestBuildSearchTargets_SelectedDocumentTagCheckFailure(t *testing.T) {
	svc := newTagTargetSessionService()
	svc.knowledgeService.(*tagTargetKnowledgeService).tagErr = fmt.Errorf("database unavailable")
	targets, err := svc.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb"}, []string{"doc-1"},
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}})
	require.ErrorContains(t, err, "database unavailable")
	assert.Empty(t, targets)
}

func TestBuildSearchTargets_EmptyDocumentTagIntersectionDoesNotWidenScope(t *testing.T) {
	svc := newTagTargetSessionService()
	targets, err := svc.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb"}, []string{"doc-2"},
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}})
	require.NoError(t, err)
	assert.Empty(t, targets)
}

func TestBuildSearchTargets_DocumentTagsUseORWithoutEnumeration(t *testing.T) {
	for _, explicitIDs := range [][]string{nil, {"doc-2", "doc-3"}} {
		svc := newTagTargetSessionService()
		targets, err := svc.buildSearchTargets(tagTargetContext(), 100,
			[]string{"doc-kb"}, explicitIDs,
			[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a", "tag-b"}}})
		require.NoError(t, err)
		require.Len(t, targets, 1)
		assert.Equal(t, explicitIDs, targets[0].KnowledgeIDs)
		assert.Equal(t, []string{"tag-a", "tag-b"}, targets[0].TagIDs)
		assert.Equal(t, targets[0].TagIDs, targets[0].ScopeTagIDs)
		assert.True(t, targets[0].DisableRecallThresholds)
		assert.EqualValues(t, 100, targets[0].TenantID)
		assert.ElementsMatch(t, explicitIDs, svc.knowledgeService.(*tagTargetKnowledgeService).checkedIDs)
	}
}

func TestBuildSearchTargets_UnknownTagRemainsConstrained(t *testing.T) {
	svc := newTagTargetSessionService()
	svc.knowledgeService.(*tagTargetKnowledgeService).tagErr = fmt.Errorf("tag-only scopes must not read document tags")
	targets, err := svc.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb"}, nil,
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"unknown-tag"}}})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Empty(t, targets[0].KnowledgeIDs)
	assert.Equal(t, []string{"unknown-tag"}, targets[0].TagIDs)
}

func TestAgentDocumentTagScopePrompt(t *testing.T) {
	for _, tc := range []struct {
		name        string
		explicitIDs []string
		wantPinned  []string
		wantKBs     int
	}{
		{name: "tag only", wantKBs: 1},
		{name: "explicit intersection", explicitIDs: []string{"doc-2", "doc-3"}, wantPinned: []string{"doc-3"}, wantKBs: 1},
		{name: "empty intersection", explicitIDs: []string{"doc-2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTagTargetSessionService()
			cfg, err := svc.buildAgentConfig(tagTargetContext(), &types.QARequest{
				Session: &types.Session{ID: "session-1", TenantID: 100},
				CustomAgent: &types.CustomAgent{ID: "agent-1", TenantID: 100, Config: types.CustomAgentConfig{
					AgentMode: types.AgentModeSmartReasoning, WebSearchProviderID: "provider-1",
				}},
				KnowledgeBaseIDs: []string{"doc-kb"}, KnowledgeIDs: tc.explicitIDs,
				TagScopes: []types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
			}, &types.Tenant{ID: 100}, 100)
			require.NoError(t, err)
			// The fake has no listing implementation: loading whole-KB examples
			// or enumerating tag documents would panic here.
			promptSvc := &agentService{knowledgeBaseService: svc.knowledgeBaseService, knowledgeService: svc.knowledgeService}
			kbs, docs := promptSvc.resolveKBAndDocInfos(tagTargetContext(), cfg)
			require.Len(t, kbs, tc.wantKBs)
			for _, kb := range kbs {
				assert.True(t, kb.TagScoped)
				assert.Empty(t, kb.RecentDocs)
				assert.Nil(t, kb.Profile)
			}
			var pinned []string
			for _, doc := range docs {
				pinned = append(pinned, doc.KnowledgeID)
			}
			assert.Equal(t, tc.wantPinned, pinned)
			assert.ElementsMatch(t, tc.explicitIDs, svc.knowledgeService.(*tagTargetKnowledgeService).checkedIDs)
		})
	}
}
