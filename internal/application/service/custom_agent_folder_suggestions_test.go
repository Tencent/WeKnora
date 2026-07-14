package service

import (
	"context"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type folderSuggestionAgentRepo struct {
	interfaces.CustomAgentRepository
	agent *types.CustomAgent
}

func (r *folderSuggestionAgentRepo) GetAgentByID(context.Context, string, uint64) (*types.CustomAgent, error) {
	return r.agent, nil
}

type folderSuggestionKBService struct {
	interfaces.KnowledgeBaseService
	kbs map[string]*types.KnowledgeBase
}

func (s *folderSuggestionKBService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return s.kbs[id], nil
}

func (s *folderSuggestionKBService) GetKnowledgeBasesByIDsOnly(_ context.Context, ids []string) ([]*types.KnowledgeBase, error) {
	result := make([]*types.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		if kb := s.kbs[id]; kb != nil {
			result = append(result, kb)
		}
	}
	return result, nil
}

type folderSuggestionKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	byFolder map[string][]string
	byID     map[string]*types.Knowledge
	byTag    map[string][]string
}

func (r *folderSuggestionKnowledgeRepo) ListIDsByFolderIDs(_ context.Context, _ uint64, kbID string, _ []string) ([]string, error) {
	return append([]string(nil), r.byFolder[kbID]...), nil
}

func (r *folderSuggestionKnowledgeRepo) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	return r.byID[id], nil
}

func (r *folderSuggestionKnowledgeRepo) ListIDsByTagIDs(_ context.Context, _ uint64, kbID string, _ []string) ([]string, error) {
	return append([]string(nil), r.byTag[kbID]...), nil
}

type folderSuggestionTagRepo struct {
	interfaces.KnowledgeTagRepository
	tags []*types.KnowledgeTag
}

func (r *folderSuggestionTagRepo) GetByIDs(context.Context, uint64, []string) ([]*types.KnowledgeTag, error) {
	return r.tags, nil
}

type folderSuggestionChunkRepo struct {
	interfaces.ChunkRepository
	documentCalls []folderSuggestionChunkCall
}

type folderSuggestionChunkCall struct {
	kbIDs        []string
	knowledgeIDs []string
}

func (r *folderSuggestionChunkRepo) ListRecommendedFAQChunks(context.Context, uint64, []string, []string, int) ([]*types.Chunk, error) {
	return nil, nil
}

func (r *folderSuggestionChunkRepo) ListRecentDocumentChunksWithQuestions(
	_ context.Context, _ uint64, kbIDs, knowledgeIDs []string, _ int,
) ([]*types.Chunk, error) {
	r.documentCalls = append(r.documentCalls, folderSuggestionChunkCall{
		kbIDs: append([]string(nil), kbIDs...), knowledgeIDs: append([]string(nil), knowledgeIDs...),
	})
	if len(kbIDs) != 1 {
		return nil, nil
	}
	chunk := &types.Chunk{KnowledgeBaseID: kbIDs[0]}
	switch kbIDs[0] {
	case "kb-full":
		if len(knowledgeIDs) != 0 {
			return nil, nil
		}
		chunk.ID, chunk.KnowledgeID = "chunk-full", "doc-full"
		if err := chunk.SetDocumentMetadata(&types.DocumentChunkMetadata{
			GeneratedQuestions: []types.GeneratedQuestion{{Question: "Question from the full KB?"}},
		}); err != nil {
			panic(err)
		}
	case "kb-folder":
		if len(knowledgeIDs) != 1 || knowledgeIDs[0] != "doc-folder" {
			return nil, nil
		}
		chunk.ID, chunk.KnowledgeID = "chunk-folder", "doc-folder"
		if err := chunk.SetDocumentMetadata(&types.DocumentChunkMetadata{
			GeneratedQuestions: []types.GeneratedQuestion{{Question: "Question from the folder?"}},
		}); err != nil {
			panic(err)
		}
	default:
		return nil, nil
	}
	return []*types.Chunk{chunk}, nil
}

