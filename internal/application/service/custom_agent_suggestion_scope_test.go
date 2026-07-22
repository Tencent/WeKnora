package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type suggestionTagRepo struct {
	interfaces.KnowledgeTagRepository
	tagsByTenant map[uint64][]*types.KnowledgeTag
}

func (r *suggestionTagRepo) GetByIDs(_ context.Context, tenantID uint64, ids []string) ([]*types.KnowledgeTag, error) {
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	var result []*types.KnowledgeTag
	for _, tag := range r.tagsByTenant[tenantID] {
		if tag != nil && wanted[tag.ID] {
			result = append(result, tag)
		}
	}
	return result, nil
}

type suggestionKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	idsByTenantAndKB map[uint64]map[string][]string
}

func (r *suggestionKnowledgeRepo) ListIDsByTagIDs(
	_ context.Context,
	tenantID uint64,
	kbID string,
	_ []string,
) ([]string, error) {
	return append([]string(nil), r.idsByTenantAndKB[tenantID][kbID]...), nil
}

type suggestionKBService struct {
	interfaces.KnowledgeBaseService
	kbs map[string]*types.KnowledgeBase
}

func (s *suggestionKBService) GetKnowledgeBasesByIDsOnly(
	_ context.Context,
	ids []string,
) ([]*types.KnowledgeBase, error) {
	result := make([]*types.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		if kb := s.kbs[id]; kb != nil {
			result = append(result, kb)
		}
	}
	return result, nil
}

type suggestionKBShareService struct {
	interfaces.KBShareService
	allowed map[string]bool
}

func (s *suggestionKBShareService) HasTenantKBPermission(
	_ context.Context,
	kbID string,
	_ uint64,
	_ types.TenantRole,
	_ types.OrgMemberRole,
) (bool, error) {
	return s.allowed[kbID], nil
}

func TestResolveSuggestionTagScopes_UsesSourceTenantForSharedKB(t *testing.T) {
	const (
		callerTenant = uint64(1)
		sourceTenant = uint64(2)
		kbID         = "shared-kb"
		tagID        = "shared-tag"
	)
	svc := &customAgentService{
		tagRepo: &suggestionTagRepo{tagsByTenant: map[uint64][]*types.KnowledgeTag{
			sourceTenant: {{ID: tagID, TenantID: sourceTenant, KnowledgeBaseID: kbID}},
		}},
		knowledgeRepo: &suggestionKnowledgeRepo{idsByTenantAndKB: map[uint64]map[string][]string{
			sourceTenant: {kbID: {"doc-in-tag"}},
		}},
		kbService: &suggestionKBService{kbs: map[string]*types.KnowledgeBase{
			kbID: {ID: kbID, TenantID: sourceTenant},
		}},
		kbShareService: &suggestionKBShareService{allowed: map[string]bool{kbID: true}},
	}

	resolved, err := svc.resolveSuggestionTagScopes(
		context.Background(),
		callerTenant,
		[]types.TagScope{{KnowledgeBaseID: kbID, TagIDs: []string{tagID}}},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{kbID}, resolved.KnowledgeBaseIDs)
	assert.Equal(t, []string{"doc-in-tag"}, resolved.KnowledgeIDs)
	assert.Equal(t, []string{tagID}, resolved.TagIDsByTenant[sourceTenant])
	assert.Empty(t, resolved.TagIDsByTenant[callerTenant])
}

func TestMergeHybridStarterSuggestions_ReservesKnowledgeSlots(t *testing.T) {
	curated := []types.SuggestedQuestion{
		{Question: "curated 1", Source: "agent_config"},
		{Question: "curated 2", Source: "agent_config"},
		{Question: "curated 3", Source: "agent_config"},
		{Question: "curated 4", Source: "agent_config"},
		{Question: "curated 5", Source: "agent_config"},
		{Question: "curated 6", Source: "agent_config"},
	}
	knowledge := []types.SuggestedQuestion{
		{Question: "knowledge 1", Source: "document"},
		{Question: "knowledge 2", Source: "faq"},
		{Question: "knowledge 3", Source: "document"},
	}

	got := mergeHybridStarterSuggestions(curated, knowledge, 6)
	require.Len(t, got, 6)
	assert.Equal(t, []string{
		"curated 1", "curated 2", "curated 3", "curated 4", "knowledge 1", "knowledge 2",
	}, []string{got[0].Question, got[1].Question, got[2].Question, got[3].Question, got[4].Question, got[5].Question})
}

