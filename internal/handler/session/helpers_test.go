package session

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestTagScopesFromMentionedItems(t *testing.T) {
	scopes := tagScopesFromMentionedItems([]MentionedItemRequest{
		{Type: "tag", ID: "tag-1", KBID: "kb-1"},
		{Type: "tag", ID: "tag-2", KBID: "kb-1"},
		{Type: "tag", ID: "tag-3", KBID: "kb-2"},
		{Type: "tag", ID: "orphan", KBID: ""},
	})
	assert.Len(t, scopes, 2)
	byKB := make(map[string][]string)
	for _, scope := range scopes {
		byKB[scope.KnowledgeBaseID] = scope.TagIDs
	}
	assert.ElementsMatch(t, []string{"tag-1", "tag-2"}, byKB["kb-1"])
	assert.Equal(t, []string{"tag-3"}, byKB["kb-2"])
}

func TestMergeTagScopesFromRequestIDs_SingleKB(t *testing.T) {
	scopes := mergeTagScopesFromRequestIDs(
		[]types.TagScope{{KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-1"}}},
		[]string{"tag-2"},
		[]string{"kb-1"},
	)
	assert.Len(t, scopes, 1)
	assert.ElementsMatch(t, []string{"tag-1", "tag-2"}, scopes[0].TagIDs)
}

func TestMergeTagScopesFromRequestIDs_OrphanWithSingleKB(t *testing.T) {
	scopes := mergeTagScopesFromRequestIDs(nil, []string{"tag-9"}, []string{"kb-1"})
	assert.Len(t, scopes, 1)
	assert.Equal(t, "kb-1", scopes[0].KnowledgeBaseID)
	assert.Equal(t, []string{"tag-9"}, scopes[0].TagIDs)
}

func TestMergeTagScopesFromRequestIDs_AmbiguousKBIgnored(t *testing.T) {
	scopes := mergeTagScopesFromRequestIDs(nil, []string{"tag-9"}, []string{"kb-1", "kb-2"})
	assert.Empty(t, scopes)
}

func TestValidateUnscopedTagIDs(t *testing.T) {
	assert.NoError(t, validateUnscopedTagIDs(nil, nil))
	assert.NoError(t, validateUnscopedTagIDs(nil, []string{"kb-1", "kb-2"}))
	assert.NoError(t, validateUnscopedTagIDs([]string{"tag-9"}, []string{"kb-1"}))
	assert.Error(t, validateUnscopedTagIDs([]string{"tag-9"}, []string{"kb-1", "kb-2"}))
	assert.Error(t, validateUnscopedTagIDs([]string{"tag-9"}, nil))
}

// TestFolderScopesFromMentionedItems: an @folder carries its own kb_id, which
// is what lets it scope retrieval without the user also selecting that KB.
func TestFolderScopesFromMentionedItems(t *testing.T) {
	scopes := folderScopesFromMentionedItems([]MentionedItemRequest{
		{Type: "folder", ID: "f-1", KBID: "kb-1"},
		{Type: "folder", ID: "f-2", KBID: "kb-1"},
		{Type: "folder", ID: "f-1", KBID: "kb-1"}, // duplicate
		{Type: "folder", ID: "f-3", KBID: "kb-2"},
		{Type: "folder", ID: "orphan", KBID: ""}, // unattributable, dropped
		{Type: "tag", ID: "tag-1", KBID: "kb-1"}, // other types ignored
	})
	assert.Len(t, scopes, 2)
	byKB := make(map[string][]string)
	for _, scope := range scopes {
		byKB[scope.KnowledgeBaseID] = scope.FolderIDs
	}
	assert.Equal(t, []string{"f-1", "f-2"}, byKB["kb-1"])
	assert.Equal(t, []string{"f-3"}, byKB["kb-2"])
}

// TestMergeFolderScopesFromRequestIDs_MentionScopesNeedNoKB: with every folder
// attributed by its mention, no single-KB requirement applies — two folders in
// two different KBs both scope their own KB.
func TestMergeFolderScopesFromRequestIDs_MentionScopesNeedNoKB(t *testing.T) {
	mention := folderScopesFromMentionedItems([]MentionedItemRequest{
		{Type: "folder", ID: "f-1", KBID: "kb-1"},
		{Type: "folder", ID: "f-3", KBID: "kb-2"},
	})
	scopes, err := mergeFolderScopesFromRequestIDs(mention,
		[]string{"f-1", "f-3"}, []string{"kb-1", "kb-2"})
	assert.NoError(t, err)
	assert.Len(t, scopes, 2)
}

func TestMergeFolderScopesFromRequestIDs_BareIDsWithSingleKB(t *testing.T) {
	scopes, err := mergeFolderScopesFromRequestIDs(nil, []string{"f-1", "f-1", "f-2"}, []string{"kb-1"})
	assert.NoError(t, err)
	assert.Len(t, scopes, 1)
	assert.Equal(t, "kb-1", scopes[0].KnowledgeBaseID)
	assert.Equal(t, []string{"f-1", "f-2"}, scopes[0].FolderIDs)
}

// A bare id for a KB that already has a mention scope merges into it rather
// than creating a second scope for the same KB.
func TestMergeFolderScopesFromRequestIDs_BareIDMergesIntoExistingScope(t *testing.T) {
	scopes, err := mergeFolderScopesFromRequestIDs(
		[]types.FolderScope{{KnowledgeBaseID: "kb-1", FolderIDs: []string{"f-1"}}},
		[]string{"f-1", "f-2"},
		[]string{"kb-1"},
	)
	assert.NoError(t, err)
	assert.Len(t, scopes, 1)
	assert.Equal(t, []string{"f-1", "f-2"}, scopes[0].FolderIDs)
}

func TestMergeFolderScopesFromRequestIDs_Empty(t *testing.T) {
	scopes, err := mergeFolderScopesFromRequestIDs(nil, nil, nil)
	assert.NoError(t, err)
	assert.Empty(t, scopes)
}

// An unattributable bare id is rejected rather than silently widened to a
// full-KB search.
func TestMergeFolderScopesFromRequestIDs_AmbiguousKBRejected(t *testing.T) {
	_, err := mergeFolderScopesFromRequestIDs(nil, []string{"f-1"}, []string{"kb-1", "kb-2"})
	assert.Error(t, err)
	_, err = mergeFolderScopesFromRequestIDs(nil, []string{"f-1"}, nil)
	assert.Error(t, err)
}

// TestMergeKnowledgeTargets_ScopedMentionsPullInTheirKB: an @tag or @folder
// mention names a scope inside a knowledge base, so that KB must join the
// retrieval scope — otherwise the scope resolves against a KB nobody is
// searching and recalls nothing. Only the KB is added; the tag/folder id itself
// is not a document id.
func TestMergeKnowledgeTargets_ScopedMentionsPullInTheirKB(t *testing.T) {
	kbIDs, knowledgeIDs := mergeKnowledgeTargets(nil, nil, []MentionedItemRequest{
		{Type: "folder", ID: "f-1", KBID: "kb-folder"},
		{Type: "tag", ID: "tag-1", KBID: "kb-tag"},
		{Type: "kb", ID: "kb-explicit"},
		{Type: "file", ID: "doc-1"},
		{Type: "folder", ID: "f-orphan", KBID: ""}, // unattributable, ignored
	})
	assert.ElementsMatch(t, []string{"kb-folder", "kb-tag", "kb-explicit"}, kbIDs)
	assert.Equal(t, []string{"doc-1"}, knowledgeIDs)
}

// A scoped mention whose KB the user also selected must not duplicate it.
func TestMergeKnowledgeTargets_ScopedMentionDoesNotDuplicateKB(t *testing.T) {
	kbIDs, _ := mergeKnowledgeTargets([]string{"kb-1"}, nil, []MentionedItemRequest{
		{Type: "folder", ID: "f-1", KBID: "kb-1"},
	})
	assert.Equal(t, []string{"kb-1"}, kbIDs)
}

// TestKBIDsFromScopes: scopes narrow a knowledge base rather than standing
// alone, so callers pull the scoped KBs into the retrieval scope.
func TestKBIDsFromScopes(t *testing.T) {
	assert.Equal(t, []string{"kb-1"}, kbIDsFromTagScopes([]types.TagScope{
		{KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-1"}},
		{KnowledgeBaseID: "kb-empty"}, // no tags: contributes nothing
	}))
	assert.Equal(t, []string{"kb-2"}, kbIDsFromFolderScopes([]types.FolderScope{
		{KnowledgeBaseID: "kb-2", FolderIDs: []string{"f-1"}},
		{KnowledgeBaseID: "kb-empty"},
	}))
}