func newFolderSuggestionService(folderKnowledgeIDs []string) (*customAgentService, *folderSuggestionChunkRepo) {
	chunkRepo := &folderSuggestionChunkRepo{}
	return &customAgentService{
		repo: &folderSuggestionAgentRepo{agent: &types.CustomAgent{
			ID: "agent-1", TenantID: 1,
			Config: types.CustomAgentConfig{
				AgentMode:       types.AgentModeQuickAnswer,
				KBSelectionMode: "selected",
				KnowledgeBases:  []string{"kb-full", "kb-folder"},
				QuestionSuggestions: &types.QuestionSuggestionConfig{
					Starters: types.StarterSuggestionConfig{
						Enabled: true,
						Mode:    types.SuggestionModeHybrid,
						Items:   []string{"Agent prompt?"},
						Count:   10,
					},
				},
			},
		}},
		chunkRepo: chunkRepo,
		kbService: &folderSuggestionKBService{kbs: map[string]*types.KnowledgeBase{
			"kb-full":   {ID: "kb-full", TenantID: 1, Type: types.KnowledgeBaseTypeDocument},
			"kb-folder": {ID: "kb-folder", TenantID: 1, Type: types.KnowledgeBaseTypeDocument},
		}},
		knowledgeRepo: &folderSuggestionKnowledgeRepo{
			byFolder: map[string][]string{"kb-folder": folderKnowledgeIDs},
			byID: map[string]*types.Knowledge{
				"doc-folder": {ID: "doc-folder", TenantID: 1, KnowledgeBaseID: "kb-folder"},
				"doc-full":   {ID: "doc-full", TenantID: 1, KnowledgeBaseID: "kb-full"},
			},
			byTag: map[string][]string{},
		},
	}, chunkRepo
}

func folderSuggestionContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
}

func TestGetSuggestedQuestionsWithFoldersKeepsWholeKBAndFolderScopesIndependent(t *testing.T) {
	svc, chunkRepo := newFolderSuggestionService([]string{"doc-folder"})
	questions, err := svc.GetSuggestedQuestionsWithFolders(
		folderSuggestionContext(), "agent-1",
		[]string{"kb-full"}, nil, nil, 10,
		[]types.FolderScope{{KnowledgeBaseID: "kb-folder", FolderIDs: []string{"folder-1"}}},
	)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"Agent prompt?", "Question from the full KB?", "Question from the folder?"}, suggestedQuestionTexts(questions))
	require.Len(t, chunkRepo.documentCalls, 2)
	assert.ElementsMatch(t, []folderSuggestionChunkCall{
		{kbIDs: []string{"kb-full"}},
		{kbIDs: []string{"kb-folder"}, knowledgeIDs: []string{"doc-folder"}},
	}, chunkRepo.documentCalls)
}

func TestGetSuggestedQuestionsWithFoldersExplicitWholeKBSupersedesFolder(t *testing.T) {
	svc, chunkRepo := newFolderSuggestionService(nil)
	questions, err := svc.GetSuggestedQuestionsWithFolders(
		folderSuggestionContext(), "agent-1", []string{"kb-folder"}, nil, nil, 10,
		[]types.FolderScope{{KnowledgeBaseID: "kb-folder", FolderIDs: []string{"empty-folder"}}},
	)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"Agent prompt?"}, suggestedQuestionTexts(questions))
	require.Len(t, chunkRepo.documentCalls, 1)
	assert.Equal(t, []string{"kb-folder"}, chunkRepo.documentCalls[0].kbIDs)
	assert.Empty(t, chunkRepo.documentCalls[0].knowledgeIDs)
}

func TestGetSuggestedQuestionsWithFoldersDoesNotWidenEmptyFolder(t *testing.T) {
	svc, chunkRepo := newFolderSuggestionService(nil)
	questions, err := svc.GetSuggestedQuestionsWithFolders(
		folderSuggestionContext(), "agent-1", nil, nil, nil, 10,
		[]types.FolderScope{{KnowledgeBaseID: "kb-folder", FolderIDs: []string{"empty-folder"}}},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"Agent prompt?"}, suggestedQuestionTexts(questions))
	assert.Empty(t, chunkRepo.documentCalls)
}

func TestGetKnowledgeSuggestedQuestionsWithFoldersExcludesCuratedStarters(t *testing.T) {
	svc, chunkRepo := newFolderSuggestionService([]string{"doc-folder"})
	questions, err := svc.GetKnowledgeSuggestedQuestionsWithFolders(
		folderSuggestionContext(), "agent-1", nil, nil, nil, 10,
		[]types.FolderScope{{KnowledgeBaseID: "kb-folder", FolderIDs: []string{"folder-1"}}},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"Question from the folder?"}, suggestedQuestionTexts(questions))
	require.Len(t, chunkRepo.documentCalls, 1)
	assert.Equal(t, []string{"doc-folder"}, chunkRepo.documentCalls[0].knowledgeIDs)
}