func TestMergeHybridStarterSuggestions_BackfillsWhenKnowledgeIsEmpty(t *testing.T) {
	curated := []types.SuggestedQuestion{
		{Question: "curated 1"}, {Question: "curated 2"}, {Question: "curated 3"},
	}
	got := mergeHybridStarterSuggestions(curated, nil, 3)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"curated 1", "curated 2", "curated 3"}, []string{
		got[0].Question, got[1].Question, got[2].Question,
	})
}

func TestExcludeSuggestionStrings_TagScopeOverridesSameKnowledgeBase(t *testing.T) {
	got := excludeSuggestionStrings([]string{"kb-with-tag", "kb-explicit"}, []string{"kb-with-tag"})
	assert.Equal(t, []string{"kb-explicit"}, got)
}

type suggestionFolderService struct {
	interfaces.KnowledgeFolderService
	folders     map[string]*types.KnowledgeFolder
	foldersByKB map[string]*types.KnowledgeFolder
	scopes      map[string]*types.FolderKnowledgeScope
}

func (s *suggestionFolderService) GetFolder(
	_ context.Context, kbID, folderID string,
) (*types.KnowledgeFolder, error) {
	folder := s.foldersByKB[kbID+":"+folderID]
	if folder == nil {
		folder = s.folders[folderID]
	}
	if folder == nil || folder.KnowledgeBaseID != kbID {
		return nil, types.ErrInvalidArgument
	}
	return folder, nil
}

func (s *suggestionFolderService) ResolveKnowledgeScope(
	_ context.Context, kbID string, folderIDs []string,
) (*types.FolderKnowledgeScope, error) {
	key := kbID + ":"
	for _, folderID := range folderIDs {
		key += folderID + ","
	}
	key += "sub"
	if scope := s.scopes[key]; scope != nil {
		return scope, nil
	}
	return &types.FolderKnowledgeScope{}, nil
}

func TestResolveSuggestionFolderScopesNamedAndRoot(t *testing.T) {
	const tenantID = uint64(1)
	svc := &customAgentService{
		kbService: &suggestionKBService{kbs: map[string]*types.KnowledgeBase{
			"kb-1": {ID: "kb-1", TenantID: tenantID},
		}},
		knowledgeFolderService: &suggestionFolderService{
			folders: map[string]*types.KnowledgeFolder{
				"folder-1": {ID: "folder-1", TenantID: tenantID, KnowledgeBaseID: "kb-1"},
			},
			scopes: map[string]*types.FolderKnowledgeScope{
				"kb-1:folder-1,sub": {KnowledgeIDs: []string{"doc-1", "doc-2"}},
				"kb-1:,sub":         {FullKnowledgeBase: true},
			},
		},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)

	resolved, err := svc.resolveSuggestionFolderScopes(ctx, tenantID, []string{"kb-1"}, []string{"folder-1"})
	require.NoError(t, err)
	assert.Equal(t, []string{"doc-1", "doc-2"}, resolved.KnowledgeIDs)
	assert.Equal(t, []string{"kb-1"}, resolved.ScopedKnowledgeBaseIDs)
	assert.Empty(t, resolved.FullKnowledgeBaseIDs)

	root, err := svc.resolveSuggestionFolderScopes(ctx, tenantID, []string{"kb-1"}, []string{types.FolderRootID})
	require.NoError(t, err)
	assert.Equal(t, []string{"kb-1"}, root.FullKnowledgeBaseIDs)
	assert.Empty(t, root.KnowledgeIDs)
	explicit := []string{"doc-explicit"}
	if len(root.KnowledgeIDs) > 0 {
		explicit = intersectOptionalSuggestionScopes(explicit, root.KnowledgeIDs)
	}
	assert.Equal(t, []string{"doc-explicit"}, explicit)
}

