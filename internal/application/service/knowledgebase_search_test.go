package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeKnowledgeIDs_BothEmpty(t *testing.T) {
	result := mergeKnowledgeIDs(nil, nil)
	assert.Nil(t, result, "both empty should return nil")

	result = mergeKnowledgeIDs([]string{}, []string{})
	assert.Nil(t, result, "both empty slices should return nil")
}

func TestMergeKnowledgeIDs_OnlyExisting(t *testing.T) {
	existing := []string{"a", "b", "c"}
	result := mergeKnowledgeIDs(existing, nil)
	assert.Equal(t, existing, result, "only existing should be returned as-is")

	result = mergeKnowledgeIDs(existing, []string{})
	assert.Equal(t, existing, result, "only existing should be returned as-is")
}

func TestMergeKnowledgeIDs_OnlyFolder(t *testing.T) {
	folder := []string{"x", "y", "z"}
	result := mergeKnowledgeIDs(nil, folder)
	assert.Equal(t, folder, result, "only folder IDs should be returned as-is")

	result = mergeKnowledgeIDs([]string{}, folder)
	assert.Equal(t, folder, result, "only folder IDs should be returned as-is")
}

func TestMergeKnowledgeIDs_Intersection(t *testing.T) {
	existing := []string{"a", "b", "c", "d"}
	folder := []string{"b", "d", "e", "f"}
	result := mergeKnowledgeIDs(existing, folder)
	assert.ElementsMatch(t, []string{"b", "d"}, result, "should return intersection")
}

func TestMergeKnowledgeIDs_NoOverlap(t *testing.T) {
	existing := []string{"a", "b"}
	folder := []string{"x", "y"}
	result := mergeKnowledgeIDs(existing, folder)
	assert.Empty(t, result, "no overlap should return empty")
}

func TestMergeKnowledgeIDs_Deduplicates(t *testing.T) {
	existing := []string{"a", "a", "b", "b"}
	folder := []string{"a", "b", "b", "c"}
	result := mergeKnowledgeIDs(existing, folder)
	assert.ElementsMatch(t, []string{"a", "b"}, result, "should deduplicate")
}

func TestMergeKnowledgeIDs_SingleElement(t *testing.T) {
	existing := []string{"only"}
	folder := []string{"only"}
	result := mergeKnowledgeIDs(existing, folder)
	assert.Equal(t, []string{"only"}, result, "single matching element")
}