func TestGetKnowledgeSuggestedQuestionsWithFoldersDoesNotWidenEmptyFolder(t *testing.T) {
	svc, chunkRepo := newFolderSuggestionService(nil)
	questions, err := svc.GetKnowledgeSuggestedQuestionsWithFolders(
		folderSuggestionContext(), "agent-1", nil, nil, nil, 10,
		[]types.FolderScope{{KnowledgeBaseID: "kb-folder", FolderIDs: []string{"empty-folder"}}},
	)
	require.NoError(t, err)
	assert.Empty(t, questions)
	assert.Empty(t, chunkRepo.documentCalls)
}

func TestGetKnowledgeSuggestedQuestionsWithFoldersIntersectsExplicitKnowledge(t *testing.T) {
	svc, chunkRepo := newFolderSuggestionService([]string{"doc-folder"})
	questions, err := svc.GetKnowledgeSuggestedQuestionsWithFolders(
		folderSuggestionContext(), "agent-1", nil, []string{"doc-full"}, nil, 10,
		[]types.FolderScope{{KnowledgeBaseID: "kb-folder", FolderIDs: []string{"folder-1"}}},
	)
	require.NoError(t, err)
	assert.Empty(t, questions)
	assert.Empty(t, chunkRepo.documentCalls)
}

func TestGetKnowledgeSuggestedQuestionsWithFoldersIntersectsFolderTagAndExplicitKnowledge(t *testing.T) {
	svc, chunkRepo := newFolderSuggestionService([]string{"doc-folder"})
	svc.tagRepo = &folderSuggestionTagRepo{tags: []*types.KnowledgeTag{{
		ID: "tag-1", TenantID: 1, KnowledgeBaseID: "kb-folder",
	}}}
	svc.knowledgeRepo.(*folderSuggestionKnowledgeRepo).byTag["kb-folder"] = []string{"doc-folder"}
	questions, err := svc.GetKnowledgeSuggestedQuestionsWithFolders(
		folderSuggestionContext(), "agent-1", nil, []string{"doc-folder", "doc-full"}, []string{"tag-1"}, 10,
		[]types.FolderScope{{KnowledgeBaseID: "kb-folder", FolderIDs: []string{"folder-1"}}},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"Question from the folder?"}, suggestedQuestionTexts(questions))
	require.Len(t, chunkRepo.documentCalls, 1)
	assert.Equal(t, []string{"doc-folder"}, chunkRepo.documentCalls[0].knowledgeIDs)
}

func TestGetSuggestedQuestionsWithFoldersRejectsKBOutsideAgentScope(t *testing.T) {
	svc, _ := newFolderSuggestionService([]string{"doc-folder"})
	svc.repo = &folderSuggestionAgentRepo{agent: &types.CustomAgent{
		ID: "agent-1", TenantID: 1,
		Config: types.CustomAgentConfig{
			AgentMode:       types.AgentModeQuickAnswer,
			KBSelectionMode: "selected",
			KnowledgeBases:  []string{"kb-full"},
		},
	}}

	_, err := svc.GetSuggestedQuestionsWithFolders(
		folderSuggestionContext(), "agent-1", nil, nil, nil, 10,
		[]types.FolderScope{{KnowledgeBaseID: "kb-folder", FolderIDs: []string{"folder-1"}}},
	)
	require.Error(t, err)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, apperrors.ErrForbidden, appErr.Code)
}

func TestGetSuggestedQuestionsWithFoldersRejectsUnreadableSharedKB(t *testing.T) {
	svc, _ := newFolderSuggestionService([]string{"doc-folder"})
	svc.repo = &folderSuggestionAgentRepo{agent: &types.CustomAgent{
		ID: "agent-1", TenantID: 1,
		Config: types.CustomAgentConfig{
			AgentMode:       types.AgentModeQuickAnswer,
			KBSelectionMode: "selected",
			KnowledgeBases:  []string{"kb-foreign"},
		},
	}}
	svc.kbService = &folderSuggestionKBService{kbs: map[string]*types.KnowledgeBase{
		"kb-foreign": {ID: "kb-foreign", TenantID: 9, Type: types.KnowledgeBaseTypeDocument},
	}}

	_, err := svc.GetSuggestedQuestionsWithFolders(
		folderSuggestionContext(), "agent-1", nil, nil, nil, 10,
		[]types.FolderScope{{KnowledgeBaseID: "kb-foreign", FolderIDs: []string{"folder-1"}}},
	)
	require.Error(t, err)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, apperrors.ErrForbidden, appErr.Code)
}

func suggestedQuestionTexts(questions []types.SuggestedQuestion) []string {
	result := make([]string, 0, len(questions))
	for _, question := range questions {
		result = append(result, question.Question)
	}
	return result
}
