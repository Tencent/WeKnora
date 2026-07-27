package service

import (
	"context"
	"fmt"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

type folderTargetFolderService struct {
	interfaces.KnowledgeFolderService
	folders     map[string]*types.KnowledgeFolder
	foldersByKB map[string]*types.KnowledgeFolder
	scopes      map[string]*types.FolderKnowledgeScope
	calls       map[string]int
}

func (s *folderTargetFolderService) GetFolder(_ context.Context, kbID, folderID string) (*types.KnowledgeFolder, error) {
	f := s.foldersByKB[kbID+":"+folderID]
	if f == nil {
		f = s.folders[folderID]
	}
	if f == nil || f.KnowledgeBaseID != kbID {
		return nil, fmt.Errorf("folder not found")
	}
	return f, nil
}
func (s *folderTargetFolderService) ResolveKnowledgeScope(_ context.Context, kbID string, folderIDs []string) (*types.FolderKnowledgeScope, error) {
	s.calls[kbID]++
	key := kbID + ":"
	for _, id := range folderIDs {
		key += id + ","
	}
	key += "sub"
	if x := s.scopes[key]; x != nil {
		return x, nil
	}
	return &types.FolderKnowledgeScope{}, nil
}
func newFolderTargetSessionService() (*sessionService, *folderTargetFolderService) {
	b := newTagTargetSessionService()
	b.knowledgeService.(*tagTargetKnowledgeService).knowledges = append(b.knowledgeService.(*tagTargetKnowledgeService).knowledges, &types.Knowledge{ID: "other-1", TenantID: 100, KnowledgeBaseID: "doc-kb-2"})
	b.knowledgeBaseService.(*tagTargetKnowledgeBaseService).kbs["doc-kb-2"] = &types.KnowledgeBase{ID: "doc-kb-2", TenantID: 100, Type: types.KnowledgeBaseTypeDocument}
	f := &folderTargetFolderService{folders: map[string]*types.KnowledgeFolder{"folder-a": {ID: "folder-a", TenantID: 100, KnowledgeBaseID: "doc-kb"}, "folder-b": {ID: "folder-b", TenantID: 100, KnowledgeBaseID: "doc-kb"}, "folder-c": {ID: "folder-c", TenantID: 100, KnowledgeBaseID: "doc-kb-2"}}, scopes: map[string]*types.FolderKnowledgeScope{"doc-kb:folder-a,sub": {KnowledgeIDs: []string{"doc-1", "doc-3"}}, "doc-kb:folder-a,folder-b,sub": {KnowledgeIDs: []string{"doc-1", "doc-2", "doc-3"}}, "doc-kb:folder-a,": {KnowledgeIDs: []string{"doc-1"}}, "doc-kb:,sub": {FullKnowledgeBase: true}, "doc-kb:,": {KnowledgeIDs: []string{"doc-1"}}, "doc-kb-2:folder-c,sub": {KnowledgeIDs: []string{"other-1"}}}, calls: map[string]int{}}
	b.knowledgeFolderService = f
	return b, f
}
func TestBuildSearchTargets_FolderTargetsNamedDescendantsAndExplicitIntersection(t *testing.T) {
	s, f := newFolderTargetSessionService()
	got, e := s.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb"}, []string{"doc-2", "doc-3"}, nil, []string{"folder-a"})
	require.NoError(t, e)
	require.Len(t, got, 1)
	assert.Equal(t, []string{"doc-3"}, got[0].KnowledgeIDs)
	assert.Equal(t, 1, f.calls["doc-kb"])
}
func TestBuildSearchTargets_FolderTargetsTagANDAndMultipleFoldersOR(t *testing.T) {
	s, f := newFolderTargetSessionService()
	got, e := s.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb"}, nil, []types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}}, []string{"folder-a", "folder-b"})
	require.NoError(t, e)
	require.Len(t, got, 1)
	assert.ElementsMatch(t, []string{"doc-1", "doc-3"}, got[0].KnowledgeIDs)
	assert.Equal(t, 1, f.calls["doc-kb"])
}
func TestBuildSearchTargets_FolderTargetsEmptyDoesNotSearchFullKB(t *testing.T) {
	s, f := newFolderTargetSessionService()
	f.folders["empty"] = &types.KnowledgeFolder{ID: "empty", TenantID: 100, KnowledgeBaseID: "doc-kb"}
	got, e := s.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb"}, nil, nil, []string{"empty"})
	require.NoError(t, e)
	assert.Empty(t, got)
}
func TestBuildSearchTargets_FolderTargetsRootIsFullKnowledgeBase(t *testing.T) {
	s, _ := newFolderTargetSessionService()
	full, err := s.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb"}, nil, nil, []string{types.FolderRootID})
	require.NoError(t, err)
	require.Len(t, full, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledgeBase, full[0].Type)
}

func TestBuildSearchTargets_FolderTargetsGroupedByKnowledgeBase(t *testing.T) {
	s, f := newFolderTargetSessionService()
	got, e := s.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb", "doc-kb-2"}, nil, nil, []string{"folder-a", "folder-c"})
	require.NoError(t, e)
	require.Len(t, got, 2)
	assert.Equal(t, 1, f.calls["doc-kb"])
	assert.Equal(t, 1, f.calls["doc-kb-2"])
}

func TestBuildSearchTargets_FolderTargetsFAQTagAND(t *testing.T) {
	s, f := newFolderTargetSessionService()
	s.knowledgeBaseService.(*tagTargetKnowledgeBaseService).kbs["faq-kb"] = &types.KnowledgeBase{
		ID: "faq-kb", TenantID: 100, Type: types.KnowledgeBaseTypeFAQ,
	}
	s.knowledgeService.(*tagTargetKnowledgeService).knowledges = append(
		s.knowledgeService.(*tagTargetKnowledgeService).knowledges,
		&types.Knowledge{ID: "faq-1", TenantID: 100, KnowledgeBaseID: "faq-kb"},
	)
	f.folders["faq-folder"] = &types.KnowledgeFolder{
		ID: "faq-folder", TenantID: 100, KnowledgeBaseID: "faq-kb",
	}
	f.scopes["faq-kb:faq-folder,sub"] = &types.FolderKnowledgeScope{KnowledgeIDs: []string{"faq-1"}}

	got, err := s.buildSearchTargets(tagTargetContext(), 100, []string{"faq-kb"}, nil,
		[]types.TagScope{{KnowledgeBaseID: "faq-kb", TagIDs: []string{"tag-a"}}},
		[]string{"faq-folder"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, []string{"faq-1"}, got[0].KnowledgeIDs)
	assert.Equal(t, []string{"tag-a"}, got[0].TagIDs)
}

func TestBuildSearchTargets_FolderTargetsEmptyWithTagReturnsNoTargets(t *testing.T) {
	s, f := newFolderTargetSessionService()
	f.folders["empty"] = &types.KnowledgeFolder{ID: "empty", TenantID: 100, KnowledgeBaseID: "doc-kb"}
	got, err := s.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb"}, nil,
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
		[]string{"empty"})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestBuildSearchTargets_FolderTargetsOneEmptyOneNonempty(t *testing.T) {
	s, f := newFolderTargetSessionService()
	f.folders["empty"] = &types.KnowledgeFolder{ID: "empty", TenantID: 100, KnowledgeBaseID: "doc-kb"}
	got, err := s.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb", "doc-kb-2"}, nil, nil,
		[]string{"empty", "folder-c"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "doc-kb-2", got[0].KnowledgeBaseID)
	assert.Equal(t, []string{"other-1"}, got[0].KnowledgeIDs)
}

func TestBuildSearchTargets_FolderTargetsExplicitFolderDocumentTagEmptyIntersection(t *testing.T) {
	s, _ := newFolderTargetSessionService()
	got, err := s.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb"}, []string{"doc-2"},
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
		[]string{"folder-a"})
	require.NoError(t, err)
	assert.Empty(t, got)
}