func TestResolveSuggestionFolderScopesEmptyAndCrossKB(t *testing.T) {
	const tenantID = uint64(1)
	svc := &customAgentService{
		kbService: &suggestionKBService{kbs: map[string]*types.KnowledgeBase{
			"kb-1": {ID: "kb-1", TenantID: tenantID},
			"kb-2": {ID: "kb-2", TenantID: tenantID},
		}},
		knowledgeFolderService: &suggestionFolderService{
			folders: map[string]*types.KnowledgeFolder{
				"empty": {ID: "empty", TenantID: tenantID, KnowledgeBaseID: "kb-1"},
			},
			scopes: map[string]*types.FolderKnowledgeScope{},
		},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)

	empty, err := svc.resolveSuggestionFolderScopes(ctx, tenantID, []string{"kb-1"}, []string{"empty"})
	require.NoError(t, err)
	assert.True(t, empty.Empty)

	_, err = svc.resolveSuggestionFolderScopes(ctx, tenantID, []string{"kb-2"}, []string{"empty"})
	require.Error(t, err)
}

func TestResolveSuggestionFolderScopesUsesSharedKBOwnerTenant(t *testing.T) {
	const callerTenant, ownerTenant = uint64(1), uint64(2)
	folderService := &suggestionFolderService{
		folders: map[string]*types.KnowledgeFolder{
			"shared-folder": {ID: "shared-folder", TenantID: ownerTenant, KnowledgeBaseID: "shared-kb"},
		},
		scopes: map[string]*types.FolderKnowledgeScope{
			"shared-kb:shared-folder,sub": {KnowledgeIDs: []string{"shared-doc"}},
		},
	}
	svc := &customAgentService{
		kbService: &suggestionKBService{kbs: map[string]*types.KnowledgeBase{
			"shared-kb": {ID: "shared-kb", TenantID: ownerTenant},
		}},
		kbShareService:         &suggestionKBShareService{allowed: map[string]bool{"shared-kb": true}},
		knowledgeFolderService: folderService,
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, callerTenant)
	ctx = context.WithValue(ctx, types.UserIDContextKey, "user-1")

	resolved, err := svc.resolveSuggestionFolderScopes(
		ctx, callerTenant, []string{"shared-kb"}, []string{"shared-folder"},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"shared-doc"}, resolved.KnowledgeIDs)
}

func TestIntersectSuggestionScope(t *testing.T) {
	assert.ElementsMatch(t, []string{"doc-2"}, intersectOptionalSuggestionScopes(
		[]string{"doc-1", "doc-2"}, []string{"doc-2", "doc-3"},
	))
	assert.ElementsMatch(t, []string{"doc-1"}, intersectOptionalSuggestionScopes(nil, []string{"doc-1"}))
}

func TestResolveSuggestionFolderScopesRejectsAmbiguousBareFolderID(t *testing.T) {
	const tenantID = uint64(1)
	svc := &customAgentService{
		kbService: &suggestionKBService{kbs: map[string]*types.KnowledgeBase{
			"kb-1": {ID: "kb-1", TenantID: tenantID},
			"kb-2": {ID: "kb-2", TenantID: tenantID},
		}},
		knowledgeFolderService: &suggestionFolderService{foldersByKB: map[string]*types.KnowledgeFolder{
			"kb-1:duplicate": {ID: "duplicate", TenantID: tenantID, KnowledgeBaseID: "kb-1"},
			"kb-2:duplicate": {ID: "duplicate", TenantID: tenantID, KnowledgeBaseID: "kb-2"},
		}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)

	_, err := svc.resolveSuggestionFolderScopes(
		ctx, tenantID, []string{"kb-1", "kb-2"}, []string{"duplicate"},
	)
	require.Error(t, err)
}

func TestSuggestionFolderExplicitIntersectionEmptyIsAuthoritative(t *testing.T) {
	assert.Empty(t, intersectOptionalSuggestionScopes([]string{"doc-outside"}, []string{"doc-inside"}))
}

func newSuggestedQuestionFolderSQLiteService(t *testing.T) (*customAgentService, context.Context, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))
	chunkRepo := repository.NewChunkRepository(db)
	const tenantID = uint64(1)
	const tagID = "faq-tag"
	chunks := []*types.Chunk{
		makeSuggestionFAQChunk(t, "folder-no-tag", "other-tag", "folder no tag"),
		makeSuggestionFAQChunk(t, "outside-with-tag", tagID, "outside with tag"),
		makeSuggestionFAQChunk(t, "folder-with-tag", tagID, "folder with tag"),
	}
	require.NoError(t, chunkRepo.CreateChunks(context.Background(), chunks))
	svc := &customAgentService{
		repo: &suggestionAgentRepo{agent: &types.CustomAgent{
			ID: "agent-1", TenantID: tenantID,
			Config: types.CustomAgentConfig{KBSelectionMode: "selected", KnowledgeBases: []string{"kb-1"}},
		}},
		chunkRepo: chunkRepo,
		kbService: &suggestionKBService{kbs: map[string]*types.KnowledgeBase{
			"kb-1": {ID: "kb-1", TenantID: tenantID},
		}},
		tagRepo: &suggestionTagRepo{tagsByTenant: map[uint64][]*types.KnowledgeTag{
			tenantID: {{ID: tagID, TenantID: tenantID, KnowledgeBaseID: "kb-1"}},
		}},
		knowledgeRepo: &suggestionKnowledgeRepo{idsByTenantAndKB: map[uint64]map[string][]string{}},
		knowledgeFolderService: &suggestionFolderService{
			folders: map[string]*types.KnowledgeFolder{
				"folder-1": {ID: "folder-1", TenantID: tenantID, KnowledgeBaseID: "kb-1"},
			},
			scopes: map[string]*types.FolderKnowledgeScope{
				"kb-1:folder-1,sub": {KnowledgeIDs: []string{"folder-no-tag", "folder-with-tag"}},
			},
		},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	return svc, ctx, tagID
}

