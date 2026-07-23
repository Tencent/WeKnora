package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	folderDocKB = "11111111-1111-1111-1111-111111111111"
	folderFAQKB = "22222222-2222-2222-2222-222222222222"
	folderAID   = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	folderBID   = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	folderCID   = "cccccccc-cccc-cccc-cccc-cccccccccccc"
)

func newFolderScopeSessionService() *sessionService {
	return &sessionService{
		cfg: &config.Config{},
		knowledgeBaseService: &tagTargetKnowledgeBaseService{kbs: map[string]*types.KnowledgeBase{
			folderDocKB: {ID: folderDocKB, TenantID: 100, Type: types.KnowledgeBaseTypeDocument},
			folderFAQKB: {ID: folderFAQKB, TenantID: 100, Type: types.KnowledgeBaseTypeFAQ},
		}},
		knowledgeService: &tagTargetKnowledgeService{
			knowledges: []*types.Knowledge{
				{ID: "doc-1", TenantID: 100, KnowledgeBaseID: folderDocKB},
				{ID: "doc-2", TenantID: 100, KnowledgeBaseID: folderDocKB},
				{ID: "doc-3", TenantID: 100, KnowledgeBaseID: folderDocKB},
				{ID: "faq-1", TenantID: 100, KnowledgeBaseID: folderFAQKB},
			},
			tagIDs: map[string][]string{"doc-1": {"tag-a"}, "doc-3": {"tag-a"}},
			folderIDs: map[string][]string{
				folderDocKB + "/" + folderAID: {"doc-1", "doc-2"},
				folderDocKB + "/" + folderBID: {},
				folderDocKB + "/" + folderCID: {"doc-2", "doc-3"},
				folderFAQKB + "/" + folderAID: {"faq-1"},
			},
		},
	}
}

func TestBuildSearchTargetsCombinesFolderUnionWithTagAndFile(t *testing.T) {
	targets, allowed, err := newFolderScopeSessionService().buildSearchTargetsWithFolderScopes(
		tagTargetContext(), 100, []string{folderDocKB}, []string{"doc-2", "doc-3"},
		[]types.TagScope{{KnowledgeBaseID: folderDocKB, TagIDs: []string{"tag-a"}}},
		[]types.FolderScope{{KnowledgeBaseID: folderDocKB, FolderIDs: []string{folderCID, folderAID, folderAID}}},
	)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"doc-1", "doc-2", "doc-3"}, allowed[folderDocKB])
	require.Len(t, targets, 1)
	assert.Equal(t, []string{"doc-3"}, targets[0].KnowledgeIDs)
	assert.True(t, targets[0].KnowledgeIDsSet)
}

func TestBuildSearchTargetsPreservesLegacySingleAndRestrictedEmpty(t *testing.T) {
	svc := newFolderScopeSessionService()
	legacy, _, err := svc.buildSearchTargetsWithFolderScopes(
		tagTargetContext(), 100, []string{folderDocKB}, nil, nil,
		[]types.FolderScope{{KnowledgeBaseID: folderDocKB, FolderID: ptrString(folderAID)}},
	)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"doc-1", "doc-2"}, legacy[0].KnowledgeIDs)

	empty, _, err := svc.buildSearchTargetsWithFolderScopes(
		tagTargetContext(), 100, []string{folderDocKB}, nil, nil,
		[]types.FolderScope{{KnowledgeBaseID: folderDocKB, FolderIDs: []string{folderBID}}},
	)
	require.NoError(t, err)
	require.Len(t, empty, 1)
	assert.True(t, empty[0].KnowledgeIDsSet)
	assert.Empty(t, empty[0].KnowledgeIDs)
}

func TestBuildSearchTargetsKeepsFAQTagAndRejectsUnselectedKB(t *testing.T) {
	svc := newFolderScopeSessionService()
	targets, _, err := svc.buildSearchTargetsWithFolderScopes(
		tagTargetContext(), 100, []string{folderFAQKB}, nil,
		[]types.TagScope{{KnowledgeBaseID: folderFAQKB, TagIDs: []string{"faq-tag"}}},
		[]types.FolderScope{{KnowledgeBaseID: folderFAQKB, FolderIDs: []string{folderAID}}},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"faq-1"}, targets[0].KnowledgeIDs)
	assert.Equal(t, []string{"faq-tag"}, targets[0].TagIDs)

	multiKB, _, err := svc.buildSearchTargetsWithFolderScopes(
		tagTargetContext(), 100, []string{folderDocKB, folderFAQKB}, nil, nil,
		[]types.FolderScope{{KnowledgeBaseID: folderDocKB, FolderIDs: []string{folderAID}}},
	)
	require.NoError(t, err)
	require.Len(t, multiKB, 2)
	assert.Equal(t, []string{"doc-1", "doc-2"}, multiKB[0].KnowledgeIDs)
	assert.Equal(t, types.SearchTargetTypeKnowledgeBase, multiKB[1].Type)

	_, _, err = svc.buildSearchTargetsWithFolderScopes(
		tagTargetContext(), 100, nil, nil, nil,
		[]types.FolderScope{{KnowledgeBaseID: folderDocKB, FolderIDs: []string{folderAID}}},
	)
	assert.Error(t, err)
}

func ptrString(value string) *string { return &value }
