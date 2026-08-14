package session

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestSearchResultFromMap_RoundTrip simulates the Redis round trip: a
// *types.SearchResult is serialized to JSON, deserialized into a generic map,
// and rebuilt via searchResultFromMap. All fields, including metadata, must
// survive.
func TestSearchResultFromMap_RoundTrip(t *testing.T) {
	original := &types.SearchResult{
		ID:                   "chunk-1",
		Content:              "first part\nsecond part",
		KnowledgeID:          "knowledge-1",
		ChunkIndex:           3,
		KnowledgeTitle:       "title",
		StartAt:              10,
		EndAt:                20,
		Seq:                  2,
		Score:                4.5,
		ChunkType:            "text",
		ParentChunkID:        "parent-1",
		ImageInfo:            `[{"url":"cdn.example.com"}]`,
		KnowledgeFilename:    "doc.txt",
		KnowledgeSource:      "upload",
		KnowledgeDescription: "desc",
		KnowledgeBaseID:      "kb-1",
		Metadata:             map[string]string{"page": "3"},
	}

	raw, err := json.Marshal(original)
	require.NoError(t, err)
	var refMap map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &refMap))

	got := searchResultFromMap(refMap)

	assert.Equal(t, original.ID, got.ID)
	assert.Equal(t, original.Content, got.Content)
	assert.Equal(t, original.KnowledgeID, got.KnowledgeID)
	assert.Equal(t, original.ChunkIndex, got.ChunkIndex)
	assert.Equal(t, original.KnowledgeTitle, got.KnowledgeTitle)
	assert.Equal(t, original.StartAt, got.StartAt)
	assert.Equal(t, original.EndAt, got.EndAt)
	assert.Equal(t, original.Seq, got.Seq)
	assert.Equal(t, original.Score, got.Score)
	assert.Equal(t, original.ChunkType, got.ChunkType)
	assert.Equal(t, original.ParentChunkID, got.ParentChunkID)
	assert.Equal(t, original.ImageInfo, got.ImageInfo)
	assert.Equal(t, original.KnowledgeFilename, got.KnowledgeFilename)
	assert.Equal(t, original.KnowledgeSource, got.KnowledgeSource)
	assert.Equal(t, original.KnowledgeDescription, got.KnowledgeDescription)
	assert.Equal(t, original.KnowledgeBaseID, got.KnowledgeBaseID)
	assert.Equal(t, original.Metadata, got.Metadata)
}
