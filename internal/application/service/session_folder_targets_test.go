package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// folderTargetFolderService fakes only the scope-resolution method the
// search-target builder uses; folderKnowledge maps folderID -> knowledge IDs.
type folderTargetFolderService struct {
	interfaces.KnowledgeFolderService
	folderKnowledge map[string][]string
}

func (s *folderTargetFolderService) ListKnowledgeIDsByFolderIDs(
	_ context.Context, _ uint64, _ string, folderIDs []string, _ bool,
) ([]string, error) {
	out := make([]string, 0)
	for _, id := range folderIDs {
		out = append(out, s.folderKnowledge[id]...)
	}
	return out, nil
}

func newFolderTargetSessionService() *sessionService {
	svc := newTagTargetSessionService()
	svc.folderService = &folderTargetFolderService{
		folderKnowledge: map[string][]string{
			"folder-a": {"doc-1", "doc-2"},
			"folder-b": {"doc-2"},
			// "folder-empty" intentionally absent: resolves to nothing.
		},
	}
	return svc
}

// TestBuildSearchTargets_FolderScopeResolvesKnowledgeIDs: a folder scope
// narrows the KB to the subtree's documents and suppresses the full-KB target.
func TestBuildSearchTargets_FolderScopeResolvesKnowledgeIDs(t *testing.T) {
	svc := newFolderTargetSessionService()

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		nil,
		nil,
		[]types.FolderScope{{KnowledgeBaseID: "doc-kb", FolderIDs: []string{"folder-a"}}},
	)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledge, targets[0].Type)
	assert.ElementsMatch(t, []string{"doc-1", "doc-2"}, targets[0].KnowledgeIDs)
	assert.Equal(t, []string{"folder-a"}, targets[0].ScopeFolderIDs)
	assert.True(t, targets[0].DisableRecallThresholds)
}

// TestBuildSearchTargets_EmptyFolderScopeYieldsNoTarget locks the no-leak
// contract: a folder scope that resolves to zero documents must drop the KB
// from retrieval entirely — it must never degrade into a full-KB search.
func TestBuildSearchTargets_EmptyFolderScopeYieldsNoTarget(t *testing.T) {
	svc := newFolderTargetSessionService()

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		nil,
		nil,
		[]types.FolderScope{{KnowledgeBaseID: "doc-kb", FolderIDs: []string{"folder-empty"}}},
	)
	require.NoError(t, err)
	assert.Empty(t, targets,
		"an empty folder must produce no search target, not an unfiltered KB search")
}

// TestBuildSearchTargets_FolderScopeIntersectsExplicitKnowledgeIDs: explicit
// file selection combined with a folder narrows to the intersection.
func TestBuildSearchTargets_FolderScopeIntersectsExplicitKnowledgeIDs(t *testing.T) {
	svc := newFolderTargetSessionService()

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		nil,
		[]string{"doc-2", "doc-3"},
		nil,
		[]types.FolderScope{{KnowledgeBaseID: "doc-kb", FolderIDs: []string{"folder-a"}}},
	)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.ElementsMatch(t, []string{"doc-2"}, targets[0].KnowledgeIDs)
}

// TestBuildSearchTargets_FolderAndTagScopesIntersect: tag + folder on the
// same KB means "documents in the folder AND carrying the tag".
func TestBuildSearchTargets_FolderAndTagScopesIntersect(t *testing.T) {
	svc := newFolderTargetSessionService()

	// tag-a covers doc-1/doc-3; folder-b covers doc-2 → empty intersection.
	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		nil,
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
		[]types.FolderScope{{KnowledgeBaseID: "doc-kb", FolderIDs: []string{"folder-b"}}},
	)
	require.NoError(t, err)
	assert.Empty(t, targets)

	// tag-b covers doc-2/doc-3; folder-a covers doc-1/doc-2 → doc-2.
	targets, err = svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		nil,
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-b"}}},
		[]types.FolderScope{{KnowledgeBaseID: "doc-kb", FolderIDs: []string{"folder-a"}}},
	)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.ElementsMatch(t, []string{"doc-2"}, targets[0].KnowledgeIDs)
	assert.Equal(t, []string{"folder-a"}, targets[0].ScopeFolderIDs)
}
