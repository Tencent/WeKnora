package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestFilterSearchResultsByFolderScopeKeepsOnlyAllowedFolders(t *testing.T) {
	results := []*types.SearchResult{
		{ID: "root", FolderID: types.DocumentFolderRootID},
		{ID: "parent", FolderID: "folder-parent"},
		{ID: "child", FolderID: "folder-child"},
		{ID: "sibling", FolderID: "folder-sibling"},
	}

	got := filterSearchResultsByFolderScope(
		results,
		[]string{"folder-parent", "folder-child"},
	)

	assert.Equal(t, []*types.SearchResult{results[1], results[2]}, got)
}

func TestFilterSearchResultsByFolderScopeLeavesUnscopedSearchUnchanged(t *testing.T) {
	results := []*types.SearchResult{{ID: "document", FolderID: "folder-a"}}

	assert.Equal(t, results, filterSearchResultsByFolderScope(results, nil))
}

func TestFilterSearchResultsByFolderScopeSupportsVirtualRoot(t *testing.T) {
	root := &types.SearchResult{ID: "root", FolderID: types.DocumentFolderRootID}
	nested := &types.SearchResult{ID: "nested", FolderID: "folder-a"}

	got := filterSearchResultsByFolderScope(
		[]*types.SearchResult{root, nested},
		[]string{types.DocumentFolderRootID},
	)

	assert.Equal(t, []*types.SearchResult{root}, got)
}
