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

func TestFolderScopesFromRequestIDs_SingleKB(t *testing.T) {
	scopes, err := folderScopesFromRequestIDs([]string{"f-1", "f-1", "f-2"}, []string{"kb-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scopes) != 1 || scopes[0].KnowledgeBaseID != "kb-1" || len(scopes[0].FolderIDs) != 2 {
		t.Fatalf("unexpected scopes: %+v", scopes)
	}
}

func TestFolderScopesFromRequestIDs_Empty(t *testing.T) {
	scopes, err := folderScopesFromRequestIDs(nil, nil)
	if err != nil || scopes != nil {
		t.Fatalf("empty folder ids must be a no-op, got scopes=%+v err=%v", scopes, err)
	}
}

func TestFolderScopesFromRequestIDs_AmbiguousKBRejected(t *testing.T) {
	if _, err := folderScopesFromRequestIDs([]string{"f-1"}, []string{"kb-1", "kb-2"}); err == nil {
		t.Fatal("folder ids with multiple KBs must be rejected")
	}
	if _, err := folderScopesFromRequestIDs([]string{"f-1"}, nil); err == nil {
		t.Fatal("folder ids with no KB must be rejected")
	}
}
