package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyKnowledgeTagBatch(t *testing.T) {
	req := &KnowledgeTagBatchRequest{
		KnowledgeIDs: []string{" k-1 ", "k-2"},
		TagIDs:       []string{"t-1"},
	}
	require.NoError(t, applyKnowledgeTagBatch(req))
	assert.Equal(t, map[string][]string{"k-1": {"t-1"}, "k-2": {"t-1"}}, req.Updates)

	cleared := &KnowledgeTagBatchRequest{KnowledgeIDs: []string{"k-1"}, TagIDs: []string{}}
	require.NoError(t, applyKnowledgeTagBatch(cleared))
	assert.Empty(t, cleared.Updates["k-1"])

	kept := &KnowledgeTagBatchRequest{Updates: map[string][]string{"k-1": {"t-1"}}}
	require.NoError(t, applyKnowledgeTagBatch(kept))
	assert.Equal(t, map[string][]string{"k-1": {"t-1"}}, kept.Updates)

	cases := []*KnowledgeTagBatchRequest{
		{},
		{KnowledgeIDs: []string{"k-1"}, Updates: map[string][]string{"k-2": nil}},
		{KnowledgeIDs: []string{"k-1"}},
		{KnowledgeIDs: []string{"k-1", "k-1"}, TagIDs: []string{}},
		{KnowledgeIDs: []string{" "}, TagIDs: []string{}},
	}
	for _, bad := range cases {
		require.Error(t, applyKnowledgeTagBatch(bad))
	}
}