func makeSuggestionFAQChunk(t *testing.T, knowledgeID, tagID, question string) *types.Chunk {
	t.Helper()
	chunk := &types.Chunk{
		ID: "chunk-" + knowledgeID, TenantID: 1, KnowledgeBaseID: "kb-1", KnowledgeID: knowledgeID,
		ChunkType: types.ChunkTypeFAQ, TagID: tagID, IsEnabled: true, Flags: types.ChunkFlagRecommended,
	}
	require.NoError(t, chunk.SetFAQMetadata(&types.FAQChunkMetadata{StandardQuestion: question}))
	return chunk
}

func TestSuggestedQuestionFolderFAQTagUsesIntersection(t *testing.T) {
	svc, ctx, tagID := newSuggestedQuestionFolderSQLiteService(t)
	got, err := svc.GetKnowledgeSuggestedQuestions(ctx, "agent-1", []string{"kb-1"}, nil,
		[]types.TagScope{{KnowledgeBaseID: "kb-1", TagIDs: []string{tagID}}}, []string{"folder-1"}, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "folder with tag", got[0].Question)
}

func TestSuggestedQuestionFolderFAQSingleScopesRemainCompatible(t *testing.T) {
	svc, ctx, tagID := newSuggestedQuestionFolderSQLiteService(t)
	folderOnly, err := svc.GetKnowledgeSuggestedQuestions(
		ctx, "agent-1", []string{"kb-1"}, nil, nil, []string{"folder-1"}, 10)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"folder no tag", "folder with tag"}, suggestedQuestionTexts(folderOnly))

	tagOnly, err := svc.GetKnowledgeSuggestedQuestions(ctx, "agent-1", nil, nil,
		[]types.TagScope{{KnowledgeBaseID: "kb-1", TagIDs: []string{tagID}}}, nil, 10)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"outside with tag", "folder with tag"}, suggestedQuestionTexts(tagOnly))
}

func TestSuggestedQuestionFolderFAQTagEmptyIntersectionDoesNotFallbackDefaults(t *testing.T) {
	svc, ctx, _ := newSuggestedQuestionFolderSQLiteService(t)
	svc.tagRepo = &suggestionTagRepo{tagsByTenant: map[uint64][]*types.KnowledgeTag{
		1: {{ID: "missing-tag", TenantID: 1, KnowledgeBaseID: "kb-1"}},
	}}
	got, err := svc.GetKnowledgeSuggestedQuestions(ctx, "agent-1", []string{"kb-1"}, nil,
		[]types.TagScope{{KnowledgeBaseID: "kb-1", TagIDs: []string{"missing-tag"}}}, []string{"folder-1"}, 10)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func suggestedQuestionTexts(items []types.SuggestedQuestion) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Question)
	}
	return out
}
